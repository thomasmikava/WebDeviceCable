package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/thomasmikava/WebDeviceCable/internal/wirelimits"
)

func executionCatalog(count int, marker string) []AppTool {
	tools := []AppTool{}
	for i := 0; i < count; i++ {
		tools = append(tools, AppTool{ID: fmt.Sprintf("tool_%d", i), Title: "Search", Description: marker + strings.Repeat("catalog ", 200), InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)})
	}
	return tools
}

func TestOwnerCompletionNoticesSurviveHistoryDeletion(t *testing.T) {
	f := setup(t)
	origin := "https://scope.example"
	token, pair := f.pair(t, "scope", origin, "owner", true)
	lease := f.lease(t, origin, token)
	request := JobRequest{Kind: "text", Provider: "codex", Prompt: "fixture", IdempotencyKey: "unobserved-submission", Tools: executionCatalog(3, "fixture")}
	created := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 202)
	id := created["id"].(string)
	object(t, f.req("POST", "/v2/jobs/"+id+"/cancel", origin, token, lease, nil, false), 200)
	f.a.state.Jobs = nil // Ordinary history removal never deletes durable receipts.
	poll := func(ownerLease string) map[string]any {
		return object(t, f.req("POST", "/v2/tool-calls/poll", origin, token, ownerLease, map[string]any{"requests": []string{request.IdempotencyKey, "unknown-request"}}, false), 200)
	}
	result := poll(lease)
	notices := result["completed"].([]any)
	if len(notices) != 1 || notices[0].(map[string]any)["jobId"] != id {
		t.Fatal("lost unobserved completion", result)
	}
	otherLease := f.lease(t, origin, token)
	if len(poll(otherLease)["completed"].([]any)) != 0 {
		t.Fatal("other tab received completion")
	}
	if _, consumed, err := f.a.receipt(digest(pair + ":" + request.IdempotencyKey)); err != nil || !consumed {
		t.Fatal("receipt lost", err)
	}
}

