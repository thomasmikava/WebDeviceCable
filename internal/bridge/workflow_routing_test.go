package bridge

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWorkflowOptionsInheritIndependentlyAndKeepLegacySemantics(t *testing.T) {
	f := setup(t)
	p := &Pairing{Groups: []ActionGroup{{ID: "grading", Kind: "ai"}, {ID: "grading.answers", Kind: "ai", ParentID: "grading"}}, Routing: Routing{
		Default: &ExecutionTarget{Provider: "codex", Model: "fast", Reasoning: "medium"},
		Groups:  map[string]ExecutionTarget{"grading": {InheritOptions: true, Reasoning: "high"}, "grading.answers": {InheritOptions: true, Model: "careful"}},
	}}
	check := func(provider, model, reasoning string) {
		t.Helper()
		plan, err := f.a.resolvePlan(p, JobRequest{Kind: "action", Group: "grading.answers"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Provider != provider || plan.Model != model || plan.Reasoning != reasoning {
			t.Fatalf("wrong effective route: %+v", plan)
		}
	}
	check("codex", "careful", "high")
	p.Routing.Groups["grading.answers"] = ExecutionTarget{InheritOptions: true, ModelDefault: true}
	check("codex", "", "high")
	p.Routing.Groups["grading.answers"] = ExecutionTarget{InheritOptions: true, Provider: "claude"}
	check("claude", "", "")
	p.Routing.Groups["grading.answers"] = ExecutionTarget{Provider: "codex"}
	check("codex", "", "") // Existing whole-target overrides must still reset options.
}

func TestWorkflowDeclarationMigratesOnlyExistingLocalChoicesOnce(t *testing.T) {
	f := setup(t)
	origin := "https://workflow.example"
	id, token := f.pair(t, "Workflow", origin, "owner", false)
	p := f.a.state.Pairings[id]
	p.Groups = []ActionGroup{{ID: "ai", Name: "AI", Kind: "ai"}, {ID: "ai.generation", Name: "Generation", Kind: "ai", ParentID: "ai"}}
	p.Routing = Routing{Default: &ExecutionTarget{Provider: "codex"}, Groups: map[string]ExecutionTarget{"ai": {Provider: "claude", Model: "opus", Reasoning: "high"}}}
	incoming := map[string]any{"groups": []ActionGroup{{ID: "ai", Kind: "ai", Name: "Legacy", Hidden: true}, {ID: "images", Kind: "ai", Name: "Images"}, {ID: "images.plan", Kind: "ai", Name: "Plan", ParentID: "images", Replaces: []string{"ai.generation"}}}}
	object(t, f.req("POST", "/v2/groups", origin, token, "", incoming, false), 200)
	p = f.a.state.Pairings[id]
	if got := routingTarget(p, "images.plan"); got.Provider != "claude" || got.Model != "opus" || got.Reasoning != "high" {
		t.Fatalf("lost original choice: %+v", got)
	}
	p.Routing.Groups["images.plan"] = ExecutionTarget{Provider: "codex", Model: "custom"}
	object(t, f.req("POST", "/v2/groups", origin, token, "", incoming, false), 200)
	if f.a.state.Pairings[id].Routing.Groups["images.plan"].Model != "custom" {
		t.Fatal("registration overwrote a local customization")
	}
	if !groupExists(f.a.state.Pairings[id], "ai.generation") {
		t.Fatal("legacy recovery route disappeared")
	}
}

func TestRelayLaunchValidationAndLocalConsentBoundary(t *testing.T) {
	code := strings.Repeat("a", 64)
	raw := "webdevicecable://relay-connect?" + url.Values{"relayUrl": {"https://relay.example"}, "code": {code}}.Encode()
	intent, err := ParseLaunchURI(raw)
	if err != nil || intent.Kind != "relay-connect" || intent.Code != code {
		t.Fatal(intent, err)
	}
	for _, uri := range []string{raw + "&extra=1", raw + "&code=" + code, strings.Replace(raw, "https%3A%2F%2Frelay.example", "http%3A%2F%2F192.168.1.1", 1), raw + "#fragment", "webdevicecable://relay-connect/path?code=" + code} {
		if _, err := ParseLaunchURI(uri); err == nil {
			t.Fatal("accepted invalid launch", uri)
		}
	}
	f := setup(t)
	register := func(origin, key string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]string{"uri": raw})
		r := httptest.NewRequest("POST", f.origin+"/api/launch-intents", bytes.NewReader(data))
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	if register(f.origin, f.a.adminKey).Code != 403 || register("", "wrong").Code != 403 {
		t.Fatal("browser could create installed launch intent")
	}
	if register("", f.a.adminKey).Code != 201 || register("", f.a.adminKey).Code != 201 {
		t.Fatal("safe reopening failed")
	}
	if len(f.a.pendingLaunchIntents()) != 1 || len(f.a.state.Pairings) != 0 || len(f.a.state.Jobs) != 0 {
		t.Fatal("launch approved access or inference")
	}
	object(t, f.req("POST", "/api/launch-intents/"+intent.ID+"/dismiss", f.origin, "", "", map[string]bool{}, false), 401)
	object(t, f.req("POST", "/api/launch-intents/"+intent.ID+"/dismiss", f.origin, "", "", map[string]bool{}, true), 200)
	if len(f.a.pendingLaunchIntents()) != 0 {
		t.Fatal("dismissed invitation remained pending")
	}
}
