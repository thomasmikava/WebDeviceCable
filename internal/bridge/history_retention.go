package bridge

import (
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// No new acknowledgement is added to the app protocol. A successful signed
// response collects the result; file results remain until every byte is sent.
// Failed writes can retry. The deadline never extends, even on repeated reads.
const resultDeliveryTTL = time.Minute

type resultDelivery struct {
	ephemeral bool
	result    bool
	artifacts map[string][]byte
	offsets   map[string]int
}

func globalRequestHistory(s *State) bool { return s.KeepRequestHistory == nil || *s.KeepRequestHistory }
func keepRequestHistory(s *State, p *Pairing) bool {
	if p != nil {
		switch p.RequestHistory {
		case "keep":
			return true
		case "discard":
			return false
		}
	}
	return globalRequestHistory(s)
}
func validRequestHistory(value string) bool {
	return value == "" || value == "global" || value == "keep" || value == "discard"
}
func discardFinished(s *State, matches func(*Job) bool) {
	jobs := s.Jobs[:0]
	for _, j := range s.Jobs {
		if !terminal(j.Status) || !matches(j) {
			jobs = append(jobs, j)
		}
	}
	clear(s.Jobs[len(jobs):])
	s.Jobs = jobs
}

// A setting change erases durable history immediately without racing an app
// still collecting a just-finished result. Older/already-collected records need
// no delivery copy. Refusal entries have no app-side job to collect at all.
func (a *App) discardStoredHistory(s *State, matches func(*Job) bool) {
	discardFinished(s, func(j *Job) bool {
		if j.OmitHistory || !matches(j) {
			return false
		}
		if j.Code == "routing_required" && j.Request.IdempotencyKey == "" || j.FinishedAt == nil || !a.now().Before(j.FinishedAt.Add(resultDeliveryTTL)) || deliveryComplete(j, a.deliveries[j.ID]) {
			return true
		}
		j.OmitHistory = true
		return false
	})
}
func deliveryComplete(j *Job, d *resultDelivery) bool {
	if d == nil || !d.result {
		return false
	}
	for _, file := range j.Artifacts {
		if sent, ok := d.offsets[file.ID]; !ok || sent < file.Size {
			return false
		}
	}
	return true
}
func newResultDelivery() *resultDelivery {
	return &resultDelivery{artifacts: map[string][]byte{}, offsets: map[string]int{}}
}
func cleanHistoryConversations(s *State) {
	for _, c := range s.Conversations {
		if !keepRequestHistory(s, s.Pairings[c.PairingID]) {
			c.OmitHistory = true
		}
		if j := findJob(s, c.LastJobID); j != nil && terminal(j.Status) && j.OmitHistory {
			c.LastJobID = ""
			if j.Status != "completed" {
				c.Status = "closed"
				c.Code = j.Code
			}
			if j.FinishedAt != nil {
				c.UpdatedAt = *j.FinishedAt
			}
		}
		if c.OmitHistory && findJob(s, c.LastJobID) == nil {
			c.LastJobID = ""
		}
		// Keep only the live lease's closure notice for heartbeat/explicit close.
		// It is neither persisted nor included in the panel's conversation list.
		if c.Status != "open" && c.OmitHistory {
			c.Plan = nil
			c.Tools = nil
			c.ToolSetHash = ""
			c.LastJobID = ""
			c.Group = ""
			c.Mode = ""
			c.Turns = 0
			c.UsesFiles = false
		}
	}
}
func (a *App) prepareHistory(s *State) {
	for _, j := range s.Jobs {
		if !keepRequestHistory(s, s.Pairings[j.PairingID]) {
			j.OmitHistory = true
		}
	}
	cleanHistoryConversations(s)
	discardFinished(s, func(j *Job) bool {
		return j.OmitHistory && (j.FinishedAt == nil || !a.now().Before(j.FinishedAt.Add(resultDeliveryTTL)))
	})
}

// Transfer ephemeral artifacts into memory before removing their disk copies.
// Called only after the durable state no longer contains the finished record.
func (a *App) prepareDeliveries(s *State) error {
	if a.deliveries == nil {
		a.deliveries = map[string]*resultDelivery{}
	}
	for _, j := range s.Jobs {
		if !terminal(j.Status) || !j.OmitHistory {
			continue
		}
		d := a.deliveries[j.ID]
		if d == nil {
			d = newResultDelivery()
			a.deliveries[j.ID] = d
		}
		if !d.ephemeral {
			d.ephemeral = true
			for _, artifact := range j.Artifacts {
				data, err := ReadLocalFile(a.artifactPath(j.ID, artifact.ID), maxNativeFile)
				if err == nil && len(data) == artifact.Size && fileHash(data) == artifact.Hash {
					d.artifacts[artifact.ID] = data
				}
			}
		}
	}
	return a.pruneArtifacts(s)
}
func (a *App) pruneArtifacts(s *State) error {
	entries, err := os.ReadDir(filepath.Join(a.dir, "artifacts"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		j := findJob(s, entry.Name())
		if j == nil || terminal(j.Status) && j.OmitHistory {
			if err := os.RemoveAll(filepath.Join(a.dir, "artifacts", entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Register only on the authenticated transport, never on panel reads or owner
// cancellation. secureCall runs these callbacks after signing/encryption/write.
func (a *App) collectResult(w http.ResponseWriter, j *Job) {
	if j == nil || !terminal(j.Status) {
		return
	}
	if sw, ok := w.(*secureWriter); ok {
		id := j.ID
		sw.delivered = append(sw.delivered, func() { a.collected(id, "", 0, 0) })
	}
}
func (a *App) collectArtifact(w http.ResponseWriter, j *Job, artifact string, offset, length int) {
	if sw, ok := w.(*secureWriter); ok {
		id := j.ID
		sw.delivered = append(sw.delivered, func() { a.collected(id, artifact, offset, length) })
	}
}

// Called with mu held. Removing a delivery-only record needs no disk write: it
// was excluded from durable state when it finished. Preserve session runtimes.
func (a *App) collected(id, artifact string, offset, length int) {
	j := findJob(&a.state, id)
	d := a.deliveries[id]
	if j == nil {
		return
	}
	if d == nil {
		if a.deliveries == nil {
			a.deliveries = map[string]*resultDelivery{}
		}
		d = newResultDelivery()
		a.deliveries[id] = d
	}
	if artifact == "" {
		d.result = true
	} else if offset <= d.offsets[artifact] {
		d.offsets[artifact] = max(d.offsets[artifact], offset+length)
	}
	if !j.OmitHistory || !deliveryComplete(j, d) {
		return
	}
	discardFinished(&a.state, func(j *Job) bool { return j.ID == id })
	delete(a.deliveries, id)
	// Other response handlers can have a snapshot, but the app state no longer
	// owns any output. A later retry sees its durable history_deleted receipt.
}

// Delivery-only files still count toward the existing 64 MiB file budget.
// Moving files off disk must not turn the opt-out into unbounded RAM storage.
func (a *App) deliveryArtifactBytes() int64 {
	var total int64
	for _, delivery := range a.deliveries {
		for _, data := range delivery.artifacts {
			total += int64(len(data))
		}
	}
	return total
}
