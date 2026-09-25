package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// envStubMCPLog is where the stub MCP server appends every tools/call it
// receives (one JSON object per line).
const envStubMCPLog = "YIP_CLAUDE_STUB_MCP_LOG"

// runStubMCP is a minimal stdio MCP server standing in for `yip bridge`. It
// exposes room_post and permission_prompt. The permission tool allows Bash
// commands that mention "yip-smoke" and denies everything else, answering in
// the format Claude Code expects: a single text content block whose text is
// JSON {"behavior":"allow","updatedInput":{...}} or
// {"behavior":"deny","message":"..."}.
func runStubMCP() int {
	logPath := os.Getenv(envStubMCPLog)
	in := bufio.NewReader(os.Stdin)
	write := func(v any) {
		b, _ := json.Marshal(v)
		os.Stdout.Write(append(b, '\n'))
	}
	logCall := func(v any) {
		if logPath == "" {
			return
		}
		fh, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		b, _ := json.Marshal(v)
		fh.Write(append(b, '\n'))
		fh.Close()
	}
	text := func(s string) map[string]any {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": s}}}
	}
	for {
		line, err := in.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(line, &req) == nil && len(req.ID) > 0 {
				var result any
				switch req.Method {
				case "initialize":
					var p struct {
						ProtocolVersion string `json:"protocolVersion"`
					}
					_ = json.Unmarshal(req.Params, &p)
					result = map[string]any{
						"protocolVersion": p.ProtocolVersion,
						"capabilities":    map[string]any{"tools": map[string]any{}},
						"serverInfo":      map[string]any{"name": "yip-stub", "version": "0.0.0"},
					}
				case "tools/list":
					result = map[string]any{"tools": []map[string]any{
						{
							"name":        "room_post",
							"description": "Post a message to the current yip room.",
							"inputSchema": map[string]any{"type": "object",
								"properties": map[string]any{"body": map[string]any{"type": "string"}},
								"required":   []string{"body"}},
						},
						{
							"name":        "permission_prompt",
							"description": "Answer a Claude Code permission prompt.",
							"inputSchema": map[string]any{"type": "object",
								"properties": map[string]any{
									"tool_name":   map[string]any{"type": "string"},
									"input":       map[string]any{"type": "object"},
									"tool_use_id": map[string]any{"type": "string"},
								},
								"required": []string{"tool_name", "input"}},
						},
					}}
				case "tools/call":
					var p struct {
						Name      string         `json:"name"`
						Arguments map[string]any `json:"arguments"`
					}
					_ = json.Unmarshal(req.Params, &p)
					logCall(map[string]any{"name": p.Name, "arguments": p.Arguments})
					switch p.Name {
					case "room_post":
						result = text(`{"ok":true,"messageId":"m-smoke"}`)
					case "permission_prompt":
						input, _ := p.Arguments["input"].(map[string]any)
						cmd, _ := input["command"].(string)
						var decision map[string]any
						if p.Arguments["tool_name"] == "Bash" && strings.Contains(cmd, "yip-smoke") {
							decision = map[string]any{"behavior": "allow", "updatedInput": input}
						} else {
							decision = map[string]any{"behavior": "deny", "message": "Denied by the yip smoke-test stub."}
						}
						b, _ := json.Marshal(decision)
						result = text(string(b))
					default:
						result = map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "unknown tool"}}}
					}
				case "ping":
					result = map[string]any{}
				default:
					write(map[string]any{"jsonrpc": "2.0", "id": req.ID,
						"error": map[string]any{"code": -32601, "message": "method not found"}})
					continue
				}
				write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
			}
		}
		if err != nil {
			return 0
		}
	}
}

// TestRealClaudeSmoke drives the real Claude Code CLI end to end. It sends a
// real (tiny) prompt to the user's signed-in account, so it only runs with
// YIP_REAL_PROVIDER_TESTS=1.
func TestRealClaudeSmoke(t *testing.T) {
	if os.Getenv("YIP_REAL_PROVIDER_TESTS") != "1" {
		t.Skip("set YIP_REAL_PROVIDER_TESTS=1 to run against the real Claude Code CLI")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=yip", "GIT_AUTHOR_EMAIL=yip@example.invalid",
			"GIT_COMMITTER_NAME=yip", "GIT_COMMITTER_EMAIL=yip@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("smoke\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(t.TempDir(), "calls.jsonl")
	a := NewAdapter(Options{})
	inst := a.Probe(context.Background())
	t.Logf("probe: version=%s tested=%v auth=%s billing=%s detail=%q", inst.Version, inst.Tested, inst.AuthState, inst.Billing, inst.AuthDetail)
	if inst.AuthState == protocol.AuthNotInstalled {
		t.Skip("claude is not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sess, err := a.Start(ctx, providers.StartSpec{
		RunID:        "smoke",
		Workdir:      repo,
		Mode:         protocol.ModeEdit,
		Instructions: "You are a yip smoke test. Follow the user's steps exactly and keep replies short.",
		Prompt: "Do exactly these steps: 1) Call the yip room_post tool with body \"smoke hello\". " +
			"2) Use the Bash tool to run exactly: git commit --allow-empty -m yip-smoke " +
			"3) Reply with the single word DONE.",
		MCP: providers.MCPServer{
			Name:    "yip",
			Command: self,
			Args:    []string{"-test.run=^$"},
			Env:     map[string]string{envStubMCP: "1", envStubMCPLog: callLog},
		},
		PermissionTool: "permission_prompt",
		Timeout:        4 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range sess.Events() {
		t.Logf("event %-16s tool=%-24s %s", ev.Kind, ev.Tool, clip(ev.Text, 160))
	}
	res := sess.Wait()
	t.Logf("result: %+v", res)
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Error)
	}
	if res.VendorSessionID == "" || !res.ExitConfirmed {
		t.Fatalf("session id %q exitConfirmed %v", res.VendorSessionID, res.ExitConfirmed)
	}
	calls, _ := os.ReadFile(callLog)
	t.Logf("bridge calls:\n%s", calls)
	if !strings.Contains(string(calls), `"name":"room_post"`) {
		t.Errorf("room_post was not called")
	}
	if !strings.Contains(string(calls), `"name":"permission_prompt"`) || !strings.Contains(string(calls), `"tool_name":"Bash"`) {
		t.Errorf("no Bash permission prompt reached the bridge")
	}
	if log := git("log", "--oneline"); !strings.Contains(log, "yip-smoke") {
		t.Errorf("allowed command did not run; git log:\n%s", log)
	}
}
