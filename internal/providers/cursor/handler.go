package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

// HandleRequest answers or records every agent -> client request. Nothing is
// ever left unanswered: unknown methods (including fs/* and terminal/*, which
// yip does not advertise) get JSON-RPC "method not found".
func (s *session) HandleRequest(req *acp.Request) {
	switch req.Method {
	case acp.MethodRequestPermission:
		s.onPermission(req)
	case methodAskQuestion:
		s.onAskQuestion(req)
	case methodCreatePlan:
		s.onCreatePlan(req)
	case methodUpdateTodos:
		// Documented as a notification; if sent as a request, accept the
		// list for display (documented "accepted" reply).
		var p updateTodosParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			_ = req.ReplyError(acp.CodeInvalidParams, "invalid cursor/update_todos params")
			return
		}
		s.emit(providers.Event{Kind: providers.EventStatus, Text: todoSummary(p.Todos), Data: req.Params})
		_ = req.Reply(map[string]any{"outcome": map[string]any{"outcome": "accepted", "todos": p.Todos}})
	default:
		_ = req.MethodNotFound()
		s.warn(fmt.Sprintf("Cursor sent a request yip does not support (%s); answered with JSON-RPC \"method not found\".", req.Method))
	}
}

// HandleNotification processes session/update and Cursor's fire-and-forget
// extension notifications in wire order.
func (s *session) HandleNotification(method string, params json.RawMessage) {
	switch method {
	case acp.MethodSessionUpdate:
		s.onUpdate(params)
	case methodUpdateTodos:
		var p updateTodosParams
		if json.Unmarshal(params, &p) == nil && !s.isReplaying() {
			s.emit(providers.Event{Kind: providers.EventStatus, Text: todoSummary(p.Todos), Data: params})
		}
	case methodTask:
		var p taskParams
		if json.Unmarshal(params, &p) == nil && !s.isReplaying() {
			s.emit(providers.Event{Kind: providers.EventStatus, Text: "Subagent task: " + oneLine(p.Description, 160), Data: params})
		}
	case methodGenerateImage:
		var p generateImageParams
		if json.Unmarshal(params, &p) == nil && !s.isReplaying() {
			text := "Generated image: " + oneLine(p.Description, 160)
			if p.FilePath != "" {
				text += " (" + p.FilePath + ")"
			}
			s.emit(providers.Event{Kind: providers.EventStatus, Text: text, Data: params})
		}
	case acp.MethodCancelRequest:
		s.onCancelRequest(params)
	default:
		// ACP: unrecognised notifications SHOULD be ignored.
	}
}

func (s *session) isReplaying() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replaying
}

// ---------- permissions ----------

func (s *session) onPermission(req *acp.Request) {
	var p acp.RequestPermissionParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		_ = req.ReplyError(acp.CodeInvalidParams, "invalid session/request_permission params")
		s.warn("Cursor sent a malformed permission request; it was rejected as invalid.")
		return
	}
	s.mu.Lock()
	if s.cancelReq || s.ended || s.abortResult != nil {
		s.mu.Unlock()
		_ = req.Reply(acp.CancelledPermission())
		return
	}
	tc := acp.ToolCall{ToolCallID: p.ToolCall.ToolCallID}
	if prev, ok := s.tools[p.ToolCall.ToolCallID]; ok {
		tc = *prev
	}
	tc.Merge(p.ToolCall)
	action := approvalAction(tc)

	if reason := s.policyDenyLocked(tc, action); reason != "" {
		s.mu.Unlock()
		_ = req.Reply(rejectReply(p.Options))
		s.warn(reason)
		return
	}
	s.seq++
	id := fmt.Sprintf("cursor-perm-%d", s.seq)
	s.perms[id] = &pendingPerm{req: req, options: p.Options, action: action}
	s.byRPC[req.Key()] = id
	s.mu.Unlock()

	s.emit(providers.Event{
		Kind:     providers.EventApprovalRequest,
		Text:     action.Summary,
		Tool:     toolLabel(tc),
		Approval: &providers.ApprovalRequest{ID: id, Action: action, Raw: req.Params},
	})
}

