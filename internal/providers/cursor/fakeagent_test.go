package cursor

// A faithful fake of `agent acp` for tests, built from Cursor's ACP page
// (https://cursor.com/docs/cli/acp) and the ACP v1 spec. The test binary
// re-executes itself with YIP_FAKE_CURSOR_ACP=1 and behaves as the Cursor CLI:
//
//	<exe> acp                      ACP server on stdio (newline-delimited JSON-RPC 2.0)
//	<exe> --version                version string
//	<exe> status --format json     auth status
//	<exe> models                   model list
//
// Every message the fake receives is appended to YIP_FAKE_CURSOR_LOG as one
// JSON line so tests can assert exactly what the adapter sent.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	envFake     = "YIP_FAKE_CURSOR_ACP"
	envScenario = "YIP_FAKE_CURSOR_SCENARIO"
	envLog      = "YIP_FAKE_CURSOR_LOG"
)

type fmsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type fakeAgent struct {
	scenario  string
	in        chan fmsg
	wmu       sync.Mutex
	logf      *os.File
	nextID    int
	sessionID string
	mode      string
	model     string
	prompts   int
	cancelled bool
}

func runFakeAgent() int {
	args := os.Args[1:]
	scenario := os.Getenv(envScenario)
	switch {
	case len(args) == 1 && (args[0] == "--version" || args[0] == "-v"):
		fmt.Println("2026.09.20-fake")
		return 0
	case len(args) >= 1 && args[0] == "status":
		if scenario == "probe-signedout" {
			fmt.Println(`{"isAuthenticated":false,"endpoint":"https://api2.cursor.sh"}`)
			return 0
		}
		fmt.Println(`{"status":"authenticated","user":{"email":"dev@example.com"}}`)
		return 0
	case len(args) == 1 && args[0] == "models":
		fmt.Print("Available models:\n* auto - Auto (current)\n  gpt-5 - GPT-5\n  sonnet-4-thinking - Claude Sonnet 4 Thinking\n\nTip: use --model <id>\n")
		return 0
	case len(args) == 1 && args[0] == "acp":
	default:
		fmt.Fprintf(os.Stderr, "fake agent: unexpected args %q\n", args)
		return 2
	}

	f := &fakeAgent{scenario: scenario, in: make(chan fmsg, 64), mode: "agent", model: "auto"}
	if p := os.Getenv(envLog); p != "" {
		lf, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			f.logf = lf
		}
	}
	if scenario == "cancel-hang" {
		signal.Ignore(syscall.SIGTERM) // force the adapter's SIGKILL path
	}
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			line := sc.Bytes()
			f.logRaw(line)
			var m fmsg
			if json.Unmarshal(line, &m) == nil {
				f.in <- m
			}
		}
		close(f.in)
	}()
	for m := range f.in {
		f.handle(m)
	}
	return 0
}

func (f *fakeAgent) logRaw(line []byte) {
	if f.logf != nil {
		f.logf.Write(append(append([]byte(nil), line...), '\n'))
	}
}

func (f *fakeAgent) note(v map[string]any) {
	v["fake"] = true
	b, _ := json.Marshal(v)
	f.logRaw(b)
}

func (f *fakeAgent) send(v any) {
	b, _ := json.Marshal(v)
	f.wmu.Lock()
	os.Stdout.Write(append(b, '\n'))
	f.wmu.Unlock()
}

