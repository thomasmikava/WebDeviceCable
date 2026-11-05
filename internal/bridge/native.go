package bridge

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// These diagnostics invoke official clients, never read their credential files,
// never start inference, and never return raw client output to an application.
var nativeVersionPattern = regexp.MustCompile(`\b\d+\.\d+\.\d+(?:[-+.][a-zA-Z0-9.-]+)?\b`)

type NativeStatus struct {
	accountKey     string
	Integration    string        `json:"integration,omitempty"`
	ConfigHash     string        `json:"configHash,omitempty"`
	Account        string        `json:"account,omitempty"`
	Sessions       bool          `json:"sessions"`
	ID             string        `json:"id"`
	Installed      bool          `json:"installed"`
	Version        string        `json:"version,omitempty"`
	Authentication string        `json:"authentication"`
	Images         bool          `json:"images"`
	Execution      bool          `json:"execution"`
	Code           string        `json:"code,omitempty"`
	Detail         string        `json:"detail,omitempty"`
	Models         []NativeModel `json:"models,omitempty"`
}
type cappedBuffer struct {
	bytes.Buffer
	mu sync.Mutex
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := 4096 - b.Len()
	if left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		b.Buffer.Write(p)
	}
	return n, nil
}
func clientEnv() []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true, "TMPDIR": true, "CODEX_HOME": true, "CLAUDE_CONFIG_DIR": true}
	env := []string{}
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if allowed[strings.ToUpper(k)] {
			env = append(env, v)
		}
	}
	return env
}
func nativeOutput(path, dir string, args ...string) (string, error) {
	return nativeOutputContext(context.Background(), path, dir, args...)
}
func nativeOutputContext(parent context.Context, path, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	program := contextProgram(parent)
	args = append(append([]string{}, program.Arguments...), args...)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = programEnv(program)
	var output cappedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	var diagnostic cappedBuffer
	if program.Type == "custom" {
		cmd.Stderr = &diagnostic
	}
	// Diagnostics use the same hidden, owned process path as native requests.
	cleanup, e := startOwnedCommand(cmd)
	if e != nil {
		return output.String(), e
	}
	defer cleanup()
	e = cmd.Wait()
	return output.String(), e
}
func CheckNative(dir string) []NativeStatus {
	out := []NativeStatus{}
	for _, id := range []string{"codex", "claude"} {
		out = append(out, checkProgram(dir, defaultPrograms()[id]))
	}
	return out
}
func checkProgram(dir string, p AIProgram) NativeStatus {
	s := NativeStatus{ID: p.ID, Integration: p.Type, ConfigHash: p.hash(), Authentication: "not_checked"}
	path, err := p.path()
	if err != nil {
		s.Detail = "Executable is unavailable. Edit the AI program or install its client."
		return s
	}
	s.Installed = true
	ctx := withProgram(context.Background(), p)
	if p.Type == "custom" {
		info, err := inspectCustom(ctx, path, dir)
		if err != nil {
			s.Code = "custom_interface_unavailable"
			s.Detail = err.Error()
			return s
		}
		s.Version = info.Version
		s.Account = info.Account
		s.accountKey = info.Account
		if s.accountKey == "" {
			s.accountKey = "unreported:" + p.hash()
		}
		s.Models = info.Models
		s.Sessions = info.Sessions
		s.Execution = true
		s.Authentication = "reported_by_program"
		s.Detail = "WDC JSONL interface available. Runs with your OS account permissions; no native sandbox guarantee."
		if s.Account == "" {
			s.Authentication = "identity_unavailable"
			s.Detail += " Account identity is not reported by this harness."
		}
		return s
	}
	raw, err := nativeOutputContext(ctx, path, dir, "--version")
	if err == nil {
		s.Version = nativeVersionPattern.FindString(raw)
	}
	if p.Type == "claude" {
		return checkClaude(dir, path, s, p)
	}
	raw, err = nativeOutputContext(ctx, path, dir, "login", "status")
	switch {
	case err != nil:
		s.Authentication = "not_signed_in_or_unavailable"
	case strings.Contains(raw, "ChatGPT"):
		s.Authentication = "native_subscription"
	case strings.Contains(strings.ToLower(raw), "api key"):
		s.Authentication = "api_key_not_allowed"
	default:
		s.Authentication = "unknown"
	}
	if s.Authentication == "native_subscription" {
		s = checkCodex(dir, path, s, p)
	}
	return s
}

type NativeReasoning struct {
	Effort string `json:"reasoningEffort"`
}
type NativeModel struct {
	ID               string            `json:"model"`
	Name             string            `json:"displayName"`
	DefaultReasoning string            `json:"defaultReasoningEffort"`
	Reasoning        []NativeReasoning `json:"supportedReasoningEfforts"`
}

