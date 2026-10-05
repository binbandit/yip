package opencode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

type pendingPermission struct {
	request *acp.Request
	options []acp.PermissionOption
}

type session struct {
	conn          *acp.Conn
	process       *providers.Process
	input         *os.File
	output        *os.File
	configDir     string
	ctx           context.Context
	stop          context.CancelFunc
	events        chan providers.Event
	done          chan struct{}
	serveDone     chan struct{}
	mu            sync.Mutex
	id            string
	readOnly      bool
	cancelled     bool
	failure       string
	text          strings.Builder
	usage         *protocol.Usage
	pending       map[string]pendingPermission
	tools         map[string]acp.ToolCall
	started       map[string]bool
	finished      map[string]bool
	result        providers.Result
	terminateOnce sync.Once
	processExited bool
	exitUncertain bool
}

func (a *Adapter) Start(ctx context.Context, spec providers.StartSpec) (providers.Session, error) {
	if spec.EngineerDraft && (spec.Mode != protocol.ModeConversation || spec.ResumeSessionID != "" || spec.InheritUserConfig) {
		return nil, fmt.Errorf("%w: invalid engineer draft scope", providers.ErrUnsupported)
	}
	if spec.ResumeSessionID != "" {
		return nil, fmt.Errorf("%w: OpenCode sessions are not resumed across isolated permission profiles", providers.ErrUnsupported)
	}
	if spec.Mode != protocol.ModeEdit && spec.Mode != protocol.ModeReadOnly && spec.Mode != protocol.ModeConversation {
		return nil, fmt.Errorf("opencode: invalid run mode %q", spec.Mode)
	}
	if !filepath.IsAbs(spec.Workdir) {
		return nil, fmt.Errorf("opencode: workdir must be absolute")
	}
	if spec.MCP.Name != "yip" || spec.MCP.Command == "" {
		return nil, fmt.Errorf("opencode: the yip stdio MCP bridge is required")
	}
	exe, err := a.resolve(spec.Executable)
	if err != nil {
		return nil, err
	}
	env, err := baseEnv(spec.Env)
	if err != nil {
		return nil, err
	}
	if err = isolation(env); err != nil {
		return nil, err
	}
	auth, err := command(ctx, exe, env, "auth", "list")
	if err != nil {
		return nil, fmt.Errorf("opencode: `opencode auth list` failed; run it locally to diagnose")
	}
	count, err := authSummary(auth)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("opencode: no local provider sign-in; run `opencode auth login`")
	}
	orgs, err := command(ctx, exe, env, "console", "orgs")
	if err != nil || (orgs != "No accounts found" && orgs != "No orgs found") {
		return nil, fmt.Errorf("%w: OpenCode console organization configuration cannot be safely merged; use a dedicated local provider sign-in profile without console organizations", providers.ErrUnsupported)
	}
	dir, err := os.MkdirTemp("", "yip-opencode-")
	if err != nil {
		return nil, err
	}
	runCtx, stop := context.WithCancel(ctx)
	if spec.Timeout > 0 {
		stop()
		runCtx, stop = context.WithTimeout(ctx, spec.Timeout)
	}
	s := &session{
		configDir: dir, ctx: runCtx, stop: stop, readOnly: spec.Mode != protocol.ModeEdit,
		events: make(chan providers.Event, 1024), done: make(chan struct{}), serveDone: make(chan struct{}),
		pending: map[string]pendingPermission{}, tools: map[string]acp.ToolCall{},
		started: map[string]bool{}, finished: map[string]bool{},
	}
	if err = s.launch(exe, spec, launchEnv(env, dir, spec.Mode, spec.EngineerDraft)); err != nil {
		stop()
		os.RemoveAll(dir)
		return nil, err
	}
	// Closing stdin and killing the host must not depend on a protocol write
	// completing: the agent may stop reading while a large prompt is in flight.
	go func() {
		<-runCtx.Done()
		s.terminate()
	}()
	handshake, cancel := context.WithTimeout(runCtx, 30*time.Second)
	go func() {
		<-handshake.Done()
		if errors.Is(handshake.Err(), context.DeadlineExceeded) {
			s.stop()
		}
	}()
	err = s.setup(handshake, spec)
	cancel()
	if err != nil {
		stop()
		s.cleanup()
		return nil, fmt.Errorf("opencode: ACP setup: %w", err)
	}
	go s.run(spec)
	return s, nil
}

