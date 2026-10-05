package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/skilltest"
	"github.com/binbandit/yip/protocol"
)

var forbiddenArgs = []string{
	"--dangerously-skip-permissions",
	"--allow-dangerously-skip-permissions",
	"bypassPermissions",
	"--permission-prompts=none",
}

func testSpec(t *testing.T, mode string) providers.StartSpec {
	t.Helper()
	return providers.StartSpec{
		RunID:          "run-1",
		Workdir:        t.TempDir(),
		Mode:           mode,
		Instructions:   "You are Ada, a backend engineer.",
		Prompt:         "Fix the refresh bug.",
		MCP:            providers.MCPServer{Name: "yip", Command: "/usr/local/bin/yip", Args: []string{"bridge", "--run", "run-1"}, Env: map[string]string{"YIP_BRIDGE_TOKEN": "secret-token"}},
		PermissionTool: "permission_prompt",
	}
}

func argValue(args []string, flag string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, flag+"=") {
			return strings.TrimPrefix(a, flag+"="), true
		}
	}
	return "", false
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---- argument construction ----

func TestLaunchArgsAlwaysBounded(t *testing.T) {
	for _, mode := range []string{protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation} {
		t.Run(mode, func(t *testing.T) {
			plan, err := buildLaunch(testSpec(t, mode), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(plan.TempDir)
			args := plan.Args
			for _, want := range []string{"-p", "--verbose", "--include-partial-messages", "--restricted",
				"--strict-mcp-config", "--disable-slash-commands"} {
				if !hasArg(args, want) {
					t.Errorf("missing %s in %v", want, args)
				}
			}
			checks := map[string]string{
				"--output-format":          "stream-json",
				"--input-format":           "stream-json",
				"--permission-prompt-tool": "mcp__yip__permission_prompt",
				"--permission-prompts":     "host",
				"--settings":               `{"disableAllHooks":true}`,
				"--mcp-config":             plan.MCPPath,
				"--append-system-prompt":   "You are Ada, a backend engineer.",
			}
			for flag, want := range checks {
				if got, ok := argValue(args, flag); !ok || got != want {
					t.Errorf("%s = %q (present %v), want %q", flag, got, ok, want)
				}
			}
			joined := strings.Join(args, " ")
			for _, bad := range forbiddenArgs {
				if strings.Contains(joined, bad) {
					t.Errorf("forbidden %q in args", bad)
				}
			}
			if strings.Contains(joined, "secret-token") {
				t.Errorf("bridge secret leaked onto argv")
			}
			if strings.Contains(joined, "Fix the refresh bug") {
				t.Errorf("prompt must travel over stdin, not argv")
			}
			for _, a := range args {
				if !strings.HasPrefix(a, "-") {
					t.Errorf("unexpected positional argument %q", a)
				}
			}
			if hasArg(args, "--resume") || hasArg(args, "--model") {
				t.Errorf("unexpected --resume/--model: %v", args)
			}

			// The MCP config is private and defines only the yip server.
			fi, err := os.Stat(plan.MCPPath)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("mcp config perms %v", fi.Mode().Perm())
			}
			raw, _ := os.ReadFile(plan.MCPPath)
			var cfg mcpConfigFile
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			if len(cfg.MCPServers) != 1 || cfg.MCPServers["yip"].Command != "/usr/local/bin/yip" ||
				cfg.MCPServers["yip"].Type != "stdio" || cfg.MCPServers["yip"].Env["YIP_BRIDGE_TOKEN"] != "secret-token" ||
				strings.Join(cfg.MCPServers["yip"].Args, " ") != "bridge --run run-1" {
				t.Errorf("mcp config = %s", raw)
			}
		})
	}
}

func TestLaunchArgsEditMode(t *testing.T) {
	plan, err := buildLaunch(testSpec(t, protocol.ModeEdit), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(plan.TempDir)
	if v, _ := argValue(plan.Args, "--permission-mode"); v != "acceptEdits" {
		t.Errorf("permission mode %q", v)
	}
	tools, _ := argValue(plan.Args, "--tools")
	for _, want := range []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep"} {
		if !contains(splitList(tools), want) {
			t.Errorf("edit tools %q missing %s", tools, want)
		}
	}
	if v, _ := argValue(plan.Args, "--allowedTools"); v != "mcp__yip" {
		t.Errorf("allowedTools %q: only yip tools may be pre-approved", v)
	}
	if hasArg(plan.Args, "--disallowedTools") {
		t.Errorf("edit mode should not deny tools by name")
	}
}

