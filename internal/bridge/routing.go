package bridge

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type ActionGroup struct {
	ID       string `json:"id"`
	Kind     string `json:"kind,omitempty"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
}
type ExecutionTarget struct {
	Provider  string `json:"provider,omitempty"`
	Action    string `json:"action,omitempty"`
	Model     string `json:"model,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}
type Routing struct {
	Revision uint64                     `json:"revision"`
	Default  *ExecutionTarget           `json:"default,omitempty"`
	Groups   map[string]ExecutionTarget `json:"groups,omitempty"`
}
type ExecutionPlan struct {
	ExecutionTarget
	Kind       string `json:"kind"`
	ActionHash string `json:"actionHash,omitempty"`
}
type ExecutionMetadata struct {
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
	if target.Provider != "codex" && target.Provider != "claude" {
		return fail(400, "invalid_route", "Choose a native client or an allowed command.")
	}
	for _, value := range []string{target.Model, target.Reasoning} {
		if len(value) > 128 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return fail(400, "invalid_route", "Model and reasoning identifiers must be short, single-line values.")
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
		if p.Routing.Default != nil {
			target = *p.Routing.Default
		}
		for id := request.Group; id != ""; {
			if override, ok := p.Routing.Groups[id]; ok {
				target = override
				break
			}
			parent := ""
			for _, group := range p.Groups {
				if group.ID == id {
					parent = group.ParentID
					break
				}
			}
			id = parent
		}
		if target.Provider == "" && target.Action == "" {
			return nil, fail(409, "routing_required", "Choose this app’s default in Connections, then Routing.")
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
	plan := &ExecutionPlan{ExecutionTarget: target, Kind: request.Kind}
	if target.Action != "" {
		plan.Kind = "command"
		plan.ActionHash = a.actions[target.Action].Hash
	} else if request.Kind == "action" || request.Kind == "command" {
		plan.Kind = "text"
	}
	if plan.Kind != "text" && plan.Kind != "command" && target.Provider != "codex" {
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
		c.Execution = &metadata
		if !sharing.Names {
			metadata.Name = ""
			metadata.Provider = ""
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
	c.Folders = append([]FolderGrant(nil), p.Folders...)
	for i := range c.Folders {
		c.Folders[i].Path = ""
		c.Folders[i].Identity = ""
	}
	c.Routing = Routing{}
	c.Actions = nil
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