func (s *session) launch(exe string, spec providers.StartSpec, env []string) error {
	// Explicit os.Pipe ownership preserves the final buffered protocol lines.
	inR, inW, err := os.Pipe()
	if err != nil {
		return err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return err
	}
	cmd := exec.Command(exe, "acp", "--cwd", spec.Workdir, "--hostname", "127.0.0.1", "--port", "0")
	cmd.Dir, cmd.Env = spec.Workdir, env
	cmd.Stdin, cmd.Stdout = inR, outW
	cmd.Stderr = providers.NewTailBuffer(32 * 1024)
	s.process, err = providers.StartProcess(cmd)
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return err
	}
	s.input, s.output = inW, outR
	s.conn = acp.NewConn(outR, inW, s)
	s.conn.OnMalformed = func(_ []byte, _ error) { s.fail("OpenCode emitted malformed ACP data") }
	go func() {
		_ = s.conn.Serve()
		close(s.serveDone)
	}()
	return nil
}

func (s *session) setup(ctx context.Context, spec providers.StartSpec) error {
	var init acp.InitializeResult
	if err := s.conn.Call(ctx, acp.MethodInitialize, acp.InitializeParams{
		ProtocolVersion: acp.ProtocolVersion,
		ClientInfo:      &acp.Implementation{Name: "yip", Version: "1"},
	}, &init); err != nil {
		return err
	}
	if init.ProtocolVersion != acp.ProtocolVersion {
		return fmt.Errorf("%w: ACP version %d", providers.ErrUnsupported, init.ProtocolVersion)
	}
	bridge := acp.MCPServerStdio{
		Name: spec.MCP.Name, Command: spec.MCP.Command, Args: append([]string{}, spec.MCP.Args...), Env: []acp.EnvVariable{},
	}
	keys := make([]string, 0, len(spec.MCP.Env))
	for key := range spec.MCP.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bridge.Env = append(bridge.Env, acp.EnvVariable{Name: key, Value: spec.MCP.Env[key]})
	}
	var setup acp.SessionSetup
	servers := []acp.MCPServerStdio{bridge}
	if spec.EngineerDraft {
		servers = []acp.MCPServerStdio{}
	}
	if err := s.conn.Call(ctx, acp.MethodSessionNew, acp.NewSessionParams{
		Cwd: spec.Workdir, MCPServers: servers,
	}, &setup); err != nil {
		return err
	}
	if setup.SessionID == "" {
		return errors.New("session/new returned an empty session ID")
	}
	s.mu.Lock()
	s.id = setup.SessionID
	s.mu.Unlock()
	mode, ok := acp.FindConfig(setup.ConfigOptions, "mode")
	if !ok || !hasValue(mode, "yip") {
		return fmt.Errorf("%w: agent did not advertise the isolated yip mode", providers.ErrUnsupported)
	}
	var configured acp.SetConfigOptionResult
	if err := s.conn.Call(ctx, acp.MethodSessionSetConfig, acp.SetConfigOptionParams{
		SessionID: setup.SessionID, ConfigID: mode.ID, Value: "yip",
	}, &configured); err != nil {
		return err
	}
	selected, ok := acp.FindConfig(configured.ConfigOptions, "mode")
	if !ok || selected.Current() != "yip" {
		return errors.New("agent did not confirm the isolated yip mode")
	}
	if spec.Model != "" {
		model, ok := acp.FindConfig(setup.ConfigOptions, "model")
		if !ok || !hasValue(model, spec.Model) {
			return fmt.Errorf("%w: model %q was not advertised by OpenCode", providers.ErrUnsupported, spec.Model)
		}
		if err := s.conn.Call(ctx, acp.MethodSessionSetConfig, acp.SetConfigOptionParams{
			SessionID: setup.SessionID, ConfigID: model.ID, Value: spec.Model,
		}, &configured); err != nil {
			return err
		}
		selected, ok := acp.FindConfig(configured.ConfigOptions, "model")
		if !ok || selected.Current() != spec.Model {
			return errors.New("agent did not confirm the requested model")
		}
	}
	return nil
}

func hasValue(option acp.ConfigOption, value string) bool {
	for _, item := range option.Values() {
		if item.Value == value {
			return true
		}
	}
	return false
}

func (s *session) emit(event providers.Event) {
	event.At = time.Now().UTC()
	select {
	case s.events <- event:
	case <-s.ctx.Done():
	}
}

