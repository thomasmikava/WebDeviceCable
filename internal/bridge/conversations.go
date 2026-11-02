package bridge

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

type Conversation struct {
	Mode      string         `json:"mode,omitempty"`
	ID        string         `json:"id"`
	PairingID string         `json:"pairingId,omitempty"`
	LeaseID   string         `json:"leaseId,omitempty"`
	Group     string         `json:"group,omitempty"`
	Plan      *ExecutionPlan `json:"plan,omitempty"`
	Status    string         `json:"status"`
	Code      string         `json:"code,omitempty"`
	LastJobID string         `json:"lastJobId,omitempty"`
	Turns     int            `json:"turns"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

func remoteConversation(c *Conversation) Conversation {
	copy := *c
	copy.Plan = nil
	copy.LeaseID = ""
	copy.PairingID = ""
	return copy
}
func (a *App) liveLease(r *http.Request, p *Pairing) (string, error) {
	if !a.state.Enabled || p.Paused {
		return "", fail(409, "paused", "Runner or application is paused.")
	}
	id := r.Header.Get("X-Bridge-Lease")
	lease, ok := a.leases[id]
	if !ok || lease.PairingID != p.ID || !a.now().Before(lease.ExpiresAt) {
		return "", fail(410, "lease_expired", "An active requesting-tab lease is required.")
	}
	return id, nil
}
func (a *App) usableConversation(p *Pairing, id, lease string) (*Conversation, error) {
	c := a.state.Conversations[id]
	if c == nil || c.PairingID != p.ID {
		return nil, fail(404, "not_found", "Session not found.")
	}
	if c.LeaseID != lease {
		return nil, fail(409, "other_tab", "This session belongs to another requesting tab.")
	}
	if c.Status != "open" {
		return nil, fail(410, "session_closed", "This session has ended. Start a new session explicitly.")
	}
	if runtime := a.conversations[id]; runtime != nil {
		if ended, _ := runtime.status(); ended {
			return nil, fail(410, "session_closed", "The session has ended. Start a new session explicitly.")
		}
	}
	plan, e := a.resolvePlan(p, JobRequest{Kind: "action", Group: c.Group, Provider: c.Plan.Provider, Action: c.Plan.Action})
	if e != nil {
		return nil, e
	}
	if planHash(plan) != planHash(c.Plan) {
		return nil, fail(409, "route_changed", "The selected route changed. Start a new session explicitly.")
	}
	if c.LastJobID != "" {
		if previous := findJob(&a.state, c.LastJobID); previous != nil && !terminal(previous.Status) {
			return nil, fail(409, "session_busy", "Wait for the current message to finish before sending another.")
		}
	}
	return c, nil
}
func (a *App) conversationRoutes(mux *http.ServeMux, paired func(func(http.ResponseWriter, *http.Request, *Pairing) error) http.HandlerFunc, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("POST /v2/conversations", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		var in struct {
			Mode     string `json:"mode"`
			Group    string `json:"group"`
			Provider string `json:"provider"`
			Action   string `json:"action"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Mode == "" {
			in.Mode = "restricted"
		}
		if in.Mode != "restricted" && in.Mode != "app-tools" && in.Mode != "workspace" {
			return fail(400, "invalid_mode", "Unsupported conversation mode.")
		}
		plan, err := a.resolvePlan(p, JobRequest{Kind: "action", Group: in.Group, Provider: in.Provider, Action: in.Action})
		if err != nil {
			return err
		}
		if plan.Kind != "command" {
			if err := a.nativeAvailable(p, plan.Provider, plan.Kind); err != nil {
				return err
			}
		}
		if program, ok := a.program(plan.Provider); ok && program.Type == "custom" {
			if err := a.checkProgramRequest(p, plan, JobRequest{Mode: in.Mode, ConversationID: "checking"}); err != nil {
				return err
			}
		}
		if plan.Kind == "command" && a.actions[plan.Action].Session == nil {
			return fail(400, "sessions_unsupported", "This command does not support continuing sessions. Enable its JSONL session interface in configuration.")
		}
		c := &Conversation{Mode: in.Mode, ID: secret(), PairingID: p.ID, LeaseID: lease, Group: in.Group, Plan: plan, Status: "open", CreatedAt: a.now(), UpdatedAt: a.now()}
		if err := a.update(func(s *State) error {
			if len(s.Conversations) >= 128 {
				closed := []*Conversation{}
				for _, item := range s.Conversations {
					if item.Status != "open" {
						closed = append(closed, item)
					}
				}
				sort.Slice(closed, func(i, j int) bool { return closed[i].UpdatedAt.Before(closed[j].UpdatedAt) })
				for _, item := range closed {
					if len(s.Conversations) < 128 {
						break
					}
					delete(s.Conversations, item.ID)
				}
			}
			if len(s.Conversations) >= 128 {
				return fail(429, "session_limit", "End unused sessions before starting another.")
			}
			s.Conversations[c.ID] = c
			return nil
		}); err != nil {
			return err
		}
		writeJSON(w, 201, remoteConversation(c))
		return nil
	}))
	mux.HandleFunc("POST /v2/conversations/{id}/turns", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		var in struct {
			IdempotencyKey string            `json:"idempotencyKey"`
			Prompt         string            `json:"prompt"`
			Arguments      map[string]string `json:"arguments"`
			TimeoutSeconds int               `json:"timeoutSeconds"`
			OutputSchema   json.RawMessage   `json:"outputSchema,omitempty"`
			Inputs         []string          `json:"inputs,omitempty"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		c := a.state.Conversations[r.PathValue("id")]
		if c == nil || c.PairingID != p.ID {
			return fail(404, "not_found", "Session not found.")
		}
		request := JobRequest{Kind: "action", Group: c.Group, ConversationID: c.ID, Prompt: in.Prompt, Arguments: in.Arguments, IdempotencyKey: in.IdempotencyKey, TimeoutSeconds: in.TimeoutSeconds, OutputSchema: in.OutputSchema, Inputs: in.Inputs}
		job, duplicate, err := a.createJob(p, request, lease)
		if err != nil {
			return err
		}
		status := 202
		if duplicate {
			status = 200
		}
		writeJSON(w, status, a.remoteJob(job))
		return nil
	}))
	mux.HandleFunc("GET /v2/conversations/{id}", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		c := a.state.Conversations[r.PathValue("id")]
		if c == nil || c.PairingID != p.ID {
			return fail(404, "not_found", "Session not found.")
		}
		writeJSON(w, 200, remoteConversation(c))
		return nil
	}))
	mux.HandleFunc("DELETE /v2/conversations/{id}", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		return a.endConversation(w, r.PathValue("id"), p.ID, lease)
	}))
	mux.HandleFunc("DELETE /api/conversations/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		return a.endConversation(w, r.PathValue("id"), "", "")
	}))
}
func (a *App) endConversation(w http.ResponseWriter, id, pairing, lease string) error {
	c := a.state.Conversations[id]
	if c == nil || pairing != "" && c.PairingID != pairing {
		return fail(404, "not_found", "Session not found.")
	}
	if lease != "" && c.LeaseID != lease {
		return fail(409, "other_tab", "This session belongs to another requesting tab.")
	}
	if err := a.update(func(s *State) error {
		conversation := s.Conversations[id]
		conversation.Status = "closed"
		conversation.Code = "session_closed"
		conversation.UpdatedAt = a.now()
		for _, job := range s.Jobs {
			if job.Request.ConversationID == id && !terminal(job.Status) {
				finish(job, "cancelled", "session_closed", "The session was ended.", a.now())
			}
		}
		return nil
	}); err != nil {
		return err
	}
	writeJSON(w, 200, remoteConversation(a.state.Conversations[id]))
	return nil
}

func (a *App) reconcileConversations(s *State, now time.Time) {
	for id, c := range s.Conversations {
		if c.Status != "open" {
			continue
		}
		code := ""
		p := s.Pairings[c.PairingID]
		lease, ok := a.leases[c.LeaseID]
		switch {
		case !ok || lease.PairingID != c.PairingID || !now.Before(lease.ExpiresAt):
			code = "tab_disconnected"
		case p == nil || p.Revoked || p.Paused:
			code = "app_access_changed"
		case !s.Enabled:
			code = "runner_paused"
		default:
			if c.Plan.Kind == "command" {
				if err := a.authorizeAction(p, c.Plan.Action, c.Plan.ActionHash); err != nil {
					code = "command_access_changed"
				}
			}
		}
		if code == "" {
			if runtime := a.conversations[id]; runtime != nil {
				if ended, reason := runtime.status(); ended {
					last := findJob(s, c.LastJobID)
					if last == nil || terminal(last.Status) {
						code = reason
					}
				}
			}
		}
		if job := findJob(s, c.LastJobID); job != nil && terminal(job.Status) {
			if job.FinishedAt != nil && c.UpdatedAt.Before(*job.FinishedAt) {
				c.UpdatedAt = *job.FinishedAt
			}
			if job.Status != "completed" {
				code = job.Code
			}
		}
		if code != "" {
			c.Status = "closed"
			c.Code = code
			c.UpdatedAt = now
		}
	}
}
