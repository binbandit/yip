package protocol

import (
	"encoding/json"
	"time"
)

// RunnerProtocolVersion is the product-owned runner protocol version. The hub
// refuses runners that speak a different major version with a clear diagnostic.
const RunnerProtocolVersion = 1

// Frame is the envelope for every runner <-> hub WebSocket message.
type Frame struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"` // commandId or producer event id
	RunID      string          `json:"runId,omitempty"`
	LeaseEpoch int64           `json:"leaseEpoch,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// Hub -> runner commands.
const (
	CmdOfferRun        = "offer_run"
	CmdStartRun        = "start_run"
	CmdDeliverInput    = "deliver_input"
	CmdResolveApproval = "resolve_approval"
	CmdCancelRun       = "cancel_run"
	CmdDrain           = "drain"
	CmdRevoke          = "revoke"
	CmdToolResult      = "tool_result"
	CmdAck             = "ack"
	CmdLease           = "lease"
	CmdWelcome         = "welcome"
	CmdReconcile       = "reconcile"
	CmdProbe           = "probe"
	CmdCleanup         = "cleanup_workspace"
)

// Runner -> hub events.
const (
	EvHello           = "hello"
	EvCapabilities    = "capabilities"
	EvHeartbeat       = "heartbeat"
	EvRunAck          = "run_ack"
	EvRunEvent        = "run_event"
	EvArtifactReady   = "artifact_ready"
	EvRunTerminal     = "run_terminal"
	EvToolCall        = "tool_call"
	EvApprovalRequest = "approval_request"
	EvCommandAck      = "command_ack"
)

type Hello struct {
	NodeID          string            `json:"nodeId"`
	ProtocolVersion int               `json:"protocolVersion"`
	RunnerVersion   string            `json:"runnerVersion"`
	Hostname        string            `json:"hostname"`
	OS              string            `json:"os"`
	Arch            string            `json:"arch"`
	BootTime        time.Time         `json:"bootTime"`
	Runs            []JournalRunState `json:"runs"`
}

// JournalRunState is what the runner's journal knows about a run on reconnect.
type JournalRunState struct {
	RunID        string       `json:"runId"`
	LeaseEpoch   int64        `json:"leaseEpoch"`
	State        string       `json:"state"` // accepted | preparing | running | awaiting_input | stopping | terminal
	Terminal     *RunTerminal `json:"terminal,omitempty"`
	LastSeq      int64        `json:"lastSeq"`
	UnackedFrom  int64        `json:"unackedFrom"`
	Workspace    string       `json:"workspace,omitempty"`
	Dirty        bool         `json:"dirty"`
	LastActivity string       `json:"lastActivity,omitempty"`
}

type Welcome struct {
	NodeID          string `json:"nodeId"`
	HeartbeatMs     int64  `json:"heartbeatMs"`
	LeaseMs         int64  `json:"leaseMs"`
	StopMarginMs    int64  `json:"stopMarginMs"`
	ProtocolVersion int    `json:"protocolVersion"`
	Draining        bool   `json:"draining"`
}

type RunnerCapabilities struct {
	OS           string                 `json:"os"`
	Arch         string                 `json:"arch"`
	CPUs         int                    `json:"cpus"`
	MemMB        int64                  `json:"memMb"`
	DiskFreeMB   int64                  `json:"diskFreeMb"`
	Slots        int                    `json:"slots"`
	Profiles     []ExecutionProfile     `json:"profiles"`
	Providers    []ProviderInstallation `json:"providers"`
	Toolchains   map[string]string      `json:"toolchains"`
	Replicas     []string               `json:"replicas"`
	ServiceState string                 `json:"serviceState"`
	// Workspaces are the per-job worktrees and review snapshots on disk.
	Workspaces []WorkspaceInfo `json:"workspaces"`
}

// WorkspaceInfo describes one workspace directory on a runner.
type WorkspaceInfo struct {
	Name    string `json:"name"` // directory name under the runner's work dir
	Kind    string `json:"kind"` // job | review | scratch
	Ref     string `json:"ref"`  // short job ID (job) or run ID (review, scratch)
	Branch  string `json:"branch,omitempty"`
	Head    string `json:"head,omitempty"`
	Changes int    `json:"changes"` // uncommitted files
	// SizeMB is SizeBytes in whole megabytes, rounded down (a tiny
	// workspace reports 0). Read the other size fields before showing it.
	SizeMB int64 `json:"sizeMb"`
	// SizeBytes is what the runner counted. SizeKnown is false when it
	// couldn't measure the workspace at all (then the size is unknown, not
	// zero). SizeApprox is true when it stopped counting early, on a very
	// large tree or unreadable parts: the size is then a lower bound.
	SizeBytes  int64     `json:"sizeBytes"`
	SizeKnown  bool      `json:"sizeKnown"`
	SizeApprox bool      `json:"sizeApprox,omitempty"`
	ModifiedAt time.Time `json:"modifiedAt"`
	InUse      bool      `json:"inUse"` // an active attempt is using it
}

type ActiveRun struct {
	RunID        string `json:"runId"`
	LeaseEpoch   int64  `json:"leaseEpoch"`
	State        string `json:"state"`
	LastActivity string `json:"lastActivity,omitempty"`
}

type Heartbeat struct {
	ActiveRuns []ActiveRun `json:"activeRuns"`
	FreeSlots  int         `json:"freeSlots"`
	DiskFreeMB int64       `json:"diskFreeMb"`
}

// LeaseRenewal renews (or refuses) leases in response to a heartbeat.
type LeaseRenewal struct {
	Leases []Lease `json:"leases"`
}

type Lease struct {
	RunID       string `json:"runId"`
	LeaseEpoch  int64  `json:"leaseEpoch"`
	ExpiresInMs int64  `json:"expiresInMs"`
	// Revoked means the hub no longer recognises this epoch: stop immediately.
	Revoked bool   `json:"revoked"`
	Reason  string `json:"reason,omitempty"`
}

// RepoSpec is the explicit repository scope of a run.
type RepoSpec struct {
	RepoID        string `json:"repoId"`
	Name          string `json:"name"`
	RemoteURL     string `json:"remoteUrl"`
	DefaultBranch string `json:"defaultBranch"`
	BaseRev       string `json:"baseRev,omitempty"`
	Branch        string `json:"branch,omitempty"`
	// SnapshotRev is set for read-only review snapshots of an exact revision.
	SnapshotRev string `json:"snapshotRev,omitempty"`
	// CheckpointArtifact restores a verified checkpoint bundle instead of the base.
	CheckpointArtifact *Artifact `json:"checkpointArtifact,omitempty"`
	// SourceBundle is the imported git bundle for a repository without a
	// remote; the replica is built (and refreshed) from it.
	SourceBundle *Artifact `json:"sourceBundle,omitempty"`
	// FetchRefs are extra remote refs to fetch when a revision isn't local,
	// e.g. "refs/pull/42/head" for a pull request under review.
	FetchRefs []string `json:"fetchRefs,omitempty"`
}

// ExecutionManifest is the pinned, immutable description of one run attempt.
type ExecutionManifest struct {
	RunID             string          `json:"runId"`
	JobID             string          `json:"jobId"`
	Attempt           int             `json:"attempt"`
	EngineerID        string          `json:"engineerId"`
	EngineerName      string          `json:"engineerName"`
	EngineerVersionID string          `json:"engineerVersionId"`
	Provider          string          `json:"provider"`
	Model             string          `json:"model,omitempty"`
	ProfileID         string          `json:"profileId,omitempty"`
	Mode              string          `json:"mode"`
	ExecutionProfile  string          `json:"executionProfile"`
	Repo              *RepoSpec       `json:"repo,omitempty"`
	ReviewArtifact    *Artifact       `json:"reviewArtifact,omitempty"`
	Instructions      string          `json:"instructions"`
	Prompt            string          `json:"prompt"`
	ResumeSessionID   string          `json:"resumeSessionId,omitempty"`
	ScopeFingerprint  string          `json:"scopeFingerprint"`
	Tools             []string        `json:"tools"`
	Checks            []string        `json:"checks"`
	Context           json.RawMessage `json:"context,omitempty"`
	TimeoutMs         int64           `json:"timeoutMs"`
	// FakeScript configures the deterministic fake provider.
	FakeScript json.RawMessage `json:"fakeScript,omitempty"`
}

type OfferRun struct {
	Manifest     ExecutionManifest `json:"manifest"`
	LeaseMs      int64             `json:"leaseMs"`
	StopMarginMs int64             `json:"stopMarginMs"`
	DeadlineAt   time.Time         `json:"deadlineAt"`
}

type RunAck struct {
	CommandID string `json:"commandId"`
	Accepted  bool   `json:"accepted"`
	Reason    string `json:"reason,omitempty"`
	State     string `json:"state"`
	Duplicate bool   `json:"duplicate"`
}

// CommandAck acknowledges non-run commands after the runner journals them.
type CommandAck struct {
	CommandID string          `json:"commandId"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

