package claude

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// ---- stdout wire types (Claude Code stream-json, pinned 2.1.282) ----

// wireMsg is the union of the stream-json message shapes the adapter reads.
// Unknown types and fields are ignored.
type wireMsg struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	UUID      string `json:"uuid"`

	// system/init
	Model         string          `json:"model"`
	Tools         []string        `json:"tools"`
	MCPServers    []wireMCPServer `json:"mcp_servers"`
	MCPErrors     []wireMCPError  `json:"mcp_server_errors"`
	APIKeySource  string          `json:"apiKeySource"`
	Capabilities  []string        `json:"capabilities"`
	Version       string          `json:"claude_code_version"`
	PermissionMod string          `json:"permissionMode"`

	// system/api_retry
	Attempt      int   `json:"attempt"`
	MaxRetries   int   `json:"max_retries"`
	RetryDelayMs int64 `json:"retry_delay_ms"`
	ErrorStatus  *int  `json:"error_status"`
	// ErrorRaw is a string category on api_retry, assistant, and auth_status
	// messages; decoded lazily because other types may use another shape.
	ErrorRaw json.RawMessage `json:"error"`

	// system/status (string or null); other types reuse the name.
	StatusRaw json.RawMessage `json:"status"`

	// system/informational
	Content json.RawMessage `json:"content"`
	Level   string          `json:"level"`

	// assistant / user: an object; permission_denied uses a string here.
	MessageRaw      json.RawMessage `json:"message"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	IsReplay        bool            `json:"isReplay"`

	// stream_event
	EventRaw json.RawMessage `json:"event"`

	// result
	IsError           bool                      `json:"is_error"`
	ResultRaw         json.RawMessage           `json:"result"`
	ErrorsRaw         json.RawMessage           `json:"errors"`
	APIErrorStatus    *int                      `json:"api_error_status"`
	NumTurns          int                       `json:"num_turns"`
	TotalCostUSD      *float64                  `json:"total_cost_usd"`
	Usage             *wireUsage                `json:"usage"`
	ModelUsage        map[string]wireModelUsage `json:"modelUsage"`
	TerminalReason    string                    `json:"terminal_reason"`
	StartupFailure    string                    `json:"startup_failure_reason"`
	UserMessageUUID   string                    `json:"user_message_uuid"`
	UserMessageUUIDs  []string                  `json:"user_message_uuids"`
	Origin            *wireOrigin               `json:"origin"`
	PermissionDenials []json.RawMessage         `json:"permission_denials"`

	// rate_limit_event
	RateLimitInfo *wireRateLimit `json:"rate_limit_info"`

	// auth_status
	IsAuthenticating bool `json:"isAuthenticating"`

	// control_request / control_response
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`
}

func rawString(raw json.RawMessage) string {
	var s string
	if len(raw) > 0 && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func (m *wireMsg) errorString() string  { return rawString(m.ErrorRaw) }
func (m *wireMsg) statusString() string { return rawString(m.StatusRaw) }
func (m *wireMsg) resultText() string   { return rawString(m.ResultRaw) }

func (m *wireMsg) errorsList() []string {
	var out []string
	if len(m.ErrorsRaw) > 0 && json.Unmarshal(m.ErrorsRaw, &out) == nil {
		return out
	}
	return nil
}

func (m *wireMsg) message() *wireMessage {
	if len(m.MessageRaw) == 0 || m.MessageRaw[0] != '{' {
		return nil
	}
	var wm wireMessage
	if json.Unmarshal(m.MessageRaw, &wm) != nil {
		return nil
	}
	return &wm
}

type wireMCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

type wireMCPError struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wireOrigin struct {
	Kind string `json:"kind"`
}

type wireMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"` // string or []wireBlock
}

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type wireStreamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type wireUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type wireModelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

type wireRateLimit struct {
	Status        string   `json:"status"` // allowed | allowed_warning | rejected
	ResetsAt      *int64   `json:"resetsAt"`
	Utilization   *float64 `json:"utilization"`
	RateLimitType string   `json:"rateLimitType"`
	ErrorCode     string   `json:"errorCode"`
}

// ---- stdin wire types ----

