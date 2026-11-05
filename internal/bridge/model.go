package bridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const LeaseTTL = 12 * time.Second
const maxJobs = 500

// Provider-backed execution stays disabled until eligibility AND isolation are verified.
// A UI checkbox or application request cannot override these release gates.
type Capability struct {
	Integration     string `json:"integration,omitempty"`
	Sessions        bool   `json:"sessions"`
	Streaming       bool   `json:"streaming"`
	Isolation       string `json:"isolation,omitempty"`
	AppTools        bool   `json:"appTools"`
	Workspace       bool   `json:"workspace"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Execution       bool   `json:"execution"`
	NativeInterface string `json:"nativeInterface"`
	Eligibility     string `json:"eligibility"`
	Reason          string `json:"reason"`
	Source          string `json:"source"`
	Text            bool   `json:"text"`
	JSON            bool   `json:"json"`
	ImageInput      bool   `json:"imageInput"`
	DocumentInput   bool   `json:"documentInput"`
	ImageGeneration bool   `json:"imageGeneration"`
	ImageEditing    bool   `json:"imageEditing"`
}

func Capabilities() []Capability {
	return []Capability{
		{ID: "codex", Name: "Codex", NativeInterface: "installed codex app-server (stdio)", Eligibility: "Documented for local/open-source clients; commercial/hosted use is excluded.", Reason: "Install a compatible Codex client and sign in there with ChatGPT. Execution uses an isolated local app-server session; provider distribution conditions apply.", Source: "https://learn.chatgpt.com/docs/app-server#auth-endpoints"},
		{ID: "claude", Name: "Claude Code", NativeInterface: "installed claude -p (stream-json)", Eligibility: "User-controlled local invocation of the unmodified CLI; provider terms apply.", Reason: "Install a compatible Claude Code client and sign in there with your personal subscription. WDC uses a private, tool-free native session.", Source: "https://code.claude.com/docs/en/headless"},
	}
}

type Pairing struct {
	GroupsDeclared  bool               `json:"groupsDeclared,omitempty"`
	Relay           *RelayBinding      `json:"relay,omitempty"`
	Transport       string             `json:"transport,omitempty"`
	Tools           map[string]AppTool `json:"tools,omitempty"`
	Folders         []FolderGrant      `json:"folders,omitempty"`
	ID              string             `json:"id"`
	AppID           string             `json:"appId"`
	Name            string             `json:"name"`
	Origin          string             `json:"origin"`
	AppUser         string             `json:"appUser"`
	DeviceID        string             `json:"deviceId"`
	ClientPublicKey string             `json:"clientPublicKey,omitempty"`
	CredentialHash  string             `json:"credentialHash,omitempty"`
	Automatic       bool               `json:"automatic"`
	Paused          bool               `json:"paused"`
	Revoked         bool               `json:"revoked"`
	CreatedAt       time.Time          `json:"createdAt"`
	Mode            string             `json:"mode"`
	ProgramGrants   map[string]string  `json:"programGrants,omitempty"`
	NativeAccounts  map[string]string  `json:"nativeAccounts,omitempty"`
	Actions         map[string]string  `json:"actions,omitempty"`
	Groups          []ActionGroup      `json:"groups,omitempty"`
	Routing         Routing            `json:"routing,omitempty"`
}
type Invitation struct {
	Pairing    Pairing   `json:"pairing"`
	SecretHash string    `json:"secretHash"`
	ExpiresAt  time.Time `json:"expiresAt"`
}
type JobRequest struct {
	Tools          []AppTool         `json:"tools,omitempty"`
	ToolSetHash    string            `json:"toolSetHash,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey"`
	Kind           string            `json:"kind"`
	Provider       string            `json:"provider,omitempty"`
	Prompt         string            `json:"prompt,omitempty"`
	Mode           string            `json:"mode"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
	Action         string            `json:"action,omitempty"`
	Arguments      map[string]string `json:"arguments,omitempty"`
	Group          string            `json:"group,omitempty"`
	ConversationID string            `json:"conversationId,omitempty"`
	OutputSchema   json.RawMessage   `json:"outputSchema,omitempty"`
	Inputs         []string          `json:"inputs,omitempty"`
}
type Job struct {
	InputNames      []string           `json:"inputNames,omitempty"`
	Turn            int                `json:"turn,omitempty"`
	ID              string             `json:"id"`
	PairingID       string             `json:"pairingId"`
	Request         JobRequest         `json:"request"`
	RequestHash     string             `json:"requestHash,omitempty"`
	LeaseID         string             `json:"leaseId,omitempty"`
	Status          string             `json:"status"`
	Code            string             `json:"code,omitempty"`
	Detail          string             `json:"detail,omitempty"`
	Output          string             `json:"output,omitempty"`
	JSON            json.RawMessage    `json:"json,omitempty"`
	Artifacts       []Artifact         `json:"artifacts,omitempty"`
	Usage           *TokenUsage        `json:"usage,omitempty"`
	ActualProvider  string             `json:"actualProvider,omitempty"`
	ActualModel     string             `json:"actualModel,omitempty"`
	Billing         string             `json:"billing"`
	Revision        uint64             `json:"revision"`
	CreatedAt       time.Time          `json:"createdAt"`
	StartedAt       *time.Time         `json:"startedAt,omitempty"`
	FinishedAt      *time.Time         `json:"finishedAt,omitempty"`
	ActionHash      string             `json:"actionHash,omitempty"`
	ActionTitle     string             `json:"actionTitle,omitempty"`
	ExitCode        *int               `json:"exitCode,omitempty"`
	OutputTruncated bool               `json:"outputTruncated,omitempty"`
	Plan            *ExecutionPlan     `json:"plan,omitempty"`
	Execution       *ExecutionMetadata `json:"execution,omitempty"`
	Error           *Failure           `json:"error,omitempty"`
	Diagnostic      string             `json:"diagnostic,omitempty"`
}
type Receipt struct {
	LeaseHash   string `json:"leaseHash,omitempty"`
	ToolSetHash string `json:"toolSetHash,omitempty"`
	JobID       string `json:"jobId"`
	Hash        string `json:"hash"`
}
type State struct {
	Programs      map[string]AIProgram     `json:"programs"`
	IdentityKey   string                   `json:"identityKey,omitempty"`
	SecurityReset bool                     `json:"securityReset,omitempty"`
	Version       int                      `json:"version"`
	DeviceID      string                   `json:"deviceId"`
	Enabled       bool                     `json:"enabled"`
	Revision      uint64                   `json:"revision"`
	Pairings      map[string]*Pairing      `json:"pairings"`
	Invitations   map[string]*Invitation   `json:"invitations"`
	Jobs          []*Job                   `json:"jobs"`
	Receipts      map[string]Receipt       `json:"receipts,omitempty"`
	Sharing       *Sharing                 `json:"sharing,omitempty"`
	Conversations map[string]*Conversation `json:"conversations"`
}
type Lease struct {
	PairingID string
	ExpiresAt time.Time
}

