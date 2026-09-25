package cursor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

func TestMain(m *testing.M) {
	if os.Getenv(envFake) == "1" {
		os.Exit(runFakeAgent())
	}
	os.Exit(m.Run())
}

// ---------- harness ----------

type harness struct {
	t       *testing.T
	a       *Adapter
	spec    providers.StartSpec
	logPath string
}

func fakeEnv(scenario, logPath string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		envFake + "=1",
		envScenario + "=" + scenario,
		envLog + "=" + logPath,
	}
}

func newHarness(t *testing.T, scenario, mode string) *harness {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "fake.log")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := newAdapter(WithExecutable(exe), WithTimeouts(15*time.Second, 2*time.Second), WithUserConfigDir(t.TempDir()))
	a.termGrace = time.Second
	a.eofWait = 500 * time.Millisecond
	a.authTimeout = 5 * time.Second
	return &harness{
		t: t,
		a: a,
		spec: providers.StartSpec{
			RunID:        "run-1",
			Workdir:      t.TempDir(),
			Mode:         mode,
			Instructions: "You are Ada, a careful backend engineer.",
			Prompt:       "Please do the task.",
			MCP:          providers.MCPServer{Name: "yip", Command: exe, Args: []string{"bridge", "--run", "run-1"}, Env: map[string]string{"YIP_RUN_TOKEN": "tok"}},
			Env:          fakeEnv(scenario, logPath),
		},
		logPath: logPath,
	}
}

func (h *harness) start() providers.Session {
	h.t.Helper()
	s, err := h.a.Start(context.Background(), h.spec)
	if err != nil {
		h.t.Fatalf("Start: %v", err)
	}
	return s
}

type recorder struct {
	mu     sync.Mutex
	events []providers.Event
	done   chan struct{}
}

// drive consumes events, calling fn for each (fn may call session methods).
func drive(s providers.Session, fn func(providers.Event)) *recorder {
	r := &recorder{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for e := range s.Events() {
			r.mu.Lock()
			r.events = append(r.events, e)
			r.mu.Unlock()
			if fn != nil {
				fn(e)
			}
		}
	}()
	return r
}

func (r *recorder) wait(t *testing.T) []providers.Event {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the event stream to close")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]providers.Event(nil), r.events...)
}

func waitResult(t *testing.T, s providers.Session) providers.Result {
	t.Helper()
	ch := make(chan providers.Result, 1)
	go func() { ch <- s.Wait() }()
	select {
	case r := <-ch:
		return r
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the session result")
	}
	return providers.Result{}
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

func hasText(evs []providers.Event, kind, sub string) bool {
	for _, e := range kinds(evs, kind) {
		if strings.Contains(e.Text, sub) {
			return true
		}
	}
	return false
}

type logEntry map[string]any

func readLog(t *testing.T, path string) []logEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fake log: %v", err)
	}
	defer f.Close()
	var out []logEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var m logEntry
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func requests(log []logEntry, method string) []logEntry {
	var out []logEntry
	for _, m := range log {
		if m["method"] == method {
			out = append(out, m)
		}
	}
	return out
}

func notes(log []logEntry, key string) []any {
	var out []any
	for _, m := range log {
		if m["fake"] == true {
			if v, ok := m[key]; ok {
				out = append(out, v)
			}
		}
	}
	return out
}

func params(m logEntry) map[string]any {
	p, _ := m["params"].(map[string]any)
	return p
}

// ---------- tests ----------

