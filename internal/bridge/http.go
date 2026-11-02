package bridge

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web/*
var assets embed.FS

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string                 { return e.Message }
func fail(status int, code, message string) error { return &apiError{status, code, message} }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, e error) {
	var ae *apiError
	if !errors.As(e, &ae) {
		ae = &apiError{500, "storage_error", "Could not save local state. Check free disk space and directory permissions."}
	}
	writeJSON(w, ae.Status, map[string]any{"error": failureFor(ae.Status, ae.Code, ae.Message)})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return fail(415, "json_required", "Use Content-Type: application/json.")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 40<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return fail(400, "invalid_request", "Invalid JSON, unknown field, or request exceeds 40 KiB.")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return fail(400, "invalid_request", "Exactly one JSON value is required.")
	}
	return nil
}
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}
func equal(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Handler accepts only the exact loopback Host on which this instance is listening.
func (a *App) Handler(host string, shutdown func()) http.Handler {
	origin := "http://" + host
	mux := http.NewServeMux()
	wrap := func(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if e := fn(w, r); e != nil {
				writeError(w, e)
			}
		}
	}
	control := func(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return wrap(func(w http.ResponseWriter, r *http.Request) error {
			if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin {
				return fail(403, "origin_denied", "Control-panel origin mismatch.")
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			id := r.Header.Get("X-Bridge-Session")
			if id == "" {
				return fail(401, "session_required", "Open the control panel using WebDeviceCable open.")
			}
			s, ok := a.sessions[id]
			if !ok || a.now().After(s.Expires) {
				delete(a.sessions, id)
				return fail(401, "session_expired", "Open the control panel using WebDeviceCable open.")
			}
			if r.Method != "GET" && (r.Header.Get("Origin") != origin || !equal(r.Header.Get("X-Bridge-CSRF"), s.CSRF)) {
				return fail(403, "csrf_denied", "Control changes require the session CSRF token and exact origin.")
			}
			if a.updating && r.Method != "GET" && !strings.HasPrefix(r.URL.Path, "/api/updates") {
				return fail(503, "bridge_updating", "WebDeviceCable is updating. Reconnect in a moment.")
			}
			return fn(w, r)
		})
	}
	paired := func(fn func(http.ResponseWriter, *http.Request, *Pairing) error) http.HandlerFunc {
		return wrap(func(w http.ResponseWriter, r *http.Request) error {
			a.mu.Lock()
			defer a.mu.Unlock()
			p, e := a.authenticate(r)
			if e != nil {
				return e
			}
			if a.updating {
				w.Header().Set("Retry-After", "3")
				return fail(503, "bridge_updating", "WebDeviceCable is updating. Reconnect in a moment.")
			}
			return fn(w, r, p)
		})
	}
	a.launchRoutes(mux, wrap, control, origin)
	a.updateRoutes(mux, control, wrap)
	a.permissionRoutes(mux, paired, control)
	a.commandRoutes(mux, control)
	a.programRoutes(mux, control)
	a.pairingRequestRoutes(mux, control, origin)
	a.preferenceRoutes(mux, paired, control)
	a.conversationRoutes(mux, paired, control)
	a.nativeFileRoutes(mux, paired, control)
	a.relayRoutes(mux, control, origin)
	mux.HandleFunc("POST /api/runtime", wrap(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Nonce string `json:"nonce"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if !validStorageID(in.Nonce) {
			return fail(400, "invalid_nonce", "A 32-byte hex challenge is required.")
		}
		info := RuntimeInfo{URL: origin, PID: os.Getpid()}
		writeJSON(w, 200, map[string]any{"pid": info.PID, "proof": RuntimeProof(a.adminKey, in.Nonce, info)})
		return nil
	}))
	mux.HandleFunc("POST /api/session", wrap(func(w http.ResponseWriter, r *http.Request) error {
		if r.Header.Get("Origin") != origin || !equal(bearer(r), a.adminKey) {
			return fail(401, "invalid_control_key", "Use the private link printed by WebDeviceCable open.")
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		for k, s := range a.sessions {
			if a.now().After(s.Expires) {
				delete(a.sessions, k)
			}
		}
		if id := r.Header.Get("X-Bridge-Session"); id != "" {
			if previous, ok := a.sessions[id]; ok {
				previous.Expires = a.now().Add(12 * time.Hour)
				a.sessions[id] = previous
				writeJSON(w, 200, map[string]string{"session": id, "csrf": previous.CSRF})
				return nil
			}
		}
		if len(a.sessions) >= 32 {
			return fail(429, "session_limit", "Too many control-panel sessions.")
		}
		id := secret()
		csrf := secret()
		a.sessions[id] = session{CSRF: csrf, Expires: a.now().Add(12 * time.Hour)}
		writeJSON(w, 200, map[string]string{"session": id, "csrf": csrf})
		return nil
	}))
	mux.HandleFunc("GET /api/session", control(func(w http.ResponseWriter, r *http.Request) error {
		id := r.Header.Get("X-Bridge-Session")
		writeJSON(w, 200, map[string]string{"session": id, "csrf": a.sessions[id].CSRF})
		return nil
	}))
	mux.HandleFunc("DELETE /api/session", control(func(w http.ResponseWriter, r *http.Request) error {
		delete(a.sessions, r.Header.Get("X-Bridge-Session"))
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("GET /api/state", control(func(w http.ResponseWriter, r *http.Request) error {
		pairs := []Pairing{}
		providersByPairing := map[string][]Capability{}
		relayConnections := []map[string]string{}
		for _, p := range a.state.Pairings {
			pairs = append(pairs, publicPairing(p))
			providersByPairing[p.ID] = a.capabilities(p)
			if p.Relay != nil && !p.Revoked {
				relayConnections = append(relayConnections, map[string]string{"relayUrl": p.Relay.URL, "connectionId": p.Relay.ConnectionID, "pairingId": p.ID})
			}
		}
		jobs := []Job{}
		for _, j := range a.state.Jobs {
			copy := publicJob(j)
			copy.Plan = j.Plan
			jobs = append(jobs, copy)
		}
		writeJSON(w, 200, map[string]any{"updates": updateStatusJSON(a.updater), "providersByPairing": providersByPairing, "pairingRequests": a.pendingPairingRequests(), "launches": a.pendingLaunchIntents(), "relayConnections": relayConnections, "permissions": a.permissionList(), "sharing": a.state.Sharing, "conversations": a.state.Conversations, "deviceId": a.state.DeviceID, "securityReset": a.state.SecurityReset, "enabled": a.state.Enabled, "revision": a.state.Revision, "pairings": pairs, "jobs": jobs, "commands": a.actionList(), "configPath": filepath.Join(a.dir, "config.yaml"), "allowWebCommandEditing": a.config.Bridge.AllowWebCommandEditing, "programs": a.programList(), "programHashes": a.programHashes(), "providers": a.capabilities(nil), "native": a.native, "nativeChecking": a.nativeChecking, "leaseSeconds": int(LeaseTTL.Seconds()), "modes": []map[string]any{{"id": "restricted", "available": true}, {"id": "commands", "available": true}, {"id": "app-tools", "available": true}, {"id": "workspace", "available": true}}})
		return nil
	}))
	mux.HandleFunc("POST /api/providers/check", control(func(w http.ResponseWriter, r *http.Request) error {
		go a.checkNative()
		writeJSON(w, 202, map[string]bool{"checking": true})
		return nil
	}))
	mux.HandleFunc("POST /api/invitations", control(func(w http.ResponseWriter, r *http.Request) error {
		var in invitationInput
		if err := decode(w, r, &in); err != nil {
			return err
		}
		result, err := a.createInvitation(in, origin)
		if err != nil {
			return err
		}
		writeJSON(w, 201, result)
		return nil
	}))
	mux.HandleFunc("PATCH /api/pairings/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Paused                     *bool              `json:"paused"`
			Automatic                  *bool              `json:"automatic"`
			Revoke                     bool               `json:"revoke"`
			Actions                    *map[string]string `json:"actions"`
			CommandPermissionsAccepted bool               `json:"commandPermissionsAccepted"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		id := r.PathValue("id")
		if a.state.Pairings[id] == nil {
			return fail(404, "not_found", "Pairing not found.")
		}
		var grants map[string]string
		if in.Actions != nil {
			if len(*in.Actions) > 0 && !in.CommandPermissionsAccepted {
				return fail(400, "command_consent_required", "Command access must be explicitly accepted with OS account permissions.")
			}
			var e error
			grants, e = a.grants(*in.Actions)
			if e != nil {
				return e
			}
		}
		if e := a.update(func(s *State) error {
			p := s.Pairings[id]
			if p.Revoked {
				return fail(409, "revoked", "Create a new pairing after revocation.")
			}
			if in.Paused != nil {
				p.Paused = *in.Paused
			}
			if in.Automatic != nil {
				p.Automatic = *in.Automatic
			}
			if in.Actions != nil {
				p.Actions = grants
				p.Mode = "restricted"
				if len(grants) > 0 {
					p.Mode = "commands"
				}
			}
			if in.Revoke || in.Paused != nil && *in.Paused || in.Actions != nil {
				a.closeConversations(s, id, "app_access_changed", a.now())
			}
			if in.Revoke {
				p.Revoked = true
				p.CredentialHash = ""
				p.ClientPublicKey = ""
			}
			for _, j := range s.Jobs {
				if j.PairingID == id && jobKind(j) == "command" && !terminal(j.Status) && in.Actions != nil {
					finish(j, "cancelled", "command_access_changed", "Command access was reviewed. Submit a new request.", a.now())
				}
				if j.PairingID == id && !terminal(j.Status) && (p.Revoked || p.Paused) {
					finish(j, "cancelled", "app_access_changed", "User paused or revoked application access.", a.now())
				}
			}
			return nil
		}); e != nil {
			return e
		}
		writeJSON(w, 200, publicPairing(a.state.Pairings[id]))
		return nil
	}))
	mux.HandleFunc("PATCH /api/runner", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if e := a.update(func(s *State) error {
			s.Enabled = in.Enabled
			if !in.Enabled {
				a.closeConversations(s, "", "runner_paused", a.now())
			}
			if !in.Enabled {
				for _, j := range s.Jobs {
					if !terminal(j.Status) {
						finish(j, "cancelled", "runner_paused", "Runner paused by the user.", a.now())
					}
				}
			}
			return nil
		}); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"enabled": in.Enabled})
		return nil
	}))
	mux.HandleFunc("POST /api/jobs/{id}/decision", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Approve bool `json:"approve"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if e := a.update(func(s *State) error {
			j := findJob(s, r.PathValue("id"))
			if j == nil {
				return fail(404, "not_found", "Job not found.")
			}
			if j.Status != "awaiting_approval" {
				return fail(409, "invalid_state", "This job is no longer awaiting approval.")
			}
			l, ok := a.leases[j.LeaseID]
			p := s.Pairings[j.PairingID]
			if !ok || !a.now().Before(l.ExpiresAt) || p == nil || p.Revoked || p.Paused || !s.Enabled {
				return fail(409, "inactive_request", "The requesting tab or app is no longer active.")
			}
			if in.Approve {
				j.Status = "queued"
				j.Revision++
			} else {
				finish(j, "cancelled", "user_declined", "User declined this request.", a.now())
			}
			return nil
		}); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("POST /api/jobs/{id}/cancel", control(func(w http.ResponseWriter, r *http.Request) error { return a.cancelJob(w, r.PathValue("id"), "") }))
	mux.HandleFunc("DELETE /api/history", control(func(w http.ResponseWriter, r *http.Request) error {
		if e := a.update(func(s *State) error {
			keep := []*Job{}
			for _, j := range s.Jobs {
				if !terminal(j.Status) {
					keep = append(keep, j)
				}
			}
			s.Jobs = keep
			for id, conversation := range s.Conversations {
				if conversation.Status != "open" {
					delete(s.Conversations, id)
				}
			}
			return nil
		}); e != nil {
			return e
		}
		entries, _ := os.ReadDir(filepath.Join(a.dir, "artifacts"))
		for _, entry := range entries {
			if findJob(&a.state, entry.Name()) == nil {
				_ = os.RemoveAll(filepath.Join(a.dir, "artifacts", entry.Name()))
			}
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("POST /api/shutdown", wrap(func(w http.ResponseWriter, r *http.Request) error {
		if !equal(bearer(r), a.adminKey) {
			return fail(401, "invalid_control_key", "A local control key is required.")
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		if shutdown != nil {
			go shutdown()
		}
		return nil
	}))
	mux.HandleFunc("POST /v2/pairings/claim", wrap(func(w http.ResponseWriter, r *http.Request) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		key := digest(bearer(r))
		inv, ok := a.state.Invitations[key]
		if !ok || !a.now().Before(inv.ExpiresAt) {
			return fail(401, "invalid_invitation", "Invitation expired or has already been used.")
		}
		var in struct {
			AppID   string `json:"appId"`
			AppUser string `json:"appUser"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		if r.Header.Get("Origin") != inv.Pairing.Origin || in.AppID != inv.Pairing.AppID || in.AppUser != inv.Pairing.AppUser {
			return fail(403, "identity_mismatch", "App identity, origin or app user does not match the invitation.")
		}

		verified, ok := r.Context().Value(verifiedContextKey{}).(requestIdentity)
		if !ok {
			return fail(401, "secure_channel_required", "Secure pairing is required.")
		}
		p := inv.Pairing
		if p.ClientPublicKey != "" && p.ClientPublicKey != verified.PublicKey {
			return fail(403, "identity_mismatch", "This invitation belongs to its requesting key.")
		}
		p.ClientPublicKey = verified.PublicKey
		p.CredentialHash = ""
		if e := a.update(func(s *State) error { s.Pairings[p.ID] = &p; delete(s.Invitations, key); return nil }); e != nil {
			return e
		}
		writeJSON(w, 201, map[string]any{"pairing": a.remotePairing(&p)})
		return nil
	}))
	mux.HandleFunc("GET /v2/capabilities", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		writeJSON(w, 200, map[string]any{"protocolVersion": 2, "routingVersion": 2, "routingConfigured": routingConfigured(p), "appToolPermissions": "application", "fileTransfer": true, "maxTextBytes": a.textLimit(), "pairing": a.remotePairing(p), "providers": a.remoteCapabilities(p), "actions": a.actionCapabilities(p), "connectionTest": true, "modes": []string{"restricted", "commands", "app-tools", "workspace"}, "leaseSeconds": int(pairingLeaseTTL(p).Seconds()), "remoteAccess": false})
		return nil
	}))
	mux.HandleFunc("GET /v2/actions", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		writeJSON(w, 200, map[string]any{"actions": a.actionCapabilities(p)})
		return nil
	}))
	mux.HandleFunc("POST /v2/admission", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		var in AdmissionRequest
		if err := decode(w, r, &in); err != nil {
			return err
		}
		status, err := a.preflight(p, in)
		if err != nil {
			return err
		}
		writeJSON(w, 200, status)
		return nil
	}))
	mux.HandleFunc("POST /v2/jobs/lookup", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		var in struct {
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 128 {
			return fail(400, "invalid_job", "Supply the original idempotency key.")
		}
		receipt, consumed, err := a.receipt(digest(p.ID + ":" + in.IdempotencyKey))
		if err != nil {
			return err
		}
		if consumed {
			job := findJob(&a.state, receipt.JobID)
			if job == nil {
				return fail(410, "history_deleted", "This request was consumed and its history was cleared.")
			}
			writeJSON(w, 200, map[string]any{"job": a.remoteJob(job)})
		} else {
			writeJSON(w, 200, map[string]any{"job": nil})
		}
		return nil
	}))
	mux.HandleFunc("POST /v2/lease", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		if !a.state.Enabled || p.Paused {
			return fail(409, "paused", "Runner or application is paused.")
		}
		if len(a.leases) >= 500 {
			return fail(429, "lease_limit", "Too many active tabs.")
		}
		id := secret()
		l := Lease{PairingID: p.ID, ExpiresAt: a.now().Add(pairingLeaseTTL(p))}
		a.leases[id] = l
		writeJSON(w, 201, map[string]any{"lease": id, "expiresAt": l.ExpiresAt, "heartbeatSeconds": 3})
		return nil
	}))
	mux.HandleFunc("PUT /v2/lease", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		id := r.Header.Get("X-Bridge-Lease")
		l, ok := a.leases[id]
		if !ok || l.PairingID != p.ID || !a.now().Before(l.ExpiresAt) {
			return fail(410, "lease_expired", "This tab's lease expired. Open a new lease and explicitly submit a new request.")
		}
		if !a.state.Enabled || p.Paused {
			return fail(409, "paused", "Runner or application is paused.")
		}
		l.ExpiresAt = a.now().Add(pairingLeaseTTL(p))
		a.leases[id] = l
		writeJSON(w, 200, map[string]any{"expiresAt": l.ExpiresAt})
		return nil
	}))
	mux.HandleFunc("DELETE /v2/lease", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		id := r.Header.Get("X-Bridge-Lease")
		if l, ok := a.leases[id]; ok && l.PairingID == p.ID {
			if e := a.update(func(s *State) error {
				for _, c := range s.Conversations {
					if c.LeaseID == id && c.Status == "open" {
						c.Status = "closed"
						c.Code = "tab_disconnected"
						c.UpdatedAt = a.now()
					}
				}
				for _, j := range s.Jobs {
					if j.LeaseID == id && !terminal(j.Status) {
						finish(j, "cancelled", "tab_disconnected", "The requesting tab closed its lease.", a.now())
					}
				}
				return nil
			}); e != nil {
				return e
			}
			delete(a.leases, id)
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("POST /v2/jobs", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		var in JobRequest
		if err := decode(w, r, &in); err != nil {
			return err
		}
		job, duplicate, err := a.createJob(p, in, lease)
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
	mux.HandleFunc("GET /v2/jobs/{id}", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		j := findJob(&a.state, r.PathValue("id"))
		if j == nil || j.PairingID != p.ID {
			return fail(404, "not_found", "Job not found.")
		}
		writeJSON(w, 200, a.remoteJob(j))
		return nil
	}))
	mux.HandleFunc("POST /v2/jobs/{id}/cancel", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		return a.cancelJob(w, r.PathValue("id"), p.ID)
	}))
	mux.HandleFunc("GET /v2/jobs/{id}/events", a.streamJob)
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		mux.HandleFunc(method+" /v2/", wrap(func(w http.ResponseWriter, r *http.Request) error {
			return fail(404, "not_found", "Resource not found.")
		}))
	}
	mux.HandleFunc("POST /v2/channel", wrap(func(w http.ResponseWriter, r *http.Request) error { return a.openChannel(w, r, origin) }))
	mux.HandleFunc("DELETE /v2/channel/current", wrap(func(w http.ResponseWriter, r *http.Request) error {
		identity, ok := r.Context().Value(verifiedContextKey{}).(requestIdentity)
		if !ok || identity.ChannelID == "" {
			return fail(401, "signature_required", "Use the secure channel.")
		}
		a.mu.Lock()
		delete(a.channels, identity.ChannelID)
		a.mu.Unlock()
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("POST /v2/request", wrap(func(w http.ResponseWriter, r *http.Request) error { return a.secureCall(w, r, mux) }))
	static, _ := fs.Sub(assets, "web")
	a.resumeRelays(mux, origin)
	files := http.FileServer(http.FS(static))
	mux.Handle("GET /", files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.Host != host {
			writeError(w, fail(403, "host_denied", "Invalid local host."))
			return
		}
		remote, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil || !net.ParseIP(remote).IsLoopback() {
			writeError(w, fail(403, "loopback_required", "Loopback requests only."))
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			allowed := o == origin
			if r.URL.Path == "/v2/connect" {
				a.mu.Lock()
				for _, intent := range a.launchIntents {
					if intent.Origin == o && a.now().Before(intent.Expires) {
						allowed = true
					}
				}
				a.mu.Unlock()
			}
			if !allowed && strings.HasPrefix(r.URL.Path, "/v2/") {
				a.mu.Lock()
				for _, p := range a.state.Pairings {
					if !p.Revoked && p.Origin == o {
						allowed = true
					}
				}
				for _, channel := range a.channels {
					if channel.Origin == o && a.now().Before(channel.Expires) {
						allowed = true
					}
				}
				for _, inv := range a.state.Invitations {
					if a.now().Before(inv.ExpiresAt) && inv.Pairing.Origin == o {
						allowed = true
					}
				}
				a.mu.Unlock()
			}
			if !allowed {
				writeError(w, fail(403, "origin_denied", "This browser origin is not paired."))
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Bridge-Lease, X-Bridge-CSRF, Last-Event-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v2/") && r.URL.Path != "/v2/channel" && r.URL.Path != "/v2/request" && r.URL.Path != "/v2/connect" {
			writeError(w, fail(426, "secure_transport_required", "Use protocol v2 signed, encrypted transport."))
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *App) authenticate(r *http.Request) (*Pairing, error) {
	verified, ok := r.Context().Value(verifiedContextKey{}).(requestIdentity)
	if !ok {
		return nil, fail(401, "secure_channel_required", "A signed secure request is required.")
	}
	p := a.state.Pairings[verified.PairingID]
	if p == nil || p.Revoked || !equal(p.ClientPublicKey, verified.PublicKey) {
		return nil, fail(401, "pairing_revoked", "Unknown, revoked or mismatched pairing key.")
	}
	if r.Header.Get("Origin") != p.Origin {
		return nil, fail(403, "origin_mismatch", "Pairing origin mismatch.")
	}
	return p, nil
}
func findJob(s *State, id string) *Job {
	for _, j := range s.Jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}
func (a *App) cancelJob(w http.ResponseWriter, id, pairID string) error {
	j := findJob(&a.state, id)
	if j == nil || pairID != "" && j.PairingID != pairID {
		return fail(404, "not_found", "Job not found.")
	}
	if !terminal(j.Status) {
		if e := a.update(func(s *State) error {
			finish(findJob(s, id), "cancelled", "user_cancelled", "Request cancelled.", a.now())
			return nil
		}); e != nil {
			return e
		}
	}
	writeJSON(w, 200, a.remoteJob(findJob(&a.state, id)))
	return nil
}
func (a *App) streamJob(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, fail(500, "stream_unavailable", "Streaming unavailable."))
		return
	}
	last := ""
	started := false
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		a.mu.Lock()
		p, e := a.authenticate(r)
		var job Job
		if e == nil {
			j := findJob(&a.state, r.PathValue("id"))
			if j == nil || j.PairingID != p.ID {
				e = fail(404, "not_found", "Job not found.")
			} else {
				job = a.remoteJob(j)
			}
		}
		a.mu.Unlock()
		if e != nil {
			if !started {
				writeError(w, e)
			}
			return
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Accel-Buffering", "no")
			started = true
		}
		revision := fmt.Sprint(job.Revision)
		if revision != last {
			b, _ := json.Marshal(job)
			if _, e = fmt.Fprintf(w, "id: %s\nevent: job\ndata: %s\n\n", revision, b); e != nil {
				return
			}
			flusher.Flush()
			last = revision
		}
		if terminal(job.Status) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}
