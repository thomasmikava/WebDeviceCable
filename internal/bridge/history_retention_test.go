package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setHistory(t *testing.T, f *fixture, keep bool) {
	t.Helper()
	object(t, f.req("PATCH", "/api/history", f.origin, "", "", map[string]bool{"keep": keep, "confirmDiscard": true}, true), 200)
}
func setConnectionHistory(t *testing.T, f *fixture, id, value string) {
	t.Helper()
	object(t, f.req("PATCH", "/api/pairings/"+id, f.origin, "", "", map[string]any{"requestHistory": value, "confirmDiscard": true}, true), 200)
}
func assertNoSavedJob(t *testing.T, f *fixture, id string) {
	t.Helper()
	for _, j := range historyJobs(t, f) {
		if j.ID == id {
			t.Fatal("finished opted-out request appeared in panel state")
		}
	}
	data, err := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(id)) {
		t.Fatal("finished request ID survived in durable state")
	}
	if _, err := os.Stat(filepath.Join(f.a.dir, "artifacts", id)); !os.IsNotExist(err) {
		t.Fatal("finished artifacts survived on disk", err)
	}
}
func finishHistoryJob(t *testing.T, f *fixture, id, status string) {
	t.Helper()
	f.a.mu.Lock()
	err := f.a.update(func(s *State) error {
		j := findJob(s, id)
		j.Output = "private finished output"
		finish(j, status, "fixture_done", "Fixture finished.", f.now)
		return nil
	})
	f.a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRequestHistoryPrecedenceAndConfirmationScope(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\nactions: []\n")
	ids := map[string]string{}
	jobs := map[string]string{}
	for _, choice := range []string{"global", "keep", "discard"} {
		id, token := f.pair(t, "writer", "https://writer.example", choice, false)
		ids[choice] = id
		if choice != "global" {
			setConnectionHistory(t, f, id, choice)
		}
		lease := f.lease(t, "https://writer.example", token)
		jobs[choice] = f.job(t, "https://writer.example", token, lease, "history-"+choice, map[string]any{"kind": "connection-test"})
		finishHistoryJob(t, f, jobs[choice], "completed")
	}
	if len(historyJobs(t, f)) != 2 {
		t.Fatal("default or explicit discard precedence failed")
	}
	object(t, f.req("PATCH", "/api/history", f.origin, "", "", map[string]bool{"keep": false}, true), 400)
	if !globalRequestHistory(&f.a.state) || len(historyJobs(t, f)) != 2 {
		t.Fatal("unconfirmed change applied")
	}
	setHistory(t, f, false)
	remaining := historyJobs(t, f)
	if len(remaining) != 1 || remaining[0].ID != jobs["keep"] {
		t.Fatal("global off erased the explicit keep override", remaining)
	}
	assertNoSavedJob(t, f, jobs["global"])
	setHistory(t, f, true)
	if len(historyJobs(t, f)) != 1 {
		t.Fatal("enabling history restored old records")
	}
	object(t, f.req("PATCH", "/api/pairings/"+ids["keep"], f.origin, "", "", map[string]string{"requestHistory": "discard"}, true), 400)
	if len(historyJobs(t, f)) != 1 {
		t.Fatal("connection purge did not require confirmation")
	}
	setConnectionHistory(t, f, ids["keep"], "discard")
	if len(historyJobs(t, f)) != 0 {
		t.Fatal("connection history not purged")
	}
	object(t, f.req("PATCH", "/api/pairings/"+ids["keep"], f.origin, "", "", map[string]string{"requestHistory": "invalid"}, true), 400)
	for _, global := range []bool{true, false} {
		setHistory(t, f, global)
		for _, choice := range []string{"global", "keep", "discard"} {
			p := f.a.state.Pairings[ids[choice]]
			setConnectionHistory(t, f, p.ID, choice)
			p = f.a.state.Pairings[p.ID]
			want := choice == "keep" || choice == "global" && global
			if keepRequestHistory(&f.a.state, p) != want {
				t.Fatalf("global=%t choice=%s", global, choice)
			}
		}
	}
	// Owner-only controls retain session, CSRF and exact-origin protection.
	object(t, f.req("PATCH", "/api/history", f.origin, "", "", map[string]bool{"keep": true}, false), 401)
	object(t, f.req("PATCH", "/api/history", "http://localhost:47321", "", "", map[string]bool{"keep": true}, true), 403)
}

func TestRequestHistoryDeliveryPathsAndReplay(t *testing.T) {
	for _, delivery := range []string{"get", "stream", "lookup", "retry", "app-cancel"} {
		t.Run(delivery, func(t *testing.T) {
			f := setupConfig(t, "bridge:\n  disableNative: true\nactions: []\n")
			setHistory(t, f, false)
			id, token := f.pair(t, "writer", "https://writer.example", "alice", false)
			lease := f.lease(t, "https://writer.example", token)
			request := JobRequest{Kind: "connection-test", IdempotencyKey: "history-delivery-key"}
			job := object(t, f.req("POST", "/v2/jobs", "https://writer.example", token, lease, request, false), 202)["id"].(string)
			if len(historyJobs(t, f)) != 1 {
				t.Fatal("active request was hidden")
			}
			// Another connection must not read or consume a private result.
			_, other := f.pair(t, "writer", "https://writer.example", "bob", false)
			finishHistoryJob(t, f, job, "completed")
			assertNoSavedJob(t, f, job)
			object(t, f.req("GET", "/v2/jobs/"+job, "https://writer.example", other, "", nil, false), 404)
			object(t, f.req("POST", "/api/jobs/"+job+"/cancel", f.origin, "", "", nil, true), 404)
			if findJob(&f.a.state, job) == nil {
				t.Fatal("uncollected result lost")
			}
			var body string
			switch delivery {
			case "get":
				body = f.req("GET", "/v2/jobs/"+job, "https://writer.example", token, "", nil, false).Body.String()
			case "stream":
				body = f.req("GET", "/v2/jobs/"+job+"/events", "https://writer.example", token, "", nil, false).Body.String()
			case "lookup":
				body = f.req("POST", "/v2/jobs/lookup", "https://writer.example", token, "", map[string]string{"idempotencyKey": request.IdempotencyKey}, false).Body.String()
			case "retry":
				body = f.req("POST", "/v2/jobs", "https://writer.example", token, lease, request, false).Body.String()
			case "app-cancel":
				body = f.req("POST", "/v2/jobs/"+job+"/cancel", "https://writer.example", token, "", nil, false).Body.String()
			}
			if !strings.Contains(body, "private finished output") {
				t.Fatal("app did not receive result", body)
			}
			if findJob(&f.a.state, job) != nil || len(f.a.deliveries) != 0 {
				t.Fatal("delivered result was retained")
			}
			replay := object(t, f.req("POST", "/v2/jobs", "https://writer.example", token, lease, request, false), 410)
			if replay["error"].(map[string]any)["code"] != "history_deleted" {
				t.Fatal("replay was not blocked")
			}
			receipt, ok, err := f.a.receipt(digest(id + ":" + request.IdempotencyKey))
			if err != nil || !ok || receipt.JobID != job {
				t.Fatal("missing replay receipt", err)
			}
		})
	}
}

func TestRequestHistoryEveryTerminalStatusAndBoundedFallback(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled", "timed_out", "blocked", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			f := setup(t)
			setHistory(t, f, false)
			_, token := f.pair(t, "writer", "https://writer.example", "alice", false)
			lease := f.lease(t, "https://writer.example", token)
			job := f.job(t, "https://writer.example", token, lease, "history-status-key", map[string]any{"kind": "connection-test"})
			finishHistoryJob(t, f, job, status)
			assertNoSavedJob(t, f, job)
			f.advance(t, resultDeliveryTTL-time.Second)
			if findJob(&f.a.state, job) == nil {
				t.Fatal("fallback expired early")
			}
			f.advance(t, time.Second)
			if findJob(&f.a.state, job) != nil || len(f.a.deliveries) != 0 {
				t.Fatal("uncollected result outlived deadline")
			}
		})
	}
	// Actual owner decline/cancel paths must hide immediately and still deliver.
	for _, decline := range []bool{false, true} {
		f := setup(t)
		setHistory(t, f, false)
		_, token := f.pair(t, "writer", "https://writer.example", "alice", false)
		lease := f.lease(t, "https://writer.example", token)
		job := f.job(t, "https://writer.example", token, lease, "history-decline-key", map[string]any{"kind": "connection-test"})
		if decline {
			object(t, f.req("POST", "/api/jobs/"+job+"/decision", f.origin, "", "", map[string]bool{"approve": false}, true), 200)
		} else {
			object(t, f.req("POST", "/api/jobs/"+job+"/cancel", f.origin, "", "", nil, true), 200)
		}
		assertNoSavedJob(t, f, job)
		object(t, f.req("GET", "/v2/jobs/"+job, "https://writer.example", token, "", nil, false), 200)
		if findJob(&f.a.state, job) != nil {
			t.Fatal("cancelled result survived delivery")
		}
	}
}

