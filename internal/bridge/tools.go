// Package bridge implements yip's agent-facing tools.
//
// A provider CLI launches `yip bridge` as a stdio MCP server. The bridge
// forwards each tool call over a local socket to the runner, which binds it
// to the run's authority (run ID, lease epoch, engineer, scope) before
// executing it locally or forwarding it to the hub. Identity and scope are
// never model-writable parameters.
package bridge

import (
	"encoding/json"
	"slices"

	"github.com/binbandit/yip/protocol"
)

// Tool describes one agent-facing tool.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Local tools execute in the runner against the run's workspace.
	Local bool `json:"-"`
	// Modes lists the run modes allowed to call the tool.
	Modes []string `json:"-"`
}

var (
	all      = []string{protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation}
	editOnly = []string{protocol.ModeEdit}
	notRO    = []string{protocol.ModeEdit, protocol.ModeConversation}
	roOnly   = []string{protocol.ModeReadOnly}
	// workModes are runs with a repository workspace (not conversation replies).
	workModes = []string{protocol.ModeEdit, protocol.ModeReadOnly}
)

// Tool names. MCP clients require [a-zA-Z0-9_-] names, so the spec's dotted
// names (room.post, work.create …) use underscores here.
const (
	RoomRead           = "room_read"
	RoomPost           = "room_post"
	WorkCreate         = "work_create"
	WorkUpdate         = "work_update"
	WorkRequestHelp    = "work_request_help"
	WorkRespond        = "work_respond"
	WorkRequestReview  = "work_request_review"
	WorkReview         = "work_review"
	WorkRespondReview  = "work_respond_to_review"
	WorkWait           = "work_wait"
	WorkStatus         = "work_status"
	HumanAsk           = "human_ask"
	DecisionPropose    = "decision_propose"
	KnowledgeSearch    = "knowledge_search"
	NoteRecord         = "note_record"
	WorkAddInput       = "work_add_input"
	ForgeReadPR        = "forge_read_pr"
	ForgeLinkPR        = "forge_link_pr"
	ForgePublishReview = "forge_publish_review"
	WorkRunCheck       = "work_run_check"
	WorkPublishRev     = "work_publish_revision"
	ArtifactPublish    = "artifact_publish"
	PermissionPrompt   = "permission_prompt"
)

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// Tools is the complete tool catalogue.
var Tools = []Tool{
	{Name: RoomRead, Modes: all, Description: "Read recent messages from the conversation this run replies to (its room or thread only). Returns messages with IDs you can cite.",
		InputSchema: schema(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":100},"beforeSeq":{"type":"integer"}},"additionalProperties":false}`)},
	{Name: RoomPost, Modes: all, Description: "Post a message to this run's conversation. Use for meaningful updates, findings, or a reply. Mentions are rendered for readability; they do not wake anyone. To ask a colleague use work_request_help, to get review use work_request_review, to ask the human use human_ask.",
		InputSchema: schema(`{"type":"object","required":["body"],"properties":{"body":{"type":"string","maxLength":20000},"mentions":{"type":"array","items":{"type":"string"},"description":"handles of room members to mention"}},"additionalProperties":false}`)},
	{Name: WorkCreate, Modes: notRO, Description: "Create accountable work: a code change, investigation, or document. From a conversation, this starts a job you own. From a job, it creates a child job; set owner to a colleague's handle to delegate bounded work. Code jobs need exactly one repository.",
		InputSchema: schema(`{"type":"object","required":["title","objective","kind"],"properties":{"title":{"type":"string","maxLength":120},"objective":{"type":"string"},"kind":{"type":"string","enum":["code","investigation","document"]},"acceptance":{"type":"array","items":{"type":"string"}},"project":{"type":"string","description":"project name or ID"},"repo":{"type":"string","description":"repository name or ID (required for code)"},"owner":{"type":"string","description":"handle of the owner; defaults to you"},"requireHumanReview":{"type":"boolean"}},"additionalProperties":false}`)},
	{Name: WorkUpdate, Modes: all, Description: "Report progress or propose a state for your current job. state=completed asks the hub to complete the job: it verifies the published revision, recorded checks, and required reviews, and tells you exactly what evidence is missing. state=failed records that the objective cannot be met, with the reason.",
		InputSchema: schema(`{"type":"object","properties":{"progress":{"type":"string","description":"short last-confirmed activity"},"state":{"type":"string","enum":["completed","failed"]},"summary":{"type":"string","description":"what changed, assumptions made, limitations"}},"additionalProperties":false}`)},
	{Name: WorkRequestHelp, Modes: all, Description: "Ask a named colleague a bounded question or for a bounded piece of work. It is posted visibly in this conversation and wakes only that colleague. If you cannot continue without the answer, call work_wait afterwards so you stop holding capacity.",
		InputSchema: schema(`{"type":"object","required":["to","question"],"properties":{"to":{"type":"string","description":"colleague handle"},"question":{"type":"string"},"expectedOutput":{"type":"string"},"evidence":{"type":"string"}},"additionalProperties":false}`)},
	{Name: WorkRespond, Modes: all, Description: "Answer a specific help request addressed to you. This resolves the request and returns your answer to the requester.",
		InputSchema: schema(`{"type":"object","required":["requestId","body"],"properties":{"requestId":{"type":"string"},"body":{"type":"string"},"artifactIds":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`)},
	{Name: WorkRequestReview, Modes: editOnly, Description: "Ask a distinct, suitable colleague to review your current work. The review is bound to your latest published revision (call work_publish_revision first) or to a pull request. Choose the reviewer yourself by expertise and availability; the human does not assign reviewers.",
		InputSchema: schema(`{"type":"object","required":["reviewer","message"],"properties":{"reviewer":{"type":"string","description":"colleague handle"},"criteria":{"type":"string","description":"what the reviewer should focus on"},"message":{"type":"string","description":"the request as posted in the conversation"},"pullRequest":{"type":"integer","description":"review a linked PR instead of the published revision"}},"additionalProperties":false}`)},
	{Name: WorkReview, Modes: roOnly, Description: "Record your review verdict for the exact revision you inspected. Blocking findings must cite file/line or test evidence. Use approved only when you verified the change; comments_only does not satisfy a required approval; unable_to_review when access or checks were missing (never approve what you could not check).",
		InputSchema: schema(`{"type":"object","required":["verdict","summary"],"properties":{"verdict":{"type":"string","enum":["approved","changes_requested","comments_only","unable_to_review"]},"summary":{"type":"string"},"expectedHead":{"type":"string","description":"the exact Git revision you reviewed (code)"},"expectedHash":{"type":"string","description":"the exact SHA-256 of the supplied review-artifact (documents)"},"findings":{"type":"array","items":{"type":"object","required":["severity","body"],"properties":{"severity":{"type":"string","enum":["blocking","suggestion","note"]},"body":{"type":"string"},"file":{"type":"string"},"line":{"type":"integer"},"evidence":{"type":"string"}}}},"resolve":{"type":"array","items":{"type":"string"},"description":"IDs of earlier findings you verified as resolved"},"message":{"type":"string","description":"what to say in the conversation"}},"additionalProperties":false}`)},
	{Name: WorkRespondReview, Modes: editOnly, Description: "Respond to review findings on your work: reply to each finding with evidence (or dispute it), and request another review round on your newly published revision. You cannot dismiss a reviewer's blocking finding; only the reviewer resolves it.",
		InputSchema: schema(`{"type":"object","required":["responses"],"properties":{"responses":{"type":"array","items":{"type":"object","required":["findingId","body"],"properties":{"findingId":{"type":"string"},"body":{"type":"string"},"evidence":{"type":"string"},"disputed":{"type":"boolean"}}}},"requestRereview":{"type":"boolean"},"message":{"type":"string"}},"additionalProperties":false}`)},
	{Name: WorkWait, Modes: all, Description: "Record that your job cannot make further useful progress until something happens (an answer, a review, a colleague's help). After calling this, finish your turn: your capacity is released and you will be resumed automatically when the dependency resolves.",
		InputSchema: schema(`{"type":"object","required":["reason"],"properties":{"reason":{"type":"string","enum":["review","missing_information","dependency","approval"]},"detail":{"type":"string"}},"additionalProperties":false}`)},
	{Name: WorkStatus, Modes: all, Description: "Read the work ledger visible from this conversation: jobs, owners, states, last confirmed activity, blockers. Reading status never wakes other engineers.",
		InputSchema: schema(`{"type":"object","properties":{"project":{"type":"string"},"includeCompleted":{"type":"boolean"}},"additionalProperties":false}`)},
	{Name: HumanAsk, Modes: all, Description: "Ask the human one genuine question in this conversation, as a colleague would. Only use this after checking instructions, code, decisions, docs, and colleagues. Say what you checked and what you will continue doing meanwhile; only the dependent step waits.",
		InputSchema: schema(`{"type":"object","required":["question","missingFact","contextChecked"],"properties":{"question":{"type":"string","description":"the message to post"},"missingFact":{"type":"string"},"contextChecked":{"type":"string"},"dependentStep":{"type":"string"},"continuingWith":{"type":"string"}},"additionalProperties":false}`)},
	{Name: DecisionPropose, Modes: all, Description: "Record a scoped, sourced decision (e.g. an implementation contract you established). Cite the message or job IDs it came from. It becomes shared knowledge only where its sources are visible.",
		InputSchema: schema(`{"type":"object","required":["title","body","sources"],"properties":{"title":{"type":"string"},"body":{"type":"string"},"project":{"type":"string"},"sources":{"type":"array","items":{"type":"string"},"description":"message or job IDs"},"supersedes":{"type":"string"}},"additionalProperties":false}`)},
	{Name: WorkAddInput, Modes: []string{protocol.ModeConversation}, Description: "Add the message you're replying to to one of your open assignments in this conversation (a clarification, a constraint, a change of scope). It's delivered to that work as the owner's input: immediately if the work can take it, otherwise before its next step. Afterwards end your turn without text; the owner sees a receipt on their message.",
		InputSchema: schema(`{"type":"object","required":["job"],"properties":{"job":{"type":"string","description":"the job ID of your open assignment"}},"additionalProperties":false}`)},
	{Name: NoteRecord, Modes: all, Description: "Keep a short note for your own future work: a conclusion with where it came from (e.g. \"Atlas's integration tests need the fake clock; see job X\"). Not a transcript, and not your reasoning. Cite message or job IDs; the note is used only where those sources are visible. Notes from your own finished work are kept automatically; others wait as suggestions for the owner. Supersede an outdated note instead of adding a new one.",
		InputSchema: schema(`{"type":"object","required":["body","sources"],"properties":{"body":{"type":"string","maxLength":400},"project":{"type":"string"},"sources":{"type":"array","items":{"type":"string"},"description":"message or job IDs"},"supersedes":{"type":"string","description":"the ID of your note this replaces"}},"additionalProperties":false}`)},
	{Name: KnowledgeSearch, Modes: all, Description: "Search accepted decisions and conversation history visible from this conversation. Results carry source IDs you can cite.",
		InputSchema: schema(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":25}},"additionalProperties":false}`)},
	{Name: ForgeReadPR, Modes: all, Description: "Read a pull request on the job's repository: canonical URL, base/head revisions, changed files, remote reviews, checks, and merge status as reported by the forge.",
		InputSchema: schema(`{"type":"object","properties":{"number":{"type":"integer"},"url":{"type":"string"},"includeDiff":{"type":"boolean"}},"additionalProperties":false}`)},
	{Name: ForgeLinkPR, Modes: editOnly, Description: "Link an existing pull request to your job so its reviews, checks, and merge state are tracked.",
		InputSchema: schema(`{"type":"object","properties":{"number":{"type":"integer"},"url":{"type":"string"}},"additionalProperties":false}`)},
	{Name: ForgePublishReview, Modes: roOnly, Description: "Publish your recorded review verdict to the linked pull request through the project's authorized forge credential. It is revision-bound and refused if the PR moved, if the credential is not eligible, or if publication is not granted.",
		InputSchema: schema(`{"type":"object","properties":{"body":{"type":"string"}},"additionalProperties":false}`)},
	{Name: WorkRunCheck, Modes: workModes, Local: true, Description: "Run a check command (tests, build, lint) in your workspace. The runner executes it and records the command, exit status, revision, and log as evidence. Use this for any check you want counted. Routine checks run straight away; commands that push, publish, reach the network, or leave the workspace need the owner's approval. Checks run with a scratch home directory and no access to the machine's credentials.",
		InputSchema: schema(`{"type":"object","required":["command"],"properties":{"name":{"type":"string"},"command":{"type":"string"},"timeoutSeconds":{"type":"integer","minimum":1,"maximum":3600}},"additionalProperties":false}`)},
	{Name: WorkPublishRev, Modes: editOnly, Local: true, Description: "Commit any outstanding changes in your workspace and publish the result revision: the runner records base/head, the diff, and a portable bundle so reviewers inspect exactly this revision.",
		InputSchema: schema(`{"type":"object","required":["summary"],"properties":{"summary":{"type":"string","description":"commit message / change summary"}},"additionalProperties":false}`)},
	{Name: ArtifactPublish, Modes: all, Local: true, Description: "Publish a file from your workspace (report, diagram, document) as an artifact attached to your job.",
		InputSchema: schema(`{"type":"object","required":["path"],"properties":{"path":{"type":"string","description":"path relative to the workspace root"},"name":{"type":"string"},"kind":{"type":"string","enum":["document","file"]}},"additionalProperties":false}`)},
	{Name: PermissionPrompt, Modes: all, Local: true, Description: "Permission prompt handler used by the provider CLI. Do not call directly.",
		InputSchema: schema(`{"type":"object","properties":{"tool_name":{"type":"string"},"input":{"type":"object"},"tool_use_id":{"type":"string"}},"additionalProperties":true}`)},
}

// Lookup returns a tool by name.
func Lookup(name string) (Tool, bool) {
	for _, t := range Tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// Allowed reports whether a mode may call a tool.
func (t Tool) Allowed(mode string) bool { return slices.Contains(t.Modes, mode) }

// NamesForMode lists tools available to a run mode.
func NamesForMode(mode string) []string {
	var out []string
	for _, t := range Tools {
		if t.Allowed(mode) {
			out = append(out, t.Name)
		}
	}
	return out
}

// ---- argument types (shared by hub and runner) ----

type RoomReadArgs struct {
	Limit     int   `json:"limit"`
	BeforeSeq int64 `json:"beforeSeq"`
}

type RoomPostArgs struct {
	Body     string   `json:"body"`
	Mentions []string `json:"mentions"`
}

type WorkCreateArgs struct {
	Title              string   `json:"title"`
	Objective          string   `json:"objective"`
	Kind               string   `json:"kind"`
	Acceptance         []string `json:"acceptance"`
	Project            string   `json:"project"`
	Repo               string   `json:"repo"`
	Owner              string   `json:"owner"`
	RequireHumanReview bool     `json:"requireHumanReview"`
}

type WorkUpdateArgs struct {
	Progress string `json:"progress"`
	State    string `json:"state"`
	Summary  string `json:"summary"`
}

type WorkRequestHelpArgs struct {
	To             string `json:"to"`
	Question       string `json:"question"`
	ExpectedOutput string `json:"expectedOutput"`
	Evidence       string `json:"evidence"`
}

type WorkRespondArgs struct {
	RequestID   string   `json:"requestId"`
	Body        string   `json:"body"`
	ArtifactIDs []string `json:"artifactIds"`
}

type WorkRequestReviewArgs struct {
	Reviewer    string `json:"reviewer"`
	Criteria    string `json:"criteria"`
	Message     string `json:"message"`
	PullRequest int    `json:"pullRequest"`
}

type ReviewFindingArg struct {
	Severity string `json:"severity"`
	Body     string `json:"body"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Evidence string `json:"evidence"`
}

type WorkReviewArgs struct {
	Verdict      string             `json:"verdict"`
	Summary      string             `json:"summary"`
	ExpectedHead string             `json:"expectedHead"`
	ExpectedHash string             `json:"expectedHash"`
	Findings     []ReviewFindingArg `json:"findings"`
	Resolve      []string           `json:"resolve"`
	Message      string             `json:"message"`
}

type FindingResponseArg struct {
	FindingID string `json:"findingId"`
	Body      string `json:"body"`
	Evidence  string `json:"evidence"`
	Disputed  bool   `json:"disputed"`
}

type WorkRespondReviewArgs struct {
	Responses       []FindingResponseArg `json:"responses"`
	RequestRereview bool                 `json:"requestRereview"`
	Message         string               `json:"message"`
}

type WorkWaitArgs struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

type WorkStatusArgs struct {
	Project          string `json:"project"`
	IncludeCompleted bool   `json:"includeCompleted"`
}

type HumanAskArgs struct {
	Question       string `json:"question"`
	MissingFact    string `json:"missingFact"`
	ContextChecked string `json:"contextChecked"`
	DependentStep  string `json:"dependentStep"`
	ContinuingWith string `json:"continuingWith"`
}

type DecisionProposeArgs struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Project    string   `json:"project"`
	Sources    []string `json:"sources"`
	Supersedes string   `json:"supersedes"`
}

type WorkAddInputArgs struct {
	Job string `json:"job"`
}

type NoteRecordArgs struct {
	Body       string   `json:"body"`
	Project    string   `json:"project"`
	Sources    []string `json:"sources"`
	Supersedes string   `json:"supersedes"`
}

type KnowledgeSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type ForgeReadPRArgs struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	IncludeDiff bool   `json:"includeDiff"`
}

type ForgePublishReviewArgs struct {
	Body string `json:"body"`
}

type WorkRunCheckArgs struct {
	Name           string `json:"name"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type WorkPublishRevArgs struct {
	Summary string `json:"summary"`
}

type ArtifactPublishArgs struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// PermissionPromptArgs is the payload Claude Code sends to a permission-prompt tool.
type PermissionPromptArgs struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

// Hub-side tool call results carried back from the runner for local tools.

// RecordCheck is sent from runner to hub (tool "_record_check") after a
// runner-executed check, so the hub persists runner-produced evidence.
const RecordCheck = "_record_check"

// RecordRevision is sent after the runner publishes a revision.
const RecordRevision = "_record_revision"

// RecordArtifact registers an uploaded artifact with the job.
const RecordArtifact = "_record_artifact"

// RequestApproval asks the hub's policy for a permission decision.
const RequestApproval = "_request_approval"
