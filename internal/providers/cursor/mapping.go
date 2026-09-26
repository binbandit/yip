package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

// ---------- tool calls and approvals ----------

// isWriteKind reports ACP tool kinds that modify the filesystem.
func isWriteKind(kind string) bool {
	switch kind {
	case acp.KindEdit, acp.KindDelete, acp.KindMove:
		return true
	}
	return false
}

// deniedInReadOnly reports kinds yip refuses in readonly/conversation mode.
// Commands are refused too: a shell command can modify files, and yip runs
// verification checks itself.
func deniedInReadOnly(kind string) bool {
	return isWriteKind(kind) || kind == acp.KindExecute || kind == acp.KindSwitchMode
}

// approvalKind normalises an ACP tool kind into protocol.ApprovalAction.Kind.
func approvalKind(tc acp.ToolCall) string {
	switch tc.Kind {
	case acp.KindEdit, acp.KindDelete, acp.KindMove:
		return "edit"
	case acp.KindExecute:
		return "exec"
	case acp.KindFetch:
		return "network"
	}
	if looksLikeMCP(tc) {
		return "mcp"
	}
	return "other"
}

func looksLikeMCP(tc acp.ToolCall) bool {
	n := strings.ToLower(tc.Name)
	t := strings.ToLower(tc.Title)
	return strings.HasPrefix(n, "mcp") || strings.Contains(n, "mcp__") || strings.Contains(t, "mcp")
}

// approvalAction builds the exact-action description shown to the approver.
func approvalAction(tc acp.ToolCall) protocol.ApprovalAction {
	a := protocol.ApprovalAction{Kind: approvalKind(tc)}
	a.Command = extractCommand(tc.RawInput)
	targets := toolTargets(tc)
	if len(targets) > 0 {
		a.Target = targets[0]
	}
	a.Summary = oneLine(tc.Title, 200)
	if a.Summary == "" {
		a.Summary = defaultSummary(tc, a)
	}
	var detail []string
	if tc.Kind != "" {
		detail = append(detail, "Cursor tool kind: "+tc.Kind)
	}
	if tc.Name != "" {
		detail = append(detail, "tool: "+tc.Name)
	}
	if len(targets) > 1 {
		detail = append(detail, "files: "+strings.Join(targets, ", "))
	}
	for _, c := range tc.Content {
		if c.Type == "diff" && c.Path != "" {
			oldN := 0
			if c.OldText != nil {
				oldN = countLines(*c.OldText)
			}
			detail = append(detail, fmt.Sprintf("diff %s: %d → %d lines", c.Path, oldN, countLines(c.NewText)))
		}
	}
	if len(tc.RawInput) > 0 && string(tc.RawInput) != "null" && a.Command == "" {
		detail = append(detail, "input: "+oneLine(string(tc.RawInput), 400))
	}
	a.Detail = strings.Join(detail, "\n")
	return a
}

func defaultSummary(tc acp.ToolCall, a protocol.ApprovalAction) string {
	switch {
	case a.Command != "":
		return "Run: " + oneLine(a.Command, 160)
	case tc.Kind == acp.KindEdit && a.Target != "":
		return "Edit " + a.Target
	case tc.Kind == acp.KindDelete && a.Target != "":
		return "Delete " + a.Target
	case tc.Kind == acp.KindFetch && a.Target != "":
		return "Fetch " + a.Target
	case tc.Name != "":
		return "Use tool " + tc.Name
	case tc.Kind != "":
		return "Cursor " + tc.Kind + " tool"
	}
	return "Cursor tool call"
}

// extractCommand pulls a shell command from a tool's raw input, if any.
func extractCommand(raw json.RawMessage) string {
	m := rawObject(raw)
	if m == nil {
		return ""
	}
	for _, k := range []string{"command", "cmd", "commandLine", "command_line", "script"} {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case string:
			cmd := t
			if args, ok := m["args"].([]any); ok {
				cmd += " " + joinAny(args)
			}
			return cmd
		case []any:
			return joinAny(t)
		}
	}
	return ""
}