func TestLaunchArgsReadOnlyNeverAllowsMutation(t *testing.T) {
	for _, mode := range []string{protocol.ModeReadOnly, protocol.ModeConversation} {
		t.Run(mode, func(t *testing.T) {
			plan, err := buildLaunch(testSpec(t, mode), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(plan.TempDir)
			if v, _ := argValue(plan.Args, "--permission-mode"); v != "dontAsk" {
				t.Errorf("permission mode %q", v)
			}
			tools, _ := argValue(plan.Args, "--tools")
			if got := splitList(tools); strings.Join(got, ",") != "Read,Glob,Grep" {
				t.Errorf("readonly tools = %v", got)
			}
			allowed, _ := argValue(plan.Args, "--allowedTools")
			denied, _ := argValue(plan.Args, "--disallowedTools")
			for _, mut := range []string{"Edit", "Write", "NotebookEdit", "Bash"} {
				if contains(splitList(tools), mut) || contains(splitList(allowed), mut) {
					t.Errorf("%s is available in %s mode", mut, mode)
				}
				if !contains(splitList(denied), mut) {
					t.Errorf("%s not denied in %s mode (denied=%q)", mut, mode, denied)
				}
			}
			if allowed != "mcp__yip" {
				t.Errorf("allowedTools %q", allowed)
			}
		})
	}
}

func TestLaunchArgsOptionalFlags(t *testing.T) {
	spec := testSpec(t, protocol.ModeEdit)
	spec.Model = "opus"
	spec.ResumeSessionID = "0b6a3c3e-1111-4222-8333-944455556666"
	spec.Instructions = "--looks-like-a-flag"
	plan, err := buildLaunch(spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(plan.TempDir)
	if v, _ := argValue(plan.Args, "--model"); v != "opus" {
		t.Errorf("model %q", v)
	}
	if v, _ := argValue(plan.Args, "--resume"); v != spec.ResumeSessionID {
		t.Errorf("resume %q", v)
	}
	if v, _ := argValue(plan.Args, "--append-system-prompt"); v != "--looks-like-a-flag" {
		t.Errorf("instructions %q", v)
	}
	if !plan.Resumed {
		t.Errorf("plan not marked resumed")
	}
}

func TestLaunchRejectsBadSpecs(t *testing.T) {
	cases := map[string]func(*providers.StartSpec){
		"mode":            func(s *providers.StartSpec) { s.Mode = "yolo" },
		"permission tool": func(s *providers.StartSpec) { s.PermissionTool = "" },
		"tool name":       func(s *providers.StartSpec) { s.PermissionTool = "a b" },
		"workdir":         func(s *providers.StartSpec) { s.Workdir = "relative/dir" },
		"bridge":          func(s *providers.StartSpec) { s.MCP.Command = "" },
		"prompt":          func(s *providers.StartSpec) { s.Prompt = " " },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			spec := testSpec(t, protocol.ModeEdit)
			mut(&spec)
			if plan, err := buildLaunch(spec, t.TempDir()); err == nil {
				os.RemoveAll(plan.TempDir)
				t.Fatalf("expected error")
			}
		})
	}
	spec := testSpec(t, "yolo")
	if _, err := buildLaunch(spec, t.TempDir()); !errors.Is(err, providers.ErrUnsupported) {
		t.Errorf("unknown mode should wrap ErrUnsupported, got %v", err)
	}
}

// ---- sessions against the fake CLI ----

type fakeRun struct {
	dir     string
	adapter *Adapter
	sess    providers.Session
	spec    providers.StartSpec
	mu      sync.Mutex
	events  []providers.Event
	result  providers.Result
}

func newFakeAdapter(t *testing.T, scenario string, env ...string) (*Adapter, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	base := append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		envFake + "=" + scenario,
		envRecord + "=" + dir,
	}, env...)
	a := NewAdapter(Options{Executable: exe, Env: base, Grace: 500 * time.Millisecond,
		InterruptWait: 500 * time.Millisecond, SkipAuthStatus: true})
	a.exitWait = 300 * time.Millisecond
	return a, dir
}

func startFake(t *testing.T, scenario string, mut func(*providers.StartSpec), env ...string) *fakeRun {
	t.Helper()
	a, dir := newFakeAdapter(t, scenario, env...)
	spec := testSpec(t, protocol.ModeEdit)
	spec.Env = a.opts.Env
	if mut != nil {
		mut(&spec)
	}
	sess, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeRun{dir: dir, adapter: a, sess: sess, spec: spec}
}

// collect drains events (calling on for each) and waits for the result.
func (r *fakeRun) collect(t *testing.T, on func(providers.Event)) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range r.sess.Events() {
			r.mu.Lock()
			r.events = append(r.events, ev)
			r.mu.Unlock()
			if on != nil {
				on(ev)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		r.mu.Lock()
		defer r.mu.Unlock()
		t.Fatalf("session did not end; %d events so far", len(r.events))
	}
	r.result = r.sess.Wait()
}

