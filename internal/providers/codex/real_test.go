package codex

// Real-provider smoke test. It launches the installed `codex app-server`,
// sends a real (tiny) prompt to the user's Codex account, exercises one
// approval (denied), the yip MCP tool path, and a resume. It is skipped
// unless YIP_REAL_PROVIDER_TESTS=1 because it consumes account allowance.
//
//	YIP_REAL_PROVIDER_TESTS=1 go test -run TestRealCodex -v ./internal/providers/codex/

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

// runFakeMCP turns the test binary into a minimal stdio MCP server exposing
// one tool, `ping`, standing in for the yip bridge.
func runFakeMCP() int {
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	out := json.NewEncoder(os.Stdout)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			var m struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"params"`
			}
			if json.Unmarshal(line, &m) == nil && len(m.ID) > 0 {
				var result any
				switch m.Method {
				case "initialize":
					v := m.Params.ProtocolVersion
					if v == "" {
						v = "2025-06-18"
					}
					result = map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{}},
						"serverInfo": map[string]any{"name": "yip-test-bridge", "version": "0.0.1"}}
				case "tools/list":
					result = map[string]any{"tools": []any{map[string]any{
						"name": "ping", "description": "Health check for the yip bridge. Returns pong.",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
					}}}
				case "tools/call":
					result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "pong"}}}
				case "ping":
					result = map[string]any{}
				default:
					out.Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
					continue
				}
				out.Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
			}
		}
		if err != nil {
			return 0
		}
	}
}

func TestRealCodex(t *testing.T) {
	if os.Getenv("YIP_REAL_PROVIDER_TESTS") != "1" {
		t.Skip("set YIP_REAL_PROVIDER_TESTS=1 to run against the installed codex (sends a real prompt)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	a := NewAdapter()

	inst := a.Probe(ctx)
	t.Logf("probe: version=%s tested=%v auth=%s (%s) billing=%s models=%d path=%s",
		inst.Version, inst.Tested, inst.AuthState, inst.AuthDetail, inst.Billing, len(inst.Models), inst.Path)
	if inst.AuthState != protocol.AuthReady {
		t.Fatalf("codex is not ready (%s: %s); sign in with `codex login` first", inst.AuthState, inst.AuthDetail)
	}

	repo := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=yip", "-c", "user.email=yip@example.invalid"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# smoke\n"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "init")

	self, _ := os.Executable()
	spec := providers.StartSpec{
		RunID:        "smoke",
		Workdir:      repo,
		Mode:         protocol.ModeEdit,
		Instructions: "You are a terse test engineer. Follow the user's steps exactly and keep replies to one line.",
		Prompt: "Do these steps in order: " +
			"1) Call the `ping` tool from the `yip` MCP server. " +
			"2) Create hello.txt in the current directory containing exactly `hi`. " +
			"3) Run `curl -sI https://example.com` with escalated permissions (require_escalated), since the sandbox blocks network. " +
			"If that is declined, do not retry. Then reply with the single word: finished.",
		MCP: providers.MCPServer{Name: "yip", Command: self, Env: map[string]string{"YIP_CODEX_FAKE_MCP": "1"}},
		Env: providers.BaseEnv(envKeys),
	}
	s, err := a.Start(ctx, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var approvals, tools int
	for ev := range s.Events() {
		t.Logf("event %-16s %s %s", ev.Kind, ev.Tool, truncate(ev.Text, 200))
		switch ev.Kind {
		case providers.EventApprovalRequest:
			approvals++
			t.Logf("approval %+v", ev.Approval.Action)
			if err := s.ResolveApproval(ctx, ev.Approval.ID, providers.ApprovalDecision{Allow: false, Reason: "smoke test denies network"}); err != nil {
				t.Errorf("ResolveApproval: %v", err)
			}
		case providers.EventQuestion:
			s.AnswerQuestion(ctx, ev.Question.ID, providers.QuestionAnswer{Declined: true, Selected: -1})
		case providers.EventToolStarted:
			if strings.HasPrefix(ev.Tool, "yip.") {
				tools++
			}
		}
	}
	res := s.Wait()
	t.Logf("result: %+v", res)
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome %s: %s", res.Outcome, res.Error)
	}
	if !res.ExitConfirmed || res.VendorSessionID == "" || res.Usage == nil {
		t.Errorf("result incomplete: %+v", res)
	}
	if b, err := os.ReadFile(filepath.Join(repo, "hello.txt")); err != nil || strings.TrimSpace(string(b)) != "hi" {
		t.Errorf("hello.txt: %q %v", b, err)
	}
	if approvals == 0 {
		t.Errorf("expected at least one approval request")
	}
	if tools == 0 {
		t.Errorf("expected a yip MCP tool call")
	}

	// Resume the same thread.
	spec.ResumeSessionID = res.VendorSessionID
	spec.Prompt = "Reply with the single word: resumed."
	s, err = a.Start(ctx, spec)
	if err != nil {
		t.Fatalf("resume Start: %v", err)
	}
	for ev := range s.Events() {
		t.Logf("event %-16s %s", ev.Kind, truncate(ev.Text, 200))
		if ev.Kind == providers.EventApprovalRequest {
			s.ResolveApproval(ctx, ev.Approval.ID, providers.ApprovalDecision{Allow: false})
		}
	}
	res2 := s.Wait()
	t.Logf("resume result: %+v", res2)
	if res2.Outcome != protocol.OutcomeSucceeded || res2.VendorSessionID != res.VendorSessionID {
		t.Fatalf("resume: %+v", res2)
	}
}
