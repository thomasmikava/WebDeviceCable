package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/gofrs/flock"
	"github.com/thomasmikava/WebDeviceCable/internal/bridge"
	"github.com/thomasmikava/WebDeviceCable/internal/updates"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var official = "false"
var releasePublicKey = ""

func updateCLI(args []string) error {
	if len(args) == 0 {
		return errors.New("use WebDeviceCable update status, check or install")
	}
	action := args[0]
	flags := flag.NewFlagSet("WebDeviceCable update "+action, flag.ContinueOnError)
	root, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	dir := flags.String("data-dir", defaultDataDirectory(root), "Private state directory")
	force := flags.Bool("end-sessions", false, "End current work before installing")
	v := flags.String("version", "", "Native update version")
	stage := flags.String("stage", "", "Native update stage")
	if e = flags.Parse(args[1:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected update arguments")
	}
	switch action {
	case "status", "check", "install", "native-poll", "native-report":
	default:
		return errors.New("unknown update action")
	}
	data, e := updateIPC(*dir, map[string]any{"action": action, "endSessions": *force, "version": *v, "stage": *stage})
	if e != nil {
		return e
	}
	_, e = fmt.Fprintln(os.Stdout, string(data))
	return e
}
func updateIPC(dir string, body any) ([]byte, error) {
	info, e := readRuntime(dir)
	if e != nil || !alive(dir, info) {
		return nil, errors.New("open WebDeviceCable before checking for updates")
	}
	key, e := bridge.ReadLocalFile(filepath.Join(dir, "admin.key"), 64)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(body)
	if e != nil {
		return nil, e
	}
	req, e := http.NewRequest("POST", info.URL+"/api/internal/updates", bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+string(key))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	r, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	data, e := io.ReadAll(io.LimitReader(r.Body, 16384))
	if e != nil {
		return nil, e
	}
	if r.StatusCode != 200 {
		var result struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &result)
		if result.Error.Message != "" {
			return nil, errors.New(result.Error.Message)
		}
		return nil, errors.New("update request was refused")
	}
	return data, nil
}
func configuredUpdates(a *bridge.App, dir string, port int) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	if runtime.GOOS == "windows" && filepath.Base(exe) == "WebDeviceCable Launcher.exe" {
		exe = filepath.Join(filepath.Dir(exe), "WebDeviceCable.exe")
	}
	native := runtime.GOOS == "darwin" && strings.HasSuffix(filepath.Dir(exe), ".app/Contents/Resources")
	return a.ConfigureUpdates(updates.Build{Version: version, Official: official == "true", PublicKey: releasePublicKey, OS: runtime.GOOS, Arch: runtime.GOARCH, Native: native}, func(p updates.Plan) error {
		if runtime.GOOS == "darwin" {
			return errors.New("open the installed WebDeviceCable application to install this update")
		}
		if e := validatePayload(p.Staged); e != nil {
			return e
		}
		// Run a copy of the current, trusted executable outside the installation.
		helperDir, e := os.MkdirTemp(filepath.Join(dir, "updates"), "install-")
		if e != nil {
			return e
		}
		if e = bridge.PrepareDataDirectory(helperDir); e != nil {
			return e
		}
		helper := filepath.Join(helperDir, "WebDeviceCable")
		if runtime.GOOS == "windows" {
			helper += ".exe"
		}
		if e = copyUpdateFile(exe, helper); e != nil {
			return e
		}
		planPath := filepath.Join(helperDir, "plan.json")
		raw, _ := json.Marshal(p)
		if e = os.WriteFile(planPath, raw, 0600); e != nil {
			return e
		}
		args := []string{"internal-update", dir, exe, planPath, strconv.Itoa(port)}
		cmd := exec.Command(helper, args...)
		detach(cmd)
		if e = cmd.Start(); e != nil {
			return e
		}
		return cmd.Process.Release()
	})
}
func copyUpdateFile(from, to string) error {
	i, e := os.Lstat(from)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() {
		return errors.New("update executable is not a regular file")
	}
	s, e := os.Open(from)
	if e != nil {
		return e
	}
	defer s.Close()
	d, e := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		return e
	}
	_, e = io.Copy(d, s)
	if e == nil {
		e = d.Sync()
	}
	c := d.Close()
	if e != nil {
		return e
	}
	return c
}
func validatePayload(dir string) error {
	name := "WebDeviceCable"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(dir, name)
	if e := verifyExecutable(p); e != nil {
		return e
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"WebDeviceCable Launcher.exe", "Uninstall.ps1"} {
			if e := verifyExecutable(filepath.Join(dir, name)); e != nil {
				return e
			}
		}
	}
	return nil
}
func applyUpdate(args []string) (result error) {
	if len(args) != 4 {
		return errors.New("invalid internal update invocation")
	}
	dir, target, planPath := args[0], args[1], args[2]
	defer func() {
		if result != nil {
			_, _ = updateIPC(dir, map[string]any{"action": "failed"})
		}
	}()
	port, e := strconv.Atoi(args[3])
	if e != nil || port < 1 || port > 65535 {
		return errors.New("invalid update port")
	}
	if !updates.Inside(filepath.Join(dir, "updates"), planPath) {
		return errors.New("invalid update plan location")
	}
	raw, e := bridge.ReadLocalFile(planPath, 128<<10)
	if e != nil {
		return e
	}
	var p updates.Plan
	if json.Unmarshal(raw, &p) != nil {
		return errors.New("invalid update plan")
	}
	// Revalidate bytes in the helper, immediately before replacement.
	release, e := updates.Verify(p.Metadata, releasePublicKey)
	if e != nil || release.Version != p.Release.Version || !updates.Newer(release.Version, version) {
		return errors.New("update authorization could not be verified")
	}
	artifact, e := release.Target(updates.Build{OS: runtime.GOOS, Arch: runtime.GOARCH})
	if e != nil {
		return e
	}
	archive, e := bridge.ReadLocalFile(p.Archive, updates.MaxDownload)
	if e != nil || int64(len(archive)) != artifact.Size || updates.Hash(archive) != artifact.SHA256 {
		return errors.New("staged update has changed")
	}
	// Extract again to a fresh directory; do not trust a mutable extracted payload.
	staged := filepath.Join(filepath.Dir(planPath), "payload")
	if e = updates.Extract(p.Archive, staged, runtime.GOOS == "windows"); e != nil {
		return e
	}
	if e = validatePayload(staged); e != nil {
		return e
	}
	if data, e := updateIPC(dir, map[string]any{"action": "status"}); e != nil {
		return e
	} else {
		var s updates.Status
		if json.Unmarshal(data, &s) != nil || s.Stage != "installing" {
			return errors.New("runner has not authorized update shutdown")
		}
	}
	parent := filepath.Dir(target)
	name := "WebDeviceCable"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	names := []string{name}
	if runtime.GOOS == "windows" {
		names = append(names, "WebDeviceCable Launcher.exe", "Uninstall.cmd", "Uninstall.ps1")
	}
	replacements, e := prepareReplacements(staged, parent, filepath.Dir(planPath), names)
	if e != nil {
		return e
	}
	defer func() {
		for _, r := range replacements {
			_ = os.Remove(r.next)
		}
	}()
	if e = stopDesktop(dir); e != nil {
		return e
	}
	if e = run([]string{"stop", "--data-dir", dir, "--if-running"}); e != nil {
		return e
	}
	unlock, e := waitForRunnerExit(dir)
	if e != nil {
		return e
	}
	changed := 0
	for i, r := range replacements {
		if e = replaceUpdateFile(r.next, r.target); e != nil {
			for j := changed - 1; j >= 0; j-- {
				restoreReplacement(replacements[j])
			}
			unlock()
			_ = exec.Command(target, "start", "--data-dir", dir, "--port", strconv.Itoa(port), "--no-open").Run()
			return fmt.Errorf("update replacement failed; recovery files were retained: %w", e)
		}
		changed = i + 1
	}
	unlock()
	cmd := exec.Command(target, "start", "--data-dir", dir, "--port", strconv.Itoa(port), "--no-open")
	detach(cmd)
	if e = cmd.Start(); e != nil {
		return e
	}
	_ = cmd.Wait()
	info, e := readRuntime(dir)
	if e != nil || !alive(dir, info) {
		return errors.New("update installed but startup failed; keep private data and reinstall the previous verified release")
	}
	// Restore the tray without opening the browser, preserving the same data directory.
	launcher := target
	if runtime.GOOS == "windows" {
		launcher = filepath.Join(parent, "WebDeviceCable Launcher.exe")
	}
	cmd = exec.Command(launcher, "desktop", "--data-dir", dir, "--port", strconv.Itoa(port), "--no-open")
	detach(cmd)
	if e = cmd.Start(); e != nil {
		return e
	}
	_ = cmd.Process.Release()
	_ = installedVersion(parent, p.Release.Version)
	return nil
}

