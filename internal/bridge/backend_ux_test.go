package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const routingRequiredMessage = "Choose a default program for this app in WebDeviceCable, under Connections."

func TestLocalhostNavigationRedirect(t *testing.T) {
	f := setup(t)
	const canonical = "http://127.0.0.1:47441"
	handler := f.a.Handler("127.0.0.1:47441", nil)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/", "/connections?tab=apps&name=a%20b", "/nested/a%2Fb?next=%2Fapi%2Fstate", "/?", "//evil.example/path?x=1"} {
			t.Run(method+path, func(t *testing.T) {
				r := httptest.NewRequest(method, "http://localhost:47441"+path, nil)
				r.RemoteAddr = "127.0.0.1:12345"
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != canonical+path {
					t.Fatalf("redirect: %d %q", w.Code, w.Header().Get("Location"))
				}
				if method == http.MethodHead && w.Body.Len() != 0 {
					t.Fatal("HEAD returned a body")
				}
				if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("redirect changed cache or origin policy")
				}
			})
		}
	}
	for _, tc := range []struct{ method, host, path, remote string }{
		{"GET", "localhost", "/", "127.0.0.1:12345"},
		{"GET", "localhost:47321", "/", "127.0.0.1:12345"},
		{"GET", "localhost:047441", "/", "127.0.0.1:12345"},
		{"GET", "LOCALHOST:47441", "/", "127.0.0.1:12345"},
		{"GET", "localhost.:47441", "/", "127.0.0.1:12345"},
		{"GET", "localhost.evil.example:47441", "/", "127.0.0.1:12345"},
		{"GET", "evil.example:47441", "/", "127.0.0.1:12345"},
		{"GET", "127.0.0.2:47441", "/", "127.0.0.1:12345"},
		{"GET", "[::1]:47441", "/", "127.0.0.1:12345"},
		{"GET", "localhost:47441", "/api", "127.0.0.1:12345"},
		{"HEAD", "localhost:47441", "/api/", "127.0.0.1:12345"},
		{"GET", "localhost:47441", "/api/state?x=1", "127.0.0.1:12345"},
		{"GET", "localhost:47441", "/api%2Fstate", "127.0.0.1:12345"},
		{"POST", "localhost:47441", "/", "127.0.0.1:12345"},
		{"PUT", "localhost:47441", "/connections", "127.0.0.1:12345"},
		{"PATCH", "localhost:47441", "/", "127.0.0.1:12345"},
		{"DELETE", "localhost:47441", "/", "127.0.0.1:12345"},
		{"OPTIONS", "localhost:47441", "/", "127.0.0.1:12345"},
		{"GET", "localhost:47441", "/", "192.0.2.1:12345"},
		{"GET", "localhost:47441", "/", "invalid"},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
			r.RemoteAddr = tc.remote
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			body := object(t, w, http.StatusForbidden)
			failure := body["error"].(map[string]any)
			if failure["code"] != "host_denied" || failure["message"] != "Invalid local host." || w.Header().Get("Location") != "" {
				t.Fatalf("host rejection changed: %v", body)
			}
		})
	}
	// Canonical requests keep the exact-origin and remote-address checks.
	for _, tc := range []struct{ origin, remote, code string }{
		{"http://localhost:47441", "127.0.0.1:12345", "origin_denied"},
		{canonical, "192.0.2.1:12345", "loopback_required"},
	} {
		r := httptest.NewRequest("GET", canonical+"/api/state", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if object(t, w, 403)["error"].(map[string]any)["code"] != tc.code {
			t.Fatalf("lost %s check", tc.code)
		}
	}
}

func historyJobs(t *testing.T, f *fixture) []Job {
	t.Helper()
	w := f.req("GET", "/api/state", f.origin, "", "", nil, true)
	object(t, w, 200)
	var state struct{ Jobs []Job }
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state.Jobs
}

func routingRefusal(t *testing.T, f *fixture, token, lease, path string, request any) {
	t.Helper()
	body := object(t, f.req("POST", path, "https://writer.example", token, lease, request, false), 409)
	failure := body["error"].(map[string]any)
	if failure["code"] != "routing_required" || failure["message"] != routingRequiredMessage || len(body) != 1 {
		t.Fatalf("changed app refusal: %v", body)
	}
}

func TestRoutingRefusalHistoryDoesNotAdmitOrConsumeRequest(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\nactions: []\n")
	id, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	lease := f.lease(t, "https://writer.example", token)
	request := JobRequest{Kind: "action", Prompt: "private rejected prompt", IdempotencyKey: "unrouted-request"}
	preflight := object(t, f.req("POST", "/v2/admission", "https://writer.example", token, lease, AdmissionRequest{Request: request}, false), 200)
	if preflight["code"] != "routing_required" || len(historyJobs(t, f)) != 0 {
		t.Fatal("preflight must remain read-only")
	}
	routingRefusal(t, f, token, lease, "/v2/jobs", request)
	jobs := historyJobs(t, f)
	if len(jobs) != 1 {
		t.Fatalf("history count: %d", len(jobs))
	}
	j := jobs[0]
	if j.PairingID != id || j.Status != "failed" || j.Code != "routing_required" || j.Detail != routingRequiredMessage || j.Error == nil || j.Error.Message != routingRequiredMessage || j.FinishedAt == nil || !j.FinishedAt.Equal(f.now) {
		t.Fatalf("missing terminal refusal details: %+v", j)
	}
	if j.Request.Prompt != "" || j.Request.IdempotencyKey != "" || j.Request.Tools != nil || j.Plan != nil || j.Execution != nil || j.StartedAt != nil || j.Billing != "none" || len(f.a.commands) != 0 {
		t.Fatal("refusal retained request content or admitted execution")
	}
	lookup := object(t, f.req("POST", "/v2/jobs/lookup", "https://writer.example", token, lease, map[string]string{"idempotencyKey": request.IdempotencyKey}, false), 200)
	if lookup["job"] != nil {
		t.Fatal("refusal consumed the idempotency key")
	}
	object(t, f.req("PUT", "/api/pairings/"+id+"/routing", f.origin, "", "", map[string]any{"expectedRevision": 0, "routing": Routing{Default: &ExecutionTarget{Provider: "codex"}}}, true), 200)
	job := object(t, f.req("POST", "/v2/jobs", "https://writer.example", token, lease, request, false), 202)
	if job["status"] != "awaiting_approval" || len(historyJobs(t, f)) != 2 {
		t.Fatal("explicit retry after route selection did not retain mandatory consent")
	}
}

