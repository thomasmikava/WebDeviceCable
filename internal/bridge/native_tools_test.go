package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"testing"

	"github.com/thomasmikava/WebDeviceCable/internal/wirelimits"
)

func TestClaudeLargeToolCatalogLaunchAndIsolation(t *testing.T) {
	f, _, _, job := makeToolJob(t, "app-tools")
	tools := make([]AppTool, 4000)
	for i := range tools {
		tools[i] = AppTool{ID: fmt.Sprintf("tool_%d", i), Title: "Tool", InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)}
	}
	mode := "app-tools"
	snapshot, _, err := snapshotTools(tools, &mode)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonData(snapshot)
	if err != nil || len(encoded) >= wirelimits.Definitions {
		t.Fatal("regression catalog must fit the admitted definition budget", len(encoded), err)
	}
	f.a.state.Jobs[0].Request.Tools = snapshot
	definitions := f.a.nativeToolDefinitions(job)
	if len(definitions) != len(tools) {
		t.Fatal("native catalog was truncated", len(definitions))
	}
	args := claudeToolArgs(claudeArgs(ExecutionTarget{}, nativeUUID()), "private-mcp.json")
	bytes := 0
	for _, arg := range args {
		// Stay below Linux's per-argument limit and Windows' total command-line
		// limit, even when the admitted catalog contains thousands of tools.
		bytes += len(arg) + 1
	}
	if bytes >= 16<<10 {
		t.Fatal("catalog expanded the native command line", bytes)
	}
	if runtime.GOOS != "windows" {
		path, err := exec.LookPath("true")
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.Command(path, args...).Run(); err != nil {
			t.Fatal("OS rejected native launch arguments", err)
		}
	}

	names := make([]string, len(definitions))
	for i, definition := range definitions {
		names[i] = "mcp__wdc__" + definition["name"].(string)
	}
	servers := []json.RawMessage{json.RawMessage(`{"name":"wdc","status":"connected"}`)}
	dir := t.TempDir()
	event := claudeEvent{CWD: dir, Version: claudeMinimumVersion, APIKeySource: "none", PermissionMode: "dontAsk", Model: "fixture", Tools: &names, MCP: &servers}
	if !event.isolatedTools(dir, claudeMinimumVersion, definitions) {
		t.Fatal("complete execution catalog failed isolation check")
	}
	names[len(names)-1] = "mcp__wdc__app_unregistered"
	if event.isolatedTools(dir, claudeMinimumVersion, definitions) {
		t.Fatal("unregistered tool passed isolation check")
	}
	_, err = f.a.invokeNativeTool(context.Background(), job, "app_"+digest("unregistered")[:24], json.RawMessage(`{}`))
	var failure *apiError
	if !errors.As(err, &failure) || failure.Code != "unknown_tool" || len(f.a.permissions) != 0 {
		t.Fatal("unregistered tool reached application callbacks", err)
	}
}
