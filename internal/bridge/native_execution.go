package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Website origin is a pairing boundary, not proof of provider deployment eligibility.
func nativeEligibility(p *Pairing, provider string) error { return nil }
func (a *App) nativeAvailable(p *Pairing, provider, kind string) error {
	if a.config.Bridge.DisableNative {
		return fail(503, "native_disabled", "Native execution is disabled in config.yaml.")
	}
	program, ok := a.program(provider)
	if !ok {
		return fail(503, "program_unavailable", "The selected AI program was removed. Choose a program explicitly in this connection's settings.")
	}
	if program.Type == "custom" && p.ProgramGrants[program.ID] != program.hash() {
		return fail(403, "program_not_granted", "Grant this custom AI program OS account permissions in the connection's Program access settings.")
	}
	if err := a.knownProgramAccount(p, provider); err != nil {
		return err
	}
	if e := nativeEligibility(p, provider); e != nil {
		return e
	}
	for _, s := range a.native {
		if s.ID == provider && s.Execution {
			if kind != "text" && !s.Images {
				return fail(503, "native_image_unavailable", "This AI program does not support image generation.")
			}
			return nil
		}
	}
	for _, s := range a.native {
		if s.ID == provider && s.Code != "" {
			return fail(503, s.Code, s.Detail)
		}
	}
	return fail(503, "native_client_unavailable", "The selected AI program is unavailable. Check its installation, profile and sign-in in Settings.")
}
func (a *App) bindNativeAccount(jobID, account string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	account = digest(a.state.DeviceID + ":" + account)
	j := findJob(&a.state, jobID)
	if j == nil || j.Status != "running" {
		return context.Canceled
	}
	p := a.state.Pairings[j.PairingID]
	if p == nil || p.Revoked || p.Paused {
		return context.Canceled
	}
	provider := jobProvider(j)
	if p.NativeAccounts != nil && p.NativeAccounts[provider] != "" && p.NativeAccounts[provider] != account {
		return fail(403, "native_account_changed", "The AI client is using a different subscription account. Reconnect this application to approve the new account.")
	}
	if p.NativeAccounts[provider] == account {
		return nil
	}
	return a.update(func(s *State) error {
		p := s.Pairings[j.PairingID]
		if p.NativeAccounts == nil {
			p.NativeAccounts = map[string]string{}
		}
		p.NativeAccounts[provider] = account
		return nil
	})
}
func (a *App) startNative(job *Job) {
	remaining := executionRemaining(job, a.now())
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	run := &commandRun{jobID: job.ID, limit: a.textLimit(a.state.Pairings[job.PairingID]), ctx: ctx, cancel: cancel, stopped: make(chan struct{})}
	a.commands[job.ID] = run
	inputs, err := a.selectedInputs(a.state.Pairings[job.PairingID], job.LeaseID, job.Request.Inputs)
	prompt, promptError := a.executionPrompt(job)
	if err == nil {
		err = promptError
	}

	if err != nil {
		run.complete(err, failureFor(400, "unsupported_input", "This native client cannot accept this input directly. Use a file-enabled command or a supported native input format."))
		close(run.stopped)
		cancel()
		return
	}
	turn := conversationTurn{JobID: job.ID, Prompt: prompt, Run: run, Schema: job.Request.OutputSchema, Inputs: inputs}
	id := job.Request.ConversationID
	conversation := a.conversations[id]
	if id == "" || conversation == nil {
		lifetime, stop := context.WithCancel(context.Background())
		conversation = &commandConversation{cancel: stop, stopped: make(chan struct{}), turns: make(chan conversationTurn, 1), active: run}
		if id != "" {
			a.conversations[id] = conversation
		}

		target := job.Plan.ExecutionTarget
		dir := filepath.Join(a.dir, "runs", "native-"+job.ID)
		program, _ := a.program(target.Provider)
		if program.Type == "custom" {
			go conversation.loop(withProgram(lifetime, program), customAction(program, id != ""), dir, turn, a)
		} else if program.Type == "claude" {
			go a.claudeLoop(lifetime, conversation, dir, turn, target, id != "")
		} else {
			go a.codexLoop(lifetime, conversation, dir, turn, target, id != "", jobKind(job))
		}
		return
	}
	conversation.mu.Lock()
	defer conversation.mu.Unlock()
	if conversation.ended {
		run.complete(errors.New("session ended"), failureFor(410, "session_closed", "The session has ended."))
		close(run.stopped)
		cancel()
		return
	}
	select {
	case conversation.turns <- turn:
	default:
		run.complete(errors.New("session busy"), failureFor(409, "session_busy", "The session is busy."))
		close(run.stopped)
		cancel()
	}
}
func (a *App) codexLoop(ctx context.Context, c *commandConversation, dir string, first conversationTurn, target ExecutionTarget, continuing bool, kind string) {
	if !continuing {
		defer close(first.Run.stopped)
	}
	active := &first
	reason := "session_closed"
	abortCurrent := context.AfterFunc(first.Run.ctx, c.cancel)
	finish := func(err error, failure *Failure) {
		if active != nil {
			abortCurrent()
			active.Run.complete(err, failure)
			active.Run.cancel()
			if continuing {
				close(active.Run.stopped)
			}
			active.Prompt = ""
			active.Inputs = nil
			active.Schema = nil
			active = nil
			c.setActive(nil)
		}
	}
	defer func() {
		c.cancel()
		finish(errors.New(reason), failureFor(410, reason, "The native session has ended. Start a new session explicitly."))
		os.RemoveAll(dir)
		c.mu.Lock()
		c.ended = true
		c.code = reason
		for {
			select {
			case pending := <-c.turns:
				pending.Run.complete(errors.New(reason), failureFor(410, reason, "The session has ended."))
				pending.Run.cancel()
				close(pending.Run.stopped)
			default:
				c.mu.Unlock()
				close(c.stopped)
				return
			}
		}
	}()
	failRun := func(err error) {
		code, message := "native_execution_failed", "Codex could not complete this request."
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
	if e := os.MkdirAll(dir, 0700); e != nil {
		failRun(e)
		return
	}
	if e := os.WriteFile(filepath.Join(dir, "instructions.txt"), []byte("You are a helpful assistant. Use supplied conversation inputs only.\n"), 0600); e != nil {
		failRun(e)
		return
	}
	program, e := a.programForJob(first.JobID)
	if e != nil {
		failRun(e)
		return
	}
	ctx = withProgram(ctx, program)
	path, e := program.path()
	if e != nil {
		failRun(e)
		return
	}
	images := kind == "image-generate" || kind == "image-edit"
	config := codexConfig(dir, images)
	if len(a.nativeToolDefinitions(first.JobID)) > 0 {
		config["features"].(map[string]any)["code_mode_host"] = true
	}
	if target.Reasoning != "" {
		config["model_reasoning_effort"] = target.Reasoning
	}
	rpc, e := startNativeRPC(ctx, path, dir, config, conversationStderr{c})
	if e != nil {
		failRun(e)
		return
	}
	defer rpc.close()
	// All startup work belongs to the first turn's deadline too.
	setup, stop := context.WithCancel(first.Run.ctx)
	defer stop()
	thread, account, e := prepareCodex(setup, rpc, dir, config, target, a.nativeToolDefinitions(first.JobID))
	if e != nil {
		failRun(e)
		return
	}
	idle := time.NewTimer(60 * time.Second)
	defer idle.Stop()
	if !idle.Stop() {
		<-idle.C
	}
	currentTurn := ""
	seenCalls := map[string]bool{}
	rpc.toolHandler = func(callCtx context.Context, m nativeMessage) error {
		var in struct {
			ThreadID  string          `json:"threadId"`
			TurnID    string          `json:"turnId"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(m.Params, &in) != nil || in.ThreadID != thread.Thread.ID || in.TurnID != currentTurn || active == nil {
			return errors.New("native tool session mismatch")
		}
		callKey := currentTurn + ":" + string(m.ID)
		if seenCalls[callKey] {
			return errors.New("replayed native tool call")
		}
		if len(seenCalls) >= 512 {
			return errors.New("native tool call limit exceeded")
		}
		seenCalls[callKey] = true
		result, err := a.invokeNativeTool(callCtx, active.JobID, in.Tool, in.Arguments)
		var content []any
		if err == nil {
			content, err = a.nativeToolContent(callCtx, active.JobID, result, true)
		}
		if err == nil {
			content, err = deliverCodexToolImages(callCtx, rpc, thread.Thread.ID, currentTurn, in.Tool+" (call "+string(m.ID)+")", content)
		}
		success := err == nil
		if err != nil {
			content = []any{map[string]any{"type": "inputText", "text": err.Error()}}
		}
		return rpc.send(map[string]any{"id": m.ID, "result": map[string]any{"contentItems": content, "success": success}})
	}
	for {
		if active == nil {
			select {
			case <-ctx.Done():
				return
			case <-idle.C:
				reason = "session_expired"
				return
			case turn := <-c.turns:
				active = &turn
				abortCurrent = context.AfterFunc(turn.Run.ctx, c.cancel)
				c.setActive(turn.Run)
			}
		}
		if e = a.bindNativeAccount(active.JobID, account); e != nil {
			failRun(e)
			return
		}
		// An account switch between turns is a fresh consent boundary.
		current, e := codexAccount(active.Run.ctx, rpc)
		if e != nil {
			failRun(e)
			return
		}
		if current != account {
			failRun(fail(403, "native_account_changed", "Codex changed accounts. Reconnect this application."))
			return
		}
		active.Inputs, e = prepareProviderInputs(active.Run.ctx, active.Inputs, "codex")
		if e != nil {
			failRun(e)
			return
		}
		active.Run.mu.Lock()
		active.Run.started = true
		active.Run.execution = &ExecutionMetadata{Kind: "native", Name: program.Name, ProgramID: program.ID, Integration: program.Type, Provider: "codex", Version: thread.NativeVersion, VersionSource: "native", Model: thread.Model, Reasoning: thread.Reasoning}
		active.Run.mu.Unlock()
		input := []any{map[string]any{"type": "text", "text": active.Prompt, "text_elements": []any{}}}
		for _, file := range active.Inputs {
			if strings.HasPrefix(file.MediaType, "image/") {
				input = append(input, map[string]any{"type": "image", "url": "data:" + file.MediaType + ";base64," + base64.StdEncoding.EncodeToString(file.Data)})
			} else {
				input = append(input, map[string]any{"type": "text", "text": "Supplied document: " + file.Name + "\n" + string(file.Data), "text_elements": []any{}})
			}
		}
		params := map[string]any{"threadId": thread.Thread.ID, "input": input}
		if len(active.Schema) > 0 {
			params["outputSchema"] = active.Schema
		}
		if target.Reasoning != "" {
			params["effort"] = target.Reasoning
			active.Run.mu.Lock()
			active.Run.execution.Reasoning = target.Reasoning
			active.Run.mu.Unlock()
		}
		var started struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if e = rpc.call(active.Run.ctx, "turn/start", params, &started); e != nil {
			failRun(e)
			return
		}
		currentTurn = started.Turn.ID
		if started.Turn.ID == "" {
			failRun(errors.New("native turn identifier missing"))
			return
		}
		for active != nil {
			m, e := rpc.receive(active.Run.ctx)
			if e != nil {
				failRun(e)
				return
			}
			var p struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				Delta    string `json:"delta"`
				Turn     struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
						Info    any    `json:"codexErrorInfo"`
					} `json:"error"`
				} `json:"turn"`
				TokenUsage struct {
					Last TokenUsage `json:"last"`
				} `json:"tokenUsage"`
				Item struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Result  string `json:"result"`
					Status  string `json:"status"`
					Failure *struct {
						Type string `json:"type"`
					} `json:"failure"`
				} `json:"item"`
			}
			if json.Unmarshal(m.Params, &p) != nil {
				failRun(errors.New("invalid native event"))
				return
			}
			if p.ThreadID != "" && p.ThreadID != thread.Thread.ID {
				failRun(errors.New("cross-session native event"))
				return
			}
			if p.TurnID != "" && p.TurnID != started.Turn.ID {
				failRun(errors.New("cross-turn native event"))
				return
			}

			switch m.Method {
			case "thread/tokenUsage/updated":
				active.Run.mu.Lock()
				usage := p.TokenUsage.Last
				active.Run.usage = &usage
				active.Run.mu.Unlock()
			case "item/completed":
				if p.Item.Type == "agentMessage" {
					output, _, _, _, _, _ := active.Run.snapshot()
					if output == "" && p.Item.Text != "" {
						active.Run.Write([]byte(p.Item.Text))
					}
				}
				if p.Item.Type == "imageGeneration" {
					if p.Item.Failure != nil && p.Item.Failure.Type == "usageLimitExceeded" {
						failRun(fail(429, "rate_limited", "Your Codex image usage limit has been reached."))
						return
					}
					if !images || p.Item.Status != "completed" || p.Item.Failure != nil {
						failRun(fail(503, "image_generation_failed", "Codex could not generate the image."))
						return
					}
					active.Run.mu.Lock()
					count := len(active.Run.artifacts)
					active.Run.mu.Unlock()
					if count >= 4 {
						failRun(errors.New("image output limit exceeded"))
						return
					}
					artifact, err := a.saveNativeImage(active.JobID, p.Item.Result)
					if err != nil {
						failRun(err)
						return
					}
					active.Run.mu.Lock()
					active.Run.artifacts = append(active.Run.artifacts, artifact)
					active.Run.mu.Unlock()
				}
			case "item/agentMessage/delta":
				active.Run.Write([]byte(p.Delta))
			case "item/started":
				switch p.Item.Type {
				case "dynamicToolCall":
					if active == nil || len(a.nativeToolDefinitions(active.JobID)) == 0 {
						failRun(fail(403, "permission_denied", "No app tools were authorized for this mode."))
						return
					}
				case "commandExecution", "fileChange", "mcpToolCall", "webSearch", "collabAgentToolCall":
					failRun(fail(403, "permission_denied", "Codex attempted a tool outside Restricted mode."))
					return
				}
			case "turn/completed":
				if p.Turn.ID != started.Turn.ID {
					failRun(errors.New("unexpected completed turn"))
					return
				}
				if p.Turn.Status != "completed" {
					code, message := "native_execution_failed", "Codex could not complete the request."
					if p.Turn.Error != nil {
						active.Run.writeDiagnostic([]byte(p.Turn.Error.Message))
						info, _ := json.Marshal(p.Turn.Error.Info)
						key := strings.ToLower(string(info))
						switch {
						case strings.Contains(key, "ratelimit") || strings.Contains(key, "usagelimit") || strings.Contains(key, "budget"):
							code, message = "rate_limited", "Your Codex usage limit has been reached."
						case strings.Contains(key, "contextwindow"):
							code, message = "context_limit", "The conversation reached its context limit. Start a new session."
						case strings.Contains(key, "unauthorized"):
							code, message = "authentication_required", "Sign in again using the installed Codex application."
						case strings.Contains(key, "sandboxerror"):
							code, message = "permission_denied", "Codex could not enforce its sandbox. Review Settings in WebDeviceCable."
						case strings.Contains(key, "serveroverloaded") || strings.Contains(key, "flexunavailable"):
							code, message = "execution_unavailable", "Codex is temporarily unavailable. Try a new request later."
						}
					}
					failRun(fail(503, code, message))
					return
				}
				if images {
					active.Run.mu.Lock()
					count := len(active.Run.artifacts)
					active.Run.mu.Unlock()
					if count == 0 {
						failRun(fail(503, "image_generation_failed", "Codex returned no image. No image result was fabricated."))
						return
					}
				}
				if len(active.Schema) > 0 {
					active.Run.mu.Lock()
					output, truncated := string(active.Run.output), active.Run.truncated
					active.Run.mu.Unlock()
					if truncated {
						failRun(errors.New("structured output exceeded the output limit"))
						return
					}
					if err := validateJSONOutput(active.Schema, output); err != nil {
						failRun(fail(503, "invalid_model_output", err.Error()))
						return
					}
					active.Run.mu.Lock()
					active.Run.jsonOutput = json.RawMessage(output)
					active.Run.mu.Unlock()
				}
				finish(nil, nil)
			}
		}
		if !continuing {
			return
		}
		// Session lifetime follows its requesting tab lease.
	}
}
