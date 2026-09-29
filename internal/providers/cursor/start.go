package cursor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

// ErrUnapprovedProjectConfig is returned (wrapped together with
// providers.ErrUnsupported) when the workspace contains Cursor startup
// configuration that would grant execution authority and the owner has not
// approved it (acceptance A31).
var ErrUnapprovedProjectConfig = errors.New("unapproved repository Cursor configuration")

// gatedProjectFiles are repository files Cursor loads at startup that grant
// execution authority. Documented at https://cursor.com/docs/hooks,
// https://cursor.com/docs/cli/reference/permissions and
// https://cursor.com/docs/cli/acp#mcp-servers.
var gatedProjectFiles = []struct{ path, why string }{
	{".cursor/hooks.json", "project hooks run commands during the agent loop"},
	{".cursor/cli.json", "project CLI permissions can pre-approve shell, write and MCP actions so Cursor never asks yip"},
	{".cursor/mcp.json", "project MCP servers start processes and add tools"},
}

// instructionFiles are loaded by Cursor as rules (https://cursor.com/docs/cli/using#rules).
// They carry no execution authority, so they are reported, not refused.
var instructionFiles = []string{".cursor/rules", "AGENTS.md", "CLAUDE.md", ".cursorrules"}

// checkProjectConfig refuses unapproved execution-granting files and lists
// instruction files Cursor will also read.
func (a *Adapter) checkProjectConfig(workdir string) (notes []string, err error) {
	return a.checkImportedProjectConfig(workdir, false)
}

func (a *Adapter) checkImportedProjectConfig(workdir string, inherit bool) (notes []string, err error) {
	var refused []string
	for _, f := range gatedProjectFiles {
		if inherit && f.path == ".cursor/mcp.json" {
			continue
		}
		if _, statErr := os.Lstat(filepath.Join(workdir, f.path)); statErr == nil && !a.approvedConfig[f.path] {
			refused = append(refused, fmt.Sprintf("%s (%s)", f.path, f.why))
		}
	}
	if len(refused) > 0 {
		return nil, fmt.Errorf("%w: %w: the workspace contains Cursor startup configuration that has not been approved: %s. Cursor has no documented switch to skip it, so yip will not launch Cursor here until the owner approves these files", providers.ErrUnsupported, ErrUnapprovedProjectConfig, strings.Join(refused, "; "))
	}
	var found []string
	for _, f := range instructionFiles {
		if _, statErr := os.Lstat(filepath.Join(workdir, f)); statErr == nil {
			found = append(found, f)
		}
	}
	if len(found) > 0 {
		notes = append(notes, "Cursor will also load repository instruction files as rules: "+strings.Join(found, ", ")+".")
	}
	for _, f := range gatedProjectFiles {
		if a.approvedConfig[f.path] {
			if _, statErr := os.Lstat(filepath.Join(workdir, f.path)); statErr == nil {
				notes = append(notes, "Loading owner-approved repository Cursor configuration: "+f.path+".")
			}
		}
	}
	return notes, nil
}

// buildMCP converts the yip bridge into an ACP stdio MCP server. ACP requires
// an absolute command path.
func buildMCP(m providers.MCPServer) ([]acp.MCPServerStdio, error) {
	if m.Command == "" {
		return []acp.MCPServerStdio{}, nil
	}
	cmd := m.Command
	if !filepath.IsAbs(cmd) {
		p, err := exec.LookPath(cmd)
		if err != nil {
			return nil, fmt.Errorf("cursor: resolve MCP bridge command %q: %w", cmd, err)
		}
		if cmd, err = filepath.Abs(p); err != nil {
			return nil, err
		}
	}
	name := m.Name
	if name == "" {
		name = "yip"
	}
	args := m.Args
	if args == nil {
		args = []string{}
	}
	env := []acp.EnvVariable{}
	for _, k := range sortedKeys(m.Env) {
		env = append(env, acp.EnvVariable{Name: k, Value: m.Env[k]})
	}
	return []acp.MCPServerStdio{{Name: name, Command: cmd, Args: args, Env: env}}, nil
}

