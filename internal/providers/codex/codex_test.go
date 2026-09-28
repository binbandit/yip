package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// harness wires a scenario of the fake app-server into an adapter.
type harness struct {
	t       *testing.T
	adapter *Adapter
	script  string
	logPath string
	workdir string
}

func newHarness(t *testing.T, scenario string, extraEnv ...string) *harness {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "fake.log")
	script := filepath.Join(dir, "codex")
	body := "#!/bin/sh\n" +
		"export YIP_CODEX_FAKE=" + shellQuote(scenario) + "\n" +
		"export YIP_CODEX_FAKE_LOG=" + shellQuote(logPath) + "\n" +
		exports(extraEnv) +
		"exec " + shellQuote(self) + " \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	workdir := filepath.Join(dir, "work")
	if err := os.MkdirAll(filepath.Join(workdir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(WithExecutable(script), WithTimeouts(Timeouts{
		Handshake: 10 * time.Second,
		Request:   10 * time.Second,
		Interrupt: 2 * time.Second,
		Grace:     time.Second,
	}))
	return &harness{t: t, adapter: a, script: script, logPath: logPath, workdir: workdir}
}

func exports(kvs []string) string {
	var b strings.Builder
	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		b.WriteString("export " + k + "=" + shellQuote(v) + "\n")
	}
	return b.String()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (h *harness) spec() providers.StartSpec {
	return providers.StartSpec{
		RunID:        "run_1",
		Workdir:      h.workdir,
		Mode:         protocol.ModeEdit,
		Instructions: "You are Ada, a careful Go engineer.",
		Prompt:       "Fix the failing test.",
		MCP: providers.MCPServer{
			Name:    "yip",
			Command: "/usr/local/bin/yip",
			Args:    []string{"bridge", "--run", "run_1"},
			Env:     map[string]string{"YIP_BRIDGE_TOKEN": "secret-token"},
		},
		Env: []string{"PATH=" + os.Getenv("PATH"), "YIP_ALLOWED=1"},
	}
}

func (h *harness) start(spec providers.StartSpec) providers.Session {
	h.t.Helper()
	s, err := h.adapter.Start(context.Background(), spec)
	if err != nil {
		h.t.Fatalf("Start: %v", err)
	}
	return s
}

// collect drains events until the channel closes, calling on for each.
func collect(t *testing.T, s providers.Session, on func(providers.Event)) []providers.Event {
	t.Helper()
	var evs []providers.Event
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				return evs
			}
			evs = append(evs, ev)
			if on != nil {
				on(ev)
			}
		case <-timeout:
			t.Fatalf("timed out waiting for events; got %d: %+v", len(evs), evs)
		}
	}
}

func waitResult(t *testing.T, s providers.Session) providers.Result {
	t.Helper()
	ch := make(chan providers.Result, 1)
	go func() { ch <- s.Wait() }()
	select {
	case r := <-ch:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("Wait did not return")
	}
	return providers.Result{}
}

type logEntry struct {
	Argv     []string `json:"argv"`
	Env      []string `json:"env"`
	Cwd      string   `json:"cwd"`
	ChildPid int      `json:"childPid"`
	EOF      bool     `json:"eof"`
	Stuck    bool     `json:"stuck"`
	In       *fakeMsg `json:"in"`
}

