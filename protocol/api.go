// Package protocol defines yip's versioned wire contracts: the browser API
// resources, the event envelope, and the runner protocol. Go types here are
// the source of truth; JSON schemas and TypeScript types are generated from
// them (see cmd/yip schema).
package protocol

import (
	"encoding/json"
	"time"
)

// SchemaVersion is the version of the event envelope and API resources.
const SchemaVersion = 1

// Actor identifies who performed an action.
type Actor struct {
	Kind string `json:"kind"` // user | engineer | system | node
	ID   string `json:"id"`
}

const (
	ActorUser     = "user"
	ActorEngineer = "engineer"
	ActorSystem   = "system"
	ActorNode     = "node"
)

// Destination is an immutable reply location.
type Destination struct {
	RoomID    string `json:"roomId"`
	ThreadID  string `json:"threadId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
}

// Ref links a message or event to another resource.
type Ref struct {
	Kind string `json:"kind"` // job | review | approval | question | pr | artifact | decision | run | check
	ID   string `json:"id"`
}

// APIError is the structured error for every failed request.
type APIError struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	Recoverable       bool   `json:"recoverable"`
	CorrelationID     string `json:"correlationId,omitempty"`
	MissingCapability string `json:"missingCapability,omitempty"`
	Details           any    `json:"details,omitempty"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

type Org struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

type User struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	Name      string    `json:"name"`
	Handle    string    `json:"handle"`
	AvatarID  string    `json:"avatarId,omitempty"` // picture at /v1/avatars/{id}; empty shows the initial
	CreatedAt time.Time `json:"createdAt"`
}

type Preferences struct {
	Theme   string `json:"theme"`   // system | day | night
	Density string `json:"density"` // comfortable | compact
	SendKey string `json:"sendKey"` // enter | mod-enter
	Notify  string `json:"notify"`  // mentions | all | none
	// MutedRoomIDs are rooms whose notifications are silenced (execution is
	// unaffected; a question or mention for you still notifies).
	MutedRoomIDs []string `json:"mutedRoomIds,omitempty"`
}

// ProviderPreference is an engineer's configured provider route.
type ProviderPreference struct {
	Provider  string `json:"provider"` // adapter identifier from the provider catalog
	Model     string `json:"model,omitempty"`
	ProfileID string `json:"profileId,omitempty"`
	// Alternatives are explicitly configured fallbacks. Empty means wait.
	Alternatives []string `json:"alternatives,omitempty"`
	// AllowAPIBilling lets this engineer run on installations billed to an
	// API key. Off by default: yip never falls back from a subscription
	// sign-in to paid API usage unless the owner opts in.
	AllowAPIBilling bool `json:"allowApiBilling,omitempty"`
}

// ProviderProfile is one provider account (or pooled installation) and how
// many runs it may carry at once across all machines.
type ProviderProfile struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	Label          string `json:"label"`
	Billing        string `json:"billing"`
	MaxConcurrency int    `json:"maxConcurrency"`
	// PausedUntil is set while the account's allowance is exhausted; queued
	// work for it waits until then.
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
}

type ProviderProfileRequest struct {
	MaxConcurrency int `json:"maxConcurrency"`
}

type Engineer struct {
	ID             string             `json:"id"`
	OrgID          string             `json:"orgId"`
	Name           string             `json:"name"`
	Handle         string             `json:"handle"`
	Role           string             `json:"role"`
	Description    string             `json:"description"`
	Instructions   string             `json:"instructions"`
	CapabilityTags []string           `json:"capabilityTags"`
	Provider       ProviderPreference `json:"provider"`
	Hue            int                `json:"hue"`
	AvatarID       string             `json:"avatarId,omitempty"` // picture at /v1/avatars/{id}; not versioned configuration
	Archived       bool               `json:"archived"`
	VersionID      string             `json:"versionId"`
	VersionNo      int                `json:"versionNo"`
	Version        int64              `json:"version"`
	RoomIDs        []string           `json:"roomIds"`
	ActiveJobIDs   []string           `json:"activeJobIds"`
	CreatedAt      time.Time          `json:"createdAt"`
	UpdatedAt      time.Time          `json:"updatedAt"`
}

type EngineerVersion struct {
	ID             string             `json:"id"`
	EngineerID     string             `json:"engineerId"`
	VersionNo      int                `json:"versionNo"`
	Name           string             `json:"name"`
	Role           string             `json:"role"`
	Description    string             `json:"description"`
	Instructions   string             `json:"instructions"`
	CapabilityTags []string           `json:"capabilityTags"`
	Provider       ProviderPreference `json:"provider"`
	CreatedAt      time.Time          `json:"createdAt"`
}

type Member struct {
	Kind string `json:"kind"` // user | engineer
	ID   string `json:"id"`
}

