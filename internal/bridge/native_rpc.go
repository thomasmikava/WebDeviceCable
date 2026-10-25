package bridge

// Codex owns authentication and inference. WDC speaks only the documented
// app-server stdio protocol; it never opens a provider credential file.
import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type nativeRPC struct {
	toolHandler   func(context.Context, nativeMessage) error
	input         io.WriteCloser
	packets       chan conversationPacket
	cancel        context.CancelFunc
	stopped       chan struct{}
	next          int
	notifications []nativeMessage
	readerStopped chan struct{}
	spawn         func(map[string]any) (*nativeRPC, error)
}
type nativeMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func nativePath(name string) (string, error) {
	if p, e := exec.LookPath(name); e == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	paths := []string{filepath.Join(home, ".local", "bin", name), filepath.Join(home, ".npm-global", "bin", name), "/opt/homebrew/bin/" + name, "/usr/local/bin/" + name}
	if name == "codex" {
		paths = append(paths, "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex")
	}
	for _, p := range paths {
		if info, e := os.Stat(p); e == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not installed", name)
}
func tomlValue(v any) string {
	switch value := v.(type) {
	case []any:
		parts := []string{}
		for _, item := range value {
			parts = append(parts, tomlValue(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, key := range keys {
			parts = append(parts, strconv.Quote(key)+"="+tomlValue(value[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
func codexConfig(dir string, images bool) map[string]any {
	features := map[string]any{}
	for _, key := range []string{"apps", "plugins", "hooks", "shell_tool", "multi_agent", "memories", "skill_search", "web_search_request", "web_search_cached", "code_mode", "code_mode_host", "browser_use", "computer_use", "in_app_browser", "workspace_dependencies", "view_image", "remote_plugin", "skill_mcp_dependency_install", "tool_suggest", "enable_mcp_apps", "auth_elicitation"} {
		features[key] = false
	}
	features["image_generation"] = images
	features["code_mode_host"] = images
	return map[string]any{
		"features": features, "mcp_servers": map[string]any{}, "plugins": map[string]any{}, "apps": map[string]any{"_default": map[string]any{"enabled": false}},
		"model_provider": "openai", "notify": []string{}, "web_search": "disabled", "project_doc_max_bytes": 0, "model_instructions_file": filepath.Join(dir, "instructions.txt"), "developer_instructions": "",
		"history": map[string]any{"persistence": "none"}, "analytics": map[string]any{"enabled": false}, "otel": map[string]any{"exporter": "none", "trace_exporter": "none", "metrics_exporter": "none", "log_user_prompt": false},
		"default_permissions": "wdc", "permissions": map[string]any{"wdc": map[string]any{"filesystem": map[string]any{":minimal": "read", dir: "write"}, "network": map[string]any{"enabled": false}}},
	}
}
func startNativeRPC(parent context.Context, path, dir string, config map[string]any, stderr io.Writer) (*nativeRPC, error) {
	args := []string{"app-server", "--stdio"}
	keys := []string{}
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-c", key+"="+tomlValue(config[key]))
	}
	rpc, e := startNativeProcess(parent, path, dir, args, stderr)
	if e != nil {
		return nil, e
	}
	rpc.spawn = func(configuration map[string]any) (*nativeRPC, error) {
		return startNativeRPC(parent, path, dir, configuration, stderr)
	}
	return rpc, nil
}

func startNativeProcess(parent context.Context, path, dir string, args []string, stderr io.Writer) (*nativeRPC, error) {
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = clientEnv()
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	input, e := cmd.StdinPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	reader, writer := io.Pipe()
	cmd.Stdout = writer
	cleanup, e := startOwnedCommand(cmd)
	if e != nil {
		cancel()
		input.Close()
		reader.Close()
		writer.Close()
		return nil, e
	}
	rpc := &nativeRPC{input: input, packets: make(chan conversationPacket, 8), cancel: cancel, stopped: make(chan struct{}), readerStopped: make(chan struct{})}
	go func() { e := cmd.Wait(); cleanup(); writer.CloseWithError(e); close(rpc.stopped) }()
	go func() {
		defer close(rpc.readerStopped)
		defer reader.Close()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 24<<20)
		for scanner.Scan() {
			packet := conversationPacket{Data: append([]byte{}, scanner.Bytes()...)}
			select {
			case rpc.packets <- packet:
			case <-ctx.Done():
				return
			}
		}
		e := scanner.Err()
		if e == nil {
			e = io.EOF
		}
		select {
		case rpc.packets <- conversationPacket{Err: e}:
		case <-ctx.Done():
		}
	}()
	return rpc, nil
}
func (r *nativeRPC) relaunch(configuration map[string]any) error {
	if r.spawn == nil {
		return errors.New("native process cannot be isolated")
	}
	spawn := r.spawn
	r.close()
	fresh, e := spawn(configuration)
	if e != nil {
		return e
	}
	*r = *fresh
	return nil
}
func (r *nativeRPC) close() { r.cancel(); r.input.Close(); <-r.stopped; <-r.readerStopped }
func (r *nativeRPC) send(value any) error {
	data, e := json.Marshal(value)
	if e != nil {
		return e
	}
	_, e = r.input.Write(append(data, '\n'))
	return e
}
func (r *nativeRPC) receive(ctx context.Context) (nativeMessage, error) {
	if len(r.notifications) > 0 {
		m := r.notifications[0]
		r.notifications = r.notifications[1:]
		return m, nil
	}
	return r.receiveRaw(ctx)
}
func (r *nativeRPC) receiveRaw(ctx context.Context) (nativeMessage, error) {
	select {
	case <-ctx.Done():
		return nativeMessage{}, ctx.Err()
	case p := <-r.packets:
		if p.Err != nil {
			return nativeMessage{}, p.Err
		}
		var m nativeMessage
		if json.Unmarshal(p.Data, &m) != nil {
			return m, errors.New("invalid native protocol message")
		}
		// Only the explicit WDC dynamic-tool dispatcher can answer a tool request.
		if len(m.ID) > 0 && m.Method == "item/tool/call" && r.toolHandler != nil {
			if err := r.toolHandler(ctx, m); err != nil {
				return m, err
			}
			return r.receiveRaw(ctx)
		}
		if len(m.ID) > 0 && m.Method != "" {
			_ = r.send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "Tools and authentication requests are disabled in WebDeviceCable Restricted mode."}})
			return m, errors.New("unexpected native tool or authentication request")
		}
		return m, nil
	}
}
func (r *nativeRPC) call(ctx context.Context, method string, params any, out any) error {
	r.next++
	id := r.next
	if e := r.send(map[string]any{"id": id, "method": method, "params": params}); e != nil {
		return e
	}
	for {
		m, e := r.receiveRaw(ctx)
		if e != nil {
			return e
		}
		if len(m.ID) == 0 {
			if len(r.notifications) >= 128 {
				return errors.New("too many native startup events")
			}
			r.notifications = append(r.notifications, m)
			continue
		}
		var got int
		if json.Unmarshal(m.ID, &got) != nil || got != id {
			return errors.New("unexpected native response")
		}
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(m.Result, out)
	}
}
func codexAccount(ctx context.Context, rpc *nativeRPC) (string, error) {
	var result struct {
		Account *struct {
			Type  string `json:"type"`
			Email string `json:"email"`
		} `json:"account"`
	}
	if e := rpc.call(ctx, "account/read", map[string]any{"refreshToken": false}, &result); e != nil {
		return "", e
	}
	if result.Account == nil {
		return "", fail(503, "authentication_required", "Sign in using the installed Codex application, then check again.")
	}
	if result.Account.Type != "chatgpt" {
		return "", fail(403, "api_key_not_allowed", "Codex must be signed in with your ChatGPT subscription. API billing is not used.")
	}
	if result.Account.Email == "" {
		return "", fail(503, "account_identity_unavailable", "Codex did not supply an account identity to pin. This account is not supported yet.")
	}
	return digest(strings.ToLower(result.Account.Email)), nil
}

type codexThread struct {
	NativeVersion string `json:"-"`
	Model         string `json:"model"`
	ModelProvider string `json:"modelProvider"`
	Reasoning     string `json:"reasoningEffort"`
	Cwd           string `json:"cwd"`
	Approval      string `json:"approvalPolicy"`
	Instructions  []any  `json:"instructionSources"`
	Profile       struct {
		ID      string  `json:"id"`
		Extends *string `json:"extends"`
	} `json:"activePermissionProfile"`
	Sandbox struct {
		Network bool `json:"networkAccess"`
	} `json:"sandbox"`
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

func prepareCodex(ctx context.Context, rpc *nativeRPC, dir string, config map[string]any, target ExecutionTarget, tools ...[]map[string]any) (codexThread, string, error) {
	var thread codexThread
	version, e := initializeCodex(ctx, rpc)
	if e != nil {
		return thread, "", e
	}
	thread.NativeVersion = version
	account, e := codexAccount(ctx, rpc)
	if e != nil {
		return thread, "", e
	}
	effective, e := readCodexControls(ctx, rpc, dir, config)
	if e != nil {
		return thread, "", e
	}
	// Empty TOML tables merge with user tables. Disable each inherited entry;
	// never edit config.toml or return raw configuration to a paired application.
	for _, key := range []string{"mcp_servers", "plugins", "apps"} {
		var entries map[string]any
		if json.Unmarshal(effective[key], &entries) != nil {
			return thread, "", errors.New("native integration configuration unavailable")
		}
		for name := range entries {
			config[key].(map[string]any)[name] = map[string]any{"enabled": false}
		}
	}
	var skills struct {
		Data []struct {
			Skills []struct {
				Path string `json:"path"`
			} `json:"skills"`
		} `json:"data"`
	}
	if e = rpc.call(ctx, "skills/list", map[string]any{"cwds": []string{dir}}, &skills); e != nil {
		return thread, "", e
	}
	disabled := []any{}
	for _, entry := range skills.Data {
		for _, skill := range entry.Skills {
			disabled = append(disabled, map[string]any{"path": skill.Path, "enabled": false})
		}
	}
	config["skills"] = map[string]any{"config": disabled}
	// The metadata-only process has never received a model prompt. Start the
	// execution process with the complete per-entry allowlist, then verify its
	// effective settings. This also detects managed settings that override us.
	if e = rpc.relaunch(config); e != nil {
		return thread, "", e
	}
	version, e = initializeCodex(ctx, rpc)
	if e != nil {
		return thread, "", e
	}
	thread.NativeVersion = version
	current, e := codexAccount(ctx, rpc)
	if e != nil {
		return thread, "", e
	}
	if current != account {
		return thread, "", fail(403, "native_account_changed", "Codex changed accounts during setup. Reconnect the application.")
	}
	effective, e = readCodexControls(ctx, rpc, dir, config)
	if e != nil {
		return thread, "", e
	}
	for _, key := range []string{"mcp_servers", "plugins", "apps"} {
		var entries map[string]struct {
			Enabled *bool `json:"enabled"`
		}
		if json.Unmarshal(effective[key], &entries) != nil {
			return thread, "", errors.New("native integration controls unavailable")
		}
		for _, entry := range entries {
			if entry.Enabled == nil || *entry.Enabled {
				return thread, "", errors.New("native managed integration could not be disabled")
			}
		}
	}
	var selectedSkills struct {
		Config []struct {
			Enabled bool `json:"enabled"`
		} `json:"config"`
	}
	if json.Unmarshal(effective["skills"], &selectedSkills) != nil {
		return thread, "", errors.New("native skill controls unavailable")
	}
	for _, skill := range selectedSkills.Config {
		if skill.Enabled {
			return thread, "", errors.New("native managed skill could not be disabled")
		}
	}

	instructions := "You are a helpful assistant. Use supplied conversation inputs only. No application tools, shell, browsing or filesystem tools are available."
	if config["features"].(map[string]any)["image_generation"] == true {
		instructions += " The built-in image generation tool is available for requested image generation or editing."
	}
	params := map[string]any{"cwd": dir, "permissions": "wdc", "approvalPolicy": "never", "ephemeral": true, "baseInstructions": instructions, "environments": []string{}, "runtimeWorkspaceRoots": []string{dir}, "config": config, "modelProvider": "openai"}
	if len(tools) > 0 && len(tools[0]) > 0 {
		params["dynamicTools"] = tools[0]
		params["baseInstructions"] = "Use only supplied inputs and the explicit WDC tools. WDC enforces user permissions. No other shell, network, files or integrations are available."
	}
	if target.Model != "" {
		params["model"] = target.Model
	}
	if e = rpc.call(ctx, "thread/start", params, &thread); e != nil {
		return thread, "", e
	}
	if target.Reasoning != "" && thread.Reasoning != target.Reasoning {
		return thread, "", errors.New("native reasoning preference was not enforced")
	}
	if thread.Profile.ID != "wdc" || thread.Profile.Extends != nil || thread.Approval != "never" || thread.Sandbox.Network || len(thread.Instructions) != 0 || thread.ModelProvider != "openai" || filepath.Clean(thread.Cwd) != filepath.Clean(dir) || thread.Thread.ID == "" {
		return thread, "", fail(503, "native_isolation_unavailable", "This Codex version or managed policy cannot enforce the required isolated session. Update Codex or review its managed settings.")
	}
	return thread, account, nil
}

func initializeCodex(ctx context.Context, rpc *nativeRPC) (string, error) {
	var initialized struct {
		UserAgent string `json:"userAgent"`
	}
	if e := rpc.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "webdevicecable", "title": "WebDeviceCable", "version": "0.1.0"}, "capabilities": map[string]any{"experimentalApi": true}}, &initialized); e != nil {
		return "", e
	}
	if e := rpc.send(map[string]any{"method": "initialized"}); e != nil {
		return "", e
	}
	return nativeVersionPattern.FindString(strings.Split(initialized.UserAgent, " ")[0]), nil
}
func readCodexControls(ctx context.Context, rpc *nativeRPC, dir string, config map[string]any) (map[string]json.RawMessage, error) {
	var read struct {
		Config map[string]json.RawMessage `json:"config"`
	}
	if e := rpc.call(ctx, "config/read", map[string]any{"includeLayers": false}, &read); e != nil {
		return nil, e
	}
	// Managed policy must not silently turn a disabled capability back on.
	var effectiveFeatures map[string]any
	if json.Unmarshal(read.Config["features"], &effectiveFeatures) != nil {
		return nil, errors.New("native feature controls unavailable")
	}
	for key, wanted := range config["features"].(map[string]any) {
		if effectiveFeatures[key] != wanted {
			return nil, fmt.Errorf("native feature policy could not disable %s", key)
		}
	}
	var permissionProfiles map[string]struct {
		Extends    *string        `json:"extends"`
		Filesystem map[string]any `json:"filesystem"`
		Network    struct {
			Enabled bool `json:"enabled"`
		} `json:"network"`
	}
	if json.Unmarshal(read.Config["permissions"], &permissionProfiles) != nil {
		return nil, errors.New("native permissions unavailable")
	}
	permission, exists := permissionProfiles["wdc"]
	if !exists || permission.Extends != nil || permission.Network.Enabled || permission.Filesystem[":minimal"] != "read" || permission.Filesystem[dir] != "write" {
		return nil, errors.New("native filesystem profile could not be enforced")
	}
	for key, value := range permission.Filesystem {
		if value != nil && key != ":minimal" && key != dir {
			return nil, errors.New("native filesystem profile inherited additional access")
		}
	}
	// Custom inference endpoints are not an eligible subscription path.
	var providers map[string]any
	_ = json.Unmarshal(read.Config["model_providers"], &providers)
	if _, exists := providers["openai"]; exists {
		return nil, errors.New("custom OpenAI provider configuration is not supported")
	}
	var base string
	_ = json.Unmarshal(read.Config["chatgpt_base_url"], &base)
	if base != "" && strings.TrimSuffix(base, "/") != "https://chatgpt.com/backend-api" {
		return nil, errors.New("custom ChatGPT endpoint is not supported")
	}
	return read.Config, nil
}