func (h *harness) log() []logEntry {
	h.t.Helper()
	f, err := os.Open(h.logPath)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	var out []logEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e logEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			h.t.Fatalf("bad log line %s: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

// sent returns the params of every message the adapter sent with method.
func (h *harness) sent(method string) []json.RawMessage {
	var out []json.RawMessage
	for _, e := range h.log() {
		if e.In != nil && e.In.Method == method {
			out = append(out, e.In.Params)
		}
	}
	return out
}

// response returns the adapter's response (result or error) to server
// request id.
func (h *harness) response(id int) *fakeMsg {
	want := strings.TrimSpace(string(mustJSON(id)))
	for _, e := range h.log() {
		if e.In != nil && e.In.Method == "" && strings.Trim(string(e.In.ID), `"`) == want {
			return e.In
		}
	}
	return nil
}

func kinds(evs []providers.Event, kind string) []providers.Event {
	var out []providers.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func hasText(evs []providers.Event, kind, substr string) bool {
	for _, e := range kinds(evs, kind) {
		if strings.Contains(e.Text, substr) {
			return true
		}
	}
	return false
}

func decode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func TestHandshakeStreamingAndLaunchBoundary(t *testing.T) {
	t.Setenv("YIP_PARENT_ONLY", "must-not-leak")
	h := newHarness(t, "basic")
	s := h.start(h.spec())
	evs := collect(t, s, nil)
	res := waitResult(t, s)

	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome %q (%s)", res.Outcome, res.Error)
	}
	if res.FinalText != "Hello" || res.VendorSessionID != "thr_1" || !res.ExitConfirmed {
		t.Fatalf("result %+v", res)
	}
	if res.Usage == nil || res.Usage.Source != "vendor" || *res.Usage.InputTokens != 100 || *res.Usage.OutputTokens != 20 || res.Usage.Billing != protocol.BillingSubscription {
		t.Fatalf("usage %+v", res.Usage)
	}

	// Streaming and item mapping.
	var deltas string
	for _, e := range kinds(evs, providers.EventMessageDelta) {
		deltas += e.Text
	}
	if deltas != "Hello" {
		t.Fatalf("deltas %q", deltas)
	}
	if vs := kinds(evs, providers.EventVendorSession); len(vs) != 1 || vs[0].VendorSessionID != "thr_1" {
		t.Fatalf("vendor session events %+v", vs)
	}
	if !hasText(evs, providers.EventMessage, "Hello") ||
		!hasText(evs, providers.EventToolStarted, "Running `go test ./...`") ||
		!hasText(evs, providers.EventToolFinished, "Ran `go test ./...` (exit 0)") ||
		!hasText(evs, providers.EventToolStarted, "Editing main.go") ||
		!hasText(evs, providers.EventToolFinished, "Edited main.go") ||
		!hasText(evs, providers.EventToolFinished, "yip.room_post finished") {
		t.Fatalf("missing tool/message events: %+v", evs)
	}
	if u := kinds(evs, providers.EventUsage); len(u) != 1 || *u[0].Usage.InputTokens != 100 {
		t.Fatalf("usage events %+v", u)
	}
	if !hasText(evs, providers.EventStatus, "Disabled Codex MCP servers for this run: evil, github") {
		t.Fatalf("expected MCP disable status: %+v", evs)
	}

	// Launch arguments and environment.
	lg := h.log()
	argv := strings.Join(lg[0].Argv, " ")
	for _, want := range []string{"app-server --listen stdio://", "--disable hooks", "--disable plugins", "--disable apps", "--disable memories", "-c notify=[]"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q missing %q", argv, want)
		}
	}
	if strings.Contains(argv, "secret-token") || strings.Contains(argv, "dangerously") || strings.Contains(argv, "danger-full-access") {
		t.Errorf("argv leaks secrets or bypass flags: %q", argv)
	}
	env := strings.Join(lg[0].Env, "\n")
	if !strings.Contains(env, "YIP_ALLOWED=1") || strings.Contains(env, "YIP_PARENT_ONLY") {
		t.Errorf("env not allowlisted: %s", env)
	}
	if !samePath(lg[0].Cwd, h.workdir) {
		t.Errorf("app-server cwd %q, want %q", lg[0].Cwd, h.workdir)
	}

	// initialize identifies as yip.
	init := decode[initializeParams](t, h.sent("initialize")[0])
	if init.ClientInfo.Name != "yip" {
		t.Errorf("clientInfo %+v", init.ClientInfo)
	}
	if len(h.sent("initialized")) != 1 {
		t.Errorf("initialized notification not sent")
	}

	// thread/start carries the boundary.
	type threadStartSent struct {
		Cwd                   *string        `json:"cwd"`
		Sandbox               string         `json:"sandbox"`
		ApprovalPolicy        any            `json:"approvalPolicy"`
		ApprovalsReviewer     string         `json:"approvalsReviewer"`
		DeveloperInstructions string         `json:"developerInstructions"`
		Model                 *string        `json:"model"`
		Config                map[string]any `json:"config"`
	}
	ts := decode[threadStartSent](t, h.sent("thread/start")[0])
	if ts.Cwd != nil {
		t.Errorf("thread/start must omit cwd (avoids app-server's trust write), got %q", *ts.Cwd)
	}
	if ts.Sandbox != "workspace-write" || ts.ApprovalPolicy != "on-request" || ts.ApprovalsReviewer != "user" {
		t.Errorf("sandbox/approval %q %v %q", ts.Sandbox, ts.ApprovalPolicy, ts.ApprovalsReviewer)
	}
	if ts.DeveloperInstructions != "You are Ada, a careful Go engineer." || ts.Model != nil {
		t.Errorf("instructions/model %q %v", ts.DeveloperInstructions, ts.Model)
	}
	servers, _ := ts.Config["mcp_servers"].(map[string]any)
	yip, _ := servers["yip"].(map[string]any)
	if yip["command"] != "/usr/local/bin/yip" || yip["default_tools_approval_mode"] != "approve" || yip["required"] != true {
		t.Errorf("yip mcp config %+v", yip)
	}
	if envm, _ := yip["env"].(map[string]any); envm["YIP_BRIDGE_TOKEN"] != "secret-token" {
		t.Errorf("yip mcp env %+v", yip["env"])
	}
	for _, n := range []string{"github", "evil"} {
		if srv, _ := servers[n].(map[string]any); srv["enabled"] != false || len(srv) != 1 {
			t.Errorf("MCP server %s not disabled: %+v", n, servers[n])
		}
	}
	if len(servers) != 3 {
		t.Errorf("unexpected MCP servers %+v", servers)
	}
	projects, _ := ts.Config["projects"].(map[string]any)
	if p, _ := projects[h.workdir].(map[string]any); p["trust_level"] != "untrusted" {
		t.Errorf("workdir not marked untrusted: %+v", projects)
	}
	sww, _ := ts.Config["sandbox_workspace_write"].(map[string]any)
	if sww["network_access"] != false {
		t.Errorf("sandbox_workspace_write not pinned: %+v", sww)
	}

	// The prompt is the user turn; instructions are not.
	tsp := decode[turnStartParams](t, h.sent("turn/start")[0])
	if tsp.ThreadID != "thr_1" || len(tsp.Input) != 1 || tsp.Input[0].Text != "Fix the failing test." {
		t.Errorf("turn/start %+v", tsp)
	}
}

func TestCommandApprovalAllowedAndFileChangeDenied(t *testing.T) {
	h := newHarness(t, "approvals")
	s := h.start(h.spec())
	var approvals []providers.ApprovalRequest
	evs := collect(t, s, func(ev providers.Event) {
		if ev.Kind != providers.EventApprovalRequest {
			return
		}
		approvals = append(approvals, *ev.Approval)
		allow := len(approvals) == 1
		go func(id string) {
			// A human may take a while; the request must stay pending.
			time.Sleep(300 * time.Millisecond)
			if err := s.ResolveApproval(context.Background(), id, providers.ApprovalDecision{Allow: allow, Reason: "not now"}); err != nil {
				t.Errorf("ResolveApproval: %v", err)
			}
		}(ev.Approval.ID)
	})
	res := waitResult(t, s)
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome %q (%s)", res.Outcome, res.Error)
	}
	if len(approvals) != 2 {
		t.Fatalf("approvals %+v", approvals)
	}
	a0, a1 := approvals[0].Action, approvals[1].Action
	if a0.Kind != "exec" || a0.Command != "rm -rf build" || !samePath(a0.Target, h.workdir) || a0.Summary != "Run `rm -rf build`" || a0.Detail != "needs to write outside the sandbox" {
		t.Errorf("command action %+v", a0)
	}
	if a1.Kind != "edit" || a1.Target != "main.go" || !strings.Contains(a1.Summary, "main.go") || !strings.Contains(a1.Detail, "+b") {
		t.Errorf("file action %+v", a1)
	}
	if len(approvals[0].Raw) == 0 {
		t.Errorf("raw payload not kept for audit")
	}
	if r := h.response(100); r == nil || !strings.Contains(string(r.Result), `"decision":"accept"`) {
		t.Errorf("command decision %+v", r)
	}
	if r := h.response(101); r == nil || !strings.Contains(string(r.Result), `"decision":"decline"`) {
		t.Errorf("file decision %+v", r)
	}
	if !hasText(evs, providers.EventToolFinished, "Ran `rm -rf build`") || !hasText(evs, providers.EventToolFinished, "Edit declined: main.go") {
		t.Errorf("tool outcomes missing: %+v", evs)
	}
	// Resolving twice is an error, not a second reply.
	if err := s.ResolveApproval(context.Background(), approvals[0].ID, providers.ApprovalDecision{Allow: true}); err == nil {
		t.Errorf("expected error resolving a finished request")
	}
}