func TestBasicTurnStreamsAndCompletes(t *testing.T) {
	h := newHarness(t, "basic", protocol.ModeEdit)
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	evs := rec.wait(t)

	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome = %q (%s), want succeeded", res.Outcome, res.Error)
	}
	if !res.ExitConfirmed {
		t.Error("ExitConfirmed = false, want true")
	}
	if res.VendorSessionID != "sess-new-1" {
		t.Errorf("VendorSessionID = %q", res.VendorSessionID)
	}
	if res.FinalText != "Hello world" {
		t.Errorf("FinalText = %q, want %q", res.FinalText, "Hello world")
	}
	if vs := kinds(evs, providers.EventVendorSession); len(vs) != 1 || vs[0].VendorSessionID != "sess-new-1" {
		t.Errorf("vendor session events = %+v", vs)
	}
	var deltas strings.Builder
	for _, e := range kinds(evs, providers.EventMessageDelta) {
		deltas.WriteString(e.Text)
	}
	if deltas.String() != "Looking at the repo.Hello world" {
		t.Errorf("deltas = %q", deltas.String())
	}
	msgs := kinds(evs, providers.EventMessage)
	if len(msgs) != 2 || msgs[0].Text != "Looking at the repo." || msgs[1].Text != "Hello world" {
		t.Errorf("messages = %+v", msgs)
	}
	if st := kinds(evs, providers.EventToolStarted); len(st) != 1 || st[0].Text != "Read README.md" || st[0].Tool != "read" {
		t.Errorf("tool started = %+v", st)
	}
	if fin := kinds(evs, providers.EventToolFinished); len(fin) != 1 || fin[0].Text != "Read README.md" {
		t.Errorf("tool finished = %+v", fin)
	}
	if !hasText(evs, providers.EventStatus, "Plan: 0/2 steps done; now: Read the README") {
		t.Error("missing plan status")
	}
	if !hasText(evs, providers.EventStatus, "Todos: 1/2 done") {
		t.Error("missing todos status")
	}
	if res.Usage == nil || res.Usage.CostUSD == nil || *res.Usage.CostUSD != 0.12 || res.Usage.InputTokens == nil || *res.Usage.InputTokens != 20 || *res.Usage.OutputTokens != 10 {
		t.Errorf("usage = %+v", res.Usage)
	} else if res.Usage.Source != "vendor" || res.Usage.Billing != protocol.BillingUnknown {
		t.Errorf("usage labels = %+v", res.Usage)
	}
	if len(kinds(evs, providers.EventUsage)) != 2 {
		t.Errorf("usage events = %d, want 2", len(kinds(evs, providers.EventUsage)))
	}

	log := readLog(t, h.logPath)
	init := requests(log, "initialize")
	if len(init) != 1 {
		t.Fatalf("initialize requests = %d", len(init))
	}
	ip := params(init[0])
	if ip["protocolVersion"] != float64(1) {
		t.Errorf("protocolVersion = %v", ip["protocolVersion"])
	}
	caps, _ := json.Marshal(ip["clientCapabilities"])
	if string(caps) != `{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false}` {
		t.Errorf("clientCapabilities = %s", caps)
	}
	if a := requests(log, "authenticate"); len(a) != 1 || params(a[0])["methodId"] != "cursor_login" {
		t.Errorf("authenticate = %+v", a)
	}
	news := requests(log, "session/new")
	if len(news) != 1 {
		t.Fatalf("session/new requests = %d", len(news))
	}
	np := params(news[0])
	if np["cwd"] != h.spec.Workdir {
		t.Errorf("cwd = %v, want %s", np["cwd"], h.spec.Workdir)
	}
	servers, _ := json.Marshal(np["mcpServers"])
	exe, _ := os.Executable()
	want, _ := json.Marshal([]map[string]any{{"name": "yip", "command": exe, "args": []string{"bridge", "--run", "run-1"}, "env": []map[string]string{{"name": "YIP_RUN_TOKEN", "value": "tok"}}}})
	if string(servers) != string(want) {
		t.Errorf("mcpServers = %s\nwant %s", servers, want)
	}
	prompts := requests(log, "session/prompt")
	if len(prompts) != 1 {
		t.Fatalf("prompts = %d", len(prompts))
	}
	text := promptText(params(prompts[0]))
	if !strings.HasPrefix(text, "<yip_instructions>") || !strings.Contains(text, "You are Ada") || !strings.HasSuffix(text, "Please do the task.") {
		t.Errorf("first prompt did not carry delimited instructions:\n%s", text)
	}
	if len(requests(log, "session/set_config_option")) != 0 {
		t.Error("edit mode already in agent mode: no set_config_option expected")
	}
}

