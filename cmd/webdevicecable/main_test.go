package main

import (
	"encoding/json"
	"github.com/thomasmikava/WebDeviceCable/internal/bridge"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLauncherAuthenticatesRuntimeWithoutDisclosingKey(t *testing.T) {
	dir := t.TempDir()
	key := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	if err := os.WriteFile(filepath.Join(dir, "admin.key"), []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	var info bridge.RuntimeInfo
	spoof := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("disclosed control key")
		}
		var request struct{ Nonce string }
		json.NewDecoder(r.Body).Decode(&request)
		proof := bridge.RuntimeProof(key, request.Nonce, info)
		if spoof {
			proof = "wrong"
		}
		json.NewEncoder(w).Encode(map[string]any{"pid": info.PID, "proof": proof})
	}))
	defer server.Close()
	info = bridge.RuntimeInfo{URL: server.URL, PID: os.Getpid()}
	if !alive(dir, info) {
		t.Fatal("rejected authenticated peer")
	}
	spoof = true
	if alive(dir, info) {
		t.Fatal("trusted an unrelated listener")
	}
	if err := openPanel(dir, info, true); err == nil {
		t.Fatal("opened private link on imposter")
	}
}

func TestDefaultDataDirectoryPreservesExistingInstallation(t *testing.T) {
	root := t.TempDir()
	current, legacy := filepath.Join(root, "webdevicecable"), filepath.Join(root, "ai-bridge")
	if got := defaultDataDirectory(root); got != current {
		t.Fatalf("fresh installation: %q", got)
	}
	if e := os.Mkdir(legacy, 0700); e != nil {
		t.Fatal(e)
	}
	identity := filepath.Join(legacy, "identity.key")
	if e := os.WriteFile(identity, []byte("existing identity"), 0600); e != nil {
		t.Fatal(e)
	}
	if got := defaultDataDirectory(root); got != legacy {
		t.Fatalf("existing installation lost its data directory: %q", got)
	}
	if data, e := os.ReadFile(identity); e != nil || string(data) != "existing identity" {
		t.Fatal("existing identity changed")
	}
	if e := os.Mkdir(current, 0700); e != nil {
		t.Fatal(e)
	}
	if got := defaultDataDirectory(root); got != current {
		t.Fatalf("explicit current installation: %q", got)
	}
}

func TestDefaultDataDirectoryRejectsLegacyFile(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "ai-bridge"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if got := defaultDataDirectory(root); got != filepath.Join(root, "webdevicecable") {
		t.Fatalf("used a legacy file as the state directory: %q", got)
	}
}