func TestReadOnlyModeEnforcedByPolicyAndAdapter(t *testing.T) {
	h := newHarness(t, "readonly")
	spec := h.spec()
	spec.Mode = protocol.ModeReadOnly
	s := h.start(spec)
	evs := collect(t, s, nil)
	res := waitResult(t, s)
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome %q (%s)", res.Outcome, res.Error)
	}
	if n := len(kinds(evs, providers.EventApprovalRequest)); n != 0 {
		t.Fatalf("read-only run surfaced %d approvals", n)
	}
	for _, id := range []int{100, 101} {
		if r := h.response(id); r == nil || !strings.Contains(string(r.Result), `"decline"`) {
			t.Errorf("request %d not declined: %+v", id, r)
		}
	}
	type readOnlySent struct {
		Sandbox        string `json:"sandbox"`
		ApprovalPolicy string `json:"approvalPolicy"`
	}
	ts := decode[readOnlySent](t, h.sent("thread/start")[0])
	if ts.Sandbox != "read-only" || ts.ApprovalPolicy != "never" {
		t.Errorf("read-only thread params %+v", ts)
	}
	if !hasText(evs, providers.EventWarning, "read-only") {
		t.Errorf("expected a read-only warning: %+v", evs)
	}
}

func TestQuestionsElicitationsAndUnknownRequests(t *testing.T) {
	h := newHarness(t, "questions")
	s := h.start(h.spec())
	var question *providers.Question
	var approvals []providers.ApprovalRequest
	evs := collect(t, s, func(ev providers.Event) {
		switch ev.Kind {
		case providers.EventQuestion:
			question = ev.Question
			go s.AnswerQuestion(context.Background(), ev.Question.ID, providers.QuestionAnswer{Selected: 1})
		case providers.EventApprovalRequest:
			approvals = append(approvals, *ev.Approval)
			go s.ResolveApproval(context.Background(), ev.Approval.ID, providers.ApprovalDecision{Allow: true})
		}
	})
	res := waitResult(t, s)
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome %q (%s)", res.Outcome, res.Error)
	}
	if question == nil || question.Kind != "ask" || !strings.Contains(question.Text, "Which database?") || strings.Join(question.Options, ",") != "sqlite,postgres" {
		t.Fatalf("question %+v", question)
	}
	if r := h.response(200); r == nil || !strings.Contains(string(r.Result), `"q1":{"answers":["postgres"]}`) {
		t.Errorf("user input answer %+v", r)
	}
	if len(approvals) != 2 || approvals[0].Action.Kind != "mcp" || approvals[0].Action.Target != "yip" || approvals[1].Action.Kind != "network" {
		t.Fatalf("approvals %+v", approvals)
	}
	if r := h.response(201); r == nil || !strings.Contains(string(r.Result), `"action":"accept"`) {
		t.Errorf("mcp approval response %+v", r)
	}
	if r := h.response(202); r == nil || r.Error == nil {
		t.Errorf("unknown request must get an explicit error response: %+v", r)
	}
	if r := h.response(203); r == nil || !strings.Contains(string(r.Result), `"network":{"enabled":true}`) {
		t.Errorf("permissions grant %+v", r)
	}
	if !hasText(evs, providers.EventWarning, "currentTime/read") {
		t.Errorf("unsupported request not surfaced: %+v", evs)
	}
}