type stdinUserMessage struct {
	Type            string          `json:"type"` // "user"
	UUID            string          `json:"uuid"`
	SessionID       string          `json:"session_id"`
	Message         stdinMessageArg `json:"message"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
}

type stdinMessageArg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type controlRequest struct {
	Type      string         `json:"type"` // "control_request"
	RequestID string         `json:"request_id"`
	Request   map[string]any `json:"request"`
}

type controlResponse struct {
	Type     string         `json:"type"` // "control_response"
	Response map[string]any `json:"response"`
}

// ---- stream state and event mapping ----

// streamState accumulates what the adapter has learned from stdout. It is
// owned by the reader goroutine until the stream ends.
type streamState struct {
	resumed bool

	sessionID    string
	model        string
	apiKeySource string
	caps         map[string]bool

	toolNames map[string]string // tool_use_id -> tool name
	toolText  map[string]string // tool_use_id -> start summary

	lastAssistantText string
	lastErrorText     string
	lastResult        *wireMsg
	results           int

	// Error signals (the final result decides; these refine classification).
	authSignal   string
	rateSignal   string
	rateResetAt  time.Time
	billingLimit bool
	warnedQuota  bool

	// Usage
	turnIn, turnOut int64 // summed per-turn `usage` from results in this run
	usage           *protocol.Usage
	billing         string
}

func newStreamState(resumed bool, billing string) *streamState {
	return &streamState{
		resumed:   resumed,
		caps:      map[string]bool{},
		toolNames: map[string]string{},
		toolText:  map[string]string{},
		billing:   billing,
	}
}

// eventSink receives normalized events.
type eventSink func(providers.Event)

// handle maps one stdout message to events.
func (st *streamState) handle(m *wireMsg, emit eventSink, mcpName string) {
	now := time.Now()
	ev := func(e providers.Event) {
		if e.At.IsZero() {
			e.At = now
		}
		emit(e)
	}
	if m.SessionID != "" && m.Type != "control_response" {
		if st.sessionID != m.SessionID {
			st.sessionID = m.SessionID
			ev(providers.Event{Kind: providers.EventVendorSession, VendorSessionID: m.SessionID})
		}
	}
	switch m.Type {
	case "system":
		st.handleSystem(m, ev, mcpName)
	case "stream_event":
		var se wireStreamEvent
		if json.Unmarshal(m.EventRaw, &se) == nil && se.Type == "content_block_delta" && se.Delta != nil &&
			se.Delta.Type == "text_delta" && se.Delta.Text != "" && isMainThread(m) {
			ev(providers.Event{Kind: providers.EventMessageDelta, Text: se.Delta.Text})
		}
	case "assistant":
		st.handleAssistant(m, ev)
	case "user":
		if !m.IsReplay {
			st.handleToolResults(m, ev)
		}
	case "result":
		st.handleResult(m, ev)
	case "rate_limit_event":
		st.handleRateLimit(m, ev)
	case "auth_status":
		if e := m.errorString(); e != "" {
			st.authSignal = e
			ev(providers.Event{Kind: providers.EventWarning, Text: "Claude Code authentication: " + e})
		}
	case "tool_progress", "control_response", "control_request", "keep_alive":
		// control_request is answered by the session; progress is liveness only.
	}
}

func isMainThread(m *wireMsg) bool {
	return m.ParentToolUseID == nil || *m.ParentToolUseID == ""
}

func (st *streamState) handleSystem(m *wireMsg, ev func(providers.Event), mcpName string) {
	switch m.Subtype {
	case "init":
		st.model = m.Model
		st.apiKeySource = m.APIKeySource
		for _, c := range m.Capabilities {
			st.caps[c] = true
		}
		switch m.APIKeySource {
		case "ANTHROPIC_API_KEY", "apiKeyHelper", "/login managed key":
			st.billing = protocol.BillingAPI
		}
		text := "Claude Code session started"
		if m.Model != "" {
			text += " (model " + m.Model + ")"
		}
		data, _ := json.Marshal(map[string]any{
			"model": m.Model, "version": m.Version, "permissionMode": m.PermissionMod,
			"tools": m.Tools, "mcpServers": m.MCPServers, "apiKeySource": m.APIKeySource,
		})
		ev(providers.Event{Kind: providers.EventStatus, Text: text, Data: data})
		st.checkMCP(m, ev, mcpName)
	case "api_retry":
		cat := m.errorString()
		if cat == "" {
			cat = "error"
		}
		text := fmt.Sprintf("Claude API request failed (%s); retrying", cat)
		if m.MaxRetries > 0 {
			text += fmt.Sprintf(" (attempt %d of %d)", m.Attempt, m.MaxRetries)
		}
		if m.RetryDelayMs > 0 {
			text += fmt.Sprintf(" in %s", (time.Duration(m.RetryDelayMs) * time.Millisecond).Round(time.Second))
		}
		ev(providers.Event{Kind: providers.EventStatus, Text: text})
	case "status":
		if m.statusString() == "compacting" {
			ev(providers.Event{Kind: providers.EventStatus, Text: "Compacting conversation"})
		}
	case "informational":
		if m.Level == "warning" {
			var s string
			if json.Unmarshal(m.Content, &s) == nil && s != "" {
				ev(providers.Event{Kind: providers.EventWarning, Text: s})
			}
		}
	case "permission_denied":
		// Not emitted when a permission-prompt tool is configured; kept for
		// completeness (e.g. rule denials on other configurations).
	}
}

// checkMCP warns when the yip bridge did not load or its permission tool is
// missing. The CLI itself exits with an error on the first prompt if the
// permission tool is unavailable, so this is an early, visible signal.
func (st *streamState) checkMCP(m *wireMsg, ev func(providers.Event), mcpName string) {
	found := false
	for _, s := range m.MCPServers {
		if s.Name != mcpName {
			continue
		}
		found = true
		switch s.Status {
		case "connected":
		case "pending":
			ev(providers.Event{Kind: providers.EventStatus, Text: "yip tools are still connecting"})
		default:
			msg := "yip MCP bridge did not connect (status " + s.Status + ")"
			if s.Error != "" {
				msg += ": " + s.Error
			}
			ev(providers.Event{Kind: providers.EventWarning, Text: msg})
		}
	}
	for _, e := range m.MCPErrors {
		ev(providers.Event{Kind: providers.EventWarning, Text: "MCP config entry " + e.Name + " skipped (" + e.Type + "): " + e.Message})
	}
	if !found {
		ev(providers.Event{Kind: providers.EventWarning, Text: "yip MCP bridge is missing from the Claude Code session"})
	}
}

func (st *streamState) handleAssistant(m *wireMsg, ev func(providers.Event)) {
	errCat := m.errorString()
	if errCat != "" {
		st.noteErrorCategory(errCat)
	}
	msg := m.message()
	if msg == nil {
		return
	}
	blocks := parseBlocks(msg.Content)
	main := isMainThread(m)
	var text strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if main {
				text.WriteString(b.Text)
			}
		case "tool_use", "server_tool_use", "mcp_tool_use":
			summary := toolSummary(b.Name, b.Input)
			st.toolNames[b.ID] = b.Name
			st.toolText[b.ID] = summary
			ev(providers.Event{
				Kind: providers.EventToolStarted,
				Tool: b.Name,
				Text: summary,
				Data: toolData(map[string]any{"id": b.ID, "name": b.Name, "subagent": !main}, "input", b.Input),
			})
		}
	}
	t := text.String()
	switch {
	case t == "":
	case errCat != "":
		// Claude Code reports API failures (usage limit, sign-in) as a
		// synthetic assistant message carrying an error category. That text
		// is a provider error, not the engineer's reply.
		st.lastErrorText = t
		ev(providers.Event{Kind: providers.EventWarning, Text: t})
	default:
		st.lastAssistantText = t
		ev(providers.Event{Kind: providers.EventMessage, Text: t})
	}
}

func (st *streamState) handleToolResults(m *wireMsg, ev func(providers.Event)) {
	msg := m.message()
	if msg == nil {
		return
	}
	for _, b := range parseBlocks(msg.Content) {
		if b.Type != "tool_result" {
			continue
		}
		name := st.toolNames[b.ToolUseID]
		summary := st.toolText[b.ToolUseID]
		if summary == "" {
			summary = "Tool call"
		}
		text := summary
		if b.IsError {
			text += " (failed)"
		}
		ev(providers.Event{
			Kind: providers.EventToolFinished,
			Tool: name,
			Text: text,
			Data: toolData(map[string]any{"id": b.ToolUseID, "name": name, "isError": b.IsError}, "output", excerpt(b.Content)),
		})
	}
}

func (st *streamState) handleRateLimit(m *wireMsg, ev func(providers.Event)) {
	info := m.RateLimitInfo
	if info == nil {
		return
	}
	switch info.Status {
	case "rejected":
		st.rateSignal = "usage limit reached"
		if info.ErrorCode == "credits_required" {
			st.rateSignal = "usage limit reached; usage credits required"
		}
		text := "Claude usage limit reached"
		if info.ResetsAt != nil && *info.ResetsAt > 0 {
			st.rateResetAt = time.Unix(*info.ResetsAt, 0)
			text += "; resets " + st.rateResetAt.UTC().Format(time.RFC3339)
		}
		ev(providers.Event{Kind: providers.EventWarning, Text: text})
	case "allowed_warning":
		if !st.warnedQuota {
			st.warnedQuota = true
			text := "Approaching the Claude usage limit"
			if info.Utilization != nil {
				text += fmt.Sprintf(" (%.0f%% used)", *info.Utilization*100)
			}
			ev(providers.Event{Kind: providers.EventWarning, Text: text})
		}
	}
}

func (st *streamState) noteErrorCategory(cat string) {
	switch cat {
	case "authentication_failed", "oauth_org_not_allowed":
		st.authSignal = cat
	case "rate_limit":
		st.rateSignal = cat
	case "billing_error":
		st.rateSignal = cat
		st.billingLimit = true
	}
}

func (st *streamState) handleResult(m *wireMsg, ev func(providers.Event)) {
	st.results++
	if m.StartupFailure != "" {
		ev(providers.Event{Kind: providers.EventWarning, Text: "Claude Code refused to start: " + m.StartupFailure})
	}
	// A crash can end with a zeroed result; keep earlier totals in that case.
	zeroed := m.Subtype == "error_during_execution" && (m.TotalCostUSD == nil || *m.TotalCostUSD == 0) && len(m.ModelUsage) == 0
	if m.Usage != nil {
		st.turnIn += m.Usage.InputTokens + m.Usage.CacheCreationInputTokens + m.Usage.CacheReadInputTokens
		st.turnOut += m.Usage.OutputTokens
	}
	if !(zeroed && st.usage != nil) {
		st.usage = st.computeUsage(m)
	}
	st.lastResult = m
	if st.usage != nil {
		u := *st.usage
		ev(providers.Event{Kind: providers.EventUsage, Usage: &u})
	}
}

// computeUsage derives this run's usage. Fresh sessions use the latest
// result's cumulative per-model totals and the CLI's cost estimate. Resumed
// sessions: the CLI restores earlier runs' totals into those figures, so only
// the per-turn token counts of this run are reported and cost is omitted.
func (st *streamState) computeUsage(m *wireMsg) *protocol.Usage {
	u := &protocol.Usage{Source: "vendor", Billing: st.billing}
	if u.Billing == "" {
		u.Billing = protocol.BillingUnknown
	}
	if !st.resumed && len(m.ModelUsage) > 0 {
		var in, out int64
		for _, mu := range m.ModelUsage {
			in += mu.InputTokens + mu.CacheReadInputTokens + mu.CacheCreationInputTokens
			out += mu.OutputTokens
		}
		u.InputTokens, u.OutputTokens = &in, &out
	} else if m.Usage != nil || st.turnIn > 0 || st.turnOut > 0 {
		in, out := st.turnIn, st.turnOut
		u.InputTokens, u.OutputTokens = &in, &out
	}
	if !st.resumed && m.TotalCostUSD != nil {
		c := *m.TotalCostUSD
		u.CostUSD = &c
	}
	if u.InputTokens == nil && u.OutputTokens == nil && u.CostUSD == nil {
		return nil
	}
	return u
}

// ---- outcome classification ----

var (
	authTextRE = regexp.MustCompile(`(?i)not logged in|please run /login|invalid api key|login expired|oauth token (has )?expired|authentication[_ ]failed|invalid x-api-key|oauth_org_not_allowed|claude auth login`)
	rateTextRE = regexp.MustCompile(`(?i)you've hit your [a-z0-9 ]*limit|usage limit|rate[ _]limit|request rejected \(429\)|credit balance is too low|spend limit reached|limiting requests`)
)

