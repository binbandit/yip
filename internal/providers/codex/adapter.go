// Package codex adapts OpenAI Codex's documented `codex app-server` JSON-RPC
// interface (stdio transport) to yip's provider contract.
//
// The adapter is pinned to TestedVersion. Protocol types are hand-written
// against the JSON schema generated from that binary (schema/), and the
// launch configuration is documented in docs/providers/codex.md.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

const (
	// ProviderName is the stable provider identifier.
	ProviderName = "codex"
	// TestedVersion is the Codex CLI version this adapter was built and
	// verified against (schema generated from this binary).
	TestedVersion = "0.147.0"

	clientVersion = "0.1.0"
)

// Option configures the adapter.
type Option func(*Adapter)

// WithExecutable pins the codex executable instead of resolving it on PATH.
func WithExecutable(path string) Option { return func(a *Adapter) { a.exe = path } }

// WithProbeEnv overrides the allowlisted environment used by Probe (tests).
func WithProbeEnv(env []string) Option { return func(a *Adapter) { a.probeEnv = env } }

// WithTimeouts overrides protocol timeouts (tests use short values).
func WithTimeouts(t Timeouts) Option { return func(a *Adapter) { a.timeouts = t } }

// Timeouts bounds the adapter's own protocol waits. None of them bound how
// long an approval or question may stay pending: those wait for the runner.
type Timeouts struct {
	Handshake time.Duration // initialize + startup inspection requests
	Request   time.Duration // ordinary requests (turn/start, steer, reads)
	Interrupt time.Duration // how long Cancel waits for turn/interrupt to land
	Grace     time.Duration // SIGTERM → SIGKILL grace for the process group
}

func defaultTimeouts() Timeouts {
	return Timeouts{
		Handshake: 60 * time.Second,
		Request:   60 * time.Second,
		Interrupt: 10 * time.Second,
		Grace:     5 * time.Second,
	}
}

// Adapter is the Codex app-server provider adapter.
type Adapter struct {
	exe      string
	probeEnv []string
	timeouts Timeouts
}

// New returns the Codex adapter.
func New(opts ...Option) providers.Adapter { return NewAdapter(opts...) }

// NewAdapter returns the concrete adapter type (for callers needing options).
func NewAdapter(opts ...Option) *Adapter {
	a := &Adapter{timeouts: defaultTimeouts()}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *Adapter) Name() string  { return ProviderName }
func (a *Adapter) Label() string { return "Codex" }

// envKeys are the provider-specific host variables added to the base
// allowlist when the runner does not supply an explicit environment.
// CODEX_HOME is honoured (never relocated) so Codex finds the user's own
// sign-in; no API key variables are forwarded implicitly.
var envKeys = []string{"CODEX_HOME"}

func (a *Adapter) resolve(override string) (string, error) {
	if override == "" {
		override = a.exe
	}
	return providers.LookPath(override, "codex")
}

// Capabilities are the capabilities this adapter implements on the pinned
// version. Probe clears ModelEnumeration when model/list fails.
func Capabilities() protocol.ProviderCapabilities {
	return protocol.ProviderCapabilities{
		StructuredEvents: true,
		ToolApprovals:    true,
		UserQuestions:    true,
		SessionResume:    true,
		ActiveSteering:   true,
		UsageTelemetry:   true,
		Sandbox:          true,
		ModelEnumeration: true,
		ReadOnly:         true,
		MCPTools:         true,
	}
}

// Limitations are surfaced with every probe so the UI can explain them.
var Limitations = []string{
	"Codex still applies the user's own ~/.codex settings (model provider, profile, shell environment policy, AGENTS.md, skills). Repository .codex config is ignored; other MCP servers, hooks, plugins, apps and memories are disabled for yip runs.",
	"Engineers cannot git commit from inside Codex's sandbox (.git is read-only), and network access is off unless a human approves an escalation.",
	"Token usage is reported by Codex; dollar cost is not available.",
	"Rate-limit reset times are shown only when Codex reports them.",
	"A Codex thread can only be resumed on the machine that created it.",
}

var versionRE = regexp.MustCompile(`(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)`)

func parseVersion(out string) string {
	if m := versionRE.FindString(out); m != "" {
		return m
	}
	return strings.TrimSpace(out)
}

