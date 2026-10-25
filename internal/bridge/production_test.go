package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRuntimeAddressValidation(t *testing.T) {
	for _, address := range []string{"http://127.0.0.1:123@evil.example", "http://127.0.0.1:123/path", "http://127.0.0.1:123?", "http://127.0.0.1:0", "http://localhost:123", "https://127.0.0.1:123", "http://127.0.0.1:123#key", "http://127.0.0.1:00123"} {
		if ValidateRuntime(RuntimeInfo{URL: address, PID: 1}) == nil {
			t.Fatalf("accepted %s", address)
		}
	}
	if ValidateRuntime(RuntimeInfo{URL: "http://127.0.0.1:123", PID: 1}) != nil {
		t.Fatal("rejected loopback")
	}
}

func TestLocalStorageRefusesLinksOversizedAndCorruptState(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err == nil {
		if _, err := ReadLocalFile(link, 100); err == nil {
			t.Fatal("read symlink")
		}
		if err := writeLocalFile(link, []byte("replace")); err == nil {
			t.Fatal("replaced symlink")
		}
		data, _ := os.ReadFile(target)
		if string(data) != "keep" {
			t.Fatal("changed target")
		}
	} else if runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	if _, err := ReadLocalFile(target, 3); err == nil {
		t.Fatal("read oversized file")
	}
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalFile(filepath.Join(dir, "directory"), nil); err == nil {
		t.Fatal("replaced directory")
	}
	state := []byte(`{"version":1,"pairings":{"bad":null},"invitations":{},"jobs":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), state, 0600); err != nil {
		t.Fatal(err)
	}
	if a, err := Open(dir); err == nil {
		a.Close()
		t.Fatal("accepted corrupt state")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(data) != string(state) {
		t.Fatal("overwrote corrupt state")
	}
}

func TestReceiptsMigrateAndSurviveRestartAndHistoryRollover(t *testing.T) {
	f := setup(t)
	id, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	lease := f.lease(t, "https://writer.example", token)
	p := f.a.state.Pairings[id]
	request := JobRequest{Kind: "connection-test", IdempotencyKey: "old-original-key"}
	first, _, err := f.a.createJob(p, request, lease)
	if err != nil {
		t.Fatal(err)
	}
	first.Status = "completed"
	for len(f.a.state.Jobs) < maxJobs {
		f.a.state.Jobs = append(f.a.state.Jobs, &Job{ID: secret(), Status: "completed", CreatedAt: f.now})
	}
	if _, _, err = f.a.createJob(p, JobRequest{Kind: "connection-test", IdempotencyKey: "new-request-key"}, lease); err != nil {
		t.Fatal(err)
	}
	if len(f.a.state.Jobs) != maxJobs {
		t.Fatal("history is not bounded")
	}
	if _, _, err = f.a.createJob(p, request, lease); err == nil || !strings.Contains(err.Error(), "consumed") {
		t.Fatalf("replayed removed history: %v", err)
	}
	legacyKey, legacyReceipt := digest("legacy-key"), Receipt{JobID: secret(), Hash: digest("legacy-hash")}
	f.a.state.Receipts = map[string]Receipt{legacyKey: legacyReceipt}
	if err = f.a.save(&f.a.state); err != nil {
		t.Fatal(err)
	}
	if err = f.a.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := Open(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	got, ok, err := other.receipt(legacyKey)
	if err != nil || !ok || got != legacyReceipt {
		t.Fatal("lost legacy receipt", err)
	}
	data, _ := os.ReadFile(filepath.Join(other.dir, "state.json"))
	var state map[string]any
	json.Unmarshal(data, &state)
	if _, ok := state["receipts"]; ok {
		t.Fatal("retained ledger in job state")
	}
	if err := os.WriteFile(filepath.Join(other.dir, "receipts", legacyKey[:2]+".json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = other.receipt(legacyKey); err == nil {
		t.Fatal("ignored corrupt receipt shard")
	}
}

func TestPrivatePanelLoginReusesExistingSession(t *testing.T) {
	f := setup(t)
	for i := 0; i < 40; i++ {
		object(t, f.req("POST", "/api/session", f.origin, f.a.adminKey, "", nil, true), 200)
	}
	if len(f.a.sessions) != 1 {
		t.Fatal("launcher exhausted control sessions")
	}
}

func TestReceiptReservationSurvivesFailedJobPersistence(t *testing.T) {
	f := setup(t)
	id, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	lease := f.lease(t, "https://writer.example", token)
	path := filepath.Join(f.a.dir, "state.json")
	backup := path + ".backup"
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	request := JobRequest{Kind: "connection-test", IdempotencyKey: "durable-reservation"}
	if _, _, err := f.a.createJob(f.a.state.Pairings[id], request, lease); err == nil {
		t.Fatal("accepted failed state save")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.a.createJob(f.a.state.Pairings[id], request, lease); err == nil || !strings.Contains(err.Error(), "consumed") {
		t.Fatalf("replayed reservation: %v", err)
	}
}

func TestRetiringChannelReleasesOnlyItsOwnSlot(t *testing.T) {
	f := setup(t)
	_, token := f.pair(t, "writer", "https://writer.example", "alice", false)
	channel, _, err := f.testChannel("https://writer.example", f.clients[token])
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.a.channels)
	request := secureRequest{Version: 2, Kind: "ai-bridge-request", SessionID: channel.id, Nonce: secret(), Timestamp: f.now.UnixMilli(), Origin: channel.origin, PairingID: token, PublicKey: publicKey(channel.peer.key), Method: "DELETE", Path: "/v2/channel/current"}
	envelope, err := testSeal(channel, request)
	if err != nil {
		t.Fatal(err)
	}
	wire := f.rawReq("POST", "/v2/request", channel.origin, "", "", envelope, false)
	result, err := testDecode(channel, request, wire)
	if err != nil {
		t.Fatal(err)
	}
	object(t, result, 200)
	if len(f.a.channels) != before-1 || f.a.channels[channel.id] != nil {
		t.Fatal("did not release channel slot")
	}
}