type outcomeInfo struct {
	Outcome    string
	Error      string
	RetryAfter time.Duration
	FinalText  string
}

// classify decides the terminal outcome from what the stream reported, the
// process exit, and the stderr tail. It never invents a reset time.
func classify(st *streamState, exitErr error, stderrTail string, cancelled bool, timeout time.Duration, now time.Time) outcomeInfo {
	var oi outcomeInfo
	r := st.lastResult
	if r != nil && !r.IsError && r.Subtype == "success" {
		oi.FinalText = r.resultText()
	} else {
		oi.FinalText = st.lastAssistantText
	}
	if cancelled {
		oi.Outcome = protocol.OutcomeCancelled
		return oi
	}
	if timeout > 0 {
		oi.Outcome = protocol.OutcomeFailed
		oi.Error = fmt.Sprintf("Claude Code run exceeded its %s time limit", timeout)
		return oi
	}
	if r != nil && !r.IsError && r.Subtype == "success" {
		oi.Outcome = protocol.OutcomeSucceeded
		return oi
	}

	// Build the most specific error text available.
	var parts []string
	if r != nil {
		if t := r.resultText(); r.IsError && t != "" {
			parts = append(parts, t)
		}
		parts = append(parts, r.errorsList()...)
	}
	if len(parts) == 0 && st.lastErrorText != "" {
		parts = append(parts, st.lastErrorText)
	}
	if len(parts) == 0 && r != nil && r.Subtype != "" && r.Subtype != "success" {
		parts = append(parts, "Claude Code ended with "+r.Subtype)
	}
	errText := strings.Join(parts, "; ")
	combined := errText + "\n" + stderrTail
	status := 0
	if r != nil && r.APIErrorStatus != nil {
		status = *r.APIErrorStatus
	}

	switch {
	case st.authSignal != "" || status == 401 || authTextRE.MatchString(combined):
		oi.Outcome = protocol.OutcomeAuthRequired
		if errText == "" {
			errText = "Claude Code is not signed in or its credentials were rejected"
		}
	case st.rateSignal != "" || status == 429 || rateTextRE.MatchString(combined):
		oi.Outcome = protocol.OutcomeRateLimited
		if errText == "" {
			errText = "Claude usage limit reached"
		}
		if !st.rateResetAt.IsZero() && st.rateResetAt.After(now) {
			oi.RetryAfter = st.rateResetAt.Sub(now)
		}
	default:
		oi.Outcome = protocol.OutcomeFailed
		if errText == "" {
			switch {
			case exitErr != nil:
				errText = fmt.Sprintf("Claude Code exited (%v) without a result", exitErr)
			case r == nil:
				errText = "Claude Code exited without a result"
			default:
				errText = "Claude Code run failed"
			}
		}
		if r == nil && strings.TrimSpace(stderrTail) != "" {
			errText += "\nstderr: " + strings.TrimSpace(stderrTail)
		}
	}
	oi.Error = errText
	return oi
}

