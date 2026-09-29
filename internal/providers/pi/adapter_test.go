package pi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

func fixture(t *testing.T, auth string) (*Adapter, providers.StartSpec) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the SDK host tests")
	}
	root := t.TempDir()
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, path), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	write("package.json", []byte(`{"name":"@earendil-works/pi-coding-agent","version":"0.87.1","type":"module"}`))
	write("dist/cli.js", nil)
	sdk, err := os.ReadFile("testdata/sdk.mjs")
	if err != nil {
		t.Fatal(err)
	}
	write("dist/index.js", sdk)
	bridge, err := filepath.Abs("testdata/bridge.mjs")
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + filepath.Dir(node), "HOME=" + root, "PI_TEST_AUTH=" + auth}
	a := NewAdapter(Options{Executable: filepath.Join(root, "dist/cli.js"), NodeExecutable: node, Env: env, Grace: 100 * time.Millisecond})
	return a, providers.StartSpec{Mode: protocol.ModeReadOnly, Workdir: root, Prompt: "run", MCP: providers.MCPServer{Command: node, Args: []string{bridge}}}
}
func run(t *testing.T, a *Adapter, spec providers.StartSpec) (providers.Result, []providers.Event) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := a.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	var events []providers.Event
	for event := range s.Events() {
		events = append(events, event)
	}
	return s.Wait(), events
}
func TestProbeAuth(t *testing.T) {
	for _, test := range []struct {
		auth, state, billing string
		models               int
	}{
		{"none", protocol.AuthNeedsSignIn, protocol.BillingUnknown, 0},
		{"oauth", protocol.AuthReady, protocol.BillingSubscription, 1},
		{"mixed", protocol.AuthReady, protocol.BillingAPI, 2},
	} {
		t.Run(test.auth, func(t *testing.T) {
			a, _ := fixture(t, test.auth)
			got := a.Probe(context.Background())
			if got.AuthState != test.state || got.Billing != test.billing || len(got.Models) != test.models {
				t.Fatalf("%+v", got)
			}
			if !got.Capabilities.ReadOnly || got.Capabilities.SessionResume || !got.Capabilities.MCPTools {
				t.Fatalf("%+v", got.Capabilities)
			}
			if len(got.Models) > 0 && (!got.Models[0].Default || got.Models[0].ID != "oauth/model") {
				t.Fatalf("%+v", got.Models)
			}
		})
	}
}
func TestExecutionBridgeAndUsage(t *testing.T) {
	t.Setenv("PI_TEST_SECRET", "must not leak")
	a, spec := fixture(t, "oauth")
	result, events := run(t, a, spec)
	if result.Outcome != protocol.OutcomeSucceeded || result.FinalText != "bridge result" || !result.ExitConfirmed {
		t.Fatalf("%+v", result)
	}
	if result.Usage == nil || *result.Usage.InputTokens != 11 || *result.Usage.OutputTokens != 3 || result.Usage.Billing != protocol.BillingSubscription {
		t.Fatalf("%+v", result.Usage)
	}
	kinds := map[string]bool{}
	for _, e := range events {
		kinds[e.Kind] = true
	}
	for _, kind := range []string{providers.EventMessageDelta, providers.EventToolStarted, providers.EventToolFinished, providers.EventUsage} {
		if !kinds[kind] {
			t.Errorf("missing %s", kind)
		}
	}
}
func TestFailures(t *testing.T) {
	for _, prompt := range []string{"error", "crash"} {
		t.Run(prompt, func(t *testing.T) {
			a, spec := fixture(t, "oauth")
			spec.Prompt = prompt
			result, _ := run(t, a, spec)
			if result.Outcome != protocol.OutcomeFailed || result.Error == "" || !result.ExitConfirmed {
				t.Fatalf("%+v", result)
			}
		})
	}
	a, spec := fixture(t, "none")
	result, _ := run(t, a, spec)
	if result.Outcome != protocol.OutcomeFailed || !strings.Contains(result.Error, "No configured Pi model") {
		t.Fatalf("%+v", result)
	}
}
func TestApprovalAndDenial(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "deny", true: "allow"}[allow], func(t *testing.T) {
			a, spec := fixture(t, "oauth")
			spec.Mode = protocol.ModeEdit
			spec.Prompt = "approval"
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s, err := a.Start(ctx, spec)
			if err != nil {
				t.Fatal(err)
			}
			got := false
			for e := range s.Events() {
				if e.Approval != nil {
					got = true
					if e.Approval.Action.Kind != "exec" || !strings.Contains(e.Approval.Action.Detail, "echo safe") {
						t.Fatalf("%+v", e.Approval)
					}
					if err := s.ResolveApproval(ctx, e.Approval.ID, providers.ApprovalDecision{Allow: allow}); err != nil {
						t.Fatal(err)
					}
				}
			}
			result := s.Wait()
			if !got || (result.Outcome == protocol.OutcomeSucceeded) != allow {
				t.Fatalf("%+v approval=%v", result, got)
			}
		})
	}
}
func TestSteeringAndCancel(t *testing.T) {
	for _, steer := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "steer"}[steer], func(t *testing.T) {
			a, spec := fixture(t, "oauth")
			spec.Prompt = "wait"
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s, err := a.Start(ctx, spec)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-s.Events():
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if steer {
				mode, err := s.SendInput(ctx, "continue")
				if err != nil || mode != "queued" {
					t.Fatalf("%s %v", mode, err)
				}
			} else if err := s.Cancel(ctx); err != nil {
				t.Fatal(err)
			}
			for range s.Events() {
			}
			result := s.Wait()
			want := protocol.OutcomeCancelled
			if steer {
				want = protocol.OutcomeSucceeded
			}
			if result.Outcome != want || !result.ExitConfirmed {
				t.Fatalf("%+v", result)
			}
		})
	}
}
func TestUnsupportedBoundaries(t *testing.T) {
	a, spec := fixture(t, "oauth")
	spec.ResumeSessionID = "old"
	if _, err := a.Start(context.Background(), spec); err == nil {
		t.Fatal("resume accepted")
	}
	spec.ResumeSessionID = ""
	spec.Mode = "unsafe"
	if _, err := a.Start(context.Background(), spec); err == nil {
		t.Fatal("unsafe mode accepted")
	}
	spec.Mode = protocol.ModeReadOnly
	spec.MCP.Command = ""
	if _, err := a.Start(context.Background(), spec); err == nil {
		t.Fatal("missing bridge accepted")
	}
}

