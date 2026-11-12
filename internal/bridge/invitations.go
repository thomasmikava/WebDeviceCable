package bridge

import (
	"net/http"
	"sort"
	"time"
)

// The pairing ID is a public management handle, never an invitation verifier.
// Tokens remain hash-only on disk and are not recoverable through this API.
type pendingInvitation struct {
	ID        string    `json:"id"`
	Pairing   Pairing   `json:"pairing"`
	ExpiresAt time.Time `json:"expiresAt"`
	Manual    bool      `json:"manual"`
}

func (a *App) pendingInvitations() []pendingInvitation {
	result := []pendingInvitation{}
	for _, invitation := range a.state.Invitations {
		if !a.now().Before(invitation.ExpiresAt) {
			continue
		}
		p := invitation.Pairing
		manual := p.ClientPublicKey == "" && p.Relay == nil
		for _, request := range a.pairingRequests {
			if request.PairingID == p.ID {
				manual = false
				break
			}
		}
		result = append(result, pendingInvitation{ID: p.ID, Pairing: publicPairing(&p), ExpiresAt: invitation.ExpiresAt, Manual: manual})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ExpiresAt.Equal(result[j].ExpiresAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].ExpiresAt.Before(result[j].ExpiresAt)
	})
	return result
}

func (a *App) invitationRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("GET /api/invitations", control(func(w http.ResponseWriter, r *http.Request) error {
		// Also require the session's CSRF token for this private metadata read.
		if !equal(r.Header.Get("X-Bridge-CSRF"), a.sessions[r.Header.Get("X-Bridge-Session")].CSRF) {
			return fail(403, "csrf_denied", "Invitation access requires the session CSRF token.")
		}
		writeJSON(w, 200, map[string]any{"invitations": a.pendingInvitations()})
		return nil
	}))
	mux.HandleFunc("DELETE /api/invitations/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		if !validStorageID(id) {
			return fail(400, "invalid_invitation", "Invalid invitation ID.")
		}
		if a.state.Pairings[id] != nil {
			return fail(409, "invitation_used", "This invitation was already used. Choose Disconnect on the connection to revoke access.")
		}
		if err := a.update(func(s *State) error {
			for key, invitation := range s.Invitations {
				if invitation.Pairing.ID == id {
					delete(s.Invitations, key)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		// A previously approved rendezvous must not continue handing out its token.
		for _, request := range a.pairingRequests {
			if request.PairingID == id {
				request.Invitation = ""
				request.Status = "rejected"
			}
		}
		a.notifyConnections()
		// Retrying cancellation after an expired/revoked invitation is harmless.
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
}
