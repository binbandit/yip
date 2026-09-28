package codex

// Hand-written Go types for the subset of the Codex app-server v2 protocol
// this adapter uses. They mirror the JSON schema committed under schema/,
// which was generated from the pinned binary (see docs/providers/codex.md):
//
//	codex app-server generate-json-schema --out internal/providers/codex/schema
//
// Only fields the adapter reads or writes are declared; unknown fields are
// ignored on decode and optional fields are omitted on encode. The schema
// test (schema_test.go) checks every method name and field used here against
// the committed schema so drift is caught when the pinned version changes.

import "encoding/json"

// JSON-RPC methods (client → server requests).
const (
	methodInitialize       = "initialize"
	methodThreadStart      = "thread/start"
	methodThreadResume     = "thread/resume"
	methodTurnStart        = "turn/start"
	methodTurnSteer        = "turn/steer"
	methodTurnInterrupt    = "turn/interrupt"
	methodAccountRead      = "account/read"
	methodRateLimitsRead   = "account/rateLimits/read"
	methodModelList        = "model/list"
	methodConfigRead       = "config/read"
	methodHooksList        = "hooks/list"
	notifyInitialized      = "initialized"
	clientName             = "yip"
	clientTitle            = "yip"
	jsonrpcMethodNotFound  = -32601
	jsonrpcInvalidRequest  = -32600
	jsonrpcServerOverload  = -32001
	defaultNotFoundMessage = "method not supported by the yip client"
)

// Server → client requests.
const (
	reqCommandApproval    = "item/commandExecution/requestApproval"
	reqFileChangeApproval = "item/fileChange/requestApproval"
	reqPermissions        = "item/permissions/requestApproval"
	reqUserInput          = "item/tool/requestUserInput"
	reqElicitation        = "mcpServer/elicitation/request"
	reqDynamicToolCall    = "item/tool/call"
	reqAuthRefresh        = "account/chatgptAuthTokens/refresh"
	reqAttestation        = "attestation/generate"
	reqLegacyApplyPatch   = "applyPatchApproval"
	reqLegacyExecCommand  = "execCommandApproval"
)

// Server → client notifications.
const (
	ntfError             = "error"
	ntfThreadStarted     = "thread/started"
	ntfThreadStatus      = "thread/status/changed"
	ntfTokenUsage        = "thread/tokenUsage/updated"
	ntfTurnStarted       = "turn/started"
	ntfTurnCompleted     = "turn/completed"
	ntfTurnPlan          = "turn/plan/updated"
	ntfItemStarted       = "item/started"
	ntfItemCompleted     = "item/completed"
	ntfAgentDelta        = "item/agentMessage/delta"
	ntfRequestResolved   = "serverRequest/resolved"
	ntfMCPStartup        = "mcpServer/startupStatus/updated"
	ntfRateLimits        = "account/rateLimits/updated"
	ntfWarning           = "warning"
	ntfConfigWarning     = "configWarning"
	ntfGuardianWarning   = "guardianWarning"
	ntfDeprecation       = "deprecationNotice"
	ntfHookStarted       = "hook/started"
	ntfModelRerouted     = "model/rerouted"
	ntfAccountUpdated    = "account/updated"
	ntfThreadClosed      = "thread/closed"
	ntfModelVerification = "model/verification"
)

// Enumerations used on the wire.
const (
	sandboxReadOnly       = "read-only"       // SandboxMode
	sandboxWorkspaceWrite = "workspace-write" // SandboxMode

	approvalOnRequest     = "on-request" // AskForApproval
	approvalNever         = "never"
	approvalsReviewerUser = "user" // ApprovalsReviewer: approvals go to the client, not an AI reviewer

	turnCompleted   = "completed"
	turnInterrupted = "interrupted"
	turnFailed      = "failed"
	turnInProgress  = "inProgress"

	decisionAccept  = "accept"
	decisionDecline = "decline"

	errUsageLimitExceeded = "usageLimitExceeded"
	errUnauthorized       = "unauthorized"
	errServerOverloaded   = "serverOverloaded"

	accountAPIKey    = "apiKey"
	accountChatGPT   = "chatgpt"
	accountBedrock   = "amazonBedrock"
	hookTrustManaged = "managed"
	hookTrustTrusted = "trusted"
)

// ---- JSON-RPC envelope (the "jsonrpc":"2.0" field is omitted on the wire).

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

