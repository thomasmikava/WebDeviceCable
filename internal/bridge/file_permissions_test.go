package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func grantFiles(t *testing.T, f *fixture, pairing string) {
	t.Helper()
	object(t, f.req("PUT", "/api/pairings/"+pairing+"/files", f.origin, "", "", map[string]bool{"allowed": true}, true), 200)
}
func uploadForTest(t *testing.T, f *fixture, origin, pairing, lease, path string, data []byte, media string) string {
	t.Helper()
	in := object(t, f.req("POST", path, origin, pairing, lease, map[string]any{"name": "reference", "mediaType": media, "size": len(data), "sha256": fileHash(data)}, false), 201)
	id := in["id"].(string)
	for offset := 0; offset < len(data); offset += fileChunk {
		object(t, f.req("PUT", "/v2/inputs/"+id, origin, pairing, lease, map[string]any{"offset": offset, "data": base64.StdEncoding.EncodeToString(data[offset:min(offset+fileChunk, len(data))])}, false), 200)
	}
	return id
}
func TestFilePermissionIndependentGrantAndTextContext(t *testing.T) {
	f := setup(t)
	origin := "https://files.example"
	pair, _ := f.pair(t, "Files", origin, "owner", true)
	lease := f.lease(t, origin, pair)
	if f.a.state.Pairings[pair].Files {
		t.Fatal("automatic approval silently granted files")
	}
	denied := object(t, f.req("POST", "/v2/inputs", origin, pair, lease, map[string]any{"name": "x", "size": 1, "sha256": fileHash([]byte{1})}, false), 403)
	if denied["error"].(map[string]any)["code"] != "files_not_granted" {
		t.Fatal(denied)
	}
	data := []byte(strings.Repeat("გ", 20000))
	id := uploadForTest(t, f, origin, pair, lease, "/v2/contexts", data, "text/plain")
	prompt, err := f.a.contextText(f.a.state.Pairings[pair], lease, []string{id})
	if err != nil || prompt != string(data) {
		t.Fatal("text context did not survive encrypted chunks", err)
	}
	if _, err = f.a.selectedInputs(f.a.state.Pairings[pair], lease, []string{id}); err == nil {
		t.Fatal("context bypassed file permission")
	}
	request := map[string]bool{"files": true}
	pending := object(t, f.req("POST", "/v2/permissions", origin, pair, lease, request, false), 202)
	repeat := object(t, f.req("POST", "/v2/permissions", origin, pair, lease, request, false), 202)
	if pending["id"] != repeat["id"] {
		t.Fatal("duplicate grant prompts")
	}
	other := f.lease(t, origin, pair)
	object(t, f.req("GET", "/v2/permissions/"+pending["id"].(string), origin, pair, other, nil, false), 404)
	object(t, f.req("POST", "/api/permissions/"+pending["id"].(string)+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	approved := object(t, f.req("GET", "/v2/permissions", origin, pair, lease, nil, false), 200)
	if approved["files"] != true || !f.a.state.Pairings[pair].Automatic {
		t.Fatal("file grant modified automatic policy", approved)
	}
	// A completed text context never becomes a file even after the grant changes.
	if _, err = f.a.selectedInputs(f.a.state.Pairings[pair], lease, []string{id}); err == nil {
		t.Fatal("context was staged as a file")
	}
	file := uploadForTest(t, f, origin, pair, lease, "/v2/inputs", []byte{0, 255, 1}, "application/octet-stream")
	if _, err = f.a.contextText(f.a.state.Pairings[pair], lease, []string{file}); err == nil {
		t.Fatal("binary file became prompt context")
	}
}
func TestTextContextValidationAndOwner(t *testing.T) {
	f := setup(t)
	origin := "https://context.example"
	pair, _ := f.pair(t, "Context", origin, "owner", false)
	lease := f.lease(t, origin, pair)
	for _, data := range [][]byte{{0}, {255}} {
		in := object(t, f.req("POST", "/v2/contexts", origin, pair, lease, map[string]any{"name": "Context", "mediaType": "text/plain", "size": len(data), "sha256": fileHash(data)}, false), 201)
		object(t, f.req("PUT", "/v2/inputs/"+in["id"].(string), origin, pair, lease, map[string]any{"offset": 0, "data": base64.StdEncoding.EncodeToString(data)}, false), 400)
	}
	for _, meta := range []map[string]any{{"name": "x", "mediaType": "image/png", "size": 1, "sha256": strings.Repeat("a", 64)}, {"name": "x", "mediaType": "text/plain", "size": 128<<10 + 1, "sha256": strings.Repeat("a", 64)}} {
		object(t, f.req("POST", "/v2/contexts", origin, pair, lease, meta, false), 400)
	}
	id := uploadForTest(t, f, origin, pair, lease, "/v2/contexts", []byte("saffron"), "text/plain")
	other := f.lease(t, origin, pair)
	if _, err := f.a.contextText(f.a.state.Pairings[pair], other, []string{id}); err == nil {
		t.Fatal("context crossed tabs")
	}
}
func TestFileRevocationEndsOnlyFileScopesAndPreservesReceipts(t *testing.T) {
	f, origin, pair, lease := groupedNativeFixture(t)
	image := uploadForTest(t, f, origin, pair, lease, "/v2/inputs", []byte{1}, "application/octet-stream")
	text := uploadForTest(t, f, origin, pair, lease, "/v2/contexts", []byte("context"), "text/plain")
	first := object(t, f.req("POST", "/v2/conversations", origin, pair, lease, map[string]string{"group": "ai.chat"}, false), 201)["id"].(string)
	second := object(t, f.req("POST", "/v2/conversations", origin, pair, lease, map[string]string{"group": "ai.chat"}, false), 201)["id"].(string)
	request := JobRequest{Kind: "action", Group: "ai.chat", ConversationID: first, Prompt: "Inspect", Inputs: []string{image}, IdempotencyKey: "file-scope-turn"}
	created := object(t, f.req("POST", "/v2/jobs", origin, pair, lease, request, false), 202)
	plain := object(t, f.req("POST", "/v2/conversations/"+second+"/turns", origin, pair, lease, map[string]any{"idempotencyKey": "text-scope-turn", "prompt": "Continue", "contexts": []string{text}}, false), 202)
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]bool{"automatic": true, "files": false}, true), 200)
	if f.a.state.Conversations[first].Status != "closed" || f.a.state.Conversations[second].Status != "open" {
		t.Fatal("revocation crossed scopes")
	}
	job := findJob(&f.a.state, created["id"].(string))
	plainJob := findJob(&f.a.state, plain["id"].(string))
	if job.Code != "files_revoked" || terminal(plainJob.Status) {
		t.Fatal("revocation stopped text work or retained file work")
	}
	if f.a.inputs[image] != nil || f.a.inputs[text] == nil {
		t.Fatal("revocation cleared the wrong upload class")
	}
	recovered := object(t, f.req("POST", "/v2/jobs", origin, pair, lease, request, false), 200)
	if recovered["id"] != job.ID {
		t.Fatal("revocation lost receipt recovery")
	}
	request.IdempotencyKey = "another-file-turn"
	request.ConversationID = ""
	object(t, f.req("POST", "/v2/jobs", origin, pair, lease, request, false), 403)
}
func TestFileToolResultsSupplyActualPixelsToBothAdapters(t *testing.T) {
	f, pair, lease, job := makeToolJob(t, "app-tools")
	grantFiles(t, f, pair)
	data, err := os.ReadFile("web/claude-icon.png")
	if err != nil {
		t.Fatal(err)
	}
	id := uploadForTest(t, f, "https://tools.example", pair, lease, "/v2/inputs", data, "image/png")
	raw, _ := json.Marshal(FileToolResult{Type: "tool-result", Result: json.RawMessage(`{"ok":true}`), Inputs: []string{id}})
	for _, codex := range []bool{false, true} {
		content, err := f.a.nativeToolContent(t.Context(), job, raw, codex)
		if err != nil || len(content) != 2 {
			t.Fatal("missing visual tool output", err)
		}
		image := content[1].(map[string]any)
		encoded, _ := image["data"].(string)
		if codex {
			if image["type"] != "inputImage" {
				t.Fatal(image)
			}
			encoded = strings.TrimPrefix(image["imageUrl"].(string), "data:image/png;base64,")
		} else if image["type"] != "image" {
			t.Fatal(image)
		}
		actual, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatal("adapter lost original pixels", err)
		}
	}
	f.a.state.Pairings[pair].Files = false
	if _, err := f.a.nativeToolContent(t.Context(), job, raw, true); err == nil {
		t.Fatal("revoked files reached a native callback")
	}
}