// policyDenyLocked returns a reason when yip refuses the request without
// asking a human: writes, commands and mode switches in read-only modes, and
// file changes outside the workspace in edit mode.
func (s *session) policyDenyLocked(tc acp.ToolCall, action protocol.ApprovalAction) string {
	if s.readOnly && deniedInReadOnly(tc.Kind) {
		return fmt.Sprintf("Denied Cursor %s request in a %s session: %s", tc.Kind, s.spec.Mode, action.Summary)
	}
	if isWriteKind(tc.Kind) {
		if p, out := outsideWorkdir(s.root, pathTargets(tc)); out {
			return fmt.Sprintf("Denied Cursor %s request outside the workspace (%s): %s", tc.Kind, p, action.Summary)
		}
	}
	return ""
}

func toolLabel(tc acp.ToolCall) string {
	if tc.Name != "" {
		return tc.Name
	}
	if tc.Kind != "" {
		return tc.Kind
	}
	return "tool"
}

// onCancelRequest handles $/cancel_request for a pending blocking request:
// the agent withdrew it, so reply -32800 and tell the runner.
func (s *session) onCancelRequest(params json.RawMessage) {
	var p acp.CancelRequestParams
	if json.Unmarshal(params, &p) != nil {
		return
	}
	key := p.Key()
	s.mu.Lock()
	id, ok := s.byRPC[key]
	var req *acp.Request
	kind := ""
	if ok {
		delete(s.byRPC, key)
		if pp, ok := s.perms[id]; ok {
			req, kind = pp.req, "approval"
			delete(s.perms, id)
		} else if pq, ok := s.questions[id]; ok {
			req, kind = pq.req, "question"
			delete(s.questions, id)
		}
	}
	s.mu.Unlock()
	if req == nil {
		return
	}
	_ = req.ReplyError(acp.CodeRequestCancelled, "Request cancelled")
	s.emit(providers.Event{
		Kind: providers.EventWarning,
		Text: fmt.Sprintf("Cursor withdrew pending %s %s.", kind, id),
		Data: mustJSON(map[string]string{"withdrawn": id, "type": kind}),
	})
}

// ---------- Cursor blocking extensions ----------

func (s *session) onAskQuestion(req *acp.Request) {
	var p askQuestionParams
	if err := json.Unmarshal(req.Params, &p); err != nil || len(p.Questions) == 0 {
		_ = req.ReplyError(acp.CodeInvalidParams, "invalid cursor/ask_question params")
		s.warn("Cursor sent a malformed ask_question request; it was rejected as invalid.")
		return
	}
	text, options, flat := askQuestionView(p)
	id, ok := s.registerQuestion(req, &pendingQuestion{req: req, kind: "ask", ask: p, flat: flat})
	if !ok {
		_ = req.Reply(outcomeReply[askOutcome]{askOutcome{Outcome: "cancelled"}})
		return
	}
	s.emit(providers.Event{
		Kind:     providers.EventQuestion,
		Text:     text,
		Question: &providers.Question{ID: id, Kind: "ask", Text: text, Options: options, Raw: req.Params},
	})
}

func (s *session) onCreatePlan(req *acp.Request) {
	var p createPlanParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		_ = req.ReplyError(acp.CodeInvalidParams, "invalid cursor/create_plan params")
		s.warn("Cursor sent a malformed create_plan request; it was rejected as invalid.")
		return
	}
	text, options := planView(p)
	id, ok := s.registerQuestion(req, &pendingQuestion{req: req, kind: "plan"})
	if !ok {
		_ = req.Reply(outcomeReply[planOutcome]{planOutcome{Outcome: "cancelled"}})
		return
	}
	s.emit(providers.Event{
		Kind:     providers.EventQuestion,
		Text:     text,
		Question: &providers.Question{ID: id, Kind: "plan", Text: text, Options: options, Raw: req.Params},
	})
}

func (s *session) registerQuestion(req *acp.Request, q *pendingQuestion) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelReq || s.ended || s.abortResult != nil {
		return "", false
	}
	s.seq++
	id := fmt.Sprintf("cursor-q-%d", s.seq)
	s.questions[id] = q
	s.byRPC[req.Key()] = id
	return id, true
}

// ---------- session/update ----------

