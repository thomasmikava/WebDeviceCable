package bridge

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type ActionGroup struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind,omitempty"`
	Name      string   `json:"name"`
	ParentID  string   `json:"parentId,omitempty"`
	Operation string   `json:"operation,omitempty"`
	Hidden    bool     `json:"hidden,omitempty"`
	Replaces  []string `json:"replaces,omitempty"`
}
type ExecutionTarget struct {
	Provider  string `json:"provider,omitempty"`
	Action    string `json:"action,omitempty"`
	Model     string `json:"model,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	// Old targets replace the whole selection. New editors explicitly opt in to
	// inheriting unspecified options, preserving every existing saved route.
	InheritOptions   bool `json:"inheritOptions,omitempty"`
	ModelDefault     bool `json:"modelDefault,omitempty"`
	ReasoningDefault bool `json:"reasoningDefault,omitempty"`
}
type Routing struct {
	Revision uint64                     `json:"revision"`
	Default  *ExecutionTarget           `json:"default,omitempty"`
	Groups   map[string]ExecutionTarget `json:"groups,omitempty"`
}
type ExecutionPlan struct {
	ExecutionTarget
	ProgramHash string `json:"programHash,omitempty"`
	Kind        string `json:"kind"`
	ActionHash  string `json:"actionHash,omitempty"`
}
type ExecutionMetadata struct {
	ToolSetHash   string `json:"toolSetHash,omitempty"`
	ProgramID     string `json:"programId,omitempty"`
	Integration   string `json:"integration,omitempty"`
	Kind          string `json:"kind"`
	Name          string `json:"name,omitempty"`
	Version       string `json:"version,omitempty"`
	VersionSource string `json:"versionSource,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Reasoning     string `json:"reasoning,omitempty"`
}
type Sharing struct {
	Names     bool `json:"names"`
	Versions  bool `json:"versions"`
	Models    bool `json:"models"`
	RawErrors bool `json:"rawErrors"`
}

func defaultSharing() *Sharing { return &Sharing{Names: true, Versions: true, Models: true} }
func jobKind(j *Job) string {
	if j.Plan != nil {
		return j.Plan.Kind
	}
	return j.Request.Kind
}
func jobAction(j *Job) string {
	if j.Plan != nil {
		return j.Plan.Action
	}
	return j.Request.Action
}
func jobProvider(j *Job) string {
	if j.Plan != nil {
		return j.Plan.Provider
	}
	return j.Request.Provider
}
func groupExists(p *Pairing, id string) bool {
	for _, group := range p.Groups {
		if group.ID == id {
			return true
		}
	}
	return false
}
func (a *App) validateTarget(p *Pairing, target ExecutionTarget) error {
	if target.Action != "" {
		if target.Provider != "" || target.Model != "" || target.Reasoning != "" {
			return fail(400, "invalid_route", "Command routes cannot include native provider options.")
		}
		return a.authorizeAction(p, target.Action, "")
	}
	if _, ok := a.program(target.Provider); !ok {
		return fail(409, "program_unavailable", "The selected AI program is unavailable. Choose a program explicitly in this connection’s settings.")
	}
	for _, value := range []string{target.Model, target.Reasoning} {
		if len(value) > 128 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return fail(400, "invalid_route", "Model and reasoning identifiers must be short, single-line values.")
		}
	}
	return nil
}

func applyRoute(base, override ExecutionTarget) ExecutionTarget {
	if !override.InheritOptions || override.Action != "" || override.Provider != "" && override.Provider != base.Provider {
		base = ExecutionTarget{Provider: override.Provider, Action: override.Action}
	}
	if override.Provider != "" {
		base.Provider, base.Action = override.Provider, ""
	}
	if !override.InheritOptions || override.Model != "" || override.ModelDefault {
		base.Model = override.Model
	}
	if !override.InheritOptions || override.Reasoning != "" || override.ReasoningDefault {
		base.Reasoning = override.Reasoning
	}
	return base
}