// ---- initialize

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

type initializeCapabilities struct {
	ExperimentalAPI bool `json:"experimentalApi"`
}

type initializeParams struct {
	ClientInfo   clientInfo              `json:"clientInfo"`
	Capabilities *initializeCapabilities `json:"capabilities,omitempty"`
}

type initializeResponse struct {
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
	UserAgent      string `json:"userAgent"`
}

// ---- account

type getAccountParams struct {
	RefreshToken bool `json:"refreshToken"`
}

type account struct {
	Type     string  `json:"type"` // apiKey | chatgpt | amazonBedrock
	Email    *string `json:"email,omitempty"`
	PlanType string  `json:"planType,omitempty"`
}

type getAccountResponse struct {
	Account            *account `json:"account"`
	RequiresOpenaiAuth bool     `json:"requiresOpenaiAuth"`
}

type rateLimitWindow struct {
	UsedPercent        int    `json:"usedPercent"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"` // unix seconds
}

type rateLimitSnapshot struct {
	LimitID              *string          `json:"limitId"`
	Primary              *rateLimitWindow `json:"primary"`
	Secondary            *rateLimitWindow `json:"secondary"`
	RateLimitReachedType *string          `json:"rateLimitReachedType"`
}

type accountRateLimitsUpdated struct {
	RateLimits rateLimitSnapshot `json:"rateLimits"`
}

type getAccountRateLimitsResponse struct {
	RateLimits          rateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID map[string]rateLimitSnapshot `json:"rateLimitsByLimitId"`
}

// ---- models

type modelListParams struct {
	Cursor *string `json:"cursor,omitempty"`
	Limit  *uint32 `json:"limit,omitempty"`
}

type modelInfo struct {
	ID          string `json:"id"`
	Model       string `json:"model"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden"`
	IsDefault   bool   `json:"isDefault"`
}

type modelListResponse struct {
	Data       []modelInfo `json:"data"`
	NextCursor *string     `json:"nextCursor"`
}

// ---- startup inspection

type configReadParams struct {
	Cwd           string `json:"cwd,omitempty"`
	IncludeLayers bool   `json:"includeLayers"`
}

type configLayerSource struct {
	Type           string `json:"type"`
	File           string `json:"file,omitempty"`
	DotCodexFolder string `json:"dotCodexFolder,omitempty"`
}

type configLayer struct {
	Name           configLayerSource `json:"name"`
	Config         json.RawMessage   `json:"config"`
	DisabledReason *string           `json:"disabledReason"`
}

type configReadResponse struct {
	Config json.RawMessage `json:"config"`
	Layers []configLayer   `json:"layers"`
}

type hooksListParams struct {
	Cwds []string `json:"cwds"`
}

type hookMetadata struct {
	Key         string  `json:"key"`
	EventName   string  `json:"eventName"`
	Command     *string `json:"command"`
	Enabled     bool    `json:"enabled"`
	IsManaged   bool    `json:"isManaged"`
	Source      string  `json:"source"`
	SourcePath  string  `json:"sourcePath"`
	TrustStatus string  `json:"trustStatus"`
}

type hooksListEntry struct {
	Cwd      string         `json:"cwd"`
	Hooks    []hookMetadata `json:"hooks"`
	Warnings []string       `json:"warnings"`
}

type hooksListResponse struct {
	Data []hooksListEntry `json:"data"`
}

// ---- threads and turns

// threadParams is the shared shape of ThreadStartParams and
// ThreadResumeParams (resume adds threadId).
type threadParams struct {
	ThreadID              string         `json:"threadId,omitempty"`
	Cwd                   string         `json:"cwd,omitempty"`
	Model                 string         `json:"model,omitempty"`
	ApprovalPolicy        any            `json:"approvalPolicy,omitempty"`    // AskForApproval
	ApprovalsReviewer     string         `json:"approvalsReviewer,omitempty"` // ApprovalsReviewer
	Sandbox               string         `json:"sandbox,omitempty"`           // SandboxMode
	DeveloperInstructions string         `json:"developerInstructions,omitempty"`
	Config                map[string]any `json:"config,omitempty"`
}

// sandboxPolicy is the SandboxPolicy union (the thread response's legacy
// projection). networkAccess is a bool for readOnly/workspaceWrite.
type sandboxPolicy struct {
	Type          string          `json:"type"`
	NetworkAccess json.RawMessage `json:"networkAccess,omitempty"`
	WritableRoots []string        `json:"writableRoots,omitempty"`
}