func (f *fakeAgent) reply(id json.RawMessage, result any) {
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (f *fakeAgent) replyErr(id json.RawMessage, code int, msg string, data any) {
	e := map[string]any{"code": code, "message": msg}
	if data != nil {
		e["data"] = data
	}
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func (f *fakeAgent) notify(method string, params any) {
	f.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (f *fakeAgent) update(u map[string]any) {
	f.notify("session/update", map[string]any{"sessionId": f.sessionID, "update": u})
}

func (f *fakeAgent) chunk(msgID, text string) {
	u := map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}
	if msgID != "" {
		u["messageId"] = msgID
	}
	f.update(u)
}

// call sends an agent->client request (string IDs, as some agents use) and
// waits for its response, handling other traffic meanwhile.
func (f *fakeAgent) call(method string, params any) fmsg {
	f.nextID++
	id := fmt.Sprintf("agent-%d", f.nextID)
	idRaw, _ := json.Marshal(id)
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	for m := range f.in {
		if m.Method == "" && string(m.ID) == string(idRaw) {
			return m
		}
		f.handle(m)
	}
	os.Exit(0)
	return fmsg{}
}

// serveUntil handles traffic until pred matches a message (which is returned
// unhandled) or input ends.
func (f *fakeAgent) serveUntil(pred func(fmsg) bool) (fmsg, bool) {
	for m := range f.in {
		if pred(m) {
			return m, true
		}
		f.handle(m)
	}
	return fmsg{}, false
}

func (f *fakeAgent) configOptions() []map[string]any {
	return []map[string]any{
		{"id": "mode", "name": "Mode", "category": "mode", "type": "select", "currentValue": f.mode, "options": []map[string]any{
			{"value": "agent", "name": "Agent"}, {"value": "plan", "name": "Plan"}, {"value": "ask", "name": "Ask"},
		}},
		{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": f.model, "options": []map[string]any{
			{"group": "recommended", "name": "Recommended", "options": []map[string]any{{"value": "auto", "name": "Auto"}}},
			{"group": "all", "name": "All", "options": []map[string]any{{"value": "gpt-5[fast=true]", "name": "GPT-5 Fast"}, {"value": "sonnet-4", "name": "Sonnet 4"}}},
		}},
	}
}

func (f *fakeAgent) sessionSetup(id string) map[string]any {
	if f.scenario == "nomodes" {
		return map[string]any{"sessionId": id}
	}
	return map[string]any{
		"sessionId":     id,
		"configOptions": f.configOptions(),
		"modes": map[string]any{"currentModeId": f.mode, "availableModes": []map[string]any{
			{"id": "agent", "name": "Agent"}, {"id": "plan", "name": "Plan"}, {"id": "ask", "name": "Ask"},
		}},
		"models": map[string]any{"currentModelId": f.model, "availableModels": []map[string]any{
			{"modelId": "auto", "name": "Auto"}, {"modelId": "gpt-5[fast=true]", "name": "GPT-5 Fast"}, {"modelId": "sonnet-4", "name": "Sonnet 4"},
		}},
	}
}

func (f *fakeAgent) handle(m fmsg) {
	if m.Method == "" {
		return // response to something we no longer wait for
	}
	var p map[string]any
	_ = json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "initialize":
		caps := map[string]any{
			"loadSession":        f.scenario != "noload",
			"promptCapabilities": map[string]any{"image": true, "embeddedContext": true},
			"mcpCapabilities":    map[string]any{"http": true, "sse": false},
		}
		if f.scenario == "sresume" {
			caps["sessionCapabilities"] = map[string]any{"resume": map[string]any{}}
		}
		f.reply(m.ID, map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": caps,
			"agentInfo":         map[string]any{"name": "cursor-agent", "title": "Cursor Agent (fake)", "version": "2026.09.20-fake"},
			"authMethods":       []map[string]any{{"id": "cursor_login", "name": "Cursor Login"}},
		})
	case "authenticate":
		if f.scenario == "auth" {
			f.replyErr(m.ID, -32000, "Authentication required: run `agent login`", nil)
			return
		}
		f.reply(m.ID, map[string]any{})
	case "session/new":
		if f.scenario == "hang-new" {
			return // never answer
		}
		f.sessionID = "sess-new-1"
		f.reply(m.ID, f.sessionSetup(f.sessionID))
	case "session/load":
		id, _ := p["sessionId"].(string)
		if id != "sess-old" {
			f.replyErr(m.ID, -32002, "Session not found", nil)
			return
		}
		f.sessionID = id
		f.update(map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "old question"}})
		f.chunk("old-1", "OLD HISTORY ANSWER")
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "old-tool", "title": "Old tool", "kind": "edit", "status": "completed"})
		f.reply(m.ID, f.sessionSetup(id))
	case "session/resume":
		id, _ := p["sessionId"].(string)
		f.sessionID = id
		f.reply(m.ID, f.sessionSetup(id))
	case "session/set_config_option":
		cfg, _ := p["configId"].(string)
		val, _ := p["value"].(string)
		switch cfg {
		case "mode":
			f.mode = val
		case "model":
			f.model = val
		}
		f.reply(m.ID, map[string]any{"configOptions": f.configOptions()})
		if cfg == "mode" {
			f.update(map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": val})
		}
	case "session/set_mode":
		f.mode, _ = p["modeId"].(string)
		f.reply(m.ID, map[string]any{})
	case "session/cancel":
		f.cancelled = true
	case "session/prompt":
		f.prompts++
		f.cancelled = false
		f.turn(m, p)
	default:
		if len(m.ID) > 0 {
			f.replyErr(m.ID, -32601, "Method not found", nil)
		}
	}
}

