package pi

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/skills"
	"github.com/binbandit/yip/internal/skilltest"
	"github.com/binbandit/yip/protocol"
)

func fixture(t *testing.T, auth string) (*Adapter, providers.StartSpec) {
	t.Helper()
	return installationFixture(t, auth, false, false)
}

func installationFixture(t *testing.T, auth string, legacy, global bool) (*Adapter, providers.StartSpec) {
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
	cli := "dist/bundle/cli.js"
	metadata := `{"name":"@earendil-works/pi-coding-agent","version":"0.87.1","type":"module","bin":{"pi":"dist/bundle/cli.js"},"main":"./dist/index.js"}`
	if legacy {
		cli = "dist/cli.js"
		metadata = `{"name":"@mariozechner/pi-coding-agent","version":"0.73.1","type":"module","bin":{"pi":"dist/cli.js"},"main":"./dist/index.js"}`
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, cli)), 0700); err != nil {
		t.Fatal(err)
	}
	write("package.json", []byte(metadata))
	write(cli, nil)
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
	if legacy {
		env = append(env, "PI_TEST_LEGACY=1")
	}
	executable := filepath.Join(root, cli)
	if global {
		link := filepath.Join(t.TempDir(), "pi")
		if err := os.Symlink(executable, link); err != nil {
			t.Fatal(err)
		}
		executable = link
	}
	a := NewAdapter(Options{Executable: executable, NodeExecutable: node, Env: env, Grace: 100 * time.Millisecond})
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

func TestNPMEntryLayouts(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, global := range []bool{false, true} {
			name := map[bool]string{false: "current", true: "legacy"}[legacy] + "/" + map[bool]string{false: "direct", true: "global-symlink"}[global]
			t.Run(name, func(t *testing.T) {
				a, spec := installationFixture(t, "oauth", legacy, global)
				version := TestedVersion
				if legacy {
					version = LegacyTestedVersion
				}
				if got := a.Probe(context.Background()); got.AuthState != protocol.AuthReady || !got.Tested || got.TestedVersion != version || got.Version != version || got.Path != a.opts.Executable {
					t.Fatalf("%+v", got)
				}
				if result, _ := run(t, a, spec); result.Outcome != protocol.OutcomeSucceeded || result.FinalText != "bridge result" || !result.ExitConfirmed {
					t.Fatalf("%+v", result)
				}
			})
		}
	}
}

