package acp

import (
	"encoding/json"
	"strings"
)

// ProtocolVersion is the ACP major version this client speaks
// (https://agentclientprotocol.com/protocol/v1/initialization#protocol-version).
const ProtocolVersion = 1

// Method names (ACP v1 schema, https://agentclientprotocol.com/protocol/v1/schema).
const (
	MethodInitialize        = "initialize"
	MethodAuthenticate      = "authenticate"
	MethodSessionNew        = "session/new"
	MethodSessionLoad       = "session/load"
	MethodSessionResume     = "session/resume"
	MethodSessionPrompt     = "session/prompt"
	MethodSessionCancel     = "session/cancel"
	MethodSessionSetMode    = "session/set_mode"
	MethodSessionSetConfig  = "session/set_config_option"
	MethodSessionSetModel   = "session/set_model" // unstable ACP; only used when the agent reports a models block
	MethodSessionUpdate     = "session/update"
	MethodRequestPermission = "session/request_permission"
	MethodCancelRequest     = "$/cancel_request"
)

// Stop reasons (https://agentclientprotocol.com/protocol/v1/prompt-turn#stop-reasons).
const (
	StopEndTurn         = "end_turn"
	StopMaxTokens       = "max_tokens"
	StopMaxTurnRequests = "max_turn_requests"
	StopRefusal         = "refusal"
	StopCancelled       = "cancelled"
)

// Implementation identifies a client or agent.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// FileSystemCapability advertises fs/* client methods.
type FileSystemCapability struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

// ClientCapabilities advertised in initialize. Omitted capabilities are
// treated as unsupported by the agent.
type ClientCapabilities struct {
	FS       FileSystemCapability `json:"fs"`
	Terminal bool                 `json:"terminal"`
}

type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

type MCPCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

// SessionCapabilities holds optional session methods. Presence of an
// (even empty) object means support.
type SessionCapabilities struct {
	Resume *json.RawMessage `json:"resume,omitempty"`
	Close  *json.RawMessage `json:"close,omitempty"`
	List   *json.RawMessage `json:"list,omitempty"`
}

type AgentCapabilities struct {
	LoadSession         bool                 `json:"loadSession,omitempty"`
	PromptCapabilities  PromptCapabilities   `json:"promptCapabilities"`
	MCPCapabilities     MCPCapabilities      `json:"mcpCapabilities"`
	SessionCapabilities *SessionCapabilities `json:"sessionCapabilities,omitempty"`
	Meta                json.RawMessage      `json:"_meta,omitempty"`
}

// CanResume reports whether session/resume is advertised.
func (a AgentCapabilities) CanResume() bool {
	return a.SessionCapabilities != nil && present(a.SessionCapabilities.Resume)
}

func present(r *json.RawMessage) bool {
	return r != nil && len(*r) > 0 && string(*r) != "null"
}

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"` // "" / "agent" | "terminal"
}

type InitializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	AuthMethods       []AuthMethod      `json:"authMethods,omitempty"`
}

type AuthenticateParams struct {
	MethodID string `json:"methodId"`
}

// EnvVariable is a name/value pair for stdio MCP servers.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// MCPServerStdio is the stdio MCP transport every agent must support.
type MCPServerStdio struct {
	Name    string        `json:"name"`
	Command string        `json:"command"`
	Args    []string      `json:"args"`
	Env     []EnvVariable `json:"env"`
}

type NewSessionParams struct {
	Cwd        string           `json:"cwd"`
	MCPServers []MCPServerStdio `json:"mcpServers"`
}

type LoadSessionParams struct {
	SessionID  string           `json:"sessionId"`
	Cwd        string           `json:"cwd"`
	MCPServers []MCPServerStdio `json:"mcpServers"`
}

type SessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []SessionMode `json:"availableModes"`
}

// ConfigOptionValue is one selectable value.
type ConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ConfigOption is a session configuration selector. Select options may be
// flat or grouped on the wire; Values() flattens both forms.
type ConfigOption struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	Category     string          `json:"category,omitempty"`
	Type         string          `json:"type"`
	CurrentValue json.RawMessage `json:"currentValue"`
	Options      json.RawMessage `json:"options,omitempty"`
}