func TestRequestHistoryArtifactsCollectedOnlyAfterAllBytes(t *testing.T) {
	f := setup(t)
	setHistory(t, f, false)
	pair, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]bool{"files": true}, true), 200)
	lease := f.lease(t, "https://writer.example", token)
	job := f.job(t, "https://writer.example", token, lease, "history-artifact-key", map[string]any{"kind": "connection-test"})
	data := bytes.Repeat([]byte("private-file"), 3000)
	files := []Artifact{{ID: secret(), Name: "a.txt", Size: len(data), Hash: fileHash(data)}, {ID: secret(), Name: "b.txt", Size: len(data), Hash: fileHash(data)}}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(f.a.artifactPath(job, file.ID)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := writeLocalFile(f.a.artifactPath(job, file.ID), data); err != nil {
			t.Fatal(err)
		}
	}
	f.a.state.Jobs[0].Artifacts = files
	finishHistoryJob(t, f, job, "completed")
	assertNoSavedJob(t, f, job)
	object(t, f.req("GET", "/api/jobs/"+job+"/artifacts/"+files[0].ID, f.origin, "", "", nil, true), 404)
	object(t, f.req("GET", "/v2/jobs/"+job, "https://writer.example", token, "", nil, false), 200)
	if findJob(&f.a.state, job) == nil {
		t.Fatal("job disappeared before artifact delivery")
	}
	for i, file := range files {
		for offset := 0; offset < len(data); offset += fileChunk {
			chunk := object(t, f.req("GET", fmt.Sprintf("/v2/jobs/%s/artifacts/%s/%d", job, file.ID, offset), "https://writer.example", token, "", nil, false), 200)
			if chunk["data"] == "" {
				t.Fatal("empty file chunk")
			}
			if i == 0 && findJob(&f.a.state, job) == nil {
				t.Fatal("second artifact was lost")
			}
		}
	}
	if findJob(&f.a.state, job) != nil || len(f.a.deliveries) != 0 {
		t.Fatal("artifact result survived full delivery")
	}
}

