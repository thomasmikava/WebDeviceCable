package bridge

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/thomasmikava/WebDeviceCable/internal/wirelimits"
)

// State is JSON data, never an HTML/script fragment. Avoid expanding schema
// strings during state cloning, which would change their admitted byte budget.
func jsonData(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// Normalize definitions before hashing. Schema object order and caller order do
// not change execution identity. No caller-supplied hash is trusted.
func snapshotTools(tools []AppTool, mode *string) ([]AppTool, string, error) {
	if len(tools) > 0 {
		if *mode == "" {
			*mode = "app-tools"
		}
		if *mode != "app-tools" && *mode != "workspace" {
			return nil, "", fail(400, "invalid_mode", "Tools require app-tools or workspace mode.")
		}
	}
	next := make([]AppTool, 0, len(tools))
	seen := map[string]bool{}
	for _, tool := range tools {
		if !actionID.MatchString(tool.ID) || strings.HasPrefix(tool.ID, "wdc_") || seen[tool.ID] || len(tool.Title) < 1 || len(tool.Title) > 100 || len(tool.Description) > 2000 {
			return nil, "", fail(400, "invalid_tools", "Invalid or duplicate app tool definition.")
		}
		if len(tool.InputSchema) == 0 {
			return nil, "", fail(400, "invalid_tools", "An input schema is required.")
		}
		if err := checkToolSchema(tool.InputSchema); err != nil {
			return nil, "", fail(400, "invalid_tools", err.Error())
		}
		var schema any
		decoder := json.NewDecoder(bytes.NewReader(tool.InputSchema))
		decoder.UseNumber()
		if err := decoder.Decode(&schema); err != nil {
			return nil, "", err
		}
		tool.InputSchema, _ = jsonData(schema)
		if err := checkToolSchema(tool.InputSchema); err != nil {
			return nil, "", fail(400, "invalid_tools", err.Error())
		}
		tool.Hash = ""
		next = append(next, tool)
		seen[tool.ID] = true
	}
	sort.Slice(next, func(i, j int) bool { return next[i].ID < next[j].ID })
	b, _ := jsonData(next)
	if len(b) > wirelimits.Definitions {
		return nil, "", fail(413, "tool_set_too_large", "Tool definitions exceed 1 MiB.")
	}
	return next, digest(string(b)), nil
}

func (a *App) executionTools(j *Job) []AppTool {
	if j.Request.ConversationID != "" {
		if c := a.state.Conversations[j.Request.ConversationID]; c != nil {
			return c.Tools
		}
		return nil
	}
	return j.Request.Tools
}

func liveDefinitionBytes(s *State) int {
	total := 0
	for _, c := range s.Conversations {
		if c.Status == "open" && len(c.Tools) > 0 {
			b, _ := jsonData(c.Tools)
			total += len(b)
		}
	}
	for _, j := range s.Jobs {
		if !terminal(j.Status) && j.Request.ConversationID == "" && len(j.Request.Tools) > 0 {
			b, _ := jsonData(j.Request.Tools)
			total += len(b)
		}
	}
	return total
}

func admitDefinitions(s *State, tools []AppTool) error {
	if len(tools) == 0 {
		return nil
	}
	b, _ := jsonData(tools)
	if liveDefinitionBytes(s)+len(b) > wirelimits.LiveDefinitions {
		return fail(429, "tool_definition_limit", "End unused executions before adding more tool definitions.")
	}
	return nil
}