type Room struct {
	ID         string    `json:"id"`
	OrgID      string    `json:"orgId"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"` // room | dm
	Purpose    string    `json:"purpose"`
	Private    bool      `json:"private"`
	ReplyMode  string    `json:"replyMode"` // steward | quiet
	StewardID  string    `json:"stewardId,omitempty"`
	Members    []Member  `json:"members"`
	ProjectIDs []string  `json:"projectIds"`
	Archived   bool      `json:"archived"`
	LastSeq    int64     `json:"lastSeq"`
	Version    int64     `json:"version"`
	CreatedAt  time.Time `json:"createdAt"`
	// Per-viewer state.
	LastReadSeq  int64 `json:"lastReadSeq"`
	UnreadCount  int   `json:"unreadCount"`
	MentionCount int   `json:"mentionCount"`
}

const (
	RoomKindRoom = "room"
	RoomKindDM   = "dm"

	ReplyModeSteward = "steward"
	ReplyModeQuiet   = "quiet"
)

// Mention is a structured mention resolved by the composer or a validated tool.
type Mention struct {
	Kind string `json:"kind"` // user | engineer
	ID   string `json:"id"`
}

type ReactionSummary struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

type ThreadSummary struct {
	ReplyCount   int       `json:"replyCount"`
	LastReplyAt  time.Time `json:"lastReplyAt"`
	Participants []Actor   `json:"participants"`
}

type Message struct {
	ID          string            `json:"id"`
	OrgID       string            `json:"orgId"`
	RoomID      string            `json:"roomId"`
	ThreadID    string            `json:"threadId,omitempty"`
	Seq         int64             `json:"seq"`
	Author      Actor             `json:"author"`
	Body        string            `json:"body"`
	Kind        string            `json:"kind"` // text | question | approval | result | review | status | system
	Mentions    []Mention         `json:"mentions"`
	ProjectIDs  []string          `json:"projectIds"`
	Refs        []Ref             `json:"refs"`
	ReplyToID   string            `json:"replyToId,omitempty"`
	RunID       string            `json:"runId,omitempty"`
	JobID       string            `json:"jobId,omitempty"`
	ClientKey   string            `json:"clientKey,omitempty"`
	Reactions   []ReactionSummary `json:"reactions"`
	Thread      *ThreadSummary    `json:"thread,omitempty"`
	Revision    int               `json:"revision"`
	EditedAt    *time.Time        `json:"editedAt,omitempty"`
	DeletedAt   *time.Time        `json:"deletedAt,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	Attachments []Artifact        `json:"attachments,omitempty"`
}

const (
	MessageText     = "text"
	MessageQuestion = "question"
	MessageApproval = "approval"
	MessageResult   = "result"
	MessageReview   = "review"
	MessageStatus   = "status"
	MessageSystem   = "system"
)

type JobState string

const (
	JobQueued      JobState = "queued"
	JobRunning     JobState = "running"
	JobWaiting     JobState = "waiting"
	JobReviewReady JobState = "review_ready"
	JobCompleted   JobState = "completed"
	JobFailed      JobState = "failed"
	JobCancelled   JobState = "cancelled"
)

// Waiting reasons distinguish why a job is not progressing.
const (
	WaitMissingInfo      = "missing_information"
	WaitApproval         = "approval"
	WaitDependency       = "dependency"
	WaitProviderLimit    = "provider_allowance"
	WaitMachine          = "machine_availability"
	WaitRecovery         = "recovery"
	WaitProviderSignIn   = "provider_sign_in"
	WaitEngineerCapacity = "engineer_capacity"
	// WaitStalled means an attempt ended without completing or recording why;
	// the missing evidence is listed in StateDetail.
	WaitStalled = "stalled"
)

const (
	JobKindReply         = "reply"
	JobKindCode          = "code"
	JobKindInvestigation = "investigation"
	JobKindReview        = "review"
	JobKindDocument      = "document"
)

type Revision struct {
	RepoID string `json:"repoId"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base"`
	Head   string `json:"head,omitempty"`
}

