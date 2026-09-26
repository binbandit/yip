// Package cursor implements the yip provider adapter for the Cursor CLI over
// its native Agent Client Protocol server (`agent acp`).
//
// Documented interfaces used (read 2026-09-25):
//   - https://cursor.com/docs/cli/acp (launch, auth method, modes, permission
//     options, Cursor extension methods cursor/ask_question,
//     cursor/create_plan, cursor/update_todos, cursor/task,
//     cursor/generate_image)
//   - https://cursor.com/docs/cli/reference/authentication (agent login,
//     agent status, CURSOR_API_KEY)
//   - https://cursor.com/docs/cli/reference/parameters (global options,
//     `models`, `status --format json`)
//   - https://agentclientprotocol.com/protocol/v1/overview (ACP v1)
//
// This adapter has NOT been exercised against a real Cursor installation; it
// is verified only against a fake ACP agent in the tests. See
// docs/providers/cursor.md.
package cursor

import (
	"context"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

const (
	providerName  = "cursor"
	providerLabel = "Cursor"

	// untestedLimitation is always reported: no real Cursor CLI was available
	// when this adapter was written.
	untestedLimitation = "Not yet exercised against a real Cursor CLI installation; behaviour is verified only against a fake ACP agent built from Cursor's and ACP's documentation."
)

// executableNames are resolved in order. `agent` is the documented binary
// name (https://cursor.com/docs/cli/installation); `cursor-agent` is the
// older name some installations still provide.
var executableNames = []string{"agent", "cursor-agent"}

// Option configures the adapter.
type Option func(*Adapter)

// WithExecutable pins the Cursor CLI path instead of resolving it on PATH.
func WithExecutable(path string) Option { return func(a *Adapter) { a.executable = path } }

// WithProbeEnv sets the allowlisted environment used by Probe (Start uses
// StartSpec.Env). Defaults to providers.BaseEnv(nil).
func WithProbeEnv(env []string) Option { return func(a *Adapter) { a.probeEnv = env } }

// WithApprovedProjectConfig marks repository-level Cursor startup files
// (paths relative to the workdir, e.g. ".cursor/mcp.json") as explicitly
// approved by the owner. Unapproved files that grant execution authority
// make Start refuse to launch. See checkProjectConfig.
func WithApprovedProjectConfig(paths ...string) Option {
	return func(a *Adapter) {
		for _, p := range paths {
			a.approvedConfig[p] = true
		}
	}
}

// WithTimeouts overrides the handshake timeout and the grace period Cancel
// gives the agent to acknowledge session/cancel before the process group is
// terminated.
func WithTimeouts(handshake, cancelGrace time.Duration) Option {
	return func(a *Adapter) {
		if handshake > 0 {
			a.handshakeTimeout = handshake
		}
		if cancelGrace > 0 {
			a.cancelGrace = cancelGrace
		}
	}
}

// WithUserConfigDir overrides where the documented user-level CLI config
// (cli-config.json) is looked up. Defaults to $CURSOR_CONFIG_DIR or ~/.cursor.
func WithUserConfigDir(dir string) Option { return func(a *Adapter) { a.userConfigDir = dir } }

// Adapter is the Cursor provider adapter.
type Adapter struct {
	executable       string
	probeEnv         []string
	approvedConfig   map[string]bool
	handshakeTimeout time.Duration
	authTimeout      time.Duration
	cancelGrace      time.Duration
	eofWait          time.Duration
	termGrace        time.Duration
	userConfigDir    string
}

// New returns the Cursor adapter.
func New(opts ...Option) providers.Adapter {
	return newAdapter(opts...)
}

func newAdapter(opts ...Option) *Adapter {
	a := &Adapter{
		approvedConfig:   map[string]bool{},
		handshakeTimeout: 2 * time.Minute,
		authTimeout:      45 * time.Second,
		cancelGrace:      5 * time.Second,
		eofWait:          3 * time.Second,
		termGrace:        3 * time.Second,
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

func (a *Adapter) Name() string  { return providerName }
func (a *Adapter) Label() string { return providerLabel }

func (a *Adapter) resolve(override string) (string, error) {
	if override == "" {
		override = a.executable
	}
	return providers.LookPath(override, executableNames...)
}

// launchEnv returns the environment for the agent process: the allowlisted
// environment plus NO_OPEN_BROWSER=1, which Cursor documents for its login
// flow, so an unauthenticated agent can never pop a browser from a runner.
func launchEnv(env []string) []string {
	if env == nil {
		env = providers.BaseEnv(nil)
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "NO_OPEN_BROWSER=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "NO_OPEN_BROWSER=1")
}

// Probe reports installation, authentication and negotiated capabilities.
func (a *Adapter) Probe(ctx context.Context) protocol.ProviderInstallation {
	inst := protocol.ProviderInstallation{
		Provider:      providerName,
		AuthState:     protocol.AuthUnknown,
		Billing:       protocol.BillingUnknown,
		TestedVersion: "",
		Tested:        false,
		Models:        []protocol.Model{},
		Limitations:   []string{untestedLimitation},
		UpdatedAt:     time.Now().UTC(),
	}
	exe, err := a.resolve("")
	if err != nil {
		inst.AuthState = protocol.AuthNotInstalled
		inst.AuthDetail = "Cursor CLI not found: looked for `agent` and `cursor-agent` on PATH. Install it with `curl https://cursor.com/install -fsS | bash` (see https://cursor.com/docs/cli/installation), make sure ~/.local/bin is on the runner's PATH, then sign in with `agent login`."
		return inst
	}
	inst.Path = exe
	return a.probeInstalled(ctx, exe, inst)
}