// Opt in with YIP_PI_NPM_SMOKE=1. This downloads public npm packages into
// temporary directories, but never uses existing npm or Pi credentials.
func TestPublishedNPMProbe(t *testing.T) {
	if os.Getenv("YIP_PI_NPM_SMOKE") != "1" {
		t.Skip("set YIP_PI_NPM_SMOKE=1 to probe the published npm packages")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []struct{ name, version string }{
		{"@earendil-works/pi-coding-agent", TestedVersion},
		{"@mariozechner/pi-coding-agent", LegacyTestedVersion},
	} {
		t.Run(pkg.name, func(t *testing.T) {
			root, home, agentDir := t.TempDir(), t.TempDir(), t.TempDir()
			env := []string{
				"PATH=" + strings.Join([]string{filepath.Dir(node), filepath.Dir(npm), "/usr/bin", "/bin"}, string(os.PathListSeparator)),
				"HOME=" + home,
				"PI_CODING_AGENT_DIR=" + agentDir,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, npm, "install", "--prefix", root,
				"--registry=https://registry.npmjs.org", "--ignore-scripts",
				"--no-audit", "--no-fund", "--no-package-lock", "--no-save",
				pkg.name+"@"+pkg.version)
			cmd.Dir = root
			cmd.Env = append(append([]string{}, env...),
				"npm_config_userconfig="+filepath.Join(root, "empty-user.npmrc"),
				"npm_config_globalconfig="+filepath.Join(root, "empty-global.npmrc"),
				"npm_config_cache="+filepath.Join(root, "npm-cache"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("public npm install: %v\n%s", err, out)
			}
			packageRoot := filepath.Join(root, "node_modules", filepath.FromSlash(pkg.name))
			b, err := os.ReadFile(filepath.Join(packageRoot, "package.json"))
			if err != nil {
				t.Fatal(err)
			}
			var metadata struct{ Bin struct{ Pi string } }
			if err := json.Unmarshal(b, &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.Bin.Pi == "" {
				t.Fatal("published package has no bin.pi")
			}
			// Exercise npm's real executable symlink as well as its published CLI.
			executable := filepath.Join(root, "node_modules", ".bin", "pi")
			resolved, err := filepath.EvalSymlinks(executable)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := filepath.EvalSymlinks(filepath.Join(packageRoot, metadata.Bin.Pi))
			if err != nil || resolved != expected {
				t.Fatalf("npm executable does not resolve to bin.pi: %q != %q (%v)", resolved, expected, err)
			}
			a := NewAdapter(Options{Executable: executable, NodeExecutable: node, Env: env})
			got := a.Probe(ctx)
			if !got.Tested || got.Version != pkg.version || got.TestedVersion != pkg.version ||
				got.AuthState != protocol.AuthNeedsSignIn || len(got.Models) != 0 || got.Billing != protocol.BillingUnknown {
				t.Fatalf("published package probe: %+v", got)
			}
		})
	}
}

func TestOtherVersionsKeepMetadataAndRun(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, version := range []string{"999.0.0", ""} {
			t.Run(strconv.FormatBool(legacy)+"/"+version, func(t *testing.T) {
				a, spec := installationFixture(t, "oauth", legacy, false)
				path := filepath.Join(spec.Workdir, "package.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				tested := TestedVersion
				if legacy {
					tested = LegacyTestedVersion
				}
				if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), tested, version)), 0600); err != nil {
					t.Fatal(err)
				}
				got := a.Probe(context.Background())
				if got.AuthState != protocol.AuthReady || got.Tested || got.TestedVersion != tested || got.Version != version || !got.Capabilities.ReadOnly {
					t.Fatalf("probe: %+v", got)
				}
				result, _ := run(t, a, spec)
				if result.Outcome != protocol.OutcomeSucceeded || !result.ExitConfirmed {
					t.Fatalf("%+v", result)
				}
			})
		}
	}
}