type failedDeliveryWriter struct{}

func (failedDeliveryWriter) Header() http.Header       { return http.Header{} }
func (failedDeliveryWriter) WriteHeader(int)           {}
func (failedDeliveryWriter) Write([]byte) (int, error) { return 0, errors.New("fixture disconnected") }

func TestRequestHistoryFailedSignedWriteCanRetry(t *testing.T) {
	f := setup(t)
	setHistory(t, f, false)
	_, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	lease := f.lease(t, "https://writer.example", token)
	job := f.job(t, "https://writer.example", token, lease, "history-write-failure", map[string]any{"kind": "connection-test"})
	finishHistoryJob(t, f, job, "completed")
	peer := f.clients[token]
	channel, _, err := f.testChannel("https://writer.example", peer)
	if err != nil {
		t.Fatal(err)
	}
	req := secureRequest{Version: 2, Kind: "ai-bridge-request", SessionID: channel.id, Nonce: secret(), Timestamp: f.now.UnixMilli(), Origin: "https://writer.example", PairingID: token, PublicKey: publicKey(peer.key), Method: "GET", Path: "/v2/jobs/" + job}
	envelope, err := testSeal(channel, req)
	if err != nil {
		t.Fatal(err)
	}
	// Use the complete public handler so a buffered logical response cannot be
	// mistaken for delivery before its signed envelope reaches the outer writer.
	raw, _ := jsonData(envelope)
	r := httptest.NewRequest("POST", f.origin+"/v2/request", strings.NewReader(string(raw)))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://writer.example")
	f.handler.ServeHTTP(failedDeliveryWriter{}, r)
	if findJob(&f.a.state, job) == nil {
		t.Fatal("failed signed write consumed result")
	}
	object(t, f.req("GET", "/v2/jobs/"+job, "https://writer.example", token, "", nil, false), 200)
	if findJob(&f.a.state, job) != nil {
		t.Fatal("retry did not collect")
	}
}