type Job struct {
	ID            string   `json:"id"`
	OrgID         string   `json:"orgId"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	Objective     string   `json:"objective"`
	Acceptance    []string `json:"acceptance"`
	State         JobState `json:"state"`
	WaitingReason string   `json:"waitingReason,omitempty"`
	StateDetail   string   `json:"stateDetail,omitempty"`
	OwnerID       string   `json:"ownerId"`
	Contributors  []string `json:"contributors"`
	ParentID      string   `json:"parentId,omitempty"`
	// FollowsID is the finished work this was started as a follow-up to.
	FollowsID           string      `json:"followsId,omitempty"`
	RootRequestID       string      `json:"rootRequestId"`
	Source              Destination `json:"source"`
	ProjectID           string      `json:"projectId,omitempty"`
	RepoID              string      `json:"repoId,omitempty"`
	Revision            *Revision   `json:"revision,omitempty"`
	RequiresPeerReview  bool        `json:"requiresPeerReview"`
	RequiresHumanReview bool        `json:"requiresHumanReview"`
	CompletionRequested bool        `json:"completionRequested"`
	Summary             string      `json:"summary,omitempty"`
	Depth               int         `json:"depth"`
	Priority            int         `json:"priority"`
	CurrentRunID        string      `json:"currentRunId,omitempty"`
	NodeID              string      `json:"nodeId,omitempty"`
	LastActivity        string      `json:"lastActivity,omitempty"`
	LastActivityAt      *time.Time  `json:"lastActivityAt,omitempty"`
	ReviewerIDs         []string    `json:"reviewerIds"`
	Version             int64       `json:"version"`
	CreatedAt           time.Time   `json:"createdAt"`
	UpdatedAt           time.Time   `json:"updatedAt"`
	CompletedAt         *time.Time  `json:"completedAt,omitempty"`
}

type RunState string

const (
	RunCreated       RunState = "created"
	RunOffered       RunState = "offered"
	RunPreparing     RunState = "preparing"
	RunRunning       RunState = "running"
	RunAwaitingInput RunState = "awaiting_input"
	RunStopping      RunState = "stopping"
	RunSucceeded     RunState = "succeeded"
	RunFailed        RunState = "failed"
	RunCancelled     RunState = "cancelled"
	RunUnknown       RunState = "unknown"
)

// Run modes govern the workspace and permission boundary of an attempt.
const (
	ModeEdit         = "edit"
	ModeReadOnly     = "readonly"
	ModeConversation = "conversation"
)

type Usage struct {
	Source       string   `json:"source"` // vendor | unknown
	InputTokens  *int64   `json:"inputTokens,omitempty"`
	OutputTokens *int64   `json:"outputTokens,omitempty"`
	CostUSD      *float64 `json:"costUsd,omitempty"`
	Billing      string   `json:"billing"` // subscription | api | unknown
}

type Run struct {
	ID                string      `json:"id"`
	JobID             string      `json:"jobId"`
	Attempt           int         `json:"attempt"`
	EngineerID        string      `json:"engineerId"`
	EngineerVersionID string      `json:"engineerVersionId"`
	NodeID            string      `json:"nodeId,omitempty"`
	Provider          string      `json:"provider"`
	Model             string      `json:"model,omitempty"`
	ProfileID         string      `json:"profileId,omitempty"`
	Mode              string      `json:"mode"`
	State             RunState    `json:"state"`
	LeaseEpoch        int64       `json:"leaseEpoch"`
	LeaseExpiresAt    *time.Time  `json:"leaseExpiresAt,omitempty"`
	HeartbeatAt       *time.Time  `json:"heartbeatAt,omitempty"`
	VendorSessionID   string      `json:"vendorSessionId,omitempty"`
	Destination       Destination `json:"destination"`
	CauseID           string      `json:"causeId"`
	RootRequestID     string      `json:"rootRequestId"`
	PreviousRunID     string      `json:"previousRunId,omitempty"`
	Branch            string      `json:"branch,omitempty"`
	BaseRev           string      `json:"baseRev,omitempty"`
	ResultRev         string      `json:"resultRev,omitempty"`
	TerminalReason    string      `json:"terminalReason,omitempty"`
	LastActivity      string      `json:"lastActivity,omitempty"`
	LastActivityAt    *time.Time  `json:"lastActivityAt,omitempty"`
	Usage             *Usage      `json:"usage,omitempty"`
	CreatedAt         time.Time   `json:"createdAt"`
	StartedAt         *time.Time  `json:"startedAt,omitempty"`
	EndedAt           *time.Time  `json:"endedAt,omitempty"`
}

// RunActivity is one recorded, user-visible step of a run (tool logs live here).
type RunActivity struct {
	RunID string          `json:"runId"`
	Seq   int64           `json:"seq"`
	Kind  string          `json:"kind"`
	Text  string          `json:"text"`
	Tool  string          `json:"tool,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	At    time.Time       `json:"at"`
}