func routingTarget(p *Pairing, group string) ExecutionTarget {
	target := ExecutionTarget{}
	if p.Routing.Default != nil {
		target = applyRoute(target, *p.Routing.Default)
	}
	var chain []string
	for id := group; id != ""; {
		chain = append(chain, id)
		parent := ""
		for _, item := range p.Groups {
			if item.ID == id {
				parent = item.ParentID
				break
			}
		}
		id = parent
		if len(chain) > 8 {
			break
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if override, ok := p.Routing.Groups[chain[i]]; ok {
			target = applyRoute(target, override)
		}
	}
	return target
}

func routingConfigured(p *Pairing) bool {
	if p.Routing.Default == nil {
		return false
	}
	target := routingTarget(p, "")
	return target.Provider != "" || target.Action != ""
}

// Reject known-incompatible local selections during admission, before a native
// turn is created. An unavailable model is never substituted silently.
func (a *App) validateNativeOptions(target ExecutionTarget) error {
	for _, native := range a.native {
		if native.ID != target.Provider || len(native.Models) == 0 || target.Model == "" {
			continue
		}
		for _, model := range native.Models {
			if model.ID != target.Model {
				continue
			}
			if target.Reasoning != "" && len(model.Reasoning) > 0 {
				for _, effort := range model.Reasoning {
					if effort.Effort == target.Reasoning {
						return nil
					}
				}
				return fail(409, "reasoning_unavailable", "Choose a reasoning level supported by this model in WDC program settings.")
			}
			return nil
		}
		if program, _ := a.program(target.Provider); program.Type == "codex" || program.Type == "custom" {
			return fail(409, "model_unavailable", "The selected local model is no longer available. Review this stage in WDC program settings.")
		}
	}
	return nil
}

func (a *App) resolvePlan(p *Pairing, request JobRequest) (*ExecutionPlan, error) {
	target := ExecutionTarget{Provider: request.Provider, Action: request.Action}
	if request.Group != "" && !groupExists(p, request.Group) {
		return nil, fail(400, "unknown_group", "Register this action group before using it.")
	}
	if request.Group != "" || request.Kind == "action" && target.Provider == "" && target.Action == "" {
		target = routingTarget(p, request.Group)
		if target.Provider == "" && target.Action == "" {
			return nil, fail(409, "routing_required", "Choose a default program for this app in WebDeviceCable, under Connections.")
		}
	}
	if request.Kind == "connection-test" {
		if request.Group != "" || request.ConversationID != "" {
			return nil, fail(400, "invalid_job", "Connection tests do not use groups or conversations.")
		}
		return &ExecutionPlan{Kind: "connection-test"}, nil
	}
	for _, group := range p.Groups {
		if group.ID == request.Group && group.Kind != "ai" && target.Provider != "" {
			return nil, fail(409, "command_route_required", "Choose a command for this group in WebDeviceCable Preferences.")
		}
	}
	if err := a.validateTarget(p, target); err != nil {
		return nil, err
	}
	if target.Provider != "" {
		if err := a.validateNativeOptions(target); err != nil {
			return nil, err
		}
	}
	plan := &ExecutionPlan{ExecutionTarget: target, Kind: request.Kind}
	if target.Action != "" {
		plan.Kind = "command"
		plan.ActionHash = a.actions[target.Action].Hash
	} else if request.Kind == "action" || request.Kind == "command" {
		plan.Kind = "text"
	}
	if target.Provider != "" {
		program, _ := a.program(target.Provider)
		plan.ProgramHash = program.hash()
	}
	program, _ := a.program(target.Provider)
	if plan.Kind != "text" && plan.Kind != "command" && program.Type != "codex" {
		return nil, fail(400, "invalid_job", "Image operations require Codex.")
	}
	return plan, nil
}
func planHash(plan *ExecutionPlan) string { data, _ := json.Marshal(plan); return digest(string(data)) }
func (a *App) prepareJob(p *Pairing, in *JobRequest, plan *ExecutionPlan) (*ExecutionPlan, string, error) {
	if plan == nil {
		var err error
		plan, err = a.resolvePlan(p, *in)
		if err != nil {
			return nil, "", err
		}
	}
	effective := *in
	effective.Kind = plan.Kind
	effective.Provider = plan.Provider
	effective.Action = plan.Action
	title := ""
	if plan.Kind == "command" {
		command := a.actions[plan.Action]
		if in.Prompt != "" && command.Session == nil {
			return nil, "", fail(400, "invalid_arguments", "This command accepts named arguments. Use a session-enabled command for prompt messages.")
		}
		effective.Provider = ""
		effective.Prompt = ""
		effective.Mode = "commands"
		in.Mode = "commands"
		values, e := command.inputs(in.Arguments)
		if e != nil {
			return nil, "", fail(400, "invalid_arguments", e.Error())
		}
		in.Arguments = values
		effective.Arguments = values
		title = command.Title
	} else {
		effective.Mode = in.Mode
		if effective.Mode == "" {
			effective.Mode = "restricted"
		}
		in.Mode = effective.Mode
	}
	if plan.Provider != "" {
		if err := a.checkProgramRequest(p, plan, effective); err != nil {
			return nil, "", err
		}
	}
	if err := validateRequest(&effective); err != nil {
		return nil, "", fail(400, "invalid_job", err.Error())
	}
	return plan, title, nil
}
func (a *App) remoteJob(j *Job) Job {
	c := publicJob(j)
	sharing := a.state.Sharing
	if sharing == nil {
		sharing = defaultSharing()
	}
	if c.Execution != nil {
		metadata := *c.Execution
		metadata.ToolSetHash = j.Request.ToolSetHash
		c.Execution = &metadata
		if !sharing.Names {
			metadata.Name = ""
			metadata.Provider = ""
			metadata.ProgramID = ""
			metadata.Integration = ""
		}
		if !sharing.Versions {
			metadata.Version = ""
			metadata.VersionSource = ""
		}
		if !sharing.Models {
			metadata.Model = ""
			metadata.Reasoning = ""
		}
		if metadata.Name == "" && metadata.Provider == "" && metadata.Version == "" && metadata.Model == "" && metadata.Reasoning == "" {
			c.Execution = nil
		}
	}
	if !sharing.Names {
		c.ActionTitle = ""
		c.ActualProvider = ""
	}
	if !sharing.Models {
		c.ActualModel = ""
	}
	if c.Error != nil {
		failure := *c.Error
		c.Error = &failure
		if !sharing.RawErrors {
			failure.Raw = ""
		}
	}
	if !sharing.Names && j.Plan != nil && j.Request.Kind == "action" && c.Status == "blocked" && jobKind(j) != "command" {
		c.Code = "execution_unavailable"
		c.Detail = "The selected native client is unavailable. Review its status in WebDeviceCable."
		c.Error = failureFor(0, c.Code, c.Detail)
	}
	if !sharing.Names && j.Plan != nil && jobKind(j) != "command" && jobKind(j) != "connection-test" && c.Error != nil {
		message := "The selected native client could not complete this request."
		switch c.Error.Category {
		case "authentication":
			message = "Sign in to the selected native client, then review Settings in WebDeviceCable."
		case "unavailable":
			message = "The selected native client is unavailable. Review its status in WebDeviceCable."
		case "rate_limit":
			message = "The selected native client’s usage limit has been reached."
		case "context_limit":
			message = "The session reached its context limit. Start a new session."
		case "permission":
			message = "The selected native client denied the request. Review Settings in WebDeviceCable."
		}
		c.Detail = message
		c.Error.Message = message
	}

	return c
}
func (a *App) remotePairing(p *Pairing) Pairing {
	c := publicPairing(p)
	c.RequestHistory = ""
	c.PermissionsRevision = 0
	c.PermissionSaveID = ""
	c.DisplayName = ""
	c.Folders = append([]FolderGrant(nil), p.Folders...)
	for i := range c.Folders {
		c.Folders[i].Path = ""
		c.Folders[i].Identity = ""
	}
	c.Routing = Routing{}
	c.Actions = nil
	c.ProgramGrants = nil
	return c
}
func (a *App) closeConversations(s *State, pairingID, code string, now time.Time) {
	for _, conversation := range s.Conversations {
		if (pairingID == "" || conversation.PairingID == pairingID) && conversation.Status == "open" {
			conversation.Status = "closed"
			conversation.Code = code
			conversation.UpdatedAt = now
		}
	}
}
func validateGroups(groups []ActionGroup) error {
	if len(groups) > 64 {
		return errors.New("an app can declare up to 64 groups and subgroups")
	}
	seen := map[string]bool{}
	for _, group := range groups {
		if group.Operation != "" && group.Operation != "text" && group.Operation != "image" || len(group.Replaces) > 8 {
			return errors.New("invalid group operation or migration aliases")
		}
		for _, id := range group.Replaces {
			if !actionID.MatchString(id) {
				return errors.New("invalid legacy group id")
			}
		}
		if (group.Kind != "" && group.Kind != "command" && group.Kind != "ai") || !actionID.MatchString(group.ID) || seen[group.ID] || strings.TrimSpace(group.Name) == "" || len(group.Name) > 100 || !utf8.ValidString(group.Name) || group.ParentID != "" && !actionID.MatchString(group.ParentID) {
			return errors.New("groups need unique short ids and names")
		}
		seen[group.ID] = true
	}
	return nil
}

func validateGroupTree(groups []ActionGroup) error {
	parents := map[string]string{}
	for _, group := range groups {
		parents[group.ID] = group.ParentID
	}
	for _, group := range groups {
		seen := map[string]bool{}
		depth := 0
		for id := group.ID; id != ""; {
			if seen[id] {
				return errors.New("group hierarchy cannot contain cycles")
			}
			parent, ok := parents[id]
			if !ok {
				return errors.New("a subgroup must name a declared parent group")
			}
			seen[id] = true
			depth++
			if depth > 8 {
				return errors.New("group hierarchy is limited to 8 levels")
			}
			id = parent
		}
	}
	return nil
}
