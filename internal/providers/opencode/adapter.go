// Package opencode runs the official OpenCode CLI over ACP.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

type Option func(*Adapter)

type Adapter struct {
	executable string
	probeEnv   []string
}

func WithExecutable(path string) Option { return func(a *Adapter) { a.executable = path } }
func WithProbeEnv(env []string) Option {
	return func(a *Adapter) { a.probeEnv = append([]string{}, env...) }
}
func New(opts ...Option) providers.Adapter {
	a := &Adapter{}
	for _, opt := range opts {
		opt(a)
	}
	return a
}
func (*Adapter) Name() string  { return "opencode" }
func (*Adapter) Label() string { return "OpenCode" }

func (a *Adapter) resolve(override string) (string, error) {
	if override == "" {
		override = a.executable
	}
	return providers.LookPath(override, "opencode")
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
var credentialCount = regexp.MustCompile(`(?m)(?:^|[^0-9])([0-9]+) credentials\s*$`)
var credentialType = regexp.MustCompile(`(?m)\s(api|oauth)\s*$`)

// Permit registry releases without allowing file, URL, git, or package aliases.
var registryRelease = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// command never invokes a shell or inherits the ambient API environment.
func command(ctx context.Context, exe string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	out := providers.NewTailBuffer(128 * 1024)
	cmd.Stdout, cmd.Stderr = out, out
	p, err := providers.StartProcess(cmd)
	if err != nil {
		return "", err
	}
	select {
	case <-p.Done():
		err = p.Err()
	case <-ctx.Done():
		p.Terminate(time.Second)
		err = ctx.Err()
	}
	return strings.TrimSpace(ansi.ReplaceAllString(out.String(), "")), err
}

func baseEnv(env []string) ([]string, error) {
	if env == nil {
		env = providers.BaseEnv(nil)
	}
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("opencode: malformed environment entry")
		}
		// Configuration and runtime injection would defeat the controlled agent.
		if strings.HasPrefix(key, "OPENCODE_") || key == "NODE_OPTIONS" || key == "BUN_OPTIONS" ||
			strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") {
			return nil, fmt.Errorf("opencode: unsafe launch environment key %s", key)
		}
		out = append(out, kv)
	}
	return append(out, "NO_COLOR=1", "TERM=dumb"), nil
}

func envValue(env []string, key string) string {
	var value string
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			value = strings.TrimPrefix(kv, key+"=")
		}
	}
	return value
}

func authSummary(text string) (count int, err error) {
	if strings.Contains(strings.ToLower(text), "wellknown") {
		return 0, fmt.Errorf("opencode: remote-config sign-ins are not supported; use a normal provider sign-in in a dedicated local profile")
	}
	m := credentialCount.FindStringSubmatch(text)
	if m == nil {
		return 0, fmt.Errorf("opencode: could not verify `opencode auth list` credential summary")
	}
	count, err = strconv.Atoi(m[1])
	return
}

func (a *Adapter) Probe(ctx context.Context) protocol.ProviderInstallation {
	p := protocol.ProviderInstallation{
		Provider: "opencode", AuthState: protocol.AuthUnknown, Billing: protocol.BillingUnknown,
		Models: []protocol.Model{}, UpdatedAt: time.Now().UTC(),
		Limitations: []string{
			"Stored sign-in is not proof that a model subscription is valid. Billing depends on the selected provider.",
			"Dedicated configuration; no remote/managed configuration, plugins, resume, active steering, or interactive questions.",
			"Native shell, code execution, subagents and snapshots are disabled; use yip's runner-supervised check and publish tools.",
		},
	}
	exe, err := a.resolve("")
	if err != nil {
		p.AuthState = protocol.AuthNotInstalled
		p.AuthDetail = "Install OpenCode from https://opencode.ai/docs/cli/, then run `opencode auth login` locally."
		return p
	}
	p.Path = exe
	env, err := baseEnv(a.probeEnv)
	if err != nil {
		p.AuthState, p.AuthDetail = protocol.AuthError, err.Error()
		return p
	}
	p.Version, err = command(ctx, exe, env, "--version")
	if err != nil {
		p.Version = ""
	}
	p.Capabilities = protocol.ProviderCapabilities{
		StructuredEvents: true, ToolApprovals: true, UsageTelemetry: true, ReadOnly: true, MCPTools: true,
	}
	text, err := command(ctx, exe, env, "auth", "list")
	if err != nil {
		p.AuthState, p.AuthDetail = protocol.AuthError, "`opencode auth list` failed; run it locally to diagnose."
		return p
	}
	n, err := authSummary(text)
	if err != nil {
		p.AuthDetail = err.Error()
	} else if n == 0 {
		p.AuthState, p.AuthDetail = protocol.AuthNeedsSignIn, "Run `opencode auth login` on this runner as the same OS user, and choose the provider for your model."
	} else {
		if err := isolation(env); err != nil {
			p.AuthDetail = err.Error()
			return p
		}
		orgs, err := command(ctx, exe, env, "console", "orgs")
		if err != nil || (orgs != "No accounts found" && orgs != "No orgs found") {
			p.AuthDetail = "Console organization configuration is unsupported; use a dedicated local provider sign-in profile."
			return p
		}
		p.AuthState = protocol.AuthReady
		p.Billing = authBilling(text, n)
		p.AuthDetail = "Local provider sign-in found by `opencode auth list`; credential validity is unknown until a real run. ACP authentication does not validate credentials."
	}
	return p
}

