package bridge

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/updates"
)

func (a *App) ConfigureUpdates(build updates.Build, install func(updates.Plan) error) error {
	m, e := updates.New(build, filepath.Join(a.dir, "updates"), updates.Hooks{
		Read: ReadLocalFile, Write: writeLocalFile, Prepare: PrepareDataDirectory,
		Begin: a.beginUpdate, End: func() {
			a.mu.Lock()
			a.updating = false
			_ = os.Remove(filepath.Join(a.dir, "panel-resume.json"))
			a.mu.Unlock()
		}, Install: install,
	})
	if e != nil {
		return e
	}
	a.updater = m
	m.Start()
	return nil
}
func (a *App) beginUpdate(force bool) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return false, errors.New("WebDeviceCable is shutting down")
	}
	if force {
		if e := a.update(func(s *State) error {
			a.closeConversations(s, "", "update_requested", a.now())
			for _, j := range s.Jobs {
				if !terminal(j.Status) {
					finish(j, "cancelled", "update_requested", "Ended by the owner to update WebDeviceCable.", a.now())
				}
			}
			return nil
		}); e != nil {
			return false, e
		}
		a.updating = true
		for _, r := range a.commands {
			r.cancel()
		}
		for _, r := range a.conversations {
			r.cancel()
		}
		for _, op := range a.permissions {
			if op.Status == "pending" || op.Status == "approved" || op.Status == "dispatched" {
				op.Status = "denied"
				select {
				case <-op.changed:
				default:
					close(op.changed)
				}
			}
		}
		a.inputs = map[string]*nativeInput{}
		for id := range a.pairingRequests {
			delete(a.pairingRequests, id)
		}
	}
	for _, j := range a.state.Jobs {
		if !terminal(j.Status) {
			return false, nil
		}
	}
	for _, c := range a.state.Conversations {
		if c.Status == "open" {
			return false, nil
		}
	}
	for _, r := range a.commands {
		select {
		case <-r.stopped:
		default:
			return false, nil
		}
	}
	for _, r := range a.conversations {
		select {
		case <-r.stopped:
		default:
			return false, nil
		}
	}
	for _, op := range a.permissions {
		if a.operationLive(op) && (op.Status == "pending" || op.Status == "approved" || op.Status == "dispatched") {
			return false, nil
		}
	}
	for _, p := range a.pairingRequests {
		if p.Status == "pending" && a.now().Before(p.ExpiresAt) {
			return false, nil
		}
	}
	for _, f := range a.inputs {
		if l, ok := a.leases[f.LeaseID]; ok && a.now().Before(l.ExpiresAt) {
			return false, nil
		}
	}
	data, e := json.Marshal(panelResume{Expires: a.now().Add(2 * time.Minute), Sessions: a.sessions})
	if e != nil {
		return false, e
	}
	if e = writeLocalFile(filepath.Join(a.dir, "panel-resume.json"), data); e != nil {
		return false, e
	}
	a.updating = true
	return true, nil
}

type panelResume struct {
	Expires  time.Time          `json:"expires"`
	Sessions map[string]session `json:"sessions"`
}

func (a *App) resumePanel() error {
	path := filepath.Join(a.dir, "panel-resume.json")
	data, e := ReadLocalFile(path, 16384)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var resume panelResume
	if json.Unmarshal(data, &resume) != nil || len(resume.Sessions) > 32 {
		return errors.New("invalid panel update handoff; preserved for recovery")
	}
	if a.now().Before(resume.Expires) && resume.Expires.Before(a.now().Add(3*time.Minute)) {
		for id, s := range resume.Sessions {
			if !validStorageID(id) || !validStorageID(s.CSRF) {
				return errors.New("invalid panel update session")
			}
			if a.now().Before(s.Expires) {
				a.sessions[id] = s
			}
		}
	}
	return os.Remove(path)
}
func (a *App) updateRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc, wrap func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	status := func(w http.ResponseWriter) error {
		if a.updater == nil {
			return fail(503, "updates_unavailable", "Updates are not configured in this development runner.")
		}
		writeJSON(w, 200, a.updater.Status())
		return nil
	}
	mux.HandleFunc("GET /api/updates", control(func(w http.ResponseWriter, r *http.Request) error { return status(w) }))
	mux.HandleFunc("PATCH /api/updates", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Automatic bool `json:"automatic"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if a.updater == nil {
			return fail(503, "updates_unavailable", "Updates are unavailable.")
		}
		if e := a.updater.SetAutomatic(in.Automatic); e != nil {
			return fail(409, "update_settings_failed", e.Error())
		}
		return status(w)
	}))
	for _, action := range []string{"check", "install"} {
		mux.HandleFunc("POST /api/updates/"+action, control(func(w http.ResponseWriter, r *http.Request) error {
			if a.updater == nil {
				return fail(503, "updates_unavailable", "Updates are unavailable.")
			}
			var in struct {
				EndSessions bool `json:"endSessions"`
			}
			if e := decode(w, r, &in); e != nil {
				return e
			}
			var e error
			if action == "check" {
				e = a.updater.Check()
			} else {
				e = a.updater.Install(in.EndSessions)
			}
			if e != nil {
				return fail(409, "update_not_ready", e.Error())
			}
			return status(w)
		}))
	}
	// Used only by an installed local executable. Never accept browser-origin requests.
	mux.HandleFunc("POST /api/internal/updates", wrap(func(w http.ResponseWriter, r *http.Request) error {
		if r.Header.Get("Origin") != "" || !equal(bearer(r), a.adminKey) {
			return fail(403, "owner_required", "Local owner authentication is required.")
		}
		if a.updater == nil {
			return fail(503, "updates_unavailable", "Updates are unavailable.")
		}
		var in struct {
			Action      string `json:"action"`
			EndSessions bool   `json:"endSessions"`
			Version     string `json:"version"`
			Stage       string `json:"stage"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		var e error
		switch in.Action {
		case "status":
			return status(w)
		case "check":
			e = a.updater.Check()
		case "install":
			e = a.updater.Install(in.EndSessions)
		case "native-poll":
			writeJSON(w, 200, a.updater.NativePoll())
			return nil
		case "native-report":
			e = a.updater.NativeReport(in.Version, in.Stage, "")
		case "failed":
			a.updater.InstallationFailed()
		default:
			return fail(400, "invalid_update_action", "Unknown update action.")
		}
		if e != nil {
			return fail(409, "update_not_ready", e.Error())
		}
		return status(w)
	}))
}
func updateStatusJSON(m *updates.Manager) json.RawMessage {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m.Status())
	return b
}