// Probe discovers version, sign-in state (through the documented
// account/read method, never by reading credential files), billing and the
// model catalogue.
func (a *Adapter) Probe(ctx context.Context) protocol.ProviderInstallation {
	inst := protocol.ProviderInstallation{
		Provider:      ProviderName,
		AuthState:     protocol.AuthUnknown,
		Billing:       protocol.BillingUnknown,
		Capabilities:  Capabilities(),
		Models:        []protocol.Model{},
		TestedVersion: TestedVersion,
		Limitations:   append([]string(nil), Limitations...),
		UpdatedAt:     time.Now().UTC(),
	}
	exe, err := a.resolve("")
	if err != nil {
		inst.AuthState = protocol.AuthNotInstalled
		inst.AuthDetail = "codex executable not found"
		inst.Capabilities = protocol.ProviderCapabilities{}
		return inst
	}
	inst.Path = exe
	out, err := providers.RunVersion(ctx, exe, "--version")
	if err != nil {
		inst.AuthState = protocol.AuthError
		inst.AuthDetail = "codex --version failed: " + err.Error()
		return inst
	}
	inst.Version = parseVersion(out)
	inst.Tested = inst.Version == TestedVersion
	if !inst.Tested {
		inst.Limitations = append(inst.Limitations, fmt.Sprintf("Installed Codex %s differs from the tested version %s; protocol changes may break the adapter.", inst.Version, TestedVersion))
	}

	env := a.probeEnv
	if env == nil {
		env = providers.BaseEnv(envKeys)
	}
	// Run the probe from an empty directory so no project config is involved.
	dir, err := os.MkdirTemp("", "yip-codex-probe-")
	if err != nil {
		inst.AuthState = protocol.AuthError
		inst.AuthDetail = err.Error()
		return inst
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, a.timeouts.Handshake)
	defer cancel()
	srv, err := a.launch(ctx, exe, dir, env)
	if err != nil {
		inst.AuthState = protocol.AuthError
		inst.AuthDetail = "codex app-server failed to start: " + err.Error()
		return inst
	}
	defer srv.shutdown(a.timeouts.Grace)

	if err := srv.initialize(ctx); err != nil {
		inst.AuthState = protocol.AuthError
		inst.AuthDetail = "codex app-server initialize failed: " + srv.describe(err)
		return inst
	}
	var acct getAccountResponse
	if err := srv.c.call(ctx, methodAccountRead, getAccountParams{RefreshToken: false}, &acct); err != nil {
		inst.AuthState = protocol.AuthError
		inst.AuthDetail = "account/read failed: " + srv.describe(err)
	} else {
		inst.AuthState, inst.AuthDetail, inst.Account, inst.Billing = classifyAccount(acct)
	}

	var cfg configReadResponse
	if err := srv.c.call(ctx, methodConfigRead, configReadParams{Cwd: dir, IncludeLayers: true}, &cfg); err != nil {
		inst.Limitations = append(inst.Limitations, "config/read failed: "+srv.describe(err))
	} else if rules := ruleFiles(cfg.Layers); len(rules) > 0 {
		inst.Capabilities.ReadOnly = false
		inst.Capabilities.ExecPolicyRules = rules
		inst.Limitations = append(inst.Limitations, "Reviews and conversations need your permission: Codex exec-policy rules ("+strings.Join(rules, ", ")+") can run matching commands outside the sandbox without approval. Edit runs always use them.")
	}

	models, err := listModels(ctx, srv.c)
	if err != nil {
		inst.Capabilities.ModelEnumeration = false
		inst.Limitations = append(inst.Limitations, "model/list failed: "+srv.describe(err))
	} else {
		inst.Models = models
	}
	return inst
}

