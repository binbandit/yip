package protocol

// Request bodies for the browser HTTP API. Every POST with a persistent effect
// accepts an idempotency key, either as a field or the Idempotency-Key header.

type SetupRequest struct {
	BootstrapSecret string `json:"bootstrapSecret"`
	OrgName         string `json:"orgName"`
	Name            string `json:"name"`
	Handle          string `json:"handle"`
	Password        string `json:"password"`
}

type SignInRequest struct {
	Handle   string `json:"handle"`
	Password string `json:"password"`
}

type CreateRoomRequest struct {
	Name        string   `json:"name"`
	Purpose     string   `json:"purpose"`
	Kind        string   `json:"kind"` // room | dm
	Private     bool     `json:"private"`
	ReplyMode   string   `json:"replyMode"`
	StewardID   string   `json:"stewardId"`
	EngineerIDs []string `json:"engineerIds"`
	ProjectIDs  []string `json:"projectIds"`
}

type UpdateRoomRequest struct {
	Version    int64     `json:"version"`
	Name       *string   `json:"name,omitempty"`
	Purpose    *string   `json:"purpose,omitempty"`
	Private    *bool     `json:"private,omitempty"`
	ReplyMode  *string   `json:"replyMode,omitempty"`
	StewardID  *string   `json:"stewardId,omitempty"`
	ProjectIDs *[]string `json:"projectIds,omitempty"`
	Archived   *bool     `json:"archived,omitempty"`
}

// MembershipPreview describes what history a new member will be able to see.
type MembershipPreview struct {
	RoomID       string `json:"roomId"`
	EngineerID   string `json:"engineerId"`
	VisibleCount int    `json:"visibleMessageCount"`
	PrivateRoom  bool   `json:"privateRoom"`
	Explanation  string `json:"explanation"`
}

type PostMessageRequest struct {
	Body       string    `json:"body"`
	ThreadID   string    `json:"threadId,omitempty"`
	ReplyToID  string    `json:"replyToId,omitempty"`
	Mentions   []Mention `json:"mentions"`
	ProjectIDs []string  `json:"projectIds"`
	ClientKey  string    `json:"clientKey"`
	// JobID attaches the message to a selected running job as input.
	JobID string `json:"jobId,omitempty"`
}

type PostMessageResponse struct {
	Message    Message   `json:"message"`
	Duplicate  bool      `json:"duplicate"`
	Dispatched []string  `json:"dispatched"` // engineer IDs woken by this message
	Input      *JobInput `json:"input,omitempty"`
	Resolved   []string  `json:"resolvedQuestionIds"`
}

type ReactRequest struct {
	Emoji  string `json:"emoji"`
	Remove bool   `json:"remove"`
}

type MarkReadRequest struct {
	Seq int64 `json:"seq"`
}

type CreateEngineerRequest struct {
	Name           string             `json:"name"`
	Handle         string             `json:"handle"`
	Role           string             `json:"role"`
	Description    string             `json:"description"`
	Instructions   string             `json:"instructions"`
	CapabilityTags []string           `json:"capabilityTags"`
	Provider       ProviderPreference `json:"provider"`
}

type UpdateEngineerRequest struct {
	Version        int64               `json:"version"`
	Name           *string             `json:"name,omitempty"`
	Role           *string             `json:"role,omitempty"`
	Description    *string             `json:"description,omitempty"`
	Instructions   *string             `json:"instructions,omitempty"`
	CapabilityTags *[]string           `json:"capabilityTags,omitempty"`
	Provider       *ProviderPreference `json:"provider,omitempty"`
	Archived       *bool               `json:"archived,omitempty"`
}

type CreateProjectRequest struct {
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Instructions string        `json:"instructions"`
	Policy       ProjectPolicy `json:"policy"`
	RoomIDs      []string      `json:"roomIds"`
}

type UpdateProjectRequest struct {
	Version      int64          `json:"version"`
	Name         *string        `json:"name,omitempty"`
	Description  *string        `json:"description,omitempty"`
	Instructions *string        `json:"instructions,omitempty"`
	Policy       *ProjectPolicy `json:"policy,omitempty"`
}

