package bridge

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/onboarding"
)

type invitationInput struct {
	LogoID                     string            `json:"logoId,omitempty"`
	PublicKey                  string            `json:"-"`
	AppID                      string            `json:"appId"`
	Name                       string            `json:"name"`
	Origin                     string            `json:"origin"`
	AppUser                    string            `json:"appUser"`
	Files                      bool              `json:"files"`
	Automatic                  bool              `json:"automatic"`
	Mode                       string            `json:"mode"`
	Actions                    map[string]string `json:"actions"`
	CommandPermissionsAccepted bool              `json:"commandPermissionsAccepted"`
}

func (a *App) createInvitation(in invitationInput, origin string) (map[string]any, error) {
	if err := a.checkLogo(in.LogoID); err != nil {
		return nil, err
	}
	if in.Mode == "" {
		in.Mode = "restricted"
		if len(in.Actions) > 0 {
			in.Mode = "commands"
		}
	}
	p := Pairing{LogoID: in.LogoID, ClientPublicKey: in.PublicKey, ID: secret(), AppID: in.AppID, Name: in.Name, Origin: in.Origin, AppUser: in.AppUser, Automatic: in.Automatic, Files: in.Files, Mode: in.Mode, DeviceID: a.state.DeviceID, CreatedAt: a.now()}
	if len(in.Actions) > 0 && !in.CommandPermissionsAccepted {
		return nil, fail(400, "command_consent_required", "Command access must be explicitly accepted with OS account permissions.")
	}
	grants, e := a.grants(in.Actions)
	if e != nil {
		return nil, e
	}
	p.Actions = grants
	if e := validatePairing(p); e != nil {
		return nil, fail(400, "invalid_pairing", e.Error())
	}
	token := secret()
	expires := a.now().Add(5 * time.Minute)
	if e := a.update(func(s *State) error {
		for k, i := range s.Invitations {
			if !a.now().Before(i.ExpiresAt) {
				delete(s.Invitations, k)
			}
		}
		if len(s.Invitations) >= 32 || len(s.Pairings) >= 100 {
			return fail(409, "pairing_limit", "Remove unused pairings or wait for invitations to expire.")
		}
		s.Invitations[digest(token)] = &Invitation{Pairing: p, SecretHash: digest(token), ExpiresAt: expires}
		return nil
	}); e != nil {
		return nil, e
	}
	return map[string]any{"invitation": a.invitationBundle(token, origin, p, expires), "expiresAt": expires, "pairing": publicPairing(&p)}, nil
}

type pairingRequest struct {
	onboarding.Options
	PairingID    string    `json:"pairingId,omitempty"`
	LogoID       string    `json:"logoId,omitempty"`
	RelayURL     string    `json:"relayUrl,omitempty"`
	ConnectionID string    `json:"connectionId,omitempty"`
	PublicKey    string    `json:"-"`
	ID           string    `json:"id"`
	AppID        string    `json:"appId"`
	Name         string    `json:"name"`
	Origin       string    `json:"origin"`
	AppUser      string    `json:"appUser"`
	Status       string    `json:"status"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Invitation   string    `json:"invitation,omitempty"`
}

func (a *App) pendingPairingRequests() []pairingRequest {
	result := []pairingRequest{}
	for id, request := range a.pairingRequests {
		if !a.now().Before(request.ExpiresAt) {
			delete(a.pairingRequests, id)
			continue
		}
		// Keep the approved handoff visible even after a claim. Panels hide it
		// only when their full state contains the new pairing, avoiding a gap
		// when a connection notification arrives ahead of the state response.
		if request.Status == "pending" || request.Status == "approved" {
			copy := *request
			copy.Invitation = ""
			result = append(result, copy)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ExpiresAt.Before(result[j].ExpiresAt) })
	return result
}
func (a *App) pairingRequestRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc, origin string) {
	mux.HandleFunc("POST /api/pairing-requests", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			onboarding.Options
			ID      string `json:"id"`
			AppID   string `json:"appId"`
			Name    string `json:"name"`
			Origin  string `json:"origin"`
			AppUser string `json:"appUser"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if len(in.ID) != 64 || strings.Trim(in.ID, "0123456789abcdef") != "" {
			return fail(400, "invalid_pairing", "Invalid connection request ID.")
		}
		if err := validatePairing(Pairing{AppID: in.AppID, Name: in.Name, Origin: in.Origin, AppUser: in.AppUser, Mode: "restricted"}); err != nil {
			return fail(400, "invalid_pairing", err.Error())
		}
		if err := in.Options.Validate(in.Origin); err != nil {
			return fail(400, "invalid_logo", err.Error())
		}
		a.pendingPairingRequests()
		if existing := a.pairingRequests[in.ID]; existing != nil {
			if existing.Options != in.Options || existing.AppID != in.AppID || existing.Name != in.Name || existing.Origin != in.Origin || existing.AppUser != in.AppUser {
				return fail(409, "pairing_conflict", "This request ID belongs to a different connection.")
			}
			writeJSON(w, 200, map[string]string{"id": existing.ID})
			return nil
		}
		if len(a.pairingRequests) >= 32 {
			return fail(429, "pairing_limit", "Too many connection requests. Try again shortly.")
		}
		request := &pairingRequest{Options: in.Options, ID: in.ID, AppID: in.AppID, Name: in.Name, Origin: in.Origin, AppUser: in.AppUser, Status: "pending", ExpiresAt: a.now().Add(5 * time.Minute)}
		a.pairingRequests[in.ID] = request
		a.notifyConnections()
		writeJSON(w, 201, map[string]string{"id": request.ID})
		return nil
	}))
	mux.HandleFunc("GET /api/pairing-requests/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		request := a.pairingRequests[r.PathValue("id")]
		if request == nil || !a.now().Before(request.ExpiresAt) {
			return fail(410, "pairing_expired", "This connection request expired. Try again from the website.")
		}
		writeJSON(w, 200, request)
		return nil
	}))
	mux.HandleFunc("POST /api/pairing-requests/{id}/decision", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Approve   bool    `json:"approve"`
			Automatic *bool   `json:"automatic"`
			Files     *bool   `json:"files"`
			LogoID    *string `json:"logoId"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		request := a.pairingRequests[r.PathValue("id")]
		if request == nil || !a.now().Before(request.ExpiresAt) {
			return fail(410, "pairing_expired", "This connection request expired. Try again from the website.")
		}
		decision := "rejected"
		if in.Approve {
			decision = "approved"
		}
		if request.Status != "pending" && request.Status != decision {
			return fail(409, "pairing_decided", "This connection request was already answered.")
		}
		if request.Status == "pending" && in.Approve {
			automatic := request.RequestedPermissions.AutomaticApprovals
			if in.Automatic != nil {
				automatic = *in.Automatic
			}
			files := request.RequestedPermissions.Files
			if in.Files != nil {
				files = *in.Files
			}
			logoID := ""
			if in.LogoID != nil {
				logoID = *in.LogoID
			}
			if err := a.checkLogo(logoID); err != nil {
				return err
			}
			result, err := a.createInvitation(invitationInput{LogoID: logoID, Automatic: automatic, Files: files, PublicKey: request.PublicKey, AppID: request.AppID, Name: request.Name, AppUser: request.AppUser, Origin: request.Origin}, origin)
			if err != nil {
				return err
			}
			request.Invitation = result["invitation"].(string)
			request.PairingID = result["pairing"].(Pairing).ID
			request.LogoID = logoID
		}
		request.Status = decision
		a.notifyConnections()
		writeJSON(w, 200, map[string]string{"status": request.Status})
		return nil
	}))
}
