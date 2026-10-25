package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

func (a *App) textLimit() int {
	if a.config.Bridge.MaxTextBytes > 0 {
		return a.config.Bridge.MaxTextBytes
	}
	return 1 << 20
}

type commandRun struct {
	jobID      string
	limit      int
	mu         sync.Mutex
	ctx        context.Context
	started    bool
	diagnostic []byte
	failure    *Failure
	execution  *ExecutionMetadata
	artifacts  []Artifact
	usage      *TokenUsage
	jsonOutput json.RawMessage
	cancel     context.CancelFunc
	stopped    chan struct{}
	output     []byte
	truncated  bool
	done       bool
	err        error
	exitCode   *int
	timedOut   bool
}

func (r *commandRun) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	limit := r.limit
	if limit == 0 {
		limit = 1 << 20
	}
	remaining := limit - len(r.output)
	if len(p) > remaining {
		r.truncated = true
		p = p[:remaining]
		if r.cancel != nil {
			r.cancel()
		}
	}
	r.output = append(r.output, p...)
	return n, nil
}
func (r *commandRun) snapshot() (string, bool, bool, error, *int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	text := strings.ToValidUTF8(string(r.output), "�")
	text = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' {
			return -1
		}
		return c
	}, text)
	return text, r.truncated, r.done, r.err, r.exitCode, r.timedOut
}

// Intentionally exclude ambient API tokens, provider overrides and user hooks.
// Configured programs still have the user's filesystem/network permissions.
func commandEnv() []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true, "TMPDIR": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true}
	out := []string{}
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		if allowed[strings.ToUpper(key)] {
			out = append(out, v)
		}
	}
	return out
}
func (a *App) startCommand(j *Job) {
	if j.Request.ConversationID != "" {
		a.startConversationTurn(j)
		return
	}
	action := a.actions[jobAction(j)]
	argv := action.argv(j.Request.Arguments)
	inputs, inputError := a.selectedInputs(a.state.Pairings[j.PairingID], j.LeaseID, j.Request.Inputs)
	remaining := time.Duration(j.Request.TimeoutSeconds)*time.Second - a.now().Sub(j.CreatedAt)
	if limit := time.Duration(action.Timeout) * time.Second; remaining > limit {
		remaining = limit
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	run := &commandRun{jobID: j.ID, limit: a.textLimit(), ctx: ctx, cancel: cancel, stopped: make(chan struct{})}
	a.commands[j.ID] = run
	dir := filepath.Join(a.dir, "runs", j.ID)
	go func() {
		defer close(run.stopped)
		defer cancel()
		defer os.RemoveAll(dir)
		var err error
		var files commandFiles
		if inputError != nil {
			run.complete(inputError, failureFor(400, "invalid_input", "The supplied files are no longer available."))
			return
		}
		var exitCode *int
		if err = os.MkdirAll(dir, 0700); err == nil {
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = dir
			cmd.Env = commandEnv()
			if action.Files {
				files, err = stageCommandFiles(dir, inputs)
				if err != nil {
					run.complete(err, failureFor(400, "file_transfer_failed", "Could not stage supplied files."))
					return
				}
				cmd.Env = append(cmd.Env, commandFileEnv(files)...)
			}
			cmd.Stdout = run
			cmd.Stderr = commandStderr{run}
			cmd.WaitDelay = time.Second
			var cleanup func()
			cleanup, err = startOwnedCommand(cmd)
			if err == nil {
				run.mu.Lock()
				run.started = true
				run.mu.Unlock()
				err = cmd.Wait()
				cleanup()
				if cmd.ProcessState != nil {
					code := cmd.ProcessState.ExitCode()
					exitCode = &code
				}
			}
		}
		if err == nil && action.Files {
			artifacts, e := a.collectCommandFiles(j.ID, files.OutputDirectory)
			if e != nil {
				err = e
			}
			run.mu.Lock()
			run.artifacts = artifacts
			run.mu.Unlock()
		}
		run.mu.Lock()
		if err != nil {
			left := 4096 - len(run.diagnostic)
			if left > 0 {
				message := []byte(err.Error())
				if len(message) > left {
					message = message[:left]
				}
				run.diagnostic = append(run.diagnostic, message...)
			}
		}
		run.err = err
		run.exitCode = exitCode
		run.timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		run.done = true
		run.mu.Unlock()
	}()
}

// Called with the app lock held, after durable state publication.
func (a *App) syncCommands() {
	for id, conversation := range a.conversations {
		if ended, _ := conversation.status(); ended {
			if record := a.state.Conversations[id]; record == nil || record.Status != "open" {
				delete(a.conversations, id)
			}
		}
	}
	for id, run := range a.commands {
		j := findJob(&a.state, id)
		if j == nil || terminal(j.Status) {
			run.cancel()
			select {
			case <-run.stopped:
				delete(a.commands, id)
			default:
			}
		}
	}
	for _, j := range a.state.Jobs {
		if j.Status == "running" && jobKind(j) != "connection-test" && a.commands[j.ID] == nil {
			if jobKind(j) == "command" {
				a.startCommand(j)
			} else {
				a.startNative(j)
			}
		}
	}
}

type commandStderr struct{ run *commandRun }

func (w commandStderr) Write(data []byte) (int, error) {
	w.run.writeDiagnostic(data)
	return w.run.Write(data)
}
