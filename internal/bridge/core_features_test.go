package bridge

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFolderBrowserReportsLocationAndShareabilityWithoutGrantingAccess(t *testing.T) {
	f := setup(t)
	root := t.TempDir()
	for _, name := range []string{"Documents", "Project", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "note.txt")
	if err := os.WriteFile(file, []byte("not a folder"), 0600); err != nil {
		t.Fatal(err)
	}
	browse := func(path string) map[string]any {
		return object(t, f.req("POST", "/api/folders", f.origin, "", "", map[string]string{"path": path}, true), 200)
	}
	object(t, f.req("POST", "/api/folders", f.origin, "", "", map[string]string{"path": root}, false), 401)
	result := browse(root)
	canonical, _ := filepath.EvalSymlinks(root)
	if result["path"] != canonical || result["parent"] != filepath.Dir(canonical) || result["name"] != filepath.Base(canonical) || result["canShare"] != true || result["truncated"] != false {
		t.Fatal("wrong folder location or selection state", result)
	}
	folders := result["folders"].([]any)
	if len(folders) != 2 || folders[0] != "Documents" || folders[1] != "Project" {
		t.Fatal("folder browser included files or hidden folders", folders)
	}
	crumbs := result["breadcrumbs"].([]any)
	if len(crumbs) == 0 || crumbs[len(crumbs)-1].(map[string]any)["path"] != canonical {
		t.Fatal("breadcrumb does not identify the current folder")
	}
	if browse(f.a.dir)["canShare"] != false || browse("")["canShare"] != false {
		t.Fatal("private settings or the entire home folder could be selected")
	}
	object(t, f.req("POST", "/api/folders", f.origin, "", "", map[string]string{"path": file}, true), 400)
	object(t, f.req("POST", "/api/folders", f.origin, "", "", map[string]string{"path": filepath.Join(root, "missing")}, true), 400)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err == nil && browse(alias)["path"] != canonical {
		t.Fatal("alias did not resolve to its actual location")
	}
	if len(f.a.state.Pairings) != 0 || len(f.a.state.Jobs) != 0 {
		t.Fatal("browsing changed application access or admitted work")
	}
}