type Check struct {
	ID            string    `json:"id"`
	JobID         string    `json:"jobId"`
	RunID         string    `json:"runId"`
	NodeID        string    `json:"nodeId"`
	Name          string    `json:"name"`
	Command       string    `json:"command"`
	ExitCode      int       `json:"exitCode"`
	Passed        bool      `json:"passed"`
	Revision      string    `json:"revision"`
	LogArtifactID string    `json:"logArtifactId,omitempty"`
	DurationMs    int64     `json:"durationMs"`
	Summary       string    `json:"summary,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type Artifact struct {
	ID          string    `json:"id"`
	Hash        string    `json:"hash"`
	Name        string    `json:"name"`
	ContentType string    `json:"contentType"`
	Size        int64     `json:"size"`
	Kind        string    `json:"kind"` // diff | log | file | checkpoint | document | bundle | avatar
	RunID       string    `json:"runId,omitempty"`
	JobID       string    `json:"jobId,omitempty"`
	RoomID      string    `json:"roomId,omitempty"`
	ProjectID   string    `json:"projectId,omitempty"`
	Revision    string    `json:"revision,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type ReviewState string

const (
	ReviewRequested        ReviewState = "requested"
	ReviewQueued           ReviewState = "queued"
	ReviewReviewing        ReviewState = "reviewing"
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes_requested"
	ReviewCommentsOnly     ReviewState = "comments_only"
	ReviewUnable           ReviewState = "unable_to_review"
	ReviewCancelled        ReviewState = "cancelled"
)

type ReviewTarget struct {
	Kind          string `json:"kind"` // patch | document | artifact | pr
	RepoID        string `json:"repoId,omitempty"`
	Base          string `json:"base,omitempty"`
	Head          string `json:"head,omitempty"`
	ArtifactID    string `json:"artifactId,omitempty"`
	Hash          string `json:"hash,omitempty"`
	PullRequestID string `json:"pullRequestId,omitempty"`
}

type FindingReply struct {
	ID        string    `json:"id"`
	FindingID string    `json:"findingId"`
	Author    Actor     `json:"author"`
	Body      string    `json:"body"`
	Evidence  string    `json:"evidence,omitempty"`
	Revision  string    `json:"revision,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type Finding struct {
	ID        string         `json:"id"`
	RoundID   string         `json:"roundId"`
	Severity  string         `json:"severity"` // blocking | suggestion | note
	Body      string         `json:"body"`
	File      string         `json:"file,omitempty"`
	Line      int            `json:"line,omitempty"`
	Evidence  string         `json:"evidence,omitempty"`
	Status    string         `json:"status"` // open | addressed | resolved | disputed | withdrawn
	Replies   []FindingReply `json:"replies"`
	CreatedAt time.Time      `json:"createdAt"`
}

type ReviewRound struct {
	ID            string       `json:"id"`
	ReviewID      string       `json:"reviewId"`
	Number        int          `json:"number"`
	Target        ReviewTarget `json:"target"`
	State         ReviewState  `json:"state"`
	Summary       string       `json:"summary,omitempty"`
	ReviewerRunID string       `json:"reviewerRunId,omitempty"`
	ReviewJobID   string       `json:"reviewJobId,omitempty"`
	SupersededBy  string       `json:"supersededBy,omitempty"`
	Findings      []Finding    `json:"findings"`
	CreatedAt     time.Time    `json:"createdAt"`
	DecidedAt     *time.Time   `json:"decidedAt,omitempty"`
}

type Review struct {
	ID            string        `json:"id"`
	JobID         string        `json:"jobId"`
	AuthorID      string        `json:"authorId"`
	ReviewerID    string        `json:"reviewerId"`
	Source        Destination   `json:"source"`
	TargetKind    string        `json:"targetKind"`
	Criteria      string        `json:"criteria"`
	State         ReviewState   `json:"state"`
	CurrentRound  int           `json:"currentRound"`
	PullRequestID string        `json:"pullRequestId,omitempty"`
	Rounds        []ReviewRound `json:"rounds"`
	CreatedAt     time.Time     `json:"createdAt"`
	UpdatedAt     time.Time     `json:"updatedAt"`
}

type ChecksSummary struct {
	State   string `json:"state"` // success | failure | pending | none | unknown
	Passed  int    `json:"passed"`
	Failed  int    `json:"failed"`
	Pending int    `json:"pending"`
	Total   int    `json:"total"`
}

type RemoteReview struct {
	Actor       string    `json:"actor"`
	State       string    `json:"state"` // APPROVED | CHANGES_REQUESTED | COMMENTED | DISMISSED
	CommitID    string    `json:"commitId"`
	SubmittedAt time.Time `json:"submittedAt"`
	ExternalID  string    `json:"externalId"`
	// PublishedBy is set when yip published it on behalf of an engineer.
	PublishedByEngineerID string `json:"publishedByEngineerId,omitempty"`
}

type MergeStatus struct {
	Merged    bool     `json:"merged"`
	Mergeable string   `json:"mergeable"` // clean | blocked | dirty | unknown | behind | unstable
	Reasons   []string `json:"reasons"`
}

type PullRequest struct {
	ID            string         `json:"id"`
	RepoID        string         `json:"repoId"`
	JobID         string         `json:"jobId,omitempty"`
	Forge         string         `json:"forge"`
	Host          string         `json:"host"`
	Owner         string         `json:"owner"`
	Name          string         `json:"name"`
	Number        int            `json:"number"`
	URL           string         `json:"url"`
	Title         string         `json:"title"`
	State         string         `json:"state"` // open | closed | merged
	Base          string         `json:"base"`
	Head          string         `json:"head"`
	RemoteAuthor  string         `json:"remoteAuthor"`
	ViewerActor   string         `json:"viewerActor"`
	Checks        ChecksSummary  `json:"checks"`
	Merge         MergeStatus    `json:"merge"`
	RemoteReviews []RemoteReview `json:"remoteReviews"`
	LastSyncedAt  *time.Time     `json:"lastSyncedAt,omitempty"`
}

type ApprovalAction struct {
	Kind    string `json:"kind"` // exec | edit | network | push | publish | merge | mcp | other
	Summary string `json:"summary"`
	Command string `json:"command,omitempty"`
	Target  string `json:"target,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

type Approval struct {
	ID         string         `json:"id"`
	RunID      string         `json:"runId"`
	JobID      string         `json:"jobId"`
	EngineerID string         `json:"engineerId"`
	Action     ApprovalAction `json:"action"`
	ArgsDigest string         `json:"argsDigest"`
	Scope      string         `json:"scope"`
	TargetRev  string         `json:"targetRev,omitempty"`
	Status     string         `json:"status"` // pending | approved | rejected | expired | consumed | cancelled
	ExpiresAt  time.Time      `json:"expiresAt"`
	DecidedBy  *Actor         `json:"decidedBy,omitempty"`
	DecidedAt  *time.Time     `json:"decidedAt,omitempty"`
	Source     Destination    `json:"source"`
	Version    int64          `json:"version"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type Question struct {
	ID              string      `json:"id"`
	JobID           string      `json:"jobId"`
	AskerID         string      `json:"askerId"`
	Recipient       Actor       `json:"recipient"`
	MissingFact     string      `json:"missingFact"`
	ContextChecked  string      `json:"contextChecked"`
	DependentStep   string      `json:"dependentStep"`
	ContinuingWith  string      `json:"continuingWith"`
	Status          string      `json:"status"` // open | answered | cancelled
	Source          Destination `json:"source"`
	MessageID       string      `json:"messageId"`
	AnswerMessageID string      `json:"answerMessageId,omitempty"`
	CreatedAt       time.Time   `json:"createdAt"`
	AnsweredAt      *time.Time  `json:"answeredAt,omitempty"`
}

type ProviderCapabilities struct {
	StructuredEvents bool `json:"structuredEvents"`
	ToolApprovals    bool `json:"toolApprovals"`
	UserQuestions    bool `json:"userQuestions"`
	SessionResume    bool `json:"sessionResume"`
	ActiveSteering   bool `json:"activeSteering"`
	UsageTelemetry   bool `json:"usageTelemetry"`
	Sandbox          bool `json:"sandbox"`
	ModelEnumeration bool `json:"modelEnumeration"`
	ReadOnly         bool `json:"readOnly"`
	MCPTools         bool `json:"mcpTools"`
}

type Model struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Default     bool   `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

type ProviderInstallation struct {
	Provider      string               `json:"provider"`
	Version       string               `json:"version"`
	Path          string               `json:"path"`
	AuthState     string               `json:"authState"` // ready | needs_signin | unknown | error | not_installed
	AuthDetail    string               `json:"authDetail,omitempty"`
	Account       string               `json:"account,omitempty"`
	Billing       string               `json:"billing"` // subscription | api | unknown
	ProfileID     string               `json:"profileId"`
	Capabilities  ProviderCapabilities `json:"capabilities"`
	Models        []Model              `json:"models"`
	TestedVersion string               `json:"testedVersion,omitempty"`
	Tested        bool                 `json:"tested"`
	Limitations   []string             `json:"limitations,omitempty"`
	UpdatedAt     time.Time            `json:"updatedAt"`
}

const (
	AuthReady        = "ready"
	AuthNeedsSignIn  = "needs_signin"
	AuthUnknown      = "unknown"
	AuthError        = "error"
	AuthNotInstalled = "not_installed"

	BillingSubscription = "subscription"
	BillingAPI          = "api"
	BillingUnknown      = "unknown"
)

type ExecutionProfile struct {
	Name      string `json:"name"` // native | container | readonly
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Summary   string `json:"summary"`
}

type NodeCapacity struct {
	Slots        int   `json:"slots"`
	Used         int   `json:"used"`
	CPUs         int   `json:"cpus"`
	MemMB        int64 `json:"memMb"`
	DiskFreeMB   int64 `json:"diskFreeMb"`
	DiskPressure bool  `json:"diskPressure"`
}

type Node struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Hostname      string                 `json:"hostname"`
	OS            string                 `json:"os"`
	Arch          string                 `json:"arch"`
	Fingerprint   string                 `json:"fingerprint"`
	Status        string                 `json:"status"` // online | suspect | offline | revoked
	Draining      bool                   `json:"draining"`
	LastSeenAt    *time.Time             `json:"lastSeenAt,omitempty"`
	Capacity      NodeCapacity           `json:"capacity"`
	Providers     []ProviderInstallation `json:"providers"`
	Profiles      []ExecutionProfile     `json:"profiles"`
	Toolchains    map[string]string      `json:"toolchains"`
	ActiveRunIDs  []string               `json:"activeRunIds"`
	RunnerVersion string                 `json:"runnerVersion"`
	ServiceState  string                 `json:"serviceState,omitempty"`
	LastActivity  string                 `json:"lastActivity,omitempty"`
	CreatedAt     time.Time              `json:"createdAt"`
	RevokedAt     *time.Time             `json:"revokedAt,omitempty"`
	// RemovedAt hides an explicitly removed machine; its revoked identity and history remain.
	RemovedAt *time.Time `json:"removedAt,omitempty"`
	// Workspaces on the machine, as last reported, with the work they hold.
	Workspaces []NodeWorkspace `json:"workspaces"`
}

// NodeWorkspace is a runner workspace joined with the work it belongs to.
type NodeWorkspace struct {
	WorkspaceInfo
	JobID    string `json:"jobId,omitempty"`
	JobTitle string `json:"jobTitle,omitempty"`
	JobState string `json:"jobState,omitempty"`
	// JobKind is the kind of that work (code, review, reply, …): a scratch
	// space belongs to a conversation reply or to work without a repository.
	JobKind string `json:"jobKind,omitempty"`
	// Published is true when the branch head is the work's published
	// revision and nothing is uncommitted: removing it loses nothing.
	Published bool `json:"published"`
	// Blocked explains why it can't be removed now (in use, open work).
	Blocked string `json:"blocked,omitempty"`
}

const (
	NodeOnline  = "online"
	NodeSuspect = "suspect"
	NodeOffline = "offline"
	NodeRevoked = "revoked"
)

type Enrollment struct {
	ID             string    `json:"id"`
	Token          string    `json:"token,omitempty"` // only returned at creation
	Name           string    `json:"name"`
	ExpiresAt      time.Time `json:"expiresAt"`
	HubURL         string    `json:"hubUrl"`
	HubFingerprint string    `json:"hubFingerprint"`
	Command        string    `json:"command"`
	RunCommand     string    `json:"runCommand,omitempty"`
}

type ProjectPolicy struct {
	RequirePeerReview  bool     `json:"requirePeerReview"`
	RequireHumanReview bool     `json:"requireHumanReview"`
	AutoPublish        bool     `json:"autoPublish"`
	Checks             []string `json:"checks"`
	ExecutionProfile   string   `json:"executionProfile"` // native | container
	// Requires lists what a machine needs for this project's work: tools the
	// runner reports (go, node, python3, docker, cargo, swift, xcodebuild, gh)
	// or an operating system as os:darwin / os:linux.
	Requires []string `json:"requires,omitempty"`
}

type Repo struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"projectId"`
	Name          string    `json:"name"`
	RemoteURL     string    `json:"remoteUrl"`
	DefaultBranch string    `json:"defaultBranch"`
	Forge         string    `json:"forge"` // github | none
	ForgeRepo     string    `json:"forgeRepo,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	// SourceBundleID is set for a repository imported from a git bundle
	// (no remote): machines build their copy from it, and nothing can be
	// pushed anywhere. ImportedAt is when the current bundle was imported.
	SourceBundleID string     `json:"sourceBundleId,omitempty"`
	ImportedAt     *time.Time `json:"importedAt,omitempty"`
}

type Grant struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"projectId"`
	EngineerID string   `json:"engineerId"`
	Access     string   `json:"access"`  // read | write
	Actions    []string `json:"actions"` // push | open_pr | publish_review | merge
}

type Project struct {
	ID           string        `json:"id"`
	OrgID        string        `json:"orgId"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Instructions string        `json:"instructions"`
	Policy       ProjectPolicy `json:"policy"`
	Repos        []Repo        `json:"repos"`
	Grants       []Grant       `json:"grants"`
	RoomIDs      []string      `json:"roomIds"`
	Version      int64         `json:"version"`
	CreatedAt    time.Time     `json:"createdAt"`
}

