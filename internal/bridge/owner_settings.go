package bridge

import (
	"net/http"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/desktop"
)

const approvalWaitLimit = 10 * time.Minute
const maxRetentionSeconds = 365 * 24 * 3600

// Minimal in-memory notification notices also cover history opt-out failures.
type FailureNotice struct {
	ID        string    `json:"id"`
	PairingID string    `json:"pairingId"`
	ExpiresAt time.Time `json:"-"`
}

func (a *App) pendingFailureNotices() []FailureNotice {
	out := []FailureNotice{}
	for _, n := range a.failureNotices {
		if a.now().Before(n.ExpiresAt) {
			out = append(out, n)
		}
	}
	return out
}

type HistoryPolicy struct {
	Mode    string `json:"mode"`
	Seconds int    `json:"seconds,omitempty"`
}
type OwnerSettings struct {
	MaximumRequestSeconds   int              `json:"maximumRequestSeconds"`
	Default                 *ExecutionTarget `json:"default,omitempty"`
	History                 *HistoryPolicy   `json:"history,omitempty"`
	OutputBytes             int              `json:"outputBytes"`
	ConnectionExpirySeconds int              `json:"connectionExpirySeconds"`
}
type ConnectionOverrides struct {
	MaximumRequestSeconds   *int `json:"maximumRequestSeconds,omitempty"`
	OutputBytes             *int `json:"outputBytes,omitempty"`
	ConnectionExpirySeconds *int `json:"connectionExpirySeconds,omitempty"`
	MuteNotifications       bool `json:"muteNotifications,omitempty"`
}

