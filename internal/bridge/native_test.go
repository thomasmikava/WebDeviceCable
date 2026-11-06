package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

type writeCloseBuffer struct{ bytes.Buffer }

func (w *writeCloseBuffer) Close() error { return nil }
func scriptedRPC(t *testing.T, results ...any) (*nativeRPC, *writeCloseBuffer) {
	t.Helper()
	out := &writeCloseBuffer{}
	done := make(chan struct{})
	close(done)
	rpc := &nativeRPC{input: out, packets: make(chan conversationPacket, 32), cancel: func() {}, stopped: done, readerStopped: done}
	for i, result := range results {
		data, _ := json.Marshal(map[string]any{"id": i + 1, "result": result})
		rpc.packets <- conversationPacket{Data: data}
	}
	return rpc, out
}
func TestCodexIsolationHandshake(t *testing.T) {
	for _, test := range []string{"safe", "api-key", "missing-profile", "additional-root", "managed-tools", "inherited-instructions", "managed-mcp"} {
		t.Run(test, func(t *testing.T) {
			dir := "/private/wdc/run"
			config := codexConfig(dir, false)
			effective := codexConfig(dir, false)
			effective["mcp_servers"] = map[string]any{"inherited": map[string]any{"enabled": true}}
			effective["plugins"] = map[string]any{"plugin": map[string]any{"enabled": true}}
			account := map[string]any{"account": map[string]any{"type": "chatgpt", "email": "owner@example.test"}}
			thread := map[string]any{"model": "native-model", "modelProvider": "openai", "cwd": dir, "approvalPolicy": "never", "instructionSources": []any{}, "sandbox": map[string]any{"networkAccess": false}, "activePermissionProfile": map[string]any{"id": "wdc"}, "thread": map[string]any{"id": "private-native-thread"}}
			switch test {
			case "api-key":
				account["account"] = map[string]any{"type": "apiKey"}
			case "missing-profile":
				delete(thread, "activePermissionProfile")
			case "additional-root":
				effective["permissions"].(map[string]any)["wdc"].(map[string]any)["filesystem"].(map[string]any)[":root"] = "read"
			case "managed-tools":
				effective["features"].(map[string]any)["shell_tool"] = true
			case "inherited-instructions":
				thread["instructionSources"] = []any{"/private/user/AGENTS.md"}
			}
			rpc, out := scriptedRPC(t, map[string]any{}, account, map[string]any{"config": effective}, map[string]any{"data": []any{map[string]any{"skills": []any{map[string]any{"path": "/private/skill/SKILL.md"}}}}}, thread)
			rpc.spawn = func(configuration map[string]any) (*nativeRPC, error) {
				isolated := codexConfig(dir, false)
				for _, key := range []string{"mcp_servers", "plugins", "apps", "skills"} {
					isolated[key] = configuration[key]
				}
				if test == "managed-mcp" {
					isolated["mcp_servers"] = map[string]any{"inherited": map[string]any{"enabled": true}}
				}
				fresh, _ := scriptedRPC(t, map[string]any{}, account, map[string]any{"config": isolated}, thread)
				fresh.input = out
				return fresh, nil
			}

			result, identity, err := prepareCodex(context.Background(), rpc, dir, config, ExecutionTarget{})
			if test != "safe" {
				if err == nil {
					t.Fatal("unsafe configuration accepted")
				}
				return
			}
			if err != nil || result.Model != "native-model" || identity == "" {
				t.Fatal(result, identity, err)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			var request struct {
				Method string `json:"method"`
				Params struct {
					Config map[string]any `json:"config"`
				} `json:"params"`
			}
			_ = json.Unmarshal([]byte(lines[len(lines)-1]), &request)
			if request.Method != "thread/start" || request.Params.Config["mcp_servers"].(map[string]any)["inherited"].(map[string]any)["enabled"] != false {
				t.Fatal("inherited MCP was not disabled")
			}
			if !strings.Contains(out.String(), `"enabled":false`) || strings.Contains(out.String(), "account/login") {
				t.Fatal("unsafe native setup")
			}
		})
	}
}
func TestNativeRPCBindingAndEarlyEvents(t *testing.T) {
	rpc, out := scriptedRPC(t, map[string]any{"ok": true})
	response := <-rpc.packets
	rpc.packets <- conversationPacket{Data: []byte(`{"method":"item/agentMessage/delta","params":{"delta":"early"}}`)}
	rpc.packets <- response
	if err := rpc.call(context.Background(), "turn/start", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	event, e := rpc.receive(context.Background())
	if e != nil || event.Method != "item/agentMessage/delta" {
		t.Fatal("early stream event lost", e)
	}
	rpc.packets <- conversationPacket{Data: []byte(`{"id":"approval","method":"item/commandExecution/requestApproval","params":{}}`)}
	if _, e = rpc.receive(context.Background()); e == nil || !strings.Contains(out.String(), "disabled") {
		t.Fatal("native approval request accepted")
	}
	rpc.packets <- conversationPacket{Err: io.EOF}
	if _, e = rpc.receive(context.Background()); e == nil {
		t.Fatal("missing native terminal accepted")
	}
}
func TestNativeJSONSchemaValidation(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"word":{"type":"string","enum":["saffron"]},"count":{"type":"integer"}},"required":["word","count"],"additionalProperties":false}`)
	if e := checkOutputSchema(schema); e != nil {
		t.Fatal(e)
	}
	if e := validateJSONOutput(schema, `{"word":"saffron","count":2}`); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{`{"word":"saffron","count":2} garbage`, `{"word":"other","count":2}`, `{"word":"saffron","count":2.5}`, `{"word":"saffron","count":2,"extra":true}`, `{}`} {
		if validateJSONOutput(schema, value) == nil {
			t.Fatal("invalid output accepted", value)
		}
	}
	numeric := json.RawMessage(`{"type":"integer","enum":[9007199254740993]}`)
	if validateJSONOutput(numeric, `9007199254740992`) == nil {
		t.Fatal("numeric precision lost")
	}
	if validateJSONOutput(numeric, `9007199254740993`) != nil {
		t.Fatal("exact integer rejected")
	}

	for _, raw := range []string{`{"$ref":"https://example.test/schema"}`, `{"type":"object","properties":{},"required":[]}`, `{"type":"array","items":true}`, `{"type":"string","pattern":".*"}`} {
		if checkOutputSchema(json.RawMessage(raw)) == nil {
			t.Fatal("unsupported schema accepted", raw)
		}
	}
}
func TestNativeFilesStayWithinPairingAndLease(t *testing.T) {
	f := setup(t)
	origin := "http://127.0.0.1:47322"
	token, pair := f.pair(t, "files", origin, "alice", false)
	other, _ := f.pair(t, "files", origin, "bob", false)
	grantFiles(t, f, token)
	lease := f.lease(t, origin, token)
	otherLease := f.lease(t, origin, other)
	image := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			image.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x * y), 255})
		}
	}
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, image)
	data := encoded.Bytes()
	in := object(t, f.req("POST", "/v2/inputs", origin, token, lease, map[string]any{"name": "example.png", "mediaType": "image/png", "size": len(data), "sha256": fileHash(data)}, false), 201)
	id := in["id"].(string)
	object(t, f.req("PUT", "/v2/inputs/"+id, origin, other, otherLease, map[string]any{"offset": 0, "data": "eA=="}, false), 404)
	object(t, f.req("PUT", "/v2/inputs/"+id, origin, token, lease, map[string]any{"offset": 1, "data": "eA=="}, false), 409)
	if _, e := f.a.selectedInputs(f.a.state.Pairings[pair], lease, []string{id}); e == nil {
		t.Fatal("incomplete input accepted")
	}
	for offset := 0; offset < len(data); offset += fileChunk {
		object(t, f.req("PUT", "/v2/inputs/"+id, origin, token, lease, map[string]any{"offset": offset, "data": base64.StdEncoding.EncodeToString(data[offset:min(offset+fileChunk, len(data))])}, false), 200)
	}
	if _, e := f.a.selectedInputs(f.a.state.Pairings[pair], otherLease, []string{id}); e == nil {
		t.Fatal("input crossed tabs")
	}
	if _, e := f.a.selectedInputs(f.a.state.Pairings[pair], lease, []string{id}); e != nil {
		t.Fatal(e)
	}
	job := &Job{ID: secret(), PairingID: pair, Status: "running"}
	f.a.state.Jobs = append(f.a.state.Jobs, job)
	artifact, e := f.a.saveNativeImage(job.ID, base64.StdEncoding.EncodeToString(data))
	if e != nil {
		t.Fatal(e)
	}
	job.Artifacts = []Artifact{artifact}
	ownerPath := "/api/jobs/" + job.ID + "/artifacts/" + artifact.ID
	object(t, f.req("GET", ownerPath, f.origin, "", "", nil, false), 401)
	ownerDownload := f.req("GET", ownerPath, f.origin, "", "", nil, true)
	if ownerDownload.Code != 200 || ownerDownload.Header().Get("Content-Type") != "image/png" || !bytes.Equal(ownerDownload.Body.Bytes(), data) {
		t.Fatal("authenticated owner download changed image bytes", ownerDownload.Code)
	}
	got := []byte{}
	for offset := 0; offset < len(data); offset += fileChunk {
		part := object(t, f.req("GET", "/v2/jobs/"+job.ID+"/artifacts/"+artifact.ID+"/"+strconv.Itoa(offset), origin, token, "", nil, false), 200)
		chunk, _ := base64.StdEncoding.DecodeString(part["data"].(string))
		got = append(got, chunk...)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("artifact chunks changed")
	}
	object(t, f.req("GET", "/v2/jobs/"+job.ID+"/artifacts/"+artifact.ID+"/0", origin, other, "", nil, false), 404)
	f.advance(t, 13*time.Second)
	if len(f.a.inputs) != 0 {
		t.Fatal("expired tab retained input files")
	}
}

func TestNativeAccountPinIsPrivateAndCannotSwitch(t *testing.T) {
	f := setup(t)
	origin := "http://127.0.0.1:47322"
	token, pair := f.pair(t, "native", origin, "alice", true)
	lease := f.lease(t, origin, token)
	id := f.job(t, origin, token, lease, "account-binding", map[string]any{"kind": "text", "provider": "codex", "prompt": "Test"})
	job := findJob(&f.a.state, id)
	job.Status = "running"
	if e := f.a.bindNativeAccount(id, "first-account-fingerprint"); e != nil {
		t.Fatal(e)
	}
	if f.a.state.Pairings[pair].NativeAccounts["codex"] == "first-account-fingerprint" {
		t.Fatal("fingerprint was not scoped to installation")
	}
	if e := f.a.bindNativeAccount(id, "first-account-fingerprint"); e != nil {
		t.Fatal(e)
	}
	if e := f.a.bindNativeAccount(id, "other-account"); e == nil {
		t.Fatal("native account switched without renewed consent")
	}
	if publicPairing(f.a.state.Pairings[pair]).NativeAccounts != nil {
		t.Fatal("private account binding exposed")
	}
}

func TestNativeAccountPinsStayIndependentAcrossProgramSwitches(t *testing.T) {
	f := setup(t)
	origin := "https://chat.example"
	_, pair := f.pair(t, "native", origin, "alice", true)
	// Group requests omit an explicit provider; the resolved plan is authoritative.
	job := &Job{ID: secret(), PairingID: pair, Status: "running", Request: JobRequest{Kind: "action"}, Plan: &ExecutionPlan{Kind: "text", ExecutionTarget: ExecutionTarget{Provider: "codex"}}}
	f.a.state.Jobs = append(f.a.state.Jobs, job)
	for _, provider := range []string{"codex", "claude", "codex", "claude"} {
		findJob(&f.a.state, job.ID).Plan.Provider = provider
		if err := f.a.bindNativeAccount(job.ID, provider+"-account"); err != nil {
			t.Fatalf("switch to %s rejected: %v", provider, err)
		}
	}
	if len(f.a.state.Pairings[pair].NativeAccounts) != 2 {
		t.Fatal("providers did not retain separate account pins")
	}
	for _, provider := range []string{"codex", "claude"} {
		findJob(&f.a.state, job.ID).Plan.Provider = provider
		if err := f.a.bindNativeAccount(job.ID, "changed-account"); err == nil {
			t.Fatalf("changed %s account accepted", provider)
		}
	}
}

func TestNativeFailureRespectsProviderPrivacy(t *testing.T) {
	f := setup(t)
	f.a.state.Sharing = &Sharing{}
	job := &Job{Status: "failed", Request: JobRequest{Kind: "action"}, Plan: &ExecutionPlan{Kind: "text", ExecutionTarget: ExecutionTarget{Provider: "codex"}}, Detail: "Codex failed", Error: failureFor(0, "native_execution_failed", "Codex failed"), Execution: &ExecutionMetadata{Kind: "native", Name: "Codex", Provider: "codex", Version: "1.0.0", Model: "native-model"}}
	remote := f.a.remoteJob(job)
	data, _ := json.Marshal(remote)
	if strings.Contains(strings.ToLower(string(data)), "codex") || remote.Execution != nil {
		t.Fatal("hidden native identity leaked", string(data))
	}
	if job.Error.Message != "Codex failed" {
		t.Fatal("privacy projection changed local history")
	}
}