func TestFolderBrowserReportsItsBoundedListing(t *testing.T) {
	f := setup(t)
	root := t.TempDir()
	for i := range 501 {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("folder-%03d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	result := object(t, f.req("POST", "/api/folders", f.origin, "", "", map[string]string{"path": root}, true), 200)
	if result["truncated"] != true || len(result["folders"].([]any)) != 500 {
		t.Fatal("bounded listing was not reported")
	}
}

func TestWebsiteOriginsAndOutputLimits(t *testing.T) {
	for _, origin := range []string{"https://example.com", "https://example.com:8443", "http://localhost:3000", "http://127.0.0.2:65535", "http://[::1]:1234"} {
		if !exactOrigin(origin) {
			t.Fatal(origin)
		}
		if err := nativeEligibility(&Pairing{Origin: origin}, "codex"); err != nil {
			t.Fatal(err)
		}
	}
	for _, origin := range []string{"http://example.com", "http://192.168.1.1:3000", "https://example.com:99999", "http://localhost:0", "file:///tmp", "https://example.com/path", "https://example.com?key=x"} {
		if exactOrigin(origin) {
			t.Fatal(origin)
		}
	}
	run := &commandRun{}
	run.Write(bytes.Repeat([]byte("x"), 100<<10))
	out, truncated, _, _, _, _ := run.snapshot()
	if truncated || len(out) != 100<<10 {
		t.Fatal("long output truncated")
	}
	run.Write(bytes.Repeat([]byte("x"), 1<<20))
	out, truncated, _, _, _, _ = run.snapshot()
	if !truncated || len(out) != 1<<20 {
		t.Fatal("output limit not enforced")
	}
	cfg, err := parseConfig([]byte("bridge:\n  maxTextBytes: 2097152\nactions: []\n"))
	if err != nil || cfg.Bridge.MaxTextBytes != 2<<20 {
		t.Fatal(cfg, err)
	}
	action := helperAction(t, "session-default", "echo")
	action.Session = &ActionSession{Protocol: "jsonl"}
	if err := validateAction(&action); err != nil || action.Session.IdleTimeout != 0 {
		t.Fatal("default idle expiry", err)
	}
}
func TestFragmentedResponsesBindEveryByte(t *testing.T) {
	f := setup(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	peer := &testPeer{key: key, pin: publicKey(f.a.identity), deviceID: f.a.state.DeviceID}
	channel, _, err := f.testChannel(f.origin, peer)
	if err != nil {
		t.Fatal(err)
	}
	f.a.channels[channel.id].Fragments = true
	raw := strings.Repeat("x😀日本語\"\\\n", 20000)
	request := secureRequest{SessionID: channel.id, Nonce: secret(), Origin: channel.origin, Method: "GET", Path: "/v2/test"}
	requestRaw, _ := json.Marshal(request)
	w := httptest.NewRecorder()
	writer := secureWriter{target: w, header: http.Header{}, status: 200, app: f.a, channel: f.a.channels[channel.id], request: request, requestHash: digest(string(requestRaw))}
	if err := writer.emit("response", raw); err != nil {
		t.Fatal(err)
	}
	frames, err := testFrames(channel, request, w)
	if err != nil {
		t.Fatal(err)
	}
	var recovered []byte
	for _, frame := range frames {
		if frame.Kind == "fragment" {
			part, err := b64.DecodeString(frame.Body)
			if err != nil || len(part) > 16<<10 {
				t.Fatal(err)
			}
			recovered = append(recovered, part...)
		} else {
			if frame.Kind != "response" || frame.Body != "" {
				t.Fatal(frame)
			}
		}
	}
	if string(recovered) != raw {
		t.Fatal("fragment boundaries changed bytes")
	}
	if len(frames) < 3 {
		t.Fatal("response was not bounded")
	}
	lines := strings.Split(w.Body.String(), "\n\n")
	bad := httptest.NewRecorder()
	bad.Header().Set("Content-Type", "text/event-stream")
	bad.Body.WriteString(strings.Join(append(lines[:1], lines[2:]...), "\n\n"))
	if _, err := testFrames(channel, request, bad); err == nil {
		t.Fatal("missing fragment accepted")
	}
}
func makeToolJob(t *testing.T, mode string) (*fixture, string, string, string) {
	t.Helper()
	f := setup(t)
	token, id := f.pair(t, "tools", "https://tools.example", "alice", true)
	lease := f.lease(t, "https://tools.example", token)
	f.a.state.Jobs = append(f.a.state.Jobs, &Job{ID: secret(), PairingID: id, LeaseID: lease, Status: "running", Request: JobRequest{Mode: mode, TimeoutSeconds: 120}, CreatedAt: f.now})
	return f, token, lease, f.a.state.Jobs[len(f.a.state.Jobs)-1].ID
}
func pendingOperation(t *testing.T, f *fixture) *PermissionRequest {
	t.Helper()
	for until := time.Now().Add(time.Second); time.Now().Before(until); {
		f.a.mu.Lock()
		for _, op := range f.a.permissions {
			if op.Status == "pending" {
				copy := *op
				f.a.mu.Unlock()
				return &copy
			}
		}
		f.a.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no pending permission")
	return nil
}
func TestAppToolDispatchClaimResultsAndImmutableDefinitions(t *testing.T) {
	f, token, lease, job := makeToolJob(t, "app-tools")
	origin := "https://tools.example"
	tool := AppTool{ID: "tasks.create", Title: "Create task", Description: "Create a task", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`)}
	tools, hash, err := snapshotTools([]AppTool{tool}, &f.a.state.Jobs[0].Request.Mode)
	if err != nil {
		t.Fatal(err)
	}
	f.a.state.Jobs[0].Request.Tools = tools
	f.a.state.Jobs[0].Request.ToolSetHash = hash
	f.a.state.Jobs[0].Request.IdempotencyKey = "scope-original"
	done := make(chan error, 1)
	go func() {
		_, err := f.a.invokeNativeTool(context.Background(), job, "app_"+digest(tool.ID)[:24], json.RawMessage(`{"title":"A"}`))
		done <- err
	}()
	var op *PermissionRequest
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && op == nil; {
		f.a.mu.Lock()
		for _, call := range f.a.permissions {
			if call.Kind == "app-tool" && call.Status == "approved" {
				copy := *call
				op = &copy
				break
			}
		}
		f.a.mu.Unlock()
		if op == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if op == nil {
		t.Fatal("registered callback did not dispatch without a WDC grant")
	}
	if len(f.a.permissionList()) != 0 {
		t.Fatal("app callback appeared as a permission prompt")
	}
	object(t, f.req("POST", "/api/permissions/"+op.ID+"/decision", f.origin, "", "", map[string]any{"approve": true}, true), 409)
	calls := object(t, f.req("GET", "/v2/tool-calls", origin, token, lease, nil, false), 200)
	if len(calls["calls"].([]any)) != 1 {
		t.Fatal(calls)
	}
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/claim", origin, token, lease, nil, false), 200)
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/claim", origin, token, lease, nil, false), 409)
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/result", origin, token, lease, map[string]any{"result": map[string]any{"ok": true}}, false), 200)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/result", origin, token, lease, map[string]any{"result": map[string]any{"ok": true}}, false), 200)
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/result", origin, token, lease, map[string]any{"result": false}, false), 409)
	other, _ := f.pair(t, "other", origin, "bob", true)
	otherLease := f.lease(t, origin, other)
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/claim", origin, other, otherLease, nil, false), 404)
	tool.Description = "Changed behavior"
	object(t, f.req("POST", "/v2/tools", origin, token, lease, map[string]any{"tools": []AppTool{tool}}, false), 404)
	if f.a.state.Jobs[0].Status != "running" || f.a.state.Jobs[0].Request.Tools[0].Description == tool.Description {
		t.Fatal("snapshot changed")
	}
	if op.JobID != job || op.RequestIdempotencyKey != "scope-original" || op.ToolSetHash != hash {
		t.Fatal("callback lost execution identity")
	}

}
func TestWorkspacePreviewStaleWritesAndContainment(t *testing.T) {
	f, token, lease, job := makeToolJob(t, "workspace")
	folder := t.TempDir()
	path := filepath.Join(folder, "note.txt")
	os.WriteFile(path, []byte("original"), 0600)
	object(t, f.req("PUT", "/api/pairings/"+token+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: folder, Write: true}}}, true), 200)
	f.a.state.Jobs[0].Status = "running"
	grant := f.a.state.Pairings[token].Folders[0]
	invoke := func(name, path string, text *string) (json.RawMessage, error) {
		args, _ := json.Marshal(map[string]any{"folder": grant.ID, "path": path, "text": text})
		return f.a.invokeNativeTool(context.Background(), job, name, args)
	}
	if _, err := invoke("wdc_read", "../outside", nil); err == nil {
		t.Fatal("escaped folder")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(folder, "link")); err == nil {
		if _, err := invoke("wdc_list", "link", nil); err == nil {
			t.Fatal("escaped via symlink")
		}
	}
	if raw, err := invoke("wdc_read", "note.txt", nil); err != nil || !strings.Contains(string(raw), "original") {
		t.Fatal(string(raw), err)
	}
	text := "edited"
	done := make(chan error, 1)
	go func() { _, err := invoke("wdc_write", "note.txt", &text); done <- err }()
	op := pendingOperation(t, f)
	if op.Before != "original" || op.After != text {
		t.Fatal("missing write preview")
	}
	os.WriteFile(path, []byte("changed concurrently"), 0600)
	object(t, f.req("POST", "/api/permissions/"+op.ID+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	if err := <-done; err == nil {
		t.Fatal("stale approval overwrote a newer file")
	}
	f.a.permissions = map[string]*PermissionRequest{}
	go func() { _, err := invoke("wdc_write", "note.txt", &text); done <- err }()
	op = pendingOperation(t, f)
	object(t, f.req("POST", "/api/permissions/"+op.ID+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != text {
		t.Fatal("edit missing")
	}
	// The website sees grant IDs and labels, never local absolute paths.
	summary := f.req("GET", "/v2/permissions", "https://tools.example", token, lease, nil, false)
	if strings.Contains(summary.Body.String(), folder) {
		t.Fatal("private folder path leaked")
	}
	object(t, f.req("PUT", "/api/pairings/"+token+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{}}, true), 200)
	if _, err := invoke("wdc_read", "note.txt", nil); err == nil {
		t.Fatal("revoked work continued")
	}
}
func TestWorkspaceEditRechecksLeaseAndCancellation(t *testing.T) {
	for _, ended := range []string{"expired", "cancelled"} {
		t.Run(ended, func(t *testing.T) {
			f, token, lease, job := makeToolJob(t, "workspace")
			folder := t.TempDir()
			path := filepath.Join(folder, "note.txt")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			object(t, f.req("PUT", "/api/pairings/"+token+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: folder, Write: true, Automatic: true}}}, true), 200)
			f.a.state.Jobs[0].Status = "running"
			grant := f.a.state.Pairings[token].Folders[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if ended == "expired" {
				l := f.a.leases[lease]
				l.ExpiresAt = f.now.Add(-time.Second)
				f.a.leases[lease] = l
			} else {
				cancel()
			}
			args, _ := json.Marshal(map[string]any{"folder": grant.ID, "path": "note.txt", "text": "must not be applied"})
			// The dispatcher checks admission too. Check the final apply boundary independently.
			if _, err := f.a.workspaceTool(ctx, job, "wdc_write", args); err == nil {
				t.Fatal("ended request applied an edit")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "original" {
				t.Fatal(string(data), err)
			}
		})
	}
}

func TestOpaqueFilesAndUnsafeOutputs(t *testing.T) {
	f, token, lease, job := makeToolJob(t, "restricted")
	grantFiles(t, f, token)
	data := []byte{0, 1, 2, 255, 10}
	meta := map[string]any{"name": "sample.anything", "mediaType": "application/octet-stream", "size": len(data), "sha256": fileHash(data)}
	origin := "https://tools.example"
	input := object(t, f.req("POST", "/v2/inputs", origin, token, lease, meta, false), 201)["id"].(string)
	object(t, f.req("PUT", "/v2/inputs/"+input, origin, token, lease, map[string]any{"offset": 0, "data": base64.StdEncoding.EncodeToString(data)}, false), 200)
	inputs, err := f.a.selectedInputs(f.a.state.Pairings[token], lease, []string{input})
	if err != nil || !bytes.Equal(inputs[0].Data, data) {
		t.Fatal("opaque upload changed", err)
	}
	stage, err := stageCommandFiles(t.TempDir(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	staged, _ := os.ReadFile(stage.Inputs[0].Path)
	if !bytes.Equal(staged, data) {
		t.Fatal("staging changed bytes")
	}
	os.WriteFile(filepath.Join(stage.OutputDirectory, "result.bin"), data, 0600)
	artifacts, err := f.a.collectCommandFiles(job, stage.OutputDirectory)
	if err != nil || len(artifacts) != 1 || artifacts[0].Hash != fileHash(data) {
		t.Fatal(artifacts, err)
	}
	os.Remove(filepath.Join(stage.OutputDirectory, "result.bin"))
	if err := os.Symlink(filepath.Join(f.a.dir, "admin.key"), filepath.Join(stage.OutputDirectory, "secret")); err == nil {
		if _, err = f.a.collectCommandFiles(job, stage.OutputDirectory); err == nil {
			t.Fatal("exported symlink target")
		}
	}
}
func TestLaunchRequiresInstalledIntentSignatureAndConsent(t *testing.T) {
	f := setup(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	requestID := secret()
	origin := "https://website.example:8443"
	uri := "webdevicecable://connect?" + url.Values{"requestId": {requestID}, "origin": {origin}, "publicKey": {publicKey(key)}}.Encode()
	object(t, f.rawReq("POST", "/api/launch-intents", f.origin, "", "", map[string]string{"uri": uri}, true), 403)
	object(t, f.rawReq("POST", "/api/launch-intents", "", f.a.adminKey, "", map[string]string{"uri": uri}, false), 201)
	object(t, f.rawReq("POST", "/api/launch-intents", "", f.a.adminKey, "", map[string]string{"uri": uri}, false), 409)
	ephemeral, _ := ecdh.P256().GenerateKey(rand.Reader)
	nonce := secret()
	payload, _ := json.Marshal(map[string]any{"requestId": requestID, "origin": origin, "publicKey": publicKey(key), "nonce": nonce, "timestamp": f.now.UnixMilli(), "appId": "website", "name": "Website", "appUser": "alice", "logoURL": "https://website.example:8443/logo.svg", "requestedPermissions": map[string]bool{"automaticApprovals": true}, "ephemeralKey": b64.EncodeToString(ephemeral.PublicKey().Bytes())})
	signed, _ := signBytes(key, payload)
	response := f.rawReq("POST", "/v2/connect", origin, "", "", signed, false)
	object(t, response, 200)
	if len(f.a.state.Pairings) != 0 || len(f.a.state.Invitations) != 0 {
		t.Fatal("launch granted access without consent")
	}
	object(t, f.rawReq("POST", "/v2/connect", origin, "", "", signed, false), 409)
	if f.a.pairingRequests[requestID].LogoURL != "https://website.example:8443/logo.svg" || !f.a.pairingRequests[requestID].RequestedPermissions.AutomaticApprovals {
		t.Fatal("signed onboarding suggestions lost")
	}
	if f.a.pairingRequests[requestID].Origin != origin || f.a.pairingRequests[requestID].PublicKey != publicKey(key) {
		t.Fatal("launch identity lost")
	}
	object(t, f.req("POST", "/api/pairing-requests/"+requestID+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	for _, inv := range f.a.state.Invitations {
		if !inv.Pairing.Automatic {
			t.Fatal("reviewed automatic approval was not granted")
		}
		if inv.Pairing.ClientPublicKey != publicKey(key) {
			t.Fatal("invitation not bound to requesting key")
		}
	}
	if _, err := ParseLaunchURI(uri + "&command=sh"); err == nil {
		t.Fatal("launch accepted executable parameters")
	}
	if _, err := ParseLaunchURI(strings.Replace(uri, "https%3A%2F%2Fwebsite.example%3A8443", "http%3A%2F%2Fwebsite.example", 1)); err == nil {
		t.Fatal("insecure origin accepted")
	}
}

func TestFileEnabledCommandRoundTrip(t *testing.T) {
	action := helperAction(t, "copy-file", "file-roundtrip")
	action.Files = true
	f := commandFixture(t, false, action)
	token, pair := f.pair(t, "file-app", f.origin, "owner", true)
	grantFiles(t, f, token)
	grantCommands(t, f, pair, action.ID)
	lease := f.lease(t, f.origin, token)
	data := []byte{0, 1, 255, 3, 5, 7, 0}
	in := object(t, f.req("POST", "/v2/inputs", f.origin, token, lease, map[string]any{"name": "file.any", "mediaType": "application/octet-stream", "size": len(data), "sha256": fileHash(data)}, false), 201)["id"].(string)
	object(t, f.req("PUT", "/v2/inputs/"+in, f.origin, token, lease, map[string]any{"offset": 0, "data": base64.StdEncoding.EncodeToString(data)}, false), 200)
	job := f.job(t, f.origin, token, lease, "file-roundtrip-key", map[string]any{"kind": "command", "action": action.ID, "inputs": []string{in}})
	result := waitJob(t, f, job, func(j *Job) bool { return terminal(j.Status) })
	if result.Status != "completed" || len(result.Artifacts) != 1 || result.Artifacts[0].Hash != fileHash(data) {
		t.Fatalf("file roundtrip: %+v", result)
	}
	part := object(t, f.req("GET", "/v2/jobs/"+job+"/artifacts/"+result.Artifacts[0].ID+"/0", f.origin, token, lease, nil, false), 200)
	raw, _ := base64.StdEncoding.DecodeString(part["data"].(string))
	if !bytes.Equal(raw, data) {
		t.Fatal("download changed bytes")
	}
	if _, err := os.Stat(filepath.Join(f.a.dir, "runs", job)); !os.IsNotExist(err) {
		t.Fatal("staged files survived process completion")
	}
}
func TestClaudeToolProfilePreservesExplicitIsolation(t *testing.T) {
	args := claudeToolArgs(claudeArgs(ExecutionTarget{}, nativeUUID()), "private-mcp.json")
	text := strings.Join(args, " ")
	for _, control := range []string{"--restricted", "--strict-mcp-config", "--tools  ", "--setting-sources  ", "--no-session-persistence", "--disable-slash-commands", "--no-chrome", "claudeMdExcludes", "syncClaudeAiSkills", "syncClaudeAiPlugins", "disableAllHooks", "--allowedTools mcp__wdc__*"} {
		if !strings.Contains(text, control) {
			t.Fatal("missing tool isolation", control)
		}
	}
	if strings.Contains(text, "--safe-mode") || strings.Contains(text, "bypassPermissions") || strings.Contains(text, "--dangerously") {
		t.Fatal("invalid tool profile")
	}
}

func TestOnlyFoldersNeedPermissionReviewAndRespectTab(t *testing.T) {
	f, token, lease, _ := makeToolJob(t, "restricted")
	origin := "https://tools.example"
	object(t, f.req("POST", "/v2/permissions", origin, token, lease, map[string]any{"tools": []string{"unknown"}}, false), 400)
	request := object(t, f.req("POST", "/v2/permissions", origin, token, lease, map[string]any{"workspace": true}, false), 202)
	id := request["id"].(string)
	otherLease := f.lease(t, origin, token)
	object(t, f.req("GET", "/v2/permissions/"+id, origin, token, otherLease, nil, false), 404)
	object(t, f.req("POST", "/api/permissions/"+id+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 400)
	object(t, f.req("PUT", "/api/pairings/"+token+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: t.TempDir()}}}, true), 200)
	status := object(t, f.req("GET", "/v2/permissions/"+id, origin, token, lease, nil, false), 200)
	if status["status"] != "completed" {
		t.Fatal(status)
	}
	if len(f.a.nativeToolDefinitions(f.a.state.Jobs[0].ID)) != 0 {
		t.Fatal("Restricted mode exposed app callbacks")
	}
}
func TestFolderPermissionEditsDoNotCancelAppOnlyWork(t *testing.T) {
	f, token, _, _ := makeToolJob(t, "app-tools")
	object(t, f.req("PUT", "/api/pairings/"+token+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: t.TempDir()}}}, true), 200)
	if f.a.state.Jobs[0].Status != "running" {
		t.Fatal("folder edit cancelled app-only work")
	}
}

func TestPrivateMCPMetadataCannotWidenAccess(t *testing.T) {
	f, token, _, job := makeToolJob(t, "workspace")
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, "note"), []byte("scoped"), 0600)
	object(t, f.req("PUT", "/api/pairings/"+token+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: folder}}}, true), 200)
	f.a.state.Jobs[0].Status = "running"
	grant := f.a.state.Pairings[token].Folders[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conversation := &commandConversation{active: &commandRun{ctx: ctx, jobID: job}}
	config, stop, err := f.a.startToolAdapter(ctx, conversation, f.a.nativeToolDefinitions(job))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	entry := config["mcpServers"].(map[string]any)["wdc"].(map[string]any)
	env := entry["env"].(map[string]string)
	send := func(origin, key string) *http.Response {
		raw, _ := json.Marshal(map[string]any{"id": 7, "method": "tools/call", "params": map[string]any{"name": "wdc_read", "arguments": map[string]string{"folder": grant.ID, "path": "note"}, "_meta": map[string]any{"progressToken": 1}}})
		request, _ := http.NewRequest("POST", env["WDC_HELPER_ENDPOINT"], bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+key)
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	denied := send("https://tools.example", env["WDC_HELPER_TOKEN"])
	denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatal("browser reached private adapter")
	}
	denied = send("", "wrong")
	denied.Body.Close()
	if denied.StatusCode != 403 {
		t.Fatal("missing capability reached adapter")
	}
	response := send("", env["WDC_HELPER_TOKEN"])
	defer response.Body.Close()
	var result map[string]any
	json.NewDecoder(response.Body).Decode(&result)
	if response.StatusCode != 200 || result["isError"] != false {
		t.Fatal(response.Status, result)
	}
	replayed := send("", env["WDC_HELPER_TOKEN"])
	replayed.Body.Close()
	if replayed.StatusCode != 409 {
		t.Fatal("repeated native call was dispatched again")
	}
}
