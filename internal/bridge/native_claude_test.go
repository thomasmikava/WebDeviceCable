package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Re-executed test binary stands in for the installed CLI. These tests never
// contact a provider and exercise the real process/permission/session path.
func TestMain(m *testing.M) {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "claude" {
		claudeFixtureMain()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func claudeFixtureMain() {
	root := filepath.Dir(os.Args[0])
	config := filepath.Join(root, "config")
	if configured := os.Getenv("CLAUDE_CONFIG_DIR"); configured != "" {
		config = configured
	}
	emit := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }
	value := func(flag string) string {
		for i, arg := range os.Args {
			if arg == flag && i+1 < len(os.Args) {
				return os.Args[i+1]
			}
		}
		return ""
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(claudeMinimumVersion)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Println("--effort (low, medium, high)")
		return
	}
	isAuth := false
	for i, arg := range os.Args {
		if arg == "auth" && i+1 < len(os.Args) && os.Args[i+1] == "status" {
			isAuth = true
		}
	}
	if isAuth {
		identity, _ := os.ReadFile(filepath.Join(config, "account"))
		if len(identity) == 0 {
			identity, _ = os.ReadFile(filepath.Join(root, "identity"))
		}
		if len(identity) == 0 {
			identity = []byte("fixture-owner")
		}
		emit(map[string]any{"loggedIn": true, "authMethod": "claude.ai", "email": "fixture@example.test", "orgId": string(identity), "subscriptionType": "max", "configDirectory": config})
		return
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "NODE_OPTIONS"} {
		if os.Getenv(key) != "" {
			os.Exit(93)
		}
	}
	for _, flag := range []string{"--restricted", "--safe-mode", "--no-session-persistence", "--disable-slash-commands", "--strict-mcp-config", "--no-chrome"} {
		found := false
		for _, arg := range os.Args {
			found = found || arg == flag
		}
		if !found {
			os.Exit(94)
		}
	}
	if value("--tools") != "" || value("--disallowedTools") != "mcp__*" || value("--permission-mode") != "dontAsk" || value("--permission-prompts") != "none" {
		os.Exit(95)
	}
	id, mode := value("--session-id"), value("--model")
	dir, _ := os.Getwd()
	prompts := []string{}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		var message struct {
			Type    string `json:"type"`
			Session string `json:"session_id"`
			Message struct {
				Role    string        `json:"role"`
				Content []claudeBlock `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil || message.Session != id || message.Type != "user" || message.Message.Role != "user" || len(message.Message.Content) == 0 {
			os.Exit(96)
		}
		prompt := message.Message.Content[0].Text
		prompts = append(prompts, prompt)
		if prompt == "HANG" {
			for {
				time.Sleep(time.Second)
			}
		}
		if mode == "malformed" {
			fmt.Println("not json")
			continue
		}
		tools := []string{}
		mcp := []any{}
		if mode == "badtools" {
			tools = []string{"Bash"}
		}
		if mode == "badmcp" {
			mcp = []any{map[string]string{"name": "unrelated"}}
		}
		emit(map[string]any{"type": "system", "subtype": "ui_invalidate", "message": "Sign-in state refreshed", "session_id": id})
		emit(map[string]any{"type": "system", "subtype": "init", "session_id": id, "cwd": dir, "claude_code_version": claudeMinimumVersion, "model": "fixture-native-model", "permissionMode": "dontAsk", "apiKeySource": "none", "tools": tools, "mcp_servers": mcp, "skills": []string{}})
		if mode == "cross" {
			id = "another-session"
		}
		if mode == "hook" {
			emit(map[string]any{"type": "system", "subtype": "hook_started", "session_id": id})
			continue
		}
		if mode == "tooluse" {
			emit(map[string]any{"type": "stream_event", "session_id": id, "event": map[string]any{"type": "content_block_start", "content_block": map[string]string{"type": "tool_use"}}})
			continue
		}
		if mode == "limit" {
			emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": "Rate limit reached", "api_error_status": 429, "session_id": id})
			continue
		}
		data, _ := json.Marshal(map[string]any{"prompts": prompts, "pid": os.Getpid(), "cwd": dir, "profile": config})
		if mode == "oversized" {
			data = []byte(strings.Repeat("x", 18000))
		}
		emit(map[string]any{"type": "stream_event", "session_id": id, "event": map[string]any{"type": "content_block_delta", "delta": map[string]string{"type": "text_delta", "text": string(data[:len(data)/2])}}})
		time.Sleep(120 * time.Millisecond)
		emit(map[string]any{"type": "stream_event", "session_id": id, "event": map[string]any{"type": "content_block_delta", "delta": map[string]string{"type": "text_delta", "text": string(data[len(data)/2:])}}})
		emit(map[string]any{"type": "assistant", "session_id": id, "parent_tool_use_id": nil, "message": map[string]any{"model": "fixture-native-model", "content": []any{map[string]string{"type": "text", "text": string(data)}}}})
		emit(map[string]any{"type": "result", "subtype": "success", "session_id": id, "is_error": false, "result": string(data), "usage": map[string]int{"input_tokens": 4, "cache_read_input_tokens": 3, "output_tokens": 7}})
	}
}

func claudeFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	root := t.TempDir()
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, name)
	if err = os.Link(exe, dest); err != nil {
		src, e := os.Open(exe)
		if e != nil {
			t.Fatal(e)
		}
		defer src.Close()
		out, e := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY, 0700)
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(out, src)
		out.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	if err = os.Mkdir(filepath.Join(root, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "config"))
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := setup(t)
	f.a.native = []NativeStatus{{ID: "claude", Execution: true, Installed: true, Authentication: "native_subscription", Version: claudeMinimumVersion}}
	return f, root
}

func waitClaudeJob(t *testing.T, f *fixture, id string, condition func(*Job) bool) *Job {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		f.tick(t)
		f.a.mu.Lock()
		data, err := json.Marshal(findJob(&f.a.state, id))
		f.a.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		var snapshot Job
		if err = json.Unmarshal(data, &snapshot); err != nil {
			t.Fatal(err)
		}
		if condition(&snapshot) {
			return &snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Claude fixture did not reach the expected state")
	return nil
}

func TestClaudeOwnedSessionStreamsContinuesAndCancels(t *testing.T) {
	f, _ := claudeFixture(t)
	t.Setenv("ANTHROPIC_API_KEY", "fixture-secret-must-not-reach-cli")
	t.Setenv("ANTHROPIC_BASE_URL", "https://untrusted.invalid")
	origin := "https://chat.example"
	token, pair := f.pair(t, "chat", origin, "alice", false)
	lease := f.lease(t, origin, token)
	c := createConversation(t, f, origin, token, lease, map[string]any{"provider": "claude"})
	first := conversationTurnJob(t, f, origin, token, lease, c, "claude-turn-one", "Hello")
	f.tick(t)
	if len(f.a.commands) != 0 {
		t.Fatal("Claude started without owner consent")
	}
	object(t, f.req("POST", "/api/jobs/"+first+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	partial := waitClaudeJob(t, f, first, func(j *Job) bool { return j.Status == "running" && j.Output != "" })
	if partial.Output == "" {
		t.Fatal("no incremental output")
	}
	job := waitClaudeJob(t, f, first, func(j *Job) bool { return terminal(j.Status) })
	if job.Status != "completed" || job.Execution == nil || job.Execution.Model != "fixture-native-model" || job.Billing != "native_subscription" || job.Usage.Total != 14 {
		t.Fatalf("invalid native result: %+v", job)
	}
	var one, two struct {
		Prompts []string `json:"prompts"`
		PID     int      `json:"pid"`
		CWD     string   `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(job.Output), &one); err != nil {
		t.Fatal(err)
	}
	second := conversationTurnJob(t, f, origin, token, lease, c, "claude-turn-two", "Again")
	object(t, f.req("POST", "/api/jobs/"+second+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	job = waitClaudeJob(t, f, second, func(j *Job) bool { return terminal(j.Status) })
	if job.Status != "completed" {
		t.Fatalf("continuation failed: %+v", job)
	}
	if err := json.Unmarshal([]byte(job.Output), &two); err != nil {
		t.Fatal(err)
	}
	if one.PID != two.PID || one.CWD != two.CWD || len(two.Prompts) != 2 {
		t.Fatal("continuation lost its owned process/context")
	}
	if f.a.state.Pairings[pair].NativeAccounts["claude"] == "" {
		t.Fatal("Claude account was not pinned")
	}
	f.a.state.Sharing = &Sharing{}
	remote := f.a.remoteJob(job)
	if remote.Execution != nil || remote.ActualProvider != "" || remote.ActualModel != "" {
		t.Fatal("execution metadata was not hidden")
	}
	hang := conversationTurnJob(t, f, origin, token, lease, c, "claude-turn-hang", "HANG")
	object(t, f.req("POST", "/api/jobs/"+hang+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	waitClaudeJob(t, f, hang, func(j *Job) bool { return j.Status == "running" })
	runtime := f.a.conversations[c]
	object(t, f.req("POST", "/v2/jobs/"+hang+"/cancel", origin, token, lease, nil, false), 200)
	select {
	case <-runtime.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled native process survived")
	}
	if _, err := os.Stat(one.CWD); !os.IsNotExist(err) {
		t.Fatal("private working directory survived cancellation")
	}
}

func TestClaudeProtocolAndIsolationFailures(t *testing.T) {
	for _, tc := range []struct{ mode, code string }{{"badtools", "native_isolation_unavailable"}, {"badmcp", "native_isolation_unavailable"}, {"cross", "native_protocol_error"}, {"tooluse", "permission_denied"}, {"hook", "permission_denied"}, {"malformed", "native_protocol_error"}, {"limit", "rate_limited"}, {"oversized", "invalid_model_output"}} {
		t.Run(tc.mode, func(t *testing.T) {
			f, _ := claudeFixture(t)
			origin := "https://chat.example"
			token, pair := f.pair(t, "chat", origin, "alice", true)
			lease := f.lease(t, origin, token)
			routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Provider: "claude", Model: tc.mode}})
			request := map[string]any{"kind": "action", "prompt": "Test"}
			if tc.mode == "oversized" {
				request["outputSchema"] = map[string]any{"type": "object", "properties": map[string]any{"word": map[string]any{"type": "string"}}, "required": []string{"word"}, "additionalProperties": false}
			}
			id := f.job(t, origin, token, lease, "claude-failure-"+tc.mode, request)
			job := waitClaudeJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
			if job.Status != "failed" || job.Code != tc.code {
				t.Fatalf("unsafe or incorrect completion: %+v", job)
			}
		})
	}
}

func TestClaudeAccountSwitchAndPolicy(t *testing.T) {
	f, root := claudeFixture(t)
	origin := "https://chat.example"
	token, pair := f.pair(t, "chat", origin, "alice", true)
	lease := f.lease(t, origin, token)
	c := createConversation(t, f, origin, token, lease, map[string]any{"provider": "claude"})
	id := conversationTurnJob(t, f, origin, token, lease, c, "claude-account-first", "Hello")
	if job := waitClaudeJob(t, f, id, func(j *Job) bool { return terminal(j.Status) }); job.Status != "completed" {
		t.Fatalf("initial turn: %+v", job)
	}
	if err := os.WriteFile(filepath.Join(root, "identity"), []byte("different-account"), 0600); err != nil {
		t.Fatal(err)
	}
	id = conversationTurnJob(t, f, origin, token, lease, c, "claude-account-switched", "Again")
	job := waitClaudeJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
	if job.Code != "native_account_changed" || job.Billing != "none" {
		t.Fatalf("account switch not rejected before submission: %+v", job)
	}
	if publicPairing(f.a.state.Pairings[pair]).NativeAccounts != nil {
		t.Fatal("account pin leaked")
	}
	for _, contents := range []string{`{"hooks":{"SessionStart":[]}}`, `broken`, strings.Repeat("x", (1<<20)+1)} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "managed-settings.json"), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if claudePolicyDirectory(dir) == nil {
			t.Fatal("managed/invalid policy not rejected")
		}
	}
}

func TestClaudeLeaseLossStopsOnlyItsOwnedSession(t *testing.T) {
	f, _ := claudeFixture(t)
	origin := "https://chat.example"
	token, _ := f.pair(t, "chat", origin, "alice", true)
	lease := f.lease(t, origin, token)
	c := createConversation(t, f, origin, token, lease, map[string]any{"provider": "claude"})
	id := conversationTurnJob(t, f, origin, token, lease, c, "claude-tab-lease", "HANG")
	waitClaudeJob(t, f, id, func(j *Job) bool { return j.Status == "running" })
	f.a.mu.Lock()
	owned := f.a.conversations[c]
	f.a.mu.Unlock()
	f.advance(t, 13*time.Second)
	select {
	case <-owned.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("expired requesting tab left Claude alive")
	}
	f.a.mu.Lock()
	defer f.a.mu.Unlock()
	if job := findJob(&f.a.state, id); job.Status != "cancelled" {
		t.Fatalf("lease did not cancel its native job: %+v", job)
	}
}
