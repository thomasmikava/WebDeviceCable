package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConversationHelper(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--conversation-helper" && i+1 < len(os.Args) {
			history := []string{}
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Buffer(make([]byte, 4096), 64<<10)
			emit := func(value any) { json.NewEncoder(os.Stdout).Encode(value) }
			for scanner.Scan() {
				var turn struct {
					Version   int               `json:"v"`
					Type      string            `json:"type"`
					ID        string            `json:"id"`
					Prompt    string            `json:"prompt"`
					Arguments map[string]string `json:"arguments"`
				}
				if json.Unmarshal(scanner.Bytes(), &turn) != nil {
					os.Exit(4)
				}
				if turn.Prompt == "HANG" {
					time.Sleep(time.Minute)
					continue
				}
				if turn.Prompt == "BAD" {
					emit(map[string]any{"v": 1, "type": "done", "id": "some-other-job"})
					continue
				}
				if turn.Prompt == "FAIL" {
					fmt.Fprintln(os.Stderr, "authorization: secret-fixture-token")
					emit(map[string]any{"v": 1, "type": "error", "id": turn.ID, "error": map[string]string{"code": "rate_limited", "message": "provider fixture rate exceeded", "raw": "Bearer sk-secret-fixture-123456"}})
					continue
				}
				history = append(history, turn.Prompt)
				emit(map[string]any{"v": 1, "type": "output", "id": turn.ID, "delta": "first chunk\n"})
				time.Sleep(100 * time.Millisecond)
				path, _ := os.Getwd()
				data, _ := json.Marshal(map[string]any{"messages": history, "pid": os.Getpid(), "cwd": path, "arguments": turn.Arguments})
				emit(map[string]any{"v": 1, "type": "output", "id": turn.ID, "delta": string(data)})
				emit(map[string]any{"v": 1, "type": "done", "id": turn.ID})
			}
			os.Exit(0)
		}
	}
}
func sessionAction(t *testing.T, id string) Action {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return Action{ID: id, Title: "Session " + id, Exec: []string{exe, "-test.run=^TestConversationHelper$", "--", "--conversation-helper", id}, Timeout: 3, MaxConcurrent: 4, Version: "1.2.3", Session: &ActionSession{Protocol: "jsonl", IdleTimeout: 60}}
}
func createConversation(t *testing.T, f *fixture, origin, token, lease string, options map[string]any) string {
	t.Helper()
	return object(t, f.req("POST", "/v2/conversations", origin, token, lease, options, false), 201)["id"].(string)
}
func conversationTurnJob(t *testing.T, f *fixture, origin, token, lease, conversation, key, prompt string) string {
	t.Helper()
	return object(t, f.req("POST", "/v2/conversations/"+conversation+"/turns", origin, token, lease, map[string]any{"idempotencyKey": key, "prompt": prompt}, false), 202)["id"].(string)
}
func readHelperOutput(t *testing.T, job *Job) map[string]any {
	t.Helper()
	part := strings.TrimPrefix(job.Output, "first chunk\n")
	out := map[string]any{}
	if e := json.Unmarshal([]byte(part), &out); e != nil {
		t.Fatal(e, job.Output)
	}
	return out
}
func routeApp(t *testing.T, f *fixture, pairing string, route Routing) {
	t.Helper()
	revision := f.a.state.Pairings[pairing].Routing.Revision
	object(t, f.req("PUT", "/api/pairings/"+pairing+"/routing", f.origin, "", "", map[string]any{"expectedRevision": revision, "routing": route}, true), 200)
}
func registerGroups(t *testing.T, f *fixture, origin, token string, groups ...ActionGroup) {
	t.Helper()
	object(t, f.req("POST", "/v2/groups", origin, token, "", map[string]any{"groups": groups}, false), 200)
}

