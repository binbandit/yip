// Package claude adapts the unmodified Claude Code CLI to yip's provider
// contract.
//
// It uses only Claude Code's documented programmatic interface: print mode
// (-p) with newline-delimited stream-json on stdin and stdout, the documented
// MCP configuration flags, and the documented --permission-prompt-tool bridge.
// Authentication stays inside Claude Code: the user signs in with Anthropic's
// own flow on the runner (subscription), or the runner's configured
// environment carries the user's own ANTHROPIC_API_KEY (API billed). The
// adapter never reads Claude Code credential files or the keychain.
//
// See docs/providers/claude-code.md for the launch arguments, permission-tool
// wire format, and capability matrix.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// TestedVersion is the Claude Code version this adapter was verified against.
const TestedVersion = "2.1.282"

// ProviderName is the yip provider identifier.
const ProviderName = "claude"

// extraEnvKeys are host variables forwarded in addition to providers.BaseEnv
// when the runner does not supply an explicit environment. ANTHROPIC_API_KEY
// is deliberately absent: in print mode Claude Code always prefers an API key
// over a subscription sign-in, so forwarding it implicitly would silently turn
// a subscription run into a paid one. Configure it explicitly in the profile's
// environment instead.
var extraEnvKeys = []string{
	"CLAUDE_CONFIG_DIR", // selects which signed-in Claude Code profile runs
	"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
	"NODE_EXTRA_CA_CERTS",
}

// requiredFlags must appear in the installed binary's --help output (or, for
// --permission-prompt-tool, in the help text of --permission-prompts). A
// missing flag means the installed version cannot enforce yip's boundary.
var requiredFlags = []string{
	"--print", "--output-format", "--input-format", "--include-partial-messages",
	"--restricted", "--strict-mcp-config", "--mcp-config", "--permission-prompts",
	"--permission-prompt-tool", "--permission-mode", "--tools", "--allowedTools",
	"--disallowedTools", "--disable-slash-commands", "--settings", "--resume",
	"--append-system-prompt", "--model", "--verbose",
}

// Options configures the adapter.
type Options struct {
	// Executable overrides PATH lookup of "claude".
	Executable string
	// Env is the allowlisted environment used for Probe and for runs whose
	// StartSpec.Env is empty. Nil means providers.BaseEnv(extraEnvKeys).
	Env []string
	// ProfileID labels the account/profile this installation belongs to.
	ProfileID string
	// SkipAuthStatus disables `claude auth status --json` during Probe; auth
	// state is then reported as unknown unless an API key is configured.
	SkipAuthStatus bool
	// AllowClaudeMD lets Claude Code auto-load CLAUDE.md files (user and
	// repository). Off by default: yip passes approved project instructions
	// explicitly, and an unfamiliar checkout must not inject instructions
	// implicitly.
	AllowClaudeMD bool
	// TempDir is where per-run MCP config files are written ("" = os.TempDir).
	TempDir string
	// Grace is how long Cancel waits after SIGTERM before SIGKILL (default 5s).
	Grace time.Duration
	// InterruptWait bounds how long Cancel waits for the CLI to end the turn
	// after an interrupt control request (default 5s).
	InterruptWait time.Duration
	// MCPToolIdleTimeout overrides Claude Code's 30-minute idle timeout for
	// stdio MCP tool calls, so a permission prompt can wait for a human
	// decision. Default 24h; set negative to leave Claude Code's default.
	MCPToolIdleTimeout time.Duration
}

// Adapter is the Claude Code provider adapter.
type Adapter struct {
	opts Options

	exitWait  time.Duration // after stdin closes, wait for a clean exit
	drainWait time.Duration // after the leader exits, wait for stdout EOF

	mu        sync.Mutex
	lastProbe *protocol.ProviderInstallation
	missing   []string // required flags absent from the installed binary
}

// New returns a Claude Code adapter.
func New(opts ...Options) providers.Adapter {
	return NewAdapter(opts...)
}