func (s *session) onUpdate(params json.RawMessage) {
	var n acp.SessionNotification
	if err := json.Unmarshal(params, &n); err != nil {
		return
	}
	s.mu.Lock()
	sid, replaying := s.sessionID, s.replaying
	s.mu.Unlock()
	if sid != "" && n.SessionID != "" && n.SessionID != sid {
		return
	}
	u := n.Update
	switch u.SessionUpdate {
	case "agent_message_chunk":
		if replaying || u.Content == nil || u.Content.Type != "text" {
			return
		}
		s.appendChunk(u.MessageID, u.Content.Text)
	case "tool_call", "tool_call_update":
		s.onTool(u.ToolCall, u.SessionUpdate == "tool_call", replaying)
	case "plan":
		if !replaying {
			s.emit(providers.Event{Kind: providers.EventStatus, Text: planSummary(u.Entries), Data: mustJSON(u.Entries)})
		}
	case "current_mode_update":
		s.onModeChange(u.CurrentModeID, "", replaying)
	case "config_option_update":
		s.mu.Lock()
		s.configOptions = u.ConfigOptions
		cfgID := s.modeConfigID
		s.mu.Unlock()
		if cfgID != "" {
			for _, o := range u.ConfigOptions {
				if o.ID == cfgID {
					s.onModeChange(o.Current(), cfgID, replaying)
				}
			}
		}
	case "usage_update":
		if !replaying && u.Cost != nil {
			s.setCost(u.Cost, u.Raw)
		}
	default:
		// user_message_chunk, agent_thought_chunk, available_commands_update,
		// session_info_update: nothing for the runner.
	}
}

func (s *session) onTool(tc acp.ToolCall, isNew, replaying bool) {
	if tc.ToolCallID == "" {
		return
	}
	s.mu.Lock()
	t, ok := s.tools[tc.ToolCallID]
	if !ok {
		t = &acp.ToolCall{ToolCallID: tc.ToolCallID}
		s.tools[tc.ToolCallID] = t
	}
	t.Merge(tc)
	snap := *t
	var flushed string
	if isNew && !replaying {
		// A new tool call ends the current assistant message.
		flushed = s.takeMessageLocked()
		s.turnTail = nil
	}
	started := !s.startedTools[snap.ToolCallID]
	s.startedTools[snap.ToolCallID] = true
	finished := (snap.Status == acp.StatusCompleted || snap.Status == acp.StatusFailed) && !s.finishedTools[snap.ToolCallID]
	if finished {
		s.finishedTools[snap.ToolCallID] = true
	}
	// Backstop: a file-modifying tool that actually ran in a read-only
	// session means the boundary failed. Stop immediately.
	violation := !replaying && s.readOnly && isWriteKind(snap.Kind) &&
		(snap.Status == acp.StatusInProgress || snap.Status == acp.StatusCompleted)
	s.mu.Unlock()

	if replaying {
		return
	}
	if flushed != "" {
		s.emit(providers.Event{Kind: providers.EventMessage, Text: flushed})
	}
	data := mustJSON(map[string]any{"toolCallId": snap.ToolCallID, "kind": snap.Kind, "status": snap.Status, "locations": snap.Locations})
	if started {
		s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: toolLabel(snap), Text: toolText(snap), Data: data})
	}
	if finished {
		text := toolText(snap)
		if snap.Status == acp.StatusFailed {
			text += " (failed)"
		}
		s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: toolLabel(snap), Text: text, Data: data})
	}
	if violation {
		s.abort(failure{outcome: protocol.OutcomeFailed, message: fmt.Sprintf("Read-only boundary violated: Cursor ran a %s tool (%s) in a %s session without yip approval; the run was stopped.", snap.Kind, toolText(snap), s.spec.Mode)})
	}
}

func toolText(tc acp.ToolCall) string {
	if t := oneLine(tc.Title, 160); t != "" {
		return t
	}
	return defaultSummary(tc, approvalAction(acp.ToolCall{Kind: tc.Kind, Name: tc.Name, RawInput: tc.RawInput, Locations: tc.Locations}))
}

// ---------- read-only mode enforcement ----------