func TestLegacySDK(t *testing.T) {
	a, spec := fixture(t, "oauth")
	a.opts.Env = append(a.opts.Env, "PI_TEST_LEGACY=1")
	metadata := filepath.Join(spec.Workdir, "package.json")
	if err := os.WriteFile(metadata, []byte(`{"name":"@mariozechner/pi-coding-agent","version":"0.73.1","type":"module"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := a.Probe(context.Background()); got.AuthState != protocol.AuthReady || got.TestedVersion != LegacyTestedVersion {
		t.Fatalf("%+v", got)
	}
	if result, _ := run(t, a, spec); result.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("%+v", result)
	}
}

func TestVersionAndEnvironmentSafety(t *testing.T) {
	a, spec := fixture(t, "oauth")
	env := a.env([]string{"HOME=/safe", "NODE_OPTIONS=--import=unsafe", "NODE_PATH=/unsafe"})
	if len(env) != 1 || env[0] != "HOME=/safe" {
		t.Fatalf("%v", env)
	}
	if err := os.WriteFile(filepath.Join(spec.Workdir, "package.json"), []byte(`{"name":"@earendil-works/pi-coding-agent","version":"999.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(context.Background(), spec); err == nil {
		t.Fatal("untested SDK version accepted")
	}
}

func TestNonCooperatingSDKTimeout(t *testing.T) {
	a, spec := fixture(t, "oauth")
	spec.Prompt = "hang"
	spec.Timeout = 150 * time.Millisecond
	result, _ := run(t, a, spec)
	if result.Outcome != protocol.OutcomeCancelled || !result.ExitConfirmed {
		t.Fatalf("%+v", result)
	}
}

func TestMalformedHostRecord(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required")
	}
	cmd := exec.Command(node, "-e", `console.log("not json"); setInterval(()=>{},1000)`)
	cmd.Env = []string{}
	s, err := startSession(cmd, func() {}, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
	result := s.Wait()
	if result.Outcome != protocol.OutcomeFailed || result.Error != "Invalid Pi host JSON" || !result.ExitConfirmed {
		t.Fatalf("%+v", result)
	}
}

// With YIP_PI_TEST_SDK set, these scenarios use the published SDK's actual bash
// tool factory, with no session/model/auth construction. Without it they still
// exercise the same production operations backend and process supervisor.
func TestBashDescendantsStaySupervised(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required")
	}
	host, err := filepath.Abs("host.mjs")
	if err != nil {
		t.Fatal(err)
	}
	harness, err := filepath.Abs("testdata/bash-lifecycle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"background", "crash", "cancel", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "child.pid")
			cmd := exec.Command(node, harness, host)
			cmd.Dir = dir
			cmd.Env = []string{
				"PATH=/usr/bin:/bin", "HOME=" + dir, "YIP_TEST_CASE=" + scenario,
				"YIP_TEST_PID=" + pidFile, "YIP_PI_TEST_SDK=" + os.Getenv("YIP_PI_TEST_SDK"),
			}
			s, err := startSession(cmd, func() {}, 100*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Cancel(context.Background()) })
			deadline := time.Now().Add(10 * time.Second)
			pid := 0
			for time.Now().Before(deadline) {
				b, _ := os.ReadFile(pidFile)
				pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
				if pid > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("bash did not launch its child")
			}
			// Bound any regression's residual process even if the assertion fails.
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			group, err := syscall.Getpgid(pid)
			if err != nil || group != s.process.Cmd.Process.Pid {
				t.Fatalf("bash descendant escaped: child group=%d host group=%d err=%v", group, s.process.Cmd.Process.Pid, err)
			}
			switch scenario {
			case "background":
				select {
				case event := <-s.Events():
					if event.Text != "shell exited" {
						t.Fatalf("unexpected event: %+v", event)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("background descendant kept bash tool open")
				}
				_ = s.write(map[string]any{"type": "finish"})
			case "crash":
				_ = s.process.Cmd.Process.Kill()
			case "cancel":
				_ = s.Cancel(context.Background())
			}
			select {
			case <-s.done:
			case <-time.After(5 * time.Second):
				t.Fatal("session did not terminate")
			}
			result := s.Wait()
			// kill(pid, 0) can briefly see an already exited zombie after its
			// process group disappeared (notably after a host crash on macOS).
			// Verify live execution, not whether init has reaped its PID yet.
			if syscall.Kill(pid, 0) == nil {
				out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
				state := strings.TrimSpace(string(out))
				if state != "" && !strings.HasPrefix(state, "Z") {
					t.Fatalf("descendant %d still running (%s), result: %+v", pid, state, result)
				}
			}
		})
	}
}

func TestBlockedStartupWriteCanBeCancelled(t *testing.T) {
	for _, useTimeout := range []bool{false, true} {
		name := "context"
		if useTimeout {
			name = "run-timeout"
		}
		t.Run(name, func(t *testing.T) {
			a, spec := fixture(t, "oauth")
			// Simulate an SDK import that never reaches host stdin registration.
			sdk := []byte(`await new Promise(() => { setInterval(() => {}, 1000); });`)
			if err := os.WriteFile(filepath.Join(spec.Workdir, "dist", "index.js"), sdk, 0600); err != nil {
				t.Fatal(err)
			}
			spec.Prompt = strings.Repeat("blocked prompt ", 1<<20)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if useTimeout {
				spec.Timeout = 100 * time.Millisecond
			} else {
				time.AfterFunc(100*time.Millisecond, cancel)
			}
			done := make(chan error, 1)
			go func() { _, err := a.Start(ctx, spec); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("blocked startup reported successful delivery")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("startup write ignored cancellation")
			}
		})
	}
}
