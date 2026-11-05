package bridge

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/thomasmikava/WebDeviceCable/internal/updates"
)

type App struct {
	relayCancels    map[string]context.CancelFunc
	updater         *updates.Manager
	updating        bool
	launchIntents   map[string]*LaunchIntent
	permissions     map[string]*PermissionRequest
	config          Config
	inputs          map[string]*nativeInput
	actions         map[string]configuredAction
	commands        map[string]*commandRun
	pairingRequests map[string]*pairingRequest
	conversations   map[string]*commandConversation
	identity        *ecdsa.PrivateKey
	channels        map[string]*secureChannel
	mu              sync.Mutex
	state           State
	leases          map[string]Lease
	dir             string
	lock            *flock.Flock
	adminKey        string
	sessions        map[string]session
	native          []NativeStatus
	nativeChecking  bool
	nativeDone      chan struct{}
	closed          bool
	now             func() time.Time
}
type session struct {
	CSRF    string
	Expires time.Time
}

func Open(dir string) (*App, error) {
	abs, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if e = PrepareDataDirectory(abs); e != nil {
		return nil, e
	}
	if e = hardenExistingStorage(abs); e != nil {
		return nil, e
	}
	lockPath := filepath.Join(abs, "runner.lock")
	if info, err := os.Lstat(lockPath); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("runner lock must be a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lock := flock.New(filepath.Join(abs, "runner.lock"))
	ok, e := lock.TryLock()
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, errors.New("this data directory already has a running bridge")
	}
	a := &App{inputs: map[string]*nativeInput{}, dir: abs, lock: lock, leases: map[string]Lease{}, sessions: map[string]session{}, now: time.Now, channels: map[string]*secureChannel{}, commands: map[string]*commandRun{}, pairingRequests: map[string]*pairingRequest{}, conversations: map[string]*commandConversation{}}
	success := false
	defer func() {
		if !success {
			lock.Close()
		}
	}()
	if e = a.loadConfig(); e != nil {
		return nil, e
	}
	if e = a.resumePanel(); e != nil {
		return nil, e
	}
	b, e := ReadLocalFile(filepath.Join(abs, "state.json"), 256<<20)
	if errors.Is(e, os.ErrNotExist) {
		a.state = State{Version: 1, DeviceID: secret(), Enabled: true, Pairings: map[string]*Pairing{}, Invitations: map[string]*Invitation{}, Jobs: []*Job{}, Receipts: map[string]Receipt{}}
	} else if e != nil {
		return nil, e
	} else if e = json.Unmarshal(b, &a.state); e != nil {
		return nil, fmt.Errorf("state is corrupt; preserved for recovery: %w", e)
	}
	if a.state.Version != 1 || a.state.Pairings == nil || a.state.Invitations == nil {
		return nil, errors.New("unsupported state format; refusing to overwrite it")
	}
	for _, p := range a.state.Pairings {
		if p == nil {
			return nil, errors.New("invalid pairing state; preserved for recovery")
		}
	}
	for _, j := range a.state.Jobs {
		if j == nil {
			return nil, errors.New("invalid job state; preserved for recovery")
		}
	}
	for _, c := range a.state.Conversations {
		if c == nil {
			return nil, errors.New("invalid conversation state; preserved for recovery")
		}
	}
	var identityErr error
	a.identity, identityErr = loadIdentity(abs)
	if identityErr != nil {
		return nil, identityErr
	}
	currentIdentity := publicKey(a.identity)
	for _, p := range a.state.Pairings {
		if !p.Revoked && (p.ClientPublicKey == "" || a.state.IdentityKey != currentIdentity) {
			p.Revoked = true
			p.CredentialHash = ""
			a.state.SecurityReset = true
		}
	}
	a.state.IdentityKey = currentIdentity
	if a.state.Programs == nil {
		a.state.Programs = defaultPrograms()
	}
	for id, program := range a.state.Programs {
		if id != program.ID {
			return nil, errors.New("invalid AI program identity")
		}
		if err := validateProgram(program); err != nil {
			return nil, err
		}
	}
	if a.state.Sharing == nil {
		a.state.Sharing = defaultSharing()
	}
	if a.state.Conversations == nil {
		a.state.Conversations = map[string]*Conversation{}
	}
	a.closeConversations(&a.state, "", "runner_restarted", a.now())
	for _, c := range a.state.Conversations {
		c.Tools = nil
	}
	// No automatic replay after a crash: every outstanding request lost its originating tab lease.
	for _, j := range a.state.Jobs {
		if !terminal(j.Status) {
			finish(j, "interrupted", "runner_restarted", "Runner restarted. Submit a new request explicitly; this job will not be replayed.", a.now())
		}
	}
	if e = os.RemoveAll(filepath.Join(abs, "runs")); e != nil {
		return nil, e
	}
	if e = a.migrateReceipts(); e != nil {
		return nil, e
	}
	a.state.Invitations = map[string]*Invitation{}
	if e = a.save(&a.state); e != nil {
		return nil, e
	}
	keyPath := filepath.Join(abs, "admin.key")
	key, e := ReadLocalFile(keyPath, 64)
	if errors.Is(e, os.ErrNotExist) {
		a.adminKey = secret()
		if e = writeLocalFile(keyPath, []byte(a.adminKey)); e != nil {
			return nil, e
		}
	} else if e != nil {
		return nil, e
	} else {
		a.adminKey = string(key)
		if len(a.adminKey) != 64 {
			return nil, errors.New("invalid control key")
		}
	}
	success = true
	return a, nil
}
func (a *App) Close() error {
	if a.updater != nil {
		a.updater.Close()
	}
	a.mu.Lock()
	a.closed = true
	for _, cancel := range a.relayCancels {
		cancel()
	}
	nativeDone := a.nativeDone
	runs := []*commandRun{}
	for _, r := range a.commands {
		r.cancel()
		runs = append(runs, r)
	}
	conversations := []*commandConversation{}
	for _, c := range a.conversations {
		c.cancel()
		conversations = append(conversations, c)
	}
	a.mu.Unlock()
	for _, c := range conversations {
		<-c.stopped
	}
	for _, r := range runs {
		<-r.stopped
	}
	if nativeDone != nil {
		<-nativeDone
	}
	return a.lock.Close()
}
func (a *App) save(s *State) error {
	b, e := jsonData(s)
	if e != nil {
		return e
	}
	if len(b) > 32<<20 {
		return errors.New("state exceeds storage limit")
	}
	return writeLocalFile(filepath.Join(a.dir, "state.json"), b)
}