type DecisionScope struct {
	Kind string `json:"kind"` // project | room | org
	ID   string `json:"id"`
}

type Source struct {
	Kind   string `json:"kind"` // message | job | review | decision
	ID     string `json:"id"`
	RoomID string `json:"roomId,omitempty"`
}

type Decision struct {
	ID             string        `json:"id"`
	Scope          DecisionScope `json:"scope"`
	Title          string        `json:"title"`
	Body           string        `json:"body"`
	Status         string        `json:"status"` // proposed | accepted | superseded | rejected
	SupersedesID   string        `json:"supersedesId,omitempty"`
	SupersededByID string        `json:"supersededById,omitempty"`
	CreatedBy      Actor         `json:"createdBy"`
	AcceptedBy     *Actor        `json:"acceptedBy,omitempty"`
	Sources        []Source      `json:"sources"`
	VisibleRoomIDs []string      `json:"visibleRoomIds"`
	Version        int64         `json:"version"`
	CreatedAt      time.Time     `json:"createdAt"`
	AcceptedAt     *time.Time    `json:"acceptedAt,omitempty"`
}

// Event is the SSE and audit envelope.
type Event struct {
	SchemaVersion int             `json:"schemaVersion"`
	EventID       string          `json:"eventId"`
	OrgID         string          `json:"orgId"`
	Sequence      int64           `json:"sequence"`
	Type          string          `json:"type"`
	Actor         Actor           `json:"actor"`
	CauseID       string          `json:"causeId,omitempty"`
	RootRequestID string          `json:"rootRequestId,omitempty"`
	RoomID        string          `json:"roomId,omitempty"`
	ThreadID      string          `json:"threadId,omitempty"`
	JobID         string          `json:"jobId,omitempty"`
	RunID         string          `json:"runId,omitempty"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

// Bootstrap is the initial client state.
type Bootstrap struct {
	User        User              `json:"user"`
	Org         Org               `json:"org"`
	Rooms       []Room            `json:"rooms"`
	Engineers   []Engineer        `json:"engineers"`
	Projects    []Project         `json:"projects"`
	Nodes       []Node            `json:"nodes"`
	Cursor      int64             `json:"cursor"`
	CSRFToken   string            `json:"csrfToken"`
	Preferences Preferences       `json:"preferences"`
	ServerTime  time.Time         `json:"serverTime"`
	Version     string            `json:"version"`
	Providers   []ProviderSummary `json:"providers"`

	// CanManageWorkspace is advisory; configuration mutations also check ownership.
	CanManageWorkspace bool `json:"canManageWorkspace"`
}

// ProviderSummary aggregates provider readiness across machines.
type ProviderSummary struct {
	Provider   string   `json:"provider"`
	Label      string   `json:"label"`
	ReadyNodes []string `json:"readyNodes"`
	Billing    string   `json:"billing"`
}

type SetupStatus struct {
	NeedsSetup bool   `json:"needsSetup"`
	OrgName    string `json:"orgName,omitempty"`
	Version    string `json:"version"`
}

// EngineerNote is a short note an engineer keeps from earlier work (spec §8,
// context layer 5). Accepted, current notes enter the engineer's context
// only in conversations where all of their sources are visible.
type EngineerNote struct {
	ID         string `json:"id"`
	EngineerID string `json:"engineerId"`
	// Kind is "note" (kept by the engineer or the owner) or "record" (written
	// by yip from finished work: outcome, revision, approvals, checks).
	Kind           string        `json:"kind"`
	Scope          DecisionScope `json:"scope"` // project | room
	Body           string        `json:"body"`
	Status         string        `json:"status"` // proposed | accepted | superseded | rejected
	SupersedesID   string        `json:"supersedesId,omitempty"`
	SupersededByID string        `json:"supersededById,omitempty"`
	CreatedBy      Actor         `json:"createdBy"`
	AcceptedBy     *Actor        `json:"acceptedBy,omitempty"`
	Sources        []Source      `json:"sources"`
	VisibleRoomIDs []string      `json:"visibleRoomIds"`
	// ReviewAfter is when the note is due for review; past it the note is
	// left out of context until renewed.
	ReviewAfter time.Time  `json:"reviewAfter"`
	Version     int64      `json:"version"`
	CreatedAt   time.Time  `json:"createdAt"`
	AcceptedAt  *time.Time `json:"acceptedAt,omitempty"`
}

// NoteRequest is an owner-written note for an engineer (accepted at once),
// optionally correcting an earlier one.
type NoteRequest struct {
	Scope        DecisionScope `json:"scope"`
	Body         string        `json:"body"`
	SupersedesID string        `json:"supersedesId,omitempty"`
}

// NoteActionRequest accepts, rejects, renews, or removes a note.
type NoteActionRequest struct {
	Action  string `json:"action"` // accept | reject | renew | remove
	Version int64  `json:"version"`
}

// QuarantinedOutput is output an attempt sent after it lost its lease. It
// never changed the work; it's kept so a person can see what happened.
type QuarantinedOutput struct {
	ID           string    `json:"id"`
	RunID        string    `json:"runId"`
	NodeID       string    `json:"nodeId"`
	Epoch        int64     `json:"epoch"`
	CurrentEpoch int64     `json:"currentEpoch"`
	Kind         string    `json:"kind"` // event | terminal | tool_call
	Summary      string    `json:"summary"`
	ReceivedAt   time.Time `json:"receivedAt"`
}

type JobDetail struct {
	Job          Job            `json:"job"`
	Runs         []Run          `json:"runs"`
	Checks       []Check        `json:"checks"`
	Artifacts    []Artifact     `json:"artifacts"`
	Reviews      []Review       `json:"reviews"`
	Questions    []Question     `json:"questions"`
	Approvals    []Approval     `json:"approvals"`
	PullRequests []PullRequest  `json:"pullRequests"`
	Children     []Job          `json:"children"`
	Decisions    []Decision     `json:"decisions"`
	Activity     []ActivityItem `json:"activity"`
	Inputs       []JobInput     `json:"inputs"`
	Missing      []string       `json:"missing"`
	// Revisions lists every published revision, oldest first.
	Revisions []RevisionRecord `json:"revisions"`
	// FollowUps is later work started as a follow-up to this one.
	FollowUps []Job `json:"followUps"`
	// Quarantined is late output from attempts that had lost their lease.
	Quarantined []QuarantinedOutput `json:"quarantined"`
}

type JobInput struct {
	ID          string     `json:"id"`
	JobID       string     `json:"jobId"`
	RunID       string     `json:"runId,omitempty"`
	Body        string     `json:"body"`
	Delivery    string     `json:"delivery"` // immediate | queued | pending
	MessageID   string     `json:"messageId,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	DeliveredAt *time.Time `json:"deliveredAt,omitempty"`
}