type PutRepoRequest struct {
	Name          string `json:"name"`
	RemoteURL     string `json:"remoteUrl"`
	DefaultBranch string `json:"defaultBranch"`
	Forge         string `json:"forge"`
	ForgeRepo     string `json:"forgeRepo"`
}

type PutGrantRequest struct {
	Access  string   `json:"access"`
	Actions []string `json:"actions"`
}

type JobInputRequest struct {
	Body       string `json:"body"`
	ClientKey  string `json:"clientKey"`
	QuestionID string `json:"questionId,omitempty"`
}

type JobInputResponse struct {
	Input   JobInput `json:"input"`
	Message *Message `json:"message,omitempty"`
}

type CancelJobRequest struct {
	Reason          string `json:"reason"`
	IncludeChildren bool   `json:"includeChildren"`
}

type RetryJobRequest struct {
	FromCheckpoint bool   `json:"fromCheckpoint"`
	NodeID         string `json:"nodeId,omitempty"`
	Reason         string `json:"reason"`
}

type RequestReviewRequest struct {
	ReviewerID string       `json:"reviewerId"`
	Target     ReviewTarget `json:"target"`
	Criteria   string       `json:"criteria"`
	Message    string       `json:"message"`
	CauseKey   string       `json:"causeKey"`
}

type FindingInput struct {
	Severity string `json:"severity"`
	Body     string `json:"body"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type ReviewFindingsRequest struct {
	Round    int            `json:"round"`
	Findings []FindingInput `json:"findings"`
}

type ReviewVerdictRequest struct {
	Round        int            `json:"round"`
	ExpectedHead string         `json:"expectedHead"`
	Verdict      ReviewState    `json:"verdict"`
	Summary      string         `json:"summary"`
	Findings     []FindingInput `json:"findings"`
	// Resolve lists earlier blocking finding IDs the reviewer now considers resolved.
	Resolve []string `json:"resolve"`
}

type FindingResponse struct {
	FindingID string `json:"findingId"`
	Body      string `json:"body"`
	Evidence  string `json:"evidence,omitempty"`
	Disputed  bool   `json:"disputed"`
}

type ReviewRevisionRequest struct {
	Target    ReviewTarget      `json:"target"`
	Responses []FindingResponse `json:"responses"`
	Message   string            `json:"message"`
}

type LinkPullRequestRequest struct {
	RepoID string `json:"repoId"`
	Number int    `json:"number"`
	URL    string `json:"url"`
	JobID  string `json:"jobId"`
}

type AcceptJobRequest struct {
	Revision string `json:"revision"`
	Version  int64  `json:"version"`
	Note     string `json:"note"`
}

type ApprovalDecisionRequest struct {
	Decision string `json:"decision"` // approve | reject
	Version  int64  `json:"version"`
	Note     string `json:"note"`
}

type AnswerQuestionRequest struct {
	Body      string `json:"body"`
	ClientKey string `json:"clientKey"`
}

type CreateEnrollmentRequest struct {
	Name string `json:"name"`
}

// PairRequest is sent by a runner to redeem an enrollment token.
type PairRequest struct {
	Token    string `json:"token"`
	CSRPEM   string `json:"csrPem"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

type PairResponse struct {
	NodeID      string `json:"nodeId"`
	CertPEM     string `json:"certPem"`
	CAPEM       string `json:"caPem"`
	Fingerprint string `json:"fingerprint"`
}

type DecisionRequest struct {
	Scope        DecisionScope `json:"scope"`
	Title        string        `json:"title"`
	Body         string        `json:"body"`
	Sources      []Source      `json:"sources"`
	SupersedesID string        `json:"supersedesId,omitempty"`
	Accept       bool          `json:"accept"`
}

type DecisionActionRequest struct {
	Action  string `json:"action"` // accept | reject
	Version int64  `json:"version"`
}

type PreferencesRequest struct {
	Preferences Preferences `json:"preferences"`
}

type NodeActionRequest struct {
	Drain bool `json:"drain"`
}

// CleanupWorkspaceRequest deletes one workspace on a machine. Confirm must
// repeat the workspace name; Force is required when unpublished or
// uncommitted work would be lost.
type CleanupWorkspaceRequest struct {
	Confirm string `json:"confirm"`
	Force   bool   `json:"force"`
}
