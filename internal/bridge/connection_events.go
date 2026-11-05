package bridge

import (
	"net/http"
	"strconv"
	"time"
)

// Called with mu held. One generation wakes every authenticated local panel.
func (a *App) notifyConnections() {
	a.connectionRevision++
	if a.connectionChanged != nil {
		close(a.connectionChanged)
	}
	a.connectionChanged = make(chan struct{})
}

func (a *App) connectionEventRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("GET /api/connection-events", control(func(w http.ResponseWriter, r *http.Request) error {
		if a.connectionClosing {
			return fail(503, "runner_stopped", "WebDeviceCable is stopping.")
		}
		after := r.URL.Query().Get("after")
		if after != "" {
			revision, err := strconv.ParseUint(after, 10, 64)
			if err != nil {
				return fail(400, "invalid_revision", "Invalid connection revision.")
			}
			if revision == a.connectionRevision {
				if a.connectionWaiters >= 32 {
					return fail(429, "panel_limit", "Too many panels are waiting for connection updates.")
				}
				if a.connectionChanged == nil {
					a.connectionChanged = make(chan struct{})
				}
				changed := a.connectionChanged
				a.connectionWaiters++
				func() {
					a.mu.Unlock()
					defer a.mu.Lock()
					timer := time.NewTimer(20 * time.Second)
					defer timer.Stop()
					select {
					case <-changed:
					case <-timer.C:
					case <-r.Context().Done():
					}
				}()
				a.connectionWaiters--
			}
		}
		if r.Context().Err() != nil {
			return nil
		}
		// Locking the panel or expiring its session also ends its notification access.
		s, ok := a.sessions[r.Header.Get("X-Bridge-Session")]
		if !ok || a.now().After(s.Expires) {
			return fail(401, "session_expired", "Open WebDeviceCable to reconnect this panel.")
		}
		if a.connectionClosing {
			return fail(503, "runner_stopped", "WebDeviceCable is stopping.")
		}
		writeJSON(w, 200, map[string]any{"revision": a.connectionRevision, "pairingRequests": a.pendingPairingRequests(), "launches": a.pendingLaunchIntents()})
		return nil
	}))
}