func TestIncompatibleSDKFails(t *testing.T) {
	a, spec := fixture(t, "oauth")
	if err := os.WriteFile(filepath.Join(spec.Workdir, "dist/index.js"), []byte(`export const ModelRuntime = {};`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := a.Probe(context.Background()); got.AuthState == protocol.AuthReady || got.Capabilities.ReadOnly || got.AuthDetail == "" {
		t.Fatalf("probe: %+v", got)
	}
	result, _ := run(t, a, spec)
	if result.Outcome != protocol.OutcomeFailed || result.Error == "" {
		t.Fatalf("%+v", result)
	}
}

func TestIncompatibleToolAllowlistFailsBeforePrompt(t *testing.T) {
	a, spec := fixture(t, "oauth")
	a.opts.Env = append(a.opts.Env, "PI_TEST_EXTRA_TOOL=1")
	result, events := run(t, a, spec)
	if result.Outcome != protocol.OutcomeFailed || !strings.Contains(result.Error, "required tool allowlist") {
		t.Fatalf("%+v", result)
	}
	for _, event := range events {
		if event.Kind == providers.EventMessageDelta || event.Kind == providers.EventToolStarted {
			t.Fatalf("prompt ran despite incompatible tool allowlist: %+v", event)
		}
	}
}

func TestEnvironmentSafety(t *testing.T) {
	a, _ := fixture(t, "oauth")
	env := a.env([]string{"HOME=/safe", "NODE_OPTIONS=--import=unsafe", "NODE_PATH=/unsafe"})
	if len(env) != 1 || env[0] != "HOME=/safe" {
		t.Fatalf("%v", env)
	}
}

func TestNPMInstallationBoundaries(t *testing.T) {
	for _, scenario := range []string{
		"unrelated-manifest", "malformed-manifest", "manifest-directory",
		"missing-manifest", "wrong-name", "unsafe-main",
		"wrong-bin", "missing-sdk", "sdk-directory", "sdk-escape", "too-deep",
	} {
		t.Run(scenario, func(t *testing.T) {
			a, spec := fixture(t, "oauth")
			root := spec.Workdir
			write := func(path, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			remove := func(path string) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, path)); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "unrelated-manifest":
				write("dist/package.json", `{"name":"unrelated","version":"0.87.1"}`)
			case "malformed-manifest":
				write("dist/package.json", `{`)
			case "manifest-directory":
				if err := os.Mkdir(filepath.Join(root, "dist/package.json"), 0700); err != nil {
					t.Fatal(err)
				}
			case "missing-manifest":
				remove("package.json")
			case "wrong-name", "unsafe-main", "wrong-bin":
				b, err := os.ReadFile(filepath.Join(root, "package.json"))
				if err != nil {
					t.Fatal(err)
				}
				old, replacement := "@earendil-works/pi-coding-agent", "unrelated"
				switch scenario {
				case "unsafe-main":
					old, replacement = "./dist/index.js", "../index.js"
				case "wrong-bin":
					old, replacement = "dist/bundle/cli.js", "dist/cli.js"
				}
				write("package.json", strings.ReplaceAll(string(b), old, replacement))
			case "missing-sdk", "sdk-directory", "sdk-escape":
				remove("dist/index.js")
				if scenario == "sdk-directory" {
					if err := os.Mkdir(filepath.Join(root, "dist/index.js"), 0700); err != nil {
						t.Fatal(err)
					}
				} else if scenario == "sdk-escape" {
					outside := filepath.Join(t.TempDir(), "index.js")
					if err := os.WriteFile(outside, nil, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, filepath.Join(root, "dist/index.js")); err != nil {
						t.Fatal(err)
					}
				}
			case "too-deep":
				if err := os.Mkdir(filepath.Join(root, "dist/bundle/nested"), 0700); err != nil {
					t.Fatal(err)
				}
				write("dist/bundle/nested/cli.js", "")
				a.opts.Executable = filepath.Join(root, "dist/bundle/nested/cli.js")
			}
			got := a.Probe(context.Background())
			if got.Tested || got.AuthState != protocol.AuthUnknown || got.AuthDetail == "" || len(got.Models) != 0 {
				t.Fatalf("invalid installation reported ready: %+v", got)
			}
			if _, err := a.Start(context.Background(), spec); err == nil {
				t.Fatal("invalid installation accepted")
			}
		})
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
			// An exited descendant can stay visible until its adopter reaps
			// it. Confirm no live work remains, even while zombie PIDs exist.
			if syscall.Kill(pid, 0) == nil {
				out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
				state := strings.TrimSpace(string(out))
				if state != "" && !strings.HasPrefix(state, "Z") {
					t.Fatalf("descendant %d still running (%s), result: %+v", pid, state, result)
				}
			}
			if !result.ExitConfirmed {
				t.Fatalf("terminated descendant %d left exit unconfirmed: %+v", pid, result)
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

func TestBundledSkillsThroughSDKHost(t *testing.T) {
	for _, mode := range []string{"edit", "readonly", "conversation"} {
		t.Run(mode, func(t *testing.T) {
			a, spec := fixture(t, "oauth")
			spec.Mode, spec.Prompt, spec.Instructions = mode, "skills", skills.Instructions()
			spec.MCP = skilltest.New(t, mode)
			result, _ := run(t, a, spec)
			var docs []skills.Document
			if result.Outcome != protocol.OutcomeSucceeded || json.Unmarshal([]byte(result.FinalText), &docs) != nil {
				t.Fatalf("%+v", result)
			}
			if len(docs) != len(skills.Catalog()) {
				t.Fatal("missing skills")
			}
			for _, doc := range docs {
				want, _ := skills.Read(doc.Name)
				if doc != want {
					t.Fatalf("skill %s changed in transit", doc.Name)
				}
			}
		})
	}
}
