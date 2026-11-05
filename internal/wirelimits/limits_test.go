package wirelimits

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLogicalBudgets(t *testing.T) {

	for _, path := range []string{"/v2/jobs", "/v2/conversations", "/v2/admission"} {
		body := `{"prompt":"` + strings.Repeat("<", 32000) + `"}`
		if path == "/v2/admission" {
			body = `{"request":` + body + `}`
		}
		if Check("POST", path, body) != "" {
			t.Fatal("HTML escaping changed the ordinary-field byte budget", path)
		}
	}
	for _, path := range []string{"/v2/jobs", "/v2/conversations", "/v2/admission"} {
		fields := map[string]any{"tools": []any{map[string]any{"description": strings.Repeat("🙂\x00\\\"", 6000)}}}
		var value any = fields
		if path == "/v2/admission" {
			value = map[string]any{"request": fields, "features": []string{"text"}}
		}
		b, _ := json.Marshal(value)
		if len(b) <= Ordinary || Check("POST", path, string(b)) != "" {
			t.Fatal(path, "large tools rejected")
		}
		fields["prompt"] = strings.Repeat("x", Ordinary+1)
		b, _ = json.Marshal(value)
		if Check("POST", path, string(b)) != "request_too_large" {
			t.Fatal(path, "non-tool fields bypassed ordinary budget")
		}
	}
	if Check("POST", "/v2/jobs/lookup", strings.Repeat(" ", Ordinary+1)) != "request_too_large" {
		t.Fatal("ordinary budget changed")
	}
	if Check("POST", "/v2/jobs", `{"tools":"`+strings.Repeat("x", Definitions)+`"}`) != "tool_set_too_large" {
		t.Fatal("definition budget not enforced")
	}
	if Check("POST", "/v2/jobs", strings.Repeat(" ", Creation+1)) != "request_too_large" {
		t.Fatal("creation budget not enforced")
	}
}
