// Package pi runs Pi Agent Harness through its supported local Node SDK.
package pi

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

const ProviderName = "pi"
const TestedVersion = "0.87.1"
const LegacyTestedVersion = "0.73.1"

//go:embed host.mjs
var hostSource []byte

type Options struct {
	Executable     string
	NodeExecutable string
	Env            []string
	ProfileID      string
	TempDir        string
	Grace          time.Duration
}

type Adapter struct{ opts Options }

func New(opts ...Options) providers.Adapter { return NewAdapter(opts...) }
func NewAdapter(opts ...Options) *Adapter {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Grace <= 0 {
		o.Grace = time.Second
	}
	return &Adapter{opts: o}
}
func (*Adapter) Name() string  { return ProviderName }
func (*Adapter) Label() string { return "Pi Agent Harness" }
func Capabilities() protocol.ProviderCapabilities {
	return protocol.ProviderCapabilities{
		StructuredEvents: true, ToolApprovals: true, ActiveSteering: true,
		UsageTelemetry: true, ModelEnumeration: true, ReadOnly: true, MCPTools: true,
	}
}
func Limitations() []string {
	return []string{
		"Requires Node.js 22.19+ and @earendil-works/pi-coding-agent 0.87.1 (or legacy @mariozechner/pi-coding-agent 0.73.1) installed with npm; other versions are refused.",
		"Pi is a harness, not a subscription. Run pi then /login on this runner; choose provider/model in yip.",
		"Authentication readiness means locally configured, not remotely validated; probing never refreshes tokens or calls a model.",
		"Current Pi discovery requires credentials configured with Pi /login; environment-only and custom-provider configurations are not enumerated.",
		"Custom extensions, packages, models.json, settings, skills, prompt templates, themes and automatic context files are not loaded.",
		"Read-only exposes read/grep/find/ls and scoped yip bridge tools, never bash/edit/write. This is not an OS sandbox.",
		"Every native bash/edit/write operation needs runner approval; yip bridge authorization remains authoritative.",
		"Bash uses /bin/bash without startup files in the supervised process group; a bash timeout ends the attempt so descendants are terminated.",
		"Sessions are ephemeral: resume is unsupported; steering is queued at Pi's next tool/turn boundary.",
		"Mixed OAuth/API installations report API billing conservatively. Cost is vendor-estimated, not a subscription charge.",
	}
}
func (a *Adapter) env(override []string) []string {
	var env []string
	if override != nil {
		env = override
	} else if a.opts.Env != nil {
		env = a.opts.Env
	} else {
		// No ambient model API keys are implicitly forwarded.
		env = providers.BaseEnv([]string{"PI_CODING_AGENT_DIR"})
	}
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		// Do not permit module preloading to bypass the fixed SDK host.
		if key != "NODE_OPTIONS" && key != "NODE_PATH" {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}

// Resolve only package metadata, never account files. A pinned SDK is required
// because tool-allowlist semantics are a security boundary.
func (a *Adapter) installation(override string) (exe, sdk, version string, err error) {
	if override == "" {
		override = a.opts.Executable
	}
	exe, err = providers.LookPath(override, "pi")
	if err != nil {
		return
	}
	resolved, e := filepath.EvalSymlinks(exe)
	if e != nil {
		err = e
		return
	}
	root := filepath.Dir(filepath.Dir(resolved))
	var metadata struct{ Name, Version string }
	b, e := os.ReadFile(filepath.Join(root, "package.json"))
	if e != nil {
		err = fmt.Errorf("Pi must be an npm installation: %w", e)
		return
	}
	if e = json.Unmarshal(b, &metadata); e != nil {
		err = e
		return
	}
	version = metadata.Version
	current := metadata.Name == "@earendil-works/pi-coding-agent" && version == TestedVersion
	legacy := metadata.Name == "@mariozechner/pi-coding-agent" && version == LegacyTestedVersion
	if !current && !legacy {
		err = fmt.Errorf("%w: require @earendil-works/pi-coding-agent@%s or @mariozechner/pi-coding-agent@%s, found %s@%s", providers.ErrUnsupported, TestedVersion, LegacyTestedVersion, metadata.Name, version)
		return
	}
	sdk = filepath.Join(root, "dist", "index.js")
	_, err = os.Stat(sdk)
	return
}

func (a *Adapter) launch(spec providers.StartSpec) (*session, error) {
	_, sdk, _, err := a.installation(spec.Executable)
	if err != nil {
		return nil, err
	}
	node, err := providers.LookPath(a.opts.NodeExecutable, "node")
	if err != nil {
		return nil, fmt.Errorf("Pi SDK requires Node.js: %w", err)
	}
	dir, err := os.MkdirTemp(a.opts.TempDir, "yip-pi-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	host := filepath.Join(dir, "host.mjs")
	if err := os.WriteFile(host, hostSource, 0600); err != nil {
		cleanup()
		return nil, err
	}
	cmd := exec.Command(node, host, sdk)
	cmd.Env = a.env(spec.Env)
	cmd.Dir = spec.Workdir
	if cmd.Dir == "" {
		cmd.Dir = dir
	}
	s, err := startSession(cmd, cleanup, a.opts.Grace)
	if err != nil {
		cleanup()
	}
	return s, err
}

func (a *Adapter) Probe(ctx context.Context) protocol.ProviderInstallation {
	inst := protocol.ProviderInstallation{
		Provider: ProviderName, ProfileID: a.opts.ProfileID, AuthState: protocol.AuthUnknown,
		Billing: protocol.BillingUnknown, Models: []protocol.Model{},
		TestedVersion: TestedVersion, Limitations: Limitations(), UpdatedAt: time.Now().UTC(),
	}
	exe, _, version, err := a.installation("")
	inst.Path, inst.Version = exe, version
	if err != nil {
		inst.AuthDetail = err.Error()
		if exe == "" {
			inst.AuthState = protocol.AuthNotInstalled
		}
		return inst
	}
	inst.Tested = true
	inst.TestedVersion = version
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s, err := a.launch(providers.StartSpec{})
	if err != nil {
		inst.AuthDetail = err.Error()
		return inst
	}
	_ = s.write(map[string]any{"type": "probe"})
	select {
	case reply := <-s.probe:
		inst.Capabilities = Capabilities()
		inst.Models, inst.Billing = reply.Models, reply.Billing
		inst.AuthState = protocol.AuthNeedsSignIn
		inst.AuthDetail = "Run pi, then /login on this runner. Select a provider/model in yip."
		if len(inst.Models) > 0 {
			inst.AuthState = protocol.AuthReady
			inst.AuthDetail = "Pi SDK reports configured local authentication (not remotely validated)."
		}
	case <-s.done:
		inst.AuthDetail = s.Wait().Error
	case <-ctx.Done():
		inst.AuthDetail = "Pi SDK probe timed out or was cancelled"
	}
	_ = s.Cancel(context.Background())
	<-s.done
	return inst
}

func (a *Adapter) Start(ctx context.Context, spec providers.StartSpec) (providers.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if spec.ResumeSessionID != "" {
		return nil, fmt.Errorf("%w: Pi resume is not implemented", providers.ErrUnsupported)
	}
	switch spec.Mode {
	case protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation:
	default:
		return nil, fmt.Errorf("%w: invalid Pi mode %q", providers.ErrUnsupported, spec.Mode)
	}
	if spec.MCP.Command == "" {
		return nil, fmt.Errorf("Pi requires the yip bridge")
	}
	if spec.Model != "" && !strings.Contains(spec.Model, "/") {
		return nil, fmt.Errorf("Pi model must be provider/model")
	}
	s, err := a.launch(spec)
	if err != nil {
		return nil, err
	}
	// Supervise before writing: a large prompt can fill stdin while SDK import
	// is stalled, and cancellation must still close that pipe and kill the host.
	go func() {
		runCtx := ctx
		cancel := func() {}
		if spec.Timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		}
		defer cancel()
		select {
		case <-runCtx.Done():
			_ = s.Cancel(context.Background())
		case <-s.done:
		}
	}()
	if err = s.writeContext(ctx, map[string]any{"type": "start", "spec": spec}); err != nil {
		_ = s.Cancel(context.Background())
		return nil, err
	}
	return s, nil
}