func (r *fakeRun) kinds(kind string) []providers.Event {
	var out []providers.Event
	for _, e := range r.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func (r *fakeRun) file(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func (r *fakeRun) stdinLines(t *testing.T) []map[string]any {
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(r.file(t, "stdin.jsonl")), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func TestSessionStreamsEvents(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-host-should-not-leak")
	r := startFake(t, "basic", nil)
	r.collect(t, nil)

	res := r.result
	if res.Outcome != protocol.OutcomeSucceeded || res.FinalText != "Done." || res.VendorSessionID != "sess-1" || !res.ExitConfirmed {
		t.Fatalf("result = %+v", res)
	}
	if vs := r.kinds(providers.EventVendorSession); len(vs) != 1 || vs[0].VendorSessionID != "sess-1" {
		t.Errorf("vendor session events %+v", vs)
	}
	var deltas string
	for _, e := range r.kinds(providers.EventMessageDelta) {
		deltas += e.Text
	}
	if deltas != "Hello" {
		t.Errorf("deltas %q", deltas)
	}
	msgs := r.kinds(providers.EventMessage)
	if len(msgs) != 2 || msgs[0].Text != "Hello" || msgs[1].Text != "Done." {
		t.Errorf("messages %+v", msgs)
	}
	started := r.kinds(providers.EventToolStarted)
	if len(started) != 2 || started[0].Tool != "Read" || started[0].Text != "Reading refresh.go" ||
		started[1].Text != "Using yip room post" {
		t.Errorf("tool started %+v", started)
	}
	finished := r.kinds(providers.EventToolFinished)
	if len(finished) != 2 || finished[0].Tool != "Read" || !strings.Contains(string(finished[0].Data), "package auth") {
		t.Errorf("tool finished %+v", finished)
	}
	usage := r.kinds(providers.EventUsage)
	if len(usage) != 1 {
		t.Fatalf("usage events %+v", usage)
	}
	u := usage[0].Usage
	if u.Source != "vendor" || u.InputTokens == nil || *u.InputTokens != 110 || *u.OutputTokens != 5 ||
		u.CostUSD == nil || *u.CostUSD != 0.01 || u.Billing != protocol.BillingUnknown {
		t.Errorf("usage %+v", u)
	}
	if res.Usage == nil || *res.Usage.InputTokens != 110 {
		t.Errorf("result usage %+v", res.Usage)
	}
	if len(r.kinds(providers.EventWarning)) != 0 || len(r.kinds(providers.EventError)) != 0 {
		t.Errorf("unexpected warnings/errors: %+v", r.events)
	}

	// The prompt went over stdin as a stream-json user message with a uuid,
	// and stdin was closed after the answering result (the fake exited).
	lines := r.stdinLines(t)
	if len(lines) != 1 || lines[0]["type"] != "user" || lines[0]["uuid"] == "" {
		t.Fatalf("stdin %+v", lines)
	}
	msg := lines[0]["message"].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "Fix the refresh bug." {
		t.Errorf("stdin message %+v", msg)
	}

	// Launch environment: allowlist plus fixed controls; nothing from the
	// runner's own environment (ANTHROPIC_API_KEY) leaks in.
	env := r.file(t, "env.txt")
	if strings.Contains(env, "sk-host-should-not-leak") {
		t.Errorf("host ANTHROPIC_API_KEY leaked into the provider env")
	}
	for _, want := range []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1",
		"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT=86400000"} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %s", want)
		}
	}
	var args []string
	_ = json.Unmarshal([]byte(r.file(t, "args.json")), &args)
	if !hasArg(args, "--strict-mcp-config") || !hasArg(args, "--permission-prompt-tool") || !hasArg(args, "--restricted") {
		t.Errorf("args %v", args)
	}
	wantDir, _ := filepath.EvalSymlinks(r.spec.Workdir)
	if cwd, _ := filepath.EvalSymlinks(r.file(t, "cwd.txt")); cwd != wantDir {
		t.Errorf("claude ran in %q, want the worktree %q", cwd, wantDir)
	}
	// The per-run MCP config is removed after the session.
	if mcp, _ := argValue(args, "--mcp-config"); mcp == "" {
		t.Errorf("no mcp config")
	} else if _, err := os.Stat(mcp); !os.IsNotExist(err) {
		t.Errorf("mcp config %s not cleaned up", mcp)
	}
}

func TestSessionKeepsResultWrittenJustBeforeExit(t *testing.T) {
	r := startFake(t, "burst-exit", nil)
	r.collect(t, nil)
	if r.result.Outcome != protocol.OutcomeSucceeded || r.result.FinalText != "burst done" {
		t.Fatalf("result %+v", r.result)
	}
	if n := len(r.kinds(providers.EventMessageDelta)); n != 600 {
		t.Errorf("got %d deltas, want 600", n)
	}
}

