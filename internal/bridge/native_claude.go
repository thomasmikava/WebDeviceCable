package bridge

// Claude owns its subscription login. Only documented CLI flags and JSONL
// messages are used; provider credentials never enter WDC.
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const claudeMinimumVersion = "2.1.308"

type claudeAuth struct {
	LoggedIn        bool   `json:"loggedIn"`
	Method          string `json:"authMethod"`
	Email           string `json:"email"`
	OrgID           string `json:"orgId"`
	Subscription    string `json:"subscriptionType"`
	ConfigDirectory string `json:"configDirectory"`
}

func claudeAccount(path, dir string, programs ...AIProgram) (claudeAuth, error) {
	ctx := context.Background()
	if len(programs) > 0 {
		ctx = withProgram(ctx, programs[0])
	}
	raw, err := nativeOutputContext(ctx, path, dir, "--safe-mode", "--restricted", "--setting-sources", "", "auth", "status")
	var auth claudeAuth
	if json.Unmarshal([]byte(raw), &auth) != nil {
		return auth, fail(503, "native_protocol_error", "Claude Code could not report its sign-in status.")
	}
	if err != nil || !auth.LoggedIn {
		return auth, fail(401, "authentication_required", "Sign in using the installed Claude Code client, then check again.")
	}
	if auth.Method != "claude.ai" {
		return auth, fail(401, "api_key_not_allowed", "Claude Code must use its existing subscription login. API billing is not used by WDC.")
	}
	if auth.Email == "" || auth.OrgID == "" || !filepath.IsAbs(auth.ConfigDirectory) {
		return auth, fail(503, "native_protocol_error", "Claude Code did not report a stable subscription account identity.")
	}
	return auth, nil
}

func versionAtLeast(version, minimum string) bool {
	a, b := strings.Split(version, "."), strings.Split(minimum, ".")
	if len(a) < 3 || len(b) < 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		x, e := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if e != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return true
}

func checkClaude(dir, path string, status NativeStatus, programs ...AIProgram) NativeStatus {
	auth, err := claudeAccount(path, dir, programs...)
	status.Authentication = "not_signed_in_or_unavailable"
	if auth.LoggedIn && auth.Method == "claude.ai" {
		status.Authentication = "native_subscription"
	}
	if auth.LoggedIn && auth.Method != "claude.ai" {
		status.Authentication = "api_key_not_allowed"
	}
	if err == nil && !versionAtLeast(status.Version, claudeMinimumVersion) {
		err = fail(503, "native_version_unavailable", "Update Claude Code to version "+claudeMinimumVersion+" or newer for Restricted mode and isolated streaming.")
	}
	if err == nil {
		err = claudePolicy(auth)
	}
	if err != nil {
		var api *apiError
		status.Code = "native_client_unavailable"
		status.Detail = "Claude Code compatibility check failed."
		if errors.As(err, &api) {
			status.Code = api.Code
			status.Detail = api.Message
		}
		return status
	}
	status.Execution = true
	status.Sessions = true
	status.Account = auth.Email
	status.accountKey = auth.OrgID + ":" + strings.ToLower(auth.Email)
	status.Detail = "Ready. Uses your existing Claude Code subscription sign-in in a private, tool-free session."
	// Advertise only effort names reported by the installed CLI, not guesses
	// based on a cloud API's model catalog.
	var efforts []NativeReasoning
	ctx := context.Background()
	if len(programs) > 0 {
		ctx = withProgram(ctx, programs[0])
	}
	help, _ := nativeOutputContext(ctx, path, dir, "--help")
	lines := strings.Split(help, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "--effort") {
			continue
		}
		description := line
		if i+1 < len(lines) && !strings.Contains(lines[i+1], "--") {
			description += " " + lines[i+1]
		}
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
			if strings.Contains(description, "\""+effort+"\"") || strings.Contains(description, "("+effort+",") || strings.Contains(description, ", "+effort) {
				efforts = append(efforts, NativeReasoning{Effort: effort})
			}
		}
	}
	for _, model := range []struct{ ID, Name string }{{"sonnet", "Sonnet"}, {"opus", "Opus"}, {"haiku", "Haiku"}} {
		status.Models = append(status.Models, NativeModel{ID: model.ID, Name: model.Name, Reasoning: efforts})
	}
	return status
}

