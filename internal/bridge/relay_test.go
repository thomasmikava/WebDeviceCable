package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/relay"
)

func TestDirectAndBackendLeaseGraceAreIndependent(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	origin := "https://lease.example"
	direct, token := f.pair(t, "direct", origin, "owner", false)
	relayed, relayToken := f.pair(t, "backend", origin, "owner", false)
	f.a.state.Pairings[relayed].Transport = "relay"
	directLease := f.lease(t, origin, token)
	relayLease := f.lease(t, origin, relayToken)
	directJob := f.job(t, origin, token, directLease, "direct-grace", nil)
	relayJob := f.job(t, origin, relayToken, relayLease, "backend-grace", nil)
	f.advance(t, 13*time.Second)
	if findJob(&f.a.state, directJob).Status != "cancelled" || findJob(&f.a.state, relayJob).Status != "awaiting_approval" {
		t.Fatal("transport lease grace crossed accounts")
	}
	if f.a.state.Pairings[direct].Transport != "" {
		t.Fatal("direct pairing transport changed")
	}
	f.advance(t, 33*time.Second)
	if findJob(&f.a.state, relayJob).Status != "cancelled" {
		t.Fatal("backend lease did not expire")
	}
}

func TestOutboundRelayUsesExistingSecureDispatcher(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	f.a.now = time.Now
	session := f.a.sessions[f.sessionID]
	session.Expires = time.Now().Add(time.Hour)
	f.a.sessions[f.sessionID] = session
	key := strings.Repeat("k", 64)
	server, e := relay.Open(relay.Config{Directory: t.TempDir(), AdminKey: key})
	if e != nil {
		t.Fatal(e)
	}
	remote := httptest.NewServer(server.Handler())
	defer remote.Close()
	defer server.Close()
	request := func(method, path string, body any, account string) (int, []byte) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, remote.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-WDC-Account", account)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, data
	}
	status, b := request("POST", "/relay/admin/v1/projects", map[string]string{"appId": "kings-international", "name": "Kings", "origin": "https://kings.example"}, "")
	if status != 201 {
		t.Fatalf("register project %d %s", status, b)
	}
	var project struct{ APIKey string }
	if e = json.Unmarshal(b, &project); e != nil || project.APIKey == "" {
		t.Fatalf("project credential missing: %v", e)
	}
	key = project.APIKey
	status, b = request("POST", "/relay/v1/enrollments", map[string]any{"appUser": "admin-1", "logoURL": "https://kings.example/logo.svg", "requestedPermissions": map[string]bool{"automaticApprovals": true}}, "")
	if status != 201 {
		t.Fatalf("enroll %d %s", status, b)
	}
	var enrollment struct{ Code, ConnectionID string }
	if e = json.Unmarshal(b, &enrollment); e != nil {
		t.Fatal(e)
	}
	inspect := object(t, f.rawReq("POST", "/api/relay/inspect", f.origin, "", "", map[string]string{"url": remote.URL, "code": enrollment.Code}, true), 200)
	if inspect["logoURL"] != "https://kings.example/logo.svg" || inspect["requestedPermissions"].(map[string]any)["automaticApprovals"] != true {
		t.Fatal("relay suggestions lost", inspect)
	}
	tampered := map[string]any{}
	for k, v := range inspect {
		tampered[k] = v
	}
	tampered["logoURL"] = "https://kings.example/changed.svg"
	object(t, f.rawReq("POST", "/api/relay/connect", f.origin, "", "", map[string]any{"url": remote.URL, "code": enrollment.Code, "expected": tampered}, true), 409)
	logoID, err := f.a.storeLogo([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0h24v24z"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	object(t, f.rawReq("POST", "/api/relay/connect", f.origin, "", "", map[string]any{"url": remote.URL, "code": enrollment.Code, "expected": inspect, "automatic": false, "logoId": logoID}, true), 201)
	path := "/relay/v1/connections/" + enrollment.ConnectionID
	for end := time.Now().Add(time.Second); ; {
		_, b = request("GET", path, nil, "admin-1")
		var state struct{ Connected bool }
		_ = json.Unmarshal(b, &state)
		if state.Connected {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("connector offline: %s", b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	rpc := func(method, resource, body, lease, account string) (int, []byte) {
		return request("POST", path+"/request", map[string]any{"requestId": secret(), "deadline": time.Now().Add(45 * time.Second).UnixMilli(), "method": method, "path": resource, "body": body, "lease": lease}, account)
	}
	status, b = rpc("GET", "/v2/capabilities", "", "", "admin-1")
	if status != 200 {
		t.Fatalf("capabilities %d %s", status, b)
	}
	var caps struct{ Pairing Pairing }
	if e = json.Unmarshal(b, &caps); e != nil {
		t.Fatal(e)
	}
	if caps.Pairing.Automatic || caps.Pairing.LogoID != logoID {
		t.Fatal("owner override was not honored", caps.Pairing)
	}
	if caps.Pairing.AppUser != "admin-1" || caps.Pairing.Relay != nil || caps.Pairing.Transport != "relay" {
		t.Fatalf("invalid/redaction: %s", b)
	}
	if status, _ = rpc("GET", "/v2/capabilities", "", "", "admin-2"); status != 403 {
		t.Fatal("cross-account access")
	}
	if status, _ = rpc("POST", "/api/shutdown", "", "", "admin-1"); status != 403 {
		t.Fatal("control tunnel")
	}
	f.a.mu.Lock()
	_ = f.a.update(func(s *State) error { s.Pairings[caps.Pairing.ID].Automatic = true; return nil })
	f.a.mu.Unlock()
	status, b = rpc("POST", "/v2/lease", "", "", "admin-1")
	if status != 201 {
		t.Fatalf("lease %d %s", status, b)
	}
	var lease struct {
		Lease     string
		ExpiresAt time.Time
	}
	_ = json.Unmarshal(b, &lease)
	if time.Until(lease.ExpiresAt) < 40*time.Second {
		t.Fatal("relay must have backend lease")
	}
	status, b = rpc("POST", "/v2/jobs", `{"kind":"connection-test","idempotencyKey":"relay-test-operation"}`, lease.Lease, "admin-1")
	if status != 202 {
		t.Fatalf("job %d %s", status, b)
	}
	var job Job
	_ = json.Unmarshal(b, &job)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_ = f.a.tick()
			}
		}
	}()
	status, b = rpc("GET", fmt.Sprintf("/v2/jobs/%s/events", job.ID), "", lease.Lease, "admin-1")
	if status != 200 || !strings.Contains(string(b), `"status":"completed"`) {
		t.Fatalf("verified stream %d %s", status, b)
	}
	status, b = rpc("POST", "/v2/jobs", `{"kind":"connection-test","idempotencyKey":"relay-test-operation"}`, lease.Lease, "admin-1")
	if status != 200 {
		t.Fatalf("duplicate must not execute: %d %s", status, b)
	}
	if _, e = inspectRelay(context.Background(), relayInput{URL: remote.URL, Code: enrollment.Code}); e == nil {
		t.Fatal("pairing code replay")
	}
	status, _ = request("DELETE", path, nil, "admin-1")
	if status != 200 {
		t.Fatal("revoke failed")
	}
	if status, _ = rpc("GET", "/v2/capabilities", "", "", "admin-1"); status != 403 {
		t.Fatal("revoked access")
	}
}