func TestSessionResumeAndUsageScope(t *testing.T) {
	r := startFake(t, "resume", func(s *providers.StartSpec) { s.ResumeSessionID = "sess-old" })
	r.collect(t, nil)
	var args []string
	_ = json.Unmarshal([]byte(r.file(t, "args.json")), &args)
	if v, _ := argValue(args, "--resume"); v != "sess-old" {
		t.Errorf("resume flag not passed: %v", args)
	}
	if r.result.Outcome != protocol.OutcomeSucceeded || r.result.VendorSessionID != "sess-old" {
		t.Fatalf("result %+v", r.result)
	}
	// Resumed sessions report session-cumulative cost; the adapter reports
	// only this run's per-turn tokens and no cost.
	u := r.result.Usage
	if u == nil || u.CostUSD != nil || *u.InputTokens != 110 || *u.OutputTokens != 5 {
		t.Errorf("usage %+v", u)
	}
}

func TestSessionSteeringIsQueued(t *testing.T) {
	r := startFake(t, "steer", nil)
	var mode string
	var sendErr error
	r.collect(t, func(ev providers.Event) {
		if ev.Kind == providers.EventToolStarted && mode == "" {
			mode, sendErr = r.sess.SendInput(context.Background(), "Also update the changelog.")
		}
	})
	if sendErr != nil || mode != "queued" {
		t.Fatalf("SendInput = %q, %v", mode, sendErr)
	}
	lines := r.stdinLines(t)
	if len(lines) != 2 || lines[1]["type"] != "user" ||
		lines[1]["message"].(map[string]any)["content"] != "Also update the changelog." {
		t.Fatalf("stdin %+v", lines)
	}
	if r.result.Outcome != protocol.OutcomeSucceeded || r.result.FinalText != "Adjusted per your note." {
		t.Fatalf("result %+v (the session must stay open until the steering turn is answered)", r.result)
	}
	if _, err := r.sess.SendInput(context.Background(), "late"); !errors.Is(err, ErrInputClosed) {
		t.Errorf("SendInput after end = %v", err)
	}
}

func TestOnResultKeepsInputOpenForPendingMessages(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	s := &session{stdin: pw, pending: []string{"u1", "u2", "u3"}}
	s.onResult(&wireMsg{Type: "result", UserMessageUUIDs: []string{"u1"}})
	if s.inputClosed || strings.Join(s.pending, ",") != "u2,u3" {
		t.Fatalf("after u1: closed=%v pending=%v", s.inputClosed, s.pending)
	}
	s.onResult(&wireMsg{Type: "result", Origin: &wireOrigin{Kind: "task-notification"}})
	if s.inputClosed || len(s.pending) != 2 {
		t.Fatalf("task notification changed pending: %v", s.pending)
	}
	// A merged turn names only the last message.
	s.onResult(&wireMsg{Type: "result", UserMessageUUID: "u3"})
	if !s.inputClosed || len(s.pending) != 0 {
		t.Fatalf("after merged u2+u3: closed=%v pending=%v", s.inputClosed, s.pending)
	}
}

func TestSessionRateLimited(t *testing.T) {
	r := startFake(t, "ratelimit", nil)
	r.collect(t, nil)
	res := r.result
	if res.Outcome != protocol.OutcomeRateLimited {
		t.Fatalf("result %+v", res)
	}
	if res.RetryAfter < 58*time.Minute || res.RetryAfter > 61*time.Minute {
		t.Errorf("retry after %v, want ~1h from the vendor reset time", res.RetryAfter)
	}
	if !strings.Contains(res.Error, "session limit") {
		t.Errorf("error %q", res.Error)
	}
	if rl := r.kinds(providers.EventRateLimited); len(rl) != 1 || rl[0].RetryAfter != res.RetryAfter {
		t.Errorf("rate limited events %+v", rl)
	}
	for _, m := range r.kinds(providers.EventMessage) {
		if strings.Contains(m.Text, "limit") {
			t.Errorf("limit text surfaced as an engineer message: %q", m.Text)
		}
	}
	if res.Usage != nil && res.Usage.CostUSD != nil && *res.Usage.CostUSD != 0 {
		t.Errorf("invented cost %v", *res.Usage.CostUSD)
	}
}

func TestSessionRateLimitedWithoutResetTime(t *testing.T) {
	r := startFake(t, "ratelimit-noreset", nil)
	r.collect(t, nil)
	if r.result.Outcome != protocol.OutcomeRateLimited || r.result.RetryAfter != 0 {
		t.Fatalf("result %+v (never invent a reset time)", r.result)
	}
}

