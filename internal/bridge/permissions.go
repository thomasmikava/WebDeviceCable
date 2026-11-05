package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type AppTool struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	InputSchema      json.RawMessage `json:"inputSchema"`
	ReadOnly         bool            `json:"readOnly"`
	OperationDetails bool            `json:"operationDetails,omitempty"`
	Hash             string          `json:"hash,omitempty"`
}

// ToolGrant is accepted only in legacy control requests; apps own tool authorization.
type ToolGrant struct {
	Hash      string `json:"hash"`
	Automatic bool   `json:"automatic"`
}
type FolderGrant struct {
	Identity  string `json:"identity,omitempty"`
	ID        string `json:"id"`
	Path      string `json:"path,omitempty"`
	Name      string `json:"name"`
	Write     bool   `json:"write"`
	Automatic bool   `json:"automatic"`
}
type PermissionRequest struct {
	ID                    string          `json:"id"`
	PairingID             string          `json:"pairingId"`
	LeaseID               string          `json:"-"`
	JobID                 string          `json:"jobId,omitempty"`
	ConversationID        string          `json:"conversationId,omitempty"`
	RequestIdempotencyKey string          `json:"requestIdempotencyKey,omitempty"`
	ToolSetHash           string          `json:"toolSetHash,omitempty"`
	Kind                  string          `json:"kind"`
	Name                  string          `json:"name"`
	Arguments             json.RawMessage `json:"arguments"`
	Hash                  string          `json:"hash,omitempty"`
	Status                string          `json:"status"`
	CreatedAt             time.Time       `json:"createdAt"`
	Result                json.RawMessage `json:"-"`
	Operation             *ToolOperation  `json:"operation,omitempty"`
	Before                string          `json:"before,omitempty"`
	After                 string          `json:"after,omitempty"`
	changed               chan struct{}
}

