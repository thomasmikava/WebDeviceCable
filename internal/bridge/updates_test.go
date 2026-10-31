package bridge

import (
	"bytes"
	"encoding/json"
	"github.com/thomasmikava/WebDeviceCable/internal/updates"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateAdmissionWaitsAndRequiresOwnerCancellation(t *testing.T) {
	f := setup(t)
	origin := "https://writer.example"
	_, token := f.pair(t, "writer", origin, "alice", false)
	lease := f.lease(t, origin, token)
	id := f.job(t, origin, token, lease, "pending-update", nil)
	before, _ := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	idle, e := f.a.beginUpdate(false)
	if e != nil || idle || f.a.updating {
		t.Fatal("automatic update interrupted a pending request")
	}
	after, _ := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("automatic update modified private state")
	}
	idle, e = f.a.beginUpdate(true)
	if e != nil || !idle || !f.a.updating {
		t.Fatal("explicit update did not drain", e)
	}
	if j := findJob(&f.a.state, id); j.Status != "cancelled" {
		t.Fatal("explicit owner cancellation not applied")
	}
	out := object(t, f.req("POST", "/v2/jobs", origin, token, lease, map[string]string{"kind": "connection-test", "idempotencyKey": "during-update"}, false), 503)
	fail := out["error"].(map[string]any)
	if fail["code"] != "bridge_updating" || fail["retryable"] != true {
		t.Fatal("maintenance error was not signed and retryable")
	}
}
func TestUpdateWaitsForIdleConversationsAndStagedFiles(t *testing.T) {
	f := setup(t)
	id := secret()
	f.a.state.Conversations[id] = &Conversation{ID: id, Status: "open"}
	if idle, _ := f.a.beginUpdate(false); idle {
		t.Fatal("ended idle conversation")
	}
	delete(f.a.state.Conversations, id)
	lease := secret()
	f.a.leases[lease] = Lease{ExpiresAt: f.now.Add(time.Minute)}
	f.a.inputs[id] = &nativeInput{ID: id, LeaseID: lease}
	if idle, _ := f.a.beginUpdate(false); idle {
		t.Fatal("discarded staged input")
	}
	f.a.leases[lease] = Lease{ExpiresAt: f.now.Add(-time.Minute)}
	if idle, e := f.a.beginUpdate(false); !idle || e != nil {
		t.Fatal("expired input blocked update", e)
	}
}
func TestUpdateControlEndpointsAreOwnerOnly(t *testing.T) {
	f := setup(t)
	if e := f.a.ConfigureUpdates(updates.Build{Version: "dev"}, func(updates.Plan) error { return nil }); e != nil {
		t.Fatal(e)
	}
	object(t, f.rawReq("GET", "/api/updates", f.origin, "", "", nil, false), 401)
	object(t, f.rawReq("POST", "/api/internal/updates", f.origin, f.a.adminKey, "", map[string]string{"action": "status"}, false), 403)
	object(t, f.rawReq("POST", "/api/internal/updates", "", f.a.adminKey, "", map[string]string{"action": "status"}, false), 200)
	object(t, f.rawReq("PATCH", "/api/updates", f.origin, "", "", map[string]bool{"automatic": true}, true), 409)
	object(t, f.rawReq("POST", "/api/updates/check", "https://evil.example", "", "", map[string]any{}, true), 403)
}
func TestPanelUpdateHandoffIsShortLivedAndSingleUse(t *testing.T) {
	dir := t.TempDir()
	a, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	id, csrf := secret(), secret()
	a.sessions[id] = session{CSRF: csrf, Expires: time.Now().Add(time.Hour)}
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if idle, e := a.beginUpdate(false); !idle || e != nil {
		t.Fatal(e)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if b.sessions[id].CSRF != csrf {
		t.Fatal("lost authenticated panel across update")
	}
	if _, e = os.Stat(filepath.Join(dir, "panel-resume.json")); !os.IsNotExist(e) {
		t.Fatal("handoff not consumed")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("updated identities or grants during handoff")
	}
	var state State
	_ = json.Unmarshal(after, &state)
	if state.IdentityKey == "" {
		t.Fatal("missing identity")
	}
}