func TestSessionAuthRequired(t *testing.T) {
	for _, sc := range []string{"auth", "auth-stderr"} {
		t.Run(sc, func(t *testing.T) {
			r := startFake(t, sc, nil)
			r.collect(t, nil)
			if r.result.Outcome != protocol.OutcomeAuthRequired {
				t.Fatalf("result %+v", r.result)
			}
			if len(r.kinds(providers.EventAuthRequired)) != 1 {
				t.Errorf("auth events %+v", r.events)
			}
		})
	}
}

func TestSessionCrash(t *testing.T) {
	r := startFake(t, "crash", nil)
	r.collect(t, nil)
	res := r.result
	if res.Outcome != protocol.OutcomeFailed || !res.ExitConfirmed {
		t.Fatalf("result %+v", res)
	}
	if !strings.Contains(res.Error, "exit status 3") || !strings.Contains(res.Error, "TypeError: boom") {
		t.Errorf("error %q should carry exit status and stderr tail", res.Error)
	}
	if len(r.kinds(providers.EventError)) != 1 {
		t.Errorf("error events %+v", r.events)
	}
}

func TestSessionWarnsWhenBridgeFails(t *testing.T) {
	r := startFake(t, "mcpfail", nil)
	r.collect(t, nil)
	found := false
	for _, w := range r.kinds(providers.EventWarning) {
		if strings.Contains(w.Text, "yip MCP bridge did not connect") && strings.Contains(w.Text, "spawn ENOENT") {
			found = true
		}
	}
	if !found {
		t.Errorf("no bridge warning in %+v", r.events)
	}
}

func TestSessionBillingFromInit(t *testing.T) {
	r := startFake(t, "apikey", nil)
	r.collect(t, nil)
	if r.result.Usage == nil || r.result.Usage.Billing != protocol.BillingAPI {
		t.Errorf("usage %+v", r.result.Usage)
	}
}

func TestSessionCancelCleanInterrupt(t *testing.T) {
	r := startFake(t, "cancel-clean", nil)
	start := time.Now()
	cancelled := make(chan error, 1)
	r.collect(t, func(ev providers.Event) {
		if ev.Kind == providers.EventToolStarted {
			go func() { cancelled <- r.sess.Cancel(context.Background()) }()
		}
	})
	if err := <-cancelled; err != nil {
		t.Errorf("Cancel: %v", err)
	}
	if r.result.Outcome != protocol.OutcomeCancelled || !r.result.ExitConfirmed {
		t.Fatalf("result %+v", r.result)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(r.file(t, "interrupt.json")), &req); err != nil {
		t.Fatal(err)
	}
	inner, _ := req["request"].(map[string]any)
	if req["type"] != "control_request" || inner["subtype"] != "interrupt" || inner["cancel_queued"] != true || req["request_id"] == "" {
		t.Errorf("interrupt request %+v", req)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("clean cancel took %v; should not need the kill path", time.Since(start))
	}
}

func TestSessionCancelKillsProcessGroup(t *testing.T) {
	r := startFake(t, "hang", func(*providers.StartSpec) {})
	var cancelErr error
	cancelled := make(chan struct{})
	r.collect(t, func(ev providers.Event) {
		if ev.Kind == providers.EventToolStarted {
			go func() {
				defer close(cancelled)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cancelErr = r.sess.Cancel(ctx)
			}()
		}
	})
	<-cancelled
	if cancelErr != nil {
		t.Errorf("Cancel: %v", cancelErr)
	}
	if r.result.Outcome != protocol.OutcomeCancelled || !r.result.ExitConfirmed {
		t.Fatalf("result %+v", r.result)
	}
	if !strings.Contains(r.file(t, "interrupt.json"), `"interrupt"`) {
		t.Errorf("no interrupt attempted before killing")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(r.file(t, "child.pid")))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child process %d of the provider group survived cancellation", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Cancel after the end is a no-op.
	if err := r.sess.Cancel(context.Background()); err != nil {
		t.Errorf("second Cancel: %v", err)
	}
}