func checkCodex(parent, path string, status NativeStatus, programs ...AIProgram) NativeStatus {
	dir, err := os.MkdirTemp(parent, ".native-check-")
	if err != nil {
		status.Detail = "Cannot create a private compatibility check."
		return status
	}
	defer os.RemoveAll(dir)
	base := context.Background()
	if len(programs) > 0 {
		base = withProgram(base, programs[0])
	}
	ctx, cancel := context.WithTimeout(base, 15*time.Second)
	defer cancel()
	_ = os.WriteFile(filepath.Join(dir, "instructions.txt"), []byte("Use supplied inputs only.\n"), 0600)
	config := codexConfig(dir, false)
	var diagnostic cappedBuffer
	rpc, err := startNativeRPC(ctx, path, dir, config, &diagnostic)
	if err == nil {
		defer rpc.close()
		var account string
		_, account, err = prepareCodex(ctx, rpc, dir, config, ExecutionTarget{})
		if err == nil {
			status.Account = rpc.accountIdentity
			status.accountKey = account
		}
		if err == nil {
			var models struct {
				Data []NativeModel `json:"data"`
			}
			if rpc.call(ctx, "model/list", map[string]any{"includeHidden": false}, &models) == nil {
				status.Models = models.Data
			}
		}
	}
	if err != nil {
		status.Code = "native_isolation_unavailable"
		status.Detail = "Codex compatibility check: " + cleanDiagnostic(err.Error())
		return status
	}
	// Image support also needs the native code-mode host. Check that separate
	// configuration before advertising the built-in image tool.
	imageConfig := codexConfig(dir, true)
	imageRPC, imageErr := startNativeRPC(ctx, path, dir, imageConfig, &diagnostic)
	if imageErr == nil {
		_, _, imageErr = prepareCodex(ctx, imageRPC, dir, imageConfig, ExecutionTarget{})
		imageRPC.close()
	}
	status.Images = imageErr == nil
	status.Execution = true
	status.Sessions = true
	status.Detail = "Ready for local applications. Uses your existing ChatGPT sign-in."
	return status
}
func (a *App) checkNative() { a.checkPrograms("") }
func (a *App) checkPrograms(id string) {
	if a.config.Bridge.DisableNative {
		return
	}
	a.mu.Lock()
	if a.nativeChecking || a.closed {
		a.mu.Unlock()
		return
	}
	a.nativeChecking = true
	a.nativeDone = make(chan struct{})
	done := a.nativeDone
	programs := a.programList()
	a.mu.Unlock()
	result := []NativeStatus{}
	for _, p := range programs {
		if id == "" || p.ID == id {
			result = append(result, checkProgram(a.dir, p))
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range result {
		p, ok := a.program(s.ID)
		if !ok || p.hash() != s.ConfigHash {
			continue
		}
		a.dropProgramStatus(s.ID)
		a.native = append(a.native, s)
	}
	a.nativeChecking = false
	close(done)
}
func (a *App) capabilities(p *Pairing) []Capability {
	result := []Capability{}
	for _, program := range a.programList() {
		cap := Capability{ID: program.ID, Name: program.Name, Integration: program.Type, Isolation: "native", Reason: "Check this AI program in Settings."}
		if program.Type == "custom" {
			cap.Isolation = "os-account"
			cap.NativeInterface = "WDC AI JSONL v1"
			cap.Eligibility = "Explicit per-connection command permission required. No native isolation guarantee."
		} else {
			for _, builtIn := range Capabilities() {
				if builtIn.ID == program.Type {
					cap.NativeInterface = builtIn.NativeInterface
					cap.Eligibility = builtIn.Eligibility
					cap.Source = builtIn.Source
					cap.Reason = builtIn.Reason
				}
			}
		}
		for _, status := range a.native {
			if status.ID != program.ID {
				continue
			}
			cap.Execution = status.Execution
			cap.Reason = status.Detail
			cap.Sessions = status.Sessions
			if cap.Execution {
				cap.Text = true
				cap.Streaming = true
				if program.Type != "custom" {
					cap.JSON = true
					cap.ImageInput = true
					cap.DocumentInput = true
					cap.AppTools = true
					cap.Workspace = true
					cap.ImageGeneration = status.Images
					cap.ImageEditing = status.Images
				}
			}
		}
		if p != nil {
			if err := a.knownProgramAccount(p, program.ID); err != nil {
				cap.Execution = false
				cap.Reason = err.Error()
			}
		}
		if a.config.Bridge.DisableNative {
			cap.Execution = false
			cap.Reason = "AI program execution is disabled in config.yaml."
		}
		if p != nil && program.Type == "custom" && p.ProgramGrants[program.ID] != program.hash() {
			cap.Execution = false
			cap.Reason = "Grant this custom AI program OS account permissions in the connection's Program access settings."
		}
		if p != nil && program.Type != "custom" {
			if err := nativeEligibility(p, program.ID); err != nil {
				cap.Execution = false
				cap.Reason = err.Error()
			}
		}
		result = append(result, cap)
	}
	return result
}

func (a *App) remoteCapabilities(p *Pairing) []Capability {
	result := a.capabilities(p)
	if a.state.Sharing != nil && !a.state.Sharing.Names {
		for i := range result {
			result[i].Name = "AI program"
			result[i].Integration = ""
			result[i].Source = ""
			if !result[i].Execution {
				result[i].Reason = "This program is unavailable. Review the selected entry in WDC Settings."
			}
		}
	}
	return result
}
