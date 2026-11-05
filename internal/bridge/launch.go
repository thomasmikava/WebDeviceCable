package bridge

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/onboarding"
)

type LaunchIntent struct {
	ID           string          `json:"requestId"`
	Origin       string          `json:"origin"`
	PublicKey    string          `json:"publicKey"`
	Expires      time.Time       `json:"-"`
	Used         map[string]bool `json:"-"`
	Kind         string          `json:"kind,omitempty"`
	RelayURL     string          `json:"relayUrl,omitempty"`
	Code         string          `json:"code,omitempty"`
	ConnectionID string          `json:"connectionId,omitempty"`
	Handled      bool            `json:"-"`
}

func ParseLaunchURI(raw string) (LaunchIntent, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u.Scheme != "webdevicecable" || u.Path != "" || u.Fragment != "" || u.User != nil {
		return LaunchIntent{}, fail(400, "invalid_launch", "Invalid WebDeviceCable connection link.")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err == nil && (u.Host == "relay-connect" || u.Host == "settings") {
		field := "code"
		if u.Host == "settings" {
			field = "connectionId"
		}
		if len(q) != 2 || len(q["relayUrl"]) != 1 || len(q[field]) != 1 || checkRelayURL(q.Get("relayUrl")) != nil || !validStorageID(q.Get(field)) {
			return LaunchIntent{}, fail(400, "invalid_launch", "Invalid server connection link. Use a fresh link from the website.")
		}
		return LaunchIntent{ID: digest(raw), Kind: u.Host, RelayURL: q.Get("relayUrl"), Code: q.Get("code"), ConnectionID: q.Get("connectionId")}, nil
	}
	if u.Host != "connect" {
		return LaunchIntent{}, fail(400, "invalid_launch", "Unknown WebDeviceCable link.")
	}
	if err != nil || len(q) != 3 || len(q["requestId"]) != 1 || len(q["origin"]) != 1 || len(q["publicKey"]) != 1 {
		return LaunchIntent{}, fail(400, "invalid_launch", "Invalid connection link fields.")
	}
	in := LaunchIntent{ID: q.Get("requestId"), Origin: q.Get("origin"), PublicKey: q.Get("publicKey")}
	if !validStorageID(in.ID) || !exactOrigin(in.Origin) {
		return in, fail(400, "invalid_launch", "Use an HTTPS website or localhost origin.")
	}
	if _, err = parsePublicKey(in.PublicKey); err != nil {
		return in, err
	}
	return in, nil
}
func (a *App) launchRoutes(mux *http.ServeMux, wrap func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc, endpoint string) {
	mux.HandleFunc("POST /api/launch-intents", wrap(func(w http.ResponseWriter, r *http.Request) error {
		if r.Header.Get("Origin") != "" || !equal(bearer(r), a.adminKey) {
			return fail(403, "launch_denied", "Only the installed application can launch a connection.")
		}
		var in struct {
			URI string `json:"uri"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		intent, err := ParseLaunchURI(in.URI)
		if err != nil {
			return err
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.launchIntents == nil {
			a.launchIntents = map[string]*LaunchIntent{}
		}
		for id, item := range a.launchIntents {
			if !a.now().Before(item.Expires) {
				delete(a.launchIntents, id)
			}
		}
		if _, exists := a.launchIntents[intent.ID]; exists {
			if intent.Kind == "" {
				return fail(409, "launch_consumed", "This launch link was already opened.")
			}
			// Reopening a relay link only displays its invitation/settings again.
		}
		if len(a.launchIntents) >= 32 {
			return fail(429, "launch_limit", "Too many connection requests.")
		}
		intent.Expires = a.now().Add(5 * time.Minute)
		intent.Used = map[string]bool{}
		a.launchIntents[intent.ID] = &intent
		a.notifyConnections()
		writeJSON(w, 201, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("POST /api/launch-intents/{id}/dismiss", control(func(w http.ResponseWriter, r *http.Request) error {
		if intent := a.launchIntents[r.PathValue("id")]; intent != nil {
			intent.Handled = true
			a.notifyConnections()
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("POST /v2/connect", wrap(func(w http.ResponseWriter, r *http.Request) error {
		var signed signedMessage
		if err := decodeSecure(w, r, &signed); err != nil {
			return err
		}
		var in struct {
			onboarding.Options
			ID        string `json:"requestId"`
			Origin    string `json:"origin"`
			PublicKey string `json:"publicKey"`
			Nonce     string `json:"nonce"`
			Timestamp int64  `json:"timestamp"`
			AppID     string `json:"appId"`
			Name      string `json:"name"`
			AppUser   string `json:"appUser"`
			Ephemeral string `json:"ephemeralKey"`
		}
		preliminary, err := b64.DecodeString(signed.Payload)
		if err != nil || json.Unmarshal(preliminary, &in) != nil {
			return fail(400, "invalid_launch", "Invalid launch request.")
		}
		if _, err = verifyBytes(in.PublicKey, signed); err != nil {
			return fail(403, "signature_required", "The requesting key must sign its connection request.")
		}
		decoder := json.NewDecoder(bytes.NewReader(preliminary))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
			return fail(400, "invalid_launch", "Unknown launch request field.")
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		intent := a.launchIntents[in.ID]
		if intent == nil || intent.Kind != "" || !a.now().Before(intent.Expires) || intent.Origin != r.Header.Get("Origin") || in.Origin != intent.Origin || in.PublicKey != intent.PublicKey {
			return fail(403, "launch_required", "Open the installed WebDeviceCable application from this website.")
		}
		if !validStorageID(in.Nonce) || intent.Used[in.Nonce] || len(intent.Used) >= 512 || a.now().Sub(time.UnixMilli(in.Timestamp)).Abs() > time.Minute {
			return fail(409, "request_replay", "Invalid, stale or repeated launch request.")
		}
		intent.Used[in.Nonce] = true
		if err = validatePairing(Pairing{AppID: in.AppID, Name: in.Name, AppUser: in.AppUser, Origin: in.Origin, Mode: "restricted"}); err != nil {
			return fail(400, "invalid_pairing", err.Error())
		}
		if err := in.Options.Validate(in.Origin); err != nil {
			return fail(400, "invalid_logo", err.Error())
		}
		request := a.pairingRequests[in.ID]
		if request == nil {
			request = &pairingRequest{Options: in.Options, ID: in.ID, AppID: in.AppID, Name: in.Name, AppUser: in.AppUser, Origin: in.Origin, PublicKey: in.PublicKey, Status: "pending", ExpiresAt: intent.Expires}
			a.pairingRequests[in.ID] = request
			a.notifyConnections()
		}
		if request.Options != in.Options || request.AppID != in.AppID || request.AppUser != in.AppUser || request.Name != in.Name || request.PublicKey != in.PublicKey {
			return fail(409, "identity_mismatch", "The connection identity changed.")
		}
		raw, _ := b64.DecodeString(in.Ephemeral)
		peer, err := ecdh.P256().NewPublicKey(raw)
		if err != nil {
			return fail(400, "invalid_key", "Invalid ephemeral key.")
		}
		private, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		shared, err := private.ECDH(peer)
		if err != nil {
			return err
		}
		salt := make([]byte, 32)
		if _, err = rand.Read(salt); err != nil {
			return err
		}
		cipher, err := channelCipher(shared, salt, in.ID, "launch")
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{"status": request.Status, "invitation": request.Invitation, "connectionReviewVersion": 1})
		encrypted, err := sealMessage(cipher, in.ID, "launch", body)
		if err != nil {
			return err
		}
		response, _ := json.Marshal(map[string]any{"requestId": in.ID, "nonce": in.Nonce, "origin": in.Origin, "clientPublicKey": in.PublicKey, "clientEphemeralKey": in.Ephemeral, "publicKey": publicKey(a.identity), "deviceId": a.state.DeviceID, "endpoint": endpoint, "serverPublicKey": b64.EncodeToString(private.PublicKey().Bytes()), "salt": b64.EncodeToString(salt), "encrypted": encrypted})
		out, err := signBytes(a.identity, response)
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	}))
}

func (a *App) pendingLaunchIntents() []LaunchIntent {
	result := []LaunchIntent{}
	for _, intent := range a.launchIntents {
		if !intent.Handled && a.now().Before(intent.Expires) && (intent.Kind != "" || a.pairingRequests[intent.ID] == nil) {
			result = append(result, *intent)
		}
	}
	return result
}