func TestSessionTimeout(t *testing.T) {
	r := startFake(t, "hang", func(s *providers.StartSpec) { s.Timeout = 500 * time.Millisecond })
	r.collect(t, nil)
	if r.result.Outcome != protocol.OutcomeFailed || !strings.Contains(r.result.Error, "time limit") || !r.result.ExitConfirmed {
		t.Fatalf("result %+v", r.result)
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(r.file(t, "child.pid"))); err == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func TestApprovalsAndQuestionsUnsupported(t *testing.T) {
	r := startFake(t, "basic", nil)
	if err := r.sess.ResolveApproval(context.Background(), "x", providers.ApprovalDecision{Allow: true}); !errors.Is(err, providers.ErrUnsupported) {
		t.Errorf("ResolveApproval = %v", err)
	}
	if err := r.sess.AnswerQuestion(context.Background(), "x", providers.QuestionAnswer{}); !errors.Is(err, providers.ErrUnsupported) {
		t.Errorf("AnswerQuestion = %v", err)
	}
	r.collect(t, nil)
}

// ---- probe ----

func TestProbeOtherVersions(t *testing.T) {
	for _, version := range []string{"999.0.0", "unknown"} {
		t.Run(version, func(t *testing.T) {
			a, _ := newFakeAdapter(t, "probe", "YIP_TEST_VERSION="+version, envAuthJSON+`={"loggedIn":true,"authMethod":"api_key"}`)
			a.opts.SkipAuthStatus = false
			inst := a.Probe(context.Background())
			wantVersion := version
			if version == "unknown" {
				wantVersion = ""
			}
			if inst.Version != wantVersion || inst.Tested || inst.TestedVersion != TestedVersion || inst.AuthState != protocol.AuthReady || !inst.Capabilities.ReadOnly {
				t.Fatalf("probe: %+v", inst)
			}
			if strings.Contains(strings.Join(inst.Limitations, " "), "tested version") {
				t.Fatalf("version warning: %v", inst.Limitations)
			}
		})
	}
}

func TestProbeSubscription(t *testing.T) {
	a, _ := newFakeAdapter(t, "probe", envAuthJSON+`={"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"ada@example.com","orgName":"Ada's Org","subscriptionType":"max"}`)
	a.opts.SkipAuthStatus = false
	inst := a.Probe(context.Background())
	if inst.Version != "2.1.282" || !inst.Tested || inst.TestedVersion != TestedVersion {
		t.Errorf("version %+v", inst)
	}
	if inst.AuthState != protocol.AuthReady || inst.Billing != protocol.BillingSubscription || inst.Account != "ada@example.com" {
		t.Errorf("auth %+v", inst)
	}
	if !inst.Capabilities.StructuredEvents || !inst.Capabilities.ReadOnly || !inst.Capabilities.MCPTools || inst.Capabilities.UserQuestions {
		t.Errorf("capabilities %+v", inst.Capabilities)
	}
	var ids []string
	for _, m := range inst.Models {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "fable,opus,sonnet" {
		t.Errorf("models %v", ids)
	}
	if a.probeBilling() != protocol.BillingSubscription {
		t.Errorf("cached billing %q", a.probeBilling())
	}
}

func TestProbeAPIKeyAndSignedOut(t *testing.T) {
	a, _ := newFakeAdapter(t, "probe", "ANTHROPIC_API_KEY=sk-test", envAuthJSON+`={"loggedIn":true,"authMethod":"api_key","apiKeySource":"ANTHROPIC_API_KEY"}`)
	a.opts.SkipAuthStatus = false
	if inst := a.Probe(context.Background()); inst.Billing != protocol.BillingAPI || inst.AuthState != protocol.AuthReady {
		t.Errorf("api key probe %+v", inst)
	}
	a, _ = newFakeAdapter(t, "probe", envAuthJSON+`={"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`)
	a.opts.SkipAuthStatus = false
	if inst := a.Probe(context.Background()); inst.AuthState != protocol.AuthNeedsSignIn || inst.Billing != protocol.BillingUnknown {
		t.Errorf("signed-out probe %+v", inst)
	}
	a, _ = newFakeAdapter(t, "probe")
	if inst := a.Probe(context.Background()); inst.AuthState != protocol.AuthUnknown || inst.Billing != protocol.BillingUnknown {
		t.Errorf("skip-auth probe %+v", inst)
	}
}

func TestProbeGatewayBearerBilling(t *testing.T) {
	const credential = "ANTHROPIC_AUTH_TOKEN"
	const ready = `{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty","email":"saved@example.invalid"}`
	for _, tc := range []struct {
		name    string
		env     []string
		status  string
		skip    bool
		auth    string
		billing string
	}{
		{"explicit bearer", []string{credential + "=synthetic-token"}, ready, false, protocol.AuthReady, protocol.BillingAPI},
		{"unset", nil, ready, false, protocol.AuthReady, protocol.BillingSubscription},
		{"empty", []string{credential + "="}, ready, false, protocol.AuthReady, protocol.BillingSubscription},
		{"last empty wins", []string{credential + "=synthetic-token", credential + "="}, ready, false, protocol.AuthReady, protocol.BillingSubscription},
		{"last token wins", []string{credential + "=", credential + "=synthetic-token"}, ready, false, protocol.AuthReady, protocol.BillingAPI},
		{"managed sign-in required", []string{credential + "=synthetic-token"}, `{"loggedIn":false,"authMethod":"none","apiProvider":"gateway"}`, false, protocol.AuthNeedsSignIn, protocol.BillingUnknown},
		{"invalid status", []string{credential + "=synthetic-token"}, "invalid", false, protocol.AuthUnknown, protocol.BillingAPI},
		{"skipped status", []string{credential + "=synthetic-token"}, ready, true, protocol.AuthUnknown, protocol.BillingAPI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newFakeAdapter(t, "probe", append(tc.env, "HOME="+t.TempDir(), envAuthJSON+"="+tc.status)...)
			a.opts.SkipAuthStatus = tc.skip
			inst := a.Probe(t.Context())
			if inst.AuthState != tc.auth || inst.Billing != tc.billing || a.probeBilling() != tc.billing {
				t.Fatalf("auth=%s billing=%s cached=%s; want auth=%s billing=%s", inst.AuthState, inst.Billing, a.probeBilling(), tc.auth, tc.billing)
			}
			if tc.billing == protocol.BillingAPI && (inst.Account != "" || !strings.Contains(inst.AuthDetail, "unverified")) {
				t.Fatalf("gateway metadata implies verified account or access: account=%q detail=%q", inst.Account, inst.AuthDetail)
			}
			if strings.Contains(inst.AuthDetail, "synthetic-token") {
				t.Fatal("gateway credential leaked in metadata")
			}
		})
	}
}

func TestGatewayBearerUsesOnlyEffectiveEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "host-token-must-not-be-imported")
	a := NewAdapter()
	if _, ok := envLookup(a.baseEnv(), "ANTHROPIC_AUTH_TOKEN"); ok {
		t.Fatal("default environment imported a host gateway credential")
	}
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"bearer override", []string{"ANTHROPIC_AUTH_TOKEN=synthetic-token"}, protocol.BillingAPI},
		{"empty override", []string{"ANTHROPIC_AUTH_TOKEN=synthetic-token", "ANTHROPIC_AUTH_TOKEN="}, protocol.BillingSubscription},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, dir := newFakeAdapter(t, "basic", "HOME="+t.TempDir(), envAuthJSON+`={"loggedIn":true,"authMethod":"oauth_token"}`)
			a.opts.SkipAuthStatus = false
			if inst := a.Probe(t.Context()); inst.Billing != protocol.BillingSubscription {
				t.Fatalf("initial billing=%s", inst.Billing)
			}
			spec := testSpec(t, protocol.ModeEdit)
			spec.Env = append(a.opts.Env, tc.env...)
			sess, err := a.Start(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			r := &fakeRun{dir: dir, adapter: a, sess: sess, spec: spec}
			r.collect(t, nil)
			if r.result.Usage == nil || r.result.Usage.Billing != tc.want {
				t.Fatalf("effective environment billing: %+v; want %s", r.result.Usage, tc.want)
			}
		})
	}
}