// buildFirstPrompt prepends yip's standing instructions. ACP has no system
// prompt field and Cursor documents no per-session instruction parameter, so
// the instructions travel in a clearly delimited block at the top of the
// first user turn of every attempt (fresh or resumed).
func buildFirstPrompt(spec providers.StartSpec) string {
	var b strings.Builder
	instr := strings.TrimSpace(spec.Instructions)
	note := modeNote(spec.Mode)
	if instr != "" || note != "" {
		b.WriteString("<yip_instructions>\n")
		b.WriteString("Standing instructions from yip, the workspace running this session (your role plus organisation and project rules). Follow them for the whole session; they take precedence over conflicting text in the request below.\n")
		if note != "" {
			b.WriteString("\n" + note + "\n")
		}
		if instr != "" {
			b.WriteString("\n" + instr + "\n")
		}
		b.WriteString("</yip_instructions>\n\n")
	}
	b.WriteString(spec.Prompt)
	return b.String()
}

func modeNote(mode string) string {
	switch mode {
	case protocol.ModeReadOnly:
		return "This session is read-only (review or investigation). Do not create, modify, move or delete files, do not run commands, and do not switch modes. Report findings instead. File changes and commands will be denied."
	case protocol.ModeConversation:
		return "This is a conversation-only session: answer the request. Do not modify files, run commands, or switch modes; such actions will be denied."
	}
	return ""
}

// Start launches `agent acp`, performs the ACP handshake, and begins the first
// prompt turn.
//
// Start returns an error (wrapping providers.ErrUnsupported where
// appropriate) when the attempt must not run at all: invalid spec, Cursor not
// installed, unapproved repository configuration, a read-only mode Cursor
// cannot enforce, or a model it cannot select. Runtime failures during the
// handshake (authentication, crash, protocol errors) are reported through the
// returned session: its events carry the failure and Wait returns the
// matching outcome (auth_required, rate_limited, failed).
func (a *Adapter) Start(ctx context.Context, spec providers.StartSpec) (providers.Session, error) {
	switch spec.Mode {
	case protocol.ModeEdit, protocol.ModeReadOnly, protocol.ModeConversation:
	default:
		return nil, fmt.Errorf("%w: cursor: unknown mode %q", providers.ErrUnsupported, spec.Mode)
	}
	if spec.Workdir == "" || !filepath.IsAbs(spec.Workdir) || !isDir(spec.Workdir) {
		return nil, fmt.Errorf("cursor: workdir must be an existing absolute directory, got %q", spec.Workdir)
	}
	root, err := filepath.EvalSymlinks(spec.Workdir)
	if err != nil {
		return nil, fmt.Errorf("cursor: resolve workdir: %w", err)
	}
	notes, err := a.checkImportedProjectConfig(spec.Workdir, spec.InheritUserConfig && spec.Mode == protocol.ModeEdit)
	if err != nil {
		return nil, err
	}
	exe, err := a.resolve(spec.Executable)
	if err != nil {
		return nil, fmt.Errorf("cursor: Cursor CLI not installed (looked for `agent`, `cursor-agent`): %w", err)
	}
	mcp, err := buildMCP(spec.MCP)
	if err != nil {
		return nil, err
	}

	s := newSession(a, spec, root)
	ap, err := launch(exe, spec.Workdir, spec.Env, s)
	if err != nil {
		return nil, fmt.Errorf("cursor: launch %s acp: %w", exe, err)
	}
	s.ap = ap
	for _, n := range notes {
		s.warn(n)
	}

	hctx, cancel := context.WithTimeout(ctx, a.handshakeTimeout)
	err = s.handshake(hctx, mcp)
	cancel()
	if err != nil {
		var sf *startFailure
		switch {
		case ctx.Err() != nil:
			// The caller gave up; nothing ran.
			s.ap.stop(a.eofWait, a.termGrace)
			s.runCancel()
			s.closeEvents()
			close(s.done)
			return nil, fmt.Errorf("cursor: start abandoned: %w", ctx.Err())
		case errors.As(err, &sf):
			// Runtime failure: report through the session.
			s.fail(sf.f)
			s.finish()
			return s, nil
		default:
			// Refusal: the attempt must not run (ErrUnsupported).
			s.ap.stop(a.eofWait, a.termGrace)
			s.runCancel()
			s.closeEvents()
			close(s.done)
			return nil, err
		}
	}

	if spec.Timeout > 0 {
		s.timer = time.AfterFunc(spec.Timeout, func() {
			s.abort(failure{outcome: protocol.OutcomeFailed, message: fmt.Sprintf("Cursor run exceeded its %s timeout and was stopped", spec.Timeout)})
		})
	}
	go s.run(buildFirstPrompt(spec))
	return s, nil
}

