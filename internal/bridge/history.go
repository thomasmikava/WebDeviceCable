package bridge

import (
	"errors"
	"time"
)

// Both admitted work and admission failures share the ordinary history bound.
func makeJobRoom(s *State) error {
	if len(s.Jobs) < maxJobs {
		return nil
	}
	oldest := -1
	for i, previous := range s.Jobs {
		if terminal(previous.Status) && !previous.OmitHistory && (oldest < 0 || previous.CreatedAt.Before(s.Jobs[oldest].CreatedAt)) {
			oldest = i
		}
	}
	if oldest < 0 {
		return fail(429, "job_limit", "Too many active requests. Wait for a request to finish.")
	}
	s.Jobs = append(s.Jobs[:oldest], s.Jobs[oldest+1:]...)
	return nil
}

// Called under the app lock after authenticated admission fails. This is only
// owner history: no execution, request payload, lease or replay receipt is created.
// Persistence/capacity failure must never replace the original app-facing error.
func (a *App) recordRoutingFailure(p *Pairing, err error) {
	var refusal *apiError
	if !errors.As(err, &refusal) || refusal.Code != "routing_required" || !keepRequestHistory(&a.state, p) {
		return
	}
	now := a.now()
	last := a.routingFailureAt[p.ID]
	// Recover throttling from retained history after restart. The in-memory
	// timestamp also prevents a history clear/eviction from bypassing the limit.
	for _, j := range a.state.Jobs {
		if j.PairingID == p.ID && j.Code == "routing_required" && j.CreatedAt.After(last) {
			last = j.CreatedAt
		}
	}
	if !last.IsZero() && now.Sub(last) < time.Minute {
		if a.routingFailureAt == nil {
			a.routingFailureAt = map[string]time.Time{}
		}
		a.routingFailureAt[p.ID] = last
		return
	}
	j := &Job{ID: secret(), PairingID: p.ID, Request: JobRequest{Kind: "action"}, Billing: "none", Revision: 1, CreatedAt: now}
	finish(j, "failed", refusal.Code, refusal.Message, now)
	if a.update(func(s *State) error {
		if err := makeJobRoom(s); err != nil {
			return err
		}
		s.Jobs = append(s.Jobs, j)
		return nil
	}) == nil {
		if a.routingFailureAt == nil {
			a.routingFailureAt = map[string]time.Time{}
		}
		a.routingFailureAt[p.ID] = now
	}
}