// Transactions publish only after persistence succeeds. Call with mu held.
func (a *App) update(fn func(*State) error) error {
	b, _ := jsonData(a.state)
	var next State
	if e := json.Unmarshal(b, &next); e != nil {
		return e
	}
	if e := fn(&next); e != nil {
		return e
	}
	for _, c := range next.Conversations {
		if c.Status != "open" {
			c.Tools = nil
		}
	}
	for _, j := range next.Jobs {
		if terminal(j.Status) {
			j.Request.Tools = nil
		}
	}
	// Bound retained history as output limits grow; replay receipts remain durable.
	total := 0
	for _, j := range next.Jobs {
		total += len(j.Output) + len(j.JSON)
	}
	for total > 20<<20 {
		oldest := -1
		for i, j := range next.Jobs {
			if terminal(j.Status) && (oldest < 0 || j.CreatedAt.Before(next.Jobs[oldest].CreatedAt)) {
				oldest = i
			}
		}
		if oldest < 0 {
			break
		}
		j := next.Jobs[oldest]
		total -= len(j.Output) + len(j.JSON)
		next.Jobs = append(next.Jobs[:oldest], next.Jobs[oldest+1:]...)
	}
	after, e := jsonData(next)
	if e != nil {
		return e
	}
	if bytes.Equal(b, after) {
		return nil
	}
	next.Revision++
	if e := a.save(&next); e != nil {
		return e
	}
	previousJobs := a.state.Jobs
	a.state = next
	for _, job := range previousJobs {
		if terminal(job.Status) && findJob(&next, job.ID) == nil && validStorageID(job.ID) {
			_ = os.RemoveAll(filepath.Join(a.dir, "artifacts", job.ID))
		}
	}
	for id, c := range a.conversations {
		record := a.state.Conversations[id]
		if record == nil || record.Status != "open" {
			c.cancel()
		}
	}
	for id, run := range a.commands {
		j := findJob(&a.state, id)
		if j == nil || terminal(j.Status) {
			run.cancel()
		}
	}
	return nil
}
func (a *App) tick() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for id, input := range a.inputs {
		lease, ok := a.leases[input.LeaseID]
		p := a.state.Pairings[input.PairingID]
		if !ok || !now.Before(lease.ExpiresAt) || p == nil || p.Revoked || p.Paused {
			delete(a.inputs, id)
		}
	}
	for key, l := range a.leases {
		if !now.Before(l.ExpiresAt) {
			delete(a.leases, key)
		}
	}
	e := a.update(func(s *State) error {
		a.reconcileConversations(s, now)
		for _, j := range s.Jobs {
			if terminal(j.Status) {
				continue
			}
			p := s.Pairings[j.PairingID]
			l, ok := a.leases[j.LeaseID]
			switch {
			case j.Request.ConversationID != "" && (s.Conversations[j.Request.ConversationID] == nil || s.Conversations[j.Request.ConversationID].Status != "open"):
				finish(j, "cancelled", "session_closed", "The session has ended.", now)
			case !ok || l.PairingID != j.PairingID:
				finish(j, "cancelled", "tab_disconnected", "The requesting app tab stopped its lease. No other tab's jobs are affected.", now)
			case p == nil || p.Revoked:
				finish(j, "cancelled", "app_revoked", "Application access was revoked.", now)
			case now.Sub(j.CreatedAt) >= time.Duration(j.Request.TimeoutSeconds)*time.Second:
				finish(j, "timed_out", "deadline_exceeded", "The request deadline includes time awaiting approval.", now)
			case !s.Enabled || p.Paused:
				if j.Status == "running" {
					finish(j, "cancelled", "paused", "Execution paused by the user.", now)
				}
			case jobKind(j) != "connection-test":
				if jobKind(j) == "command" {
					if err := a.authorizeAction(p, jobAction(j), j.ActionHash); err != nil {
						finish(j, "blocked", "command_access_changed", "Review this website's command access in WebDeviceCable.", now)
						continue
					}
				}
				if run := a.commands[j.ID]; j.Status == "running" && run != nil {
					output, truncated, done, err, exitCode, timedOut := run.snapshot()
					started, diagnostic, failure := run.details()
					if started && j.Execution == nil && jobKind(j) == "command" {
						command := a.actions[jobAction(j)]
						j.Execution = &ExecutionMetadata{Kind: "command", Name: command.Title, Version: command.Version}
						if command.Version != "" {
							j.Execution.VersionSource = "configured"
						}
						j.Revision++
					}
					run.mu.Lock()
					j.Artifacts = append([]Artifact(nil), run.artifacts...)
					run.mu.Unlock()
					if jobKind(j) != "command" {
						run.mu.Lock()
						if run.execution != nil {
							j.Artifacts = append([]Artifact(nil), run.artifacts...)
							j.Usage = run.usage
							j.JSON = append(json.RawMessage(nil), run.jsonOutput...)
							metadata := *run.execution
							if j.Execution == nil {
								j.Revision++
							}
							j.Execution = &metadata
							j.ActualProvider = metadata.Provider
							j.ActualModel = metadata.Model
							j.Billing = "native_subscription"
							if metadata.Kind == "custom" {
								j.Billing = "program_defined"
							}
						}
						run.mu.Unlock()
					}
					if done {
						j.Diagnostic = diagnostic
					}
					if output != j.Output || truncated != j.OutputTruncated {
						j.Output = output
						j.OutputTruncated = truncated
						j.Revision++
					}
					if done {
						j.ExitCode = exitCode
						switch {
						case truncated:
							finish(j, "failed", "output_limit", "The program exceeded the configured text output limit. Partial output is retained.", now)
						case timedOut:
							finish(j, "timed_out", "deadline_exceeded", "The request timed out.", now)
						case err != nil:
							code, message := "command_failed", "The command failed. Check its output and exit code."
							if !started {
								code = "command_start_failed"
								message = "The command could not start."
							}
							if failure != nil {
								code = failure.Code
								message = failure.Message
							}
							finish(j, "failed", code, message, now)
						default:
							finish(j, "completed", "", "Request finished.", now)
						}
					}
				}
			case j.Status == "running":
				elapsed := now.Sub(*j.StartedAt)
				output := "Checking the local runner…"
				if elapsed > 400*time.Millisecond {
					output = "Checking the local runner…\nChecking this tab's lease…"
				}
				if elapsed > 900*time.Millisecond {
					j.Output = "Connection test passed. No AI provider was called."
					finish(j, "completed", "", "Local pairing, consent, queue and tab lease are working.", now)
				} else if output != j.Output {
					j.Output = output
					j.Revision++
				}
			}
		}
		active := 0
		counts := map[string]int{}
		liveSessions := map[string]bool{}
		for id, runtime := range a.conversations {
			ended, _ := runtime.status()
			if !ended {
				active++
				liveSessions[id] = true
				if c := s.Conversations[id]; c != nil {
					counts[c.Plan.Action]++
				}
			}
		}
		for _, j := range s.Jobs {
			if j.Status == "running" && j.Request.ConversationID == "" {
				active++
				if jobKind(j) == "command" {
					counts[jobAction(j)]++
				}
			}
		}
		for _, j := range s.Jobs {
			if j.Status != "queued" {
				continue
			}
			p := s.Pairings[j.PairingID]
			if p == nil || p.Paused || !s.Enabled {
				continue
			}
			existingSession := j.Request.ConversationID != "" && liveSessions[j.Request.ConversationID]
			if !existingSession && active >= 4 {
				continue
			}
			if jobKind(j) == "command" {
				if e := a.authorizeAction(p, jobAction(j), j.ActionHash); e != nil {
					finish(j, "blocked", "command_access_changed", "Review this website's command access in WebDeviceCable.", now)
					continue
				}
				if !existingSession && counts[jobAction(j)] >= a.actions[jobAction(j)].MaxConcurrent {
					continue
				}
				if !existingSession {
					counts[jobAction(j)]++
					if j.Request.ConversationID != "" {
						liveSessions[j.Request.ConversationID] = true
					}
				}
			} else if jobKind(j) != "connection-test" {
				if err := a.nativeAvailable(p, jobProvider(j), jobKind(j)); err != nil {
					var issue *apiError
					errors.As(err, &issue)
					finish(j, "blocked", issue.Code, issue.Message, now)
					continue
				}
				if j.Request.ConversationID != "" {
					liveSessions[j.Request.ConversationID] = true
				}
			}
			j.Status = "running"
			j.StartedAt = &now
			j.Revision++
			if !existingSession {
				active++
			}
		}
		a.reconcileConversations(s, now)
		return nil
	})
	if e == nil {
		a.syncCommands()
	}
	return e
}