func TestRequestHistoryRoutingRefusalsAndRestart(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\nactions: []\n")
	pair, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	lease := f.lease(t, "https://writer.example", token)
	request := JobRequest{Kind: "action", Prompt: "private refused input", IdempotencyKey: "history-refusal-key"}
	setHistory(t, f, false)
	routingRefusal(t, f, token, lease, "/v2/jobs", request)
	routingRefusal(t, f, token, lease, "/v2/conversations", map[string]string{})
	if len(f.a.state.Jobs) != 0 || len(historyJobs(t, f)) != 0 {
		t.Fatal("routing refusal left an entry with history off")
	}
	receipt, consumed, err := f.a.receipt(digest(pair + ":" + request.IdempotencyKey))
	if err != nil || consumed {
		t.Fatal("routing refusal consumed a request", receipt, err)
	}
	setConnectionHistory(t, f, pair, "keep")
	routingRefusal(t, f, token, lease, "/v2/jobs", request)
	if len(historyJobs(t, f)) != 1 {
		t.Fatal("keep override did not retain refusal")
	}
	setConnectionHistory(t, f, pair, "discard")
	if len(historyJobs(t, f)) != 0 {
		t.Fatal("stored refusal was not purged")
	}
	job := f.job(t, "https://writer.example", token, lease, "history-restart-key", map[string]any{"kind": "connection-test"})
	finishHistoryJob(t, f, job, "completed")
	active := f.job(t, "https://writer.example", token, lease, "history-restart-active", map[string]any{"kind": "connection-test"})
	assertNoSavedJob(t, f, job)
	identity := publicKey(f.a.identity)
	if err := f.a.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if globalRequestHistory(&next.state) || next.state.Pairings[pair].RequestHistory != "discard" || publicKey(next.identity) != identity {
		t.Fatal("restart lost settings or changed pairing identity")
	}
	if len(next.state.Jobs) != 0 || len(next.deliveries) != 0 {
		t.Fatal("restart resurrected opted-out records", active)
	}
	p := next.state.Pairings[pair]
	for _, key := range []string{"history-restart-key", "history-restart-active"} {
		_, _, err := next.createJob(p, JobRequest{Kind: "connection-test", IdempotencyKey: key}, secret())
		var failure *apiError
		if !errors.As(err, &failure) || failure.Code != "history_deleted" {
			t.Fatal("receipt did not block replay after restart", err)
		}
	}
	// Missing settings in an old installation continue to mean keep.
	old := setup(t)
	if !globalRequestHistory(&old.a.state) {
		t.Fatal("legacy default changed")
	}
}

func TestRequestHistoryOpenSessionSurvivesCollectedTurns(t *testing.T) {
	f := commandFixture(t, false, sessionAction(t, "memory"))
	pair, token := f.pair(t, "chat", "https://chat.example", "alice", true)
	grantCommands(t, f, pair, "memory")
	lease := f.lease(t, "https://chat.example", token)
	conversation := createConversation(t, f, "https://chat.example", token, lease, map[string]any{"action": "memory"})
	first := conversationTurnJob(t, f, "https://chat.example", token, lease, conversation, "history-session-first", "Hello")
	one := readHelperOutput(t, waitJob(t, f, first, func(j *Job) bool { return terminal(j.Status) }))
	setHistory(t, f, false)
	assertNoSavedJob(t, f, first)
	if f.a.state.Conversations[conversation].Status != "open" {
		t.Fatal("turn purge closed live context")
	}
	second := conversationTurnJob(t, f, "https://chat.example", token, lease, conversation, "history-session-next", "Again")
	two := readHelperOutput(t, waitJob(t, f, second, func(j *Job) bool { return terminal(j.Status) }))
	if one["pid"] != two["pid"] || len(two["messages"].([]any)) != 2 {
		t.Fatal("session context was dropped")
	}
	assertNoSavedJob(t, f, second)
	object(t, f.req("GET", "/v2/jobs/"+second, "https://chat.example", token, "", nil, false), 200)
	if findJob(&f.a.state, second) != nil {
		t.Fatal("session result not collected")
	}
	third := conversationTurnJob(t, f, "https://chat.example", token, lease, conversation, "history-session-third", "Third")
	waitJob(t, f, third, func(j *Job) bool { return terminal(j.Status) })
	object(t, f.req("GET", "/v2/jobs/"+third, "https://chat.example", token, "", nil, false), 200)
	object(t, f.req("DELETE", "/v2/conversations/"+conversation, "https://chat.example", token, lease, nil, false), 200)
	object(t, f.req("GET", "/v2/conversations/"+conversation, "https://chat.example", token, "", nil, false), 200)
	f.tick(t)
	replay := object(t, f.req("POST", "/v2/conversations/"+conversation+"/turns", "https://chat.example", token, lease, map[string]any{"idempotencyKey": "history-session-third", "prompt": "Third"}, false), 410)
	if replay["error"].(map[string]any)["code"] != "history_deleted" {
		t.Fatal("closed-session receipt lost its request binding")
	}
	if len(f.a.publicConversations()) != 0 {
		t.Fatal("closed opted-out session appeared in panel")
	}
	data, err := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(conversation)) {
		t.Fatal("closed session survived on disk")
	}
}