func TestSteeringImmediate(t *testing.T) {
	h := newHarness(t, "steer")
	s := h.start(h.spec())
	var mode string
	var sendErr error
	collect(t, s, func(ev providers.Event) {
		if ev.Kind == providers.EventMessageDelta && mode == "" {
			mode, sendErr = s.SendInput(context.Background(), "focus on the parser")
		}
	})
	res := waitResult(t, s)
	if sendErr != nil || mode != "immediate" {
		t.Fatalf("SendInput = %q, %v", mode, sendErr)
	}
	if res.FinalText != "steered: focus on the parser" {
		t.Fatalf("final %q", res.FinalText)
	}
	st := decode[turnSteerParams](t, h.sent("turn/steer")[0])
	if st.ExpectedTurnID != "turn_1" || st.ThreadID != "thr_1" {
		t.Errorf("steer params %+v", st)
	}
	if _, err := s.SendInput(context.Background(), "late"); err == nil {
		t.Errorf("SendInput after the session ended must fail")
	}
}

func TestSteeringQueuedWhenTurnNotSteerable(t *testing.T) {
	h := newHarness(t, "steer-queued")
	s := h.start(h.spec())
	var mode string
	var sendErr error
	collect(t, s, func(ev providers.Event) {
		if ev.Kind == providers.EventMessageDelta && mode == "" {
			mode, sendErr = s.SendInput(context.Background(), "also update the docs")
		}
	})
	res := waitResult(t, s)
	if sendErr != nil || mode != "queued" {
		t.Fatalf("SendInput = %q, %v", mode, sendErr)
	}
	if res.Outcome != protocol.OutcomeSucceeded || res.FinalText != "second turn: also update the docs" {
		t.Fatalf("result %+v", res)
	}
	if n := len(h.sent("turn/start")); n != 2 {
		t.Fatalf("expected a second turn, got %d turn/start", n)
	}
}