func classifyAccount(r getAccountResponse) (state, detail, acct, billing string) {
	if r.Account == nil {
		if r.RequiresOpenaiAuth {
			return protocol.AuthNeedsSignIn, "Sign in with `codex login` on this machine.", "", protocol.BillingUnknown
		}
		// A configured non-OpenAI model provider needs no OpenAI sign-in;
		// Codex does not tell us how it is billed.
		return protocol.AuthReady, "Configured model provider does not require OpenAI sign-in.", "", protocol.BillingUnknown
	}
	switch r.Account.Type {
	case accountChatGPT:
		email := ""
		if r.Account.Email != nil {
			email = *r.Account.Email
		}
		d := "Signed in with ChatGPT"
		if r.Account.PlanType != "" {
			d += " (" + r.Account.PlanType + " plan)"
		}
		return protocol.AuthReady, d, email, protocol.BillingSubscription
	case accountAPIKey:
		return protocol.AuthReady, "Using an OpenAI API key", "", protocol.BillingAPI
	case accountBedrock:
		return protocol.AuthReady, "Using Amazon Bedrock credentials", "", protocol.BillingAPI
	default:
		return protocol.AuthReady, "Signed in (" + r.Account.Type + ")", "", protocol.BillingUnknown
	}
}

func listModels(ctx context.Context, c *conn) ([]protocol.Model, error) {
	var out []protocol.Model
	var cursor *string
	for page := 0; page < 10; page++ {
		var resp modelListResponse
		if err := c.call(ctx, methodModelList, modelListParams{Cursor: cursor}, &resp); err != nil {
			return nil, err
		}
		for _, m := range resp.Data {
			if m.Hidden {
				continue
			}
			id := m.Model
			if id == "" {
				id = m.ID
			}
			label := m.DisplayName
			if label == "" {
				label = id
			}
			out = append(out, protocol.Model{ID: id, Label: label, Default: m.IsDefault, Description: m.Description})
		}
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	if out == nil {
		out = []protocol.Model{}
	}
	return out, nil
}

// launchArgs are the exact arguments passed to the codex executable. Only
// non-secret, structural overrides go on the command line (argv is visible
// to other local users); per-run settings, including the yip MCP server and
// its environment, travel over stdin in thread/start's `config`.
func launchArgs() []string {
	args := []string{"app-server", "--listen", "stdio://"}
	for _, f := range disabledFeatures {
		args = append(args, "--disable", f)
	}
	for _, kv := range launchOverrides {
		args = append(args, "-c", kv)
	}
	return args
}

// disabledFeatures are Codex feature flags turned off for every launch
// (`--disable <feature>` is documented as `-c features.<name>=false`).
// See docs/providers/codex.md ("Startup boundary").
var disabledFeatures = []string{
	"hooks",    // lifecycle hooks from user/project/plugin config never run
	"plugins",  // plugins can bring their own MCP servers, skills and hooks
	"apps",     // ChatGPT apps/connectors are extra MCP tool sources
	"memories", // cross-session memories would cross yip's context scopes
}

// launchOverrides are `-c key=value` session-flag overrides for every launch.
var launchOverrides = []string{
	// Never run a user-configured notification program (an empty argv is
	// treated as "no notifier").
	`notify=[]`,
}

// server is a launched app-server process plus its connection.
type server struct {
	proc   *providers.Process
	c      *conn
	stderr *providers.TailBuffer
}

func (a *Adapter) launch(ctx context.Context, exe, dir string, env []string) (*server, error) {
	cmd := exec.Command(exe, launchArgs()...)
	cmd.Dir = dir
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// A manual pipe (rather than StdoutPipe) so Wait cannot close stdout
	// underneath the reader and drop the final messages of a crash.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = pw
	tail := providers.NewTailBuffer(8 << 10)
	cmd.Stderr = tail
	// Descendants that keep stderr open must not wedge Wait forever.
	cmd.WaitDelay = 2 * time.Second
	proc, err := providers.StartProcess(cmd)
	_ = pw.Close()
	if err != nil {
		_ = pr.Close()
		return nil, err
	}
	srv := &server{proc: proc, stderr: tail}
	srv.c = newConn(pr, stdin)
	go func() {
		// When the process exits, give the reader a moment to drain what the
		// server wrote, then force EOF even if a stray descendant still holds
		// the pipe open.
		<-proc.Done()
		select {
		case <-srv.c.closed:
		case <-time.After(2 * time.Second):
			_ = pr.Close()
		}
	}()
	return srv, nil
}

func (s *server) initialize(ctx context.Context) error {
	var resp initializeResponse
	err := s.c.call(ctx, methodInitialize, initializeParams{
		ClientInfo:   clientInfo{Name: clientName, Title: clientTitle, Version: clientVersion},
		Capabilities: &initializeCapabilities{ExperimentalAPI: false},
	}, &resp)
	if err != nil {
		return err
	}
	return s.c.notify(notifyInitialized, nil)
}

// describe renders an error, adding the stderr tail when the process died.
func (s *server) describe(err error) string {
	msg := err.Error()
	if errors.Is(err, errConnClosed) || s.proc.Exited() {
		if tail := strings.TrimSpace(s.stderr.String()); tail != "" {
			msg += ": " + lastLines(tail, 8)
		}
	}
	return msg
}

// shutdown closes stdin, waits briefly for a clean exit, then terminates the
// process group. It reports whether the whole group is confirmed gone.
func (s *server) shutdown(grace time.Duration) bool {
	s.c.closeWrite()
	select {
	case <-s.proc.Done():
	case <-time.After(grace):
	}
	return s.proc.Terminate(grace)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Start launches app-server for one run attempt. It returns once the process
// is running and initialized; thread and turn setup continue in the session
// and failures there are reported through Wait with a typed outcome.
func (a *Adapter) Start(ctx context.Context, spec providers.StartSpec) (providers.Session, error) {
	if !filepath.IsAbs(spec.Workdir) {
		return nil, fmt.Errorf("codex: workdir must be absolute, got %q", spec.Workdir)
	}
	if st, err := os.Stat(spec.Workdir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("codex: workdir %q is not a directory", spec.Workdir)
	}
	switch spec.Mode {
	case protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation:
	default:
		return nil, fmt.Errorf("codex: unknown mode %q: %w", spec.Mode, providers.ErrUnsupported)
	}
	if spec.MCP.Command == "" {
		return nil, errors.New("codex: the yip MCP bridge command is required")
	}
	exe, err := a.resolve(spec.Executable)
	if err != nil {
		return nil, fmt.Errorf("codex: executable not found: %w", err)
	}
	env := spec.Env
	if env == nil {
		env = providers.BaseEnv(envKeys)
	}
	srv, err := a.launch(ctx, exe, spec.Workdir, env)
	if err != nil {
		return nil, fmt.Errorf("codex: start app-server: %w", err)
	}
	hctx, cancel := context.WithTimeout(ctx, a.timeouts.Handshake)
	defer cancel()
	if err := srv.initialize(hctx); err != nil {
		desc := srv.describe(err)
		srv.shutdown(a.timeouts.Grace)
		return nil, fmt.Errorf("codex: initialize: %s", desc)
	}
	// Read the effective config layers for the workdir before any thread
	// exists: they name the MCP servers to disable and the exec-policy
	// rule folders Codex will load.
	var cfg configReadResponse
	if err := srv.c.call(hctx, methodConfigRead, configReadParams{Cwd: spec.Workdir, IncludeLayers: true}, &cfg); err != nil {
		desc := srv.describe(err)
		srv.shutdown(a.timeouts.Grace)
		return nil, fmt.Errorf("codex: config/read: %s", desc)
	}
	rules := ruleFiles(cfg.Layers)
	// Read-only runs go ahead with rules only when the owner allowed them on
	// this machine; app-server has no switch to ignore them.
	if spec.Mode != protocol.ModeEdit && len(rules) > 0 && !spec.TrustProviderRules {
		srv.shutdown(a.timeouts.Grace)
		return nil, fmt.Errorf("codex: cannot enforce %s mode: Codex exec-policy rules (%s) can run matching commands outside the sandbox without approval, and app-server has no switch to ignore them: %w",
			spec.Mode, strings.Join(rules, ", "), providers.ErrUnsupported)
	}
	s := newSession(a, spec, srv, cfg, rules)
	go s.run()
	return s, nil
}

// ruleFiles lists the exec-policy rule files Codex loads from the enabled
// user and system config folders (`<folder>/rules/*.rules`). Only directory
// entries are listed; file contents are never read. Rules can mark commands
// "allow", which Codex runs outside the sandbox without asking.
func ruleFiles(layers []configLayer) []string {
	var out []string
	for _, l := range layers {
		if l.DisabledReason != nil || l.Name.File == "" || (l.Name.Type != "user" && l.Name.Type != "system") {
			continue
		}
		dir := filepath.Join(filepath.Dir(l.Name.File), "rules")
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".rules") {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

// rawJSON is a helper for audit payloads.
func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