// toolTargets lists the files or URLs a tool call names (locations, diffs,
// and well-known raw input keys), de-duplicated in order.
func toolTargets(tc acp.ToolCall) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, l := range tc.Locations {
		add(l.Path)
	}
	for _, c := range tc.Content {
		if c.Type == "diff" {
			add(c.Path)
		}
	}
	if m := rawObject(tc.RawInput); m != nil {
		for _, k := range []string{"path", "file_path", "filePath", "target_file", "targetFile", "file", "new_path", "newPath", "destination", "source", "url", "uri"} {
			if s, ok := m[k].(string); ok {
				add(s)
			}
		}
		for _, k := range []string{"paths", "files"} {
			if arr, ok := m[k].([]any); ok {
				for _, v := range arr {
					if s, ok := v.(string); ok {
						add(s)
					}
				}
			}
		}
	}
	return out
}

// pathTargets returns the targets that are filesystem paths (not URLs).
func pathTargets(tc acp.ToolCall) []string {
	var out []string
	for _, t := range toolTargets(tc) {
		if strings.Contains(t, "://") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// outsideWorkdir returns the first path that resolves outside root.
// root must already be symlink-resolved.
func outsideWorkdir(root string, paths []string) (string, bool) {
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		r := resolvePath(abs)
		if r != root && !strings.HasPrefix(r, root+string(filepath.Separator)) {
			return p, true
		}
	}
	return "", false
}

// resolvePath cleans p and resolves symlinks on its longest existing prefix
// (so /var vs /private/var on macOS compare correctly for new files).
func resolvePath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		base := filepath.Base(cur)
		if rest == "" {
			rest = base
		} else {
			rest = filepath.Join(base, rest)
		}
		cur = parent
	}
}

func findOption(opts []acp.PermissionOption, kind, fallbackID string) string {
	for _, o := range opts {
		if o.Kind == kind {
			return o.OptionID
		}
	}
	for _, o := range opts {
		if o.OptionID == fallbackID {
			return o.OptionID
		}
	}
	return ""
}

// ---------- Cursor extension requests ----------

// Cursor extension methods (https://cursor.com/docs/cli/acp#cursor-extension-methods).
const (
	methodAskQuestion   = "cursor/ask_question"   // blocking
	methodCreatePlan    = "cursor/create_plan"    // blocking
	methodUpdateTodos   = "cursor/update_todos"   // notification
	methodTask          = "cursor/task"           // notification
	methodGenerateImage = "cursor/generate_image" // notification
)

type cursorOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type cursorQuestion struct {
	ID            string         `json:"id"`
	Prompt        string         `json:"prompt"`
	Options       []cursorOption `json:"options"`
	AllowMultiple bool           `json:"allowMultiple,omitempty"`
}

type askQuestionParams struct {
	ToolCallID string           `json:"toolCallId"`
	Title      string           `json:"title,omitempty"`
	Questions  []cursorQuestion `json:"questions"`
}

type cursorTodo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type createPlanParams struct {
	ToolCallID string       `json:"toolCallId"`
	Name       string       `json:"name,omitempty"`
	Overview   string       `json:"overview,omitempty"`
	Plan       string       `json:"plan"`
	Todos      []cursorTodo `json:"todos"`
	IsProject  bool         `json:"isProject,omitempty"`
	Phases     []struct {
		Name  string       `json:"name"`
		Todos []cursorTodo `json:"todos"`
	} `json:"phases,omitempty"`
}

type updateTodosParams struct {
	ToolCallID string       `json:"toolCallId"`
	Todos      []cursorTodo `json:"todos"`
	Merge      bool         `json:"merge"`
}