func TestRequestHistoryReenableOnlyRetainsNewRequests(t *testing.T) {
	f := setup(t)
	setHistory(t, f, false)
	pair, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	lease := f.lease(t, "https://writer.example", token)
	first := f.job(t, "https://writer.example", token, lease, "history-before-on", map[string]any{"kind": "connection-test"})
	setHistory(t, f, true)
	finishHistoryJob(t, f, first, "completed")
	assertNoSavedJob(t, f, first)
	second := f.job(t, "https://writer.example", token, lease, "history-after-on", map[string]any{"kind": "connection-test"})
	finishHistoryJob(t, f, second, "completed")
	if len(historyJobs(t, f)) != 1 {
		t.Fatal("new request history was not kept")
	}
	setHistory(t, f, false)
	setConnectionHistory(t, f, pair, "keep")
	third := f.job(t, "https://writer.example", token, lease, "history-override-on", map[string]any{"kind": "connection-test"})
	finishHistoryJob(t, f, third, "completed")
	if len(historyJobs(t, f)) != 1 || historyJobs(t, f)[0].ID != third {
		t.Fatal("keep override failed when global off")
	}
}

func TestRequestHistoryEmptyFilesAndPartialFileExpiry(t *testing.T) {
	for _, size := range []int{0, fileChunk + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := setup(t)
			setHistory(t, f, false)
			pair, token := f.pair(t, "writer", "https://writer.example", "alice", false)
			object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]bool{"files": true}, true), 200)
			lease := f.lease(t, "https://writer.example", token)
			job := f.job(t, "https://writer.example", token, lease, "history-empty-file", map[string]any{"kind": "connection-test"})
			data := make([]byte, size)
			file := Artifact{ID: secret(), Name: "fixture.bin", Size: size, Hash: fileHash(data)}
			if err := os.MkdirAll(filepath.Dir(f.a.artifactPath(job, file.ID)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := writeLocalFile(f.a.artifactPath(job, file.ID), data); err != nil {
				t.Fatal(err)
			}
			findJob(&f.a.state, job).Artifacts = []Artifact{file}
			finishHistoryJob(t, f, job, "completed")
			object(t, f.req("GET", "/v2/jobs/"+job, "https://writer.example", token, "", nil, false), 200)
			if findJob(&f.a.state, job) == nil {
				t.Fatal("file result purged before download")
			}
			if size > 0 {
				object(t, f.req("GET", fmt.Sprintf("/v2/jobs/%s/artifacts/%s/%d", job, file.ID, fileChunk), "https://writer.example", token, "", nil, false), 200)
				if findJob(&f.a.state, job) == nil {
					t.Fatal("last chunk incorrectly collected unsent earlier bytes")
				}
				f.advance(t, resultDeliveryTTL)
			} else {
				object(t, f.req("GET", fmt.Sprintf("/v2/jobs/%s/artifacts/%s/0", job, file.ID), "https://writer.example", token, "", nil, false), 200)
			}
			if findJob(&f.a.state, job) != nil || len(f.a.deliveries) != 0 {
				t.Fatal("file result retained after collection/expiry")
			}
		})
	}
}

func TestRequestHistoryDisableDoesNotRacePendingDelivery(t *testing.T) {
	for _, scope := range []string{"global", "connection"} {
		t.Run(scope, func(t *testing.T) {
			f := setup(t)
			pair, token := f.pair(t, "writer", "https://writer.example", "alice", false)
			lease := f.lease(t, "https://writer.example", token)
			collected := f.job(t, "https://writer.example", token, lease, "history-already-collected", map[string]any{"kind": "connection-test"})
			pending := f.job(t, "https://writer.example", token, lease, "history-still-collecting", map[string]any{"kind": "connection-test"})
			finishHistoryJob(t, f, collected, "completed")
			finishHistoryJob(t, f, pending, "completed")
			object(t, f.req("GET", "/v2/jobs/"+collected, "https://writer.example", token, "", nil, false), 200)
			if scope == "global" {
				setHistory(t, f, false)
			} else {
				setConnectionHistory(t, f, pair, "discard")
			}
			assertNoSavedJob(t, f, collected)
			assertNoSavedJob(t, f, pending)
			if findJob(&f.a.state, collected) != nil {
				t.Fatal("already collected history kept a delivery copy")
			}
			result := object(t, f.req("POST", "/v2/jobs/lookup", "https://writer.example", token, "", map[string]string{"idempotencyKey": "history-still-collecting"}, false), 200)
			if result["job"].(map[string]any)["output"] != "private finished output" {
				t.Fatal("turning off history lost pending result")
			}
			if findJob(&f.a.state, pending) != nil {
				t.Fatal("pending result survived collection")
			}
		})
	}
}