// NewAdapter returns the concrete adapter type (for callers needing options).
func NewAdapter(opts ...Options) *Adapter {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Grace <= 0 {
		o.Grace = 5 * time.Second
	}
	if o.InterruptWait <= 0 {
		o.InterruptWait = 5 * time.Second
	}
	if o.MCPToolIdleTimeout == 0 {
		o.MCPToolIdleTimeout = 24 * time.Hour
	}
	return &Adapter{opts: o, exitWait: defaultExitWait, drainWait: defaultDrainWait}
}

func (a *Adapter) Name() string  { return ProviderName }
func (a *Adapter) Label() string { return "Claude Code" }

// Capabilities are what this adapter implements on the tested version.
func Capabilities() protocol.ProviderCapabilities {
	return protocol.ProviderCapabilities{
		StructuredEvents: true,
		ToolApprovals:    true, // via the yip bridge's MCP permission-prompt tool
		UserQuestions:    false,
		SessionResume:    true,
		ActiveSteering:   true, // queued delivery; see SendInput
		UsageTelemetry:   true,
		Sandbox:          false,
		ModelEnumeration: false,
		ReadOnly:         true,
		EngineerDrafts:   true,
		MCPTools:         true,
	}
}

// Limitations are shown with the installation so the UI can explain them.
func Limitations() []string {
	return []string{
		"Steering input is queued: Claude Code picks it up between tool calls or at the next turn boundary.",
		"Approvals are answered by the yip bridge through Claude Code's --permission-prompt-tool; the adapter emits no approval_request events.",
		"Claude Code's AskUserQuestion tool is not available; questions go through yip room tools.",
		"Models cannot be enumerated non-interactively; the listed aliases come from the CLI's own help text.",
		"Claude Code's Bash sandbox is not enabled by this adapter; use a container profile for OS-level isolation.",
		"On resumed sessions the CLI reports session-cumulative cost, so per-run cost is omitted (token counts are per run).",
	}
}

func (a *Adapter) baseEnv() []string {
	if a.opts.Env != nil {
		return append([]string{}, a.opts.Env...)
	}
	return providers.BaseEnv(extraEnvKeys)
}

