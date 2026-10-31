package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/thomasmikava/WebDeviceCable/internal/bridge"
	"github.com/thomasmikava/WebDeviceCable/internal/updates"
)

// Runs the generic update helper and a real replacement runner. macOS production
// app installation uses Sparkle separately; unsigned Windows fixtures cannot pass
// Authenticode and therefore require native signed release acceptance instead.
func TestUpdateHelperStartsRealReplacementWithoutChangingIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("real Windows replacement requires an Authenticode-signed fixture")
	}
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	installed := filepath.Join(root, "installed")
	backup := filepath.Join(dataDir, "updates", "install-fixture")
	for _, p := range []string{dataDir, installed, backup} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(dataDir, "config.yaml"), []byte("bridge:\n  disableNative: true\nactions: []\n"), 0600); e != nil {
		t.Fatal(e)
	}
	a, e := bridge.Open(dataDir)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	baseline := map[string][]byte{}
	for _, name := range []string{"identity.key", "admin.key", "state.json", "config.yaml"} {
		baseline[name], e = os.ReadFile(filepath.Join(dataDir, name))
		if e != nil {
			t.Fatal(e)
		}
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	trust := base64.StdEncoding.EncodeToString(pub)
	source, _ := filepath.Abs("../..")
	payload := filepath.Join(root, "new-WebDeviceCable")
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-ldflags=-s -w -X main.version=0.2.0", "-o", payload, "./cmd/webdevicecable")
	command.Dir = source
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("fixture build failed: %v %s", e, output)
	}
	target := filepath.Join(installed, "WebDeviceCable")
	oldBuild := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-ldflags=-s -w -X main.version=0.1.0", "-o", target, "./cmd/webdevicecable")
	oldBuild.Dir = source
	if output, e := oldBuild.CombinedOutput(); e != nil {
		t.Fatalf("old fixture build failed: %v %s", e, output)
	}
	if output, e := exec.Command(target, "version").Output(); e != nil || strings.TrimSpace(string(output)) != "0.1.0" {
		t.Fatal("old fixture has the wrong version", e)
	}
	content, e := os.ReadFile(payload)
	if e != nil {
		t.Fatal(e)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	writer := tar.NewWriter(gz)
	if e = writer.WriteHeader(&tar.Header{Name: "WebDeviceCable-0.2.0/WebDeviceCable", Size: int64(len(content)), Mode: 0755}); e != nil {
		t.Fatal(e)
	}
	_, _ = writer.Write(content)
	_ = writer.Close()
	_ = gz.Close()
	archivePath := filepath.Join(backup, "release.tgz")
	if e = os.WriteFile(archivePath, archive.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	artifact := updates.Artifact{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "https://github.com/thomasmikava/WebDeviceCable/releases/download/v0.2.0/fixture.tar.gz", Size: int64(archive.Len()), SHA256: updates.Hash(archive.Bytes())}
	release := updates.Release{Format: 1, Version: "0.2.0", StateVersion: 1, ProtocolVersion: 2, Artifacts: []updates.Artifact{artifact}}
	raw, _ := json.Marshal(release)
	metadata, _ := json.Marshal(updates.Envelope{Payload: base64.StdEncoding.EncodeToString(raw), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, updates.Message(raw)))})
	plan := updates.Plan{Metadata: metadata, Release: release, Artifact: artifact, Archive: archivePath}
	planFile := filepath.Join(backup, "plan.json")
	raw, _ = json.Marshal(plan)
	if e = os.WriteFile(planFile, raw, 0600); e != nil {
		t.Fatal(e)
	}
	// The old controller fixture authenticates the owner's already-drained install.
	// Real idle/consent/admission behavior is exercised independently in bridge tests.
	var server *httptest.Server
	var info bridge.RuntimeInfo
	admin := string(baseline["admin.key"])
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/runtime" {
			var in struct {
				Nonce string `json:"nonce"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			_ = json.NewEncoder(w).Encode(map[string]any{"pid": info.PID, "proof": bridge.RuntimeProof(admin, in.Nonce, info)})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+admin {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Path == "/api/shutdown" {
			_ = os.Remove(filepath.Join(dataDir, "runner.json"))
			_, _ = io.WriteString(w, `{"ok":true}`)
			go server.Close()
			return
		}
		_ = json.NewEncoder(w).Encode(updates.Status{Stage: "installing", Version: "0.1.0"})
	}))
	defer server.Close()
	info = bridge.RuntimeInfo{URL: server.URL, PID: os.Getpid()}
	raw, _ = json.Marshal(info)
	_ = os.WriteFile(filepath.Join(dataDir, "runner.json"), raw, 0600)
	defer func() {
		cmd := exec.Command(target, "stop", "--data-dir", dataDir, "--if-running")
		_ = cmd.Run()
		_ = stopDesktop(dataDir)
	}()
	previousVersion, previousTrust := version, releasePublicKey
	version, releasePublicKey = "0.1.0", trust
	defer func() { version, releasePublicKey = previousVersion, previousTrust }()
	port := strings.Split(server.URL, ":")
	number, e := strconv.Atoi(port[len(port)-1])
	if e != nil {
		t.Fatal(e)
	}
	if e = applyUpdate([]string{dataDir, target, planFile, strconv.Itoa(number)}); e != nil {
		t.Fatal(e)
	}
	current, e := readRuntime(dataDir)
	if e != nil || !alive(dataDir, current) {
		t.Fatal("replacement runner was not authenticated", e)
	}
	for name, before := range baseline {
		after, e := os.ReadFile(filepath.Join(dataDir, name))
		if e != nil || !bytes.Equal(before, after) {
			t.Fatalf("changed private %s", name)
		}
	}
	output, e := exec.Command(target, "version").Output()
	if e != nil || strings.TrimSpace(string(output)) != "0.2.0" {
		t.Fatal("wrong replacement version", e)
	}
	if _, e = os.Stat(filepath.Join(backup, "previous-WebDeviceCable")); e != nil {
		t.Fatal("lost recovery executable", e)
	}
}