func TestContinuingSessionUsesSameOwnedProcessAndPrivateDirectory(t *testing.T) {
	f := commandFixture(t, false, sessionAction(t, "memory"))
	origin := "https://chat.example"
	pair, token := f.pair(t, "chat", origin, "alice", true)
	grantCommands(t, f, pair, "memory")
	lease := f.lease(t, origin, token)
	conversation := createConversation(t, f, origin, token, lease, map[string]any{"action": "memory"})
	first := conversationTurnJob(t, f, origin, token, lease, conversation, "session-turn-first", "Hello")
	object(t, f.req("POST", "/v2/conversations/"+conversation+"/turns", origin, token, lease, map[string]any{"idempotencyKey": "overlap-should-fail", "prompt": "Next"}, false), 409)
	firstJob := waitJob(t, f, first, func(job *Job) bool { return terminal(job.Status) })
	if firstJob.Status != "completed" {
		t.Fatal(firstJob)
	}
	one := readHelperOutput(t, firstJob)
	if firstJob.Execution == nil || firstJob.Execution.Version != "1.2.3" || firstJob.Execution.VersionSource != "configured" {
		t.Fatal("missing truthful execution metadata", firstJob)
	}
	second := conversationTurnJob(t, f, origin, token, lease, conversation, "session-turn-second", "Again")
	secondJob := waitJob(t, f, second, func(job *Job) bool { return terminal(job.Status) })
	two := readHelperOutput(t, secondJob)
	if one["pid"] != two["pid"] || one["cwd"] != two["cwd"] || len(two["messages"].([]any)) != 2 {
		t.Fatal("session did not continue", one, two)
	}
	if !strings.HasPrefix(two["cwd"].(string), filepath.Join(f.a.dir, "runs")) {
		t.Fatal("session directory escaped data directory")
	}
	if secondJob.Request.Prompt != "" || secondJob.Request.Arguments != nil {
		t.Fatal("terminal input was retained")
	}
	duplicate := object(t, f.req("POST", "/v2/conversations/"+conversation+"/turns", origin, token, lease, map[string]any{"idempotencyKey": "session-turn-second", "prompt": "Again"}, false), 200)
	if duplicate["id"] != second {
		t.Fatal("turn replayed")
	}
	otherLease := f.lease(t, origin, token)
	object(t, f.req("POST", "/v2/conversations/"+conversation+"/turns", origin, token, otherLease, map[string]any{"idempotencyKey": "other-tab-denied", "prompt": "Private"}, false), 409)
	_, other := f.pair(t, "chat", origin, "bob", true)
	object(t, f.req("GET", "/v2/conversations/"+conversation, origin, other, "", nil, false), 404)
	runtime := f.a.conversations[conversation]
	object(t, f.req("DELETE", "/v2/conversations/"+conversation, origin, token, lease, nil, false), 200)
	select {
	case <-runtime.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("session process not stopped")
	}
	if _, e := os.Stat(one["cwd"].(string)); !os.IsNotExist(e) {
		t.Fatal("private session directory retained", e)
	}
	object(t, f.req("POST", "/v2/conversations/"+conversation+"/turns", origin, token, lease, map[string]any{"idempotencyKey": "closed-turn-denied", "prompt": "No"}, false), 410)
}

func TestGroupsRoutingPrivacyAndStalePreferences(t *testing.T) {
	a, b := helperAction(t, "alpha", "echo"), helperAction(t, "beta", "echo")
	a.Version = "v1"
	b.Version = "v2"
	f := commandFixture(t, false, a, b)
	origin := "https://groups.example"
	pair, token := f.pair(t, "writer", origin, "alice", true)
	grantCommands(t, f, pair, "alpha", "beta")
	lease := f.lease(t, origin, token)
	registerGroups(t, f, origin, token, ActionGroup{ID: "chat", Name: "Chat"}, ActionGroup{ID: "courses", Name: "Course creation"})
	routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Action: "alpha"}, Groups: map[string]ExecutionTarget{"courses": {Action: "beta"}}})
	first := f.job(t, origin, token, lease, "routing-first-chat", map[string]any{"kind": "action", "group": "chat"})
	second := f.job(t, origin, token, lease, "routing-first-course", map[string]any{"kind": "action", "group": "courses"})
	one := waitJob(t, f, first, func(job *Job) bool { return terminal(job.Status) })
	two := waitJob(t, f, second, func(job *Job) bool { return terminal(job.Status) })
	if jobAction(one) != "alpha" || jobAction(two) != "beta" {
		t.Fatal("group override ignored")
	}
	if one.Request.Action != "" || two.Request.Action != "" {
		t.Fatal("resolved selection leaked into caller request")
	}
	projection := object(t, f.req("GET", "/v2/jobs/"+first, origin, token, "", nil, false), 200)
	if projection["plan"] != nil || projection["execution"].(map[string]any)["name"] != a.Title {
		t.Fatal("invalid metadata projection", projection)
	}
	object(t, f.req("PATCH", "/api/sharing", f.origin, "", "", map[string]any{"names": false, "versions": false, "models": false}, true), 200)
	hidden := object(t, f.req("GET", "/v2/jobs/"+first, origin, token, "", nil, false), 200)
	for _, key := range []string{"plan", "execution", "actionTitle", "actualProvider", "actualModel", "diagnostic"} {
		if _, ok := hidden[key]; ok {
			t.Fatal("private field sent", key, hidden)
		}
	}
	caps := object(t, f.req("GET", "/v2/capabilities", origin, token, "", nil, false), 200)
	if strings.Contains(fmt.Sprint(caps["pairing"]), "alpha") {
		t.Fatal("route leaked in public pairing", caps)
	}
	// The key still refers to the original action after a local routing change.
	routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Action: "beta"}})
	old := object(t, f.req("POST", "/v2/jobs", origin, token, lease, map[string]any{"kind": "action", "group": "chat", "idempotencyKey": "routing-first-chat"}, false), 200)
	if old["id"] != first {
		t.Fatal("routing change replayed idempotent request")
	}
	object(t, f.req("PUT", "/api/pairings/"+pair+"/routing", f.origin, "", "", map[string]any{"expectedRevision": 0, "routing": Routing{}}, true), 409)
	object(t, f.req("POST", "/v2/groups", origin, token, "", map[string]any{"groups": []ActionGroup{{ID: "../bad", Name: "Bad"}}}, false), 400)
	bad := object(t, f.req("POST", "/v2/jobs", origin, token, lease, map[string]any{"kind": "action", "group": "missing", "idempotencyKey": "missing-group-test"}, false), 400)
	if bad["error"].(map[string]any)["category"] != "validation" {
		t.Fatal("error was not normalized", bad)
	}
}