func claudePolicy(auth claudeAuth) error {
	// Organization policy can add executable hooks even under --safe-mode.
	// Do not override it or advertise isolation that the CLI cannot enforce.
	if auth.Subscription != "pro" && auth.Subscription != "max" {
		return fail(503, "native_policy_unavailable", "This Claude account has organization-managed or unknown policy. WDC currently supports personal Pro and Max sign-ins in Restricted mode.")
	}
	if err := claudeOSPolicy(); err != nil {
		return err
	}
	return claudePolicyDirectory(auth.ConfigDirectory)
}

func claudePolicyDirectory(dir string) error {
	for _, name := range []string{"managed-settings.json", "managed-mcp.json", "remote-settings.json"} {
		path := filepath.Join(dir, name)
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fail(503, "native_policy_unavailable", "Claude policy files could not be checked safely.")
		}
		data, err := ReadLocalFile(path, 1<<20)
		var policy map[string]json.RawMessage
		if err != nil || json.Unmarshal(data, &policy) != nil || len(policy) != 0 {
			return fail(503, "native_policy_unavailable", "Claude has managed settings that WDC cannot isolate. Its administrator must review the integration.")
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "managed-settings.d"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(503, "native_policy_unavailable", "Claude managed policy could not be checked safely.")
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".json") {
			return fail(503, "native_policy_unavailable", "Claude has managed policy fragments that WDC cannot isolate.")
		}
	}
	return nil
}

func claudeArgs(target ExecutionTarget, id string) []string {
	args := []string{"--safe-mode", "--restricted", "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--no-session-persistence", "--session-id", id, "--tools", "", "--disallowedTools", "mcp__*", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--disable-slash-commands", "--no-chrome", "--permission-mode", "dontAsk", "--permission-prompts", "none", "--setting-sources", "", "--settings", `{"disableAllHooks":true,"disableClaudeAiConnectors":true,"autoMemoryEnabled":false}`, "--system-prompt", "You are a helpful assistant. Use only the conversation inputs supplied here. No local tools or files are available."}
	if target.Model != "" {
		args = append(args, "--model", target.Model)
	}
	if target.Reasoning != "" {
		args = append(args, "--effort", target.Reasoning)
	}
	return args
}

func nativeUUID() string {
	s := secret()
	return fmt.Sprintf("%s-%s-4%s-a%s-%s", s[:8], s[8:12], s[13:16], s[17:20], s[20:32])
}

func claudeInput(turn *conversationTurn, session string) any {
	prompt := turn.Prompt
	if len(turn.Schema) > 0 {
		prompt += "\n\nReturn only a JSON value matching this JSON Schema, without Markdown fences:\n" + string(turn.Schema)
	}
	content := []any{map[string]any{"type": "text", "text": prompt}}
	for _, file := range turn.Inputs {
		if strings.HasPrefix(file.MediaType, "image/") {
			content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": file.MediaType, "data": base64.StdEncoding.EncodeToString(file.Data)}})
		} else if file.MediaType == "application/pdf" {
			content = append(content, map[string]any{"type": "document", "title": file.Name, "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": base64.StdEncoding.EncodeToString(file.Data)}})
		} else {
			content = append(content, map[string]any{"type": "text", "text": "Supplied document: " + file.Name + "\n" + string(file.Data)})
		}
	}
	return map[string]any{"type": "user", "session_id": session, "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": content}}
}

type claudeBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type claudeEvent struct {
	Type           string             `json:"type"`
	Subtype        string             `json:"subtype"`
	Session        string             `json:"session_id"`
	Parent         json.RawMessage    `json:"parent_tool_use_id"`
	CWD            string             `json:"cwd"`
	Version        string             `json:"claude_code_version"`
	Model          string             `json:"model"`
	APIKeySource   string             `json:"apiKeySource"`
	PermissionMode string             `json:"permissionMode"`
	Tools          *[]string          `json:"tools"`
	MCP            *[]json.RawMessage `json:"mcp_servers"`
	Skills         []string           `json:"skills"`
	Plugins        []struct {
		Path string `json:"path"`
	} `json:"plugins"`
	IsError        bool     `json:"is_error"`
	Result         string   `json:"result"`
	Errors         []string `json:"errors"`
	APIErrorStatus int      `json:"api_error_status"`
	Message        struct {
		Model   string        `json:"model"`
		Content []claudeBlock `json:"content"`
	} `json:"message"`
	Event struct {
		Type         string      `json:"type"`
		ContentBlock claudeBlock `json:"content_block"`
		Delta        struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	Usage struct {
		Input   int64 `json:"input_tokens"`
		Output  int64 `json:"output_tokens"`
		Cached  int64 `json:"cache_read_input_tokens"`
		Created int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func decodeClaudeEvent(raw []byte) (claudeEvent, error) {
	// Non-content notifications may use fields such as "message" with a
	// different shape. Decode their envelope without interpreting that payload.
	var header struct {
		Type    string          `json:"type"`
		Subtype string          `json:"subtype"`
		Session string          `json:"session_id"`
		Parent  json.RawMessage `json:"parent_tool_use_id"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return claudeEvent{}, err
	}
	event := claudeEvent{Type: header.Type, Subtype: header.Subtype, Session: header.Session, Parent: header.Parent}
	if header.Type == "assistant" || header.Type == "stream_event" || header.Type == "result" || header.Type == "system" && header.Subtype == "init" {
		err := json.Unmarshal(raw, &event)
		return event, err
	}
	return event, nil
}

func (e *claudeEvent) isolated(dir, version string) bool {
	if filepath.Clean(e.CWD) != filepath.Clean(dir) || e.Version != version || e.APIKeySource != "none" || e.PermissionMode != "dontAsk" || e.Model == "" || e.Tools == nil || len(*e.Tools) != 0 || e.MCP == nil || len(*e.MCP) != 0 || len(e.Skills) != 0 {
		return false
	}
	for _, plugin := range e.Plugins {
		if plugin.Path != "builtin" {
			return false
		}
	}
	return true
}

func claudeFailure(event claudeEvent) error {
	text := strings.ToLower(event.Result + " " + event.Subtype + " " + strings.Join(event.Errors, " "))
	switch {
	case event.APIErrorStatus == 429 || strings.Contains(text, "rate limit") || strings.Contains(text, "usage limit") || strings.Contains(text, "hit your limit"):
		return fail(429, "rate_limited", "Your Claude Code usage limit has been reached.")
	case event.APIErrorStatus == 401 || strings.Contains(text, "not logged in") || strings.Contains(text, "authentication"):
		return fail(401, "authentication_required", "Sign in again using the installed Claude Code client.")
	case strings.Contains(text, "context") && (strings.Contains(text, "limit") || strings.Contains(text, "long")):
		return fail(503, "context_limit", "This Claude conversation reached its context limit. Start a new session.")
	case event.APIErrorStatus == 529 || event.APIErrorStatus == 503 || strings.Contains(text, "overloaded"):
		return fail(503, "execution_unavailable", "Claude Code is temporarily unavailable. Try a new request later.")
	default:
		return fail(503, "native_execution_failed", "Claude Code could not complete this request.")
	}
}

func (a *App) claudeLoop(ctx context.Context, c *commandConversation, dir string, first conversationTurn, target ExecutionTarget, continuing bool) {
	active := &first
	reason := "session_closed"
	abortCurrent := context.AfterFunc(first.Run.ctx, c.cancel)
	finish := func(err error, failure *Failure) {
		if active != nil {
			abortCurrent()
			active.Run.complete(err, failure)
			active.Run.cancel()
			close(active.Run.stopped)
			active.Prompt = ""
			active.Inputs = nil
			active.Schema = nil
			active = nil
			c.setActive(nil)
		}
	}
	defer func() {
		c.cancel()
		finish(errors.New(reason), failureFor(410, reason, "The Claude session has ended. Start a new session explicitly."))
		os.RemoveAll(dir)
		c.mu.Lock()
		c.ended = true
		c.code = reason
		for {
			select {
			case turn := <-c.turns:
				turn.Run.complete(errors.New(reason), failureFor(410, reason, "The session has ended."))
				turn.Run.cancel()
				close(turn.Run.stopped)
			default:
				c.mu.Unlock()
				close(c.stopped)
				return
			}
		}
	}()
	failRun := func(err error) {
		code, message := "native_execution_failed", "Claude Code could not complete this request."
		var api *apiError
		if errors.As(err, &api) {
			code, message = api.Code, api.Message
		}
		if active != nil {
			active.Run.writeDiagnostic([]byte(err.Error()))
			finish(err, failureFor(0, code, message))
		}
		reason = code
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		failRun(err)
		return
	}
	program, err := a.programForJob(first.JobID)
	if err != nil {
		failRun(err)
		return
	}
	ctx = withProgram(ctx, program)
	path, err := program.path()
	if err != nil {
		failRun(err)
		return
	}
	version, err := nativeOutputContext(ctx, path, dir, "--version")
	if err != nil {
		failRun(err)
		return
	}
	version = nativeVersionPattern.FindString(version)
	if !versionAtLeast(version, claudeMinimumVersion) {
		failRun(fail(503, "native_version_unavailable", "Update Claude Code before using Restricted mode."))
		return
	}
	account, err := claudeAccount(path, dir, program)
	if err != nil {
		failRun(err)
		return
	}
	if err = claudePolicy(account); err != nil {
		failRun(err)
		return
	}
	id := nativeUUID()
	definitions := a.nativeToolDefinitions(first.JobID)
	args := claudeArgs(target, id)
	if len(definitions) > 0 {
		configuration, cleanup, e := a.startToolAdapter(ctx, c, definitions)
		if e != nil {
			failRun(e)
			return
		}
		defer cleanup()
		raw, _ := json.Marshal(configuration)
		configPath := filepath.Join(dir, "wdc-mcp.json")
		if e = os.WriteFile(configPath, raw, 0600); e != nil {
			failRun(e)
			return
		}
		args = claudeToolArgs(args, configPath)
	}
	process, err := startNativeProcess(ctx, path, dir, args, conversationStderr{c})
	if err != nil {
		failRun(err)
		return
	}
	defer process.close()
	initialized := false
	idle := time.NewTimer(time.Hour)
	if !idle.Stop() {
		<-idle.C
	}
	defer idle.Stop()
	writes := make(chan error, 1)
	begin := func() error {
		auth, err := claudeAccount(path, dir, program)
		if err != nil {
			return err
		}
		if err = claudePolicy(auth); err != nil {
			return err
		}
		if auth.OrgID != account.OrgID || !strings.EqualFold(auth.Email, account.Email) {
			return fail(403, "native_account_changed", "Claude Code changed accounts. Reconnect this application.")
		}
		if err = a.bindNativeAccount(active.JobID, account.OrgID+":"+strings.ToLower(account.Email)); err != nil {
			return err
		}
		active.Inputs, err = prepareProviderInputs(active.Run.ctx, active.Inputs, "claude")
		if err != nil {
			return err
		}
		message := claudeInput(active, id)
		initialized = false
		active.Run.mu.Lock()
		active.Run.started = true
		active.Run.execution = &ExecutionMetadata{Kind: "native", Name: program.Name, ProgramID: program.ID, Integration: program.Type, Provider: "claude", Version: version, VersionSource: "native"}
		active.Run.mu.Unlock()
		go func() { writes <- process.send(message) }()
		return nil
	}
	if err = begin(); err != nil {
		failRun(err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-idle.C:
			reason = "session_expired"
			return
		case err := <-writes:
			if err != nil {
				failRun(err)
				return
			}
		case turn := <-c.turns:
			if active != nil {
				turn.Run.complete(errors.New("session busy"), failureFor(409, "session_busy", "The session is busy."))
				turn.Run.cancel()
				close(turn.Run.stopped)
				continue
			}
			idle.Stop()
			active = &turn
			c.setActive(turn.Run)
			abortCurrent = context.AfterFunc(turn.Run.ctx, c.cancel)
			if err = begin(); err != nil {
				failRun(err)
				return
			}
		case packet := <-process.packets:
			if packet.Err != nil {
				failRun(fail(503, "native_protocol_error", "Claude Code ended before completing the active session."))
				return
			}
			event, decodeErr := decodeClaudeEvent(packet.Data)
			if decodeErr != nil {
				if active != nil {
					active.Run.writeDiagnostic([]byte(decodeErr.Error()))
				}
				failRun(fail(503, "native_protocol_error", "Claude Code returned an invalid streaming event."))
				return
			}
			hasParent := len(event.Parent) > 0 && string(event.Parent) != "null"
			// Foreground MCP heartbeats identify the running tool in Parent. They
			// are non-content notifications, not output from a child session.
			scopedProgress := event.Type == "tool_progress" && event.Session == id
			if event.Session != "" && event.Session != id || hasParent && !scopedProgress {
				if active != nil {
					active.Run.writeDiagnostic([]byte(fmt.Sprintf("Rejected Claude event: type=%.64q subtype=%.64q session_matches=%t session_present=%t parent_present=%t\n", event.Type, event.Subtype, event.Session == id, event.Session != "", hasParent)))
				}
				failRun(fail(503, "native_protocol_error", "Claude Code returned an event from another session."))
				return
			}
			if event.Type == "system" && event.Subtype == "init" {
				if initialized || event.Session != id || !(len(definitions) == 0 && event.isolated(dir, version) || len(definitions) > 0 && event.isolatedTools(dir, version, definitions)) {
					if active != nil {
						names := []string{}
						if event.Tools != nil {
							names = *event.Tools
						}
						active.Run.writeDiagnostic([]byte(fmt.Sprintf("Tool isolation mismatch: tools=%v, MCP servers=%d, skills=%d", names, func() int {
							if event.MCP == nil {
								return -1
							}
							return len(*event.MCP)
						}(), len(event.Skills))))
					}
					failRun(fail(503, "native_isolation_unavailable", "Claude Code could not confirm the requested isolated tools. Ordinary Restricted chat is still available."))
					return
				}
				initialized = true
				if active != nil {
					active.Run.mu.Lock()
					active.Run.execution.Model = event.Model
					active.Run.mu.Unlock()
				}
				continue
			}
			if event.Type == "system" && (strings.HasPrefix(event.Subtype, "hook_") || event.Subtype == "permission_denied") || event.Type == "control_request" {
				failRun(fail(403, "permission_denied", "Claude Code attempted an action outside Restricted mode."))
				return
			}
			if event.Type != "assistant" && event.Type != "stream_event" && event.Type != "result" {
				continue
			}
			if !initialized || active == nil || event.Session != id {
				failRun(fail(503, "native_protocol_error", "Claude Code returned an unexpected turn event."))
				return
			}
			switch event.Type {
			case "stream_event":
				if event.Event.ContentBlock.Type == "tool_use" && len(definitions) == 0 {
					failRun(fail(403, "permission_denied", "Claude Code attempted a tool outside Restricted mode."))
					return
				}
				if event.Event.Delta.Type == "text_delta" {
					active.Run.Write([]byte(event.Event.Delta.Text))
				}
			case "assistant":
				var output strings.Builder
				for _, block := range event.Message.Content {
					if block.Type == "tool_use" && len(definitions) == 0 {
						failRun(fail(403, "permission_denied", "Claude Code attempted a tool outside Restricted mode."))
						return
					}
					if block.Type == "text" {
						output.WriteString(block.Text)
					}
				}
				active.Run.mu.Lock()
				if event.Message.Model != "" {
					active.Run.execution.Model = event.Message.Model
				}
				empty := len(active.Run.output) == 0
				active.Run.mu.Unlock()
				if empty {
					active.Run.Write([]byte(output.String()))
				}
			case "result":
				if event.IsError || event.Subtype != "success" {
					active.Run.writeDiagnostic([]byte(event.Result + " " + strings.Join(event.Errors, " ")))
					failRun(claudeFailure(event))
					return
				}
				if event.Result != "" {
					active.Run.mu.Lock()
					active.Run.output = nil
					active.Run.mu.Unlock()
					active.Run.Write([]byte(event.Result))
				}
				active.Run.mu.Lock()
				output, truncated := string(active.Run.output), active.Run.truncated
				input := event.Usage.Input + event.Usage.Created + event.Usage.Cached
				active.Run.usage = &TokenUsage{Input: input, Cached: event.Usage.Cached, Output: event.Usage.Output, Total: input + event.Usage.Output}
				active.Run.mu.Unlock()
				if len(active.Schema) > 0 {
					if truncated {
						failRun(fail(503, "invalid_model_output", "Structured output exceeded the output limit."))
						return
					}
					if err = validateJSONOutput(active.Schema, output); err != nil {
						failRun(fail(503, "invalid_model_output", err.Error()))
						return
					}
					active.Run.mu.Lock()
					active.Run.jsonOutput = json.RawMessage(output)
					active.Run.mu.Unlock()
				}
				finish(nil, nil)
				if !continuing {
					return
				}
				// Session lifetime follows its requesting tab lease.
			}
		}
	}
}
