package opencode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

func (s *session) HandleRequest(req *acp.Request) {
	if req.Method != acp.MethodRequestPermission {
		_ = req.MethodNotFound()
		return
	}
	var params acp.RequestPermissionParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		_ = req.Reply(acp.CancelledPermission())
		return
	}
	s.mu.Lock()
	if params.SessionID != s.id || s.readOnly || s.ctx.Err() != nil || unmanagedExecution(params.ToolCall) {
		s.mu.Unlock()
		_ = req.Reply(acp.CancelledPermission())
		return
	}
	id := "opencode:" + req.Key()
	s.pending[id] = pendingPermission{request: req, options: params.Options}
	s.mu.Unlock()
	s.emit(providers.Event{Kind: providers.EventApprovalRequest, Approval: &providers.ApprovalRequest{
		ID: id, Action: approvalAction(params.ToolCall), Raw: append(json.RawMessage(nil), req.Params...),
	}})
}

func unmanagedExecution(tool acp.ToolCall) bool {
	if tool.Kind == acp.KindExecute || tool.Kind == acp.KindSwitchMode {
		return true
	}
	switch tool.Name {
	case "bash", "shell", "execute", "batch", "task":
		return true
	}
	return false
}

func approvalAction(tool acp.ToolCall) protocol.ApprovalAction {
	action := protocol.ApprovalAction{Kind: "other", Summary: tool.Title, Detail: string(tool.RawInput)}
	if action.Summary == "" {
		action.Summary = tool.Name
	}
	if action.Summary == "" {
		action.Summary = "OpenCode tool permission"
	}
	switch tool.Kind {
	case acp.KindExecute:
		action.Kind = "exec"
	case acp.KindEdit, acp.KindDelete, acp.KindMove:
		action.Kind = "edit"
	case acp.KindFetch:
		action.Kind = "network"
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(tool.RawInput, &input) == nil {
		_ = json.Unmarshal(input["command"], &action.Command)
		for _, key := range []string{"filePath", "filepath", "path", "url"} {
			if json.Unmarshal(input[key], &action.Target) == nil && action.Target != "" {
				break
			}
		}
	}
	if strings.HasPrefix(tool.Name, "yip_") {
		action.Kind = "mcp"
	}
	if action.Target == "" && len(tool.Locations) > 0 {
		action.Target = tool.Locations[0].Path
	}
	return action
}

func (s *session) HandleNotification(method string, raw json.RawMessage) {
	if method != acp.MethodSessionUpdate {
		return
	}
	var notification acp.SessionNotification
	if json.Unmarshal(raw, &notification) != nil {
		s.fail("OpenCode emitted an invalid session update")
		return
	}
	s.mu.Lock()
	if s.id == "" || notification.SessionID != s.id {
		s.mu.Unlock()
		return
	}
	u := notification.Update
	var events []providers.Event
	switch u.SessionUpdate {
	case "agent_message_chunk":
		if u.Content != nil && u.Content.Type == "text" {
			s.text.WriteString(u.Content.Text)
			events = append(events, providers.Event{Kind: providers.EventMessageDelta, Text: u.Content.Text})
		}
	case "tool_call", "tool_call_update":
		tool := s.tools[u.ToolCallID]
		tool.Merge(u.ToolCall)
		tool.ToolCallID = u.ToolCallID
		s.tools[u.ToolCallID] = tool
		if unmanagedExecution(tool) && (tool.Status == acp.StatusInProgress || tool.Status == acp.StatusCompleted) {
			s.failure = "OpenCode reported forbidden unmanaged execution"
			s.exitUncertain = true
			s.stop()
		}
		if s.readOnly && (tool.Status == acp.StatusInProgress || tool.Status == acp.StatusCompleted) {
			switch tool.Kind {
			case acp.KindEdit, acp.KindDelete, acp.KindMove, acp.KindExecute, acp.KindSwitchMode:
				s.failure = "OpenCode reported a forbidden tool execution in read-only mode"
				s.stop()
			}
		}
		data, _ := json.Marshal(tool)
		name := tool.Name
		if name == "" {
			name = tool.Title
		}
		if !s.started[u.ToolCallID] {
			s.started[u.ToolCallID] = true
			events = append(events, providers.Event{Kind: providers.EventToolStarted, Tool: name, Data: data})
		}
		if (tool.Status == acp.StatusCompleted || tool.Status == acp.StatusFailed) && !s.finished[u.ToolCallID] {
			s.finished[u.ToolCallID] = true
			events = append(events, providers.Event{Kind: providers.EventToolFinished, Tool: name, Data: data})
		}
	case "usage_update":
		if u.Cost != nil && u.Cost.Currency == "USD" {
			if s.usage == nil {
				s.usage = &protocol.Usage{Source: "vendor", Billing: protocol.BillingUnknown}
			}
			cost := u.Cost.Amount
			s.usage.CostUSD = &cost
			copy := *s.usage
			events = append(events, providers.Event{Kind: providers.EventUsage, Usage: &copy})
		}
	case "current_mode_update":
		if u.CurrentModeID != "yip" {
			s.failure = "OpenCode left the controlled yip mode"
			events = append(events, providers.Event{Kind: providers.EventError, Text: "OpenCode left the controlled yip mode"})
			s.stop()
		}
	case "config_option_update":
		if mode, ok := acp.FindConfig(u.ConfigOptions, "mode"); ok && mode.Current() != "yip" {
			s.failure = "OpenCode changed the controlled agent"
			events = append(events, providers.Event{Kind: providers.EventError, Text: "OpenCode changed the controlled agent"})
			s.stop()
		}
	case "plan":
		for _, entry := range u.Entries {
			events = append(events, providers.Event{Kind: providers.EventStatus, Text: fmt.Sprintf("%s: %s", entry.Status, entry.Content)})
		}
	}
	s.mu.Unlock()
	for _, event := range events {
		s.emit(event)
	}
}
