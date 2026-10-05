package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/internal/skilltest"
	"github.com/binbandit/yip/protocol"
)

func TestMain(m *testing.M) {
	if os.Getenv("YIP_TEST_OPENCODE") == "1" {
		os.Exit(fakeCLI())
	}
	os.Exit(m.Run())
}

func fakeCLI() int {
	scenario := os.Getenv("YIP_TEST_SCENARIO")
	switch strings.Join(os.Args[1:], " ") {
	case "--version":
		if scenario == "old" {
			fmt.Println("1.0.0")
		} else {
			fmt.Println("1.18.33")
		}
		return 0
	case "auth list":
		switch scenario {
		case "signedout":
			fmt.Println("└  0 credentials")
		case "api":
			fmt.Println("│  ● Example api\n└  1 credentials")
		case "mixed":
			fmt.Println("│  ● Example oauth\n│  ● Example API api\n└  2 credentials")
		case "remote":
			fmt.Println("│  ● https://example.test wellknown\n└  1 credentials")
		case "bad-auth":
			fmt.Println("unrecognized output")
		default:
			fmt.Println("│  ● Example oauth\n└  1 credentials")
		}
		return 0
	case "console orgs":
		if scenario == "org" {
			fmt.Println("● Organization")
		} else {
			fmt.Println("No accounts found")
		}
		return 0
	}
	if len(os.Args) < 2 || os.Args[1] != "acp" {
		return 2
	}
	record, _ := os.Create(os.Getenv("YIP_TEST_RECORD"))
	defer record.Close()
	config := os.Getenv("OPENCODE_CONFIG_CONTENT")
	fmt.Fprintln(record, config)
	if os.Getenv("OPENCODE_DISABLE_PROJECT_CONFIG") != "true" ||
		os.Getenv("UNLISTED_API_KEY") != "" || os.Getenv("XDG_CONFIG_HOME") == os.Getenv("HOME") {
		return 3
	}
	scanner := bufio.NewScanner(os.Stdin)
	send := func(v any) { raw, _ := json.Marshal(v); fmt.Println(string(raw)) }
	reply := func(id json.RawMessage, v any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": v})
	}
	update := func(v any) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "oc-1", "update": v}})
	}
	var promptID json.RawMessage
	mode, model := "yip", "example/model"
	options := func() []map[string]any {
		return []map[string]any{
			{"id": "mode", "category": "mode", "currentValue": mode, "options": []map[string]string{{"value": "yip", "name": "yip"}}},
			{"id": "model", "category": "model", "currentValue": model, "options": []map[string]string{{"value": "example/model", "name": "Example"}}},
		}
	}
	for scanner.Scan() {
		fmt.Fprintln(record, scanner.Text())
		var msg acp.Message
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			return 4
		}
		switch msg.Method {
		case acp.MethodInitialize:
			var init acp.InitializeParams
			_ = json.Unmarshal(msg.Params, &init)
			if init.ClientCapabilities.Terminal || init.ClientCapabilities.FS.WriteTextFile ||
				init.ClientInfo == nil || init.ClientInfo.Version == "" {
				return 5
			}
			reply(msg.ID, acp.InitializeResult{ProtocolVersion: 1})
		case acp.MethodSessionNew:
			var params acp.NewSessionParams
			_ = json.Unmarshal(msg.Params, &params)
			if len(params.MCPServers) != 1 || params.MCPServers[0].Name != "yip" || (scenario != "skills" && len(params.MCPServers[0].Env) != 1) {
				return 6
			}
			if scenario == "skills" {
				server := params.MCPServers[0]
				env := map[string]string{}
				for _, kv := range server.Env {
					env[kv.Name] = kv.Value
				}
				if err := skilltest.Verify(providers.MCPServer{Command: server.Command, Args: server.Args, Env: env}); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 10
				}
			}
			reply(msg.ID, map[string]any{"sessionId": "oc-1", "configOptions": options()})
		case acp.MethodSessionSetConfig:
			var params acp.SetConfigOptionParams
			_ = json.Unmarshal(msg.Params, &params)
			if params.ConfigID == "mode" {
				mode = params.Value
				if scenario == "bad-mode" {
					mode = "build"
				}
			} else {
				model = params.Value
			}
			reply(msg.ID, map[string]any{"configOptions": options()})
			if scenario == "no-read" && params.ConfigID == "model" {
				time.Sleep(time.Hour)
			}
		case acp.MethodSessionPrompt:
			promptID = msg.ID
			if scenario == "skills" {
				update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": acp.TextBlock("skills verified")})
				reply(msg.ID, acp.PromptResult{StopReason: acp.StopEndTurn})
				continue
			}
			if scenario == "disconnect" {
				return 7
			}
			if scenario == "auth-error" {
				send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": acp.CodeAuthRequired, "message": "Sign in required"}})
				continue
			}
			update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": acp.TextBlock("Hello ")})
			if scenario == "cancel" {
				continue
			}
			if scenario == "mode-change" {
				update(map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "build"})
				continue
			}
			opts := []acp.PermissionOption{{OptionID: "once", Kind: acp.AllowOnce}, {OptionID: "always", Kind: acp.AllowAlways}, {OptionID: "reject", Kind: acp.RejectOnce}}
			if scenario == "persistent" {
				opts = opts[1:]
			}
			tool := acp.ToolCall{
				ToolCallID: "tool-1", Kind: acp.KindEdit, Title: "Edit file", RawInput: json.RawMessage(`{"filePath":"main.go"}`),
			}
			if scenario == "shell" {
				tool.Kind, tool.Name = acp.KindExecute, "bash"
			}
			send(map[string]any{"jsonrpc": "2.0", "id": "permission", "method": acp.MethodRequestPermission,
				"params": acp.RequestPermissionParams{SessionID: "oc-1", ToolCall: tool, Options: opts}})
		case acp.MethodSessionCancel:
			reply(promptID, acp.PromptResult{StopReason: acp.StopCancelled})
		case "":
			var result acp.RequestPermissionResult
			_ = json.Unmarshal(msg.Result, &result)
			if result.Outcome.OptionID == "always" {
				return 8
			}
			if scenario == "shell" && result.Outcome.Outcome != "cancelled" {
				return 9
			}
			update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "tool-1", "title": "Run test", "status": "pending"})
			update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "tool-1", "status": "completed"})
			update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": acp.TextBlock("world")})
			update(map[string]any{"sessionUpdate": "usage_update", "cost": map[string]any{"amount": 0.12, "currency": "USD"}})
			in, out := int64(12), int64(7)
			reply(promptID, acp.PromptResult{StopReason: acp.StopEndTurn, Usage: &acp.TurnUsage{InputTokens: &in, OutputTokens: &out}})
		}
	}
	return 0
}