// ---- helpers ----

func parseBlocks(raw json.RawMessage) []wireBlock {
	if len(raw) == 0 {
		return nil
	}
	var blocks []wireBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return []wireBlock{{Type: "text", Text: s}}
	}
	return nil
}

const maxDataBytes = 16 << 10

// toolData builds an event payload, attaching val under key only when small.
func toolData(base map[string]any, key string, val json.RawMessage) json.RawMessage {
	if len(val) > 0 {
		if len(val) <= maxDataBytes && json.Valid(val) {
			base[key] = val
		} else {
			base[key+"Truncated"] = true
		}
	}
	out, _ := json.Marshal(base)
	return out
}

// excerpt returns a short JSON string of a tool_result's text content.
func excerpt(raw json.RawMessage) json.RawMessage {
	var text string
	if len(raw) == 0 {
		return nil
	}
	if json.Unmarshal(raw, &text) != nil {
		var parts []string
		for _, b := range parseBlocks(raw) {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	if text == "" {
		return nil
	}
	out, _ := json.Marshal(clip(text, 4000))
	return out
}

// toolSummary is the short human description shown for a tool call.
func toolSummary(name string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string {
		if v, ok := in[k].(string); ok {
			return v
		}
		return ""
	}
	base := func(p string) string {
		if p == "" {
			return "a file"
		}
		return filepath.Base(p)
	}
	switch name {
	case toolRead:
		return "Reading " + base(str("file_path"))
	case toolEdit, "MultiEdit":
		return "Editing " + base(str("file_path"))
	case toolWrite:
		return "Writing " + base(str("file_path"))
	case toolNotebookEdit:
		return "Editing notebook " + base(str("notebook_path"))
	case toolBash:
		if d := str("description"); d != "" {
			return clip(oneLine(d), 80)
		}
		return "Running " + clip(oneLine(str("command")), 80)
	case toolGlob:
		return "Finding files matching " + clip(str("pattern"), 60)
	case toolGrep:
		return "Searching for " + clip(str("pattern"), 60)
	case toolWebFetch:
		if u, err := url.Parse(str("url")); err == nil && u.Host != "" {
			return "Fetching " + u.Host
		}
		return "Fetching a web page"
	case toolWebSearch:
		return "Searching the web for " + clip(str("query"), 60)
	case "TodoWrite":
		return "Updating the task list"
	}
	if strings.HasPrefix(name, "mcp__") {
		rest := strings.TrimPrefix(name, "mcp__")
		server, tool, ok := strings.Cut(rest, "__")
		if ok {
			if server == "yip" {
				return "Using yip " + strings.ReplaceAll(tool, "_", " ")
			}
			return "Using " + tool + " (" + server + ")"
		}
	}
	return "Using " + name
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	return s
}

// clip shortens s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// tailClip keeps the last n bytes of s on a rune boundary.
func tailClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return "…" + s[start:]
}