func TestPermissionsAllowRejectAndOutsideWorkspace(t *testing.T) {
	h := newHarness(t, "permissions", protocol.ModeEdit)
	s := h.start()
	var approvals []*providers.ApprovalRequest
	var mu sync.Mutex
	rec := drive(s, func(e providers.Event) {
		if e.Kind != providers.EventApprovalRequest {
			return
		}
		mu.Lock()
		approvals = append(approvals, e.Approval)
		mu.Unlock()
		// Allow the edit inside the workspace, reject the rm -rf.
		allow := e.Approval.Action.Kind == "edit"
		if err := s.ResolveApproval(context.Background(), e.Approval.ID, providers.ApprovalDecision{Allow: allow}); err != nil {
			t.Errorf("ResolveApproval: %v", err)
		}
	})
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome = %q (%s)", res.Outcome, res.Error)
	}
	if len(approvals) != 2 {
		t.Fatalf("approval requests = %d, want 2 (the out-of-workspace edit is denied without asking)", len(approvals))
	}
	edit, exec := approvals[0].Action, approvals[1].Action
	if edit.Kind != "edit" || edit.Summary != "Edit main.go" || !strings.HasSuffix(edit.Target, "/main.go") || !strings.Contains(edit.Detail, "1 → 3 lines") {
		t.Errorf("edit action = %+v", edit)
	}
	if exec.Kind != "exec" || exec.Command != "rm -rf build" || exec.Summary != "Run rm -rf build" {
		t.Errorf("exec action = %+v", exec)
	}
	if len(approvals[0].Raw) == 0 {
		t.Error("approval Raw payload missing")
	}
	if !hasText(evs, providers.EventWarning, "outside the workspace (/etc/hosts)") {
		t.Error("missing warning for out-of-workspace edit")
	}
	if err := s.ResolveApproval(context.Background(), approvals[0].ID, providers.ApprovalDecision{Allow: true}); err == nil {
		t.Error("resolving an already-resolved approval should fail")
	}
	// FinalText is the assistant text after the last tool call of the last turn.
	if res.FinalText != "perm3=reject-once\n\n\ndone with permissions" {
		t.Errorf("FinalText = %q", res.FinalText)
	}
	var all strings.Builder
	for _, e := range kinds(evs, providers.EventMessageDelta) {
		all.WriteString(e.Text)
	}
	for _, want := range []string{"perm1=allow-once", "perm2=reject-once", "perm3=reject-once"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("agent did not receive %s; transcript %q", want, all.String())
		}
	}
	if !hasText(evs, providers.EventToolFinished, "Run rm -rf build (failed)") {
		t.Error("missing failed tool finish")
	}
}

func TestQuestionsAnswerDeclineAndUnknownRequests(t *testing.T) {
	h := newHarness(t, "questions", protocol.ModeEdit)
	s := h.start()
	var qs []*providers.Question
	rec := drive(s, func(e providers.Event) {
		if e.Kind != providers.EventQuestion {
			return
		}
		qs = append(qs, e.Question)
		var a providers.QuestionAnswer
		switch len(qs) {
		case 1:
			a = providers.QuestionAnswer{Selected: 1} // SQLite
		case 2:
			a = providers.QuestionAnswer{Selected: -1, Declined: true}
		case 3:
			a = providers.QuestionAnswer{Selected: -1, Declined: true}
		}
		if err := s.AnswerQuestion(context.Background(), e.Question.ID, a); err != nil {
			t.Errorf("AnswerQuestion: %v", err)
		}
	})
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome = %q (%s)", res.Outcome, res.Error)
	}
	if len(qs) != 3 {
		t.Fatalf("questions = %d, want 3", len(qs))
	}
	if qs[0].Kind != "ask" || !strings.Contains(qs[0].Text, "Which database?") || strings.Join(qs[0].Options, "|") != "Postgres|SQLite" {
		t.Errorf("ask question = %+v", qs[0])
	}
	if qs[2].Kind != "plan" || !strings.Contains(qs[2].Text, "Refactor tabs") || !strings.Contains(qs[2].Text, "1. Inspect") || len(qs[2].Options) != 2 {
		t.Errorf("plan question = %+v", qs[2])
	}
	log := readLog(t, h.logPath)
	check := func(key, want string) {
		t.Helper()
		v := notes(log, key)
		if len(v) != 1 {
			t.Fatalf("%s notes = %v", key, v)
		}
		b, _ := json.Marshal(v[0])
		if string(b) != want {
			t.Errorf("%s = %s\nwant %s", key, b, want)
		}
	}
	check("askReply", `{"outcome":{"answers":[{"questionId":"q1","selectedOptionIds":["sqlite"]}],"outcome":"answered"}}`)
	check("ask2Reply", `{"outcome":{"outcome":"skipped","reason":"`+declinedAskReason+`"}}`)
	check("planReply", `{"outcome":{"outcome":"rejected","reason":"`+declinedPlanReason+`"}}`)
	check("unknownReplyCode", `-32601`)
	check("fsReplyCode", `-32601`)
	if !hasText(evs, providers.EventWarning, "cursor/some_future_blocking_method") {
		t.Error("unknown request should be surfaced as a warning")
	}
	if !hasText(evs, providers.EventStatus, "Subagent task: Explore codebase") {
		t.Error("missing cursor/task status")
	}
}

