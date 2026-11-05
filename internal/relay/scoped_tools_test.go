package relay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/wirelimits"
)

func TestScopedCapabilitiesRequireProjectAuthenticationAndBoundCreation(t *testing.T) {
	config := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := registryHarness{t, s}
	project := h.create(config.AdminKey, "scoped-project")
	for _, key := range []string{"", config.AdminKey, "invalid"} {
		expectStatus(t, h.request("GET", "/relay/v1/capabilities", key, nil, nil), 401)
	}
	response := h.request("GET", "/relay/v1/capabilities", project.APIKey, nil, nil)
	expectStatus(t, response, 200)
	var caps struct {
		Version int            `json:"scopedToolsVersion"`
		Limits  map[string]int `json:"limits"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Version != 1 || caps.Limits["toolDefinitionBytes"] != wirelimits.Definitions || caps.Limits["encryptedEnvelopeBytes"] != wirelimits.Envelope {
		t.Fatal("missing execution capabilities", response.Body.String())
	}
	path := "/relay/v1/connections/" + ID() + "/request"
	logical := logicalRequest{ID: ID(), Deadline: time.Now().Add(45 * time.Second).UnixMilli(), Method: "POST", Path: "/v2/jobs", Body: `{"tools":"` + strings.Repeat("x", wirelimits.Definitions) + `"}`}
	response = h.request("POST", path, project.APIKey, logical, nil)
	expectStatus(t, response, 413)
	if !strings.Contains(response.Body.String(), "tool_set_too_large") {
		t.Fatal(response.Body.String())
	}
	logical.Path = "/v2/jobs/lookup"
	logical.Body = strings.Repeat("x", wirelimits.Ordinary+1)
	response = h.request("POST", path, project.APIKey, logical, nil)
	expectStatus(t, response, 413)
	if !strings.Contains(response.Body.String(), "request_too_large") {
		t.Fatal(response.Body.String())
	}
	logical.Body = strings.Repeat("x", wirelimits.Envelope)
	response = h.request("POST", path, project.APIKey, logical, nil)
	expectStatus(t, response, 413)
	if !strings.Contains(response.Body.String(), "request_too_large") {
		t.Fatal(response.Body.String())
	}
	expectStatus(t, h.request("POST", path, "", logical, nil), 401)
}