// Current returns the current value for select options ("" otherwise).
func (o ConfigOption) Current() string {
	var s string
	if json.Unmarshal(o.CurrentValue, &s) == nil {
		return s
	}
	return ""
}

// Values returns the flattened select values.
func (o ConfigOption) Values() []ConfigOptionValue {
	if len(o.Options) == 0 {
		return nil
	}
	var raw []json.RawMessage
	if json.Unmarshal(o.Options, &raw) != nil {
		return nil
	}
	var out []ConfigOptionValue
	for _, r := range raw {
		var probe struct {
			Group   *string             `json:"group"`
			Value   *string             `json:"value"`
			Name    string              `json:"name"`
			Desc    string              `json:"description"`
			Options []ConfigOptionValue `json:"options"`
		}
		if json.Unmarshal(r, &probe) != nil {
			continue
		}
		if probe.Group != nil && probe.Value == nil {
			out = append(out, probe.Options...)
			continue
		}
		if probe.Value != nil {
			out = append(out, ConfigOptionValue{Value: *probe.Value, Name: probe.Name, Description: probe.Desc})
		}
	}
	return out
}

// FindConfig returns the first option with the given category, falling back
// to an option whose id equals the category.
func FindConfig(opts []ConfigOption, category string) (ConfigOption, bool) {
	for _, o := range opts {
		if o.Category == category {
			return o, true
		}
	}
	for _, o := range opts {
		if o.ID == category {
			return o, true
		}
	}
	return ConfigOption{}, false
}

// ModelInfo and SessionModelState model the unstable ACP "models" block some
// agents (including Cursor, per community reports) return from session/new.
type ModelInfo struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionModelState struct {
	CurrentModelID  string      `json:"currentModelId"`
	AvailableModels []ModelInfo `json:"availableModels"`
}

// SessionSetup is the common shape of session/new, session/load and
// session/resume results.
type SessionSetup struct {
	SessionID     string             `json:"sessionId,omitempty"`
	Modes         *SessionModeState  `json:"modes,omitempty"`
	ConfigOptions []ConfigOption     `json:"configOptions,omitempty"`
	Models        *SessionModelState `json:"models,omitempty"`
}

type SetModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type SetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type SetConfigOptionResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

type SetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

// ContentBlock is a prompt/content block. Only text is produced by yip.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URI  string `json:"uri,omitempty"`
	Name string `json:"name,omitempty"`
}