// RunEventKind values for runner-produced events.
const (
	RunEvStarted        = "started"
	RunEvStatus         = "status"
	RunEvMessageDelta   = "message_delta"
	RunEvMessage        = "message"
	RunEvToolStarted    = "tool_started"
	RunEvToolFinished   = "tool_finished"
	RunEvInputDelivered = "input_delivered"
	RunEvUsage          = "usage"
	RunEvWarning        = "warning"
	RunEvError          = "error"
	RunEvRateLimited    = "rate_limited"
	RunEvAuthRequired   = "auth_required"
	RunEvCheck          = "check"
	RunEvRevision       = "revision"
	RunEvCheckpoint     = "checkpoint"
	RunEvVendorSession  = "vendor_session"
	RunEvLeaseLost      = "lease_lost"
)

type RunEvent struct {
	Seq  int64           `json:"seq"`
	Kind string          `json:"kind"`
	Text string          `json:"text,omitempty"`
	Tool string          `json:"tool,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
	At   time.Time       `json:"at"`
}

// CheckRecord is evidence the runner produced by executing a command itself.
type CheckRecord struct {
	Name          string `json:"name"`
	Command       string `json:"command"`
	ExitCode      int    `json:"exitCode"`
	Revision      string `json:"revision"`
	DurationMs    int64  `json:"durationMs"`
	LogArtifactID string `json:"logArtifactId,omitempty"`
	LogHash       string `json:"logHash,omitempty"`
	Summary       string `json:"summary,omitempty"`
}

// RevisionRecord is a committed result the runner computed from git itself.
type RevisionRecord struct {
	RepoID         string `json:"repoId"`
	Branch         string `json:"branch"`
	Base           string `json:"base"`
	Head           string `json:"head"`
	DiffArtifactID string `json:"diffArtifactId"`
	DiffHash       string `json:"diffHash"`
	FilesChanged   int    `json:"filesChanged"`
	Insertions     int    `json:"insertions"`
	Deletions      int    `json:"deletions"`
	Summary        string `json:"summary"`
}

type CheckpointInfo struct {
	Head       string `json:"head"`
	ArtifactID string `json:"artifactId"`
	Hash       string `json:"hash"`
	Dirty      bool   `json:"dirty"`
	Untracked  int    `json:"untracked"`
}

const (
	OutcomeSucceeded    = "succeeded"
	OutcomeFailed       = "failed"
	OutcomeCancelled    = "cancelled"
	OutcomeRateLimited  = "rate_limited"
	OutcomeAuthRequired = "auth_required"
	OutcomeLeaseLost    = "lease_lost"
	OutcomeUnknown      = "unknown"
	OutcomeRejected     = "rejected"
)

type RunTerminal struct {
	Outcome         string          `json:"outcome"`
	Summary         string          `json:"summary,omitempty"`
	FinalText       string          `json:"finalText,omitempty"`
	Error           string          `json:"error,omitempty"`
	RetryAfterMs    int64           `json:"retryAfterMs,omitempty"`
	VendorSessionID string          `json:"vendorSessionId,omitempty"`
	Usage           *Usage          `json:"usage,omitempty"`
	Checkpoint      *CheckpointInfo `json:"checkpoint,omitempty"`
	LastSeq         int64           `json:"lastSeq"`
	ExitConfirmed   bool            `json:"exitConfirmed"`
}

// ToolCall is an agent tool invocation forwarded from the local bridge. The
// hub binds caller identity, run, and scope from the authenticated run lease;
// arguments never carry authority.
type ToolCall struct {
	CallID string          `json:"callId"`
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
}

type ToolResult struct {
	CallID string          `json:"callId"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *APIError       `json:"error,omitempty"`
}

