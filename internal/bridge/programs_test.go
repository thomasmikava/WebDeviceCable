package bridge

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only the test executable runs: no provider, credentials or real inference.
func TestAIProgramHelper(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "--program-fixture") {
		return
	}
	profile := os.Getenv("WDC_PROFILE_DIR")
	account, _ := os.ReadFile(filepath.Join(profile, "account"))
	emit := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	if os.Args[len(os.Args)-1] == "--wdc-info" {
		_, err := os.Stat(filepath.Join(profile, "no-sessions"))
		emit(customProgramInfo{Protocol: "wdc-ai-jsonl-v1", Version: "1.2.3", Account: string(account), Sessions: os.IsNotExist(err), Models: []NativeModel{{ID: "fixture", Name: "Fixture", Reasoning: []NativeReasoning{{Effort: "high"}}}}})
		os.Exit(0)
	}
	if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("NODE_OPTIONS") != "" {
		os.Exit(91)
	}
	history := []string{}
	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		var turn struct {
			ID        string `json:"id"`
			Prompt    string `json:"prompt"`
			Model     string `json:"model"`
			Reasoning string `json:"reasoning"`
		}
		if json.Unmarshal(reader.Bytes(), &turn) != nil {
			os.Exit(92)
		}
		if turn.Prompt == "HANG" {
			time.Sleep(time.Minute)
			continue
		}
		history = append(history, turn.Prompt)
		data, _ := json.Marshal(map[string]any{"profile": profile, "history": history, "pid": os.Getpid(), "model": turn.Model, "reasoning": turn.Reasoning})
		emit(map[string]any{"v": 1, "type": "output", "id": turn.ID, "delta": string(data)})
		time.Sleep(30 * time.Millisecond)
		emit(map[string]any{"v": 1, "type": "done", "id": turn.ID})
	}
	os.Exit(0)
}
func customFixture(t *testing.T, f *fixture, name, account string) AIProgram {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	if err = os.WriteFile(filepath.Join(profile, "account"), []byte(account), 0600); err != nil {
		t.Fatal(err)
	}
	p := AIProgram{ID: "program-" + secret()[:24], Name: name, Type: "custom", Executable: exe, Arguments: []string{"-test.run=^TestAIProgramHelper$", "--", "--program-fixture"}, ProfileDir: profile}
	if err = f.a.editProgram(p); err != nil {
		t.Fatal(err)
	}
	status := checkProgram(f.a.dir, p)
	if !status.Execution || status.Account != account {
		t.Fatalf("custom check: %+v", status)
	}
	f.a.native = append(f.a.native, status)
	return p
}
func grantProgram(t *testing.T, f *fixture, pair string, programs ...AIProgram) {
	t.Helper()
	grants := map[string]string{}
	for _, p := range programs {
		grants[p.ID] = p.hash()
	}
	object(t, f.req("PUT", "/api/pairings/"+pair+"/program-grants", f.origin, "", "", map[string]any{"programs": grants, "commandPermissionsAccepted": true}, true), 200)
}
func TestNamedProgramsExecuteSeparateProfilesAndPinAccounts(t *testing.T) {
	f := setup(t)
	t.Setenv("OPENAI_API_KEY", "must-not-reach-harness")
	t.Setenv("NODE_OPTIONS", "must-not-reach-harness")
	work := customFixture(t, f, "Work Qwen", "work-account")
	personal := customFixture(t, f, "Personal Qwen", "personal-account")
	origin := "https://programs.example"
	pair, token := f.pair(t, "Programs", origin, "owner", true)
	lease := f.lease(t, origin, token)
	grantProgram(t, f, pair, work, personal)
	p := f.a.state.Pairings[pair]
	p.Groups = []ActionGroup{{ID: "chat", Name: "Chat", Kind: "ai"}, {ID: "review", Name: "Review", Kind: "ai"}}
	p.Routing = Routing{Default: &ExecutionTarget{Provider: work.ID, Model: "fixture", Reasoning: "high"}, Groups: map[string]ExecutionTarget{"review": {Provider: personal.ID}}}
	c := createConversation(t, f, origin, token, lease, map[string]any{"group": "chat"})
	first := conversationTurnJob(t, f, origin, token, lease, c, "named-first-turn", "First")
	job := waitClaudeJob(t, f, first, func(j *Job) bool { return terminal(j.Status) })
	if job.Status != "completed" || job.Execution == nil || job.Execution.Kind != "custom" || job.Execution.ProgramID != work.ID || job.Execution.Name != work.Name || job.Billing != "program_defined" {
		t.Fatalf("wrong custom execution: %+v", job)
	}
	var one struct {
		Profile          string
		History          []string
		PID              int
		Model, Reasoning string
	}
	if err := json.Unmarshal([]byte(job.Output), &one); err != nil {
		t.Fatal(err)
	}
	if one.Profile != work.ProfileDir || one.Model != "fixture" || one.Reasoning != "high" {
		t.Fatalf("selected configuration ignored: %+v", one)
	}
	// A rename changes neither the selection nor the live conversation.
	work.Name = "Renamed work"
	f.a.mu.Lock()
	err := f.a.editProgram(work)
	f.a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	second := conversationTurnJob(t, f, origin, token, lease, c, "named-second-turn", "Second")
	job = waitClaudeJob(t, f, second, func(j *Job) bool { return terminal(j.Status) })
	var two struct {
		Profile string
		History []string
		PID     int
	}
	_ = json.Unmarshal([]byte(job.Output), &two)
	if job.Status != "completed" || two.PID != one.PID || len(two.History) != 2 {
		t.Fatalf("rename broke continuing conversation: %+v", job)
	}
	other := f.job(t, origin, token, lease, "named-other-program", map[string]any{"kind": "action", "group": "review", "prompt": "Personal"})
	job = waitClaudeJob(t, f, other, func(j *Job) bool { return terminal(j.Status) })
	var personalResult struct{ Profile string }
	if err := json.Unmarshal([]byte(job.Output), &personalResult); err != nil {
		t.Fatal(err)
	}
	if job.Status != "completed" || personalResult.Profile != personal.ProfileDir {
		t.Fatalf("second program ignored: %+v", job)
	}
	p = f.a.state.Pairings[pair]
	if len(p.NativeAccounts) != 2 || p.NativeAccounts[work.ID] == p.NativeAccounts[personal.ID] {
		t.Fatal("accounts were not pinned per program")
	}
	if err = os.WriteFile(filepath.Join(work.ProfileDir, "account"), []byte("different-account"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := conversationTurnJob(t, f, origin, token, lease, c, "named-account-changed", "Must not execute")
	job = waitClaudeJob(t, f, changed, func(j *Job) bool { return terminal(j.Status) })
	if job.Code != "native_account_changed" || job.Output != "" || job.Billing != "none" {
		t.Fatalf("silently switched accounts: %+v", job)
	}
	// Another program's binding and execution remain usable.
	other = f.job(t, origin, token, lease, "named-other-still-valid", map[string]any{"kind": "action", "group": "review", "prompt": "Still personal"})
	if job = waitClaudeJob(t, f, other, func(j *Job) bool { return terminal(j.Status) }); job.Status != "completed" {
		t.Fatalf("other entry affected: %+v", job)
	}
}
func TestCustomCapabilitiesGrantsAndUnsupportedInputs(t *testing.T) {
	f := setup(t)
	program := customFixture(t, f, "Harness", "fixture-owner")
	origin := "https://caps.example"
	pair, token := f.pair(t, "Capabilities", origin, "owner", true)
	lease := f.lease(t, origin, token)
	request := JobRequest{Kind: "text", Provider: program.ID, Prompt: "Test", IdempotencyKey: "unsupported-test"}
	p := f.a.state.Pairings[pair]
	if _, _, err := f.a.createJob(p, request, lease); errorCode(err) != "program_not_granted" {
		t.Fatalf("missing OS consent: %v", err)
	}
	object(t, f.req("PUT", "/api/pairings/"+pair+"/program-grants", f.origin, "", "", map[string]any{"programs": map[string]string{program.ID: program.hash()}}, true), 400)
	grantProgram(t, f, pair, program)
	p = f.a.state.Pairings[pair]
	for _, cap := range f.a.capabilities(p) {
		if cap.ID == program.ID && (!cap.Execution || !cap.Text || !cap.Streaming || !cap.Sessions || cap.JSON || cap.ImageInput || cap.AppTools || cap.Workspace || cap.Isolation != "os-account") {
			t.Fatalf("false capability claims: %+v", cap)
		}
	}
	for _, mode := range []string{"app-tools", "workspace"} {
		r := request
		r.Mode = mode
		if _, _, err := f.a.createJob(p, r, lease); errorCode(err) != "unsupported_feature" {
			t.Fatalf("allowed %s: %v", mode, err)
		}
	}
	r := request
	r.OutputSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false,"required":[]}`)
	if _, _, err := f.a.createJob(p, r, lease); errorCode(err) != "unsupported_feature" {
		t.Fatalf("allowed JSON: %v", err)
	}
	r = request
	r.Inputs = []string{secret()}
	if _, _, err := f.a.createJob(p, r, lease); errorCode(err) != "unsupported_feature" {
		t.Fatalf("allowed files: %v", err)
	}
	for _, feature := range []string{"json", "imageInput", "imageGeneration", "appTools", "documentInput"} {
		admission, err := f.a.preflight(p, AdmissionRequest{Request: request, Features: []string{feature}})
		if err != nil || admission.Available || admission.Code != "unsupported_feature" {
			t.Fatalf("bad admission for %s: %+v %v", feature, admission, err)
		}
	}
	if len(f.a.state.Jobs) != 0 {
		t.Fatal("unsupported requests consumed inference")
	}
	// Revoking the grant ends a running process and its conversation.
	c := createConversation(t, f, origin, token, lease, map[string]any{"provider": program.ID})
	id := conversationTurnJob(t, f, origin, token, lease, c, "custom-revoke-running", "HANG")
	waitClaudeJob(t, f, id, func(j *Job) bool { return j.Status == "running" })
	grantProgram(t, f, pair)
	job := waitClaudeJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
	if job.Code != "program_access_changed" || f.a.state.Conversations[c].Status != "closed" {
		t.Fatal("custom grant revocation did not cancel")
	}
}
func TestProgramEditsRemovalAndUpgradeCompatibility(t *testing.T) {
	f := setup(t)
	pair, _ := f.pair(t, "Legacy", "https://legacy.example", "owner", false)
	p := f.a.state.Pairings[pair]
	p.Routing = Routing{Default: &ExecutionTarget{Provider: "codex"}, Groups: map[string]ExecutionTarget{"review": {Provider: "claude"}}}
	p.NativeAccounts = map[string]string{"codex": "original-account"}
	// Simulate an older state file with no AI-program configuration.
	f.a.state.Programs = nil
	if err := f.a.save(&f.a.state); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.a = next
	t.Cleanup(func() { _ = next.Close() })
	if len(next.programList()) != 2 || next.state.Pairings[pair].Routing.Default.Provider != "codex" || next.state.Pairings[pair].NativeAccounts["codex"] != "original-account" {
		t.Fatal("upgrade changed legacy selection or binding")
	}
	defaultProgram, _ := next.program("codex")
	defaultProgram.Executable = filepath.Join(t.TempDir(), "other-codex")
	defaultProgram.ProfileDir = t.TempDir()
	if err = next.editProgram(defaultProgram); err != nil {
		t.Fatal(err)
	}
	if next.state.Pairings[pair].Routing.Default.Provider != "codex" {
		t.Fatal("overriding default required reselection")
	}
	// A configuration revision pins queued jobs and conversations; a rename doesn't.
	program := AIProgram{ID: "program-fixture", Name: "Named Codex", Type: "codex"}
	if err = next.editProgram(program); err != nil {
		t.Fatal(err)
	}
	plan, err := next.resolvePlan(next.state.Pairings[pair], JobRequest{Kind: "text", Provider: program.ID})
	if err != nil {
		t.Fatal(err)
	}
	next.state.Conversations["fixture"] = &Conversation{ID: "fixture", Status: "open", Plan: plan, PairingID: pair}
	next.state.Jobs = append(next.state.Jobs, &Job{ID: "queued", Plan: plan, Status: "queued", PairingID: pair})
	program.ProfileDir = t.TempDir()
	if err = next.editProgram(program); err != nil {
		t.Fatal(err)
	}
	if next.state.Conversations["fixture"].Code != "program_changed" || findJob(&next.state, "queued").Code != "program_changed" {
		t.Fatal("configuration edit kept old execution alive")
	}
	if _, err = next.programForJob("queued"); err == nil {
		t.Fatal("rebound queued job")
	}
	if len(next.programUses("codex")) != 1 {
		t.Fatal("missing affected default selection")
	}
}
func TestProgramManagementRequiresLocalPanel(t *testing.T) {
	f := setup(t)
	origin := "https://untrusted.example"
	_, token := f.pair(t, "Untrusted", origin, "owner", true)
	object(t, f.req("POST", "/api/programs", origin, token, "", AIProgram{Name: "Injected", Type: "codex"}, false), 403)
	created := object(t, f.req("POST", "/api/programs", f.origin, "", "", AIProgram{Name: "Named", Type: "codex"}, true), 201)
	id := created["id"].(string)
	if !strings.HasPrefix(id, "program-") {
		t.Fatal("unstable ID")
	}
	pair, _ := f.pair(t, "Uses", "https://uses.example", "owner", false)
	f.a.state.Pairings[pair].Routing.Default = &ExecutionTarget{Provider: id}
	object(t, f.req("DELETE", "/api/programs/"+id, f.origin, "", "", nil, true), 409)
	object(t, f.req("DELETE", "/api/programs/"+id+"?leaveUnavailable=true", f.origin, "", "", nil, true), 200)
	if f.a.state.Pairings[pair].Routing.Default.Provider != id {
		t.Fatal("removal silently reassigned selection")
	}
	if err := f.a.nativeAvailable(f.a.state.Pairings[pair], id, "text"); errorCode(err) != "program_unavailable" {
		t.Fatalf("removal silently fell back: %v", err)
	}
}

func TestNativeEntriesUseTheirOwnExecutableAndProfile(t *testing.T) {
	f, root := claudeFixture(t)
	executable := filepath.Join(root, "claude")
	if strings.HasSuffix(os.Args[0], ".exe") {
		executable += ".exe"
	}
	programs := []AIProgram{}
	for _, name := range []string{"Work", "Personal"} {
		profile := t.TempDir()
		if err := os.WriteFile(filepath.Join(profile, "account"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		p := AIProgram{ID: "program-" + strings.ToLower(name), Name: name + " Claude", Type: "claude", Executable: executable, ProfileDir: profile}
		if err := f.a.editProgram(p); err != nil {
			t.Fatal(err)
		}
		status := checkProgram(f.a.dir, p)
		if !status.Execution || status.Integration != "claude" || !strings.Contains(status.accountKey, name) {
			t.Fatalf("per-profile check failed: %+v", status)
		}
		f.a.native = append(f.a.native, status)
		programs = append(programs, p)
	}
	origin := "https://native-programs.example"
	pair, token := f.pair(t, "Native programs", origin, "owner", true)
	lease := f.lease(t, origin, token)
	for i, p := range programs {
		id := f.job(t, origin, token, lease, "native-profile-"+p.ID, map[string]any{"kind": "text", "provider": p.ID, "prompt": "Hello"})
		job := waitClaudeJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
		var result struct{ Profile string }
		if err := json.Unmarshal([]byte(job.Output), &result); err != nil {
			t.Fatal(err)
		}
		if job.Status != "completed" || job.Execution == nil || job.Execution.Provider != "claude" || job.Execution.ProgramID != p.ID || job.Execution.Name != p.Name || result.Profile != p.ProfileDir {
			t.Fatalf("wrong native entry %d: %+v", i, job)
		}
	}
	accounts := f.a.state.Pairings[pair].NativeAccounts
	if accounts[programs[0].ID] == accounts[programs[1].ID] || len(accounts) != 2 {
		t.Fatal("native entries shared an account binding")
	}
}

func TestCustomWithoutSessionsAndDefaultRemovalPersistence(t *testing.T) {
	f := setup(t)
	p := customFixture(t, f, "One-shot", "fixture")
	if err := os.WriteFile(filepath.Join(p.ProfileDir, "no-sessions"), []byte("true"), 0600); err != nil {
		t.Fatal(err)
	}
	f.a.dropProgramStatus(p.ID)
	f.a.native = append(f.a.native, checkProgram(f.a.dir, p))
	origin := "https://one-shot.example"
	pair, token := f.pair(t, "One-shot", origin, "owner", true)
	lease := f.lease(t, origin, token)
	grantProgram(t, f, pair, p)
	object(t, f.req("POST", "/v2/conversations", origin, token, lease, map[string]string{"provider": p.ID}, false), 400)
	id := f.job(t, origin, token, lease, "custom-one-shot", map[string]any{"kind": "text", "provider": p.ID, "prompt": "Hello"})
	if job := waitClaudeJob(t, f, id, func(j *Job) bool { return terminal(j.Status) }); job.Status != "completed" {
		t.Fatalf("one-shot failed: %+v", job)
	}
	for _, entry := range f.a.programList() {
		object(t, f.req("DELETE", "/api/programs/"+entry.ID+"?leaveUnavailable=true", f.origin, "", "", nil, true), 200)
	}
	if err := f.a.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.a = next
	t.Cleanup(func() { _ = next.Close() })
	f.handler = next.Handler("127.0.0.1:47321", nil)
	if len(next.programList()) != 0 {
		t.Fatal("removed defaults silently reappeared after restart")
	}
	if err = next.editProgram(defaultPrograms()["codex"]); err != nil {
		t.Fatal(err)
	}
	if restored, ok := next.program("codex"); !ok || restored.Executable != "" || restored.ProfileDir != "" {
		t.Fatal("default did not restore automatic detection")
	}
}

func TestProgramProfileEnvironmentIsIndependent(t *testing.T) {
	t.Setenv("CODEX_HOME", "ambient-codex")
	t.Setenv("CLAUDE_CONFIG_DIR", "ambient-claude")
	t.Setenv("OPENAI_API_KEY", "never-forward")
	for _, kind := range []string{"codex", "claude", "custom"} {
		p := AIProgram{Type: kind, ProfileDir: t.TempDir()}
		env := map[string]string{}
		for _, entry := range programEnv(p) {
			key, value, _ := strings.Cut(entry, "=")
			env[key] = value
		}
		key := map[string]string{"codex": "CODEX_HOME", "claude": "CLAUDE_CONFIG_DIR", "custom": "WDC_PROFILE_DIR"}[kind]
		if env[key] != p.ProfileDir || env["OPENAI_API_KEY"] != "" {
			t.Fatalf("incorrect profile environment for %s: %+v", kind, env)
		}
	}
}

func TestCustomRechecksCapabilitiesBeforeEveryTurn(t *testing.T) {
	f := setup(t)
	program := customFixture(t, f, "Changing harness", "owner")
	origin := "https://changing-harness.example"
	pair, token := f.pair(t, "Changing", origin, "owner", true)
	lease := f.lease(t, origin, token)
	grantProgram(t, f, pair, program)
	c := createConversation(t, f, origin, token, lease, map[string]any{"provider": program.ID})
	first := conversationTurnJob(t, f, origin, token, lease, c, "changing-cap-first", "Hello")
	if job := waitClaudeJob(t, f, first, func(j *Job) bool { return terminal(j.Status) }); job.Status != "completed" {
		t.Fatal(job.Code)
	}
	if err := os.WriteFile(filepath.Join(program.ProfileDir, "no-sessions"), []byte("true"), 0600); err != nil {
		t.Fatal(err)
	}
	next := conversationTurnJob(t, f, origin, token, lease, c, "changing-cap-second", "Must not execute")
	job := waitClaudeJob(t, f, next, func(j *Job) bool { return terminal(j.Status) })
	if job.Code != "sessions_unsupported" || job.Output != "" || job.Billing != "none" {
		t.Fatalf("used stale custom capabilities: %+v", job)
	}
}
