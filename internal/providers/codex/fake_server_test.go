package codex

// A scripted stand-in for `codex app-server`, used through the re-exec
// pattern: tests write a tiny `codex` shell wrapper that exports
// YIP_CODEX_FAKE=<scenario> and execs this test binary, and TestMain turns
// the process into the fake server. Every message the adapter sends is
// logged (JSON lines) to YIP_CODEX_FAKE_LOG so tests can assert on the
// exact protocol traffic.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("YIP_CODEX_FAKE_MCP") == "1" {
		os.Exit(runFakeMCP())
	}
	if sc := os.Getenv("YIP_CODEX_FAKE"); sc != "" {
		os.Exit(runFake(sc, os.Args[1:]))
	}
	os.Exit(m.Run())
}

type fakeMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type fake struct {
	in       *bufio.Reader
	log      *os.File
	thread   string
	turn     string
	turnSeq  int
	cwd      string
	scenario string
}

func (f *fake) logJSON(v any) {
	if f.log == nil {
		return
	}
	b, _ := json.Marshal(v)
	f.log.Write(append(b, '\n'))
}

func (f *fake) send(v any) {
	b, _ := json.Marshal(v)
	os.Stdout.Write(append(b, '\n'))
}

func (f *fake) respond(id json.RawMessage, result any) {
	f.send(map[string]any{"id": id, "result": result})
}

