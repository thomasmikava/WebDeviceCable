package bridge

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func manualInvitation(t *testing.T, f *fixture, user string) (string, string) {
	t.Helper()
	result := object(t, f.req("POST", "/api/invitations", f.origin, "", "", map[string]any{"appId": "writer", "name": "Writer", "origin": "https://writer.example", "appUser": user}, true), 201)
	return result["pairing"].(map[string]any)["id"].(string), result["invitation"].(string)
}

func invitationList(t *testing.T, f *fixture) []any {
	t.Helper()
	return object(t, f.req("GET", "/api/invitations", f.origin, "", "", nil, true), 200)["invitations"].([]any)
}

func TestInvitationManagementRequiresSessionAndCSRF(t *testing.T) {
	f := setup(t)
	id, _ := manualInvitation(t, f, "alice")
	for _, method := range []string{"GET", "DELETE"} {
		path := "/api/invitations"
		if method == "DELETE" {
			path += "/" + id
		}
		for _, tc := range []struct {
			name, session, csrf, origin, bearer string
			status                              int
		}{
			{"anonymous", "", "", f.origin, "", 401},
			{"root key is not a panel session", "", "", f.origin, f.a.adminKey, 401},
			{"expired session", "unknown", f.csrf, f.origin, "", 401},
			{"missing csrf", f.sessionID, "", f.origin, "", 403},
			{"wrong csrf", f.sessionID, secret(), f.origin, "", 403},
			{"wrong origin", f.sessionID, f.csrf, "https://writer.example", "", 403},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				r := httptest.NewRequest(method, f.origin+path, nil)
				r.RemoteAddr = "127.0.0.1:12345"
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("X-Bridge-Session", tc.session)
				r.Header.Set("X-Bridge-CSRF", tc.csrf)
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
				w := httptest.NewRecorder()
				f.handler.ServeHTTP(w, r)
				object(t, w, tc.status)
			})
		}
	}
	object(t, f.req("DELETE", "/api/invitations/"+id, "", "", "", nil, true), 403)
	if len(invitationList(t, f)) != 1 {
		t.Fatal("rejected request changed invitations")
	}
	// GET works without a browser-supplied Origin, still requiring both credentials.
	object(t, f.req("GET", "/api/invitations", "", "", "", nil, true), 200)
	f.now = f.now.Add(13 * time.Hour)
	object(t, f.req("GET", "/api/invitations", f.origin, "", "", nil, true), 401)
}

func TestInvitationListAndRevokeAcrossSessions(t *testing.T) {
	f := setup(t)
	before := f.a.connectionRevision
	id, token := manualInvitation(t, f, "alice")
	otherID, otherToken := manualInvitation(t, f, "bob")
	if f.a.connectionRevision <= before {
		t.Fatal("creation did not notify panels")
	}
	// Another authenticated panel sees identical server-owned metadata.
	session := object(t, f.req("POST", "/api/session", f.origin, f.a.adminKey, "", nil, false), 200)
	f.sessionID = session["session"].(string)
	f.csrf = session["csrf"].(string)
	list := invitationList(t, f)
	if len(list) != 2 {
		t.Fatalf("pending list: %v", list)
	}
	data, _ := json.Marshal(list)
	for _, forbidden := range []string{token, otherToken, "secretHash", "invitation", "clientPublicKey", "credentialHash"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("list leaked %s", forbidden)
		}
	}
	for hash := range f.a.state.Invitations {
		if strings.Contains(string(data), hash) {
			t.Fatal("list leaked verifier")
		}
	}
	found := false
	for _, item := range list {
		entry := item.(map[string]any)
		if entry["id"] == id {
			p := entry["pairing"].(map[string]any)
			found = entry["manual"] == true && p["appUser"] == "alice" && p["origin"] == "https://writer.example" && entry["expiresAt"] != nil
		}
	}
	if !found {
		t.Fatal("missing management identity")
	}
	before = f.a.connectionRevision
	object(t, f.req("DELETE", "/api/invitations/"+id, f.origin, "", "", nil, true), 200)
	object(t, f.req("DELETE", "/api/invitations/"+id, f.origin, "", "", nil, true), 200)
	if f.a.connectionRevision <= before || len(invitationList(t, f)) != 1 {
		t.Fatal("cancellation not visible to other panels")
	}
	// Revocation invalidates the copied credential, without touching a different invitation.
	object(t, f.req("POST", "/v2/pairings/claim", "https://writer.example", token, "", map[string]string{"appId": "writer", "appUser": "alice"}, false), 401)
	object(t, f.req("POST", "/v2/pairings/claim", "https://writer.example", otherToken, "", map[string]string{"appId": "writer", "appUser": "bob"}, false), 201)
	object(t, f.req("DELETE", "/api/invitations/"+otherID, f.origin, "", "", nil, true), 409)
	if f.a.state.Pairings[otherID].Revoked || len(invitationList(t, f)) != 0 {
		t.Fatal("cancellation changed an already claimed connection")
	}
	saved, err := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted State
	if err = json.Unmarshal(saved, &persisted); err != nil || len(persisted.Invitations) != 0 {
		t.Fatal("revocation was not persisted")
	}
}

func TestInvitationExpiryAndRevocationStorageFailure(t *testing.T) {
	f := setup(t)
	id, _ := manualInvitation(t, f, "alice")
	path := filepath.Join(f.a.dir, "state.json")
	if err := os.Rename(path, path+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	object(t, f.req("DELETE", "/api/invitations/"+id, f.origin, "", "", nil, true), 500)
	if len(invitationList(t, f)) != 1 {
		t.Fatal("failed save published revocation")
	}
	f.now = f.now.Add(5 * time.Minute)
	if len(invitationList(t, f)) != 0 {
		t.Fatal("expired invitation is pending")
	}
}

func TestRevokeApprovedInvitationClearsRendezvous(t *testing.T) {
	f := setup(t)
	id := secret()
	object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", map[string]string{"id": id, "appId": "writer", "name": "Writer", "origin": "https://writer.example", "appUser": "alice"}, true), 201)
	object(t, f.req("POST", "/api/pairing-requests/"+id+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	list := invitationList(t, f)
	entry := list[0].(map[string]any)
	if entry["manual"] != false {
		t.Fatal("approved handoff listed as a manual invitation")
	}
	object(t, f.req("DELETE", "/api/invitations/"+entry["id"].(string), f.origin, "", "", nil, true), 200)
	request := object(t, f.req("GET", "/api/pairing-requests/"+id, f.origin, "", "", nil, true), 200)
	if request["invitation"] != nil || request["status"] != "rejected" {
		t.Fatal("revoked rendezvous still returns a token")
	}
}

func TestOnlyRuntimeAssetsEmbeddedAndServed(t *testing.T) {
	err := fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(path, "test") || strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".d.ts") {
			t.Errorf("development file embedded: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t)
	for _, path := range []string{"/_tests/", "/_tests/_app.test.mjs", "/_tests/README.md", "/scripts/panel.test.mjs"} {
		if w := f.req(http.MethodGet, path, "", "", "", nil, false); w.Code != 404 {
			t.Fatalf("served test path %s: %d", path, w.Code)
		}
	}
}