type thread struct {
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	Ephemeral bool   `json:"ephemeral"`
}

type threadStartResponse struct {
	Thread             thread          `json:"thread"`
	ApprovalsReviewer  string          `json:"approvalsReviewer"`
	Cwd                string          `json:"cwd"`
	Model              string          `json:"model"`
	ModelProvider      string          `json:"modelProvider"`
	ApprovalPolicy     json.RawMessage `json:"approvalPolicy"`
	Sandbox            sandboxPolicy   `json:"sandbox"`
	InstructionSources []string        `json:"instructionSources"`
}

type userInput struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []userInput `json:"input"`
}

type turnError struct {
	Message           string          `json:"message"`
	CodexErrorInfo    json.RawMessage `json:"codexErrorInfo"`
	AdditionalDetails *string         `json:"additionalDetails"`
}

type turn struct {
	ID     string       `json:"id"`
	Status string       `json:"status"`
	Error  *turnError   `json:"error"`
	Items  []threadItem `json:"items"`
}

type turnStartResponse struct {
	Turn turn `json:"turn"`
}

type turnSteerParams struct {
	ThreadID       string      `json:"threadId"`
	ExpectedTurnID string      `json:"expectedTurnId"`
	Input          []userInput `json:"input"`
}

type turnSteerResponse struct {
	TurnID string `json:"turnId"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnNotification struct {
	ThreadID string `json:"threadId"`
	Turn     turn   `json:"turn"`
}

type errorNotification struct {
	Error     turnError `json:"error"`
	ThreadID  string    `json:"threadId"`
	TurnID    string    `json:"turnId"`
	WillRetry bool      `json:"willRetry"`
}

// ---- items

type fileUpdateChange struct {
	Path string `json:"path"`
	Kind struct {
		Type     string  `json:"type"` // add | delete | update
		MovePath *string `json:"move_path"`
	} `json:"kind"`
	Diff string `json:"diff"`
}

type mcpToolCallError struct {
	Message string `json:"message"`
}

// threadItem is the tagged ThreadItem union, flattened to the fields used.
type threadItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	// agentMessage
	Text string `json:"text,omitempty"`
	// commandExecution
	Command  string  `json:"command,omitempty"`
	Cwd      string  `json:"cwd,omitempty"`
	Status   string  `json:"status,omitempty"`
	ExitCode *int    `json:"exitCode,omitempty"`
	Output   *string `json:"aggregatedOutput,omitempty"`
	// fileChange
	Changes []fileUpdateChange `json:"changes,omitempty"`
	// mcpToolCall / dynamicToolCall
	Server string            `json:"server,omitempty"`
	Tool   string            `json:"tool,omitempty"`
	Err    *mcpToolCallError `json:"error,omitempty"`
	// webSearch
	Query string `json:"query,omitempty"`
}

type itemNotification struct {
	ThreadID string     `json:"threadId"`
	TurnID   string     `json:"turnId"`
	Item     threadItem `json:"item"`
}

type agentMessageDelta struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type tokenUsageBreakdown struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

type tokenUsageUpdated struct {
	ThreadID   string `json:"threadId"`
	TurnID     string `json:"turnId"`
	TokenUsage struct {
		Total tokenUsageBreakdown `json:"total"`
		Last  tokenUsageBreakdown `json:"last"`
	} `json:"tokenUsage"`
}

type planUpdated struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Plan     []struct {
		Step   string `json:"step"`
		Status string `json:"status"`
	} `json:"plan"`
}

type warningNotification struct {
	Message  string  `json:"message"`
	ThreadID *string `json:"threadId"`
}

type configWarningNotification struct {
	Summary string  `json:"summary"`
	Details *string `json:"details"`
	Path    *string `json:"path"`
}

type mcpStartupNotification struct {
	ThreadID *string `json:"threadId"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Error    *string `json:"error"`
}

type hookStartedNotification struct {
	ThreadID string `json:"threadId"`
	Run      struct {
		EventName  string `json:"eventName"`
		Source     string `json:"source"`
		SourcePath string `json:"sourcePath"`
	} `json:"run"`
}