// Keep shutdown and replacement separate; a failed graceful exit never replaces a live executable.
func waitForRunnerExit(dir string) (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		release, ok, e := runnerUnlocked(dir)
		if e != nil {
			return nil, e
		}
		if ok {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("WebDeviceCable did not stop; update was not installed")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func runnerUnlocked(dir string) (func(), bool, error) {
	l := flock.New(filepath.Join(dir, "runner.lock"))
	ok, e := l.TryLock()
	if e != nil || !ok {
		_ = l.Close()
		return nil, false, e
	}
	return func() { _ = l.Close() }, true, nil
}

// Prepare complete replacement files beside their targets before stopping any work.
type replacement struct{ target, next, backup string }

func prepareReplacements(staged, parent, backupDir string, names []string) (result []replacement, err error) {
	defer func() {
		if err != nil {
			for _, r := range result {
				_ = os.Remove(r.next)
			}
		}
	}()
	for _, name := range names {
		target := filepath.Join(parent, name)
		nextFile, e := os.CreateTemp(parent, ".WebDeviceCable-update-")
		if e != nil {
			return result, errors.New("installation is not writable; install this update manually")
		}
		next := nextFile.Name()
		_ = nextFile.Close()
		_ = os.Remove(next)
		r := replacement{target: target, next: next, backup: filepath.Join(backupDir, "previous-"+name)}
		result = append(result, r)
		if e = copyUpdateFile(filepath.Join(staged, name), next); e != nil {
			return result, e
		}
		if e = copyUpdateFile(target, r.backup); e != nil {
			return result, e
		}
	}
	return result, nil
}
func restoreReplacement(r replacement) error {
	temp, e := os.CreateTemp(filepath.Dir(r.target), ".WebDeviceCable-restore-")
	if e != nil {
		return e
	}
	path := temp.Name()
	_ = temp.Close()
	_ = os.Remove(path)
	defer os.Remove(path)
	if e = copyUpdateFile(r.backup, path); e != nil {
		return e
	}
	return replaceUpdateFile(path, r.target)
}