func TestRoutingRefusalHistoryThrottleAndClear(t *testing.T) {
	f := setup(t)
	_, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	lease := f.lease(t, "https://writer.example", token)
	request := JobRequest{Kind: "action", IdempotencyKey: "unrouted-request"}
	routingRefusal(t, f, token, lease, "/v2/jobs", request)
	for i := 0; i < 3; i++ {
		routingRefusal(t, f, token, lease, "/v2/jobs", request)
		routingRefusal(t, f, token, lease, "/v2/conversations", map[string]string{})
	}
	if len(historyJobs(t, f)) != 1 {
		t.Fatal("retries/session creation bypassed the per-connection throttle")
	}
	_, other := f.pair(t, "writer", "https://writer.example", "bob", false)
	otherLease := f.lease(t, "https://writer.example", other)
	routingRefusal(t, f, other, otherLease, "/v2/conversations", map[string]string{})
	if len(historyJobs(t, f)) != 2 {
		t.Fatal("one connection suppressed another connection's failure")
	}
	f.advance(t, 59*time.Second)
	lease = f.lease(t, "https://writer.example", token)
	routingRefusal(t, f, token, lease, "/v2/jobs", request)
	if len(historyJobs(t, f)) != 2 {
		t.Fatal("throttle expired before a minute")
	}
	f.advance(t, time.Second)
	routingRefusal(t, f, token, lease, "/v2/jobs", request)
	if len(historyJobs(t, f)) != 3 {
		t.Fatal("failure was not recorded after a minute")
	}
	object(t, f.req("DELETE", "/api/history", f.origin, "", "", nil, true), 200)
	routingRefusal(t, f, token, lease, "/v2/jobs", request)
	if len(historyJobs(t, f)) != 0 {
		t.Fatal("cleared failures survived or history clear bypassed throttling")
	}
	f.advance(t, time.Minute)
	lease = f.lease(t, "https://writer.example", token)
	routingRefusal(t, f, token, lease, "/v2/jobs", request)
	if len(historyJobs(t, f)) != 1 {
		t.Fatal("throttle did not expire after history deletion")
	}
}

func TestRoutingRefusalHistorySurvivesRestartAndUsesNormalLimits(t *testing.T) {
	t.Run("restart", func(t *testing.T) {
		f := setup(t)
		id, token := f.pair(t, "writer", "https://writer.example", "alice", false)
		lease := f.lease(t, "https://writer.example", token)
		routingRefusal(t, f, token, lease, "/v2/jobs", JobRequest{Kind: "action", IdempotencyKey: "unrouted-request"})
		if err := f.a.Close(); err != nil {
			t.Fatal(err)
		}
		next, err := Open(f.a.dir)
		if err != nil {
			t.Fatal(err)
		}
		defer next.Close()
		next.now = func() time.Time { return f.now.Add(30 * time.Second) }
		next.recordRoutingFailure(next.state.Pairings[id], fail(409, "routing_required", routingRequiredMessage))
		if len(next.state.Jobs) != 1 || next.state.Jobs[0].Detail != routingRequiredMessage || next.state.Jobs[0].Status != "failed" {
			t.Fatal("restart lost refusal history or its throttle")
		}
	})
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("full_history_active_%t", active), func(t *testing.T) {
			f := setup(t)
			_, token := f.pair(t, "writer", "https://writer.example", "alice", false)
			lease := f.lease(t, "https://writer.example", token)
			status := "completed"
			if active {
				status = "awaiting_approval"
			}
			for i := 0; i < maxJobs; i++ {
				f.a.state.Jobs = append(f.a.state.Jobs, &Job{ID: secret(), Status: status, CreatedAt: f.now.Add(time.Duration(i-maxJobs) * time.Second)})
			}
			oldest := f.a.state.Jobs[0].ID
			routingRefusal(t, f, token, lease, "/v2/jobs", JobRequest{Kind: "action", IdempotencyKey: "unrouted-request"})
			jobs := historyJobs(t, f)
			if len(jobs) != maxJobs || (findJob(&f.a.state, oldest) != nil) != active {
				t.Fatal("failure history did not use ordinary terminal-job eviction")
			}
			if !active && jobs[len(jobs)-1].Code != "routing_required" {
				t.Fatal("missing refusal after history rollover")
			}
		})
	}
	t.Run("storage_failure", func(t *testing.T) {
		f := setup(t)
		_, token := f.pair(t, "writer", "https://writer.example", "alice", false)
		lease := f.lease(t, "https://writer.example", token)
		statePath := filepath.Join(f.a.dir, "state.json")
		if err := os.Rename(statePath, statePath+".backup"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(statePath, 0700); err != nil {
			t.Fatal(err)
		}
		routingRefusal(t, f, token, lease, "/v2/jobs", JobRequest{Kind: "action", IdempotencyKey: "unrouted-request"})
		if len(f.a.state.Jobs) != 0 {
			t.Fatal("failed persistence published a refusal entry")
		}
	})
}