func TestQueuedSteering(t *testing.T) {
	h := newHarness(t, "steer", protocol.ModeEdit)
	s := h.start()
	var mode string
	var sendErr error
	rec := drive(s, func(e providers.Event) {
		if e.Kind == providers.EventApprovalRequest {
			mode, sendErr = s.SendInput(context.Background(), "also update the changelog")
			_ = s.ResolveApproval(context.Background(), e.Approval.ID, providers.ApprovalDecision{Allow: true})
		}
	})
	res := waitResult(t, s)
	evs := rec.wait(t)
	if sendErr != nil || mode != "queued" {
		t.Fatalf("SendInput = %q, %v; want queued", mode, sendErr)
	}
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("outcome = %q (%s)", res.Outcome, res.Error)
	}
	if res.FinalText != "steered: also update the changelog" {
		t.Errorf("FinalText = %q", res.FinalText)
	}
	if !hasText(evs, protocol.RunEvInputDelivered, "also update the changelog") {
		t.Error("missing input_delivered event")
	}
	prompts := requests(readLog(t, h.logPath), "session/prompt")
	if len(prompts) != 2 || promptText(params(prompts[1])) != "also update the changelog" {
		t.Errorf("prompts = %+v", prompts)
	}
	if _, err := s.SendInput(context.Background(), "late"); err == nil {
		t.Error("SendInput after the session ended should fail")
	}
}

func TestResumeWithSessionLoad(t *testing.T) {
	h := newHarness(t, "resume", protocol.ModeEdit)
	h.spec.ResumeSessionID = "sess-old"
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeSucceeded || res.VendorSessionID != "sess-old" {
		t.Fatalf("result = %+v", res)
	}
	for _, e := range evs {
		if strings.Contains(e.Text, "OLD HISTORY") || e.Text == "Old tool" {
			t.Errorf("replayed history leaked as event: %+v", e)
		}
	}
	if res.FinalText != "resumed ok in sess-old" {
		t.Errorf("FinalText = %q", res.FinalText)
	}
	log := readLog(t, h.logPath)
	if len(requests(log, "session/load")) != 1 || len(requests(log, "session/new")) != 0 {
		t.Error("expected exactly one session/load and no session/new")
	}
	if len(kinds(evs, providers.EventWarning)) != 0 {
		t.Errorf("unexpected warnings: %+v", kinds(evs, providers.EventWarning))
	}
}

func TestResumeUnknownSessionStartsFresh(t *testing.T) {
	h := newHarness(t, "resume", protocol.ModeEdit)
	h.spec.ResumeSessionID = "sess-missing"
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeSucceeded || res.VendorSessionID != "sess-new-1" {
		t.Fatalf("result = %+v", res)
	}
	if !hasText(evs, providers.EventWarning, "Could not resume Cursor session sess-missing") {
		t.Error("missing resume fallback warning")
	}
}

func TestResumeUnavailableWarns(t *testing.T) {
	h := newHarness(t, "noload", protocol.ModeEdit)
	h.spec.ResumeSessionID = "sess-old"
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeSucceeded || res.VendorSessionID != "sess-new-1" {
		t.Fatalf("result = %+v", res)
	}
	if !hasText(evs, providers.EventWarning, "did not advertise session/load") {
		t.Error("missing resume-unavailable warning")
	}
	if len(requests(readLog(t, h.logPath), "session/load")) != 0 {
		t.Error("session/load must not be called when loadSession is not advertised")
	}
}