// launchEnv builds the run environment: the runner's allowlist (or the
// adapter default) plus fixed Claude Code controls. Nothing else from the
// runner's own environment is added.
func (a *Adapter) launchEnv(specEnv []string) []string {
	env := specEnv
	if len(env) == 0 {
		env = a.baseEnv()
	}
	env = append([]string{}, env...)
	set := func(k, v string) {
		for i, kv := range env {
			if strings.HasPrefix(kv, k+"=") {
				env = append(env[:i], env[i+1:]...)
				break
			}
		}
		env = append(env, k+"="+v)
	}
	setDefault := func(k, v string) {
		if _, ok := envLookup(env, k); !ok {
			env = append(env, k+"="+v)
		}
	}
	// yip owns engineer memory and confidentiality scopes; Claude Code's auto
	// memory would carry context across scopes.
	set("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "1")
	if !a.opts.AllowClaudeMD {
		set("CLAUDE_CODE_DISABLE_CLAUDE_MDS", "1")
	}
	// The pinned version is the tested version; never self-update mid-run.
	set("DISABLE_AUTOUPDATER", "1")
	// Ask for a result message naming why startup was refused.
	set("CLAUDE_CODE_STARTUP_FAILURE_RESULTS", "1")
	if a.opts.MCPToolIdleTimeout > 0 {
		setDefault("CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT", strconv.FormatInt(a.opts.MCPToolIdleTimeout.Milliseconds(), 10))
	}
	return env
}

func envLookup(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func hasAPIKey(env []string) bool {
	v, ok := envLookup(env, "ANTHROPIC_API_KEY")
	return ok && v != ""
}

func hasGatewayBearer(env []string) bool {
	v, ok := envLookup(env, "ANTHROPIC_AUTH_TOKEN")
	return ok && v != ""
}

const gatewayBearerDetail = "Gateway bearer credential is configured; API billing permission is required. Reachability and pricing are unverified."

func (a *Adapter) resolveExecutable(override string) (string, error) {
	if override == "" {
		override = a.opts.Executable
	}
	if override == "" {
		// Resolve against the configured PATH, not only the runner's.
		if p, ok := envLookup(a.baseEnv(), "PATH"); ok && p != "" {
			for _, dir := range strings.Split(p, string(os.PathListSeparator)) {
				if dir == "" {
					continue
				}
				cand := dir + string(os.PathSeparator) + "claude"
				if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
					return cand, nil
				}
			}
		}
	}
	return providers.LookPath(override, "claude")
}

// runCommand runs a short non-interactive CLI command with the adapter's
// environment in a neutral directory (never a repository), returning stdout.
func runCommand(ctx context.Context, exe string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = env
	cmd.Dir = os.TempDir()
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil && out.Len() == 0 && errb.Len() > 0 {
		err = errors.New(strings.TrimSpace(tailClip(errb.String(), 400)))
	}
	return strings.TrimSpace(out.String()), err
}

var versionRE = regexp.MustCompile(`\b(\d+\.\d+\.\d+)\b`)

// authStatus is the JSON printed by `claude auth status --json` on the pinned
// version (documented: JSON output, exit 0 when logged in, 1 when not).
type authStatus struct {
	LoggedIn         bool    `json:"loggedIn"`
	AuthMethod       string  `json:"authMethod"`
	APIProvider      string  `json:"apiProvider"`
	APIKeySource     string  `json:"apiKeySource"`
	Email            *string `json:"email"`
	OrgName          *string `json:"orgName"`
	SubscriptionType *string `json:"subscriptionType"`
}

// Probe discovers the installation. It runs only `claude --version`,
// `claude --help`, and (unless disabled) the documented non-interactive
// `claude auth status --json`; it never reads credential files.
func (a *Adapter) Probe(ctx context.Context) protocol.ProviderInstallation {
	inst := protocol.ProviderInstallation{
		Provider:      ProviderName,
		ProfileID:     a.opts.ProfileID,
		AuthState:     protocol.AuthUnknown,
		Billing:       protocol.BillingUnknown,
		Capabilities:  Capabilities(),
		Models:        []protocol.Model{},
		TestedVersion: TestedVersion,
		Limitations:   Limitations(),
		UpdatedAt:     time.Now().UTC(),
	}
	defer func() {
		a.mu.Lock()
		cp := inst
		a.lastProbe = &cp
		a.mu.Unlock()
	}()

	env := a.baseEnv()
	exe, err := a.resolveExecutable("")
	if err != nil {
		inst.AuthState = protocol.AuthNotInstalled
		inst.AuthDetail = "Claude Code is not installed on this machine (claude not found on PATH)."
		inst.Capabilities = protocol.ProviderCapabilities{}
		return inst
	}
	inst.Path = exe

	out, err := runCommand(ctx, exe, env, "--version")
	if err == nil {
		if m := versionRE.FindStringSubmatch(out); m != nil {
			inst.Version = m[1]
		} else {
			inst.Version = out
		}
	}
	inst.Tested = inst.Version == TestedVersion

	help, herr := runCommand(ctx, exe, env, "--help")
	var missing []string
	if herr == nil {
		for _, f := range requiredFlags {
			if !strings.Contains(help, f) {
				missing = append(missing, f)
			}
		}
		inst.Models = modelsFromHelp(help)
	} else {
		missing = []string{"--help unavailable"}
	}
	a.mu.Lock()
	a.missing = missing
	a.mu.Unlock()
	if len(missing) > 0 {
		inst.Capabilities = protocol.ProviderCapabilities{}
		inst.Limitations = append(inst.Limitations,
			"Installed Claude Code lacks required flags ("+strings.Join(missing, ", ")+"); yip will refuse to start runs with it.")
	}

	apiKey := hasAPIKey(env)
	gatewayBearer := hasGatewayBearer(env)
	if apiKey || gatewayBearer {
		inst.Billing = protocol.BillingAPI
	}
	if a.opts.SkipAuthStatus {
		if apiKey {
			inst.AuthDetail = "ANTHROPIC_API_KEY is configured; Claude Code uses it in print mode (API billed)."
		}
		if gatewayBearer {
			inst.AuthDetail = gatewayBearerDetail
		}
		return inst
	}
	raw, _ := runCommand(ctx, exe, env, "auth", "status", "--json")
	applyAuthStatus(&inst, raw, apiKey, gatewayBearer)
	return inst
}

// applyAuthStatus maps `claude auth status --json` output onto inst.
func applyAuthStatus(inst *protocol.ProviderInstallation, raw string, apiKey, gatewayBearer bool) {
	var st authStatus
	if raw == "" || json.Unmarshal([]byte(raw), &st) != nil {
		inst.AuthState = protocol.AuthUnknown
		inst.AuthDetail = "Could not read `claude auth status --json` output."
		if apiKey || gatewayBearer {
			inst.Billing = protocol.BillingAPI
		}
		if gatewayBearer {
			inst.AuthDetail += " " + gatewayBearerDetail
		}
		return
	}
	if !st.LoggedIn {
		inst.AuthState = protocol.AuthNeedsSignIn
		inst.AuthDetail = "Not signed in. Run `claude auth login` on this machine to sign in with your own Claude account."
		inst.Billing = protocol.BillingUnknown
		return
	}
	inst.AuthState = protocol.AuthReady
	switch st.AuthMethod {
	case "claude.ai", "oauth_token":
		inst.Billing = protocol.BillingSubscription
		inst.AuthDetail = "Signed in with a Claude subscription"
		if st.SubscriptionType != nil && *st.SubscriptionType != "" {
			inst.AuthDetail += " (" + *st.SubscriptionType + ")"
		}
		inst.AuthDetail += "."
		if st.Email != nil && *st.Email != "" {
			inst.Account = *st.Email
		} else if st.OrgName != nil {
			inst.Account = *st.OrgName
		}
	case "api_key", "api_key_helper":
		inst.Billing = protocol.BillingAPI
		inst.AuthDetail = "Using an Anthropic API key (API billed)."
	case "third_party":
		inst.Billing = protocol.BillingAPI
		inst.AuthDetail = "Using a cloud provider (" + st.APIProvider + "), billed by that provider."
	default:
		inst.Billing = protocol.BillingUnknown
		inst.AuthDetail = "Signed in (method " + st.AuthMethod + ")."
	}
	// In print mode an API key in the environment always wins.
	if apiKey {
		inst.Billing = protocol.BillingAPI
		inst.AuthDetail = "ANTHROPIC_API_KEY is configured; Claude Code uses it in print mode (API billed)."
	}
	if gatewayBearer {
		inst.Billing = protocol.BillingAPI
		inst.AuthDetail = gatewayBearerDetail
		inst.Account = ""
	}
}

var aliasSentenceRE = regexp.MustCompile(`alias for the latest model \(e\.g\.([^)]*)\)`)
var quotedRE = regexp.MustCompile(`'([A-Za-z0-9._-]+)'`)

// modelsFromHelp extracts the model aliases the installed CLI names in its
// own --model help text. The empty model (Claude Code's default) is always
// valid and is represented by omitting --model.
func modelsFromHelp(help string) []protocol.Model {
	flat := strings.Join(strings.Fields(help), " ")
	m := aliasSentenceRE.FindStringSubmatch(flat)
	models := []protocol.Model{}
	if m == nil {
		return models
	}
	for _, q := range quotedRE.FindAllStringSubmatch(m[1], -1) {
		models = append(models, protocol.Model{
			ID:          q[1],
			Label:       q[1],
			Description: "Claude Code alias for the latest " + q[1] + " model",
		})
	}
	return models
}

// probeBilling returns the billing label from the most recent Probe.
func (a *Adapter) probeBilling() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastProbe == nil {
		return protocol.BillingUnknown
	}
	return a.lastProbe.Billing
}

func (a *Adapter) missingFlags() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string{}, a.missing...)
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