// ActivityItem is a human-readable entry in a job's history.
type ActivityItem struct {
	At     time.Time `json:"at"`
	Actor  Actor     `json:"actor"`
	Type   string    `json:"type"`
	Text   string    `json:"text"`
	RoomID string    `json:"roomId,omitempty"`
	Refs   []Ref     `json:"refs"`
}

type WorkRow struct {
	Job             Job        `json:"job"`
	ProjectName     string     `json:"projectName,omitempty"`
	RoomName        string     `json:"roomName,omitempty"`
	NodeName        string     `json:"nodeName,omitempty"`
	LastConfirmed   string     `json:"lastConfirmed"`
	LastConfirmedAt *time.Time `json:"lastConfirmedAt,omitempty"`
	Blocker         string     `json:"blocker,omitempty"`
	// RunState is the state of the latest attempt ("unknown" means its
	// outcome is not confirmed).
	RunState RunState `json:"runState,omitempty"`
}

type SearchResult struct {
	Kind     string     `json:"kind"` // message | room | engineer | job | decision | project
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Snippet  string     `json:"snippet"`
	RoomID   string     `json:"roomId,omitempty"`
	ThreadID string     `json:"threadId,omitempty"`
	JobID    string     `json:"jobId,omitempty"`
	At       *time.Time `json:"at,omitempty"`
}