// startFailure marks a handshake error that should become a session outcome
// rather than a Start error.
type startFailure struct{ f failure }

func (e *startFailure) Error() string { return e.f.message }

func (s *session) runtimeFailure(stage string, err error) error {
	if errors.Is(err, providers.ErrUnsupported) {
		return err
	}
	if e, ok := rpcError(err); ok {
		f := classifyRPC(e)
		if f.outcome == protocol.OutcomeFailed {
			f.message = fmt.Sprintf("Cursor %s failed: %s", stage, e.Error())
		}
		return &startFailure{f}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// Start checks for caller cancellation first, so this is the
		// adapter's own handshake deadline: a hung agent, reported visibly.
		return &startFailure{failure{outcome: protocol.OutcomeFailed, message: fmt.Sprintf("Cursor did not complete %s within %s. %s", stage, s.a.handshakeTimeout, s.ap.exitSummary())}}
	}
	f := s.classify(err)
	f.message = fmt.Sprintf("Cursor %s failed. %s", stage, f.message)
	return &startFailure{f}
}

func (s *session) handshake(ctx context.Context, mcp []acp.MCPServerStdio) error {
	init, err := s.ap.initialize(ctx)
	if err != nil {
		if ctx.Err() != nil && !errors.Is(err, acp.ErrClosed) {
			return &startFailure{failure{outcome: protocol.OutcomeFailed, message: "Cursor did not answer ACP initialize in time. " + s.ap.exitSummary()}}
		}
		return s.runtimeFailure("initialize", err)
	}
	s.init = init

	// Authentication: Cursor documents `authenticate` with methodId
	// "cursor_login", which uses the existing CLI login or CURSOR_API_KEY /
	// CURSOR_AUTH_TOKEN from the environment. NO_OPEN_BROWSER=1 is set, and
	// the call is bounded, so a signed-out CLI can never drive a browser.
	if hasAuthMethod(init, cursorAuthMethod) {
		actx, cancel := context.WithTimeout(ctx, s.a.authTimeout)
		err := s.ap.conn.Call(actx, acp.MethodAuthenticate, acp.AuthenticateParams{MethodID: cursorAuthMethod}, nil)
		timedOut := actx.Err() != nil
		cancel()
		if err != nil {
			if timedOut && ctx.Err() == nil {
				return &startFailure{failure{outcome: protocol.OutcomeAuthRequired, message: "Cursor did not confirm authentication within " + s.a.authTimeout.String() + " (the CLI is probably not signed in on this machine)"}}
			}
			if e, ok := rpcError(err); ok && e.Code == acp.CodeMethodNotFound {
				s.warn("Cursor advertised the cursor_login auth method but does not implement authenticate; continuing with the existing CLI login.")
			} else if e, ok := rpcError(err); ok && classifyRPC(e).outcome == protocol.OutcomeFailed {
				// Any error from the auth method itself means we are not signed in.
				return &startFailure{failure{outcome: protocol.OutcomeAuthRequired, message: e.Message}}
			} else {
				return s.runtimeFailure("authenticate", err)
			}
		}
	}

	setup, err := s.openSession(ctx, mcp)
	if err != nil {
		return err
	}
	s.emit(providers.Event{Kind: providers.EventVendorSession, VendorSessionID: s.sessionID, Text: s.sessionID})

	if err := s.applyMode(ctx, setup); err != nil {
		return err
	}
	if err := s.applyModel(ctx, setup); err != nil {
		return err
	}
	s.mu.Lock()
	mode := s.currentMode
	s.mu.Unlock()
	status := "Cursor session ready"
	if mode != "" {
		status += " (mode " + mode + ")"
	}
	s.emit(providers.Event{Kind: providers.EventStatus, Text: status})
	return nil
}