func TestPairingReviewFilesSuggestedButOwnerDecides(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchecked", true: "checked"}[allow], func(t *testing.T) {
			f := setup(t)
			id := secret()
			origin := "https://review-files.example"
			object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", map[string]any{"id": id, "appId": "Files", "name": "Files", "appUser": "owner", "origin": origin, "requestedPermissions": map[string]bool{"automaticApprovals": true, "files": true}}, true), 201)
			if len(f.a.state.Invitations) != 0 {
				t.Fatal("suggestions granted access")
			}
			object(t, f.req("POST", "/api/pairing-requests/"+id+"/decision", f.origin, "", "", map[string]bool{"approve": true, "automatic": false, "files": allow}, true), 200)
			request := f.a.pairingRequests[id]
			claim := object(t, f.req("POST", "/v2/pairings/claim", origin, request.Invitation, "", map[string]string{"appId": "Files", "appUser": "owner"}, false), 201)
			p := claim["pairing"].(map[string]any)
			if p["files"] != allow || p["automatic"] != false {
				t.Fatal("review did not honor owner choices", p)
			}
		})
	}
}

func TestFileToolResultClaimsAndScopeRevocation(t *testing.T) {
	f, pair, lease, job := makeToolJob(t, "app-tools")
	grantFiles(t, f, pair)
	origin := "https://tools.example"
	j := findJob(&f.a.state, job)
	conversation := secret()
	f.a.state.Conversations[conversation] = &Conversation{ID: conversation, PairingID: pair, LeaseID: lease, Status: "open"}
	j.Request.ConversationID = conversation
	data, err := os.ReadFile("web/claude-icon.png")
	if err != nil {
		t.Fatal(err)
	}
	id := uploadForTest(t, f, origin, pair, lease, "/v2/inputs", data, "image/png")
	op, err := f.a.newPermission(f.a.state.Pairings[pair], lease, job, "app-tool", "create_image", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	op.Status = "approved"
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/claim", origin, pair, lease, nil, false), 200)
	result := map[string]any{"result": FileToolResult{Type: "tool-result", Result: json.RawMessage(`{"ok":true}`), Inputs: []string{id}}}
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/result", origin, pair, lease, result, false), 200)
	if !findJob(&f.a.state, job).UsesFiles || !f.a.state.Conversations[conversation].UsesFiles {
		t.Fatal("file callback did not bind the native session")
	}
	delete(f.a.inputs, id)
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/result", origin, pair, lease, result, false), 200)
	object(t, f.req("POST", "/v2/tool-calls/"+op.ID+"/result", origin, pair, lease, map[string]any{"result": map[string]bool{"ok": false}}, false), 409)
	object(t, f.req("PUT", "/api/pairings/"+pair+"/files", f.origin, "", "", map[string]bool{"allowed": false}, true), 200)
	if f.a.state.Conversations[conversation].Status != "closed" || findJob(&f.a.state, job).Code != "files_revoked" {
		t.Fatal("cached native image context survived revocation")
	}
}

func TestTextContextReachesCustomPromptWithoutFileGrant(t *testing.T) {
	f := setup(t)
	program := customFixture(t, f, "Text context harness", "owner")
	origin := "https://context-harness.example"
	pair, _ := f.pair(t, "Text context", origin, "owner", true)
	lease := f.lease(t, origin, pair)
	grantProgram(t, f, pair, program)
	context := uploadForTest(t, f, origin, pair, lease, "/v2/contexts", []byte("The saved word is saffron."), "text/plain")
	created := object(t, f.req("POST", "/v2/jobs", origin, pair, lease, JobRequest{Kind: "text", Provider: program.ID, Prompt: "Continue", Contexts: []string{context}, IdempotencyKey: "custom-text-context"}, false), 202)
	job := waitClaudeJob(t, f, created["id"].(string), func(j *Job) bool { return terminal(j.Status) })
	if job.Status != "completed" || !strings.Contains(job.Output, "The saved word is saffron.") || job.UsesFiles || f.a.state.Pairings[pair].Files {
		t.Fatalf("prompt context required files or was omitted: %+v", job)
	}
}

func TestCodexToolImagesStayOnTheirOwningTurn(t *testing.T) {
	for _, accepted := range []string{"owning-turn", "other-turn"} {
		t.Run(accepted, func(t *testing.T) {
			rpc, wire := scriptedRPC(t, map[string]any{"turnId": accepted})
			content := []any{map[string]any{"type": "inputText", "text": "saved result"}, map[string]any{"type": "inputImage", "imageUrl": "data:image/png;base64,cGl4ZWxz"}}
			result, err := deliverCodexToolImages(t.Context(), rpc, "owning-thread", "owning-turn", "image_tool", content)
			if accepted != "owning-turn" {
				if err != nil || len(result) != 2 || result[0].(map[string]any)["text"] != "saved result" {
					t.Fatal("lost committed JSON after image delivery failure", result, err)
				}
				assertFileDeliveryWarning(t, result[1], "inputText")
				return
			}
			if err != nil || len(result) != 1 || result[0].(map[string]any)["text"] != "saved result" {
				t.Fatal("text tool result lost", err)
			}
			var request struct {
				Method string
				Params struct {
					ThreadID       string           `json:"threadId"`
					ExpectedTurnID string           `json:"expectedTurnId"`
					Input          []map[string]any `json:"input"`
				}
			}
			if json.Unmarshal(wire.Bytes(), &request) != nil || request.Method != "turn/steer" || request.Params.ThreadID != "owning-thread" || request.Params.ExpectedTurnID != "owning-turn" || len(request.Params.Input) != 2 || request.Params.Input[1]["url"] != "data:image/png;base64,cGl4ZWxz" {
				t.Fatal("image delivery changed bytes or scope")
			}
		})
	}
	rpc, wire := scriptedRPC(t, map[string]any{})
	text := []any{map[string]any{"type": "inputText", "text": "ordinary result"}}
	if _, err := deliverCodexToolImages(t.Context(), rpc, "thread", "turn", "text_tool", text); err != nil || wire.Len() != 0 {
		t.Fatal("ordinary tools steered the native session", err)
	}
}

func assertFileDeliveryWarning(t *testing.T, item any, textType string) {
	t.Helper()
	block := item.(map[string]any)
	var warning struct {
		FileDelivery struct {
			OK            bool
			Code, Message string
		}
	}
	if block["type"] != textType || json.Unmarshal([]byte(block["text"].(string)), &warning) != nil || warning.FileDelivery.OK || warning.FileDelivery.Code != "file_delivery_failed" || !strings.Contains(warning.FileDelivery.Message, "repeat the tool automatically") || !strings.Contains(warning.FileDelivery.Message, "Do not claim to have inspected") {
		t.Fatal("missing separate file-delivery warning", block)
	}
}

func TestFileToolDeliveryFailurePreservesAcceptedJSON(t *testing.T) {
	for _, codex := range []bool{false, true} {
		for _, media := range []string{"image/png", "text/plain", "application/pdf", "missing"} {
			t.Run(map[bool]string{false: "claude", true: "codex"}[codex]+"/"+media, func(t *testing.T) {
				f, pair, lease, job := makeToolJob(t, "app-tools")
				grantFiles(t, f, pair)
				id := secret()
				data := []byte{255, 0, 1}
				f.a.inputs[id] = &nativeInput{ID: id, Name: "created", MediaType: media, Size: len(data), Data: data, Hash: fileHash(data), PairingID: pair, LeaseID: lease}
				if _, err := f.a.selectedInputs(f.a.state.Pairings[pair], lease, []string{id}); err != nil {
					t.Fatal("fixture would be rejected before callback acceptance", err)
				}
				if media == "missing" {
					delete(f.a.inputs, id)
				}
				original := json.RawMessage(`{"createdId":"committed","url":"https://example.test/saved"}`)
				raw, _ := json.Marshal(FileToolResult{Type: "tool-result", Result: original, Inputs: []string{id}})
				content, err := f.a.nativeToolContent(t.Context(), job, raw, codex)
				if err != nil || len(content) != 2 || content[0].(map[string]any)["text"] != string(original) {
					t.Fatal("accepted mutation was relabeled as failed or lost its JSON", content, err)
				}
				textType := "text"
				if codex {
					textType = "inputText"
				}
				assertFileDeliveryWarning(t, content[1], textType)
			})
		}
	}
}

func TestCodexImageDeliveryRejectionKeepsToolResult(t *testing.T) {
	rpc, wire := scriptedRPC(t)
	rpc.packets <- conversationPacket{Data: []byte(`{"id":1,"error":{"code":-1,"message":"steering unavailable"}}`)}
	content := []any{map[string]any{"type": "inputText", "text": `{"createdId":"committed"}`}, map[string]any{"type": "inputImage", "imageUrl": "data:image/png;base64,eA=="}}
	result, err := deliverCodexToolImages(t.Context(), rpc, "thread", "turn", "create_image", content)
	if err != nil || len(result) != 2 || result[0].(map[string]any)["text"] != content[0].(map[string]any)["text"] {
		t.Fatal("image delivery failure lost the application outcome", result, err)
	}
	assertFileDeliveryWarning(t, result[1], "inputText")
	if strings.Count(wire.String(), `"turn/steer"`) != 1 {
		t.Fatal("delivery failure retried the operation", wire.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := deliverCodexToolImages(ctx, rpc, "thread", "turn", "create_image", content); err == nil || len(result) != 0 {
		t.Fatal("cancelled execution received a tool result", result, err)
	}
}

func TestLegacyPairingKeepsIdentityGrantsAndReceiptsWithoutFiles(t *testing.T) {
	dir := t.TempDir()
	app, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pair := secret()
	key := digest(pair + ":retained-request")
	err = app.update(func(s *State) error {
		s.Pairings[pair] = &Pairing{ID: pair, AppID: "Legacy", Name: "Legacy", Origin: "https://legacy.example", AppUser: "owner", Mode: "restricted", Automatic: true, CredentialHash: "retained-credential", ClientPublicKey: "retained-public-key", ProgramGrants: map[string]string{"harness": "retained-grant"}}
		s.Receipts = map[string]Receipt{key: {JobID: secret(), Hash: digest("retained-receipt")}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	device := app.state.DeviceID
	identity := app.state.IdentityKey
	if err = app.Close(); err != nil {
		t.Fatal(err)
	}
	// Model a real older state file: no permission flag existed in its pairing.
	path := dir + "/state.json"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte(`"files": false,`), nil)
	data = bytes.ReplaceAll(data, []byte(`"files":false,`), nil)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	p := reopened.state.Pairings[pair]
	if p == nil || p.Files || !p.Automatic || p.CredentialHash != "retained-credential" || p.ClientPublicKey != "retained-public-key" || p.ProgramGrants["harness"] != "retained-grant" || reopened.state.DeviceID != device || reopened.state.IdentityKey != identity {
		t.Fatal("loading legacy state changed identity or access")
	}
	receipt, consumed, err := reopened.receipt(key)
	if err != nil || !consumed || receipt.Hash != digest("retained-receipt") {
		t.Fatal("loading legacy state reset receipts", err)
	}
}