func authBilling(text string, count int) string {
	types := credentialType.FindAllStringSubmatch(text, -1)
	for _, match := range types {
		if match[1] == "api" {
			return protocol.BillingAPI
		}
	}
	if len(types) == count && count > 0 {
		return protocol.BillingSubscription
	}
	return protocol.BillingUnknown
}

// isolation rejects startup sources that OpenCode can merge after or alongside
// the inline configuration. Credential files are never opened.
func isolation(env []string) error {
	home := envValue(env, "HOME")
	if !filepath.IsAbs(home) {
		return fmt.Errorf("opencode: an absolute HOME is required for local sign-in")
	}
	if err := checkHomeExtensions(filepath.Join(home, ".opencode")); err != nil {
		return err
	}
	paths := []string{"/etc/opencode"}
	if runtime.GOOS == "darwin" {
		paths = append(paths, "/Library/Application Support/opencode", "/Library/Managed Preferences/ai.opencode.managed.plist")
		u, err := user.Current()
		if err != nil {
			return fmt.Errorf("opencode: cannot check managed preferences: %w", err)
		}
		paths = append(paths, filepath.Join("/Library/Managed Preferences", u.Username, "ai.opencode.managed.plist"))
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%w: opencode startup configuration %s must be absent in the runner profile", providers.ErrUnsupported, path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("opencode: cannot check startup configuration %s: %w", path, err)
		}
	}
	return nil
}

// The official installer uses ~/.opencode/bin. OpenCode also creates dependency
// scaffolding there; neither is itself an agent/plugin configuration.
func checkHomeExtensions(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opencode: cannot inspect home extension directory: %w", err)
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		switch entry.Name() {
		case "bin", "node_modules", ".gitignore", "bun.lock", "bun.lockb", "package-lock.json":
			continue
		case "package.json":
			raw, err := os.ReadFile(path)
			var pkg map[string]json.RawMessage
			if err == nil && json.Unmarshal(raw, &pkg) == nil && len(pkg) == 1 {
				var deps map[string]string
				if json.Unmarshal(pkg["dependencies"], &deps) == nil &&
					len(deps) == 1 && registryRelease.MatchString(deps["@opencode-ai/plugin"]) {
					continue
				}
			}
		}
		return fmt.Errorf("%w: home OpenCode extension/configuration %s is not supported by the isolated runner", providers.ErrUnsupported, path)
	}
	return nil
}

func launchEnv(env []string, dir, mode string) []string {
	permission := map[string]string{
		"*": "deny", "read": "allow", "glob": "allow", "grep": "allow",
		"bash": "deny", "shell": "deny", "execute": "deny", "batch": "deny",
		"question": "deny", "task": "deny", "plan_enter": "deny", "plan_exit": "deny",
	}
	if mode == protocol.ModeEdit {
		permission["edit"] = "ask"
		permission["external_directory"] = "ask"
		permission["webfetch"], permission["websearch"] = "ask", "ask"
	}
	// The bridge independently validates mode and authority. Commands run
	// through its supervised checks, never OpenCode's detached native shell.
	for _, name := range bridge.NamesForMode(mode) {
		permission["yip_"+name] = "allow"
	}
	config := map[string]any{
		"default_agent": "yip",
		"permission":    permission,
		// Even reads can warm language servers, and edits can run formatters.
		// Neither background path goes through the runner's tool approval.
		"lsp":       false,
		"formatter": false,
		"snapshot":  false,
		"agent": map[string]any{
			"yip": map[string]any{
				"description": "Runner-controlled engineer", "mode": "primary", "permission": permission,
			},
			"build":   map[string]any{"disable": true},
			"plan":    map[string]any{"disable": true},
			"general": map[string]any{"disable": true},
			"explore": map[string]any{"disable": true},
		},
	}
	raw, _ := json.Marshal(config)
	out := []string{}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			out = append(out, kv)
		}
	}
	return append(out, "XDG_CONFIG_HOME="+dir, "OPENCODE_DISABLE_PROJECT_CONFIG=true",
		"OPENCODE_CONFIG_CONTENT="+string(raw))
}