func secret() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func digest(s string) string          { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func requestHash(r JobRequest) string { b, _ := json.Marshal(r); return digest(string(b)) }
func terminal(s string) bool {
	switch s {
	case "completed", "cancelled", "timed_out", "blocked", "interrupted", "failed":
		return true
	}
	return false
}
func finish(j *Job, status, code, detail string, now time.Time) {
	j.Status = status
	j.Code = code
	j.Detail = detail
	j.FinishedAt = &now
	j.Request.Prompt = ""
	j.Request.Arguments = nil
	j.Request.OutputSchema = nil
	j.Request.Inputs = nil
	j.Request.Tools = nil
	j.InputNames = nil
	if status != "completed" {
		j.Error = failureFor(0, code, detail)
		if j.Diagnostic != "" {
			j.Error.Raw = cleanDiagnostic(j.Diagnostic)
		}
	}
	j.Revision++
}
func publicJob(j *Job) Job {
	c := *j
	c.RequestHash = ""
	c.Request.Tools = nil
	c.ActionHash = ""
	c.LeaseID = ""
	c.Plan = nil
	c.Diagnostic = ""
	return c
}
func publicPairing(p *Pairing) Pairing {
	c := *p
	c.Relay = nil
	c.Tools = nil
	c.CredentialHash = ""
	c.ClientPublicKey = ""
	c.NativeAccounts = nil
	return c
}

func pairingLeaseTTL(p *Pairing) time.Duration {
	if p.Transport == "relay" {
		return 45 * time.Second
	}
	return LeaseTTL
}
func exactOrigin(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Host == "" || u.Opaque != "" {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	if u.Scheme == "https" {
		return u.String() == raw
	}
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return (host == "localhost" || ip != nil && ip.IsLoopback()) && u.String() == raw
}
func validatePairing(p Pairing) error {
	if len(p.AppID) < 1 || len(p.AppID) > 100 || len(p.Name) < 1 || len(p.Name) > 100 || len(p.AppUser) < 1 || len(p.AppUser) > 200 {
		return errors.New("appId, name and appUser are required (100/100/200 character limits)")
	}
	if !exactOrigin(p.Origin) {
		return errors.New("origin must be an exact HTTPS origin or loopback HTTP origin, without a path")
	}
	if p.Mode != "restricted" && p.Mode != "commands" {
		return errors.New("connection modes are restricted or commands; request app-tools/workspace on jobs after granting access")
	}
	if (p.Mode == "commands") != (len(p.Actions) > 0) {
		return errors.New("command access requires explicit command grants")
	}
	return nil
}
func validateRequest(r *JobRequest) error {
	if e := checkOutputSchema(r.OutputSchema); e != nil {
		return e
	}
	if len(r.OutputSchema) > 0 && (r.Kind == "command" || r.Kind == "connection-test") {
		return errors.New("this job kind does not accept AI inputs or schemas")
	}
	if len(r.Inputs) > 8 {
		return errors.New("at most 8 supplied inputs are allowed")
	}
	if r.Group != "" && !actionID.MatchString(r.Group) {
		return errors.New("group must be a short action-group id")
	}
	if r.ConversationID != "" && (len(r.ConversationID) != 64 || strings.Trim(r.ConversationID, "0123456789abcdef") != "") {
		return errors.New("invalid conversation id")
	}
	if len(r.IdempotencyKey) < 8 || len(r.IdempotencyKey) > 128 || strings.TrimSpace(r.IdempotencyKey) != r.IdempotencyKey {
		return errors.New("idempotencyKey must be 8–128 characters")
	}
	if r.Mode == "" {
		r.Mode = "restricted"
		if r.Kind == "command" {
			r.Mode = "commands"
		}
	}
	if r.Kind == "connection-test" && r.Mode != "restricted" {
		return errors.New("connection tests use Restricted mode")
	}
	if r.Mode != "restricted" && r.Mode != "app-tools" && r.Mode != "workspace" && !(r.Kind == "command" && r.Mode == "commands") {
		return errors.New("unsupported security mode; only Restricted is implemented")
	}
	if r.TimeoutSeconds == 0 {
		r.TimeoutSeconds = 120
	}
	if r.TimeoutSeconds < 1 || r.TimeoutSeconds > 600 {
		return errors.New("timeoutSeconds must be 1–600")
	}
	if r.Kind != "command" && r.Kind != "action" && (r.Action != "" || len(r.Arguments) > 0) {
		return errors.New("action and arguments are only accepted for command jobs")
	}
	switch r.Kind {
	case "action":
		if r.Provider != "" || r.Action != "" {
			return errors.New("group actions use the route chosen in WebDeviceCable")
		}
		if len(r.Prompt) > 32768 {
			return errors.New("prompt exceeds 32768 bytes")
		}
	case "command":
		if r.Mode != "commands" {
			return errors.New("command jobs require commands mode; they are not sandboxed Restricted jobs")
		}
		if !actionID.MatchString(r.Action) || r.Provider != "" || r.Prompt != "" {
			return errors.New("command jobs require an action id and accept no provider or prompt")
		}
	case "connection-test":
		if r.Provider != "" || r.Prompt != "" {
			return errors.New("connection-test accepts no provider or prompt")
		}
	case "text", "image-generate", "image-edit":
		if !actionID.MatchString(r.Provider) {
			return errors.New("provider must be a program ID")
		}
		if len(strings.TrimSpace(r.Prompt)) == 0 || len(r.Prompt) > 32768 {
			return errors.New("prompt must be 1–32768 bytes")
		}
	default:
		return fmt.Errorf("unsupported job kind: %s", r.Kind)
	}
	return nil
}
