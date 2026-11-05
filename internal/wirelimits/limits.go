// Package wirelimits defines shared, transport-independent admission budgets.
package wirelimits

import (
	"bytes"
	"encoding/json"
)

func logicalJSON(value any) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
}

const (
	Ordinary        = 40 << 10
	Creation        = 1088 << 10
	Definitions     = 1 << 20
	Envelope        = 4 << 20
	Poll            = 8 << 20
	LiveDefinitions = 8 << 20
	Tools           = 32
)

func CreationRequest(method, path string) bool {
	return method == "POST" && (path == "/v2/jobs" || path == "/v2/conversations" || path == "/v2/admission")
}

// Check retains the ordinary budget for all fields other than inline tools.
// Invalid JSON is left to the strict resource decoder.
func Check(method, path, body string) string {
	limit := Ordinary
	if CreationRequest(method, path) {
		limit = Creation
	}
	if len(body) > limit {
		return "request_too_large"
	}
	if limit == Ordinary {
		return ""
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &fields) != nil {
		return ""
	}
	owner := fields
	if path == "/v2/admission" {
		owner = map[string]json.RawMessage{}
		if json.Unmarshal(fields["request"], &owner) != nil {
			return ""
		}
	}
	if len(owner["tools"]) > Definitions {
		return "tool_set_too_large"
	}
	delete(owner, "tools")
	if path == "/v2/admission" {
		fields["request"] = logicalJSON(owner)
	}
	b := logicalJSON(fields)
	if len(b) > Ordinary {
		return "request_too_large"
	}
	return ""
}

func Capabilities() map[string]int {
	return map[string]int{"toolsPerExecution": Tools, "inputSchemaBytes": 8192, "inputSchemaDepth": 12, "toolDefinitionBytes": Definitions, "executionRequestBytes": Creation, "ordinaryRequestBytes": Ordinary, "encryptedEnvelopeBytes": Envelope, "liveDefinitionBytes": LiveDefinitions, "pollResponseBytes": Poll}
}
