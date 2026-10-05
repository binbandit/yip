package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/internal/skills"
	"github.com/binbandit/yip/internal/skilltest"
	"github.com/binbandit/yip/protocol"
)

// The helper is a real process with private stdio. No installed provider,
// container runtime or credentials are required to exercise the transport.
func TestWorkerProcess(t *testing.T) {
	if os.Getenv("YIP_WORKER_TEST") != "1" {
		return
	}
	switch os.Getenv("YIP_WORKER_SCENARIO") {
	case "malformed":
		fmt.Fprintln(os.Stdout, "not JSON")
		os.Exit(0)
	case "silent":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "request-flood":
		conn := acp.NewConn(os.Stdin, os.Stdout, dispatcher{request: func(r *acp.Request) {
			_ = r.Reply("immediate")
			if r.Method == "session/sendInput" {
				for i := range maxRequests + 1 {
					fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":\"bridge/call\",\"params\":{\"tool\":\"blocked\"}}\n", i)
				}
			}
		}})
		_ = conn.Serve()
		os.Exit(0)
	case "result-crash":
		var conn *acp.Conn
		conn = acp.NewConn(os.Stdin, os.Stdout, dispatcher{request: func(r *acp.Request) {
			_ = r.Reply(struct{}{})
			_ = conn.Notify("session/result", providers.Result{Outcome: protocol.OutcomeSucceeded, ExitConfirmed: true})
			os.Exit(3)
		}})
		_ = conn.Serve()
		os.Exit(3)
	}
	if err := Serve(context.Background(), os.Stdin, os.Stdout, testAdapter{}, "test-version"); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func command(scenario string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerProcess$")
	cmd.Env = append(os.Environ(), "YIP_WORKER_TEST=1", "YIP_WORKER_SCENARIO="+scenario, "WORKER_LOCAL_ENV=present")
	return cmd
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type testAdapter struct{}

func (testAdapter) Name() string  { return "test" }
func (testAdapter) Label() string { return "Test" }
func (testAdapter) Probe(context.Context) protocol.ProviderInstallation {
	return protocol.ProviderInstallation{Provider: "test", Version: "1.2", AuthState: protocol.AuthReady}
}
func (testAdapter) Start(ctx context.Context, spec providers.StartSpec) (providers.Session, error) {
	if spec.Prompt == "fail" {
		return nil, errors.New("private-secret-must-not-leak")
	}
	if spec.Prompt == "unsupported" {
		return nil, providers.ErrUnsupported
	}
	if spec.MCP.Command != "/usr/local/bin/yip" || !reflect.DeepEqual(spec.MCP.Args, []string{"bridge", "--mode", spec.Mode}) {
		return nil, errors.New("incorrect MCP command")
	}
	if spec.Env == nil {
		return nil, errors.New("worker environment not initialized")
	}
	if spec.Prompt == "environment" && !contains(spec.Env, "WORKER_LOCAL_ENV=present") {
		return nil, errors.New("missing worker-local environment")
	}
	path := spec.MCP.Env[bridge.EnvSocket]
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 || !strings.HasPrefix(path, "/tmp/yip-worker-") {
		return nil, errors.New("bridge socket must be private")
	}
	if spec.Prompt == "startup-bridge" {
		client, err := bridge.Dial(path, spec.MCP.Env[bridge.EnvToken])
		if err != nil {
			return nil, err
		}
		resp := client.Call("startup", json.RawMessage(`{}`))
		_ = client.Close()
		if !resp.OK {
			return nil, errors.New("startup bridge failed")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &testSession{ctx: ctx, cancel: cancel, events: make(chan providers.Event, 16), done: make(chan struct{}), approval: make(chan struct{}), question: make(chan struct{}), flood: make(chan struct{})}
	go s.run(spec)
	return s, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type testSession struct {
	ctx                      context.Context
	cancel                   context.CancelFunc
	events                   chan providers.Event
	done, approval, question chan struct{}
	flood                    chan struct{}
	approveOnce, answerOnce  sync.Once
	result                   providers.Result
}

func (s *testSession) run(spec providers.StartSpec) {
	defer close(s.done)
	defer close(s.events)
	s.result = providers.Result{Outcome: protocol.OutcomeSucceeded, FinalText: "final", ExitConfirmed: true}
	switch spec.Prompt {
	case "crash":
		time.Sleep(100 * time.Millisecond)
		os.Exit(3)
	case "hang":
		<-s.ctx.Done()
		s.result.Outcome = protocol.OutcomeCancelled
	case "process", "flood-count", "flood-bytes":
		cmd := exec.Command("sh", "-c", `trap 'kill "$child" 2>/dev/null; wait "$child"; exit 0' TERM; sleep 60 & child=$!; echo "$child"; wait "$child"`)
		childPID, childOut, err := os.Pipe()
		if err != nil {
			s.result.Error = err.Error()
			return
		}
		cmd.Stdout = childOut
		process, err := providers.StartProcess(cmd)
		_ = childOut.Close()
		if err != nil {
			_ = childPID.Close()
			s.result.Error = err.Error()
			return
		}
		var pid int
		_, _ = fmt.Fscanln(childPID, &pid)
		_ = childPID.Close()
		s.events <- providers.Event{Kind: providers.EventStatus, Text: strconv.Itoa(pid)}
		if strings.HasPrefix(spec.Prompt, "flood-") {
			select {
			case <-s.flood:
				count, text := maxQueuedEvents+1, "x"
				if spec.Prompt == "flood-bytes" {
					count, text = 5, strings.Repeat("x", 8<<20)
				}
			flood:
				for range count {
					select {
					case s.events <- providers.Event{Kind: providers.EventMessageDelta, Text: text}:
					case <-s.ctx.Done():
						break flood
					}
				}
			case <-s.ctx.Done():
			}
		}
		<-s.ctx.Done()
		s.result.Outcome = protocol.OutcomeCancelled
		s.result.ExitConfirmed = process.Terminate(time.Second)
	case "skills":
		// The worker replaces the host command with its container command; run
		// that same stdio bridge from this test binary without requiring Docker.
		spec.MCP.Command, spec.MCP.Args = os.Args[0], nil
		spec.MCP.Env["YIP_SKILL_TEST_BRIDGE"] = spec.Mode
		if !strings.Contains(spec.Instructions, skills.Revision) {
			s.result.Error = "missing skill catalog"
			return
		}
		if err := skilltest.Verify(spec.MCP); err != nil {
			s.result.Error = err.Error()
		}
	case "interactive":
		client, err := bridge.Dial(spec.MCP.Env[bridge.EnvSocket], spec.MCP.Env[bridge.EnvToken])
		if err != nil {
			s.result.Error = err.Error()
			return
		}
		defer client.Close()
		responses := make(chan bridge.LocalResponse, 2)
		go func() { responses <- client.Call("permission", json.RawMessage(`{"command":"test"}`)) }()
		go func() { responses <- client.Call("parallel", json.RawMessage(`{}`)) }()
		s.events <- providers.Event{Kind: providers.EventApprovalRequest, Approval: &providers.ApprovalRequest{ID: "approval"}}
		s.events <- providers.Event{Kind: providers.EventQuestion, Question: &providers.Question{ID: "question"}}
		for range 2 {
			select {
			case resp := <-responses:
				if !resp.OK {
					s.result.Error = "bridge failed"
					return
				}
			case <-s.ctx.Done():
				s.result.Outcome = protocol.OutcomeCancelled
				return
			}
		}
		select {
		case <-s.approval:
		case <-s.ctx.Done():
			return
		}
		select {
		case <-s.question:
		case <-s.ctx.Done():
			return
		}
	default:
		for i := range 300 {
			select {
			case s.events <- providers.Event{Kind: providers.EventMessageDelta, Text: strconv.Itoa(i)}:
			case <-s.ctx.Done():
				s.result.Outcome = protocol.OutcomeCancelled
				return
			}
		}
		s.events <- providers.Event{Kind: providers.EventMessage, Text: strings.Repeat("x", 256<<10)}
	}
}
func (s *testSession) Events() <-chan providers.Event { return s.events }
func (s *testSession) Wait() providers.Result         { <-s.done; return s.result }
func (s *testSession) SendInput(_ context.Context, text string) (string, error) {
	if text == "flood" {
		close(s.flood)
	}
	if text == "unsupported" {
		return "", providers.ErrUnsupported
	}
	return "immediate", nil
}
func (s *testSession) ResolveApproval(_ context.Context, id string, d providers.ApprovalDecision) error {
	if id != "approval" || !d.Allow {
		return errors.New("wrong approval")
	}
	s.approveOnce.Do(func() { close(s.approval) })
	return nil
}
func (s *testSession) AnswerQuestion(_ context.Context, id string, a providers.QuestionAnswer) error {
	if id != "question" || a.Text != "answer" {
		return errors.New("wrong answer")
	}
	s.answerOnce.Do(func() { close(s.question) })
	return nil
}
func (s *testSession) Cancel(context.Context) error { s.cancel(); return nil }

func TestProbeReapsWorker(t *testing.T) {
	cmd := command("")
	got, err := Probe(testContext(t), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "test" || got.Version != "1.2" || got.AuthState != protocol.AuthReady {
		t.Fatalf("%+v", got)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("worker not reaped")
	}
}

func TestOrderedEventsAndWaitBeforeDrain(t *testing.T) {
	s, err := Start(testContext(t), command(""), providers.StartSpec{Prompt: "environment"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := s.Wait()
	if result.Outcome != protocol.OutcomeSucceeded || !result.ExitConfirmed || result.FinalText != "final" {
		t.Fatalf("%+v", result)
	}
	i := 0
	for event := range s.Events() {
		if i < 300 && event.Text != strconv.Itoa(i) {
			t.Fatalf("event %d: %q", i, event.Text)
		}
		if i == 300 && len(event.Text) != 256<<10 {
			t.Fatalf("large event: %d", len(event.Text))
		}
		i++
	}
	if i != 301 {
		t.Fatalf("received %d events", i)
	}
	assertQueueFreed(t, s.(*session))
}

func TestConcurrentBridgeAndSessionRequests(t *testing.T) {
	ctx := testContext(t)
	permission := make(chan struct{})
	parallel := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	handler := func(req bridge.LocalRequest) bridge.LocalResponse {
		if req.Token != "run-token" {
			return bridge.LocalResponse{}
		}
		switch req.Tool {
		case "permission":
			close(permission)
			<-release
		case "parallel":
			close(parallel)
		}
		return bridge.LocalResponse{OK: true, Result: json.RawMessage(`{"allowed":true}`)}
	}
	s, err := Start(ctx, command(""), providers.StartSpec{Prompt: "interactive", MCP: providers.MCPServer{Env: map[string]string{bridge.EnvToken: "run-token"}}}, handler)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range []chan struct{}{permission, parallel} {
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("bridge request deadlocked")
		}
	}
	if mode, err := s.SendInput(ctx, strings.Repeat("input", 65536)); err != nil || mode != "immediate" {
		t.Fatalf("%q %v", mode, err)
	}
	if _, err := s.SendInput(ctx, "unsupported"); !errors.Is(err, providers.ErrUnsupported) {
		t.Fatalf("unsupported: %v", err)
	}
	if err := s.ResolveApproval(ctx, "approval", providers.ApprovalDecision{Allow: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.AnswerQuestion(ctx, "question", providers.QuestionAnswer{Text: "answer"}); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	result := s.Wait()
	if result.Error != "" || !result.ExitConfirmed {
		t.Fatalf("%+v", result)
	}
	var kinds []string
	for event := range s.Events() {
		kinds = append(kinds, event.Kind)
	}
	if !reflect.DeepEqual(kinds, []string{providers.EventApprovalRequest, providers.EventQuestion}) {
		t.Fatal(kinds)
	}
}

func TestBridgeDuringStart(t *testing.T) {
	s, err := Start(testContext(t), command(""), providers.StartSpec{Prompt: "startup-bridge"}, func(req bridge.LocalRequest) bridge.LocalResponse {
		return bridge.LocalResponse{OK: req.Tool == "startup"}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := s.Wait(); result.Error != "" {
		t.Fatal(result)
	}
	for range s.Events() {
	}
}

func TestCancelAndUnexpectedEOF(t *testing.T) {
	for _, prompt := range []string{"hang", "crash"} {
		t.Run(prompt, func(t *testing.T) {
			ctx := testContext(t)
			s, err := Start(ctx, command(""), providers.StartSpec{Prompt: prompt}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if prompt == "hang" {
				if err := s.Cancel(ctx); err != nil {
					t.Fatal(err)
				}
			}
			r := s.Wait()
			if prompt == "hang" && (r.Outcome != protocol.OutcomeCancelled || !r.ExitConfirmed) {
				t.Fatalf("%+v", r)
			}
			if prompt == "crash" && (r.Outcome != protocol.OutcomeFailed || r.ExitConfirmed) {
				t.Fatalf("%+v", r)
			}
			for range s.Events() {
			}
		})
	}
}

func TestFailedAndMalformedStart(t *testing.T) {
	for _, tc := range []struct{ scenario, prompt string }{{"", "fail"}, {"", "unsupported"}, {"malformed", ""}} {
		t.Run(tc.scenario+tc.prompt, func(t *testing.T) {
			cmd := command(tc.scenario)
			_, err := Start(testContext(t), cmd, providers.StartSpec{Prompt: tc.prompt}, nil)
			if err == nil || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("error: %v", err)
			}
			if tc.prompt == "unsupported" && !errors.Is(err, providers.ErrUnsupported) {
				t.Fatal(err)
			}
			if cmd.ProcessState == nil {
				t.Fatal("failed worker was not reaped")
			}
		})
	}
}

func TestResultDoesNotConfirmFailedWorkerExit(t *testing.T) {
	s, err := Start(testContext(t), command("result-crash"), providers.StartSpec{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result := s.Wait(); result.ExitConfirmed {
		t.Fatalf("trusted failed transport: %+v", result)
	}
	for range s.Events() {
	}
}

func TestContextBoundsBlockedStartWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cmd := command("silent")
	start := time.Now()
	_, err := Start(ctx, cmd, providers.StartSpec{Prompt: strings.Repeat("x", 1<<20)}, nil)
	if err == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > 8*time.Second {
		t.Fatal("unbounded process wait")
	}
	if cmd.ProcessState == nil {
		t.Fatal("worker not reaped")
	}
}

func TestCancellationReapsProviderDescendants(t *testing.T) {
	ctx := testContext(t)
	s, err := Start(ctx, command(""), providers.StartSpec{Prompt: "process"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	select {
	case event := <-s.Events():
		pid, err = strconv.Atoi(event.Text)
	case <-ctx.Done():
		t.Fatal("provider did not start")
	}
	if err != nil || pid <= 0 {
		t.Fatalf("invalid child PID: %d %v", pid, err)
	}
	if err := s.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	if result := s.Wait(); !result.ExitConfirmed {
		t.Fatalf("%+v", result)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child still exists: %v", err)
	}
	for range s.Events() {
	}
}

func TestTimeoutAndProbeCancellation(t *testing.T) {
	ctx := testContext(t)
	s, err := Start(ctx, command(""), providers.StartSpec{Prompt: "hang", Timeout: 150 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result := s.Wait(); result.Outcome != protocol.OutcomeCancelled {
		t.Fatalf("%+v", result)
	}
	for range s.Events() {
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	cmd := command("silent")
	if _, err := Probe(short, cmd); err == nil {
		t.Fatal("probe did not time out")
	}
	if cmd.ProcessState == nil {
		t.Fatal("probe command not reaped")
	}
}

func TestServeEOFAndInvalidParams(t *testing.T) {
	in, write := io.Pipe()
	read, out := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), in, out, testAdapter{}, "test") }()
	started := make(chan int, 1)
	conn := acp.NewConn(read, write, dispatcher{notify: func(method string, raw json.RawMessage) {
		if method == "session/event" {
			var event providers.Event
			_ = json.Unmarshal(raw, &event)
			pid, _ := strconv.Atoi(event.Text)
			started <- pid
		}
	}})
	go conn.Serve()
	ctx := testContext(t)
	if err := conn.Call(ctx, "start", "bad-spec", nil); err == nil {
		t.Fatal("accepted malformed StartSpec")
	}
	if err := conn.Call(ctx, "start", providers.StartSpec{Prompt: "process"}, nil); err != nil {
		t.Fatal(err)
	}
	var pid int
	select {
	case pid = <-started:
	case <-ctx.Done():
		t.Fatal("provider did not start")
	}
	_ = write.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("stdin EOF did not stop Serve")
	}
	if pid <= 0 {
		t.Fatal("missing provider descendant")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("EOF left child alive: %v", err)
	}
}

func TestFrameLimit(t *testing.T) {
	if _, err := io.ReadAll(frames(strings.NewReader(strings.Repeat("x", maxFrame)))); err == nil {
		t.Fatal("oversized frame accepted")
	}
	want := strings.Repeat("x", 2<<20) + "\n"
	got, err := io.ReadAll(frames(strings.NewReader(want)))
	if err != nil || string(got) != want {
		t.Fatalf("large frame: %d, %v", len(got), err)
	}
}

func assertQueueFreed(t *testing.T, s *session) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queue != nil || s.queueBytes != 0 {
		t.Fatalf("retained queue: %d events, %d bytes", len(s.queue), s.queueBytes)
	}
}

func TestBlockedConsumerOverflowReapsProvider(t *testing.T) {
	for _, prompt := range []string{"flood-count", "flood-bytes"} {
		t.Run(prompt, func(t *testing.T) {
			ctx := testContext(t)
			cmd := command("")
			started, err := Start(ctx, cmd, providers.StartSpec{Prompt: prompt}, nil)
			if err != nil {
				t.Fatal(err)
			}
			s := started.(*session)
			t.Cleanup(func() { _ = s.Cancel(context.Background()) })
			var pid int
			select {
			case event := <-s.Events():
				pid, err = strconv.Atoi(event.Text)
			case <-ctx.Done():
				t.Fatal("provider did not start")
			}
			if err != nil || pid <= 0 {
				t.Fatalf("invalid provider PID: %d %v", pid, err)
			}
			if mode, err := s.SendInput(ctx, "flood"); err != nil || mode != "immediate" {
				t.Fatalf("trigger: %q %v", mode, err)
			}
			// Do not receive another event. Overflow must finish independently
			// of the consumer, including EOF cleanup of the provider group.
			select {
			case <-s.done:
			case <-ctx.Done():
				t.Fatal("overflow did not shut down")
			}
			result := s.Wait()
			if result.Outcome != protocol.OutcomeFailed || result.ExitConfirmed ||
				result.Error != "Worker event buffering limit exceeded." {
				t.Fatalf("overflow result: %+v", result)
			}
			assertQueueFreed(t, s)
			for range s.Events() {
			}
			if cmd.ProcessState == nil {
				t.Fatal("worker not reaped")
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("overflow left provider descendant alive: %v", err)
			}
		})
	}
}

func TestCancelReleasesUndrainedEvents(t *testing.T) {
	ctx := testContext(t)
	started, err := Start(ctx, command(""), providers.StartSpec{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := started.(*session)
	if result := s.Wait(); result.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("%+v", result)
	}
	if err := s.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	assertQueueFreed(t, s)
	for range s.Events() {
	}
}

func TestRequestFloodFailsClosed(t *testing.T) {
	ctx := testContext(t)
	release := make(chan struct{})
	defer close(release)
	started, err := Start(ctx, command("request-flood"), providers.StartSpec{},
		func(bridge.LocalRequest) bridge.LocalResponse {
			<-release
			return bridge.LocalResponse{OK: true}
		})
	if err != nil {
		t.Fatal(err)
	}
	s := started.(*session)
	t.Cleanup(func() { _ = s.Cancel(context.Background()) })
	if _, err := s.SendInput(ctx, "flood"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		t.Fatal("request overflow blocked the read loop")
	}
	if result := s.Wait(); result.Outcome != protocol.OutcomeFailed ||
		result.Error != "Worker request buffering limit exceeded." || result.ExitConfirmed {
		t.Fatalf("%+v", result)
	}
	assertQueueFreed(t, s)
	for range s.Events() {
	}
}

func TestRequestBudgetBoundsCountAndBytes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size, count int
	}{{"count", 1, maxRequests}, {"bytes", 8 << 20, maxRequestBytes / (8 << 20)}} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			overflow := make(chan struct{})
			b := &requestBudget{overflow: func() { close(overflow) }}
			d := dispatcher{budget: b, request: func(*acp.Request) { <-release }}
			for range tc.count {
				d.HandleRequest(&acp.Request{Params: make(json.RawMessage, tc.size)})
			}
			select {
			case <-overflow:
				t.Fatal("overflow before limit")
			default:
			}
			d.HandleRequest(&acp.Request{Params: make(json.RawMessage, tc.size)})
			select {
			case <-overflow:
			default:
				t.Fatal("overflow not detected")
			}
			b.mu.Lock()
			count, bytes := b.count, b.bytes
			b.mu.Unlock()
			if count != tc.count || bytes != tc.count*tc.size {
				t.Fatalf("unbounded requests: %d, %d", count, bytes)
			}
		})
	}
}

func TestEventBudgetIncludesBlockedDelivery(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size, count int
	}{{"count", 64, maxQueuedEvents}, {"bytes", 8 << 20, maxQueuedEventBytes / (8 << 20)}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			defer write.Close()
			s := &session{
				transport: &transport{in: write},
				ctx:       ctx, cancel: cancel, events: make(chan providers.Event),
				wake: make(chan struct{}, 1), abortEvents: make(chan struct{}),
				resultReady: make(chan struct{}),
			}
			go s.deliverEvents()
			// JSON whitespace makes the encoded payload exactly the desired
			// size without relying on serialization overhead estimates.
			raw := json.RawMessage(`{"text":"x"}` + strings.Repeat(" ", tc.size-len(`{"text":"x"}`)))
			for range tc.count {
				s.notification("session/event", raw)
			}
			s.mu.Lock()
			count, bytes, failure := len(s.queue), s.queueBytes, s.failure
			s.mu.Unlock()
			if count != tc.count || bytes != tc.size*tc.count || failure != "" {
				t.Fatalf("limit boundary: count=%d bytes=%d failure=%q", count, bytes, failure)
			}
			// With no consumer, even the event held by the delivery goroutine
			// must count against the budget.
			s.notification("session/event", raw)
			s.notification("session/result", json.RawMessage(`{"outcome":"succeeded"}`))
			s.mu.Lock()
			failed, haveResult := s.failure != "", s.haveResult
			s.mu.Unlock()
			if !failed || haveResult {
				t.Fatal("overflow accepted a terminal result")
			}
			assertQueueFreed(t, s)
			select {
			case _, ok := <-s.Events():
				if ok {
					// A send already waiting on this channel may race abort.
					for range s.Events() {
					}
				}
			case <-time.After(stopGrace):
				t.Fatal("blocked event delivery did not stop")
			}
		})
	}
}

func TestBundledSkillsThroughWorkerTunnel(t *testing.T) {
	for _, mode := range []string{"edit", "readonly", "conversation"} {
		t.Run(mode, func(t *testing.T) {
			spec := providers.StartSpec{Mode: mode, Prompt: "skills", Instructions: skills.Instructions(), MCP: providers.MCPServer{Env: map[string]string{bridge.EnvToken: "bound-run-token"}}}
			s, err := Start(testContext(t), command(""), spec, func(req bridge.LocalRequest) bridge.LocalResponse {
				if req.Token != "bound-run-token" {
					return bridge.LocalResponse{Error: &protocol.APIError{Code: "forbidden"}}
				}
				return skilltest.Handle(req)
			})
			if err != nil {
				t.Fatal(err)
			}
			for range s.Events() {
			}
			result := s.Wait()
			if result.Error != "" || result.Outcome != protocol.OutcomeSucceeded || !result.ExitConfirmed {
				t.Fatalf("%+v", result)
			}
		})
	}
}