func fixture(t *testing.T, scenario, mode string) (providers.Adapter, providers.StartSpec, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home, work := t.TempDir(), t.TempDir()
	record := filepath.Join(t.TempDir(), "protocol.jsonl")
	env := []string{
		"HOME=" + home, "PATH=" + os.Getenv("PATH"), "YIP_TEST_OPENCODE=1",
		"YIP_TEST_SCENARIO=" + scenario, "YIP_TEST_RECORD=" + record,
	}
	return New(WithExecutable(exe), WithProbeEnv(env)), providers.StartSpec{
		Workdir: work, Mode: mode, Model: "example/model", Env: env, Timeout: 5 * time.Second,
		Instructions: "Do the work", Prompt: "/not-a-command",
		MCP: providers.MCPServer{Name: "yip", Command: "yip", Args: []string{"bridge"}, Env: map[string]string{"YIP_RUN_ID": "run"}},
	}, record
}

func TestProbeSignInAndCapabilities(t *testing.T) {
	for _, tc := range []struct{ scenario, auth, billing string }{
		{"normal", protocol.AuthReady, protocol.BillingSubscription},
		{"api", protocol.AuthReady, protocol.BillingAPI},
		{"mixed", protocol.AuthReady, protocol.BillingAPI},
		{"signedout", protocol.AuthNeedsSignIn, protocol.BillingUnknown},
		{"bad-auth", protocol.AuthUnknown, protocol.BillingUnknown},
		{"remote", protocol.AuthUnknown, protocol.BillingUnknown},
		{"org", protocol.AuthUnknown, protocol.BillingUnknown},
		{"old", protocol.AuthUnknown, protocol.BillingUnknown},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			a, _, _ := fixture(t, tc.scenario, protocol.ModeEdit)
			p := a.Probe(context.Background())
			if p.AuthState != tc.auth || p.Billing != tc.billing {
				t.Fatalf("probe = %+v", p)
			}
			if p.Tested || p.Capabilities.ActiveSteering || p.Capabilities.SessionResume || p.Capabilities.Sandbox || p.Capabilities.UserQuestions {
				t.Fatalf("overstated capabilities: %+v", p)
			}
			if tc.auth == protocol.AuthReady && (!p.Capabilities.ReadOnly || !p.Capabilities.ToolApprovals || !p.Capabilities.MCPTools) {
				t.Fatalf("missing capabilities: %+v", p)
			}
		})
	}
}