func (a *App) signalPermission(op *PermissionRequest) {
	close(op.changed)
	op.changed = make(chan struct{})
}
func (a *App) newPermission(p *Pairing, lease, job, kind, name, hash string, args json.RawMessage) (*PermissionRequest, error) {
	if a.permissions == nil {
		a.permissions = map[string]*PermissionRequest{}
	}
	for id, op := range a.permissions {
		if a.now().Sub(op.CreatedAt) > 10*time.Minute {
			delete(a.permissions, id)
		}
	}
	activeForPair := 0
	oldest := ""
	for id, op := range a.permissions {
		live := a.operationLive(op)
		active := live && (op.Status == "pending" || op.Status == "approved" || op.Status == "dispatched")
		if active && op.PairingID == p.ID {
			activeForPair++
		}
		if !active && (oldest == "" || op.CreatedAt.Before(a.permissions[oldest].CreatedAt)) {
			oldest = id
		}
	}
	if activeForPair >= 8 {
		return nil, fail(429, "permission_limit", "Finish this app's pending permission requests first.")
	}
	if len(a.permissions) >= 128 && oldest != "" {
		delete(a.permissions, oldest)
	}
	if len(a.permissions) >= 128 {
		return nil, fail(429, "permission_limit", "Finish pending permission requests first.")
	}
	op := &PermissionRequest{ID: secret(), PairingID: p.ID, LeaseID: lease, JobID: job, Kind: kind, Name: name, Hash: hash, Arguments: append(json.RawMessage(nil), args...), Status: "pending", CreatedAt: a.now(), changed: make(chan struct{})}
	if j := findJob(&a.state, job); j != nil {
		op.ConversationID = j.Request.ConversationID
		op.RequestIdempotencyKey = j.Request.IdempotencyKey
		op.ToolSetHash = j.Request.ToolSetHash
	}
	a.permissions[op.ID] = op
	return op, nil
}
func (a *App) operationLive(op *PermissionRequest) bool {
	p := a.state.Pairings[op.PairingID]
	lease, ok := a.leases[op.LeaseID]
	if p == nil || p.Paused || p.Revoked || !a.state.Enabled || !ok || lease.PairingID != p.ID || !a.now().Before(lease.ExpiresAt) || a.now().Sub(op.CreatedAt) > 5*time.Minute {
		return false
	}
	if op.JobID != "" {
		j := findJob(&a.state, op.JobID)
		if j == nil || j.Status != "running" {
			return false
		}
	}
	return true
}
func (a *App) permissionList() []PermissionRequest {
	out := []PermissionRequest{}
	for _, op := range a.permissions {
		if op.Kind != "app-tool" && a.operationLive(op) && op.Status == "pending" {
			out = append(out, *op)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
func (a *App) waitPermission(ctx context.Context, op *PermissionRequest, complete bool) (json.RawMessage, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		live := a.operationLive(op)
		status := op.Status
		result := append(json.RawMessage(nil), op.Result...)
		changed := op.changed
		a.mu.Unlock()
		if !live {
			return nil, fail(410, "permission_expired", "The request or its tab has ended.")
		}
		if status == "denied" {
			return nil, fail(403, "permission_denied", "The user declined this operation.")
		}
		if status == "completed" || !complete && status == "approved" {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		case <-ticker.C:
		}
	}
}
func (a *App) permissionRoutes(mux *http.ServeMux, paired func(func(http.ResponseWriter, *http.Request, *Pairing) error) http.HandlerFunc, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("GET /v2/permissions", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		writeJSON(w, 200, permissionSummary(p))
		return nil
	}))
	mux.HandleFunc("GET /v2/permissions/{id}", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		op := a.permissions[r.PathValue("id")]
		if op == nil || op.PairingID != p.ID || op.LeaseID != lease {
			return fail(404, "not_found", "Permission request not found.")
		}
		if !a.operationLive(op) {
			return fail(410, "permission_expired", "This permission request has expired.")
		}
		writeJSON(w, 200, map[string]string{"id": op.ID, "status": op.Status})
		return nil
	}))
	mux.HandleFunc("POST /v2/permissions", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		var in struct {
			Tools     []string `json:"tools"`
			Workspace bool     `json:"workspace"`
		}
		if err = decode(w, r, &in); err != nil {
			return err
		}
		if len(in.Tools) > 0 {
			return fail(400, "invalid_permission", "App tools are execution-scoped and authorized by the application.")
		}
		if permissionsSatisfied(p, in.Tools, in.Workspace) {
			writeJSON(w, 200, map[string]string{"id": "", "status": "completed"})
			return nil
		}
		raw, _ := json.Marshal(in)
		op, err := a.newPermission(p, lease, "", "access", "Folder access", "", raw)
		if err != nil {
			return err
		}
		writeJSON(w, 202, op)
		return nil
	}))
	poll := paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		calls := []PermissionRequest{}
		for _, op := range a.permissions {
			if op.PairingID == p.ID && op.LeaseID == lease && op.Kind == "app-tool" && op.Status == "approved" && a.operationLive(op) {
				calls = append(calls, *op)
			}
		}
		var in struct {
			Requests []string `json:"requests"`
		}
		if r.Method == "POST" {
			if err := decode(w, r, &in); err != nil {
				return err
			}
		}
		if len(in.Requests) > 256 {
			return fail(400, "invalid_poll", "At most 256 pending executions may be observed.")
		}
		completed := []map[string]string{}
		jobsByKey := map[string]*Job{}
		for _, job := range a.state.Jobs {
			if job.PairingID == p.ID {
				jobsByKey[job.Request.IdempotencyKey] = job
			}
		}
		for _, key := range in.Requests {
			if len(key) < 8 || len(key) > 128 {
				return fail(400, "invalid_poll", "Invalid execution request key.")
			}
			if job := jobsByKey[key]; job != nil {
				if job.LeaseID == lease && terminal(job.Status) {
					completed = append(completed, map[string]string{"jobId": job.ID, "requestIdempotencyKey": key, "toolSetHash": job.Request.ToolSetHash})
				}
				continue
			}
			receipt, consumed, err := a.receipt(digest(p.ID + ":" + key))
			if err != nil {
				return err
			}
			if !consumed || receipt.LeaseHash != digest(lease) {
				continue
			}
			job := findJob(&a.state, receipt.JobID)
			if job == nil || terminal(job.Status) {
				completed = append(completed, map[string]string{"jobId": receipt.JobID, "requestIdempotencyKey": key, "toolSetHash": receipt.ToolSetHash})
			}
		}
		writeJSON(w, 200, map[string]any{"calls": calls, "completed": completed})
		return nil
	})

	mux.HandleFunc("GET /v2/tool-calls", poll)
	mux.HandleFunc("POST /v2/tool-calls/poll", poll)
	mux.HandleFunc("POST /v2/tool-calls/{id}/operation", paired(a.reportToolOperation))
	mux.HandleFunc("POST /v2/tool-calls/{id}/claim", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		op := a.permissions[r.PathValue("id")]
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		if op == nil || op.PairingID != p.ID || op.LeaseID != lease || op.Kind != "app-tool" || !a.operationLive(op) {
			return fail(404, "not_found", "Tool call not found.")
		}
		if op.Status != "approved" {
			return fail(409, "tool_call_consumed", "This call has already been claimed. It will not be replayed.")
		}
		op.Status = "dispatched"
		a.signalPermission(op)
		writeJSON(w, 200, op)
		return nil
	}))
	mux.HandleFunc("POST /v2/tool-calls/{id}/result", paired(func(w http.ResponseWriter, r *http.Request, p *Pairing) error {
		op := a.permissions[r.PathValue("id")]
		lease, err := a.liveLease(r, p)
		if err != nil {
			return err
		}
		if op == nil || op.PairingID != p.ID || op.LeaseID != lease || op.Kind != "app-tool" || !a.operationLive(op) {
			return fail(404, "not_found", "Tool call not found.")
		}
		var in struct {
			Result json.RawMessage `json:"result"`
		}
		if err = decode(w, r, &in); err != nil {
			return err
		}
		if len(in.Result) == 0 {
			return fail(400, "invalid_result", "A JSON result is required.")
		}
		if op.Status == "completed" {
			if string(op.Result) != string(in.Result) {
				return fail(409, "result_conflict", "This call already has a different result.")
			}
		} else if op.Status == "dispatched" {
			if op.Operation != nil {
				if err := a.update(func(s *State) error {
					if j := findJob(s, op.JobID); j != nil {
						completeToolOperation(j, op.ID, "returned")
					}
					return nil
				}); err != nil {
					return err
				}
			}
			op.Result = append(json.RawMessage(nil), in.Result...)
			op.Status = "completed"
			a.signalPermission(op)
		} else {
			return fail(409, "invalid_state", "Claim the approved call before returning its result.")
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("POST /api/permissions/{id}/decision", control(func(w http.ResponseWriter, r *http.Request) error {
		op := a.permissions[r.PathValue("id")]
		if op == nil || !a.operationLive(op) {
			return fail(410, "permission_expired", "This request has ended.")
		}
		var in struct {
			Approve   bool `json:"approve"`
			Remember  bool `json:"remember"`
			Automatic bool `json:"automatic"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if op.Status != "pending" {
			return fail(409, "permission_decided", "This request was already answered.")
		}
		if in.Approve && op.Kind == "access" {
			return fail(400, "grant_required", "Choose folders in this connection's Folder access settings.")
		}

		op.Status = "denied"
		if in.Approve {
			op.Status = "approved"
		}
		a.signalPermission(op)
		writeJSON(w, 200, op)
		return nil
	}))
	mux.HandleFunc("PUT /api/pairings/{id}/permissions", control(func(w http.ResponseWriter, r *http.Request) error {
		p := a.state.Pairings[r.PathValue("id")]
		if p == nil || p.Revoked {
			return fail(404, "not_found", "Connection not found.")
		}
		var in struct {
			Tools   map[string]ToolGrant `json:"tools"`
			Folders []FolderGrant        `json:"folders"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if len(in.Folders) > 16 {
			return fail(400, "grant_limit", "Too many grants.")
		}

		ids := map[string]bool{}
		for i := range in.Folders {
			f := &in.Folders[i]
			path, err := filepath.EvalSymlinks(f.Path)
			if err != nil || !filepath.IsAbs(path) || a.protectedFolder(path) {
				return fail(400, "invalid_folder", "Choose a project folder outside WDC and provider configuration directories.")
			}
			root, err := os.OpenRoot(path)
			if err != nil {
				return fail(400, "invalid_folder", "This folder cannot be opened.")
			}
			identity, e := rootIdentity(root)
			root.Close()
			if e != nil {
				return e
			}
			f.Identity = identity
			f.Path = path
			f.Name = filepath.Base(path)
			if !f.Write {
				f.Automatic = false
			}
			if f.ID == "" {
				f.ID = secret()
			}
			if !validStorageID(f.ID) || ids[f.ID] {
				return fail(400, "invalid_folder", "Invalid folder ID.")
			}
			ids[f.ID] = true
		}
		if err := a.update(func(s *State) error {
			pair := s.Pairings[p.ID]
			pair.Folders = in.Folders
			a.cancelPermissionWork(s, p.ID, "permissions_changed")
			return nil
		}); err != nil {
			return err
		}
		for _, op := range a.permissions {
			if op.PairingID == p.ID && op.Kind == "access" && op.Status == "pending" {
				var requested struct {
					Tools     []string `json:"tools"`
					Workspace bool     `json:"workspace"`
				}
				_ = json.Unmarshal(op.Arguments, &requested)
				op.Status = "denied"
				if permissionsSatisfied(a.state.Pairings[p.ID], requested.Tools, requested.Workspace) {
					op.Status = "completed"
				}
				a.signalPermission(op)
			}
		}
		writeJSON(w, 200, permissionSummary(a.state.Pairings[p.ID]))
		return nil
	}))
	mux.HandleFunc("POST /api/folders", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Path string `json:"path"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if in.Path == "" {
			in.Path, _ = os.UserHomeDir()
		}
		path, err := filepath.Abs(in.Path)
		if err != nil {
			return fail(400, "invalid_folder", "Folder not found.")
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return fail(400, "invalid_folder", "Choose an existing folder.")
		}
		info, e := os.Stat(path)
		if e != nil || !info.IsDir() {
			return fail(400, "invalid_folder", "Choose an existing folder.")
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return fail(400, "invalid_folder", "Folder cannot be opened.")
		}
		folders := []string{}
		truncated := false
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				if len(folders) >= 500 {
					truncated = true
					break
				}
				folders = append(folders, entry.Name())
			}
		}
		home, _ := os.UserHomeDir()
		if resolved, e := filepath.EvalSymlinks(home); e == nil {
			home = resolved
		}
		breadcrumbs := []map[string]string{}
		for at := path; ; at = filepath.Dir(at) {
			name := filepath.Base(at)
			if at == home {
				name = "Home"
			} else if at == filepath.Dir(at) {
				name = at
			}
			breadcrumbs = append([]map[string]string{{"name": name, "path": at}}, breadcrumbs...)
			if at == home || at == filepath.Dir(at) {
				break
			}
		}
		name := filepath.Base(path)
		if path == home {
			name = "Home"
		}
		writeJSON(w, 200, map[string]any{"path": path, "name": name, "parent": filepath.Dir(path), "home": home, "breadcrumbs": breadcrumbs, "folders": folders, "canShare": !a.protectedFolder(path), "truncated": truncated})
		return nil
	}))
}
func permissionSummary(p *Pairing) map[string]any {
	folders := []FolderGrant{}
	for _, f := range p.Folders {
		f.Path = ""
		f.Identity = ""
		folders = append(folders, f)
	}
	tools := map[string]AppTool{}
	// Preserve the old response shape without implying WDC grants app actions.
	return map[string]any{"tools": tools, "grants": map[string]ToolGrant{}, "folders": folders}
}
func (a *App) cancelPermissionWork(s *State, pairing, code string) {
	affected := func(mode string) bool {
		return mode == "workspace"
	}
	for _, c := range s.Conversations {
		if c.PairingID == pairing && c.Status == "open" && affected(c.Mode) {
			c.Status = "closed"
			c.Code = code
			c.UpdatedAt = a.now()
		}
	}
	for _, j := range s.Jobs {
		if j.PairingID == pairing && !terminal(j.Status) && affected(j.Request.Mode) {
			finish(j, "cancelled", code, "App tool definitions or folder access changed.", a.now())
		}
	}
}
func (a *App) protectedFolder(path string) bool {
	home, _ := os.UserHomeDir()
	blocked := []string{a.dir, filepath.Join(home, ".codex"), filepath.Join(home, ".claude"), os.Getenv("CODEX_HOME"), os.Getenv("CLAUDE_CONFIG_DIR")}
	for _, private := range blocked {
		if private == "" {
			continue
		}
		private, _ = filepath.Abs(private)
		if resolved, e := filepath.EvalSymlinks(private); e == nil {
			private = resolved
		}
		if insidePath(path, private) || insidePath(private, path) {
			return true
		}
	}
	return strings.HasPrefix(path, `\\`)
}
func insidePath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func (a *App) nativeToolDefinitions(job string) []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	j := findJob(&a.state, job)
	if j == nil {
		return nil
	}
	p := a.state.Pairings[j.PairingID]
	if p == nil {
		return nil
	}
	out := []map[string]any{}
	if j.Request.Mode == "app-tools" || j.Request.Mode == "workspace" {
		for _, tool := range a.executionTools(j) {
			out = append(out, map[string]any{"name": "app_" + digest(tool.ID)[:24], "description": tool.Description, "inputSchema": json.RawMessage(tool.InputSchema)})
		}
	}
	if j.Request.Mode == "workspace" {
		for _, name := range []string{"list", "read", "write"} {
			schema := map[string]any{"type": "object", "properties": map[string]any{"folder": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "expectedHash": map[string]any{"type": "string"}}, "required": []string{"folder", "path"}, "additionalProperties": false}
			description := "Use only user-granted folders: "
			folders, _ := json.Marshal(permissionSummary(p)["folders"])
			description += string(folders)
			out = append(out, map[string]any{"name": "wdc_" + name, "description": description, "inputSchema": schema})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out
}
func (a *App) invokeNativeTool(ctx context.Context, job, name string, args json.RawMessage) (json.RawMessage, error) {
	if len(args) > 32<<10 || !json.Valid(args) {
		return nil, fail(400, "invalid_tool_arguments", "Tool arguments exceed the limit or are invalid JSON.")
	}
	a.mu.Lock()
	j := findJob(&a.state, job)
	if j == nil || j.Status != "running" {
		a.mu.Unlock()
		return nil, context.Canceled
	}
	p := a.state.Pairings[j.PairingID]
	if p == nil || p.Paused || p.Revoked || !a.state.Enabled {
		a.mu.Unlock()
		return nil, context.Canceled
	}
	lease := j.LeaseID
	if l, ok := a.leases[lease]; !ok || l.PairingID != p.ID || !a.now().Before(l.ExpiresAt) {
		a.mu.Unlock()
		return nil, fail(410, "tab_disconnected", "The requesting tab disconnected.")
	}
	mode := j.Request.Mode
	if strings.HasPrefix(name, "app_") && (mode == "app-tools" || mode == "workspace") {
		id := ""
		var tool AppTool
		ok := false
		for _, candidate := range a.executionTools(j) {
			if name == "app_"+digest(candidate.ID)[:24] {
				id = candidate.ID
				tool = candidate
				ok = true
				break
			}
		}
		if !ok {
			a.mu.Unlock()
			return nil, fail(403, "unknown_tool", "This app tool is not registered.")
		}
		if err := validateToolArguments(tool.InputSchema, args); err != nil {
			a.mu.Unlock()
			return nil, fail(400, "invalid_tool_arguments", "Arguments do not match the registered schema.")
		}
		op, err := a.newPermission(p, lease, job, "app-tool", id, tool.Hash, args)
		if err == nil {
			// Authorization and confirmation belong to the app. WDC only
			// delivers this schema-checked call to its originating tab once.
			op.Status = "approved"
		}
		a.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return a.waitPermission(ctx, op, true)
	}
	a.mu.Unlock()
	if mode != "workspace" {
		return nil, fail(403, "permission_denied", "This operation requires Workspace access.")
	}
	return a.workspaceTool(ctx, job, name, args)
}
func (a *App) workspaceTool(ctx context.Context, job, name string, args json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Folder       string  `json:"folder"`
		Path         string  `json:"path"`
		Text         *string `json:"text"`
		ExpectedHash string  `json:"expectedHash"`
	}
	if json.Unmarshal(args, &in) != nil || filepath.IsAbs(in.Path) || strings.Contains(in.Path, "\\") {
		return nil, fail(400, "invalid_path", "Use a relative path inside a granted folder.")
	}
	if in.Path == "" {
		in.Path = "."
	}
	a.mu.Lock()
	j := findJob(&a.state, job)
	if j == nil || j.Status != "running" {
		a.mu.Unlock()
		return nil, context.Canceled
	}
	p := a.state.Pairings[j.PairingID]
	var grant FolderGrant
	if p != nil {
		for _, folder := range p.Folders {
			if folder.ID == in.Folder {
				grant = folder
			}
		}
	}
	if grant.ID == "" || p.Paused || p.Revoked {
		a.mu.Unlock()
		return nil, fail(403, "folder_not_granted", "Choose and approve this folder in WDC first.")
	}
	lease := j.LeaseID
	a.mu.Unlock()
	root, err := os.OpenRoot(grant.Path)
	if err != nil {
		return nil, fail(403, "folder_unavailable", "The granted folder is unavailable.")
	}
	defer root.Close()
	identity, e := rootIdentity(root)
	if e != nil || identity != grant.Identity {
		return nil, fail(403, "folder_changed", "The folder was replaced. Review its grant in WDC.")
	}
	read := func() ([]byte, error) {
		f, err := openWorkspaceFile(root, in.Path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("not a regular file")
		}
		data, err := io.ReadAll(io.LimitReader(f, maxNativeFile+1))
		if len(data) > maxNativeFile {
			return nil, errors.New("file exceeds 8 MiB")
		}
		return data, err
	}
	switch name {
	case "wdc_list":
		f, err := openWorkspaceFile(root, in.Path)
		if err != nil {
			return nil, fail(403, "invalid_path", "This path is outside the grant or unavailable.")
		}
		defer f.Close()
		entries, err := f.ReadDir(501)
		if err != nil && err != io.EOF {
			return nil, err
		}
		names := []map[string]any{}
		for _, entry := range entries {
			if len(names) >= 500 {
				break
			}
			names = append(names, map[string]any{"name": entry.Name(), "directory": entry.IsDir(), "link": entry.Type()&os.ModeSymlink != 0})
		}
		return json.Marshal(map[string]any{"entries": names})
	case "wdc_read":
		data, err := read()
		if err != nil {
			return nil, fail(403, "invalid_path", "This file is outside the grant, unavailable or too large.")
		}
		if data == nil {
			return nil, fail(404, "not_found", "File not found.")
		}
		if utf8.Valid(data) {
			return json.Marshal(map[string]any{"text": string(data), "sha256": fileHash(data)})
		}
		return json.Marshal(map[string]any{"base64": base64.StdEncoding.EncodeToString(data), "sha256": fileHash(data)})
	case "wdc_write":
		var operation *PermissionRequest
		if !grant.Write || in.Text == nil {
			return nil, fail(403, "write_not_granted", "This folder does not allow writing, or text is missing.")
		}
		before, err := read()
		if err != nil {
			return nil, fail(403, "invalid_path", "This file cannot be edited safely.")
		}
		hash := fileHash(before)
		if in.ExpectedHash != "" && in.ExpectedHash != hash {
			return nil, fail(409, "file_changed", "The file changed. Read it again before editing.")
		}
		if !grant.Automatic {
			a.mu.Lock()
			p = a.state.Pairings[p.ID]
			op, e := a.newPermission(p, lease, job, "file-write", in.Path, hash, args)
			if e == nil {
				operation = op
				op.Before = string(before[:min(len(before), 16<<10)])
				op.After = *in.Text
			}
			a.mu.Unlock()
			if e != nil {
				return nil, e
			}
			if _, e = a.waitPermission(ctx, op, false); e != nil {
				return nil, e
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		defer func() {
			if operation != nil && operation.Status == "approved" {
				operation.Status = "completed"
				a.signalPermission(operation)
			}
		}()
		j = findJob(&a.state, job)
		p = a.state.Pairings[p.ID]
		if j == nil || j.Status != "running" || p == nil || p.Revoked || p.Paused || !a.state.Enabled {
			return nil, context.Canceled
		}
		liveLease, ok := a.leases[lease]
		if ctx.Err() != nil || !ok || liveLease.PairingID != p.ID || !a.now().Before(liveLease.ExpiresAt) {
			return nil, fail(410, "tab_disconnected", "The requesting tab disconnected before the edit was applied.")
		}
		valid := false
		for _, f := range p.Folders {
			if f == grant {
				valid = true
			}
		}
		if !valid {
			return nil, fail(403, "grant_changed", "The folder grant changed.")
		}
		current, err := read()
		if err != nil || fileHash(current) != hash {
			return nil, fail(409, "file_changed", "The file changed after its preview. Read it again and request a new edit.")
		}
		dir := filepath.Dir(in.Path)
		tmp := filepath.Join(dir, ".wdc-"+secret())
		f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		defer root.Remove(tmp)
		_, err = f.Write([]byte(*in.Text))
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = root.Rename(tmp, in.Path)
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"sha256": fileHash([]byte(*in.Text)), "written": true})
	}
	return nil, fail(403, "unknown_tool", "This operation is not available.")
}

func permissionsSatisfied(p *Pairing, tools []string, workspace bool) bool {
	if workspace && len(p.Folders) == 0 {
		return false
	}
	if len(tools) > 0 {
		return false
	}
	return true
}
