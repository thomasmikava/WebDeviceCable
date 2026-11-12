package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const starterConfig = `# Restart WebDeviceCable after changing this file.
bridge:
  allowWebCommandEditing: false

# OliveTin-compatible exec actions. See docs/commands.md for supported fields.
# Commands run with your OS account permissions. Each website needs a grant.
actions: []
`

type Config struct {
	Bridge struct {
		DisableNative          bool `yaml:"disableNative"`
		AllowWebCommandEditing bool `yaml:"allowWebCommandEditing"`
		MaxTextBytes           int  `yaml:"maxTextBytes"`
	} `yaml:"bridge"`
	Actions []Action `yaml:"actions"`
}
type Action struct {
	ID            string         `yaml:"id" json:"id"`
	Title         string         `yaml:"title" json:"title"`
	Exec          []string       `yaml:"exec" json:"exec"`
	Arguments     []Argument     `yaml:"arguments,omitempty" json:"arguments,omitempty"`
	Timeout       int            `yaml:"timeout,omitempty" json:"timeout"`
	MaxConcurrent int            `yaml:"maxConcurrent,omitempty" json:"maxConcurrent"`
	Version       string         `yaml:"version,omitempty" json:"version,omitempty"`
	Session       *ActionSession `yaml:"session,omitempty" json:"session,omitempty"`
	Files         bool           `yaml:"files,omitempty" json:"files,omitempty"`
}
type ActionSession struct {
	Protocol    string `yaml:"protocol" json:"protocol"`
	IdleTimeout int    `yaml:"idleTimeout,omitempty" json:"idleTimeout"`
}
type Argument struct {
	Name       string   `yaml:"name" json:"name"`
	Title      string   `yaml:"title,omitempty" json:"title,omitempty"`
	Type       string   `yaml:"type,omitempty" json:"type,omitempty"`
	Default    string   `yaml:"default,omitempty" json:"default,omitempty"`
	RejectNull bool     `yaml:"rejectNull,omitempty" json:"rejectNull,omitempty"`
	Choices    []Choice `yaml:"choices,omitempty" json:"choices,omitempty"`
}
type Choice struct {
	Title string `yaml:"title,omitempty" json:"title,omitempty"`
	Value string `yaml:"value" json:"value"`
}
type configuredAction struct {
	program    *AIProgram
	continuing bool
	Action
	Source string `json:"source"`
	Hash   string `json:"hash"`
}
type ActionCapability struct {
	ID        string     `json:"id"`
	Title     string     `json:"title,omitempty"`
	Arguments []Argument `json:"arguments"`
	Timeout   int        `json:"timeout"`
	Available bool       `json:"available"`
	Sessions  bool       `json:"sessions"`
}

var actionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var argumentName = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
var placeholder = regexp.MustCompile(`{{\s*(?:\.Arguments\.)?([A-Za-z0-9_]+)\s*}}`)
var argumentTypes = map[string]*regexp.Regexp{
	"ascii":            regexp.MustCompile(`^[A-Za-z0-9]*$`),
	"ascii_identifier": regexp.MustCompile(`^[A-Za-z0-9_.-]*$`),
	"ascii_sentence":   regexp.MustCompile(`^[A-Za-z0-9 ,_.-]*$`),
	"int":              regexp.MustCompile(`^[0-9]+$`),
	"url":              nil, "very_dangerous_raw_string": nil, "raw_string_multiline": nil,
}

