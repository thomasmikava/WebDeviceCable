package bridge

// AI program identities are local configuration, never supplied by a website.
// The original provider IDs remain the two automatically detected defaults.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type AIProgram struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Executable string   `json:"executable,omitempty"`
	Arguments  []string `json:"arguments,omitempty"`
	ProfileDir string   `json:"profileDir,omitempty"`
}

func defaultPrograms() map[string]AIProgram {
	return map[string]AIProgram{
		"codex":  {ID: "codex", Name: "Codex", Type: "codex"},
		"claude": {ID: "claude", Name: "Claude Code", Type: "claude"},
	}
}
func (a *App) program(id string) (AIProgram, bool) {
	if a.state.Programs == nil {
		p, ok := defaultPrograms()[id]
		return p, ok
	}
	p, ok := a.state.Programs[id]
	return p, ok
}
func (a *App) programList() []AIProgram {
	programs := a.state.Programs
	if programs == nil {
		programs = defaultPrograms()
	}
	out := []AIProgram{}
	for _, p := range programs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (a *App) programHashes() map[string]string {
	out := map[string]string{}
	for _, p := range a.programList() {
		out[p.ID] = p.hash()
	}
	return out
}
func (a *App) checkProgramRequest(p *Pairing, plan *ExecutionPlan, request JobRequest) error {
	program, ok := a.program(plan.Provider)
	if !ok {
		return fail(503, "program_unavailable", "The selected AI program is unavailable.")
	}
	if plan.ProgramHash != "" && plan.ProgramHash != program.hash() {
		return fail(409, "program_changed", "This AI program changed. Start a new conversation.")
	}
	if program.Type != "custom" {
		return nil
	}
	if p.ProgramGrants[program.ID] != program.hash() {
		return fail(403, "program_not_granted", "Grant this custom AI program OS account permissions in this connection's Program access settings.")
	}
	if request.Mode == "app-tools" || request.Mode == "workspace" || len(request.OutputSchema) > 0 || len(request.Inputs) > 0 || plan.Kind != "text" {
		return fail(400, "unsupported_feature", "This custom integration supports text only. Structured output, files, images and app tools are unavailable.")
	}
	info := customProgramInfo{}
	for _, status := range a.native {
		if status.ID == program.ID {
			info.Sessions = status.Sessions
			info.Models = status.Models
		}
	}
	return customOptions(info, plan.ExecutionTarget, request.ConversationID != "")
}
func customOptions(info customProgramInfo, target ExecutionTarget, continuing bool) error {
	if continuing && !info.Sessions {
		return fail(400, "sessions_unsupported", "This custom AI program does not support continuing conversations.")
	}
	if target.Model == "" && target.Reasoning == "" {
		return nil
	}
	for _, model := range info.Models {
		if model.ID != target.Model {
			continue
		}
		if target.Reasoning == "" {
			return nil
		}
		for _, r := range model.Reasoning {
			if r.Effort == target.Reasoning {
				return nil
			}
		}
	}
	return fail(400, "unsupported_options", "Choose a model and reasoning preference reported by this custom program.")
}

func customAction(p AIProgram, continuing bool) configuredAction {
	path, _ := p.path()
	args := append([]string{path}, p.Arguments...)
	args = append(args, "--wdc-session")
	return configuredAction{Action: Action{ID: p.ID, Title: p.Name, Exec: args, Timeout: 600, MaxConcurrent: 4, Session: &ActionSession{Protocol: "jsonl", IdleTimeout: 60}}, program: &p, continuing: continuing}
}
func (p AIProgram) hash() string {
	p.Name = "" // Renames preserve grants, routes and conversations.
	b, _ := json.Marshal(p)
	return digest(string(b))
}
func (p AIProgram) path() (string, error) {
	if p.Executable == "" {
		return nativePath(p.Type)
	}
	return exec.LookPath(p.Executable)
}
func validateProgram(p AIProgram) error {
	if !actionID.MatchString(p.ID) || p.ID == "connection-test" || (p.Type != "codex" && p.Type != "claude" && p.Type != "custom") {
		return fail(400, "invalid_program", "Choose a supported AI integration.")
	}
	if (p.ID == "codex" || p.ID == "claude") && p.Type != p.ID {
		return fail(400, "invalid_program", "Default integrations keep their original type.")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 || !utf8.ValidString(p.Name) || strings.ContainsAny(p.Name, "\x00\r\n") {
		return fail(400, "invalid_program", "Use a display name of 1–100 bytes.")
	}
	if len(p.Arguments) > 31 || len(p.Executable) > 4096 || len(p.ProfileDir) > 4096 || strings.ContainsAny(p.Executable+p.ProfileDir, "\x00\r\n") || !utf8.ValidString(p.Executable+p.ProfileDir) {
		return fail(400, "invalid_program", "Use valid executable and profile paths.")
	}
	if p.ProfileDir != "" && !filepath.IsAbs(p.ProfileDir) {
		return fail(400, "invalid_program", "The profile directory must be an absolute path.")
	}
	if p.Type == "custom" && p.Executable == "" {
		return fail(400, "invalid_program", "Custom programs require an executable.")
	}
	// Native launch options are owned by WDC's isolation integrations. A wrapper
	// executable can select another installation; arbitrary flags use Custom.
	if p.Type != "custom" && len(p.Arguments) > 0 {
		return fail(400, "invalid_program", "Native integrations manage their safety arguments. Use a custom wrapper integration for other arguments.")
	}
	n := 0
	for _, v := range p.Arguments {
		n += len(v)
		if !validInput(v) || n > 8192 {
			return fail(400, "invalid_program", "Program arguments exceed the text limit.")
		}
	}
	return nil
}

type programContextKey struct{}

func withProgram(ctx context.Context, program AIProgram) context.Context {
	return context.WithValue(ctx, programContextKey{}, program)
}
func contextProgram(ctx context.Context) AIProgram {
	p, _ := ctx.Value(programContextKey{}).(AIProgram)
	return p
}
func programEnv(p AIProgram) []string {
	env := clientEnv()
	if p.Type == "custom" {
		env = commandEnv()
	}
	if p.ProfileDir == "" {
		return env
	}
	key := "WDC_PROFILE_DIR"
	if p.Type == "codex" {
		key = "CODEX_HOME"
	}
	if p.Type == "claude" {
		key = "CLAUDE_CONFIG_DIR"
	}
	out := []string{}
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if !strings.EqualFold(k, key) {
			out = append(out, v)
		}
	}
	return append(out, key+"="+p.ProfileDir)
}
func (a *App) programForJob(jobID string) (AIProgram, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j := findJob(&a.state, jobID)
	if j == nil || j.Status != "running" {
		return AIProgram{}, context.Canceled
	}
	p, ok := a.program(jobProvider(j))
	if !ok || j.Plan == nil || j.Plan.ProgramHash != "" && p.hash() != j.Plan.ProgramHash {
		return AIProgram{}, fail(409, "program_changed", "This AI program changed. Start a new conversation.")
	}
	return p, nil
}
func (a *App) invalidateProgram(s *State, id, code string) {
	for _, c := range s.Conversations {
		if c.Plan != nil && c.Plan.Provider == id && c.Status == "open" {
			c.Status = "closed"
			c.Code = code
			c.UpdatedAt = a.now()
		}
	}
	for _, j := range s.Jobs {
		if jobProvider(j) == id && !terminal(j.Status) {
			finish(j, "cancelled", code, "The AI program configuration changed. Start a new request explicitly.", a.now())
		}
	}
}

type ProgramUse struct {
	PairingID  string   `json:"pairingId"`
	Name       string   `json:"name"`
	Selections []string `json:"selections"`
}

func (a *App) programUses(id string) []ProgramUse {
	out := []ProgramUse{}
	for _, p := range a.state.Pairings {
		if p.Revoked {
			continue
		}
		use := ProgramUse{PairingID: p.ID, Name: p.Name, Selections: []string{}}
		if p.Routing.Default != nil && p.Routing.Default.Provider == id {
			use.Selections = append(use.Selections, "Default")
		}
		for group, target := range p.Routing.Groups {
			if target.Provider == id {
				use.Selections = append(use.Selections, group)
			}
		}
		if len(use.Selections) == 0 && p.NativeAccounts[id] != "" {
			use.Selections = append(use.Selections, "Direct requests / previous execution")
		}
		if len(use.Selections) > 0 {
			sort.Strings(use.Selections)
			out = append(out, use)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PairingID < out[j].PairingID })
	return out
}
func (a *App) editProgram(p AIProgram) error {
	if err := validateProgram(p); err != nil {
		return err
	}
	old, exists := a.program(p.ID)
	if !exists && len(a.programList()) >= 32 {
		return fail(409, "program_limit", "At most 32 AI programs are supported.")
	}
	changed := !exists || old.hash() != p.hash()
	if err := a.update(func(s *State) error {
		if s.Programs == nil {
			s.Programs = defaultPrograms()
		}
		s.Programs[p.ID] = p
		if changed {
			a.invalidateProgram(s, p.ID, "program_changed")
		}
		return nil
	}); err != nil {
		return err
	}
	if changed {
		a.dropProgramStatus(p.ID)
	}
	return nil
}
func (a *App) dropProgramStatus(id string) {
	next := []NativeStatus{}
	for _, s := range a.native {
		if s.ID != id {
			next = append(next, s)
		}
	}
	a.native = next
}

func (a *App) knownProgramAccount(p *Pairing, id string) error {
	for _, status := range a.native {
		if status.ID != id || status.accountKey == "" {
			continue
		}
		expected := p.NativeAccounts[id]
		if expected != "" && expected != digest(a.state.DeviceID+":"+status.accountKey) {
			return fail(403, "native_account_changed", "This AI program is signed in to a different account. Disconnect and reconnect the affected application to approve it.")
		}
	}
	return nil
}

func (a *App) programRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("POST /api/program-defaults/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		program, ok := defaultPrograms()[id]
		if !ok {
			return fail(404, "not_found", "Default program not found.")
		}
		if _, exists := a.program(id); exists {
			return fail(409, "program_exists", "The default entry already exists. Edit or reset it instead.")
		}
		if err := a.editProgram(program); err != nil {
			return err
		}
		writeJSON(w, 201, program)
		return nil
	}))
	mux.HandleFunc("POST /api/programs", control(func(w http.ResponseWriter, r *http.Request) error {
		var p AIProgram
		if err := decode(w, r, &p); err != nil {
			return err
		}
		p.ID = "program-" + secret()[:24]
		if err := a.editProgram(p); err != nil {
			return err
		}
		writeJSON(w, 201, p)
		return nil
	}))
	mux.HandleFunc("PUT /api/programs/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		var p AIProgram
		if err := decode(w, r, &p); err != nil {
			return err
		}
		if p.ID != r.PathValue("id") {
			return fail(400, "invalid_program", "Program ID mismatch.")
		}
		old, ok := a.program(p.ID)
		if !ok {
			return fail(404, "not_found", "AI program not found.")
		}
		if expected := r.Header.Get("If-Match"); expected != "" && expected != old.hash() {
			return fail(409, "program_changed", "This entry changed in another panel. Reopen it before saving.")
		}
		if err := a.editProgram(p); err != nil {
			return err
		}
		writeJSON(w, 200, p)
		return nil
	}))
	mux.HandleFunc("GET /api/programs/{id}/uses", control(func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, 200, a.programUses(r.PathValue("id")))
		return nil
	}))
	mux.HandleFunc("DELETE /api/programs/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		if _, ok := a.program(id); !ok {
			return fail(404, "not_found", "AI program not found.")
		}
		if len(a.programUses(id)) > 0 && r.URL.Query().Get("leaveUnavailable") != "true" {
			return fail(409, "program_in_use", "Review affected selections and reassign them, or explicitly leave them unavailable.")
		}
		if err := a.update(func(s *State) error {
			delete(s.Programs, id)
			a.invalidateProgram(s, id, "program_removed")
			return nil
		}); err != nil {
			return err
		}
		a.dropProgramStatus(id)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
	mux.HandleFunc("POST /api/programs/{id}/check", control(func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		if _, ok := a.program(id); !ok {
			return fail(404, "not_found", "AI program not found.")
		}
		go a.checkPrograms(id)
		writeJSON(w, 202, map[string]bool{"checking": true})
		return nil
	}))
	mux.HandleFunc("PUT /api/pairings/{id}/program-grants", control(func(w http.ResponseWriter, r *http.Request) error {
		p := a.state.Pairings[r.PathValue("id")]
		if p == nil || p.Revoked {
			return fail(404, "not_found", "Connection not found.")
		}
		var in struct {
			Programs                   map[string]string `json:"programs"`
			CommandPermissionsAccepted bool              `json:"commandPermissionsAccepted"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if len(in.Programs) > 0 && !in.CommandPermissionsAccepted {
			return fail(400, "command_consent_required", "Custom AI programs run with your OS account's file and network permissions. Explicit consent is required.")
		}
		for id, hash := range in.Programs {
			program, ok := a.program(id)
			if !ok || program.Type != "custom" || program.hash() != hash {
				return fail(409, "program_changed", "Review the current custom program before granting access.")
			}
		}
		if err := a.update(func(s *State) error {
			previous := s.Pairings[p.ID].ProgramGrants
			changed := func(id string) bool { return previous[id] != in.Programs[id] }
			s.Pairings[p.ID].ProgramGrants = in.Programs
			for _, c := range s.Conversations {
				if c.PairingID == p.ID && c.Plan != nil && changed(c.Plan.Provider) && c.Status == "open" {
					c.Status = "closed"
					c.Code = "program_access_changed"
					c.UpdatedAt = a.now()
				}
			}
			for _, j := range s.Jobs {
				if j.PairingID == p.ID && changed(jobProvider(j)) && !terminal(j.Status) {
					finish(j, "cancelled", "program_access_changed", "Custom program access changed.", a.now())
				}
			}
			return nil
		}); err != nil {
			return err
		}

		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}))
}

// The custom protocol is deliberately small: text/streaming and optional
// continuing turns use the existing owned JSONL command session machinery.
type customProgramInfo struct {
	Protocol string        `json:"protocol"`
	Version  string        `json:"version"`
	Account  string        `json:"account,omitempty"`
	Sessions bool          `json:"sessions"`
	Models   []NativeModel `json:"models,omitempty"`
}

func inspectCustom(ctx context.Context, path, dir string) (customProgramInfo, error) {
	raw, err := nativeOutputContext(ctx, path, dir, "--wdc-info")
	var info customProgramInfo
	if err != nil || json.Unmarshal([]byte(raw), &info) != nil || info.Protocol != "wdc-ai-jsonl-v1" || len(info.Account) > 200 || len(info.Version) > 100 || len(info.Models) > 64 {
		return info, errors.New("Expected a wdc-ai-jsonl-v1 descriptor from --wdc-info.")
	}
	for _, m := range info.Models {
		if m.ID == "" || len(m.ID) > 128 || len(m.Name) > 128 || len(m.Reasoning) > 16 || strings.ContainsAny(m.ID, "\x00\r\n") {
			return info, errors.New("Invalid custom model catalog")
		}
		for _, r := range m.Reasoning {
			if r.Effort == "" || len(r.Effort) > 128 || strings.ContainsAny(r.Effort, "\x00\r\n") {
				return info, errors.New("Invalid custom reasoning catalog")
			}
		}
	}
	return info, nil
}