// TextBlock builds a text content block.
func TextBlock(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// TurnUsage is the draft end-of-turn token usage
// (https://agentclientprotocol.com/rfds/end-turn-token-usage). Optional.
type TurnUsage struct {
	TotalTokens       *int64 `json:"totalTokens,omitempty"`
	InputTokens       *int64 `json:"inputTokens,omitempty"`
	OutputTokens      *int64 `json:"outputTokens,omitempty"`
	ThoughtTokens     *int64 `json:"thoughtTokens,omitempty"`
	CachedReadTokens  *int64 `json:"cachedReadTokens,omitempty"`
	CachedWriteTokens *int64 `json:"cachedWriteTokens,omitempty"`
}

type PromptResult struct {
	StopReason string     `json:"stopReason"`
	Usage      *TurnUsage `json:"usage,omitempty"`
}

type CancelParams struct {
	SessionID string `json:"sessionId"`
}

type CancelRequestParams struct {
	RequestID json.RawMessage `json:"requestId"`
}

// Key returns the normalised request ID of a $/cancel_request.
func (p CancelRequestParams) Key() string { return idKey(p.RequestID) }

// ToolCallLocation is a file a tool call touches.
type ToolCallLocation struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

// ToolCallContent is content produced by a tool call (content, diff or terminal).
type ToolCallContent struct {
	Type       string          `json:"type"`
	Content    json.RawMessage `json:"content,omitempty"`
	Path       string          `json:"path,omitempty"`
	OldText    *string         `json:"oldText,omitempty"`
	NewText    string          `json:"newText,omitempty"`
	TerminalID string          `json:"terminalId,omitempty"`
}

// ToolCall covers both tool_call and tool_call_update payloads; every field
// other than ToolCallID is optional on updates.
type ToolCall struct {
	ToolCallID string             `json:"toolCallId"`
	Name       string             `json:"name,omitempty"`
	Title      string             `json:"title,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Status     string             `json:"status,omitempty"`
	Content    []ToolCallContent  `json:"content,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`
}

// Merge applies the non-empty fields of u onto t.
func (t *ToolCall) Merge(u ToolCall) {
	if u.Name != "" {
		t.Name = u.Name
	}
	if u.Title != "" {
		t.Title = u.Title
	}
	if u.Kind != "" {
		t.Kind = u.Kind
	}
	if u.Status != "" {
		t.Status = u.Status
	}
	if u.Content != nil {
		t.Content = u.Content
	}
	if u.Locations != nil {
		t.Locations = u.Locations
	}
	if len(u.RawInput) > 0 && string(u.RawInput) != "null" {
		t.RawInput = u.RawInput
	}
	if len(u.RawOutput) > 0 && string(u.RawOutput) != "null" {
		t.RawOutput = u.RawOutput
	}
}

// Tool kinds (https://agentclientprotocol.com/protocol/v1/tool-calls#creating).
const (
	KindRead       = "read"
	KindEdit       = "edit"
	KindDelete     = "delete"
	KindMove       = "move"
	KindSearch     = "search"
	KindExecute    = "execute"
	KindThink      = "think"
	KindFetch      = "fetch"
	KindSwitchMode = "switch_mode"
	KindOther      = "other"
)

// Tool call statuses.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// SessionUpdate is the discriminated union carried by session/update. Only
// the fields for the variant named in SessionUpdate are populated.
type SessionUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`

	// *_message_chunk / agent_thought_chunk
	MessageID string        `json:"messageId,omitempty"`
	Content   *ContentBlock `json:"-"`

	// tool_call / tool_call_update
	ToolCall

	// plan
	Entries []PlanEntry `json:"entries,omitempty"`

	// current_mode_update
	CurrentModeID string `json:"currentModeId,omitempty"`

	// config_option_update
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`

	// usage_update
	Used *int64 `json:"used,omitempty"`
	Size *int64 `json:"size,omitempty"`
	Cost *Cost  `json:"cost,omitempty"`

	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON handles the "content" field, which is a single ContentBlock
// for message chunks but an array for tool calls.
func (u *SessionUpdate) UnmarshalJSON(b []byte) error {
	type plain SessionUpdate
	var aux struct {
		plain
		Content json.RawMessage `json:"content,omitempty"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*u = SessionUpdate(aux.plain)
	u.Raw = append(json.RawMessage(nil), b...)
	c := strings.TrimSpace(string(aux.Content))
	switch {
	case c == "" || c == "null":
	case c[0] == '{':
		var cb ContentBlock
		if err := json.Unmarshal(aux.Content, &cb); err == nil {
			u.Content = &cb
		}
	case c[0] == '[':
		var tc []ToolCallContent
		if err := json.Unmarshal(aux.Content, &tc); err == nil {
			u.ToolCall.Content = tc
		}
	}
	return nil
}

type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// PermissionOption kinds.
const (
	AllowOnce    = "allow_once"
	AllowAlways  = "allow_always"
	RejectOnce   = "reject_once"
	RejectAlways = "reject_always"
)

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCall           `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// PermissionOutcome is the RequestPermissionOutcome union.
type PermissionOutcome struct {
	Outcome  string `json:"outcome"` // "selected" | "cancelled"
	OptionID string `json:"optionId,omitempty"`
}

type RequestPermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// Selected builds a "selected" permission reply.
func Selected(optionID string) RequestPermissionResult {
	return RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "selected", OptionID: optionID}}
}

// CancelledPermission builds the "cancelled" permission reply required when
// a prompt turn is cancelled.
func CancelledPermission() RequestPermissionResult {
	return RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "cancelled"}}
}