func TestACPWorkflow(t *testing.T) {
	t.Setenv("UNLISTED_API_KEY", "must-not-be-inherited")
	for _, mode := range []string{protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation} {
		t.Run(mode, func(t *testing.T) {
			a, spec, record := fixture(t, "normal", mode)
			s, err := a.Start(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for event := range s.Events() {
				counts[event.Kind]++
				if event.Approval != nil {
					if event.Approval.Action.Kind != "edit" || event.Approval.Action.Target != "main.go" {
						t.Fatal(event.Approval)
					}
					if err := s.ResolveApproval(context.Background(), event.Approval.ID, providers.ApprovalDecision{Allow: true}); err != nil {
						t.Fatal(err)
					}
				}
			}
			result := s.Wait()
			if result.Outcome != protocol.OutcomeSucceeded || result.FinalText != "Hello world" || !result.ExitConfirmed {
				t.Fatalf("result = %+v", result)
			}
			if result.Usage == nil || *result.Usage.InputTokens != 12 || *result.Usage.OutputTokens != 7 || *result.Usage.CostUSD != .12 {
				t.Fatalf("usage = %+v", result.Usage)
			}
			if counts[providers.EventToolStarted] != 1 || counts[providers.EventToolFinished] != 1 || counts[providers.EventMessageDelta] != 2 {
				t.Fatalf("events = %v", counts)
			}
			if mode != protocol.ModeEdit && counts[providers.EventApprovalRequest] != 0 {
				t.Fatal("read-only permission reached runner")
			}
			if _, err := s.SendInput(context.Background(), "steer"); !errors.Is(err, providers.ErrUnsupported) {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(raw), "\n")
			var config struct {
				Permission map[string]string
				LSP        json.RawMessage
				Formatter  json.RawMessage
				Snapshot   json.RawMessage
			}
			if err := json.Unmarshal([]byte(lines[0]), &config); err != nil {
				t.Fatal(err)
			}
			if string(config.LSP) != "false" || string(config.Formatter) != "false" || string(config.Snapshot) != "false" {
				t.Fatal("background executors must be disabled")
			}
			if config.Permission["*"] != "deny" || config.Permission["read"] != "allow" {
				t.Fatal(config)
			}
			for _, name := range bridge.NamesForMode(mode) {
				if config.Permission["yip_"+name] != "allow" {
					t.Fatalf("missing mode-scoped bridge tool %s", name)
				}
			}
			if mode == protocol.ModeEdit && config.Permission["edit"] != "ask" {
				t.Fatal(config)
			}
			for _, name := range []string{"bash", "shell", "execute", "batch", "task"} {
				if config.Permission[name] != "deny" {
					t.Fatalf("unmanaged execution %s is not denied", name)
				}
			}
			if !strings.Contains(string(raw), "Engineer instructions:") || !strings.Contains(string(raw), `"value":"example/model"`) {
				t.Fatal("missing prompt prefix/model selection")
			}
		})
	}
}

func TestUnsafeSetupRejected(t *testing.T) {
	for _, scenario := range []string{"old", "remote", "org", "bad-auth", "bad-mode", "signedout"} {
		t.Run(scenario, func(t *testing.T) {
			a, spec, _ := fixture(t, scenario, protocol.ModeEdit)
			if s, err := a.Start(context.Background(), spec); err == nil {
				_ = s.Cancel(context.Background())
				t.Fatal("unsafe setup was accepted")
			}
		})
	}
	a, spec, _ := fixture(t, "normal", protocol.ModeEdit)
	spec.ResumeSessionID = "previous"
	if _, err := a.Start(context.Background(), spec); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatal(err)
	}
	spec.ResumeSessionID = ""
	spec.Env = append(spec.Env, "OPENCODE_CONFIG_CONTENT={}")
	if _, err := a.Start(context.Background(), spec); err == nil {
		t.Fatal("injected config accepted")
	}
	a, spec, _ = fixture(t, "normal", protocol.ModeEdit)
	if err := os.Mkdir(filepath.Join(envValue(spec.Env, "HOME"), ".opencode"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envValue(spec.Env, "HOME"), ".opencode", "opencode.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(context.Background(), spec); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestOfficialInstallDirectoryAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkHomeExtensions(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"@opencode-ai/plugin":"1.18.33"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkHomeExtensions(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"postinstall":"unsafe"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkHomeExtensions(dir); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestCancellationAndFailures(t *testing.T) {
	for _, scenario := range []string{"cancel", "mode-change", "disconnect", "persistent", "auth-error"} {
		t.Run(scenario, func(t *testing.T) {
			a, spec, _ := fixture(t, scenario, protocol.ModeEdit)
			s, err := a.Start(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			for event := range s.Events() {
				if scenario == "cancel" && event.Kind == providers.EventMessageDelta {
					if err := s.Cancel(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				if event.Approval != nil {
					err := s.ResolveApproval(context.Background(), event.Approval.ID, providers.ApprovalDecision{Allow: true})
					if !errors.Is(err, providers.ErrUnsupported) {
						t.Fatalf("persistent permission result: %v", err)
					}
				}
			}
			r := s.Wait()
			if !r.ExitConfirmed {
				t.Fatalf("unconfirmed exit: %+v", r)
			}
			if scenario == "cancel" && r.Outcome != protocol.OutcomeCancelled {
				t.Fatal(r)
			}
			if scenario == "auth-error" && r.Outcome != protocol.OutcomeAuthRequired {
				t.Fatal(r)
			}
			if (scenario == "mode-change" || scenario == "disconnect") && r.Outcome != protocol.OutcomeFailed {
				t.Fatal(r)
			}
		})
	}
}

func TestBundledSkillsThroughSession(t *testing.T) {
	for _, mode := range []string{"edit", "readonly", "conversation"} {
		t.Run(mode, func(t *testing.T) {
			a, spec, _ := fixture(t, "skills", mode)
			spec.MCP = skilltest.New(t, mode)
			s, err := a.Start(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			for range s.Events() {
			}
			result := s.Wait()
			if result.Outcome != protocol.OutcomeSucceeded || result.FinalText != "skills verified" {
				t.Fatalf("%+v", result)
			}
		})
	}
}