func (s *session) run(spec providers.StartSpec) {
	s.emit(providers.Event{Kind: providers.EventVendorSession, VendorSessionID: s.id})
	var result acp.PromptResult
	// The prefix also prevents user text from being interpreted as a slash command.
	prompt := "Engineer instructions:\n" + spec.Instructions +
		"\n\nHarness restrictions: Native shell/code execution, subagents, snapshots, language servers and formatters are disabled. " +
		"Use native read/search and approved edit/write/patch tools to implement code. " +
		"Run tests/build/lint only with yip_work_run_check when available; use the mode-scoped yip publication/review tools for publishing. " +
		"Never try to replace unavailable native commands with another execution tool.\n\nUser request:\n" + spec.Prompt
	err := s.conn.Call(s.ctx, acp.MethodSessionPrompt, acp.PromptParams{
		SessionID: s.id, Prompt: []acp.ContentBlock{acp.TextBlock(prompt)},
	}, &result)
	s.mu.Lock()
	r := providers.Result{VendorSessionID: s.id, FinalText: s.text.String(), Outcome: protocol.OutcomeFailed}
	if result.Usage != nil {
		if s.usage == nil {
			s.usage = &protocol.Usage{Source: "vendor", Billing: protocol.BillingUnknown}
		}
		s.usage.InputTokens = result.Usage.InputTokens
		s.usage.OutputTokens = result.Usage.OutputTokens
	}
	if s.usage != nil {
		copy := *s.usage
		r.Usage = &copy
	}
	switch {
	case s.failure != "":
		r.Error = s.failure
	case s.cancelled || result.StopReason == acp.StopCancelled || errors.Is(s.ctx.Err(), context.Canceled):
		r.Outcome = protocol.OutcomeCancelled
	case errors.Is(s.ctx.Err(), context.DeadlineExceeded):
		r.Outcome, r.Error = protocol.OutcomeFailed, "OpenCode run timed out"
	case err != nil:
		r.Error = err.Error()
		var rpc *acp.Error
		if errors.As(err, &rpc) && rpc.Code == acp.CodeAuthRequired {
			r.Outcome = protocol.OutcomeAuthRequired
		}
	case result.StopReason == acp.StopEndTurn:
		r.Outcome = protocol.OutcomeSucceeded
	default:
		r.Error = "OpenCode stopped with reason: " + result.StopReason
	}
	s.mu.Unlock()
	if r.Usage != nil {
		s.emit(providers.Event{Kind: providers.EventUsage, Usage: r.Usage})
	}
	if r.FinalText != "" {
		s.emit(providers.Event{Kind: providers.EventMessage, Text: r.FinalText})
	}
	if r.Error != "" {
		kind := providers.EventError
		if r.Outcome == protocol.OutcomeAuthRequired {
			kind = providers.EventAuthRequired
		}
		s.emit(providers.Event{Kind: kind, Text: r.Error})
	}
	s.stop()
	exited := s.cleanup()
	s.mu.Lock()
	r.ExitConfirmed = exited && !s.exitUncertain
	s.mu.Unlock()
	s.result = r
	close(s.events)
	close(s.done)
}

func (s *session) terminate() {
	s.terminateOnce.Do(func() {
		s.input.Close()
		s.processExited = s.process.Terminate(time.Second)
		s.output.Close()
	})
}

func (s *session) cleanup() bool {
	s.terminate()
	<-s.serveDone
	s.mu.Lock()
	clear(s.pending)
	s.mu.Unlock()
	os.RemoveAll(s.configDir)
	return s.processExited
}

func (s *session) Events() <-chan providers.Event { return s.events }
func (s *session) fail(message string) {
	s.mu.Lock()
	s.failure = message
	s.mu.Unlock()
	s.stop()
}

func (s *session) Wait() providers.Result {
	<-s.done
	return s.result
}
func (*session) SendInput(context.Context, string) (string, error) {
	return "", fmt.Errorf("%w: OpenCode steering is not enabled", providers.ErrUnsupported)
}
func (*session) AnswerQuestion(context.Context, string, providers.QuestionAnswer) error {
	return fmt.Errorf("%w: OpenCode interactive questions are disabled", providers.ErrUnsupported)
}
func (s *session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	s.cancelled = true
	id := s.id
	s.mu.Unlock()
	// Best effort only; protocol backpressure cannot delay host termination.
	go func() {
		_ = s.conn.Notify(acp.MethodSessionCancel, acp.CancelParams{SessionID: id})
	}()
	s.stop()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) ResolveApproval(_ context.Context, id string, d providers.ApprovalDecision) error {
	s.mu.Lock()
	pending, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("opencode: no pending approval %q", id)
	}
	kind := acp.RejectOnce
	if d.Allow {
		kind = acp.AllowOnce
	}
	for _, option := range pending.options {
		if option.Kind == kind {
			return pending.request.Reply(acp.Selected(option.OptionID))
		}
	}
	err := pending.request.Reply(acp.CancelledPermission())
	if err != nil {
		return err
	}
	if d.Allow {
		return fmt.Errorf("%w: no one-time allow option; permission rejected", providers.ErrUnsupported)
	}
	return nil
}