func processGone(pid int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func childPid(t *testing.T, h *harness) int {
	for _, e := range h.log() {
		if e.ChildPid != 0 {
			return e.ChildPid
		}
	}
	t.Fatal("fake did not log its child pid")
	return 0
}

func TestCancelInterruptsTurnAndKillsProcessGroup(t *testing.T) {
	for _, scenario := range []string{"cancel", "cancel-stuck"} {
		t.Run(scenario, func(t *testing.T) {
			h := newHarness(t, scenario)
			s := h.start(h.spec())
			cancelled := make(chan error, 1)
			collect(t, s, func(ev providers.Event) {
				if ev.Kind == providers.EventMessageDelta {
					go func() { cancelled <- s.Cancel(context.Background()) }()
				}
			})
			if err := <-cancelled; err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			res := waitResult(t, s)
			if res.Outcome != protocol.OutcomeCancelled || !res.ExitConfirmed {
				t.Fatalf("result %+v", res)
			}
			if len(h.sent("turn/interrupt")) != 1 {
				t.Errorf("turn/interrupt not sent")
			}
			if scenario == "cancel-stuck" {
				// The fake must really have hung (not crashed), so the
				// SIGTERM→SIGKILL escalation is what ended it.
				stuck := false
				for _, e := range h.log() {
					stuck = stuck || e.Stuck
				}
				if !stuck {
					t.Fatalf("fake did not reach its stuck state")
				}
			}
			if pid := childPid(t, h); !processGone(pid) {
				t.Errorf("child %d of the provider process group survived", pid)
			}
		})
	}
}

func TestResumeUsesThreadAndReportsOnlyNewUsage(t *testing.T) {
	h := newHarness(t, "resume")
	spec := h.spec()
	spec.ResumeSessionID = "thr_old"
	spec.Model = "gpt-test"
	s := h.start(spec)
	evs := collect(t, s, nil)
	res := waitResult(t, s)
	if res.Outcome != protocol.OutcomeSucceeded || res.VendorSessionID != "thr_old" {
		t.Fatalf("result %+v", res)
	}
	if res.Usage == nil || *res.Usage.InputTokens != 100 || *res.Usage.OutputTokens != 30 {
		t.Fatalf("usage should exclude replayed history: %+v", res.Usage)
	}
	if len(h.sent("thread/start")) != 0 {
		t.Fatalf("resume must not start a new thread")
	}
	type resumeSent struct {
		ThreadID string `json:"threadId"`
		Cwd      string `json:"cwd"`
		Model    string `json:"model"`
	}
	rp := decode[resumeSent](t, h.sent("thread/resume")[0])
	if rp.ThreadID != "thr_old" || rp.Cwd != h.workdir || rp.Model != "gpt-test" {
		t.Errorf("resume params %+v", rp)
	}
	if vs := kinds(evs, providers.EventVendorSession); len(vs) != 1 || vs[0].VendorSessionID != "thr_old" {
		t.Errorf("vendor session %+v", vs)
	}
}

func TestRateLimitUsesVendorResetOnly(t *testing.T) {
	h := newHarness(t, "ratelimit")
	s := h.start(h.spec())
	evs := collect(t, s, nil)
	res := waitResult(t, s)
	if res.Outcome != protocol.OutcomeRateLimited {
		t.Fatalf("outcome %q (%s)", res.Outcome, res.Error)
	}
	if res.RetryAfter < 58*time.Minute || res.RetryAfter > time.Hour {
		t.Fatalf("RetryAfter %v, want ~1h from the exhausted primary window", res.RetryAfter)
	}
	if rl := kinds(evs, providers.EventRateLimited); len(rl) != 1 || rl[0].RetryAfter != res.RetryAfter {
		t.Fatalf("rate_limited events %+v", rl)
	}

	h = newHarness(t, "ratelimit-noreset")
	s = h.start(h.spec())
	collect(t, s, nil)
	res = waitResult(t, s)
	if res.Outcome != protocol.OutcomeRateLimited || res.RetryAfter != 0 {
		t.Fatalf("without an exhausted window RetryAfter must stay 0: %+v", res)
	}
	if len(h.sent("account/rateLimits/read")) != 1 {
		t.Errorf("expected a rate-limit read for reset data")
	}
}

func TestAuthRequired(t *testing.T) {
	h := newHarness(t, "auth")
	s := h.start(h.spec())
	evs := collect(t, s, nil)
	res := waitResult(t, s)
	if res.Outcome != protocol.OutcomeAuthRequired || !res.ExitConfirmed {
		t.Fatalf("result %+v", res)
	}
	if len(kinds(evs, providers.EventAuthRequired)) != 1 {
		t.Fatalf("auth events %+v", evs)
	}
	if len(h.sent("thread/start")) != 0 {
		t.Fatalf("no thread may start without sign-in")
	}
	if acct := decode[getAccountParams](t, h.sent("account/read")[0]); acct.RefreshToken {
		t.Errorf("account/read must not force a token refresh")
	}

	h = newHarness(t, "auth-midturn")
	s = h.start(h.spec())
	evs = collect(t, s, nil)
	res = waitResult(t, s)
	if res.Outcome != protocol.OutcomeAuthRequired || !strings.Contains(res.Error, "401") {
		t.Fatalf("mid-turn 401: %+v", res)
	}
	if len(kinds(evs, providers.EventAuthRequired)) != 1 {
		t.Fatalf("auth events %+v", evs)
	}
}

func TestCrashReportsStderrTail(t *testing.T) {
	h := newHarness(t, "crash")
	s := h.start(h.spec())
	evs := collect(t, s, nil)
	res := waitResult(t, s)
	if res.Outcome != protocol.OutcomeFailed || !strings.Contains(res.Error, "exit code 3") || !strings.Contains(res.Error, "something broke") {
		t.Fatalf("result %+v", res)
	}
	if !res.ExitConfirmed || res.VendorSessionID != "thr_1" {
		t.Fatalf("result %+v", res)
	}
	if !hasText(evs, providers.EventError, "something broke") {
		t.Fatalf("error event missing: %+v", evs)
	}
}

func TestRefusesUnapprovedHooks(t *testing.T) {
	h := newHarness(t, "hooks")
	s := h.start(h.spec())
	collect(t, s, nil)
	res := waitResult(t, s)
	if res.Outcome != protocol.OutcomeFailed || !strings.Contains(res.Error, "hooks yip has not approved") || !strings.Contains(res.Error, "sessionStart") {
		t.Fatalf("result %+v", res)
	}
	if len(h.sent("thread/start")) != 0 {
		t.Fatalf("thread started despite unapproved hook")
	}
}

func TestStartValidation(t *testing.T) {
	h := newHarness(t, "basic")
	spec := h.spec()
	spec.Mode = "yolo"
	if _, err := h.adapter.Start(context.Background(), spec); !errors.Is(err, providers.ErrUnsupported) {
		t.Errorf("unknown mode: %v", err)
	}
	spec = h.spec()
	spec.Workdir = "relative"
	if _, err := h.adapter.Start(context.Background(), spec); err == nil {
		t.Errorf("relative workdir accepted")
	}
	spec = h.spec()
	spec.MCP.Command = ""
	if _, err := h.adapter.Start(context.Background(), spec); err == nil {
		t.Errorf("missing MCP bridge accepted")
	}
}

func TestProbe(t *testing.T) {
	h := newHarness(t, "probe")
	inst := h.adapter.Probe(context.Background())
	if inst.Provider != "codex" || inst.Version != "0.147.0" || !inst.Tested || inst.TestedVersion != TestedVersion || inst.Path != h.script {
		t.Fatalf("install %+v", inst)
	}
	if inst.AuthState != protocol.AuthReady || inst.Billing != protocol.BillingAPI {
		t.Fatalf("auth %q billing %q (%s)", inst.AuthState, inst.Billing, inst.AuthDetail)
	}
	if len(inst.Models) != 2 || inst.Models[0].ID != "gpt-a" || !inst.Models[0].Default || inst.Models[1].ID != "gpt-b" {
		t.Fatalf("models %+v", inst.Models)
	}
	c := inst.Capabilities
	if !c.StructuredEvents || !c.ToolApprovals || !c.SessionResume || !c.ActiveSteering || !c.ReadOnly || !c.MCPTools || !c.ModelEnumeration {
		t.Fatalf("capabilities %+v", c)
	}

	h = newHarness(t, "probe-signedout")
	inst = h.adapter.Probe(context.Background())
	if inst.AuthState != protocol.AuthNeedsSignIn || inst.Billing != protocol.BillingUnknown {
		t.Fatalf("signed-out probe %+v", inst)
	}

	a := NewAdapter(WithExecutable(filepath.Join(t.TempDir(), "missing")))
	if inst := a.Probe(context.Background()); inst.AuthState != protocol.AuthNotInstalled {
		t.Fatalf("missing executable: %+v", inst)
	}
}

func TestResetWait(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	at := func(d time.Duration) *int64 { v := now.Add(d).Unix(); return &v }
	reached := "rate_limit_reached"
	cases := []struct {
		name string
		snap *rateLimitSnapshot
		want time.Duration
	}{
		{"nil", nil, 0},
		{"not exhausted", &rateLimitSnapshot{Primary: &rateLimitWindow{UsedPercent: 80, ResetsAt: at(time.Hour)}}, 0},
		{"primary exhausted", &rateLimitSnapshot{Primary: &rateLimitWindow{UsedPercent: 100, ResetsAt: at(time.Hour)}, Secondary: &rateLimitWindow{UsedPercent: 50, ResetsAt: at(48 * time.Hour)}}, time.Hour},
		{"both exhausted", &rateLimitSnapshot{Primary: &rateLimitWindow{UsedPercent: 100, ResetsAt: at(time.Hour)}, Secondary: &rateLimitWindow{UsedPercent: 100, ResetsAt: at(48 * time.Hour)}}, 48 * time.Hour},
		{"exhausted without reset", &rateLimitSnapshot{Primary: &rateLimitWindow{UsedPercent: 100}}, 0},
		{"reached marker", &rateLimitSnapshot{Primary: &rateLimitWindow{UsedPercent: 97, ResetsAt: at(2 * time.Hour)}, RateLimitReachedType: &reached}, 2 * time.Hour},
	}
	for _, c := range cases {
		if got := resetWait(c.snap, now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestErrorInfoDecoding(t *testing.T) {
	cases := map[string]struct {
		kind   string
		status int
	}{
		`"usageLimitExceeded"`:                                   {"usageLimitExceeded", 0},
		`{"httpConnectionFailed":{"httpStatusCode":429}}`:        {"httpConnectionFailed", 429},
		`{"responseStreamDisconnected":{"httpStatusCode":null}}`: {"responseStreamDisconnected", 0},
		`null`: {"", 0},
	}
	for raw, want := range cases {
		k, st := errorInfo(json.RawMessage(raw))
		if k != want.kind || st != want.status {
			t.Errorf("%s: got %q %d", raw, k, st)
		}
	}
}

func TestTrustKeysLinkedWorktree(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	wt := filepath.Join(root, "wt")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644)
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644)
	keys := strings.Join(trustKeys(wt), "\n")
	for _, want := range []string{wt, main, canonical(main)} {
		if !strings.Contains(keys, want) {
			t.Errorf("trust keys %q missing %q", keys, want)
		}
	}
}

func TestExecPolicyRulesRefuseReadOnlyAndWarnInEdit(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	rule := filepath.Join(home, "rules", "default.rules")
	os.WriteFile(rule, []byte(`prefix_rule(pattern=["git"], decision="allow")`+"\n"), 0o644)

	// Read-only: Start refuses synchronously with ErrUnsupported.
	h := newHarness(t, "rules", "YIP_CODEX_FAKE_HOME="+home)
	spec := h.spec()
	spec.Mode = protocol.ModeReadOnly
	if _, err := h.adapter.Start(context.Background(), spec); !errors.Is(err, providers.ErrUnsupported) || !strings.Contains(err.Error(), "default.rules") {
		t.Fatalf("read-only with rules: %v", err)
	}
	if len(h.sent("thread/start")) != 0 || len(h.sent("account/read")) != 0 {
		t.Fatalf("nothing may run after the refusal")
	}

	// Edit: runs, with a visible warning naming the rule file.
	h = newHarness(t, "rules", "YIP_CODEX_FAKE_HOME="+home)
	s := h.start(h.spec())
	evs := collect(t, s, nil)
	if res := waitResult(t, s); res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("edit with rules: %+v", res)
	}
	if !hasText(evs, providers.EventWarning, rule) {
		t.Fatalf("expected a rules warning: %+v", evs)
	}

	// Read-only the owner allowed the rules in: runs, and still warns.
	h = newHarness(t, "rules-readonly", "YIP_CODEX_FAKE_HOME="+home)
	spec = h.spec()
	spec.Mode, spec.TrustProviderRules = protocol.ModeReadOnly, true
	s = h.start(spec)
	evs = collect(t, s, nil)
	if res := waitResult(t, s); res.Outcome != protocol.OutcomeSucceeded || !hasText(evs, providers.EventWarning, rule) {
		t.Fatalf("trusted read-only with rules: %+v %+v", res, evs)
	}

	// Probe reports read-only as unavailable and names the rule files.
	h = newHarness(t, "probe-rules", "YIP_CODEX_FAKE_HOME="+home)
	if inst := h.adapter.Probe(context.Background()); inst.Capabilities.ReadOnly || !strings.Contains(strings.Join(inst.Limitations, " "), "default.rules") ||
		len(inst.Capabilities.ExecPolicyRules) != 1 || inst.Capabilities.ExecPolicyRules[0] != rule {
		t.Fatalf("probe with rules: %+v", inst)
	}
}

func TestShownCommandDropsTheShellWrapper(t *testing.T) {
	for in, want := range map[string]string{
		`/bin/zsh -lc "go test ./..."`: "go test ./...",
		`bash -c 'git status --short'`: "git status --short",
		`go vet ./...`:                 "go vet ./...",
		`/bin/zsh -lc "a" && echo "b"`: `/bin/zsh -lc "a" && echo "b"`,
	} {
		if got := shownCommand(in); got != want {
			t.Errorf("shownCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