func TestProbeRefusesVersionsMissingFlags(t *testing.T) {
	a, _ := newFakeAdapter(t, "oldhelp")
	inst := a.Probe(context.Background())
	if inst.Capabilities.StructuredEvents || !strings.Contains(strings.Join(inst.Limitations, " "), "--restricted") {
		t.Errorf("probe %+v", inst)
	}
	spec := testSpec(t, protocol.ModeEdit)
	if _, err := a.Start(context.Background(), spec); !errors.Is(err, providers.ErrUnsupported) {
		t.Errorf("Start with missing flags = %v", err)
	}
}

func TestProbeNotInstalled(t *testing.T) {
	a := NewAdapter(Options{Executable: filepath.Join(t.TempDir(), "claude"), SkipAuthStatus: true})
	if inst := a.Probe(context.Background()); inst.AuthState != protocol.AuthNotInstalled {
		t.Errorf("probe %+v", inst)
	}
}

// TestRealHelpHasRequiredFlags checks the installed binary's --help (the only
// commands run against it are --version and --help).
func TestRealHelpHasRequiredFlags(t *testing.T) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not installed")
	}
	a := NewAdapter(Options{Executable: exe, SkipAuthStatus: true})
	inst := a.Probe(context.Background())
	if inst.Version != TestedVersion {
		t.Skipf("installed claude %s is not the pinned %s", inst.Version, TestedVersion)
	}
	if missing := a.missingFlags(); len(missing) > 0 {
		t.Fatalf("pinned claude --help lacks %v", missing)
	}
	if len(inst.Models) == 0 {
		t.Errorf("no model aliases parsed from --help")
	}
}

// ---- classification ----

