package bridge

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitConnectionPanels(t *testing.T, f *fixture, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.a.mu.Lock()
		current := f.a.connectionWaiters
		f.a.mu.Unlock()
		if current == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("connection notification did not reach its waiting state")
}
func connectionPoll(f *fixture, revision float64) <-chan *httptest.ResponseRecorder {
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- f.req("GET", "/api/connection-events?after="+strconv.FormatUint(uint64(revision), 10), f.origin, "", "", nil, true)
	}()
	return result
}
func pollResult(t *testing.T, result <-chan *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	select {
	case response := <-result:
		return object(t, response, status)
	case <-time.After(time.Second):
		t.Fatal("connection notification was not immediate")
		return nil
	}
}
func TestConnectionEventsWakeAllPanelsAndShowLaunchBeforeMetadata(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	object(t, f.req("GET", "/api/connection-events", f.origin, "", "", nil, false), 401)
	object(t, f.req("GET", "/api/connection-events", "https://other.example", "", "", nil, true), 403)
	object(t, f.req("GET", "/api/connection-events?after=invalid", f.origin, "", "", nil, true), 400)
	initial := object(t, f.req("GET", "/api/connection-events", f.origin, "", "", nil, true), 200)
	first, second := connectionPoll(f, initial["revision"].(float64)), connectionPoll(f, initial["revision"].(float64))
	waitConnectionPanels(t, f, 2)
	// A waiting panel must not block ordinary owner requests or request admission.
	object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
	id := secret()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	origin := "https://new-app.example"
	uri := "webdevicecable://connect?" + url.Values{"requestId": {id}, "origin": {origin}, "publicKey": {publicKey(key)}}.Encode()
	object(t, f.rawReq("POST", "/api/launch-intents", "", f.a.adminKey, "", map[string]string{"uri": uri}, false), 201)
	snapshot := pollResult(t, first, 200)
	other := pollResult(t, second, 200)
	if snapshot["revision"] != other["revision"] || len(snapshot["pairingRequests"].([]any)) != 0 {
		t.Fatal("panels missed the same launch", snapshot, other)
	}
	launches := snapshot["launches"].([]any)
	if len(launches) != 1 || launches[0].(map[string]any)["requestId"] != id || launches[0].(map[string]any)["origin"] != origin {
		t.Fatal("no immediate loading placeholder", snapshot)
	}
	// Metadata replaces this placeholder without duplicate requests or unsigned acceptance.
	pending := connectionPoll(f, snapshot["revision"].(float64))
	waitConnectionPanels(t, f, 1)
	object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", map[string]any{"id": id, "appId": "new-app", "name": "New App", "origin": origin, "appUser": "alice", "requestedPermissions": map[string]bool{"automaticApprovals": true}}, true), 201)
	snapshot = pollResult(t, pending, 200)
	if len(snapshot["launches"].([]any)) != 0 || len(snapshot["pairingRequests"].([]any)) != 1 {
		t.Fatal("loading and ready cards duplicated", snapshot)
	}
	request := snapshot["pairingRequests"].([]any)[0].(map[string]any)
	if request["name"] != "New App" || request["invitation"] != nil {
		t.Fatal("wrong identity or invitation exposure", request)
	}
	pending = connectionPoll(f, snapshot["revision"].(float64))
	waitConnectionPanels(t, f, 1)
	object(t, f.req("POST", "/api/pairing-requests/"+id+"/decision", f.origin, "", "", map[string]bool{"approve": false}, true), 200)
	snapshot = pollResult(t, pending, 200)
	if len(snapshot["pairingRequests"].([]any)) != 0 || len(snapshot["launches"].([]any)) != 0 {
		t.Fatal("declined request remained visible", snapshot)
	}
}
func TestConnectionEventsEndOnLockAndBoundWaiters(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	initial := object(t, f.req("GET", "/api/connection-events", f.origin, "", "", nil, true), 200)
	f.a.mu.Lock()
	f.a.connectionWaiters = 32
	f.a.mu.Unlock()
	object(t, f.req("GET", "/api/connection-events?after=0", f.origin, "", "", nil, true), 429)
	f.a.mu.Lock()
	f.a.connectionWaiters = 0
	f.a.mu.Unlock()
	pending := connectionPoll(f, initial["revision"].(float64))
	waitConnectionPanels(t, f, 1)
	object(t, f.req("DELETE", "/api/session", f.origin, "", "", nil, true), 200)
	response := pollResult(t, pending, 401)
	encoded, _ := json.Marshal(response)
	if len(encoded) > 1024 {
		t.Fatal("locked panel received connection data")
	}
	waitConnectionPanels(t, f, 0)
}

func TestConnectionEventsCancellationReleasesWaiter(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("GET", f.origin+"/api/connection-events?after=0", nil).WithContext(ctx)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Origin", f.origin)
	request.Header.Set("X-Bridge-Session", f.sessionID)
	finished := make(chan struct{})
	go func() { f.handler.ServeHTTP(httptest.NewRecorder(), request); close(finished) }()
	waitConnectionPanels(t, f, 1)
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("closed panel retained its notification request")
	}
	waitConnectionPanels(t, f, 0)
	object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
}

func TestSlowRelayInspectionDoesNotDelayNewConnections(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeJSON(w, 200, map[string]string{"appId": "slow-server", "name": "Slow Server", "origin": "https://slow.example", "appUser": "alice", "connectionId": strings.Repeat("e", 64)})
	}))
	defer func() { once.Do(func() { close(release) }); remote.Close() }()
	inspection := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		inspection <- f.req("POST", "/api/relay/inspect", f.origin, "", "", map[string]string{"url": remote.URL, "code": secret()}, true)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("relay inspection did not start")
	}
	pending := connectionPoll(f, 0)
	waitConnectionPanels(t, f, 1)
	admitted := make(chan *httptest.ResponseRecorder, 1)
	id := secret()
	go func() {
		admitted <- f.req("POST", "/api/pairing-requests", f.origin, "", "", map[string]string{"id": id, "appId": "instant", "name": "Instant App", "origin": "https://instant.example", "appUser": "alice"}, true)
	}()
	pollResult(t, admitted, 201)
	snapshot := pollResult(t, pending, 200)
	if len(snapshot["pairingRequests"].([]any)) != 1 {
		t.Fatal("new request missing while relay was blocked", snapshot)
	}
	once.Do(func() { close(release) })
	pollResult(t, inspection, 200)
}