// openSession resumes the requested session when Cursor supports it, or
// starts a fresh one.
func (s *session) openSession(ctx context.Context, mcp []acp.MCPServerStdio) (acp.SessionSetup, error) {
	if id := s.spec.ResumeSessionID; id != "" {
		setup, resumed, err := s.resumeSession(ctx, id, mcp)
		if err != nil || resumed {
			return setup, err
		}
	}
	var setup acp.SessionSetup
	if err := s.ap.conn.Call(ctx, acp.MethodSessionNew, acp.NewSessionParams{Cwd: s.spec.Workdir, MCPServers: mcp}, &setup); err != nil {
		return setup, s.runtimeFailure("session/new", err)
	}
	if setup.SessionID == "" {
		return setup, &startFailure{failure{outcome: protocol.OutcomeFailed, message: "Cursor session/new returned no sessionId"}}
	}
	s.mu.Lock()
	s.sessionID = setup.SessionID
	s.mu.Unlock()
	return setup, nil
}

// resumeSession tries session/resume (no replay) or session/load (replay).
// It reports resumed=false, err=nil when the caller should start fresh.
func (s *session) resumeSession(ctx context.Context, id string, mcp []acp.MCPServerStdio) (acp.SessionSetup, bool, error) {
	var setup acp.SessionSetup
	params := acp.LoadSessionParams{SessionID: id, Cwd: s.spec.Workdir, MCPServers: mcp}
	var err error
	switch {
	case s.init.AgentCapabilities.CanResume():
		err = s.ap.conn.Call(ctx, acp.MethodSessionResume, params, &setup)
	case s.init.AgentCapabilities.LoadSession:
		// session/load replays the whole conversation as session/update
		// notifications; suppress them so history is not re-emitted.
		s.mu.Lock()
		s.replaying, s.sessionID = true, id
		s.mu.Unlock()
		err = s.ap.conn.Call(ctx, acp.MethodSessionLoad, params, &setup)
		s.mu.Lock()
		s.replaying, s.sessionID = false, ""
		s.mu.Unlock()
	default:
		s.warn("Cursor did not advertise session/load, so session " + id + " cannot be resumed; starting a fresh session. Earlier conversation context is not restored.")
		return setup, false, nil
	}
	if err == nil {
		s.mu.Lock()
		s.sessionID = id
		s.mu.Unlock()
		s.emit(providers.Event{Kind: providers.EventStatus, Text: "Resumed Cursor session " + id})
		return setup, true, nil
	}
	e, isRPC := rpcError(err)
	if !isRPC || classifyRPC(e).outcome != protocol.OutcomeFailed {
		// Crash, timeout, auth or rate limit: not a reason to start fresh.
		return setup, false, s.runtimeFailure("session resume", err)
	}
	s.warn(fmt.Sprintf("Could not resume Cursor session %s (%v); starting a fresh session. Earlier conversation context is not restored.", id, e))
	return acp.SessionSetup{}, false, nil
}

// applyMode selects Cursor's documented mode for the yip mode: "agent" for
// edit, "ask" (Q&A, read-only) for readonly and conversation. Read-only modes
// refuse to start when the ask mode cannot be selected and verified.
func (s *session) applyMode(ctx context.Context, setup acp.SessionSetup) error {
	want := modeWanted(s.spec.Mode)
	s.mu.Lock()
	s.configOptions = setup.ConfigOptions
	s.mu.Unlock()

	target, current := "", ""
	if opt, ok := acp.FindConfig(setup.ConfigOptions, "mode"); ok && opt.Type == "select" {
		if v, found := matchChoice(want, opt.Values()); found {
			target, current = v, opt.Current()
			s.mu.Lock()
			s.modeConfigID = opt.ID
			s.mu.Unlock()
		}
	}
	if target == "" && setup.Modes != nil {
		vals := make([]acp.ConfigOptionValue, 0, len(setup.Modes.AvailableModes))
		for _, m := range setup.Modes.AvailableModes {
			vals = append(vals, acp.ConfigOptionValue{Value: m.ID, Name: m.Name})
		}
		if v, found := matchChoice(want, vals); found {
			target, current = v, setup.Modes.CurrentModeID
			s.mu.Lock()
			s.modeViaLegacy = true
			s.mu.Unlock()
		}
	}
	if target == "" {
		if s.readOnly {
			return fmt.Errorf("%w: Cursor did not advertise its read-only %q mode for this session (no matching configOptions or modes entry), so a %s session cannot be enforced; refusing to run it unrestricted", providers.ErrUnsupported, want, s.spec.Mode)
		}
		s.warn("Cursor did not advertise an \"agent\" mode; running in the agent's default mode.")
		return nil
	}
	if current != target {
		if err := s.setMode(ctx, target); err != nil {
			if s.readOnly {
				return fmt.Errorf("%w: could not switch Cursor to its read-only %q mode: %v", providers.ErrUnsupported, target, err)
			}
			if _, ok := rpcError(err); !ok {
				return s.runtimeFailure("session mode selection", err)
			}
			s.warn(fmt.Sprintf("Could not switch Cursor to %q mode (%v); running in mode %q.", target, err, current))
			return nil
		}
	}
	s.mu.Lock()
	s.currentMode = target
	if s.readOnly {
		s.wantMode = target
	}
	s.mu.Unlock()
	return nil
}