type ApprovalRequest struct {
	RequestID string          `json:"requestId"`
	Action    ApprovalAction  `json:"action"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

type ResolveApproval struct {
	RequestID  string `json:"requestId"`
	ApprovalID string `json:"approvalId,omitempty"`
	Decision   string `json:"decision"` // allow | deny
	Reason     string `json:"reason,omitempty"`
}

type DeliverInput struct {
	InputID string `json:"inputId"`
	Text    string `json:"text"`
}

// InputDelivered reports what actually happened to steered input.
type InputDelivered struct {
	InputID string `json:"inputId"`
	Mode    string `json:"mode"` // immediate | queued | unsupported
	Detail  string `json:"detail,omitempty"`
}

type CancelRun struct {
	Reason  string `json:"reason"`
	GraceMs int64  `json:"graceMs"`
}

// Ack acknowledges committed runner events so the runner can drop them.
type Ack struct {
	RunID   string `json:"runId"`
	UpToSeq int64  `json:"upToSeq"`
	// Terminal is set only when the hub committed (or deliberately
	// discarded) the run's terminal report.
	Terminal bool `json:"terminal,omitempty"`
}

type ArtifactReady struct {
	Artifact Artifact `json:"artifact"`
}

type Reconcile struct {
	// Runs the hub believes this node holds, with their current epochs.
	Runs []Lease `json:"runs"`
}

// CleanupWorkspace asks a runner to delete one workspace. The runner
// refuses one in use, and one with uncommitted changes unless Force.
type CleanupWorkspace struct {
	Workspace string `json:"workspace"`
	Force     bool   `json:"force"`
}