// onModeChange keeps a read-only session in Cursor's ask mode. If the agent
// switches itself out of it, yip switches it back; if that fails the run is
// stopped. Calls are made off the read loop.
func (s *session) onModeChange(modeID, _ string, replaying bool) {
	if modeID == "" {
		return
	}
	s.mu.Lock()
	s.currentMode = modeID
	want := s.wantMode
	start := want != "" && modeID != want && !replaying && !s.reasserting && !s.ended
	if start {
		s.reasserting = true
	}
	s.mu.Unlock()
	if !start {
		return
	}
	s.warn(fmt.Sprintf("Cursor switched a %s session to mode %q; switching it back to %q.", s.spec.Mode, modeID, want))
	go func() {
		ctx, cancel := context.WithTimeout(s.runCtx, 30*time.Second)
		defer cancel()
		err := s.setMode(ctx, want)
		s.mu.Lock()
		s.reasserting = false
		s.mu.Unlock()
		if err != nil {
			s.abort(failure{outcome: protocol.OutcomeFailed, message: "Read-only boundary could not be restored: Cursor left " + want + " mode and switching back failed: " + err.Error()})
		}
	}()
}

// setMode switches the Cursor session mode using whichever mechanism the
// agent advertised (config option preferred, legacy modes otherwise) and
// verifies the result when the agent reports it.
func (s *session) setMode(ctx context.Context, mode string) error {
	s.mu.Lock()
	sid, cfgID, legacy := s.sessionID, s.modeConfigID, s.modeViaLegacy
	s.mu.Unlock()
	switch {
	case cfgID != "":
		var r acp.SetConfigOptionResult
		if err := s.ap.conn.Call(ctx, acp.MethodSessionSetConfig, acp.SetConfigOptionParams{SessionID: sid, ConfigID: cfgID, Value: mode}, &r); err != nil {
			return err
		}
		if len(r.ConfigOptions) > 0 {
			s.mu.Lock()
			s.configOptions = r.ConfigOptions
			s.mu.Unlock()
			for _, o := range r.ConfigOptions {
				if o.ID == cfgID && o.Current() != mode {
					return fmt.Errorf("agent reports mode %q after requesting %q", o.Current(), mode)
				}
			}
		}
	case legacy:
		if err := s.ap.conn.Call(ctx, acp.MethodSessionSetMode, acp.SetModeParams{SessionID: sid, ModeID: mode}, nil); err != nil {
			return err
		}
	default:
		return fmt.Errorf("agent exposes no mode control")
	}
	s.mu.Lock()
	s.currentMode = mode
	s.mu.Unlock()
	return nil
}

var _ acp.Handler = (*session)(nil)
var _ providers.Session = (*session)(nil)

func modeWanted(mode string) string {
	if mode == protocol.ModeEdit {
		return "agent"
	}
	return "ask"
}

// matchChoice finds want among (value, name) pairs: exact value, then
// case-insensitive value or name.
func matchChoice(want string, values []acp.ConfigOptionValue) (string, bool) {
	for _, v := range values {
		if v.Value == want {
			return v.Value, true
		}
	}
	for _, v := range values {
		if strings.EqualFold(v.Value, want) || strings.EqualFold(v.Name, want) {
			return v.Value, true
		}
	}
	return "", false
}

// matchModel matches a requested model against agent-reported model values.
// Cursor model values may carry parameters (e.g. "name[thinking=true,...]"),
// so the base name before '[' also matches when unambiguous.
func matchModel(want string, values []acp.ConfigOptionValue) (string, bool) {
	if v, ok := matchChoice(want, values); ok {
		return v, true
	}
	var hits []string
	for _, v := range values {
		base, _, _ := strings.Cut(v.Value, "[")
		if strings.EqualFold(base, want) {
			hits = append(hits, v.Value)
		}
	}
	if len(hits) >= 1 {
		return hits[0], true
	}
	return "", false
}

func valueList(values []acp.ConfigOptionValue, max int) string {
	var names []string
	for i, v := range values {
		if i == max {
			names = append(names, fmt.Sprintf("… (%d more)", len(values)-max))
			break
		}
		names = append(names, v.Value)
	}
	return strings.Join(names, ", ")
}
