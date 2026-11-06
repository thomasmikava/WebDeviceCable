package bridge

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
)

func groupedNativeFixture(t *testing.T) (*fixture, string, string, string) {
	t.Helper()
	f := setup(t)
	f.a.native = []NativeStatus{{ID: "codex", Execution: true, Images: true}, {ID: "claude", Execution: true}}
	origin := "https://grouped-native.example"
	token, pair := f.pair(t, "grouped-native", origin, "owner", false)
	grantFiles(t, f, token)
	lease := f.lease(t, origin, token)
	registerGroups(t, f, origin, token,
		ActionGroup{ID: "ai", Name: "AI", Kind: "ai"},
		ActionGroup{ID: "ai.chat", Name: "Chat", Kind: "ai", ParentID: "ai"},
		ActionGroup{ID: "ai.images", Name: "Images", Kind: "ai", ParentID: "ai", Operation: "image"},
	)
	routeApp(t, f, pair, Routing{Default: &ExecutionTarget{Provider: "claude"}, Groups: map[string]ExecutionTarget{"ai.images": {Provider: "codex"}}})
	return f, origin, token, lease
}

func TestGroupedNativeRequestsResolveOmittedProviders(t *testing.T) {
	for _, kind := range []string{"text", "image-generate", "image-edit"} {
		t.Run(kind, func(t *testing.T) {
			f, origin, token, lease := groupedNativeFixture(t)
			group, provider, feature := "ai.images", "codex", "imageGeneration"
			if kind == "text" {
				group, provider, feature = "ai.chat", "claude", "text"
			} else if kind == "image-edit" {
				feature = "imageEditing"
			}
			request := JobRequest{Kind: kind, Group: group, Prompt: "A yellow circle", IdempotencyKey: "grouped-native-" + kind}
			if kind == "image-edit" {
				var data bytes.Buffer
				if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
					t.Fatal(err)
				}
				id := secret()
				f.a.inputs[id] = &nativeInput{ID: id, Name: "reference.png", MediaType: "image/png", Size: data.Len(), Data: data.Bytes(), Hash: fileHash(data.Bytes()), PairingID: token, LeaseID: lease}
				request.Inputs = []string{id}
			}
			admission := object(t, f.req("POST", "/v2/admission", origin, token, lease, AdmissionRequest{Request: request, Features: []string{feature}}, false), 200)
			if admission["available"] != true || admission["kind"] != "native" {
				t.Fatal("group route was not admitted", admission)
			}
			if len(f.a.state.Jobs) != 0 || len(f.a.state.Conversations) != 0 || len(f.a.commands) != 0 || len(f.a.permissions) != 0 {
				t.Fatal("preflight created execution state")
			}
			if _, consumed, err := f.a.receipt(digest(token + ":" + request.IdempotencyKey)); err != nil || consumed {
				t.Fatal("preflight consumed the request key", err)
			}
			created := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 202)
			job := findJob(&f.a.state, created["id"].(string))
			if job.Plan.Provider != provider || job.Plan.Kind != kind || job.Request.Provider != "" || job.Status != "awaiting_approval" || job.Billing != "none" {
				t.Fatalf("incorrect route or premature execution: %+v", job)
			}
		})
	}
}

func TestGroupedNativeRequestRecoveryPrecedesCurrentRouting(t *testing.T) {
	f, origin, token, lease := groupedNativeFixture(t)
	request := JobRequest{Kind: "image-generate", Group: "ai.images", Prompt: "A yellow circle", IdempotencyKey: "grouped-image-recovery"}
	created := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 202)
	id := created["id"].(string)
	object(t, f.req("POST", "/v2/jobs/"+id+"/cancel", origin, token, lease, nil, false), 200)
	// The original image route becomes incompatible, but its receipt remains valid.
	routeApp(t, f, token, Routing{Default: &ExecutionTarget{Provider: "claude"}})
	recovered := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 200)
	if recovered["id"] != id || len(f.a.state.Jobs) != 1 || findJob(&f.a.state, id).Plan.Provider != "codex" {
		t.Fatal("changed routing replayed or replaced a consumed request", recovered)
	}
	changed := request
	changed.Prompt = "A different image"
	conflict := object(t, f.req("POST", "/v2/jobs", origin, token, lease, changed, false), 409)
	if conflict["error"].(map[string]any)["code"] != "idempotency_conflict" {
		t.Fatal("changed request did not conflict before route resolution", conflict)
	}
	f.a.state.Jobs = nil
	erased := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 410)
	if erased["error"].(map[string]any)["code"] != "history_deleted" || len(f.a.commands) != 0 {
		t.Fatal("erased history allowed a consumed image request to execute", erased)
	}
}

func TestGroupedNativeRequestsRetainValidationAndRouteGates(t *testing.T) {
	for _, tc := range []struct {
		name, group, provider, prompt string
		preflightStatus               int
		code                          string
	}{
		{"missing-provider-and-group", "", "", "A circle", 400, "invalid_job"},
		{"invalid-provider", "ai.images", "../codex", "A circle", 400, "invalid_job"},
		{"invalid-group", "../images", "", "A circle", 400, "invalid_job"},
		{"unknown-group", "ai.missing", "", "A circle", 200, "unknown_group"},
		{"incompatible-route", "ai.chat", "", "A circle", 200, "invalid_job"},
		{"blank-prompt", "ai.images", "", "  ", 400, "invalid_job"},
		{"oversized-prompt", "ai.images", "", strings.Repeat("x", 32769), 400, "invalid_job"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, origin, token, lease := groupedNativeFixture(t)
			request := JobRequest{Kind: "image-generate", Group: tc.group, Provider: tc.provider, Prompt: tc.prompt, IdempotencyKey: "invalid-grouped-" + tc.name}
			admission := object(t, f.req("POST", "/v2/admission", origin, token, lease, AdmissionRequest{Request: request}, false), tc.preflightStatus)
			code := admission["code"]
			if tc.preflightStatus != 200 {
				code = admission["error"].(map[string]any)["code"]
			}
			if code != tc.code || admission["available"] == true {
				t.Fatal("invalid preflight was admitted", admission)
			}
			rejected := object(t, f.req("POST", "/v2/jobs", origin, token, lease, request, false), 400)
			if rejected["error"].(map[string]any)["code"] != tc.code || len(f.a.state.Jobs) != 0 {
				t.Fatal("invalid request bypassed route validation", rejected)
			}
			if _, consumed, err := f.a.receipt(digest(token + ":" + request.IdempotencyKey)); err != nil || consumed {
				t.Fatal("rejected request consumed a key", err)
			}
		})
	}
}

func TestGroupedNativeAdmissionStillChecksSelectedProgramAvailability(t *testing.T) {
	f, origin, token, lease := groupedNativeFixture(t)
	request := JobRequest{Kind: "image-generate", Group: "ai.images", Prompt: "A circle", IdempotencyKey: "grouped-unavailable-image"}
	for i, tc := range []struct {
		disabled, images bool
		code             string
	}{
		{false, false, "native_image_unavailable"},
		{true, true, "native_disabled"},
	} {
		f.a.config.Bridge.DisableNative = tc.disabled
		f.a.native[0].Images = tc.images
		request.IdempotencyKey = fmt.Sprintf("grouped-unavailable-%d", i)
		admission := object(t, f.req("POST", "/v2/admission", origin, token, lease, AdmissionRequest{Request: request, Features: []string{"imageGeneration"}}, false), 200)
		if admission["available"] != false || admission["code"] != tc.code {
			t.Fatal("unsupported native image route was advertised", admission)
		}
	}
}