// applyModel selects the requested model through the agent-reported model
// selector (ACP config option with category "model"; otherwise the unstable
// models block and session/set_model).
func (s *session) applyModel(ctx context.Context, setup acp.SessionSetup) error {
	want := strings.TrimSpace(s.spec.Model)
	if want == "" {
		return nil
	}
	s.mu.Lock()
	opts, sid := s.configOptions, s.sessionID
	s.mu.Unlock()
	if opt, ok := acp.FindConfig(opts, "model"); ok && opt.Type == "select" {
		vals := opt.Values()
		v, found := matchModel(want, vals)
		if !found {
			return fmt.Errorf("%w: model %q is not offered by this Cursor account (available: %s)", providers.ErrUnsupported, want, valueList(vals, 20))
		}
		if opt.Current() != v {
			var r acp.SetConfigOptionResult
			if err := s.ap.conn.Call(ctx, acp.MethodSessionSetConfig, acp.SetConfigOptionParams{SessionID: sid, ConfigID: opt.ID, Value: v}, &r); err != nil {
				if _, ok := rpcError(err); ok {
					return fmt.Errorf("%w: Cursor rejected model %q: %v", providers.ErrUnsupported, v, err)
				}
				return s.runtimeFailure("model selection", err)
			}
			if len(r.ConfigOptions) > 0 {
				s.mu.Lock()
				s.configOptions = r.ConfigOptions
				s.mu.Unlock()
				if o, ok := acp.FindConfig(r.ConfigOptions, "model"); ok && o.Current() != v {
					return fmt.Errorf("%w: Cursor reports model %q after selecting %q", providers.ErrUnsupported, o.Current(), v)
				}
			}
		}
		s.emit(providers.Event{Kind: providers.EventStatus, Text: "Model: " + v})
		return nil
	}
	if setup.Models != nil && len(setup.Models.AvailableModels) > 0 {
		vals := make([]acp.ConfigOptionValue, 0, len(setup.Models.AvailableModels))
		for _, m := range setup.Models.AvailableModels {
			vals = append(vals, acp.ConfigOptionValue{Value: m.ModelID, Name: m.Name})
		}
		v, found := matchModel(want, vals)
		if !found {
			return fmt.Errorf("%w: model %q is not offered by this Cursor account (available: %s)", providers.ErrUnsupported, want, valueList(vals, 20))
		}
		if setup.Models.CurrentModelID != v {
			if err := s.ap.conn.Call(ctx, acp.MethodSessionSetModel, acp.SetModelParams{SessionID: sid, ModelID: v}, nil); err != nil {
				if _, ok := rpcError(err); ok {
					return fmt.Errorf("%w: Cursor could not select model %q: %v", providers.ErrUnsupported, v, err)
				}
				return s.runtimeFailure("model selection", err)
			}
		}
		s.emit(providers.Event{Kind: providers.EventStatus, Text: "Model: " + v})
		return nil
	}
	return fmt.Errorf("%w: Cursor reported no model selector for this session, so model %q cannot be honoured", providers.ErrUnsupported, want)
}
