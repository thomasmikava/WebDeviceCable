package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// This private adapter only translates MCP stdio to the owning conversation.
// It has no provider credentials and cannot grant permissions itself.
func RunMCPHelper() error {
	endpoint, token := os.Getenv("WDC_HELPER_ENDPOINT"), os.Getenv("WDC_HELPER_TOKEN")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || !validStorageID(token) {
		return errors.New("invalid private tool adapter")
	}
	client := &http.Client{Timeout: 6 * time.Minute, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	scan := bufio.NewScanner(os.Stdin)
	scan.Buffer(make([]byte, 4096), 48<<10)
	for scan.Scan() {
		var in struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &in) != nil {
			return errors.New("invalid MCP request")
		}
		if len(in.ID) == 0 {
			continue
		}
		var result any
		var callErr error
		switch in.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "WebDeviceCable", "version": "0.1.0"}}
		case "ping":
			result = map[string]any{}
		case "tools/list", "tools/call":
			raw, _ := json.Marshal(map[string]any{"id": in.ID, "method": in.Method, "params": json.RawMessage(in.Params)})
			req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			resp, e := client.Do(req)
			if e != nil {
				callErr = e
				break
			}
			data, e := io.ReadAll(io.LimitReader(resp.Body, (24<<20)+1))
			resp.Body.Close()
			if e != nil || len(data) > 24<<20 || resp.StatusCode != 200 || json.Unmarshal(data, &result) != nil {
				callErr = errors.New("private tool request failed")
			}
		default:
			callErr = errors.New("unsupported MCP request")
		}
		out := map[string]any{"jsonrpc": "2.0", "id": in.ID, "result": result}
		if callErr != nil {
			delete(out, "result")
			out["error"] = map[string]any{"code": -32603, "message": "WDC could not perform this tool request: " + callErr.Error()}
		}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			return err
		}
	}
	return scan.Err()
}
func (a *App) startToolAdapter(ctx context.Context, c *commandConversation, definitions []map[string]any) (map[string]any, func(), error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	token := secret()
	endpoint := "http://" + listener.Addr().String()
	exe, err := os.Executable()
	if err != nil {
		listener.Close()
		return nil, nil, err
	}
	seenCalls := map[string]bool{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/" || r.Host != listener.Addr().String() || r.Header.Get("Origin") != "" || !equal(bearer(r), token) {
			http.Error(w, "denied", 403)
			return
		}
		var in struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Meta      json.RawMessage `json:"_meta,omitempty"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if decode(w, r, &in) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if in.Method == "tools/list" {
			writeJSON(w, 200, map[string]any{"tools": definitions})
			return
		}
		if in.Method != "tools/call" {
			http.Error(w, "denied", 403)
			return
		}
		callID, err := nativeToolCallID(in.ID)
		if err != nil {
			http.Error(w, "invalid call ID", 400)
			return
		}
		c.mu.Lock()
		active := c.active
		duplicate := seenCalls[callID] || len(seenCalls) >= 512
		if active != nil && !duplicate {
			seenCalls[callID] = true
		}
		c.mu.Unlock()
		if active == nil {
			http.Error(w, "no active turn", 409)
			return
		}
		if duplicate {
			http.Error(w, "tool call already consumed or session call limit reached; never retry an uncertain operation", 409)
			return
		}
		data, err := a.invokeNativeTool(active.ctx, active.jobID, in.Params.Name, in.Params.Arguments)
		var content []any
		if err == nil {
			content, err = a.nativeToolContent(active.ctx, active.jobID, data, false)
		}
		if err != nil {
			content = []any{map[string]any{"type": "text", "text": err.Error()}}
		}
		writeJSON(w, 200, map[string]any{"content": content, "isError": err != nil})
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 6 * time.Minute, MaxHeaderBytes: 4096}
	go server.Serve(listener)
	stop := func() { server.Close() }
	go func() { <-ctx.Done(); stop() }()
	configuration := map[string]any{"mcpServers": map[string]any{"wdc": map[string]any{"command": exe, "args": []string{"internal-mcp"}, "env": map[string]string{"WDC_HELPER_ENDPOINT": endpoint, "WDC_HELPER_TOKEN": token}}}}
	return configuration, stop, nil
}

func nativeToolCallID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > 128 {
		return "", errors.New("invalid call ID")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return "s:" + text, nil
	}
	if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		return "n:" + strconv.FormatInt(n, 10), nil
	}
	return "", errors.New("invalid call ID")
}
func claudeToolArgs(base []string, config string) []string {
	out := []string{}
	for _, arg := range base {
		if arg != "--safe-mode" {
			out = append(out, arg)
		}
	}
	for i := 0; i < len(out)-1; i++ {
		if out[i] == "--settings" {
			out[i+1] = `{"disableAllHooks":true,"disableClaudeAiConnectors":true,"autoMemoryEnabled":false,"syncClaudeAiSkills":false,"syncClaudeAiPlugins":false,"disableBundledSkills":true,"claudeMdExcludes":["**"],"enabledPlugins":{"cc-plugin-agents-md@builtin":false},"pluginConfigs":{"cc-plugin-agents-md@builtin":{"options":{"instructionFiles":"managed-only"}}}}`
		}
		if out[i] == "--mcp-config" {
			out[i+1] = config
		}
		if out[i] == "--disallowedTools" {
			out[i+1] = "Bash,PowerShell,Read,Write,Edit,Glob,Grep,WebFetch,WebSearch,Agent"
		}
		if out[i] == "--system-prompt" {
			out[i+1] = "Use supplied inputs and only explicit WebDeviceCable MCP tools. WDC enforces the user's app and folder permissions."
		}
	}
	// Strict MCP configuration exposes only this execution's private adapter.
	// Its exact snapshot is checked at initialization and on every call; a
	// server-scoped rule keeps launch arguments independent of catalog size.
	out = append(out, "--allowedTools", "mcp__wdc__*")
	return out
}
func (e *claudeEvent) isolatedTools(dir, version string, definitions []map[string]any) bool {
	copy := *e
	empty := []string{}
	mcp := []json.RawMessage{}
	copy.Tools = &empty
	copy.MCP = &mcp
	if !copy.isolated(dir, version) || e.Tools == nil || e.MCP == nil {
		return false
	}
	allowed := map[string]bool{}
	for _, tool := range definitions {
		allowed["mcp__wdc__"+tool["name"].(string)] = true
	}
	for _, name := range *e.Tools {
		if !allowed[name] {
			return false
		}
		delete(allowed, name)
	}
	if len(allowed) != 0 || len(*e.MCP) != 1 {
		return false
	}
	var server struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	return json.Unmarshal((*e.MCP)[0], &server) == nil && server.Name == "wdc" && server.Status == "connected"
}
