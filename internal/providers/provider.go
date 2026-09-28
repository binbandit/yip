// Package providers defines the contract every provider adapter implements.
//
// An adapter converts one vendor's supported programmatic interface (Codex
// app-server, Claude Code's structured CLI, Cursor ACP, or the deterministic
// fake) into yip's internal run events. Adapters run inside the runner, never
// in the hub. They must use documented interfaces only: no terminal scraping,
// no token extraction, no spoofed clients, and no blanket permission bypass.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/binbandit/yip/protocol"
)

// ErrUnsupported is returned when an adapter cannot perform an operation on
// the installed provider version. Callers must surface the limitation.
var ErrUnsupported = errors.New("operation not supported by this provider")

// Event kinds an adapter may emit. They mirror protocol.RunEv* plus two
// adapter-level blocking requests.
const (
	EventMessageDelta  = protocol.RunEvMessageDelta // partial assistant text (coalesced by the runner)
	EventMessage       = protocol.RunEvMessage      // a complete assistant message
	EventToolStarted   = protocol.RunEvToolStarted
	EventToolFinished  = protocol.RunEvToolFinished
	EventStatus        = protocol.RunEvStatus // short human-readable progress ("Running go test")
	EventUsage         = protocol.RunEvUsage
	EventWarning       = protocol.RunEvWarning
	EventError         = protocol.RunEvError
	EventRateLimited   = protocol.RunEvRateLimited
	EventAuthRequired  = protocol.RunEvAuthRequired
	EventVendorSession = protocol.RunEvVendorSession
	// EventApprovalRequest is a blocking provider permission request. The
	// runner MUST answer it with Session.ResolveApproval.
	EventApprovalRequest = "approval_request"
	// EventQuestion is a blocking provider question (e.g. Cursor ask/plan
	// extensions). The runner MUST answer it with Session.AnswerQuestion.
	EventQuestion = "question"
)

// Event is one normalized adapter event.
type Event struct {
	Kind            string
	Text            string
	Tool            string
	Data            json.RawMessage
	Approval        *ApprovalRequest
	Question        *Question
	Usage           *protocol.Usage
	RetryAfter      time.Duration
	VendorSessionID string
	At              time.Time
}

// ApprovalRequest is a provider-originated permission prompt.
type ApprovalRequest struct {
	ID     string                  // adapter-scoped request ID
	Action protocol.ApprovalAction // normalized description of the exact action
	Raw    json.RawMessage         // vendor payload, kept for audit
}

type ApprovalDecision struct {
	Allow  bool
	Reason string
}

// Question is a blocking provider question addressed to "the user".
type Question struct {
	ID      string
	Kind    string // ask | plan | other
	Text    string
	Options []string
	Raw     json.RawMessage
}

type QuestionAnswer struct {
	// Text is a free-form answer. Selected is an option index when the
	// question offered options (-1 when not applicable).
	Text     string
	Selected int
	// Declined tells the provider no synchronous answer is available.
	Declined bool
}

// MCPServer describes the stdio MCP server (the yip bridge) the provider must
// load. It is the only MCP server yip configures for a run.
type MCPServer struct {
	Name    string // always "yip"
	Command string
	Args    []string
	Env     map[string]string
}

// StartSpec is everything an adapter needs to begin one run attempt.
type StartSpec struct {
	RunID   string
	Workdir string // absolute, runner-resolved workspace root
	// Mode is protocol.ModeEdit, ModeReadOnly, or ModeConversation. Adapters
	// must enforce read-only with the provider's own permission/sandbox
	// controls, or refuse to start (return an error wrapping ErrUnsupported).
	Mode            string
	Model           string
	Instructions    string // engineer role, org and project rules
	Prompt          string // the first user turn, built from the context manifest
	ResumeSessionID string // vendor session to resume; empty for a fresh session
	MCP             MCPServer
	// PermissionTool is the MCP tool name (without server prefix) the bridge
	// exposes for permission prompts, for providers that route approvals via MCP.
	PermissionTool string
	// Env is the allowlisted launch environment. Adapters must not add the
	// hub's or runner's own environment beyond this list.
	Env        []string
	Executable string // optional explicit executable path override
	Timeout    time.Duration
	// FakeScript is only read by the fake provider.
	FakeScript json.RawMessage
	// TrustProviderRules is the owner's permission for the provider's own
	// always-allow rules to apply when the run is read-only.
	TrustProviderRules bool
}

// Result is the terminal outcome of a session.
type Result struct {
	Outcome         string // protocol.Outcome*
	FinalText       string
	Error           string
	RetryAfter      time.Duration
	VendorSessionID string
	Usage           *protocol.Usage
	// ExitConfirmed is true only when the provider process group is known to
	// have exited. False means the runner must report uncertainty.
	ExitConfirmed bool
}

// Session is one running provider attempt.
type Session interface {
	// Events streams normalized events. It is closed when the session ends.
	Events() <-chan Event
	// SendInput delivers steering input. It returns the truthful delivery
	// mode: "immediate" (delivered into the active turn), "queued" (will be
	// read at the next turn boundary), or ErrUnsupported.
	SendInput(ctx context.Context, text string) (mode string, err error)
	ResolveApproval(ctx context.Context, id string, d ApprovalDecision) error
	AnswerQuestion(ctx context.Context, id string, a QuestionAnswer) error
	// Cancel stops the attempt: interrupt via the provider protocol if
	// available, then terminate the process group.
	Cancel(ctx context.Context) error
	// Wait blocks until the session ends and returns its outcome.
	Wait() Result
}

// Adapter is a provider integration.
type Adapter interface {
	Name() string
	// Label is the user-facing provider name ("Claude Code").
	Label() string
	// Probe discovers the installed version, authentication state (without
	// reading credential files), capabilities, and models.
	Probe(ctx context.Context) protocol.ProviderInstallation
	Start(ctx context.Context, spec StartSpec) (Session, error)
}

// RunSummary describes a command for an approval request in one line: its
// first line (a heredoc's body stays in the full command), shortened.
func RunSummary(cmd string) string {
	line, rest, _ := strings.Cut(strings.TrimSpace(cmd), "\n")
	more := strings.TrimSpace(rest) != ""
	if r := []rune(line); len(r) > 160 {
		line, more = string(r[:160]), true
	}
	if more {
		line += " …"
	}
	return "Run `" + line + "`"
}
