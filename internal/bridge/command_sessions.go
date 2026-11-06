package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type conversationTurn struct {
	JobID     string
	Prompt    string
	Arguments map[string]string
	Run       *commandRun
	Schema    json.RawMessage
	Inputs    []nativeInput
}
type conversationPacket struct {
	Data []byte
	Err  error
}
type commandConversation struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped chan struct{}
	turns   chan conversationTurn
	active  *commandRun
	ended   bool
	code    string
}

func (c *commandConversation) status() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended, c.code
}
func (c *commandConversation) setActive(run *commandRun) { c.mu.Lock(); c.active = run; c.mu.Unlock() }

type conversationStderr struct{ conversation *commandConversation }

func (w conversationStderr) Write(data []byte) (int, error) {
	w.conversation.mu.Lock()
	run := w.conversation.active
	w.conversation.mu.Unlock()
	if run != nil {
		run.writeDiagnostic(data)
	}
	return len(data), nil
}
func (r *commandRun) writeDiagnostic(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	left := 4096 - len(r.diagnostic)
	if left > 0 {
		if len(data) > left {
			data = data[:left]
		}
		r.diagnostic = append(r.diagnostic, data...)
	}
}
func (r *commandRun) details() (bool, string, *Failure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var failure *Failure
	if r.failure != nil {
		copy := *r.failure
		failure = &copy
	}
	return r.started, cleanDiagnostic(string(r.diagnostic)), failure
}
func (r *commandRun) complete(err error, failure *Failure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.err = err
	r.failure = failure
	r.timedOut = errors.Is(r.ctx.Err(), context.DeadlineExceeded)
	r.done = true
}
func (a *App) startConversationTurn(job *Job) {
	action := a.actions[jobAction(job)]
	remaining := time.Duration(job.Request.TimeoutSeconds)*time.Second - a.now().Sub(job.CreatedAt)
	if maximum := time.Duration(action.Timeout) * time.Second; remaining > maximum {
		remaining = maximum
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	run := &commandRun{jobID: job.ID, limit: a.textLimit(), ctx: ctx, cancel: cancel, stopped: make(chan struct{})}
	a.commands[job.ID] = run
	inputs, err := a.selectedInputs(a.state.Pairings[job.PairingID], job.LeaseID, job.Request.Inputs)
	prompt, promptError := a.executionPrompt(job)
	if err == nil {
		err = promptError
	}
	if err != nil {
		run.complete(err, failureFor(400, "invalid_input", "Supplied files are no longer available."))
		close(run.stopped)
		return
	}
	turn := conversationTurn{JobID: job.ID, Prompt: prompt, Arguments: job.Request.Arguments, Run: run, Inputs: inputs}
	conversation := a.conversations[job.Request.ConversationID]
	if conversation == nil {
		processContext, stop := context.WithCancel(context.Background())
		conversation = &commandConversation{cancel: stop, stopped: make(chan struct{}), turns: make(chan conversationTurn, 1), active: run}
		a.conversations[job.Request.ConversationID] = conversation
		go conversation.loop(processContext, action, filepath.Join(a.dir, "runs", "session-"+job.Request.ConversationID), turn, a)
	} else {
		conversation.mu.Lock()
		if conversation.ended {
			conversation.mu.Unlock()
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
		conversation.mu.Unlock()
	}
}
func (c *commandConversation) loop(ctx context.Context, action configuredAction, dir string, first conversationTurn, owner *App) {
	customOneShot := action.program != nil && !action.continuing
	if customOneShot {
		defer close(first.Run.stopped)
	}
	var active *conversationTurn
	var files *commandFiles
	reason := "session_closed"
	abortCurrent := func() bool { return false }
	finish := func(err error, failure *Failure) {
		if active != nil {
			if err == nil && files != nil {
				artifacts, e := owner.collectCommandFiles(active.JobID, files.OutputDirectory)
				if e != nil {
					err = e
					failure = failureFor(400, "file_transfer_failed", "The command returned unsafe or oversized output files.")
				}
				active.Run.mu.Lock()
				active.Run.artifacts = artifacts
				active.Run.mu.Unlock()
			}
			abortCurrent()
			active.Run.complete(err, failure)
			active.Run.cancel()
			if !customOneShot {
				close(active.Run.stopped)
			}
			active.Prompt = ""
			active.Arguments = nil
			active.Inputs = nil
			active = nil
			c.setActive(nil)
		}
	}
	defer func() {
		c.cancel()
		finish(errors.New("session closed"), failureFor(410, reason, "The session has ended."))
		os.RemoveAll(dir)
		c.mu.Lock()
		c.ended = true
		c.code = reason
		for {
			select {
			case pending := <-c.turns:
				pending.Run.complete(errors.New("session ended"), failureFor(410, reason, "The session has ended."))
				pending.Run.cancel()
				close(pending.Run.stopped)
			default:
				c.mu.Unlock()
				close(c.stopped)
				return
			}
		}
	}()
	active = &first
	c.setActive(first.Run)
	if err := os.MkdirAll(dir, 0700); err != nil {
		reason = "command_start_failed"
		first.Run.writeDiagnostic([]byte(err.Error()))
		finish(err, failureFor(0, reason, "The command could not start."))
		return
	}
	cmd := exec.CommandContext(ctx, action.Exec[0], action.Exec[1:]...)
	cmd.Dir = dir
	cmd.Env = commandEnv()
	if action.program != nil {
		cmd.Env = programEnv(*action.program)
	}
	cmd.WaitDelay = time.Second
	stdout, writer := io.Pipe()
	defer stdout.Close()
	defer writer.Close()
	cmd.Stdout = writer
	cmd.Stderr = conversationStderr{c}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		reason = "command_start_failed"
		first.Run.writeDiagnostic([]byte(err.Error()))
		finish(err, failureFor(0, reason, "The command could not start."))
		return
	}
	defer stdin.Close()
	cleanup, err := startOwnedCommand(cmd)
	if err != nil {
		reason = "command_start_failed"
		first.Run.writeDiagnostic([]byte(err.Error()))
		finish(err, failureFor(0, reason, "The command could not start."))
		return
	}
	waited := make(chan struct{})
	go func() { err := cmd.Wait(); cleanup(); writer.CloseWithError(err); close(waited) }()
	defer func() { c.cancel(); stdin.Close(); stdout.Close(); <-waited }()
	packets := make(chan conversationPacket, 4)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			data := append([]byte{}, scanner.Bytes()...)
			select {
			case packets <- conversationPacket{Data: data}:
			case <-ctx.Done():
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case packets <- conversationPacket{Err: err}:
		case <-ctx.Done():
		}
	}()
	writeResult := make(chan error, 1)
	begin := func(turn *conversationTurn) {
		active = turn
		c.setActive(turn.Run)
		if action.program != nil {
			abortCurrent = context.AfterFunc(turn.Run.ctx, c.cancel)
			program, err := owner.programForJob(turn.JobID)
			if err == nil && program.hash() != action.program.hash() {
				err = fail(409, "program_changed", "This AI program changed.")
			}
			var info customProgramInfo
			if err == nil {
				info, err = inspectCustom(withProgram(turn.Run.ctx, program), action.Exec[0], dir)
				if err != nil {
					err = fail(503, "custom_interface_unavailable", err.Error())
				}
			}
			if err == nil {
				owner.mu.Lock()
				target := ExecutionTarget{}
				if j := findJob(&owner.state, turn.JobID); j != nil && j.Plan != nil {
					target = j.Plan.ExecutionTarget
				}
				owner.mu.Unlock()
				err = customOptions(info, target, action.continuing)
			}
			if err == nil && len(turn.Inputs) > 0 && !info.Files {
				err = fail(400, "unsupported_feature", "This custom AI program no longer supports supplied files.")
			}
			if err == nil {
				owner.mu.Lock()
				j := findJob(&owner.state, turn.JobID)
				allowed := j != nil && owner.state.Pairings[j.PairingID] != nil && owner.state.Pairings[j.PairingID].Files
				if info.Files && allowed && j != nil {
					err = owner.update(func(s *State) error { markFileScope(s, findJob(s, turn.JobID)); return nil })
				}
				owner.mu.Unlock()
				action.Files = info.Files && allowed
			}
			if err == nil {
				account := info.Account
				if account == "" {
					account = "unreported:" + program.hash()
				}
				err = owner.bindNativeAccount(turn.JobID, account)
			}
			if err != nil {
				reason = errorCode(err)
				turn.Run.complete(err, failureFor(0, reason, err.Error()))
				c.cancel()
				turn.Run.cancel()
				return
			}
			turn.Run.mu.Lock()
			turn.Run.execution = &ExecutionMetadata{Kind: "custom", Name: program.Name, ProgramID: program.ID, Integration: program.Type, Version: info.Version, VersionSource: "program"}
			turn.Run.mu.Unlock()
		}
		turn.Run.mu.Lock()
		turn.Run.started = true
		turn.Run.mu.Unlock()
		files = nil
		if action.Files {
			staged, e := stageCommandFiles(filepath.Join(dir, "files", turn.JobID), turn.Inputs)
			if e != nil {
				turn.Run.complete(e, failureFor(400, "file_transfer_failed", "Could not stage supplied files."))
				turn.Run.cancel()
				return
			}
			files = &staged
		}
		model, reasoning := "", ""
		if action.program != nil {
			owner.mu.Lock()
			if j := findJob(&owner.state, turn.JobID); j != nil && j.Plan != nil {
				model = j.Plan.Model
				reasoning = j.Plan.Reasoning
			}
			owner.mu.Unlock()
		}
		data, _ := json.Marshal(struct {
			Model     string            `json:"model,omitempty"`
			Reasoning string            `json:"reasoning,omitempty"`
			Version   int               `json:"v"`
			Type      string            `json:"type"`
			ID        string            `json:"id"`
			Prompt    string            `json:"prompt,omitempty"`
			Arguments map[string]string `json:"arguments,omitempty"`
			Files     *commandFiles     `json:"files,omitempty"`
		}{model, reasoning, 1, "turn", turn.JobID, turn.Prompt, turn.Arguments, files})
		go func() {
			_, err := stdin.Write(append(data, '\n'))
			select {
			case writeResult <- err:
			case <-ctx.Done():
			}
		}()
	}
	begin(&first)
	idle := time.NewTimer(time.Duration(action.Session.IdleTimeout) * time.Second)
	defer idle.Stop()
	if !idle.Stop() {
		<-idle.C
	}
	for {
		var turnDone <-chan struct{}
		if active != nil {
			turnDone = active.Run.ctx.Done()
		}
		select {
		case <-ctx.Done():
			return
		case <-turnDone:
			reason = "session_closed"
			if errors.Is(active.Run.ctx.Err(), context.DeadlineExceeded) {
				reason = "deadline_exceeded"
			}
			finish(active.Run.ctx.Err(), failureFor(0, reason, "The message was cancelled or timed out. Start a new session."))
			return
		case <-idle.C:
			reason = "session_expired"
			return
		case err := <-writeResult:
			if err != nil {
				reason = "command_failed"
				if active != nil {
					active.Run.writeDiagnostic([]byte(err.Error()))
				}
				finish(err, failureFor(0, reason, "The command stopped accepting messages."))
				return
			}
		case turn := <-c.turns:
			if active != nil {
				turn.Run.complete(errors.New("overlapping turn"), failureFor(409, "session_busy", "The session is busy."))
				turn.Run.cancel()
				close(turn.Run.stopped)
				reason = "session_protocol_error"
				return
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			begin(&turn)
		case packet := <-packets:
			if packet.Err != nil {
				reason = "command_failed"
				if active != nil {
					active.Run.writeDiagnostic([]byte(packet.Err.Error()))
				}
				finish(packet.Err, failureFor(0, reason, "The command ended before completing the message."))
				return
			}
			if active == nil {
				reason = "session_protocol_error"
				return
			}
			var frame struct {
				Version int    `json:"v"`
				Type    string `json:"type"`
				ID      string `json:"id"`
				Delta   string `json:"delta"`
				Error   *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
					Raw     string `json:"raw"`
				} `json:"error"`
			}
			decoder := json.NewDecoder(bytes.NewReader(packet.Data))
			decoder.DisallowUnknownFields()
			err := decoder.Decode(&frame)
			if err != nil || decoder.Decode(new(any)) != io.EOF || frame.Version != 1 || frame.ID != active.JobID {
				reason = "session_protocol_error"
				active.Run.writeDiagnostic(packet.Data)
				finish(errors.New("invalid session frame"), failureFor(0, reason, "The command returned an invalid session message."))
				return
			}
			switch frame.Type {
			case "output":
				if frame.Error != nil {
					reason = "session_protocol_error"
					finish(errors.New("invalid output frame"), failureFor(0, reason, "The command returned an invalid output frame."))
					return
				}
				active.Run.Write([]byte(frame.Delta))
			case "done":
				if frame.Error != nil || frame.Delta != "" {
					reason = "session_protocol_error"
					finish(errors.New("invalid completion"), failureFor(0, reason, "The command returned an invalid completion frame."))
					return
				}
				finish(nil, nil)
				if action.program != nil && !action.continuing {
					return
				}
				if action.Session.IdleTimeout > 0 {
					idle.Reset(time.Duration(action.Session.IdleTimeout) * time.Second)
				}
			case "error":
				reason = "command_failed"
				raw := string(packet.Data)
				if frame.Error != nil {
					code, message := commandFailure(frame.Error.Code)
					reason = code
					raw = fmt.Sprintf("%s: %s\n%s", frame.Error.Code, frame.Error.Message, frame.Error.Raw)
					active.Run.writeDiagnostic([]byte(raw))
					finish(errors.New(message), failureFor(0, code, message))
				} else {
					finish(errors.New("command error"), failureFor(0, reason, "The command failed."))
				}
				return
			default:
				reason = "session_protocol_error"
				finish(errors.New("unknown session frame"), failureFor(0, reason, "The command returned an unsupported session message."))
				return
			}
		}
	}
}
func commandFailure(code string) (string, string) {
	switch code {
	case "rate_limited":
		return code, "The program’s service is rate limited."
	case "context_limit":
		return code, "The session reached its context limit."
	case "authentication_required":
		return code, "The program needs sign-in."
	case "permission_denied":
		return code, "The program denied permission."
	case "unavailable":
		return "execution_unavailable", "The program’s service is unavailable."
	default:
		return "command_failed", "The command failed."
	}
}