type modelReroutedNotification struct {
	ThreadID  string `json:"threadId"`
	FromModel string `json:"fromModel"`
	ToModel   string `json:"toModel"`
	Reason    string `json:"reason"`
}

type threadStatusNotification struct {
	ThreadID string `json:"threadId"`
	Status   struct {
		Type string `json:"type"`
	} `json:"status"`
}

type requestResolvedNotification struct {
	ThreadID  string          `json:"threadId"`
	RequestID json.RawMessage `json:"requestId"`
}

// ---- server requests

type commandAction struct {
	Type    string  `json:"type"`
	Command string  `json:"command"`
	Path    *string `json:"path"`
}

type networkApprovalContext struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol"`
}

type commandApprovalParams struct {
	ThreadID               string                  `json:"threadId"`
	TurnID                 string                  `json:"turnId"`
	ItemID                 string                  `json:"itemId"`
	ApprovalID             *string                 `json:"approvalId"`
	Command                *string                 `json:"command"`
	Cwd                    *string                 `json:"cwd"`
	CommandActions         []commandAction         `json:"commandActions"`
	Reason                 *string                 `json:"reason"`
	NetworkApprovalContext *networkApprovalContext `json:"networkApprovalContext"`
}

type fileChangeApprovalParams struct {
	ThreadID  string  `json:"threadId"`
	TurnID    string  `json:"turnId"`
	ItemID    string  `json:"itemId"`
	Reason    *string `json:"reason"`
	GrantRoot *string `json:"grantRoot"`
}

type approvalResponse struct {
	Decision any `json:"decision"`
}

type fileSystemPermissions struct {
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
}

type networkPermissions struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type permissionProfile struct {
	FileSystem *fileSystemPermissions `json:"fileSystem,omitempty"`
	Network    *networkPermissions    `json:"network,omitempty"`
}

type permissionsApprovalParams struct {
	ThreadID    string            `json:"threadId"`
	TurnID      string            `json:"turnId"`
	ItemID      string            `json:"itemId"`
	Cwd         string            `json:"cwd"`
	Reason      *string           `json:"reason"`
	Permissions permissionProfile `json:"permissions"`
}

type permissionsApprovalResponse struct {
	Permissions permissionProfile `json:"permissions"`
	Scope       string            `json:"scope"` // turn | session
}

type userInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type userInputQuestion struct {
	ID       string            `json:"id"`
	Header   string            `json:"header"`
	Question string            `json:"question"`
	IsOther  bool              `json:"isOther"`
	IsSecret bool              `json:"isSecret"`
	Options  []userInputOption `json:"options"`
}

type userInputParams struct {
	ThreadID   string              `json:"threadId"`
	TurnID     string              `json:"turnId"`
	ItemID     string              `json:"itemId"`
	IsBlocking bool                `json:"isBlocking"`
	Questions  []userInputQuestion `json:"questions"`
}

type userInputAnswer struct {
	Answers []string `json:"answers"`
}

type userInputResponse struct {
	Answers map[string]userInputAnswer `json:"answers"`
}

type elicitationParams struct {
	ThreadID        string          `json:"threadId"`
	TurnID          *string         `json:"turnId"`
	ServerName      string          `json:"serverName"`
	Mode            string          `json:"mode"` // form | openai/form | url
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requestedSchema"`
	URL             string          `json:"url,omitempty"`
	Meta            json.RawMessage `json:"_meta"`
}

type elicitationResponse struct {
	Action  string `json:"action"` // accept | decline | cancel
	Content any    `json:"content"`
}

type dynamicToolContentItem struct {
	Type string `json:"type"` // inputText
	Text string `json:"text"`
}

type dynamicToolCallResponse struct {
	ContentItems []dynamicToolContentItem `json:"contentItems"`
	Success      bool                     `json:"success"`
}

type legacyExecApprovalParams struct {
	CallID         string   `json:"callId"`
	ConversationID string   `json:"conversationId"`
	Command        []string `json:"command"`
	Cwd            string   `json:"cwd"`
	Reason         *string  `json:"reason"`
}

type legacyPatchApprovalParams struct {
	CallID         string                     `json:"callId"`
	ConversationID string                     `json:"conversationId"`
	FileChanges    map[string]json.RawMessage `json:"fileChanges"`
	Reason         *string                    `json:"reason"`
	GrantRoot      *string                    `json:"grantRoot"`
}