func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TestCancelMidTurnTerminatesProcessGroup(t *testing.T) {
	h := newHarness(t, "cancel", protocol.ModeEdit)
	s := h.start()
	var cancelErr error
	cancelled := make(chan struct{})
	rec := drive(s, func(e providers.Event) {
		if e.Kind == providers.EventApprovalRequest {
			go func() {
				cancelErr = s.Cancel(context.Background())
				close(cancelled)
			}()
		}
	})
	res := waitResult(t, s)
	rec.wait(t)
	<-cancelled
	if cancelErr != nil {
		t.Fatalf("Cancel: %v", cancelErr)
	}
	if res.Outcome != protocol.OutcomeCancelled {
		t.Fatalf("outcome = %q (%s)", res.Outcome, res.Error)
	}
	if !res.ExitConfirmed {
		t.Error("ExitConfirmed = false")
	}
	log := readLog(t, h.logPath)
	if len(requests(log, "session/cancel")) != 1 {
		t.Error("session/cancel notification not sent")
	}
	if v := notes(log, "cancelPermReply"); len(v) != 1 || v[0] != "cancelled" {
		t.Errorf("pending permission reply = %v, want cancelled outcome", v)
	}
	pids := notes(log, "childPid")
	if len(pids) != 1 {
		t.Fatalf("child pid notes = %v", pids)
	}
	pid := int(pids[0].(float64))
	deadline := time.Now().Add(3 * time.Second)
	for pidAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pidAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("child process %d in the agent's process group survived cancel", pid)
	}
}

func TestCancelEscalatesWhenAgentIgnoresIt(t *testing.T) {
	h := newHarness(t, "cancel-hang", protocol.ModeEdit)
	h.a.cancelGrace = 300 * time.Millisecond
	s := h.start()
	started := make(chan struct{}, 1)
	rec := drive(s, func(e providers.Event) {
		if e.Kind == providers.EventMessageDelta {
			select {
			case started <- struct{}{}:
			default:
			}
		}
	})
	<-started
	t0 := time.Now()
	if err := s.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	res := waitResult(t, s)
	rec.wait(t)
	if res.Outcome != protocol.OutcomeCancelled || !res.ExitConfirmed {
		t.Fatalf("result = %+v", res)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Errorf("cancel took %s", d)
	}
	pids := notes(readLog(t, h.logPath), "childPid")
	if len(pids) == 1 {
		pid := int(pids[0].(float64))
		time.Sleep(200 * time.Millisecond)
		if pidAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d survived", pid)
		}
	}
}

func TestRateLimitOutcome(t *testing.T) {
	h := newHarness(t, "ratelimit", protocol.ModeEdit)
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeRateLimited {
		t.Fatalf("outcome = %q (%s)", res.Outcome, res.Error)
	}
	if res.RetryAfter != 90*time.Second {
		t.Errorf("RetryAfter = %s, want 90s (vendor-provided)", res.RetryAfter)
	}
	rl := kinds(evs, providers.EventRateLimited)
	if len(rl) != 1 || rl[0].RetryAfter != 90*time.Second {
		t.Errorf("rate limited events = %+v", rl)
	}
	if !res.ExitConfirmed {
		t.Error("ExitConfirmed = false")
	}
}

func TestAuthRequiredAtAuthenticate(t *testing.T) {
	h := newHarness(t, "auth", protocol.ModeEdit)
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeAuthRequired {
		t.Fatalf("outcome = %q (%s)", res.Outcome, res.Error)
	}
	if len(kinds(evs, providers.EventAuthRequired)) != 1 || !strings.Contains(res.Error, "agent login") {
		t.Errorf("auth events/error: %+v / %q", kinds(evs, providers.EventAuthRequired), res.Error)
	}
	if len(requests(readLog(t, h.logPath), "session/new")) != 0 {
		t.Error("no session should be created when authentication fails")
	}
	if !res.ExitConfirmed {
		t.Error("ExitConfirmed = false")
	}
}

func TestAuthRequiredAtPrompt(t *testing.T) {
	h := newHarness(t, "auth-prompt", protocol.ModeEdit)
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	rec.wait(t)
	if res.Outcome != protocol.OutcomeAuthRequired {
		t.Fatalf("outcome = %q (%s)", res.Outcome, res.Error)
	}
}