func TestAuthenticatedSizeErrorsAndPreAdmissionLiveBudget(t *testing.T) {
	f := setup(t)
	origin := "https://scope.example"
	token, pair := f.pair(t, "scope", origin, "owner", true)
	lease := f.lease(t, origin, token)
	tooLarge := map[string]any{"kind": "text", "provider": "codex", "idempotencyKey": "oversized-catalog", "tools": []any{map[string]any{"description": strings.Repeat("x", wirelimits.Definitions)}}}
	response := object(t, f.req("POST", "/v2/jobs", origin, token, lease, tooLarge, false), 413)
	if response["error"].(map[string]any)["code"] != "tool_set_too_large" || response["error"].(map[string]any)["category"] != "validation" {
		t.Fatal(response)
	}
	object(t, f.req("POST", "/v2/jobs/lookup", origin, token, lease, map[string]string{"idempotencyKey": strings.Repeat("x", wirelimits.Ordinary)}, false), 413)
	tools := executionCatalog(32, "fixture")
	for i := range tools {
		tools[i].InputSchema = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false,"description":"` + strings.Repeat("x", 7000) + `"}`)
	}
	definitionBytes, _ := json.Marshal(tools)
	for liveDefinitionBytes(&f.a.state)+len(definitionBytes) <= wirelimits.LiveDefinitions {
		id := secret()
		f.a.state.Conversations[id] = &Conversation{ID: id, PairingID: pair, LeaseID: lease, Status: "open", Tools: tools}
	}
	request := JobRequest{Kind: "text", Provider: "codex", Prompt: "fixture", IdempotencyKey: "resource-rejected", Tools: tools}
	response = object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 429)
	if response["error"].(map[string]any)["code"] != "tool_definition_limit" {
		t.Fatal(response)
	}
	if _, consumed, err := f.a.receipt(digest(pair + ":" + request.IdempotencyKey)); err != nil || consumed {
		t.Fatal("resource rejection consumed inference key", err)
	}
}

func TestLegacyReceiptRecoveryWithoutResettingPairing(t *testing.T) {
	f := setup(t)
	origin := "https://scope.example"
	token, pair := f.pair(t, "scope", origin, "owner", true)
	lease := f.lease(t, origin, token)
	request := JobRequest{Kind: "text", Provider: "codex", Prompt: "legacy", IdempotencyKey: "legacy-0.1-request"}
	if err := validateRequest(&request); err != nil {
		t.Fatal(err)
	}
	id := secret()
	hash := requestHash(request)
	if err := f.a.reserveReceipt(digest(pair+":"+request.IdempotencyKey), Receipt{JobID: id, Hash: hash}); err != nil {
		t.Fatal(err)
	}
	f.a.state.Jobs = append(f.a.state.Jobs, &Job{ID: id, PairingID: pair, LeaseID: lease, Status: "completed", Request: request, RequestHash: hash})
	response := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 200)
	if response["id"] != id || len(f.a.state.Jobs) != 1 || f.a.state.Pairings[pair].Revoked {
		t.Fatal("legacy receipt or pairing changed")
	}
}

func TestExecutionCatalogAdmissionIdentityAndRedaction(t *testing.T) {
	f := setup(t)
	origin := "https://scope.example"
	token, _ := f.pair(t, "scope", origin, "owner", true)
	lease := f.lease(t, origin, token)
	request := JobRequest{Kind: "text", Provider: "codex", Prompt: "fixture", IdempotencyKey: "scoped-large-catalog", Tools: executionCatalog(30, "chat")}
	body, _ := json.Marshal(request)
	if len(body) <= 40<<10 {
		t.Fatal("fixture must exceed old limit")
	}
	first := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 202)
	id := first["id"].(string)
	stored := findJob(&f.a.state, id)
	if stored.Request.Mode != "app-tools" || len(stored.Request.Tools) != 30 || len(stored.Request.ToolSetHash) != 64 {
		t.Fatal("snapshot not admitted")
	}
	if _, ok := first["request"].(map[string]any)["tools"]; ok {
		t.Fatal("definitions leaked into job response")
	}
	second := request
	second.IdempotencyKey = "scoped-small-catalog"
	second.Tools = executionCatalog(3, "rules")
	other := object(t, f.req("POST", "/v2/jobs", origin, token, lease, second, false), 202)
	otherJob := findJob(&f.a.state, other["id"].(string))
	if otherJob.Request.ToolSetHash == stored.Request.ToolSetHash {
		t.Fatal("different catalogs share identity")
	}
	// Tool IDs overlap; native startup resolves the job's immutable snapshot.
	defs := f.a.nativeToolDefinitions(id)
	otherDefs := f.a.nativeToolDefinitions(otherJob.ID)
	if len(defs) != 30 || len(otherDefs) != 3 || !strings.HasPrefix(defs[0]["description"].(string), "chat") || !strings.HasPrefix(otherDefs[0]["description"].(string), "rules") {
		t.Fatal("native definitions crossed executions")
	}
	duplicate := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 200)
	if duplicate["id"] != id || len(f.a.state.Jobs) != 2 {
		t.Fatal("receipt replayed inference")
	}
	changed := request
	changed.Tools = executionCatalog(30, "changed")
	conflict := object(t, f.req("POST", "/v2/jobs", origin, token, lease, changed, false), 409)
	if conflict["error"].(map[string]any)["code"] != "idempotency_conflict" {
		t.Fatal(conflict)
	}
	object(t, f.req("POST", "/v2/jobs/"+id+"/cancel", origin, token, lease, nil, false), 200)
	stored = findJob(&f.a.state, id)
	otherJob = findJob(&f.a.state, otherJob.ID)
	if stored.Request.Tools != nil || stored.Request.ToolSetHash == "" || terminal(otherJob.Status) {
		t.Fatal("scoped cancellation or snapshot cleanup failed")
	}
	// Receipt recovery survives snapshot cleanup and route changes.
	delete(f.a.state.Programs, "codex")
	object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 200)
}

func TestToolSnapshotCanonicalizationAndSchemaLimits(t *testing.T) {
	tools := executionCatalog(2, "fixture")
	mode := ""
	snapshot, hash, err := snapshotTools(tools, &mode)
	if err != nil {
		t.Fatal(err)
	}
	tools[0].InputSchema[0] = ' '
	tools[0].Description = "changed"
	if snapshot[0].Description == "changed" || snapshot[0].InputSchema[0] != '{' {
		t.Fatal("snapshot aliases input")
	}
	reordered := []AppTool{snapshot[1], snapshot[0]}
	reordered[0].InputSchema = json.RawMessage(`{"additionalProperties":false,"required":["value"],"properties":{"value":{"type":"string"}},"type":"object"}`)
	_, same, err := snapshotTools(reordered, &mode)
	if err != nil || same != hash {
		t.Fatal("canonical hash depends on property or catalog order", err)
	}
	mode = "restricted"
	if _, _, err = snapshotTools(snapshot, &mode); err == nil {
		t.Fatal("restricted tools admitted")
	}
	mode = ""
	for _, count := range []int{33, 64, 300} {
		catalog, _, err := snapshotTools(executionCatalog(count, "fixture"), &mode)
		if err != nil || len(catalog) != count {
			t.Fatal("catalog rejected by count rather than bytes", count, err)
		}
	}
	if _, _, err = snapshotTools(executionCatalog(700, "fixture"), &mode); err == nil {
		t.Fatal("catalog byte budget was removed with the count cap")
	}
	bad := executionCatalog(1, "fixture")
	bad[0].InputSchema = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false,"description":"` + strings.Repeat("x", 8192) + `"}`)
	if _, _, err = snapshotTools(bad, &mode); err == nil {
		t.Fatal("oversized input schema admitted")
	}
}

func TestSchemaByteBudgetSurvivesCanonicalizationAndStateCloning(t *testing.T) {
	f := setup(t)
	_, pair := f.pair(t, "encoding", "https://encoding.example", "owner", true)
	tools := []AppTool{{ID: "html", Title: "HTML", InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false,"description":"` + strings.Repeat("<>&", 2000) + `"}`)}}
	job, _, err := f.a.createJob(f.a.state.Pairings[pair], JobRequest{Kind: "text", Provider: "codex", Prompt: "fixture", IdempotencyKey: "schema-encoding", Tools: tools}, "owner-lease")
	if err != nil {
		t.Fatal(err)
	}
	before := liveDefinitionBytes(&f.a.state)
	if err := f.a.update(func(*State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	stored := findJob(&f.a.state, job.ID)
	if len(stored.Request.Tools[0].InputSchema) > 8192 || liveDefinitionBytes(&f.a.state) != before {
		t.Fatal("state encoding inflated the admitted schema")
	}
	tools[0].InputSchema = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false,"description":"` + strings.Repeat("\u2028", 2000) + `"}`)
	mode := ""
	if _, _, err := snapshotTools(tools, &mode); err == nil {
		t.Fatal("canonical schema exceeded its byte budget")
	}
}
