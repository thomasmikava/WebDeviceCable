package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func settingsBody(f *fixture, change func(*OwnerSettings)) map[string]any {
	o := ownerSettings(&f.a.state)
	h := *o.History
	o.History = &h
	change(&o)
	return map[string]any{"settings": o, "confirmDiscard": true}
}
func saveSettings(t *testing.T, f *fixture, change func(*OwnerSettings)) {
	t.Helper()
	object(t, f.req("PATCH", "/api/settings", f.origin, "", "", settingsBody(f, change), true), 200)
}
func TestOwnerSettingsBoundsAuthorizationAndPrecedence(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n  maxTextBytes: 8192\nactions: []\n")
	if ownerSettings(&f.a.state).MaximumRequestSeconds != 600 || f.a.textLimit() != 8192 {
		t.Fatal("new defaults/config fallback")
	}
	pair, token := f.pair(t, "app", "https://settings.example", "user", false)
	for _, change := range []func(*OwnerSettings){func(o *OwnerSettings) { o.MaximumRequestSeconds = 0 }, func(o *OwnerSettings) { o.MaximumRequestSeconds = 3601 }, func(o *OwnerSettings) { o.OutputBytes = 4095 }, func(o *OwnerSettings) { o.OutputBytes = 2097153 }, func(o *OwnerSettings) { o.ConnectionExpirySeconds = 3599 }, func(o *OwnerSettings) { o.ConnectionExpirySeconds = maxRetentionSeconds + 1 }, func(o *OwnerSettings) { o.History = &HistoryPolicy{Mode: "for", Seconds: 0} }, func(o *OwnerSettings) { o.History = &HistoryPolicy{Mode: "for", Seconds: maxRetentionSeconds + 1} }} {
		object(t, f.req("PATCH", "/api/settings", f.origin, "", "", settingsBody(f, change), true), 400)
	}
	body := settingsBody(f, func(o *OwnerSettings) {
		o.MaximumRequestSeconds = 3600
		o.OutputBytes = 2 << 20
		o.ConnectionExpirySeconds = maxRetentionSeconds
	})
	object(t, f.req("PATCH", "/api/settings", f.origin, "", "", body, false), 401)
	object(t, f.req("PATCH", "/api/settings", "http://localhost:47321", "", "", body, true), 403)
	object(t, f.req("PATCH", "/api/settings", f.origin, "", "", body, true), 200)
	object(t, f.req("PATCH", "/api/settings", f.origin, "", "", map[string]any{"settings": body["settings"], "expectedRevision": 0}, true), 409)
	lease := f.lease(t, "https://settings.example", token)
	for i, timeout := range []int{0, 1, 3600} {
		in := map[string]any{"kind": "connection-test"}
		if timeout != 0 {
			in["timeoutSeconds"] = timeout
		}
		id := f.job(t, "https://settings.example", token, lease, []string{"omitted-timeout", "one-second-timeout", "one-hour-timeout"}[i], in)
		want := timeout
		if want == 0 {
			want = 3600
		}
		if findJob(&f.a.state, id).Request.TimeoutSeconds != want {
			t.Fatal("effective timeout", timeout)
		}
	}
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]any{"overrides": ConnectionOverrides{MaximumRequestSeconds: ptrInt(7), OutputBytes: ptrInt(4096), ConnectionExpirySeconds: ptrInt(0), MuteNotifications: true}}, true), 200)
	if f.a.textLimit(f.a.state.Pairings[pair]) != 4096 || f.a.maximumRequestSeconds(f.a.state.Pairings[pair]) != 7 {
		t.Fatal("override precedence")
	}
	id := f.job(t, "https://settings.example", token, lease, "capped-request-time", map[string]any{"timeoutSeconds": 3600})
	if findJob(&f.a.state, id).Request.TimeoutSeconds != 7 {
		t.Fatal("request was not capped")
	}
	// A changed owner limit cannot turn a same-key retry into different work.
	saveSettings(t, f, func(o *OwnerSettings) { o.MaximumRequestSeconds = 1 })
	retry := object(t, f.req("POST", "/v2/jobs", "https://settings.example", token, lease, map[string]any{"kind": "connection-test", "idempotencyKey": "capped-request-time", "timeoutSeconds": 3600}, false), 200)
	if retry["id"] != id {
		t.Fatal("retry changed identity")
	}
	for _, o := range []ConnectionOverrides{{MaximumRequestSeconds: ptrInt(0)}, {MaximumRequestSeconds: ptrInt(3601)}, {OutputBytes: ptrInt(0)}, {OutputBytes: ptrInt(2097153)}, {ConnectionExpirySeconds: ptrInt(1)}} {
		object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]any{"overrides": o}, true), 400)
	}
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]any{"overrides": ConnectionOverrides{}}, true), 200)
	if f.a.textLimit(f.a.state.Pairings[pair]) != 2<<20 || f.a.maximumRequestSeconds(f.a.state.Pairings[pair]) != 1 {
		t.Fatal("reset to global")
	}
}
func ptrInt(n int) *int { return &n }
func TestOwnerGlobalRoutingAndOptions(t *testing.T) {
	f := setup(t)
	pair, _ := f.pair(t, "app", "https://routing-settings.example", "user", false)
	p := f.a.state.Pairings[pair]
	p.Groups = []ActionGroup{{ID: "ai", Kind: "ai", Name: "AI"}, {ID: "ai.child", ParentID: "ai", Kind: "ai", Name: "Child"}}
	if _, err := f.a.resolvePlan(p, JobRequest{Kind: "action"}); errorCode(err) != "routing_required" {
		t.Fatal(err)
	}
	saveSettings(t, f, func(o *OwnerSettings) {
		o.Default = &ExecutionTarget{Provider: "codex", Model: "global-model", Reasoning: "high"}
	})
	p = f.a.state.Pairings[pair]
	plan, e := f.a.resolvePlan(p, JobRequest{Kind: "action", Group: "ai.child"})
	if e != nil || plan.Provider != "codex" || plan.Model != "global-model" || plan.Reasoning != "high" {
		t.Fatal(plan, e)
	}
	// Partial choices inherit global options. Explicit program choices remain whole targets.
	routeApp(t, f, pair, Routing{Default: &ExecutionTarget{InheritOptions: true, Model: "connection-model"}, Groups: map[string]ExecutionTarget{"ai": {InheritOptions: true, Reasoning: "low"}, "ai.child": {InheritOptions: true, Model: "workflow-model"}}})
	p = f.a.state.Pairings[pair]
	plan, e = f.a.resolvePlan(p, JobRequest{Kind: "action", Group: "ai.child"})
	if e != nil || plan.Model != "workflow-model" || plan.Reasoning != "low" || plan.Provider != "codex" {
		t.Fatal(plan, e)
	}
	routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Provider: "claude"}})
	plan, e = f.a.resolvePlan(f.a.state.Pairings[pair], JobRequest{Kind: "action"})
	if e != nil || plan.Provider != "claude" || plan.Model != "" {
		t.Fatal(plan, e)
	}
}
func TestOwnerOneHourExecutionApprovalAndDelivery(t *testing.T) {
	f := setup(t)
	saveSettings(t, f, func(o *OwnerSettings) { o.MaximumRequestSeconds = 3600; o.ConnectionExpirySeconds = 3600 })
	pair, token := f.pair(t, "app", "https://long.example", "user", false)
	lease := f.lease(t, "https://long.example", token)
	id := f.job(t, "https://long.example", token, lease, "long-clock-request", nil)
	f.now = f.now.Add(9 * time.Minute)
	f.a.leases[lease] = Lease{PairingID: pair, ExpiresAt: f.now.Add(LeaseTTL)}
	f.tick(t)
	if findJob(&f.a.state, id).Status != "awaiting_approval" {
		t.Fatal("approval consumed execution budget")
	}
	object(t, f.req("POST", "/api/jobs/"+id+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	f.tick(t)
	j := findJob(&f.a.state, id)
	j.Plan = &ExecutionPlan{Kind: "text", ExecutionTarget: ExecutionTarget{Provider: "codex"}}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	close(stopped)
	f.a.commands[id] = &commandRun{ctx: ctx, cancel: cancel, stopped: stopped}
	sessionID := secret()
	j.Request.ConversationID = sessionID
	f.a.state.Conversations[sessionID] = &Conversation{ID: sessionID, PairingID: pair, LeaseID: lease, Plan: j.Plan, Status: "open", LastJobID: id, CreatedAt: f.now, UpdatedAt: f.now}
	started := f.now
	for i := 1; i <= 60; i++ {
		f.now = started.Add(time.Duration(i)*time.Minute - time.Second)
		f.a.leases[lease] = Lease{PairingID: pair, ExpiresAt: f.now.Add(LeaseTTL)}
		f.tick(t)
		if findJob(&f.a.state, id).Status != "running" || f.a.state.Conversations[sessionID].Status != "open" || f.a.state.Pairings[pair].Revoked {
			t.Fatal("one-hour path expired early", i)
		}
		object(t, f.req("GET", "/v2/jobs/"+id, "https://long.example", token, "", nil, false), 200)
	}
	if remaining := executionRemaining(findJob(&f.a.state, id), f.now); remaining != time.Second {
		t.Fatal(remaining)
	}
	f.now = started.Add(time.Hour)
	f.a.leases[lease] = Lease{PairingID: pair, ExpiresAt: f.now.Add(LeaseTTL)}
	f.tick(t)
	result := object(t, f.req("GET", "/v2/jobs/"+id, "https://long.example", token, "", nil, false), 200)
	if result["status"] != "timed_out" || result["code"] != "deadline_exceeded" {
		t.Fatal(result)
	}
	if f.a.pairingExpiresAt(&f.a.state, f.a.state.Pairings[pair]) == nil {
		t.Fatal("idle clock not restarted on completion")
	}
}
func TestOwnerRetentionExpiryArtifactsRestartAndMigration(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\nactions: []\n")
	f.now = time.Now().UTC()
	session := f.a.sessions[f.sessionID]
	session.Expires = f.now.Add(12 * time.Hour)
	f.a.sessions[f.sessionID] = session
	pair, token := f.pair(t, "app", "https://retention.example", "user", false)
	lease := f.lease(t, "https://retention.example", token)
	id := f.job(t, "https://retention.example", token, lease, "retention-request", nil)
	finishHistoryJob(t, f, id, "completed")
	art := filepath.Join(f.a.dir, "artifacts", id, "fixture")
	if e := os.MkdirAll(filepath.Dir(art), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(art, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	body := settingsBody(f, func(o *OwnerSettings) {
		o.History = &HistoryPolicy{Mode: "for", Seconds: 2}
		o.MaximumRequestSeconds = 3600
		o.OutputBytes = 4096
		o.ConnectionExpirySeconds = 3600
		o.Default = &ExecutionTarget{Provider: "claude"}
	})
	body["confirmDiscard"] = false
	object(t, f.req("PATCH", "/api/settings", f.origin, "", "", body, true), 400)
	body["confirmDiscard"] = true
	object(t, f.req("PATCH", "/api/settings", f.origin, "", "", body, true), 200)
	if findJob(&f.a.state, id) == nil {
		t.Fatal("retention did not count from completion")
	}
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]any{"overrides": ConnectionOverrides{MuteNotifications: true, MaximumRequestSeconds: ptrInt(3)}, "requestHistory": "for", "historySeconds": 1, "confirmDiscard": true}, true), 200)
	// Write an already-expired finish time, then open without a maintenance tick.
	j := findJob(&f.a.state, id)
	old := f.now.Add(-3 * time.Second)
	j.FinishedAt = &old
	if e := f.a.save(&f.a.state); e != nil {
		t.Fatal(e)
	}
	identity := publicKey(f.a.identity)
	f.a.Close()
	reopened, e := Open(f.a.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if findJob(&reopened.state, id) != nil {
		t.Fatal("expired record survived restart")
	}
	if _, e := os.Stat(art); !os.IsNotExist(e) {
		t.Fatal("expired artifacts survived", e)
	}
	o := ownerSettings(&reopened.state)
	p := reopened.state.Pairings[pair]
	if o.MaximumRequestSeconds != 3600 || o.Default.Provider != "claude" || o.OutputBytes != 4096 || o.History.Seconds != 2 || p.HistorySeconds != 1 || !p.Overrides.MuteNotifications || *p.Overrides.MaximumRequestSeconds != 3 || publicKey(reopened.identity) != identity {
		t.Fatal("restart lost owner settings or identity")
	}
	reopened.now = func() time.Time { return f.now.Add(time.Hour + time.Second) }
	if e := reopened.tick(); e != nil {
		t.Fatal(e)
	}
	if !reopened.state.Pairings[pair].Revoked || reopened.state.Pairings[pair].ClientPublicKey != "" {
		t.Fatal("idle expiry did not disconnect")
	}
	// Legacy boolean and per-connection strings migrate without changing opt-out semantics.
	for _, keep := range []bool{true, false} {
		a, e := Open(t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		a.state.OwnerSettings = nil
		a.state.KeepRequestHistory = &keep
		a.state.Pairings["legacy"] = &Pairing{RequestHistory: "discard", Revoked: true}
		if e = a.save(&a.state); e != nil {
			t.Fatal(e)
		}
		dir := a.dir
		a.Close()
		a, e = Open(dir)
		if e != nil {
			t.Fatal(e)
		}
		if globalRequestHistory(&a.state) != keep || a.state.OwnerSettings.MaximumRequestSeconds != 600 || a.state.Pairings["legacy"].RequestHistory != "discard" {
			t.Fatal("legacy migration")
		}
		a.Close()
	}
}
func TestOwnerTimedHistoryTickAndOverrideConfirmation(t *testing.T) {
	f := setup(t)
	pair, token := f.pair(t, "app", "https://timed.example", "user", false)
	lease := f.lease(t, "https://timed.example", token)
	saveSettings(t, f, func(o *OwnerSettings) { o.History = &HistoryPolicy{Mode: "for", Seconds: 2} })
	id := f.job(t, "https://timed.example", token, lease, "timed-retain", nil)
	finishHistoryJob(t, f, id, "completed")
	f.advance(t, time.Second)
	if findJob(&f.a.state, id) == nil {
		t.Fatal("removed too early")
	}
	payload := map[string]any{"requestHistory": "for", "historySeconds": 1}
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", payload, true), 400)
	payload["confirmDiscard"] = true
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", payload, true), 200)
	if findJob(&f.a.state, id) != nil {
		t.Fatal("reduced retention not enforced")
	}
	// Persisted last activity survives clearing all jobs; lease heartbeats are not requests.
	p := f.a.state.Pairings[pair]
	at := *p.LastRequestAt
	f.advance(t, time.Second)
	if !f.a.state.Pairings[pair].LastRequestAt.Equal(at) {
		t.Fatal("maintenance changed activity")
	}
	data, _ := json.Marshal(f.a.remotePairing(f.a.state.Pairings[pair]))
	var remote map[string]any
	json.Unmarshal(data, &remote)
	if remote["historySeconds"] != nil || remote["lastRequestAt"] != nil {
		t.Fatal("owner metadata leaked")
	}
}

type fakeLoginItem struct {
	enabled bool
	writes  int
}

func (f *fakeLoginItem) Status() (bool, error) { return f.enabled, nil }
func (f *fakeLoginItem) Set(on bool) error     { f.enabled = on; f.writes++; return nil }
func TestOwnerLoginThroughAbstraction(t *testing.T) {
	f := setup(t)
	fake := &fakeLoginItem{}
	f.a.loginItem = fake
	object(t, f.req("PATCH", "/api/login", f.origin, "", "", map[string]bool{"enabled": true}, false), 401)
	object(t, f.req("PATCH", "/api/login", f.origin, "", "", map[string]bool{"enabled": true}, true), 200)
	state := object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
	if !state["login"].(map[string]any)["enabled"].(bool) || fake.writes != 1 {
		t.Fatal(state)
	}
	object(t, f.req("PATCH", "/api/login", f.origin, "", "", map[string]bool{"enabled": false}, true), 200)
	if fake.enabled || fake.writes != 2 {
		t.Fatal(fake)
	}
	f.a.loginItem = nil
	object(t, f.req("PATCH", "/api/login", f.origin, "", "", map[string]bool{"enabled": true}, true), 409)
}

func TestOwnerOutputLimitsApplyToCommandAndConfigTimeoutStillCaps(t *testing.T) {
	flood := helperAction(t, "flood", "flood")
	sleep := helperAction(t, "sleep", "sleep")
	sleep.Timeout = 1
	f := commandFixture(t, false, flood, sleep)
	pair, token := f.pair(t, "app", f.origin, "user", true)
	grantCommands(t, f, pair, "flood", "sleep")
	lease := f.lease(t, f.origin, token)
	saveSettings(t, f, func(o *OwnerSettings) { o.MaximumRequestSeconds = 3600; o.OutputBytes = 8192 })
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]any{"overrides": ConnectionOverrides{OutputBytes: ptrInt(4096)}}, true), 200)
	id := f.job(t, f.origin, token, lease, "owner-output-flood", map[string]any{"kind": "command", "action": "flood"})
	j := waitJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
	if j.Code != "output_limit" || len(j.Output) > 4096 || !j.OutputTruncated {
		t.Fatal("effective output limit not enforced", j.Code, len(j.Output))
	}
	id = f.job(t, f.origin, token, lease, "owner-command-timeout", map[string]any{"kind": "command", "action": "sleep", "timeoutSeconds": 3600})
	f.tick(t)
	run := f.a.commands[id]
	deadline, ok := run.ctx.Deadline()
	if !ok || time.Until(deadline) > time.Second {
		t.Fatal("config command deadline lost")
	}
	j = waitJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
	if j.Status != "timed_out" {
		t.Fatal(j.Status)
	}
}
func TestOwnerIdleExpiryOverridesAndOptOutFailureNotices(t *testing.T) {
	f := setup(t)
	pair, token := f.pair(t, "app", f.origin, "user", false)
	lease := f.lease(t, f.origin, token)
	saveSettings(t, f, func(o *OwnerSettings) { o.ConnectionExpirySeconds = 3600; o.History = &HistoryPolicy{Mode: "discard"} })
	p := f.a.state.Pairings[pair]
	if at := f.a.pairingExpiresAt(&f.a.state, p); at == nil || !at.Equal(p.CreatedAt.Add(time.Hour)) {
		t.Fatal("global expiry")
	}
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]any{"overrides": ConnectionOverrides{ConnectionExpirySeconds: ptrInt(0)}}, true), 200)
	if at := f.a.pairingExpiresAt(&f.a.state, f.a.state.Pairings[pair]); at != nil {
		t.Fatal("never override")
	}
	id := f.job(t, f.origin, token, lease, "owner-failure-notice", nil)
	finishHistoryJob(t, f, id, "failed")
	out := object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)
	if len(out["jobs"].([]any)) != 0 || len(out["failedRequests"].([]any)) != 1 {
		t.Fatal("opt-out failure notification missing")
	}
	f.advance(t, time.Minute)
	if len(f.a.pendingFailureNotices()) != 0 {
		t.Fatal("failure notice retained")
	}
}

func TestOwnerLegacyOmittedTimeoutReceiptSurvivesNewDefault(t *testing.T) {
	f := setup(t)
	pair, token := f.pair(t, "app", f.origin, "user", false)
	lease := f.lease(t, f.origin, token)
	request := JobRequest{Kind: "connection-test", Mode: "restricted", TimeoutSeconds: 120, IdempotencyKey: "legacy-omitted-timeout"}
	id := secret()
	j := &Job{ID: id, PairingID: pair, Request: request, Status: "completed", CreatedAt: f.now, FinishedAt: &f.now}
	f.a.state.Jobs = append(f.a.state.Jobs, j)
	if e := f.a.reserveReceipt(digest(pair+":"+request.IdempotencyKey), Receipt{JobID: id, Hash: requestHash(request)}); e != nil {
		t.Fatal(e)
	}
	saveSettings(t, f, func(o *OwnerSettings) { o.MaximumRequestSeconds = 3600 })
	result := object(t, f.req("POST", "/v2/jobs", f.origin, token, lease, map[string]any{"kind": "connection-test", "idempotencyKey": request.IdempotencyKey}, false), 200)
	if result["id"] != id {
		t.Fatal("legacy receipt replayed")
	}
}