func TestCrashReportsStderrTail(t *testing.T) {
	h := newHarness(t, "crash", protocol.ModeEdit)
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeFailed {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if !strings.Contains(res.Error, "renderer crashed (simulated)") || !strings.Contains(res.Error, "code 3") {
		t.Errorf("error = %q", res.Error)
	}
	if !hasText(evs, providers.EventError, "renderer crashed") {
		t.Error("missing error event")
	}
	if res.FinalText != "partial output" {
		t.Errorf("partial transcript lost: FinalText = %q", res.FinalText)
	}
	if !res.ExitConfirmed {
		t.Error("ExitConfirmed = false")
	}
}

func TestReadOnlyUsesAskModeAndDeniesWrites(t *testing.T) {
	for _, mode := range []string{protocol.ModeReadOnly, protocol.ModeConversation} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, "readonly", mode)
			s := h.start()
			var approvals []*providers.ApprovalRequest
			rec := drive(s, func(e providers.Event) {
				if e.Kind == providers.EventApprovalRequest {
					approvals = append(approvals, e.Approval)
					_ = s.ResolveApproval(context.Background(), e.Approval.ID, providers.ApprovalDecision{Allow: true})
				}
			})
			res := waitResult(t, s)
			evs := rec.wait(t)
			if res.Outcome != protocol.OutcomeSucceeded {
				t.Fatalf("outcome = %q (%s)", res.Outcome, res.Error)
			}
			log := readLog(t, h.logPath)
			sets := requests(log, "session/set_config_option")
			if len(sets) < 2 {
				t.Fatalf("set_config_option calls = %d, want initial + re-assert", len(sets))
			}
			for _, m := range sets {
				if p := params(m); p["configId"] != "mode" || p["value"] != "ask" {
					t.Errorf("unexpected set_config_option %v", p)
				}
			}
			if v := notes(log, "modeAtPrompt"); len(v) != 1 || v[0] != "ask" {
				t.Errorf("mode at first prompt = %v, want ask", v)
			}
			if v := notes(log, "modeAfterReassert"); len(v) != 1 || v[0] != "ask" {
				t.Errorf("mode after agent switched itself = %v, want ask", v)
			}
			if len(approvals) != 1 || approvals[0].Action.Kind != "network" || approvals[0].Action.Target != "https://example.com/docs" {
				t.Errorf("approvals = %+v (only the fetch should reach the runner)", approvals)
			}
			var tr strings.Builder
			for _, e := range kinds(evs, providers.EventMessageDelta) {
				tr.WriteString(e.Text)
			}
			if !strings.Contains(tr.String(), "ro-edit=reject-once") || !strings.Contains(tr.String(), "ro-fetch=allow-once") {
				t.Errorf("transcript = %q", tr.String())
			}
			if !hasText(evs, providers.EventWarning, "Denied Cursor edit request") || !hasText(evs, providers.EventWarning, "switching it back") {
				t.Errorf("warnings = %+v", kinds(evs, providers.EventWarning))
			}
			text := promptText(params(requests(log, "session/prompt")[0]))
			if !strings.Contains(text, "Do not create, modify") && !strings.Contains(text, "Do not modify files") {
				t.Errorf("read-only note missing from prompt: %s", text)
			}
		})
	}
}

func TestReadOnlyRefusedWithoutAskMode(t *testing.T) {
	h := newHarness(t, "nomodes", protocol.ModeReadOnly)
	_, err := h.a.Start(context.Background(), h.spec)
	if !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("Start err = %v, want ErrUnsupported", err)
	}
	if len(requests(readLog(t, h.logPath), "session/prompt")) != 0 {
		t.Error("no prompt may be sent when read-only cannot be enforced")
	}
	// Edit mode still works without mode control.
	h2 := newHarness(t, "nomodes", protocol.ModeEdit)
	s := h2.start()
	rec := drive(s, nil)
	if res := waitResult(t, s); res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("edit outcome = %+v", res)
	}
	rec.wait(t)
}

func TestReadOnlyViolationStopsRun(t *testing.T) {
	h := newHarness(t, "readonly-violation", protocol.ModeReadOnly)
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	rec.wait(t)
	if res.Outcome != protocol.OutcomeFailed || !strings.Contains(res.Error, "Read-only boundary violated") {
		t.Fatalf("result = %+v", res)
	}
	if !res.ExitConfirmed {
		t.Error("ExitConfirmed = false")
	}
}