// No aliases, multiple documents, ignored options, or automatic config reloads.
func parseConfig(data []byte) (Config, error) {
	var out Config
	if len(data) > 128<<10 {
		return out, errors.New("configuration exceeds 128 KiB")
	}
	var tree yaml.Node
	if e := yaml.Unmarshal(data, &tree); e != nil {
		return out, e
	}
	var inspect func(*yaml.Node) error
	inspect = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
			return errors.New("YAML aliases, anchors and merge keys are unsupported")
		}
		for _, c := range n.Content {
			if e := inspect(c); e != nil {
				return e
			}
		}
		return nil
	}
	if e := inspect(&tree); e != nil {
		return out, e
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if e := d.Decode(&out); e != nil {
		return out, e
	}
	if d.Decode(new(any)) != io.EOF {
		return out, errors.New("exactly one YAML document is required")
	}
	if len(out.Actions) > 64 {
		return out, errors.New("at most 64 commands are supported")
	}
	if out.Bridge.MaxTextBytes == 0 {
		out.Bridge.MaxTextBytes = 1 << 20
	}
	if out.Bridge.MaxTextBytes < 4096 || out.Bridge.MaxTextBytes > 2<<20 {
		return out, errors.New("bridge.maxTextBytes must be 4096–2097152")
	}
	for i := range out.Actions {
		if e := validateAction(&out.Actions[i]); e != nil {
			return out, fmt.Errorf("action %d: %w", i+1, e)
		}
	}
	return out, nil
}
func validateAction(a *Action) error {
	if len(a.Version) > 100 || !utf8.ValidString(a.Version) || strings.ContainsAny(a.Version, "\x00\r\n") {
		return errors.New("version must be a short, single-line value")
	}
	if a.Session != nil {
		if a.Session.Protocol != "jsonl" {
			return errors.New("session protocol must be jsonl")
		}
		if a.Session.IdleTimeout < 0 || a.Session.IdleTimeout > 600 {
			return errors.New("session idleTimeout must be 0–600 seconds; zero follows the requesting tab")
		}
	}
	if a.ID == "" {
		a.ID = "action-" + digest(a.Title)[:12]
	}
	if !actionID.MatchString(a.ID) || a.ID == "codex" || a.ID == "claude" || a.ID == "connection-test" {
		return errors.New("use a unique command id; built-in ids are reserved")
	}
	if strings.TrimSpace(a.Title) == "" || len(a.Title) > 100 || !utf8.ValidString(a.Title) {
		return errors.New("title must be 1–100 UTF-8 bytes")
	}
	if len(a.Exec) == 0 || len(a.Exec) > 32 || strings.TrimSpace(a.Exec[0]) == "" {
		return errors.New("exec must contain a program and at most 31 arguments; shell is unsupported")
	}
	if a.Timeout == 0 {
		a.Timeout = 3
	}
	if a.Timeout < 1 || a.Timeout > 600 {
		return errors.New("timeout must be 1–600 seconds")
	}
	if a.MaxConcurrent == 0 {
		a.MaxConcurrent = 1
	}
	if a.MaxConcurrent < 1 || a.MaxConcurrent > 4 {
		return errors.New("maxConcurrent must be 1–4")
	}
	if len(a.Arguments) > 16 {
		return errors.New("at most 16 inputs are supported")
	}
	names := map[string]bool{}
	for i := range a.Arguments {
		p := &a.Arguments[i]
		if !argumentName.MatchString(p.Name) || names[p.Name] {
			return errors.New("argument names must be unique letters, digits or underscores")
		}
		names[p.Name] = true
		if p.Type == "" {
			p.Type = "ascii"
		}
		if _, ok := argumentTypes[p.Type]; !ok {
			return fmt.Errorf("unsupported argument type %q", p.Type)
		}
		if len(p.Title) > 100 || !utf8.ValidString(p.Title) || len(p.Choices) > 32 {
			return errors.New("invalid argument title or too many choices")
		}
		choices := map[string]bool{}
		for _, c := range p.Choices {
			if choices[c.Value] || len(c.Title) > 100 || len(c.Value) > 2048 || !utf8.ValidString(c.Title) || !validInput(c.Value) {
				return errors.New("choices must have unique, bounded UTF-8 values")
			}
			choices[c.Value] = true
		}
		if p.Default != "" {
			if e := validateArgument(*p, p.Default); e != nil {
				return fmt.Errorf("invalid default for %s: %w", p.Name, e)
			}
		}
	}
	size := 0
	for i, v := range a.Exec {
		size += len(v)
		if !validInput(v) || size > 8192 {
			return errors.New("exec exceeds its size limit or contains invalid text")
		}
		matches := placeholder.FindAllStringSubmatch(v, -1)
		if a.Session != nil && len(matches) > 0 {
			return errors.New("session commands use static exec arguments; messages arrive on standard input")
		}
		if i == 0 && len(matches) > 0 {
			return errors.New("the executable cannot contain placeholders")
		}
		for _, m := range matches {
			if !names[m[1]] {
				return fmt.Errorf("unknown input %q", m[1])
			}
		}
		stripped := placeholder.ReplaceAllString(v, "")
		if strings.Contains(stripped, "{{") || strings.Contains(stripped, "}}") {
			return errors.New("only argument placeholders are supported")
		}
	}
	program := strings.TrimSuffix(strings.ToLower(filepath.Base(a.Exec[0])), ".exe")
	if program == "codex" || program == "claude" {
		return errors.New("use the built-in provider integration for Codex or Claude Code")
	}
	return nil
}
func validInput(v string) bool {
	return len(v) <= 8192 && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
}
func validateArgument(p Argument, value string) error {
	if !validInput(value) || len(value) > 2048 {
		return errors.New("input exceeds 2048 UTF-8 bytes or contains NUL")
	}
	if p.RejectNull && value == "" {
		return errors.New("input is required")
	}
	if len(p.Choices) > 0 {
		for _, c := range p.Choices {
			if value == c.Value {
				return nil
			}
		}
		return errors.New("choose a configured value")
	}
	if value == "" && !p.RejectNull {
		return nil
	}
	if pattern := argumentTypes[p.Type]; pattern != nil && !pattern.MatchString(value) {
		return errors.New("input does not match its configured type")
	}
	if p.Type == "url" {
		u, e := url.Parse(value)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("an HTTP or HTTPS URL is required")
		}
	}
	return nil
}
func (a Action) inputs(values map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range a.Arguments {
		v, ok := values[p.Name]
		if !ok {
			v = p.Default
		}
		if e := validateArgument(p, v); e != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, e)
		}
		out[p.Name] = v
	}
	total := 0
	for k, v := range values {
		if _, ok := out[k]; !ok {
			return nil, fmt.Errorf("unknown input %q", k)
		}
		total += len(v)
	}
	if total > 4096 {
		return nil, errors.New("combined inputs exceed 4096 bytes")
	}
	total = 0
	for _, v := range out {
		total += len(v)
	}
	if total > 4096 {
		return nil, errors.New("combined inputs exceed 4096 bytes")
	}
	return out, nil
}
func (a Action) argv(values map[string]string) []string {
	out := make([]string, len(a.Exec))
	for i, v := range a.Exec {
		out[i] = placeholder.ReplaceAllStringFunc(v, func(s string) string { return values[placeholder.FindStringSubmatch(s)[1]] })
	}
	return out
}
func readPrivate(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return nil, errors.New("configuration must be a regular file of at most 128 KiB")
	}
	// Windows synthesizes 0666 for writable files; POSIX mode bits do not
	// describe its ACLs. The private data directory must have a private ACL.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("configuration must not be group/world writable")
	}
	return ReadLocalFile(path, 128<<10)
}
func writePrivate(path string, data []byte) error { return writeLocalFile(path, data) }
func (a *App) loadConfig() error {
	path := filepath.Join(a.dir, "config.yaml")
	data, e := readPrivate(path)
	if errors.Is(e, os.ErrNotExist) {
		data = []byte(starterConfig)
		f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if createErr != nil {
			return createErr
		}
		_, e = f.Write(data)
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
	}
	if e != nil {
		return e
	}
	cfg, e := parseConfig(data)
	if e != nil {
		return fmt.Errorf("config.yaml: %w", e)
	}
	a.config = cfg
	a.actions = map[string]configuredAction{}
	for _, v := range cfg.Actions {
		if e = a.insertAction(v, "config.yaml"); e != nil {
			return e
		}
	}
	data, e = readPrivate(filepath.Join(a.dir, "actions.json"))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var managed []Action
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e = d.Decode(&managed); e != nil {
		return fmt.Errorf("actions.json: %w", e)
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("actions.json must contain one document")
	}
	for _, v := range managed {
		if e = validateAction(&v); e != nil {
			return fmt.Errorf("actions.json: %w", e)
		}
		if e = a.insertAction(v, "web"); e != nil {
			return e
		}
	}
	return nil
}
func (a *App) insertAction(v Action, source string) error {
	if _, ok := a.actions[v.ID]; ok {
		return fmt.Errorf("duplicate command id %q", v.ID)
	}
	if len(a.actions) >= 64 {
		return errors.New("at most 64 commands are supported")
	}
	for _, old := range a.actions {
		if old.Title == v.Title {
			return fmt.Errorf("duplicate command title %q", v.Title)
		}
	}
	data, _ := json.Marshal(v)
	a.actions[v.ID] = configuredAction{Action: v, Source: source, Hash: digest(string(data))}
	return nil
}
func (a *App) actionList() []configuredAction {
	out := []configuredAction{}
	for _, v := range a.actions {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (a *App) grants(requested map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for id, hash := range requested {
		v, ok := a.actions[id]
		if !ok {
			return nil, fail(400, "unknown_command", "Choose an available command.")
		}
		if hash != v.Hash {
			return nil, fail(409, "command_changed", "The command changed. Review its current definition before granting access.")
		}
		out[id] = v.Hash
	}
	return out, nil
}
func (a *App) actionCapabilities(p *Pairing) []ActionCapability {
	out := []ActionCapability{}
	for _, v := range a.actionList() {
		if hash, ok := p.Actions[v.ID]; ok {
			title := v.Title
			if a.state.Sharing != nil && !a.state.Sharing.Names {
				title = ""
			}
			out = append(out, ActionCapability{ID: v.ID, Title: title, Arguments: v.Arguments, Timeout: v.Timeout, Available: hash == v.Hash, Sessions: v.Session != nil})
		}
	}
	return out
}
func (a *App) authorizeAction(p *Pairing, id, hash string) error {
	v, ok := a.actions[id]
	if !ok {
		return fail(403, "command_unavailable", "This command is unavailable.")
	}
	if p.Mode != "commands" || p.Actions[id] != v.Hash {
		return fail(403, "command_not_granted", "Review this website's command access in WebDeviceCable.")
	}
	if hash != "" && hash != v.Hash {
		return fail(409, "command_changed", "The command changed. Submit a new request after reviewing access.")
	}
	return nil
}

// The editor never writes config.yaml, and cannot change its startup-only gate.
func (a *App) editAction(id string, replacement *Action) error {
	if !a.config.Bridge.AllowWebCommandEditing {
		return fail(403, "command_editing_disabled", "Command editing is disabled in config.yaml. Change the file and restart WebDeviceCable.")
	}
	if replacement != nil && replacement.Files {
		return fail(403, "file_managed_setting", "Enable files for a command in config.yaml, then restart.")
	}
	old, exists := a.actions[id]
	if exists && old.Source != "web" {
		return fail(403, "file_managed_command", "Edit this command in config.yaml and restart WebDeviceCable.")
	}
	if replacement == nil && !exists {
		return fail(404, "not_found", "Command not found.")
	}
	next := map[string]configuredAction{}
	for k, v := range a.actions {
		if k != id {
			next[k] = v
		}
	}
	if replacement != nil {
		v := *replacement
		if v.ID != id {
			return fail(400, "invalid_command", "Command id does not match.")
		}
		if e := validateAction(&v); e != nil {
			return fail(400, "invalid_command", e.Error())
		}
		if len(next) >= 64 {
			return fail(409, "command_limit", "At most 64 commands are supported.")
		}
		for _, other := range next {
			if other.Title == v.Title {
				return fail(409, "duplicate_command", "Choose a unique command title.")
			}
		}
		data, _ := json.Marshal(v)
		next[id] = configuredAction{Action: v, Source: "web", Hash: digest(string(data))}
	}
	managed := []Action{}
	for _, v := range next {
		if v.Source == "web" {
			managed = append(managed, v.Action)
		}
	}
	sort.Slice(managed, func(i, j int) bool { return managed[i].ID < managed[j].ID })
	data, e := json.Marshal(managed)
	if e != nil {
		return e
	}
	if e = writePrivate(filepath.Join(a.dir, "actions.json"), data); e != nil {
		return e
	}
	a.actions = next
	for conversationID, runtime := range a.conversations {
		if c := a.state.Conversations[conversationID]; c != nil && c.Plan != nil && c.Plan.Action == id {
			runtime.cancel()
		}
	}
	for jobID, run := range a.commands {
		j := findJob(&a.state, jobID)
		if j != nil && jobAction(j) == id {
			run.cancel()
		}
	}
	return a.update(func(s *State) error {
		for _, c := range s.Conversations {
			if c.Status == "open" && c.Plan != nil && c.Plan.Action == id {
				c.Status = "closed"
				c.Code = "command_changed"
				c.UpdatedAt = a.now()
			}
		}
		for _, j := range s.Jobs {
			if jobKind(j) == "command" && jobAction(j) == id && !terminal(j.Status) {
				finish(j, "cancelled", "command_changed", "The command definition changed.", a.now())
			}
		}
		return nil
	})
}