func TestClassify(t *testing.T) {
	now := time.Now()
	errRes := func(text string, status int) *wireMsg {
		raw, _ := json.Marshal(text)
		m := &wireMsg{Type: "result", Subtype: "success", IsError: true, ResultRaw: raw}
		if status != 0 {
			m.APIErrorStatus = &status
		}
		return m
	}
	cases := []struct {
		name string
		st   *streamState
		err  string
		want string
	}{
		{"invalid key", &streamState{lastResult: errRes("Invalid API key · Please run /login", 0)}, "", protocol.OutcomeAuthRequired},
		{"login expired", &streamState{lastResult: errRes("Login expired · Please run /login", 0)}, "", protocol.OutcomeAuthRequired},
		{"401", &streamState{lastResult: errRes("API Error: 401", 401)}, "", protocol.OutcomeAuthRequired},
		{"weekly", &streamState{lastResult: errRes("You've hit your weekly limit · resets Mon 12:00am", 0)}, "", protocol.OutcomeRateLimited},
		{"credit", &streamState{lastResult: errRes("Credit balance is too low", 0)}, "", protocol.OutcomeRateLimited},
		{"429", &streamState{lastResult: errRes("API Error", 429)}, "", protocol.OutcomeRateLimited},
		{"context limit is not a quota", &streamState{lastResult: errRes("Context limit reached · /compact or /clear to continue", 0)}, "", protocol.OutcomeFailed},
		{"max turns", &streamState{lastResult: &wireMsg{Type: "result", Subtype: "error_max_turns", IsError: true}}, "", protocol.OutcomeFailed},
		{"no result", &streamState{}, "some stderr", protocol.OutcomeFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(c.st, nil, c.err, false, 0, now)
			if got.Outcome != c.want {
				t.Errorf("outcome %s (%q), want %s", got.Outcome, got.Error, c.want)
			}
			if got.RetryAfter != 0 {
				t.Errorf("invented retry-after %v", got.RetryAfter)
			}
		})
	}
}

func TestToolSummary(t *testing.T) {
	cases := map[string][2]string{
		"Reading refresh.go":                  {"Read", `{"file_path":"/a/b/refresh.go"}`},
		"Editing main.go":                     {"Edit", `{"file_path":"main.go"}`},
		"Running go test ./... …":             {"Bash", `{"command":"go test ./...\nsecond line"}`},
		"Run the tests":                       {"Bash", `{"command":"go test","description":"Run the tests"}`},
		"Searching for TODO":                  {"Grep", `{"pattern":"TODO"}`},
		"Fetching example.com":                {"WebFetch", `{"url":"https://example.com/x"}`},
		"Using yip work create":               {"mcp__yip__work_create", `{}`},
		"Using Frobnicate":                    {"Frobnicate", `{}`},
		"Finding files matching **/*_test.go": {"Glob", `{"pattern":"**/*_test.go"}`},
	}
	for want, c := range cases {
		if got := toolSummary(c[0], json.RawMessage(c[1])); got != want {
			t.Errorf("toolSummary(%s) = %q, want %q", c[0], got, want)
		}
	}
}

func TestBundledSkillsThroughLaunchConfig(t *testing.T) {
	for _, mode := range []string{"edit", "readonly", "conversation"} {
		t.Run(mode, func(t *testing.T) {
			spec := testSpec(t, mode)
			spec.MCP = skilltest.New(t, mode)
			plan, err := buildLaunch(spec, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(plan.TempDir)
			allowed, _ := argValue(plan.Args, "--allowedTools")
			if !contains(splitList(allowed), "mcp__yip") {
				t.Fatal("bridge tools disallowed")
			}
			raw, err := os.ReadFile(plan.MCPPath)
			if err != nil {
				t.Fatal(err)
			}
			var cfg mcpConfigFile
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			server := cfg.MCPServers["yip"]
			if err := skilltest.Verify(providers.MCPServer{Command: server.Command, Args: server.Args, Env: server.Env}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEngineerDraftLaunchExposesNoTools(t *testing.T) {
	spec := testSpec(t, protocol.ModeConversation)
	spec.EngineerDraft = true
	plan, err := buildLaunch(spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(plan.TempDir)
	for _, flag := range []string{"--tools", "--allowedTools"} {
		if value, ok := argValue(plan.Args, flag); !ok || value != "" {
			t.Fatalf("%s = %q, present=%v", flag, value, ok)
		}
	}
	if value, _ := argValue(plan.Args, "--permission-mode"); value != "dontAsk" {
		t.Fatalf("permission mode: %q", value)
	}
	data, err := os.ReadFile(plan.MCPPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil || len(cfg.MCPServers) != 0 {
		t.Fatalf("MCP config: %s, %v", data, err)
	}
	spec.InheritUserConfig = true
	if _, err := buildLaunch(spec, t.TempDir()); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("imported draft accepted: %v", err)
	}
}