func TestRequestHistoryArtifactPreviewCountsTowardDelivery(t *testing.T) {
	f := setup(t)
	setHistory(t, f, false)
	pair, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]bool{"files": true}, true), 200)
	lease := f.lease(t, "https://writer.example", token)
	job := f.job(t, "https://writer.example", token, lease, "history-preview-file", map[string]any{"kind": "connection-test"})
	data := []byte("preview file")
	file := Artifact{ID: secret(), Name: "fixture.txt", Size: len(data), Hash: fileHash(data)}
	if err := os.MkdirAll(filepath.Dir(f.a.artifactPath(job, file.ID)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalFile(f.a.artifactPath(job, file.ID), data); err != nil {
		t.Fatal(err)
	}
	findJob(&f.a.state, job).Artifacts = []Artifact{file}
	object(t, f.req("GET", fmt.Sprintf("/v2/jobs/%s/artifacts/%s/0", job, file.ID), "https://writer.example", token, "", nil, false), 200)
	finishHistoryJob(t, f, job, "completed")
	object(t, f.req("GET", "/v2/jobs/"+job, "https://writer.example", token, "", nil, false), 200)
	if findJob(&f.a.state, job) != nil {
		t.Fatal("already delivered file unnecessarily held the finished result")
	}
}

type historyRelayTransport func(*http.Request) (*http.Response, error)

func (f historyRelayTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRequestHistoryRelayWaitsForTransportCompletion(t *testing.T) {
	for _, failEnd := range []bool{false, true} {
		t.Run(fmt.Sprint(failEnd), func(t *testing.T) {
			f := setup(t)
			setHistory(t, f, false)
			_, token := f.pair(t, "writer", "https://writer.example", "alice", false)
			lease := f.lease(t, "https://writer.example", token)
			job := f.job(t, "https://writer.example", token, lease, "history-relay-delivery", map[string]any{"kind": "connection-test"})
			finishHistoryJob(t, f, job, "completed")
			peer := f.clients[token]
			channel, _, err := f.testChannel("https://writer.example", peer)
			if err != nil {
				t.Fatal(err)
			}
			req := secureRequest{Version: 2, Kind: "ai-bridge-request", SessionID: channel.id, Nonce: secret(), Timestamp: f.now.UnixMilli(), Origin: "https://writer.example", PairingID: token, PublicKey: publicKey(peer.key), Method: "GET", Path: "/v2/jobs/" + job}
			envelope, err := testSeal(channel, req)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := jsonData(envelope)
			r := httptest.NewRequest("POST", f.origin+"/v2/request", bytes.NewReader(raw))
			r.RemoteAddr = "127.0.0.1:12345"
			r.Header.Set("Origin", "https://writer.example")
			r.Header.Set("Content-Type", "application/json")
			writer := &relayWriter{ctx: context.Background(), binding: RelayBinding{URL: "https://relay.invalid", ConnectionID: secret()}, id: secret(), header: http.Header{}, status: 200, client: &http.Client{Transport: historyRelayTransport(func(r *http.Request) (*http.Response, error) {
				var result relayResult
				if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
					t.Fatal(err)
				}
				status := 200
				if result.End && failEnd {
					status = 503
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
			})}}
			f.handler.ServeHTTP(writer, r)
			if findJob(&f.a.state, job) == nil {
				t.Fatal("relay body alone collected the result")
			}
			writer.finish()
			if (findJob(&f.a.state, job) != nil) != failEnd {
				t.Fatal("relay completion failed to govern collection")
			}
			if failEnd {
				object(t, f.req("GET", "/v2/jobs/"+job, "https://writer.example", token, "", nil, false), 200)
			}
		})
	}
}