func promptText(p map[string]any) string {
	blocks, _ := p["prompt"].([]any)
	var b strings.Builder
	for _, x := range blocks {
		if blk, ok := x.(map[string]any); ok {
			if t, ok := blk["text"].(string); ok {
				b.WriteString(t)
			}
		}
	}
	return b.String()
}

func optionOf(r fmsg) string {
	if r.Error != nil {
		return fmt.Sprintf("error:%d", r.Error.Code)
	}
	var res struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(r.Result, &res)
	if res.Outcome.Outcome == "cancelled" {
		return "cancelled"
	}
	return res.Outcome.OptionID
}

var permOptions = []map[string]any{
	{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
	{"optionId": "allow-always", "name": "Allow always", "kind": "allow_always"},
	{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
}

func (f *fakeAgent) permission(toolCallID string) fmsg {
	return f.call("session/request_permission", map[string]any{
		"sessionId": f.sessionID,
		"toolCall":  map[string]any{"toolCallId": toolCallID},
		"options":   permOptions,
	})
}

func (f *fakeAgent) end(id json.RawMessage, reason string) {
	f.reply(id, map[string]any{"stopReason": reason})
}

func (f *fakeAgent) turn(m fmsg, p map[string]any) {
	cwd, _ := os.Getwd()
	switch f.scenario {
	case "basic":
		f.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "thinking..."}})
		f.update(map[string]any{"sessionUpdate": "plan", "entries": []map[string]any{
			{"content": "Read the README", "priority": "high", "status": "in_progress"},
			{"content": "Answer", "priority": "medium", "status": "pending"},
		}})
		f.chunk("m1", "Looking at ")
		f.chunk("m1", "the repo.")
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Read README.md", "kind": "read", "status": "pending",
			"locations": []map[string]any{{"path": cwd + "/README.md"}}})
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "in_progress"})
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed",
			"content": []map[string]any{{"type": "content", "content": map[string]any{"type": "text", "text": "# readme"}}}})
		f.chunk("m2", "Hello ")
		f.chunk("m2", "world")
		f.update(map[string]any{"sessionUpdate": "usage_update", "used": 1200, "size": 200000, "cost": map[string]any{"amount": 0.12, "currency": "USD"}})
		f.notify("cursor/update_todos", map[string]any{"toolCallId": "td", "merge": false, "todos": []map[string]any{
			{"id": "1", "content": "Read", "status": "completed"}, {"id": "2", "content": "Answer", "status": "in_progress"},
		}})
		f.reply(m.ID, map[string]any{"stopReason": "end_turn", "usage": map[string]any{"totalTokens": 30, "inputTokens": 20, "outputTokens": 10}})

	case "permissions":
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "p1", "title": "Edit main.go", "kind": "edit", "status": "pending",
			"locations": []map[string]any{{"path": cwd + "/main.go"}},
			"content":   []map[string]any{{"type": "diff", "path": cwd + "/main.go", "oldText": "package main\n", "newText": "package main\n\nfunc main() {}\n"}}})
		r1 := optionOf(f.permission("p1"))
		f.chunk("", "perm1="+r1+"\n")
		if r1 == "allow-once" {
			f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "p1", "status": "completed"})
		}
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "p2", "title": "Run rm -rf build", "kind": "execute", "status": "pending",
			"rawInput": map[string]any{"command": "rm -rf build"}})
		r2 := optionOf(f.permission("p2"))
		f.chunk("", "perm2="+r2+"\n")
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "p3", "title": "Edit /etc/hosts", "kind": "edit", "status": "pending",
			"locations": []map[string]any{{"path": "/etc/hosts"}}})
		r3 := optionOf(f.permission("p3"))
		f.chunk("", "perm3="+r3+"\n")
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "p2", "status": "failed"})
		f.chunk("final", "done with permissions")
		f.end(m.ID, "end_turn")

	case "questions":
		ask := f.call("cursor/ask_question", map[string]any{
			"toolCallId": "call_123", "title": "Need input",
			"questions": []map[string]any{{"id": "q1", "prompt": "Which database?", "allowMultiple": false,
				"options": []map[string]any{{"id": "pg", "label": "Postgres"}, {"id": "sqlite", "label": "SQLite"}}}},
		})
		f.note(map[string]any{"askReply": json.RawMessage(ask.Result)})
		ask2 := f.call("cursor/ask_question", map[string]any{
			"toolCallId": "call_124",
			"questions":  []map[string]any{{"id": "q2", "prompt": "Ship it?", "options": []map[string]any{{"id": "yes", "label": "Yes"}, {"id": "no", "label": "No"}}}},
		})
		f.note(map[string]any{"ask2Reply": json.RawMessage(ask2.Result)})
		plan := f.call("cursor/create_plan", map[string]any{
			"toolCallId": "call_125", "name": "Refactor tabs", "overview": "Tighten layout.", "plan": "1. Inspect\n2. Update",
			"todos": []map[string]any{{"id": "t1", "content": "Inspect", "status": "pending"}},
		})
		f.note(map[string]any{"planReply": json.RawMessage(plan.Result)})
		unk := f.call("cursor/some_future_blocking_method", map[string]any{"x": 1})
		code := 0
		if unk.Error != nil {
			code = unk.Error.Code
		}
		f.note(map[string]any{"unknownReplyCode": code})
		fsr := f.call("fs/read_text_file", map[string]any{"sessionId": f.sessionID, "path": "/etc/passwd"})
		fsCode := 0
		if fsr.Error != nil {
			fsCode = fsr.Error.Code
		}
		f.note(map[string]any{"fsReplyCode": fsCode})
		f.notify("cursor/task", map[string]any{"toolCallId": "call_126", "description": "Explore codebase", "prompt": "find auth", "subagentType": "explore"})
		f.chunk("", "questions done")
		f.end(m.ID, "end_turn")

	case "steer":
		if f.prompts == 1 {
			f.chunk("", "first turn")
			f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "s1", "title": "Run go test ./...", "kind": "execute", "status": "pending",
				"rawInput": map[string]any{"command": "go test ./..."}})
			r := optionOf(f.permission("s1"))
			f.chunk("", " perm="+r)
			f.end(m.ID, "end_turn")
			return
		}
		f.chunk("", "steered: "+promptText(p))
		f.end(m.ID, "end_turn")

	case "resume", "noload", "sresume":
		f.chunk("", "resumed ok in "+f.sessionID)
		f.end(m.ID, "end_turn")

	case "cancel", "cancel-hang":
		child := exec.Command("sleep", "60")
		if err := child.Start(); err == nil {
			f.note(map[string]any{"childPid": child.Process.Pid})
		}
		f.chunk("", "working")
		if f.scenario == "cancel-hang" {
			// Never answer the prompt, ignore session/cancel, stdin EOF and
			// SIGTERM: only SIGKILL of the process group stops this agent.
			f.serveUntil(func(fmsg) bool { return false })
			f.note(map[string]any{"stdinClosed": true})
			time.Sleep(time.Hour)
			return
		}
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "Run make", "kind": "execute", "status": "pending",
			"rawInput": map[string]any{"command": "make"}})
		r := f.permission("c1")
		f.note(map[string]any{"cancelPermReply": optionOf(r), "sawCancel": f.cancelled})
		if !f.cancelled {
			f.serveUntil(func(x fmsg) bool { return x.Method == "session/cancel" })
			f.cancelled = true
		}
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "failed"})
		f.end(m.ID, "cancelled")

	case "ratelimit":
		f.chunk("", "partial")
		f.replyErr(m.ID, -32603, "Rate limit exceeded: you've hit your usage limit", map[string]any{"retryAfterMs": 90000})

	case "auth-prompt":
		f.replyErr(m.ID, -32000, "Not authenticated", nil)

	case "crash":
		f.chunk("", "partial output")
		fmt.Fprintln(os.Stderr, "fatal: renderer crashed (simulated)")
		time.Sleep(50 * time.Millisecond)
		os.Exit(3)

	case "readonly":
		f.note(map[string]any{"modeAtPrompt": f.mode})
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "r1", "title": "Edit README.md", "kind": "edit", "status": "pending",
			"locations": []map[string]any{{"path": cwd + "/README.md"}}})
		f.chunk("", "ro-edit="+optionOf(f.permission("r1"))+"\n")
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "r2", "title": "Fetch docs", "kind": "fetch", "status": "pending",
			"rawInput": map[string]any{"url": "https://example.com/docs"}})
		f.chunk("", "ro-fetch="+optionOf(f.permission("r2"))+"\n")
		// The agent switches itself out of ask mode; yip must switch it back.
		f.mode = "agent"
		f.update(map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "agent"})
		f.serveUntil(func(x fmsg) bool {
			if x.Method == "session/set_config_option" {
				f.handle(x)
				return true
			}
			return false
		})
		f.note(map[string]any{"modeAfterReassert": f.mode})
		f.chunk("", "read-only done")
		f.end(m.ID, "end_turn")

	case "readonly-violation":
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "v1", "title": "Write secrets.txt", "kind": "edit", "status": "in_progress",
			"locations": []map[string]any{{"path": cwd + "/secrets.txt"}}})
		f.serveUntil(func(x fmsg) bool { return x.Method == "session/cancel" })
		f.end(m.ID, "cancelled")

	case "withdraw":
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "w1", "title": "Run make deploy", "kind": "execute", "status": "pending"})
		f.nextID++
		id := fmt.Sprintf("agent-%d", f.nextID)
		idRaw, _ := json.Marshal(id)
		f.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/request_permission", "params": map[string]any{
			"sessionId": f.sessionID, "toolCall": map[string]any{"toolCallId": "w1"}, "options": permOptions}})
		time.Sleep(100 * time.Millisecond)
		f.notify("$/cancel_request", map[string]any{"requestId": id})
		r, _ := f.serveUntil(func(x fmsg) bool { return x.Method == "" && string(x.ID) == string(idRaw) })
		f.note(map[string]any{"withdrawReply": optionOf(r)})
		f.chunk("", "withdrawn")
		f.end(m.ID, "end_turn")

	case "model":
		f.chunk("", "model="+f.model)
		f.end(m.ID, "end_turn")

	default:
		f.chunk("", "ok: "+promptText(p))
		f.end(m.ID, "end_turn")
	}
}
