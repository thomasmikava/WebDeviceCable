package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApprovedConnectionRemainsVisibleUntilPanelHasPairing(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	id, origin := secret(), "https://handoff.example"
	logo, err := f.a.storeLogo([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0h8v8z"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", map[string]string{"id": id, "appId": "handoff", "name": "Handoff app", "origin": origin, "appUser": "alice"}, true), 201)
	initial := object(t, f.req("GET", "/api/connection-events", f.origin, "", "", nil, true), 200)
	waiting := connectionPoll(f, initial["revision"].(float64))
	waitConnectionPanels(t, f, 1)
	path := "/api/pairing-requests/" + id
	object(t, f.req("POST", path+"/decision", f.origin, "", "", map[string]any{"approve": true, "logoId": logo}, true), 200)
	approved := pollResult(t, waiting, 200)
	requests := approved["pairingRequests"].([]any)
	if len(requests) != 1 {
		t.Fatal("approved card disappeared before claim")
	}
	request := requests[0].(map[string]any)
	pairID, _ := request["pairingId"].(string)
	if pairID == "" || request["status"] != "approved" || request["logoId"] != logo || request["invitation"] != nil {
		t.Fatal("handoff must retain identity and logo without exposing its invitation")
	}
	state := object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
	if len(state["pairings"].([]any)) != 0 || len(state["pairingRequests"].([]any)) != 1 {
		t.Fatal("claim delay left a blank state")
	}
	private := object(t, f.req("GET", path, f.origin, "", "", nil, true), 200)
	object(t, f.req("POST", "/v2/pairings/claim", origin, private["invitation"].(string), "", map[string]string{"appId": "handoff", "appUser": "alice"}, false), 201)
	// The event can beat the panel's GET /state. It must still carry the card;
	// the panel removes it only when the matching paired app is available.
	after := object(t, f.req("GET", "/api/connection-events", f.origin, "", "", nil, true), 200)
	if after["pairingRequests"].([]any)[0].(map[string]any)["pairingId"] != pairID {
		t.Fatal("claim notification removed the handoff too early")
	}
	state = object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
	if state["pairings"].([]any)[0].(map[string]any)["id"] != pairID {
		t.Fatal("card cannot be matched to its paired app")
	}
	object(t, f.req("POST", path+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	if len(f.a.state.Invitations) != 0 {
		t.Fatal("repeated approval created another invitation")
	}
	f.advance(t, 6*time.Minute)
	if len(object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)["pairingRequests"].([]any)) != 0 {
		t.Fatal("handoff retained beyond its bounded lifetime")
	}
}

func TestConnectionDisplayNameIsLocalAndPreservesAccess(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	origin := "https://appearance.example"
	id, token := f.pair(t, "Original app", origin, "alice", false)
	other, _ := f.pair(t, "Original app", origin, "bob", false)
	lease := f.lease(t, origin, token)
	jobID := f.job(t, origin, token, lease, "rename-during-work", nil)
	before := *f.a.state.Pairings[id]
	logo, err := f.a.storeLogo([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0h8v8z"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/pairings/" + id
	object(t, f.req("PATCH", path, f.origin, "", "", map[string]string{"displayName": "Personal workspace", "logoId": logo}, false), 401)
	object(t, f.req("PATCH", path, f.origin, "", "", map[string]string{"displayName": "Personal workspace", "logoId": logo}, true), 200)
	p := f.a.state.Pairings[id]
	if p.Name != before.Name || p.AppID != before.AppID || p.AppUser != before.AppUser || p.ClientPublicKey != before.ClientPublicKey || p.Automatic != before.Automatic || p.Mode != before.Mode || p.DisplayName != "Personal workspace" || p.LogoID != logo || f.a.state.Pairings[other].DisplayName != "" {
		t.Fatal("appearance edit changed identity, access or another connection")
	}
	if findJob(&f.a.state, jobID).Status != "awaiting_approval" || f.a.leases[lease].PairingID != id {
		t.Fatal("rename interrupted existing work")
	}
	remote := object(t, f.req("GET", "/v2/capabilities", origin, token, "", nil, false), 200)["pairing"].(map[string]any)
	if remote["name"] != "Original app" || remote["displayName"] != nil {
		t.Fatal("local alias leaked to the app")
	}
	for _, invalid := range []string{strings.Repeat("x", 101), "hidden\x00name", "two\nlines"} {
		object(t, f.req("PATCH", path, f.origin, "", "", map[string]string{"displayName": invalid, "logoId": ""}, true), 400)
		if f.a.state.Pairings[id].LogoID != logo || f.a.state.Pairings[id].DisplayName != "Personal workspace" {
			t.Fatal("invalid edit partially changed appearance")
		}
	}
	raw, err := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved State
	if json.Unmarshal(raw, &saved) != nil || saved.Pairings[id].DisplayName != "Personal workspace" {
		t.Fatal("local alias not saved")
	}
	object(t, f.req("PATCH", path, f.origin, "", "", map[string]string{"displayName": ""}, true), 200)
	if f.a.state.Pairings[id].DisplayName != "" || f.a.state.Pairings[id].Name != "Original app" {
		t.Fatal("reset did not restore app-supplied name")
	}
}

func TestRelayApprovalIsVisibleDuringClaimAndClearsOnFailure(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "confirmed", false: "failed"}[success], func(t *testing.T) {
			f := setupConfig(t, "bridge:\n  disableNative: true\n")
			connectionID := secret()
			info := map[string]string{"appId": "relay-handoff", "name": "Relay app", "origin": "https://relay-app.example", "appUser": "alice", "connectionId": connectionID}
			started, release := make(chan struct{}), make(chan struct{})
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/connector/v1/inspect":
					writeJSON(w, 200, info)
				case "/connector/v1/claim":
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					if success {
						writeJSON(w, 200, map[string]string{"connectionId": connectionID})
					} else {
						writeJSON(w, 503, map[string]string{"error": "offline"})
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer remote.Close()
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				finished <- f.req("POST", "/api/relay/connect", f.origin, "", "", map[string]any{"url": remote.URL, "code": secret(), "expected": info}, true)
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("claim never started")
			}
			snapshot := object(t, f.req("GET", "/api/connection-events", f.origin, "", "", nil, true), 200)
			requests := snapshot["pairingRequests"].([]any)
			if len(requests) != 1 || requests[0].(map[string]any)["status"] != "approved" || requests[0].(map[string]any)["connectionId"] != connectionID {
				close(release)
				t.Fatal("relay approval vanished during network wait")
			}
			close(release)
			status := 201
			if !success {
				status = 502
			}
			pollResult(t, finished, status)
			snapshot = object(t, f.req("GET", "/api/connection-events", f.origin, "", "", nil, true), 200)
			want := 1
			if !success {
				want = 0
			}
			if len(snapshot["pairingRequests"].([]any)) != want {
				t.Fatal("relay handoff cleanup did not match its outcome")
			}
		})
	}
}
