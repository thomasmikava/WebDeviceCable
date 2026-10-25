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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = clientEnv()
	var output cappedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
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
	version := nativeVersionPattern
	for _, name := range []string{"codex", "claude"} {
		s := NativeStatus{ID: name, Authentication: "not_checked"}
		path, e := nativePath(name)
		if e != nil {
			out = append(out, s)
			continue
		}
		s.Installed = true
		raw, e := nativeOutput(path, dir, "--version")
		if e == nil {
			s.Version = version.FindString(raw)
		}
		if name == "codex" {
			raw, e = nativeOutput(path, dir, "login", "status")
			switch {
			case e != nil:
				s.Authentication = "not_signed_in_or_unavailable"
			case strings.Contains(raw, "ChatGPT"):
				s.Authentication = "native_subscription"
			case strings.Contains(strings.ToLower(raw), "api key"):
				s.Authentication = "api_key_not_allowed"
			default:
				s.Authentication = "unknown"
			}
		}
		if name == "claude" {
			s = checkClaude(dir, path, s)
		}
		if name == "codex" && s.Authentication == "native_subscription" {
			s = checkCodex(dir, path, s)
		}
		out = append(out, s)
	}
	return out
}

type NativeModel struct {
	ID               string `json:"model"`
	Name             string `json:"displayName"`
	DefaultReasoning string `json:"defaultReasoningEffort"`
	Reasoning        []struct {
		Effort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

func checkCodex(parent, path string, status NativeStatus) NativeStatus {
	dir, err := os.MkdirTemp(parent, ".native-check-")
	if err != nil {
		status.Detail = "Cannot create a private compatibility check."
		return status
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = os.WriteFile(filepath.Join(dir, "instructions.txt"), []byte("Use supplied inputs only.\n"), 0600)
	config := codexConfig(dir, false)
	var diagnostic cappedBuffer
	rpc, err := startNativeRPC(ctx, path, dir, config, &diagnostic)
	if err == nil {
		defer rpc.close()
		_, _, err = prepareCodex(ctx, rpc, dir, config, ExecutionTarget{})
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
	status.Detail = "Ready for local applications. Uses your existing ChatGPT sign-in."
	return status
}
func (a *App) checkNative() {
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
	a.mu.Unlock()
	result := CheckNative(a.dir)
	a.mu.Lock()
	a.native = result
	a.nativeChecking = false
	close(done)
	a.mu.Unlock()
}
func (a *App) capabilities(p *Pairing) []Capability {
	result := Capabilities()
	for i := range result {
		for _, status := range a.native {
			if status.ID == result[i].ID {
				result[i].Execution = status.Execution
				if status.Detail != "" {
					result[i].Reason = status.Detail
				}
			}
		}
		if p != nil {
			if err := nativeEligibility(p, result[i].ID); err != nil {
				result[i].Execution = false
				result[i].Reason = err.Error()
			}
		}
		if result[i].Execution {
			result[i].AppTools = true
			result[i].Workspace = true
			result[i].Text = true
			result[i].JSON = true
			result[i].ImageInput = true
			result[i].DocumentInput = true
			for _, s := range a.native {
				if s.ID == result[i].ID && s.Images {
					result[i].ImageGeneration = true
					result[i].ImageEditing = true
				}
			}
		}
	}
	return result
}