type taskParams struct {
	ToolCallID   string          `json:"toolCallId"`
	Description  string          `json:"description"`
	Prompt       string          `json:"prompt"`
	SubagentType json.RawMessage `json:"subagentType"`
	Model        string          `json:"model,omitempty"`
	DurationMs   *int64          `json:"durationMs,omitempty"`
}

type generateImageParams struct {
	ToolCallID  string `json:"toolCallId"`
	Description string `json:"description"`
	FilePath    string `json:"filePath,omitempty"`
}

// Reply shapes. The ask reply always nests {"outcome": {...}} exactly as
// documented for CursorAskQuestionResponse / CursorCreatePlanResponse.
type askAnswer struct {
	QuestionID        string   `json:"questionId"`
	SelectedOptionIDs []string `json:"selectedOptionIds"`
}

type askOutcome struct {
	Outcome string      `json:"outcome"` // answered | skipped | cancelled
	Answers []askAnswer `json:"answers,omitempty"`
	Reason  string      `json:"reason,omitempty"`
}

type planOutcome struct {
	Outcome string `json:"outcome"` // accepted | rejected | cancelled
	PlanURI string `json:"planUri,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type outcomeReply[T any] struct {
	Outcome T `json:"outcome"`
}

const (
	declinedAskReason  = "No synchronous answer is available: the question has been posted to the user in yip and may be answered later. Continue with your best judgement and state the assumption you made, or finish and report that you are blocked on this question."
	declinedPlanReason = "No synchronous plan approval is available in this session. Do not treat the plan as approved; continue only with work that does not depend on the approval, or finish and report the plan for review."
)

type flatOption struct {
	questionID string
	optionID   string
	label      string
}

// askQuestionView renders a Cursor ask_question request for yip.
func askQuestionView(p askQuestionParams) (text string, options []string, flat []flatOption) {
	var b strings.Builder
	if p.Title != "" {
		b.WriteString(p.Title)
	}
	multi := len(p.Questions) > 1
	for i, q := range p.Questions {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		if multi {
			fmt.Fprintf(&b, "%d. ", i+1)
		}
		b.WriteString(q.Prompt)
		if q.AllowMultiple {
			b.WriteString(" (multiple choices allowed)")
		}
		for _, o := range q.Options {
			label := o.Label
			if label == "" {
				label = o.ID
			}
			shown := label
			if multi {
				shown = fmt.Sprintf("%s — %s", q.Prompt, label)
			}
			options = append(options, shown)
			flat = append(flat, flatOption{questionID: q.ID, optionID: o.ID, label: label})
		}
	}
	return b.String(), options, flat
}

// answerAsk converts a yip answer into the documented ask_question reply.
func answerAsk(p askQuestionParams, flat []flatOption, a answerInput) outcomeReply[askOutcome] {
	if a.Declined {
		return outcomeReply[askOutcome]{askOutcome{Outcome: "skipped", Reason: declinedAskReason}}
	}
	picked := map[string][]string{}
	var order []string
	pick := func(qid, oid string) {
		if _, ok := picked[qid]; !ok {
			order = append(order, qid)
		}
		for _, x := range picked[qid] {
			if x == oid {
				return
			}
		}
		picked[qid] = append(picked[qid], oid)
	}
	if a.Selected >= 0 && a.Selected < len(flat) {
		pick(flat[a.Selected].questionID, flat[a.Selected].optionID)
	}
	if t := strings.TrimSpace(a.Text); t != "" && len(picked) == 0 {
		parts := splitAnswer(t)
		for _, q := range p.Questions {
			for _, o := range q.Options {
				if matchesAny(parts, o.ID, o.Label) {
					pick(q.ID, o.ID)
					if !q.AllowMultiple {
						break
					}
				}
			}
		}
	}
	if len(picked) == 0 {
		reason := "The user did not choose an option."
		if t := strings.TrimSpace(a.Text); t != "" {
			reason = "The user replied in free text instead of choosing an option: " + t
		}
		return outcomeReply[askOutcome]{askOutcome{Outcome: "skipped", Reason: reason}}
	}
	out := askOutcome{Outcome: "answered"}
	for _, qid := range order {
		out.Answers = append(out.Answers, askAnswer{QuestionID: qid, SelectedOptionIDs: picked[qid]})
	}
	return outcomeReply[askOutcome]{out}
}

// planView renders a Cursor create_plan request for yip.
func planView(p createPlanParams) (string, []string) {
	var b strings.Builder
	b.WriteString("Cursor asks you to approve a plan")
	if p.Name != "" {
		b.WriteString(": " + p.Name)
	}
	if p.Overview != "" {
		b.WriteString("\n\n" + p.Overview)
	}
	if p.Plan != "" {
		b.WriteString("\n\n" + p.Plan)
	}
	todos := p.Todos
	for _, ph := range p.Phases {
		todos = append(todos, ph.Todos...)
	}
	if len(todos) > 0 {
		b.WriteString("\n\nTodos:")
		for _, t := range todos {
			fmt.Fprintf(&b, "\n- [%s] %s", t.Status, t.Content)
		}
	}
	return b.String(), []string{"Accept plan", "Reject plan"}
}

// answerPlan converts a yip answer into the documented create_plan reply.
func answerPlan(a answerInput) outcomeReply[planOutcome] {
	if a.Declined {
		return outcomeReply[planOutcome]{planOutcome{Outcome: "rejected", Reason: declinedPlanReason}}
	}
	switch a.Selected {
	case 0:
		return outcomeReply[planOutcome]{planOutcome{Outcome: "accepted"}}
	case 1:
		reason := strings.TrimSpace(a.Text)
		if reason == "" {
			reason = "The user rejected the plan."
		}
		return outcomeReply[planOutcome]{planOutcome{Outcome: "rejected", Reason: reason}}
	}
	t := strings.TrimSpace(a.Text)
	switch strings.ToLower(strings.TrimRight(t, ".!")) {
	case "accept", "accepted", "approve", "approved", "yes", "y", "ok", "lgtm", "accept plan":
		return outcomeReply[planOutcome]{planOutcome{Outcome: "accepted"}}
	}
	if t == "" {
		t = "The user did not approve the plan."
	}
	return outcomeReply[planOutcome]{planOutcome{Outcome: "rejected", Reason: t}}
}

type answerInput struct {
	Text     string
	Selected int
	Declined bool
}

func splitAnswer(t string) []string {
	f := strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == '\n' || r == ';' })
	out := []string{strings.TrimSpace(t)}
	for _, s := range f {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func matchesAny(parts []string, id, label string) bool {
	for _, p := range parts {
		if (id != "" && strings.EqualFold(p, id)) || (label != "" && strings.EqualFold(p, label)) {
			return true
		}
	}
	return false
}

func todoSummary(todos []cursorTodo) string {
	done, current := 0, ""
	for _, t := range todos {
		switch t.Status {
		case "completed":
			done++
		case "in_progress":
			if current == "" {
				current = t.Content
			}
		}
	}
	s := fmt.Sprintf("Todos: %d/%d done", done, len(todos))
	if current != "" {
		s += "; now: " + oneLine(current, 120)
	}
	return s
}

func planSummary(entries []acp.PlanEntry) string {
	done, current := 0, ""
	for _, e := range entries {
		switch e.Status {
		case "completed":
			done++
		case "in_progress":
			if current == "" {
				current = e.Content
			}
		}
	}
	s := fmt.Sprintf("Plan: %d/%d steps done", done, len(entries))
	if current != "" {
		s += "; now: " + oneLine(current, 120)
	}
	return s
}

// ---------- errors: auth, rate limits, crashes ----------

var authMarkers = []string{
	"not authenticated", "unauthenticated", "authentication required", "auth required",
	"not logged in", "please log in", "please login", "login required", "log in to cursor",
	"agent login", "invalid api key", "api key is invalid", "unauthorized", "session expired",
	"token expired",
}

var rateMarkers = []string{
	"rate limit", "rate-limit", "rate_limit", "ratelimit", "usage limit", "too many requests",
	"quota exceeded", "exceeded your quota", "resource_exhausted", "resource exhausted",
	"limit reached", "hit your limit", "hit your usage limit", "out of requests", "status 429",
	"http 429", "error 429",
}

func containsAny(s string, markers []string) bool {
	s = strings.ToLower(s)
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// failure is a classified terminal error.
type failure struct {
	outcome    string
	message    string
	retryAfter time.Duration
}

// classifyRPC classifies an error returned by the agent for a request.
func classifyRPC(e *acp.Error) failure {
	text := e.Message + " " + string(e.Data)
	switch {
	case e.Code == acp.CodeAuthRequired || containsAny(text, authMarkers):
		return failure{outcome: protocol.OutcomeAuthRequired, message: e.Message}
	case containsAny(text, rateMarkers):
		return failure{outcome: protocol.OutcomeRateLimited, message: e.Message, retryAfter: retryAfterFrom(e.Data, time.Now())}
	}
	return failure{outcome: protocol.OutcomeFailed, message: "Cursor returned an error: " + e.Error()}
}

// classifyText classifies a crash from its stderr tail.
func classifyText(summary, stderr string) failure {
	switch {
	case containsAny(stderr, rateMarkers):
		return failure{outcome: protocol.OutcomeRateLimited, message: summary}
	case containsAny(stderr, authMarkers):
		return failure{outcome: protocol.OutcomeAuthRequired, message: summary}
	}
	return failure{outcome: protocol.OutcomeFailed, message: summary}
}

// retryAfterFrom reads vendor retry information from error data, only when
// the vendor supplied it. Recognised: retryAfterMs, retryAfterSeconds,
// retryAfter (seconds, number or numeric string), and resetAt / resetsAt /
// retryAt (RFC 3339 or unix seconds).
func retryAfterFrom(data json.RawMessage, now time.Time) time.Duration {
	m := rawObject(data)
	if m == nil {
		return 0
	}
	if d := retryFromMap(m, now); d > 0 {
		return d
	}
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			if d := retryFromMap(sub, now); d > 0 {
				return d
			}
		}
	}
	return 0
}

func retryFromMap(m map[string]any, now time.Time) time.Duration {
	num := func(v any) (float64, bool) {
		switch t := v.(type) {
		case float64:
			return t, true
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			return f, err == nil
		}
		return 0, false
	}
	if f, ok := num(m["retryAfterMs"]); ok && f > 0 {
		return time.Duration(f) * time.Millisecond
	}
	for _, k := range []string{"retryAfterSeconds", "retryAfter", "retry_after"} {
		if f, ok := num(m[k]); ok && f > 0 {
			return time.Duration(f * float64(time.Second))
		}
	}
	for _, k := range []string{"resetAt", "resetsAt", "retryAt", "reset_at"} {
		switch t := m[k].(type) {
		case string:
			if ts, err := time.Parse(time.RFC3339, t); err == nil && ts.After(now) {
				return ts.Sub(now)
			}
			if f, err := strconv.ParseFloat(t, 64); err == nil && f > 0 {
				if ts := time.Unix(int64(f), 0); ts.After(now) {
					return ts.Sub(now)
				}
			}
		case float64:
			if ts := time.Unix(int64(t), 0); ts.After(now) {
				return ts.Sub(now)
			}
		}
	}
	return 0
}

func rawObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// ---------- small helpers ----------

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}

func joinAny(v []any) string {
	parts := make([]string, 0, len(v))
	for _, x := range v {
		parts = append(parts, fmt.Sprint(x))
	}
	return strings.Join(parts, " ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