type MessagePage struct {
	Messages []Message `json:"messages"`
	HasMore  bool      `json:"hasMore"`
}

// DiagnosticBundle is an opt-in, previewable export for troubleshooting. It
// holds counts, health, versions, and recent failure reasons; never
// messages, prompts, repository contents, account names, or credentials.
type DiagnosticBundle struct {
	GeneratedAt    time.Time           `json:"generatedAt"`
	HubVersion     string              `json:"hubVersion"`
	GoVersion      string              `json:"goVersion"`
	Platform       string              `json:"platform"`
	SchemaVersion  int                 `json:"schemaVersion"`
	Health         []HealthCheck       `json:"health"`
	Counts         map[string]int      `json:"counts"`
	Work           map[string]int      `json:"work"`
	Machines       []DiagnosticNode    `json:"machines"`
	RecentFailures []DiagnosticFailure `json:"recentFailures"`
	Excluded       []string            `json:"excluded"`
}

type DiagnosticNode struct {
	Name          string               `json:"name"`
	Status        string               `json:"status"`
	Platform      string               `json:"platform"`
	RunnerVersion string               `json:"runnerVersion"`
	LastSeenAt    *time.Time           `json:"lastSeenAt,omitempty"`
	Providers     []DiagnosticProvider `json:"providers"`
}

