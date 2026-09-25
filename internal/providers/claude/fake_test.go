package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as a fake `claude` executable (and, for the real
// smoke test, as a stub MCP server). TestMain dispatches on these variables
// before any test runs.
const (
	envFake     = "YIP_CLAUDE_FAKE"      // scenario name
	envRecord   = "YIP_CLAUDE_FAKE_DIR"  // directory for recordings
	envAuthJSON = "YIP_CLAUDE_FAKE_AUTH" // auth status JSON
	envStubMCP  = "YIP_CLAUDE_STUB_MCP"  // run as the stub MCP server (smoke test)
)

func TestMain(m *testing.M) {
	if os.Getenv(envStubMCP) != "" {
		os.Exit(runStubMCP())
	}
	if sc := os.Getenv(envFake); sc != "" {
		os.Exit(runFake(sc))
	}
	os.Exit(m.Run())
}

func fakeHelpText(scenario string) string {
	var b strings.Builder
	b.WriteString("Usage: claude [options] [command] [prompt]\n\nOptions:\n")
	for _, f := range requiredFlags {
		if scenario == "oldhelp" && f == "--restricted" {
			continue
		}
		b.WriteString("  " + f + " <value>   description\n")
	}
	b.WriteString("  --model <model>                       Model for the current session. Provide\n" +
		"                                        an alias for the latest model (e.g.\n" +
		"                                        'fable', 'opus', or 'sonnet') or a\n" +
		"                                        model's full name (e.g.\n" +
		"                                        'claude-fable-5').\n")
	return b.String()
}

type fake struct {
	dir   string
	in    *bufio.Reader
	stdin *os.File
	log   *os.File
}

func (f *fake) out(v any) {
	b, _ := json.Marshal(v)
	os.Stdout.Write(append(b, '\n'))
}

func (f *fake) record(name string, data []byte) {
	if f.dir != "" {
		_ = os.WriteFile(filepath.Join(f.dir, name), data, 0o600)
	}
}

// readLine returns the next stdin JSON line (nil at EOF), logging it.
func (f *fake) readLine() map[string]any {
	line, err := f.in.ReadBytes('\n')
	if len(strings.TrimSpace(string(line))) > 0 {
		if f.log != nil {
			f.log.Write(line)
		}
		var m map[string]any
		_ = json.Unmarshal(line, &m)
		return m
	}
	if err != nil {
		return nil
	}
	return f.readLine()
}

// readUser reads the next stdin message, which must be a user message, and
// returns its uuid.
func (f *fake) readUser() string {
	m := f.readLine()
	if m == nil || m["type"] != "user" {
		fmt.Fprintf(os.Stderr, "fake: expected a user message, got %v\n", m)
		os.Exit(90)
	}
	id, _ := m["uuid"].(string)
	return id
}

func (f *fake) drainToEOF() {
	for f.readLine() != nil {
	}
}

func initMsg(session string, extra map[string]any) map[string]any {
	m := map[string]any{
		"type": "system", "subtype": "init", "session_id": session, "uuid": "i1",
		"model": "claude-test", "cwd": "/tmp", "claude_code_version": "2.1.282",
		"tools":        []string{"Read", "Edit", "mcp__yip__room_post", "mcp__yip__permission_prompt"},
		"mcp_servers":  []map[string]any{{"name": "yip", "status": "connected"}},
		"apiKeySource": "none", "permissionMode": "acceptEdits",
		"capabilities": []string{"interrupt_receipt_v1", "interrupt_cancel_queued_v1"},
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func assistantText(session, text string, extra map[string]any) map[string]any {
	m := map[string]any{
		"type": "assistant", "session_id": session, "uuid": "a", "parent_tool_use_id": nil,
		"message": map[string]any{"id": "msg", "role": "assistant", "model": "claude-test",
			"content": []map[string]any{{"type": "text", "text": text}}},
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func toolUse(session, id, name string, input map[string]any) map[string]any {
	return map[string]any{
		"type": "assistant", "session_id": session, "parent_tool_use_id": nil,
		"message": map[string]any{"id": "msg2", "role": "assistant",
			"content": []map[string]any{{"type": "tool_use", "id": id, "name": name, "input": input}}},
	}
}

func toolResult(session, id, content string, isErr bool) map[string]any {
	return map[string]any{
		"type": "user", "session_id": session, "parent_tool_use_id": nil,
		"message": map[string]any{"role": "user",
			"content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": isErr}}},
	}
}

func successResult(session, text string, answered []string, extra map[string]any) map[string]any {
	m := map[string]any{
		"type": "result", "subtype": "success", "session_id": session, "is_error": false,
		"result": text, "num_turns": 1, "duration_ms": 10, "duration_api_ms": 8, "stop_reason": "end_turn",
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5,
			"cache_creation_input_tokens": 0, "cache_read_input_tokens": 100},
		"modelUsage": map[string]any{"claude-test": map[string]any{
			"inputTokens": 10, "outputTokens": 5, "cacheReadInputTokens": 100,
			"cacheCreationInputTokens": 0, "costUSD": 0.01, "webSearchRequests": 0}},
		"total_cost_usd":     0.01,
		"permission_denials": []any{},
		"user_message_uuids": answered,
	}
	if len(answered) > 0 {
		m["user_message_uuid"] = answered[len(answered)-1]
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func runFake(scenario string) int {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("2.1.282 (Claude Code)")
		return 0
	}
	if len(args) == 1 && args[0] == "--help" {
		fmt.Print(fakeHelpText(scenario))
		return 0
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "status" {
		js := os.Getenv(envAuthJSON)
		fmt.Println(js)
		var st struct {
			LoggedIn bool `json:"loggedIn"`
		}
		_ = json.Unmarshal([]byte(js), &st)
		if !st.LoggedIn {
			return 1
		}
		return 0
	}

	f := &fake{dir: os.Getenv(envRecord), in: bufio.NewReader(os.Stdin)}
	argsJSON, _ := json.Marshal(args)
	f.record("args.json", argsJSON)
	f.record("env.txt", []byte(strings.Join(os.Environ(), "\n")))
	wd, _ := os.Getwd()
	f.record("cwd.txt", []byte(wd))
	if f.dir != "" {
		f.log, _ = os.Create(filepath.Join(f.dir, "stdin.jsonl"))
	}
	const sid = "sess-1"

	switch scenario {
	case "basic":
		u1 := f.readUser()
		f.out(initMsg(sid, nil))
		f.out(map[string]any{"type": "stream_event", "session_id": sid, "parent_tool_use_id": nil,
			"event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Hel"}}})
		f.out(map[string]any{"type": "stream_event", "session_id": sid, "parent_tool_use_id": nil,
			"event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "lo"}}})
		f.out(assistantText(sid, "Hello", nil))
		f.out(toolUse(sid, "tu1", "Read", map[string]any{"file_path": "/repo/auth/refresh.go"}))
		f.out(toolResult(sid, "tu1", "package auth", false))
		f.out(toolUse(sid, "tu2", "mcp__yip__room_post", map[string]any{"body": "hi"}))
		f.out(toolResult(sid, "tu2", "posted", false))
		f.out(map[string]any{"type": "permission_denied", "message": "a string where assistant messages have objects"})
		f.out(assistantText(sid, "Done.", nil))
		f.out(successResult(sid, "Done.", []string{u1}, nil))
		f.drainToEOF()
		return 0

	case "resume":
		u1 := f.readUser()
		f.out(initMsg("sess-old", nil))
		f.out(assistantText("sess-old", "Resumed.", nil))
		f.out(successResult("sess-old", "Resumed.", []string{u1}, map[string]any{"total_cost_usd": 3.5}))
		f.drainToEOF()
		return 0

	case "steer":
		u1 := f.readUser()
		f.out(initMsg(sid, nil))
		f.out(toolUse(sid, "tu1", "Bash", map[string]any{"command": "go test ./...", "description": "Running go test"}))
		m := f.readLine() // the steering message
		u2, _ := m["uuid"].(string)
		f.out(toolResult(sid, "tu1", "ok", false))
		f.out(assistantText(sid, "Tests pass.", nil))
		f.out(successResult(sid, "Tests pass.", []string{u1}, nil))
		f.out(assistantText(sid, "Adjusted per your note.", nil))
		f.out(successResult(sid, "Adjusted per your note.", []string{u2}, nil))
		f.drainToEOF()
		return 0

	case "ratelimit", "ratelimit-noreset":
		u1 := f.readUser()
		f.out(initMsg(sid, nil))
		if scenario == "ratelimit" {
			f.out(map[string]any{"type": "rate_limit_event", "session_id": sid, "uuid": "r1",
				"rate_limit_info": map[string]any{"status": "rejected", "resetsAt": time.Now().Add(time.Hour).Unix(), "rateLimitType": "five_hour"}})
		}
		text := "You've hit your session limit · resets 3:45pm"
		f.out(assistantText(sid, text, map[string]any{"error": "rate_limit"}))
		f.out(map[string]any{"type": "result", "subtype": "success", "session_id": sid, "is_error": true,
			"result": text, "api_error_status": 429, "num_turns": 1, "total_cost_usd": 0,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0}, "user_message_uuids": []string{u1}})
		f.drainToEOF()
		return 1

	case "auth":
		u1 := f.readUser()
		f.out(initMsg(sid, nil))
		text := "Not logged in · Please run /login"
		f.out(assistantText(sid, text, map[string]any{"error": "authentication_failed"}))
		f.out(map[string]any{"type": "result", "subtype": "success", "session_id": sid, "is_error": true,
			"result": text, "num_turns": 0, "total_cost_usd": 0, "user_message_uuids": []string{u1}})
		f.drainToEOF()
		return 1

	case "auth-stderr":
		f.readUser()
		fmt.Fprintln(os.Stderr, "Invalid API key · Please run /login")
		return 1

	case "crash":
		f.readUser()
		f.out(initMsg(sid, nil))
		f.out(assistantText(sid, "Starting", nil))
		fmt.Fprintln(os.Stderr, "TypeError: boom\n    at run (cli.js:1:1)")
		return 3

	case "mcpfail":
		u1 := f.readUser()
		f.out(initMsg(sid, map[string]any{
			"mcp_servers": []map[string]any{{"name": "yip", "status": "failed", "error": "spawn ENOENT"}},
		}))
		f.out(assistantText(sid, "No tools.", nil))
		f.out(successResult(sid, "No tools.", []string{u1}, nil))
		f.drainToEOF()
		return 0

	case "burst-exit":
		// Far more output than a pipe buffer holds, then the result, then an
		// immediate exit: the result must not be lost when the process ends.
		u1 := f.readUser()
		f.out(initMsg(sid, nil))
		chunk := strings.Repeat("x", 400)
		for i := 0; i < 600; i++ {
			f.out(map[string]any{"type": "stream_event", "session_id": sid, "parent_tool_use_id": nil,
				"event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": chunk}}})
		}
		f.out(successResult(sid, "burst done", []string{u1}, nil))
		return 0

	case "apikey":
		u1 := f.readUser()
		f.out(initMsg(sid, map[string]any{"apiKeySource": "ANTHROPIC_API_KEY"}))
		f.out(successResult(sid, "ok", []string{u1}, nil))
		f.drainToEOF()
		return 0

	case "cancel-clean":
		u1 := f.readUser()
		f.out(initMsg(sid, nil))
		f.out(toolUse(sid, "tu1", "Bash", map[string]any{"command": "sleep 100"}))
		for {
			m := f.readLine()
			if m == nil {
				return 0
			}
			if m["type"] == "control_request" {
				b, _ := json.Marshal(m)
				f.record("interrupt.json", b)
				f.out(map[string]any{"type": "control_response", "response": map[string]any{
					"subtype": "success", "request_id": m["request_id"], "response": map[string]any{"still_queued": []string{}}}})
				f.out(map[string]any{"type": "result", "subtype": "error_during_execution", "session_id": sid,
					"is_error": true, "errors": []string{"interrupted"}, "terminal_reason": "aborted_streaming",
					"num_turns": 1, "total_cost_usd": 0.002, "user_message_uuids": []string{u1}})
			}
		}

	case "hang":
		// A stuck CLI: ignores SIGTERM and interrupts, and leaves a child
		// process running in its group. Only SIGKILL of the group ends it.
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
		f.readUser()
		child := exec.Command("sleep", "300")
		if err := child.Start(); err == nil {
			f.record("child.pid", []byte(fmt.Sprint(child.Process.Pid)))
		}
		f.out(initMsg(sid, nil))
		f.out(toolUse(sid, "tu1", "Bash", map[string]any{"command": "sleep 300"}))
		for {
			m := f.readLine()
			if m == nil {
				break
			}
			if m["type"] == "control_request" {
				b, _ := json.Marshal(m)
				f.record("interrupt.json", b)
			}
		}
		time.Sleep(time.Hour) // never exits on its own
	}
	fmt.Fprintf(os.Stderr, "fake: unknown scenario %q\n", scenario)
	return 99
}