func (f *fake) respondErr(id json.RawMessage, code int, msg string) {
	f.send(map[string]any{"id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (f *fake) notify(method string, params any) {
	f.send(map[string]any{"method": method, "params": params})
}

func (f *fake) request(id int, method string, params any) {
	f.send(map[string]any{"id": id, "method": method, "params": params})
}

// next reads the next message from the adapter (exits cleanly on EOF).
func (f *fake) next() fakeMsg {
	line, err := f.in.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		f.logJSON(map[string]any{"eof": true})
		os.Exit(0)
	}
	var m fakeMsg
	if jerr := json.Unmarshal(line, &m); jerr != nil {
		fmt.Fprintf(os.Stderr, "fake: bad json: %s\n", line)
		os.Exit(4)
	}
	f.logJSON(map[string]any{"in": m})
	return m
}

func (f *fake) expect(method string) fakeMsg {
	for {
		m := f.next()
		if m.Method == method {
			return m
		}
	}
}

// awaitResponse waits for the adapter's response to a server request.
func (f *fake) awaitResponse(id int) fakeMsg {
	want := fmt.Sprint(id)
	for {
		m := f.next()
		if m.Method == "" && strings.Trim(string(m.ID), `"`) == want {
			return m
		}
	}
}

func (f *fake) drain() {
	for {
		f.next()
	}
}

func (f *fake) handshake() {
	m := f.expect("initialize")
	f.respond(m.ID, map[string]any{"codexHome": "/tmp/codex-home", "platformFamily": "unix", "platformOs": "macos", "userAgent": "fake/0.147.0"})
	f.expect("initialized")
}

func (f *fake) account(acct any, requires bool) {
	m := f.expect("account/read")
	f.respond(m.ID, map[string]any{"account": acct, "requiresOpenaiAuth": requires})
}

var chatgptAccount = map[string]any{"type": "chatgpt", "email": "dev@example.com", "planType": "pro"}

// configRead answers config/read. The user layer lives in
// YIP_CODEX_FAKE_HOME when set (tests put rules/ there), otherwise in a
// directory that does not exist.
func (f *fake) configRead() {
	m := f.expect("config/read")
	if config := os.Getenv("YIP_CODEX_FAKE_GATEWAY_CONFIG"); config != "" {
		f.respond(m.ID, map[string]any{"config": json.RawMessage(config), "layers": []any{}})
		return
	}
	home := os.Getenv("YIP_CODEX_FAKE_HOME")
	if home == "" {
		home = f.cwd + "/../no-codex-home"
	}
	f.respond(m.ID, map[string]any{
		"config": map[string]any{"model": "gpt-test", "mcp_servers": map[string]any{"github": map[string]any{"command": "gh-mcp"}}},
		"layers": []map[string]any{
			{"name": map[string]any{"type": "system", "file": f.cwd + "/../no-etc-codex/config.toml"}, "version": "1", "config": map[string]any{}},
			{"name": map[string]any{"type": "user", "file": home + "/config.toml"}, "version": "1", "config": map[string]any{"mcp_servers": map[string]any{"github": map[string]any{"command": "gh-mcp"}}}},
			{"name": map[string]any{"type": "project", "dotCodexFolder": f.cwd + "/.codex"}, "version": "1", "disabledReason": "untrusted project",
				"config": map[string]any{"mcp_servers": map[string]any{"evil": map[string]any{"command": "./evil.sh"}}}},
		},
		"origins": map[string]any{},
	})
}

// hooksList answers hooks/list (default: one untrusted, non-runnable hook).
func (f *fake) hooksList(hooks []map[string]any) {
	m := f.expect("hooks/list")
	if hooks == nil {
		hooks = []map[string]any{{
			"key": "project:pre_tool_use:0:0", "eventName": "preToolUse", "handlerType": "command", "isManaged": false,
			"command": "./hook.sh", "timeoutSec": 5, "sourcePath": f.cwd + "/.codex/hooks.json", "source": "project",
			"displayOrder": 0, "enabled": true, "currentHash": "sha256:x", "trustStatus": "untrusted",
		}}
	}
	f.respond(m.ID, map[string]any{"data": []map[string]any{{"cwd": f.cwd, "hooks": hooks, "warnings": []string{}, "errors": []any{}}}})
}

// preThread runs the handshake and pre-thread inspection.
func (f *fake) preThread(acct any, requires bool, hooks []map[string]any) {
	f.handshake()
	f.configRead()
	f.account(acct, requires)
	f.hooksList(hooks)
}

func (f *fake) threadResponse(id json.RawMessage, sandbox string) {
	f.respond(id, map[string]any{
		"thread":             map[string]any{"id": f.thread, "cwd": f.cwd, "ephemeral": false, "turns": []any{}},
		"cwd":                f.cwd,
		"model":              "gpt-test",
		"modelProvider":      "openai",
		"approvalPolicy":     "on-request",
		"approvalsReviewer":  "user",
		"sandbox":            map[string]any{"type": sandbox},
		"instructionSources": []string{f.cwd + "/AGENTS.md"},
	})
	f.notify("thread/started", map[string]any{"thread": map[string]any{"id": f.thread}})
}

// setup runs handshake → thread start and returns after thread/started.
func (f *fake) setup(sandbox string) {
	f.preThread(chatgptAccount, true, nil)
	m := f.expect("thread/start")
	f.thread = "thr_1"
	f.threadResponse(m.ID, sandbox)
	f.notify("mcpServer/startupStatus/updated", map[string]any{"threadId": f.thread, "name": "yip", "status": "ready", "error": nil})
}

// startTurn answers the next turn/start and returns its params.
func (f *fake) startTurn() fakeMsg {
	m := f.expect("turn/start")
	f.turnSeq++
	f.turn = fmt.Sprintf("turn_%d", f.turnSeq)
	f.respond(m.ID, map[string]any{"turn": map[string]any{"id": f.turn, "status": "inProgress", "items": []any{}}})
	f.notify("turn/started", map[string]any{"threadId": f.thread, "turn": map[string]any{"id": f.turn, "status": "inProgress", "items": []any{}}})
	return m
}

func (f *fake) item(method string, item map[string]any) {
	f.notify(method, map[string]any{"threadId": f.thread, "turnId": f.turn, "item": item, "startedAtMs": 1, "completedAtMs": 2})
}

func (f *fake) agentMessage(id, text string, deltas ...string) {
	f.item("item/started", map[string]any{"type": "agentMessage", "id": id, "text": ""})
	for _, d := range deltas {
		f.notify("item/agentMessage/delta", map[string]any{"threadId": f.thread, "turnId": f.turn, "itemId": id, "delta": d})
	}
	f.item("item/completed", map[string]any{"type": "agentMessage", "id": id, "text": text})
}

func (f *fake) usage(turnID string, in, out int) {
	b := map[string]any{"inputTokens": in, "cachedInputTokens": 0, "outputTokens": out, "reasoningOutputTokens": 0, "totalTokens": in + out}
	f.notify("thread/tokenUsage/updated", map[string]any{"threadId": f.thread, "turnId": turnID, "tokenUsage": map[string]any{"total": b, "last": b}})
}

func (f *fake) completeTurn(status string, turnErr any) {
	t := map[string]any{"id": f.turn, "status": status, "items": []any{}, "error": turnErr}
	f.notify("turn/completed", map[string]any{"threadId": f.thread, "turn": t})
}

func runFake(scenario string, args []string) int {
	for _, a := range args {
		if a == "--version" {
			fmt.Println("codex-cli 0.147.0")
			return 0
		}
	}
	if len(args) == 0 || args[0] != "app-server" {
		fmt.Fprintln(os.Stderr, "fake codex: expected app-server")
		return 2
	}
	cwd, _ := os.Getwd()
	f := &fake{in: bufio.NewReaderSize(os.Stdin, 1<<20), cwd: cwd, scenario: scenario}
	if p := os.Getenv("YIP_CODEX_FAKE_LOG"); p != "" {
		lf, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			f.log = lf
			defer lf.Close()
		}
	}
	f.logJSON(map[string]any{"argv": args, "env": os.Environ(), "cwd": cwd})

	switch scenario {
	case "probe", "probe-rules":
		f.handshake()
		f.configRead()
		f.account(map[string]any{"type": "apiKey"}, true)
		m := f.expect("model/list")
		f.respond(m.ID, map[string]any{"data": []map[string]any{
			{"id": "gpt-a", "model": "gpt-a", "displayName": "GPT A", "description": "fast", "hidden": false, "isDefault": true, "defaultReasoningEffort": "medium", "supportedReasoningEfforts": []any{}},
			{"id": "gpt-hidden", "model": "gpt-hidden", "displayName": "Hidden", "description": "", "hidden": true, "isDefault": false, "defaultReasoningEffort": "medium", "supportedReasoningEfforts": []any{}},
		}, "nextCursor": "page2"})
		m = f.expect("model/list")
		f.respond(m.ID, map[string]any{"data": []map[string]any{
			{"id": "gpt-b", "model": "gpt-b", "displayName": "GPT B", "description": "deep", "hidden": false, "isDefault": false, "defaultReasoningEffort": "high", "supportedReasoningEfforts": []any{}},
		}, "nextCursor": nil})
		f.drain()

	case "probe-signedout", "probe-gateway":
		f.handshake()
		f.configRead()
		f.account(nil, scenario != "probe-gateway")
		m := f.expect("model/list")
		f.respond(m.ID, map[string]any{"data": []any{}, "nextCursor": nil})
		f.drain()

	case "rules":
		f.handshake()
		f.configRead()
		f.account(chatgptAccount, true)
		f.hooksList(nil)
		m := f.expect("thread/start")
		f.thread = "thr_1"
		f.threadResponse(m.ID, "workspaceWrite")
		f.startTurn()
		f.agentMessage("msg_1", "ok")
		f.completeTurn("completed", nil)
		f.drain()

	case "basic":
		f.setup("workspaceWrite")
		f.startTurn()
		f.agentMessage("msg_1", "Hello", "Hel", "lo")
		f.item("item/started", map[string]any{"type": "commandExecution", "id": "cmd_1", "command": "go test ./...", "cwd": f.cwd, "status": "inProgress", "commandActions": []any{}})
		f.item("item/completed", map[string]any{"type": "commandExecution", "id": "cmd_1", "command": "go test ./...", "cwd": f.cwd, "status": "completed", "exitCode": 0, "commandActions": []any{}})
		f.item("item/started", map[string]any{"type": "fileChange", "id": "fc_1", "status": "inProgress", "changes": []any{map[string]any{"path": f.cwd + "/main.go", "kind": map[string]any{"type": "update"}, "diff": "@@ -1 +1 @@"}}})
		f.item("item/completed", map[string]any{"type": "fileChange", "id": "fc_1", "status": "completed", "changes": []any{map[string]any{"path": f.cwd + "/main.go", "kind": map[string]any{"type": "update"}, "diff": "@@ -1 +1 @@"}}})
		f.item("item/started", map[string]any{"type": "mcpToolCall", "id": "mcp_1", "server": "yip", "tool": "room_post", "status": "inProgress", "arguments": map[string]any{"text": "hi"}})
		f.item("item/completed", map[string]any{"type": "mcpToolCall", "id": "mcp_1", "server": "yip", "tool": "room_post", "status": "completed", "arguments": map[string]any{"text": "hi"}})
		f.usage(f.turn, 100, 20)
		f.completeTurn("completed", nil)
		f.drain()

	case "approvals":
		f.setup("workspaceWrite")
		f.startTurn()
		f.item("item/started", map[string]any{"type": "commandExecution", "id": "cmd_1", "command": "rm -rf build", "cwd": f.cwd, "status": "inProgress", "commandActions": []any{}})
		f.request(100, "item/commandExecution/requestApproval", map[string]any{"threadId": f.thread, "turnId": f.turn, "itemId": "cmd_1", "startedAtMs": 1, "command": "rm -rf build", "cwd": f.cwd, "reason": "needs to write outside the sandbox"})
		r := f.awaitResponse(100)
		status := "declined"
		if strings.Contains(string(r.Result), `"accept"`) {
			status = "completed"
		}
		f.notify("serverRequest/resolved", map[string]any{"threadId": f.thread, "requestId": 100})
		f.item("item/completed", map[string]any{"type": "commandExecution", "id": "cmd_1", "command": "rm -rf build", "cwd": f.cwd, "status": status, "exitCode": 0, "commandActions": []any{}})
		f.item("item/started", map[string]any{"type": "fileChange", "id": "fc_1", "status": "inProgress", "changes": []any{map[string]any{"path": f.cwd + "/main.go", "kind": map[string]any{"type": "update"}, "diff": "-a\n+b"}}})
		f.request(101, "item/fileChange/requestApproval", map[string]any{"threadId": f.thread, "turnId": f.turn, "itemId": "fc_1", "startedAtMs": 1, "reason": "edit main"})
		r = f.awaitResponse(101)
		status = "declined"
		if strings.Contains(string(r.Result), `"accept"`) {
			status = "completed"
		}
		f.item("item/completed", map[string]any{"type": "fileChange", "id": "fc_1", "status": status, "changes": []any{map[string]any{"path": f.cwd + "/main.go", "kind": map[string]any{"type": "update"}, "diff": "-a\n+b"}}})
		f.agentMessage("msg_1", "done")
		f.completeTurn("completed", nil)
		f.drain()

	case "readonly":
		f.setup("readOnly")
		f.startTurn()
		f.request(100, "item/commandExecution/requestApproval", map[string]any{"threadId": f.thread, "turnId": f.turn, "itemId": "cmd_1", "startedAtMs": 1, "command": "touch x", "cwd": f.cwd})
		f.awaitResponse(100)
		f.request(101, "item/fileChange/requestApproval", map[string]any{"threadId": f.thread, "turnId": f.turn, "itemId": "fc_9", "startedAtMs": 1})
		f.awaitResponse(101)
		f.agentMessage("msg_1", "reviewed")
		f.completeTurn("completed", nil)
		f.drain()

	case "questions":
		f.setup("workspaceWrite")
		f.startTurn()
		f.request(200, "item/tool/requestUserInput", map[string]any{"threadId": f.thread, "turnId": f.turn, "itemId": "ui_1", "isBlocking": true,
			"questions": []any{map[string]any{"id": "q1", "header": "Database", "question": "Which database?", "options": []any{
				map[string]any{"label": "sqlite", "description": "embedded"}, map[string]any{"label": "postgres", "description": "server"}}}}})
		f.awaitResponse(200)
		f.request(201, "mcpServer/elicitation/request", map[string]any{"threadId": f.thread, "turnId": f.turn, "serverName": "yip", "mode": "form",
			"message": "Allow yip to run room_post?", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			"_meta": map[string]any{"codex_approval_kind": "mcp_tool_call"}})
		f.awaitResponse(201)
		f.request(202, "currentTime/read", map[string]any{"threadId": f.thread})
		f.awaitResponse(202)
		f.request(203, "item/permissions/requestApproval", map[string]any{"threadId": f.thread, "turnId": f.turn, "itemId": "p1", "startedAtMs": 1, "cwd": f.cwd,
			"permissions": map[string]any{"network": map[string]any{"enabled": true}}})
		f.awaitResponse(203)
		f.agentMessage("msg_1", "answered")
		f.completeTurn("completed", nil)
		f.drain()

	case "steer":
		f.setup("workspaceWrite")
		f.startTurn()
		f.agentMessage("msg_0", "working", "working")
		m := f.expect("turn/steer")
		var p struct {
			ExpectedTurnID string `json:"expectedTurnId"`
			Input          []struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		json.Unmarshal(m.Params, &p)
		if p.ExpectedTurnID != f.turn {
			f.respondErr(m.ID, -32600, "expected turn mismatch")
		} else {
			f.respond(m.ID, map[string]any{"turnId": f.turn})
		}
		f.agentMessage("msg_1", "steered: "+p.Input[0].Text)
		f.completeTurn("completed", nil)
		f.drain()

	case "steer-queued":
		f.setup("workspaceWrite")
		f.startTurn()
		f.agentMessage("msg_0", "working", "working")
		m := f.expect("turn/steer")
		f.respondErr(m.ID, -32600, "no active turn to steer")
		time.Sleep(300 * time.Millisecond)
		f.agentMessage("msg_1", "first turn")
		f.completeTurn("completed", nil)
		second := f.startTurn()
		var p struct {
			Input []struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		json.Unmarshal(second.Params, &p)
		f.agentMessage("msg_2", "second turn: "+p.Input[0].Text)
		f.completeTurn("completed", nil)
		f.drain()

	case "cancel":
		f.setup("workspaceWrite")
		f.startTurn()
		child := exec.Command("sleep", "60")
		child.Start()
		f.logJSON(map[string]any{"childPid": child.Process.Pid})
		f.agentMessage("msg_0", "working", "working")
		m := f.expect("turn/interrupt")
		f.respond(m.ID, map[string]any{})
		f.completeTurn("interrupted", nil)
		f.drain()

	case "cancel-stuck":
		// Ignores turn/interrupt and SIGTERM: the adapter must escalate to
		// SIGKILL on the process group.
		signal.Ignore(syscall.SIGTERM)
		f.setup("workspaceWrite")
		f.startTurn()
		child := exec.Command("sleep", "60")
		child.Start()
		f.logJSON(map[string]any{"childPid": child.Process.Pid})
		f.agentMessage("msg_0", "working", "working")
		f.expect("turn/interrupt")
		f.logJSON(map[string]any{"stuck": true})
		for {
			// Sleep (not select{}): the runtime would otherwise abort with
			// "all goroutines are asleep" instead of hanging.
			time.Sleep(time.Hour)
		}

	case "resume":
		f.preThread(chatgptAccount, true, nil)
		m := f.expect("thread/resume")
		var p struct {
			ThreadID string `json:"threadId"`
		}
		json.Unmarshal(m.Params, &p)
		f.thread = p.ThreadID
		f.threadResponse(m.ID, "workspaceWrite")
		f.usage("old_turn", 1000, 200) // replayed usage of the stored thread
		f.startTurn()
		f.agentMessage("msg_1", "resumed")
		f.usage(f.turn, 1100, 230)
		f.completeTurn("completed", nil)
		f.drain()

	case "ratelimit", "ratelimit-noreset":
		f.setup("workspaceWrite")
		f.startTurn()
		if scenario == "ratelimit" {
			reset := time.Now().Add(time.Hour).Unix()
			f.notify("account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{
				"primary":   map[string]any{"usedPercent": 100, "windowDurationMins": 300, "resetsAt": reset},
				"secondary": map[string]any{"usedPercent": 40, "windowDurationMins": 10080, "resetsAt": time.Now().Add(72 * time.Hour).Unix()},
			}})
		}
		e := map[string]any{"message": "You've hit your usage limit.", "codexErrorInfo": "usageLimitExceeded"}
		f.notify("error", map[string]any{"threadId": f.thread, "turnId": f.turn, "willRetry": false, "error": e})
		f.completeTurn("failed", e)
		if scenario == "ratelimit-noreset" {
			m := f.expect("account/rateLimits/read")
			f.respond(m.ID, map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 30, "resetsAt": time.Now().Add(time.Hour).Unix()}}})
		}
		f.drain()

	case "auth":
		f.handshake()
		f.configRead()
		f.account(nil, true)
		f.drain()

	case "auth-midturn":
		f.setup("workspaceWrite")
		f.startTurn()
		e := map[string]any{"message": "unexpected status 401 Unauthorized", "codexErrorInfo": map[string]any{"responseStreamConnectionFailed": map[string]any{"httpStatusCode": 401}}}
		f.completeTurn("failed", e)
		f.drain()

	case "crash":
		f.setup("workspaceWrite")
		f.startTurn()
		f.agentMessage("msg_0", "working", "working")
		fmt.Fprintln(os.Stderr, "thread 'main' panicked: something broke")
		return 3

	case "hooks":
		f.preThread(chatgptAccount, true, []map[string]any{{
			"key": "user:session_start:0:0", "eventName": "sessionStart", "handlerType": "command", "isManaged": false,
			"command": "curl evil", "timeoutSec": 5, "sourcePath": "/home/.codex/hooks.json", "source": "user",
			"displayOrder": 0, "enabled": true, "currentHash": "sha256:y", "trustStatus": "trusted",
		}})
		f.drain()

	default:
		fmt.Fprintf(os.Stderr, "fake codex: unknown scenario %q\n", scenario)
		return 2
	}
	return 0
}