func TestSessionErrorsAndLiveMetadataPrivacy(t *testing.T) {
	f := commandFixture(t, false, sessionAction(t, "memory"))
	origin := "https://private.example"
	pair, token := f.pair(t, "private", origin, "alice", true)
	grantCommands(t, f, pair, "memory")
	lease := f.lease(t, origin, token)
	conversation := createConversation(t, f, origin, token, lease, map[string]any{"action": "memory"})
	id := conversationTurnJob(t, f, origin, token, lease, conversation, "privacy-live-turn", "Hello")
	waitJob(t, f, id, func(job *Job) bool { return job.Status == "running" && strings.Contains(job.Output, "first chunk") })
	object(t, f.req("PATCH", "/api/sharing", f.origin, "", "", map[string]any{"names": false, "versions": false, "models": false}, true), 200)
	hidden := object(t, f.req("GET", "/v2/jobs/"+id, origin, token, "", nil, false), 200)
	if _, ok := hidden["execution"]; ok {
		t.Fatal("live metadata still shared")
	}
	waitJob(t, f, id, func(job *Job) bool { return terminal(job.Status) })
	failed := conversationTurnJob(t, f, origin, token, lease, conversation, "privacy-error-turn", "FAIL")
	job := waitJob(t, f, failed, func(job *Job) bool { return terminal(job.Status) })
	if job.Error == nil || job.Error.Category != "rate_limit" || !job.Error.Retryable {
		t.Fatal("unclassified failure", job)
	}
	remote := object(t, f.req("GET", "/v2/jobs/"+failed, origin, token, "", nil, false), 200)
	if _, ok := remote["error"].(map[string]any)["raw"]; ok {
		t.Fatal("diagnostics shared by default")
	}
	object(t, f.req("PATCH", "/api/sharing", f.origin, "", "", map[string]any{"rawErrors": true}, true), 200)
	shared := object(t, f.req("GET", "/v2/jobs/"+failed, origin, token, "", nil, false), 200)
	raw := shared["error"].(map[string]any)["raw"].(string)
	if strings.Contains(raw, "secret-fixture") || !strings.Contains(raw, "redacted") {
		t.Fatal("diagnostic secret was not redacted", raw)
	}
	if f.a.state.Conversations[conversation].Status != "closed" {
		t.Fatal("failed session remained usable")
	}
}