func TestModelSelection(t *testing.T) {
	h := newHarness(t, "model", protocol.ModeEdit)
	h.spec.Model = "gpt-5"
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	rec.wait(t)
	if res.Outcome != protocol.OutcomeSucceeded || res.FinalText != "model=gpt-5[fast=true]" {
		t.Fatalf("result = %+v", res)
	}
	h2 := newHarness(t, "model", protocol.ModeEdit)
	h2.spec.Model = "no-such-model"
	_, err := h2.a.Start(context.Background(), h2.spec)
	if !errors.Is(err, providers.ErrUnsupported) || !strings.Contains(err.Error(), "sonnet-4") {
		t.Fatalf("unknown model err = %v", err)
	}
}

func TestUnapprovedProjectConfigRefused(t *testing.T) {
	h := newHarness(t, "basic", protocol.ModeEdit)
	if err := os.MkdirAll(filepath.Join(h.spec.Workdir, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.spec.Workdir, ".cursor", "hooks.json"), []byte(`{"version":1,"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.a.Start(context.Background(), h.spec)
	if !errors.Is(err, ErrUnapprovedProjectConfig) || !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("Start err = %v", err)
	}
	if _, statErr := os.Stat(h.logPath); statErr == nil {
		t.Error("Cursor must not be launched when repository config is unapproved")
	}
	// Owner approval lets it run.
	WithApprovedProjectConfig(".cursor/hooks.json")(h.a)
	s := h.start()
	rec := drive(s, nil)
	if res := waitResult(t, s); res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("result = %+v", res)
	}
	if !hasText(rec.wait(t), providers.EventWarning, "owner-approved") {
		t.Error("missing approved-config note")
	}
}

func TestProbe(t *testing.T) {
	exe, _ := os.Executable()
	logPath := filepath.Join(t.TempDir(), "probe.log")
	a := New(WithExecutable(exe), WithProbeEnv(fakeEnv("basic", logPath)), WithUserConfigDir(t.TempDir()))
	inst := a.Probe(context.Background())
	if inst.Provider != "cursor" || inst.Path != exe || inst.Version != "2026.09.20-fake" {
		t.Errorf("identity = %+v", inst)
	}
	if inst.AuthState != protocol.AuthReady || inst.Account != "dev@example.com" {
		t.Errorf("auth = %q %q %q", inst.AuthState, inst.Account, inst.AuthDetail)
	}
	if inst.Billing != protocol.BillingUnknown || inst.Tested || inst.TestedVersion != "" {
		t.Errorf("billing/tested = %+v", inst)
	}
	c := inst.Capabilities
	if !c.StructuredEvents || !c.ToolApprovals || !c.UserQuestions || !c.SessionResume || c.ActiveSteering || !c.ReadOnly || !c.ModelEnumeration || !c.MCPTools {
		t.Errorf("capabilities = %+v", c)
	}
	if len(inst.Models) != 3 || inst.Models[0].ID != "auto" || !inst.Models[0].Default || inst.Models[2].Label != "Claude Sonnet 4 Thinking" {
		t.Errorf("models = %+v", inst.Models)
	}
	found := false
	for _, l := range inst.Limitations {
		if strings.Contains(l, "Not yet exercised against a real Cursor") {
			found = true
		}
	}
	if !found {
		t.Errorf("limitations = %v", inst.Limitations)
	}
	// Probe must not create a session or send a prompt.
	log := readLog(t, logPath)
	if len(requests(log, "initialize")) != 1 || len(requests(log, "session/new")) != 0 || len(requests(log, "session/prompt")) != 0 || len(requests(log, "authenticate")) != 0 {
		t.Errorf("probe traffic = %v", log)
	}
}

func TestProbeSignedOutAndUserConfig(t *testing.T) {
	exe, _ := os.Executable()
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "cli-config.json"), []byte(`{"version":1,"approvalMode":"unrestricted","permissions":{"allow":["Shell(ls)"],"deny":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(WithExecutable(exe), WithProbeEnv(fakeEnv("probe-signedout", filepath.Join(t.TempDir(), "p.log"))), WithUserConfigDir(cfgDir))
	inst := a.Probe(context.Background())
	if inst.AuthState != protocol.AuthNeedsSignIn || !strings.Contains(inst.AuthDetail, "agent login") {
		t.Errorf("auth = %q %q", inst.AuthState, inst.AuthDetail)
	}
	if inst.Capabilities.ToolApprovals {
		t.Error("ToolApprovals must be false when Cursor's approvalMode is unrestricted")
	}
	joined := strings.Join(inst.Limitations, "\n")
	if !strings.Contains(joined, "unrestricted") || !strings.Contains(joined, "pre-approves 1 permission rule") {
		t.Errorf("limitations = %s", joined)
	}
}

func TestProbeNotInstalled(t *testing.T) {
	a := New(WithExecutable(filepath.Join(t.TempDir(), "agent")))
	inst := a.Probe(context.Background())
	if inst.AuthState != protocol.AuthNotInstalled || !strings.Contains(inst.AuthDetail, "cursor.com/install") {
		t.Errorf("inst = %+v", inst)
	}
	if inst.Tested || len(inst.Limitations) == 0 {
		t.Errorf("tested/limitations = %v %v", inst.Tested, inst.Limitations)
	}
}

func TestStartRejectsBadSpec(t *testing.T) {
	a := New()
	if _, err := a.Start(context.Background(), providers.StartSpec{Workdir: t.TempDir(), Mode: "yolo"}); !errors.Is(err, providers.ErrUnsupported) {
		t.Errorf("bad mode err = %v", err)
	}
	if _, err := a.Start(context.Background(), providers.StartSpec{Workdir: "relative", Mode: protocol.ModeEdit}); err == nil {
		t.Error("relative workdir accepted")
	}
}

func TestAgentWithdrawsPendingApproval(t *testing.T) {
	h := newHarness(t, "withdraw", protocol.ModeEdit)
	s := h.start()
	var ids []string
	rec := drive(s, func(e providers.Event) {
		if e.Kind == providers.EventApprovalRequest {
			ids = append(ids, e.Approval.ID) // a human is still deciding
		}
	})
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("result = %+v", res)
	}
	if len(ids) != 1 {
		t.Fatalf("approvals = %v", ids)
	}
	var withdrawn bool
	for _, e := range kinds(evs, providers.EventWarning) {
		if strings.Contains(e.Text, "withdrew pending approval "+ids[0]) && strings.Contains(string(e.Data), ids[0]) {
			withdrawn = true
		}
	}
	if !withdrawn {
		t.Errorf("missing withdrawal warning: %+v", kinds(evs, providers.EventWarning))
	}
	if v := notes(readLog(t, h.logPath), "withdrawReply"); len(v) != 1 || v[0] != "error:-32800" {
		t.Errorf("withdrawn request reply = %v, want error:-32800", v)
	}
	if err := s.ResolveApproval(context.Background(), ids[0], providers.ApprovalDecision{Allow: true}); err == nil {
		t.Error("resolving a withdrawn approval must fail")
	}
}

func TestHandshakeTimeoutIsVisibleFailure(t *testing.T) {
	h := newHarness(t, "hang-new", protocol.ModeEdit)
	h.a.handshakeTimeout = 500 * time.Millisecond
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	evs := rec.wait(t)
	if res.Outcome != protocol.OutcomeFailed || !strings.Contains(res.Error, "session/new") || !res.ExitConfirmed {
		t.Fatalf("result = %+v", res)
	}
	if len(kinds(evs, providers.EventError)) != 1 {
		t.Errorf("error events = %+v", kinds(evs, providers.EventError))
	}
}

func TestStartCallerCancelledReturnsError(t *testing.T) {
	h := newHarness(t, "hang-new", protocol.ModeEdit)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := h.a.Start(ctx, h.spec); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start err = %v", err)
	}
}

func TestResumeWithSessionResume(t *testing.T) {
	h := newHarness(t, "sresume", protocol.ModeEdit)
	h.spec.ResumeSessionID = "sess-old"
	s := h.start()
	rec := drive(s, nil)
	res := waitResult(t, s)
	rec.wait(t)
	if res.Outcome != protocol.OutcomeSucceeded || res.VendorSessionID != "sess-old" || res.FinalText != "resumed ok in sess-old" {
		t.Fatalf("result = %+v", res)
	}
	log := readLog(t, h.logPath)
	if len(requests(log, "session/resume")) != 1 || len(requests(log, "session/load")) != 0 || len(requests(log, "session/new")) != 0 {
		t.Error("expected session/resume only")
	}
}
