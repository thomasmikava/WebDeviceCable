package relay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPairingStatusIsAccountScopedAndDeclineConsumesCode(t *testing.T) {
	admin := strings.Repeat("k", 64)
	s, err := Open(Config{Directory: t.TempDir(), AdminKey: admin})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := registryHarness{t: t, s: s}
	project := h.create(admin, "pairing")
	other := h.create(admin, "other")
	ticket, code := h.enroll(project.APIKey)
	path := "/relay/v1/connections/" + ticket.ConnectionID
	response := h.request("GET", path, project.APIKey, nil, map[string]string{"X-WDC-Account": "same-account"})
	expectStatus(t, response, 200)
	var state map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &state)
	if state["enrollmentStatus"] != "pending" || state["connected"] != false {
		t.Fatal(state)
	}
	expectStatus(t, h.request("GET", path, other.APIKey, nil, nil), 404)
	expectStatus(t, h.request("GET", path, project.APIKey, nil, map[string]string{"X-WDC-Account": "different-account"}), 403)
	expectStatus(t, h.request("POST", "/connector/v1/decline", "", map[string]string{"code": code}, nil), 200)
	response = h.request("GET", path, project.APIKey, nil, nil)
	expectStatus(t, response, 200)
	_ = json.Unmarshal(response.Body.Bytes(), &state)
	if state["enrollmentStatus"] != "declined" {
		t.Fatal(state)
	}
	expectStatus(t, h.request("POST", "/connector/v1/inspect", "", map[string]string{"code": code}, nil), 410)
	expectStatus(t, h.claim(code, project.Project.AppID, project.Project.Origin, strings.Repeat("t", 64)), 410)
	expectStatus(t, h.request("DELETE", path, project.APIKey, nil, map[string]string{"X-WDC-Account": "same-account"}), 200)
	expectStatus(t, h.request("GET", path, project.APIKey, nil, nil), 404)
}