func TestTypedGroupsHierarchyAndInheritedSelections(t *testing.T) {
	f := commandFixture(t, false, helperAction(t, "alpha", "echo"), helperAction(t, "beta", "echo"))
	origin := "https://hierarchy.example"
	pair, token := f.pair(t, "hierarchy", origin, "alice", true)
	grantCommands(t, f, pair, "alpha", "beta")
	registerGroups(t, f, origin, token,
		ActionGroup{ID: "ai", Name: "AI", Kind: "ai"},
		ActionGroup{ID: "ai.chat", Name: "Chat", ParentID: "ai", Kind: "ai"},
		ActionGroup{ID: "ai.chat.notes", Name: "Notes", ParentID: "ai.chat", Kind: "ai"},
		ActionGroup{ID: "commands", Name: "Commands"},
		ActionGroup{ID: "export", Name: "Export", ParentID: "commands"})
	routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Provider: "codex", Model: "client-model-id", Reasoning: "xhigh"}, Groups: map[string]ExecutionTarget{"commands": {Action: "alpha"}, "ai.chat": {Provider: "claude", Model: "other-model-id", Reasoning: "high"}}})
	p := f.a.state.Pairings[pair]
	for _, item := range []struct{ group, provider, action, model string }{
		{"ai", "codex", "", "client-model-id"}, {"ai.chat.notes", "claude", "", "other-model-id"}, {"export", "", "alpha", ""},
	} {
		plan, err := f.a.resolvePlan(p, JobRequest{Kind: "action", Group: item.group})
		if err != nil || plan.Provider != item.provider || plan.Action != item.action || plan.Model != item.model {
			t.Fatal(item, plan, err)
		}
	}
	object(t, f.req("PUT", "/api/pairings/"+pair+"/routing", f.origin, "", "", map[string]any{"expectedRevision": p.Routing.Revision, "routing": Routing{Groups: map[string]ExecutionTarget{"commands": {Provider: "codex"}}}}, true), 400)
	for _, groups := range [][]ActionGroup{
		{{ID: "ai", Name: "AI", ParentID: "ai.chat.notes", Kind: "ai"}},
		{{ID: "orphan", Name: "Orphan", ParentID: "absent"}},
		{{ID: "wrong", Name: "Wrong", Kind: "something-else"}},
	} {
		object(t, f.req("POST", "/v2/groups", origin, token, "", map[string]any{"groups": groups}, false), 400)
	}
	lease := f.lease(t, origin, token)
	blocked := f.job(t, origin, token, lease, "native-profile-gates", map[string]any{"kind": "action", "group": "ai.chat.notes", "prompt": "Test"})
	job := waitJob(t, f, blocked, func(j *Job) bool { return terminal(j.Status) })
	if job.Status != "blocked" || job.Plan.Model != "other-model-id" || job.Execution != nil {
		t.Fatal("native gates or truthful metadata lost", job)
	}
	object(t, f.req("POST", "/v2/conversations", origin, token, lease, map[string]any{"group": "ai"}, false), 503)
	// Subgroups are optional. A standalone ordinary group works with its own command.
	registerGroups(t, f, origin, token, ActionGroup{ID: "standalone", Name: "Standalone"})
	routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Provider: "codex"}, Groups: map[string]ExecutionTarget{"standalone": {Action: "beta"}}})
	standalone := f.job(t, origin, token, lease, "standalone-command", map[string]any{"kind": "action", "group": "standalone"})
	if jobAction(waitJob(t, f, standalone, func(j *Job) bool { return terminal(j.Status) })) != "beta" {
		t.Fatal("standalone group failed")
	}
	object(t, f.req("POST", "/v2/jobs", origin, token, lease, map[string]any{"kind": "action", "group": "commands", "idempotencyKey": "no-implicit-native"}, false), 409)
}