type DiagnosticProvider struct {
	Provider  string `json:"provider"`
	Version   string `json:"version"`
	Tested    bool   `json:"tested"`
	AuthState string `json:"authState"`
	Billing   string `json:"billing"`
}

type DiagnosticFailure struct {
	At       time.Time `json:"at"`
	Provider string    `json:"provider"`
	Machine  string    `json:"machine"`
	State    string    `json:"state"`
	Reason   string    `json:"reason"`
}

type Diagnostics struct {
	Version          string        `json:"version"`
	DataDir          string        `json:"dataDir"`
	DBSizeBytes      int64         `json:"dbSizeBytes"`
	ArtifactBytes    int64         `json:"artifactBytes"`
	DiskFreeBytes    int64         `json:"diskFreeBytes"`
	QueueDepth       int           `json:"queueDepth"`
	ActiveRuns       int           `json:"activeRuns"`
	PendingOutbox    int           `json:"pendingOutbox"`
	PendingApprovals int           `json:"pendingApprovals"`
	FailedRuns24h    int           `json:"failedRuns24h"`
	Nodes            []Node        `json:"nodes"`
	SSEClients       int           `json:"sseClients"`
	LastBackupAt     *time.Time    `json:"lastBackupAt,omitempty"`
	Checks           []HealthCheck `json:"checks"`
}

type HealthCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
