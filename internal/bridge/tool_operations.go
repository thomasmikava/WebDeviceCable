package bridge

import (
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Descriptive application metadata; never an authorization or routing input.
type ToolOperation struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Version string `json:"version,omitempty"`
}
type ToolOperationRecord struct {
	ToolOperation
	CallID     string    `json:"callId"`
	ToolID     string    `json:"toolId"`
	Status     string    `json:"status"`
	ReportedAt time.Time `json:"reportedAt"`
}

func validOperationText(value string, limit int, required bool) bool {
	return len(value) <= limit && (!required || strings.TrimSpace(value) != "") && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (a *App) reportToolOperation(w http.ResponseWriter, r *http.Request, p *Pairing) error {
	op := a.permissions[r.PathValue("id")]
	lease, err := a.liveLease(r, p)
	if err != nil {
		return err
	}
	if op == nil || op.PairingID != p.ID || op.LeaseID != lease || op.Kind != "app-tool" || !a.operationLive(op) {
		return fail(404, "not_found", "Tool call not found.")
	}
	var in ToolOperation
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if !validOperationText(in.ID, 128, true) || !validOperationText(in.Title, 200, true) || !validOperationText(in.Version, 128, false) {
		return fail(400, "invalid_operation", "Supply an operation ID, title and optional version within their limits.")
	}
	if op.Operation != nil {
		if *op.Operation != in {
			return fail(409, "operation_conflict", "This call already describes a different operation.")
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}
	if op.Status != "dispatched" {
		return fail(409, "invalid_state", "Claim the tool call before reporting its operation.")
	}
	if err := a.update(func(s *State) error {
		j := findJob(s, op.JobID)
		if j == nil || j.Status != "running" {
			return fail(410, "execution_ended", "This execution has ended.")
		}
		if len(j.ToolOperations) >= 128 {
			return fail(429, "operation_limit", "This execution reached its operation-detail limit.")
		}
		j.ToolOperations = append(j.ToolOperations, ToolOperationRecord{ToolOperation: in, CallID: op.ID, ToolID: op.Name, Status: "running", ReportedAt: a.now()})
		j.Revision++
		return nil
	}); err != nil {
		return err
	}
	op.Operation = &in
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func completeToolOperation(j *Job, callID, status string) {
	for i := range j.ToolOperations {
		if j.ToolOperations[i].CallID == callID {
			j.ToolOperations[i].Status = status
			j.Revision++
			return
		}
	}
}