func TestSessionLifecycleAndHierarchyChanges(t *testing.T) {
	for _, change := range []string{"lease", "pause", "revoke", "route", "hierarchy", "deadline", "protocol", "idle"} {
		t.Run(change, func(t *testing.T) {
			action := sessionAction(t, "memory")
			if change == "idle" {
				action.Session.IdleTimeout = 1
			}
			f := commandFixture(t, false, action)
			origin := "https://lifecycle.example"
			pair, token := f.pair(t, "lifecycle", origin, "alice", true)
			grantCommands(t, f, pair, "memory")
			lease := f.lease(t, origin, token)
			registerGroups(t, f, origin, token, ActionGroup{ID: "root", Name: "Root"}, ActionGroup{ID: "chat", Name: "Chat", ParentID: "root"})
			routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Action: "memory"}})
			conversation := createConversation(t, f, origin, token, lease, map[string]any{"group": "chat"})
			prompt := "Hello"
			if change == "protocol" {
				prompt = "BAD"
			}
			if change == "deadline" {
				prompt = "HANG"
			}
			id := conversationTurnJob(t, f, origin, token, lease, conversation, "lifecycle-first-turn", prompt)
			if change == "protocol" || change == "deadline" {
				if change == "deadline" {
					waitJob(t, f, id, func(j *Job) bool { return j.Status == "running" })
					f.now = f.now.Add(121 * time.Second)
					live := f.a.leases[lease]
					live.ExpiresAt = f.now.Add(LeaseTTL)
					f.a.leases[lease] = live
				}
				job := waitJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
				if change == "deadline" && (job.Status != "timed_out" || job.Error.Category != "timeout") {
					t.Fatal("deadline did not cancel the message", job)
				}
				if change == "protocol" && (job.Error == nil || job.Error.Category != "protocol") {
					t.Fatal(job)
				}
			} else {
				waitJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
				switch change {
				case "lease":
					object(t, f.req("DELETE", "/v2/lease", origin, token, lease, nil, false), 200)
				case "pause":
					object(t, f.req("PATCH", "/api/runner", f.origin, "", "", map[string]bool{"enabled": false}, true), 200)
				case "revoke":
					object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]bool{"revoke": true}, true), 200)
				case "route":
					routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Action: "memory"}})
				case "hierarchy":
					registerGroups(t, f, origin, token, ActionGroup{ID: "chat", Name: "Chat"})
				case "idle":
					time.Sleep(1100 * time.Millisecond)
				}
			}
			runtime := f.a.conversations[conversation]
			for i := 0; i < 150 && f.a.state.Conversations[conversation].Status == "open"; i++ {
				f.a.tick()
				time.Sleep(10 * time.Millisecond)
			}
			if f.a.state.Conversations[conversation].Status != "closed" {
				t.Fatal("conversation stayed open")
			}
			if runtime != nil {
				select {
				case <-runtime.stopped:
				case <-time.After(3 * time.Second):
					t.Fatal("owned process stayed alive")
				}
			}
		})
	}
}

func TestIdleSessionCountsTowardCapacityAndIndependentContexts(t *testing.T) {
	action := sessionAction(t, "memory")
	action.MaxConcurrent = 1
	f := commandFixture(t, false, action)
	origin := "https://capacity.example"
	pair, token := f.pair(t, "capacity", origin, "alice", true)
	grantCommands(t, f, pair, "memory")
	lease := f.lease(t, origin, token)
	one := createConversation(t, f, origin, token, lease, map[string]any{"action": "memory"})
	two := createConversation(t, f, origin, token, lease, map[string]any{"action": "memory"})
	first := conversationTurnJob(t, f, origin, token, lease, one, "capacity-first-turn", "Private one")
	waitJob(t, f, first, func(j *Job) bool { return terminal(j.Status) })
	second := conversationTurnJob(t, f, origin, token, lease, two, "capacity-second-turn", "Private two")
	f.a.tick()
	if findJob(&f.a.state, second).Status != "queued" {
		t.Fatal("idle process ignored for capacity")
	}
	next := conversationTurnJob(t, f, origin, token, lease, one, "capacity-reused-turn", "Continue one")
	if waitJob(t, f, next, func(j *Job) bool { return terminal(j.Status) }).Turn != 2 {
		t.Fatal("message numbers not immutable")
	}
	object(t, f.req("DELETE", "/v2/conversations/"+one, origin, token, lease, nil, false), 200)
	job := waitJob(t, f, second, func(j *Job) bool { return terminal(j.Status) })
	history := readHelperOutput(t, job)["messages"].([]any)
	if len(history) != 1 || history[0] != "Private two" {
		t.Fatal("private contexts mixed", history)
	}
}

func TestDiagnosticsAreBoundedAndRedactStructuredSecrets(t *testing.T) {
	raw := cleanDiagnostic(`{"authorization":"Bearer plain-token-value","apiKey":"api-secret-value","refreshToken":"refresh-secret-value","password":"password-value"} Bearer other-token-value`)
	for _, secret := range []string{"plain-token-value", "api-secret-value", "refresh-secret-value", "password-value", "other-token-value"} {
		if strings.Contains(raw, secret) {
			t.Fatal("diagnostic secret survived", raw)
		}
	}
	if !strings.Contains(raw, "[redacted]") || len(cleanDiagnostic(strings.Repeat("あ", 4000))) > 4096 {
		t.Fatal("unbounded diagnostic")
	}
}