func ownerSettings(s *State) OwnerSettings {
	if s.OwnerSettings != nil {
		return *s.OwnerSettings
	}
	return OwnerSettings{MaximumRequestSeconds: 600}
}
func validHistoryPolicy(p HistoryPolicy) bool {
	return (p.Mode == "keep" || p.Mode == "discard") && p.Seconds == 0 || p.Mode == "for" && p.Seconds >= 1 && p.Seconds <= maxRetentionSeconds
}
func historyPolicy(s *State, p *Pairing) HistoryPolicy {
	if p != nil {
		if p.RequestHistory == "for" {
			return HistoryPolicy{Mode: "for", Seconds: p.HistorySeconds}
		}
		if p.RequestHistory == "keep" || p.RequestHistory == "discard" {
			return HistoryPolicy{Mode: p.RequestHistory}
		}
	}
	if o := ownerSettings(s); o.History != nil {
		return *o.History
	}
	if s.KeepRequestHistory != nil && !*s.KeepRequestHistory {
		return HistoryPolicy{Mode: "discard"}
	}
	return HistoryPolicy{Mode: "keep"}
}
func retentionReduced(before, after HistoryPolicy) bool {
	if before.Mode == "discard" {
		return false
	}
	return after.Mode == "discard" || after.Mode == "for" && (before.Mode == "keep" || before.Seconds > after.Seconds)
}
func (a *App) maximumRequestSeconds(p *Pairing) int {
	n := ownerSettings(&a.state).MaximumRequestSeconds
	if p != nil && p.Overrides.MaximumRequestSeconds != nil {
		n = *p.Overrides.MaximumRequestSeconds
	}
	return n
}
func validOverrides(o ConnectionOverrides) bool {
	return (o.MaximumRequestSeconds == nil || *o.MaximumRequestSeconds >= 1 && *o.MaximumRequestSeconds <= 3600) && (o.OutputBytes == nil || *o.OutputBytes >= 4096 && *o.OutputBytes <= 2<<20) && (o.ConnectionExpirySeconds == nil || validConnectionExpiry(*o.ConnectionExpirySeconds))
}
func validConnectionExpiry(n int) bool { return n == 0 || n >= 3600 && n <= maxRetentionSeconds }
func (a *App) pairingExpiresAt(s *State, p *Pairing) *time.Time {
	n := ownerSettings(s).ConnectionExpirySeconds
	if p.Overrides.ConnectionExpirySeconds != nil {
		n = *p.Overrides.ConnectionExpirySeconds
	}
	if n == 0 || p.Revoked {
		return nil
	}
	at := p.CreatedAt
	if p.LastRequestAt != nil {
		at = *p.LastRequestAt
	}
	// A request in progress is activity. Give it the full idle period on finish.
	for _, j := range s.Jobs {
		if j.PairingID == p.ID && !terminal(j.Status) {
			return nil
		}
	}
	at = at.Add(time.Duration(n) * time.Second)
	return &at
}
func (a *App) expireConnections(s *State, now time.Time) {
	for _, p := range s.Pairings {
		if at := a.pairingExpiresAt(s, p); at != nil && !now.Before(*at) {
			p.Revoked = true
			p.CredentialHash = ""
			p.ClientPublicKey = ""
			a.closeConversations(s, p.ID, "app_revoked", now)
		}
	}
}
func executionRemaining(j *Job, now time.Time) time.Duration {
	at := j.CreatedAt
	if j.StartedAt != nil {
		at = *j.StartedAt
	}
	return time.Duration(j.Request.TimeoutSeconds)*time.Second - now.Sub(at)
}
func (a *App) ownerSettingRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("PATCH /api/login", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if e := desktop.SetLoginItem(a.loginItem, in.Enabled); e != nil {
			return fail(409, "login_unavailable", e.Error())
		}
		writeJSON(w, 200, desktop.LoginItemStatus(a.loginItem, a.loginReason))
		return nil
	}))
	mux.HandleFunc("PATCH /api/settings", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Settings         OwnerSettings `json:"settings"`
			ConfirmDiscard   bool          `json:"confirmDiscard"`
			ExpectedRevision *uint64       `json:"expectedRevision"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.ExpectedRevision != nil && *in.ExpectedRevision != a.state.SettingsRevision {
			return fail(409, "settings_changed", "Settings changed. Reopen Settings and review them.")
		}
		o := in.Settings
		if o.MaximumRequestSeconds < 1 || o.MaximumRequestSeconds > 3600 || o.OutputBytes != 0 && (o.OutputBytes < 4096 || o.OutputBytes > 2<<20) || !validConnectionExpiry(o.ConnectionExpirySeconds) || o.History == nil || !validHistoryPolicy(*o.History) {
			return fail(400, "invalid_settings", "Check the duration, history and output limits.")
		}
		if o.Default != nil {
			if o.Default.Action != "" {
				return fail(400, "invalid_route", "Choose an AI program as the global default.")
			}
			if err := a.validateTarget(nil, *o.Default); err != nil {
				return err
			}
			if err := a.validateNativeOptions(*o.Default); err != nil {
				return err
			}
		}
		reduced := retentionReduced(historyPolicy(&a.state, nil), *o.History)
		if reduced && !in.ConfirmDiscard {
			return fail(400, "history_confirmation_required", "Confirm removal of finished requests outside the new retention period.")
		}
		if err := a.update(func(s *State) error {
			s.OwnerSettings = &o
			s.SettingsRevision++
			keep := o.History.Mode != "discard"
			s.KeepRequestHistory = &keep
			if reduced {
				a.discardStoredHistory(s, func(j *Job) bool { return historyPolicy(s, s.Pairings[j.PairingID]).Mode == "discard" })
			}
			return nil
		}); err != nil {
			return err
		}
		writeJSON(w, 200, a.state.OwnerSettings)
		return nil
	}))
}
func historyExpired(s *State, j *Job, now time.Time) bool {
	p := historyPolicy(s, s.Pairings[j.PairingID])
	return p.Mode == "discard" || p.Mode == "for" && j.FinishedAt != nil && !now.Before(j.FinishedAt.Add(time.Duration(p.Seconds)*time.Second))
}
