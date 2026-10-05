package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// Kinds of pending server requests, used to build the right response.
const (
	pendCommand     = "command"
	pendFileChange  = "fileChange"
	pendPermissions = "permissions"
	pendMCPApproval = "mcpApproval"
	pendElicitForm  = "elicitationForm"
	pendUserInput   = "userInput"
	pendLegacyExec  = "legacyExec"
	pendLegacyPatch = "legacyPatch"
)

type pendingRequest struct {
	rpcID     json.RawMessage
	kind      string
	perms     permissionProfile   // requested profile (permissions approvals)
	questions []userInputQuestion // user input questions
	formField string              // single string field for form elicitations
}

type session struct {
	a     *Adapter
	spec  providers.StartSpec
	srv   *server
	c     *conn
	cfg   configReadResponse // effective config layers read before the thread
	rules []string           // exec-policy rule files Codex will load

	evq    *queue[providers.Event]
	events chan providers.Event
	done   chan struct{}
	stop   chan struct{}
	result providers.Result

	mu              sync.Mutex
	threadID        string
	activeTurn      string
	ourTurns        map[string]bool
	queued          []string
	pending         map[string]*pendingRequest // adapter approval/question id → request
	byRPC           map[string]string          // rpc id key → adapter id
	items           map[string]threadItem      // started items (for approval context)
	lastAgentText   string
	billing         string
	usageBaseline   *tokenUsageBreakdown
	usageTotal      *tokenUsageBreakdown
	rateLimits      *rateLimitSnapshot
	lastErr         *turnError
	cancelRequested bool
	timedOut        bool
	ended           bool
	finishOnce      sync.Once
}

func newSession(a *Adapter, spec providers.StartSpec, srv *server, cfg configReadResponse, rules []string) *session {
	s := &session{
		a:        a,
		spec:     spec,
		srv:      srv,
		c:        srv.c,
		cfg:      cfg,
		rules:    rules,
		evq:      newQueue[providers.Event](),
		events:   make(chan providers.Event, 64),
		done:     make(chan struct{}),
		stop:     make(chan struct{}),
		ourTurns: map[string]bool{},
		pending:  map[string]*pendingRequest{},
		byRPC:    map[string]string{},
		items:    map[string]threadItem{},
		billing:  protocol.BillingUnknown,
	}
	go s.pump()
	return s
}

// ---- providers.Session

func (s *session) Events() <-chan providers.Event { return s.events }

func (s *session) Wait() providers.Result {
	<-s.done
	return s.result
}

// SendInput steers the active turn with turn/steer (delivered immediately
// into the running turn). With no steerable active turn, the text is queued
// and sent as the next turn when the current one completes.
func (s *session) SendInput(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("codex: empty input")
	}
	s.mu.Lock()
	if s.ended || s.cancelRequested {
		s.mu.Unlock()
		return "", errors.New("codex: session has ended")
	}
	thread, turnID := s.threadID, s.activeTurn
	s.mu.Unlock()

	if thread != "" && turnID != "" {
		ctx, cancel := context.WithTimeout(ctx, s.a.timeouts.Request)
		defer cancel()
		var resp turnSteerResponse
		err := s.c.call(ctx, methodTurnSteer, turnSteerParams{
			ThreadID: thread, ExpectedTurnID: turnID,
			Input: []userInput{{Type: "text", Text: text}},
		}, &resp)
		if err == nil {
			return "immediate", nil
		}
		var rerr *rpcError
		if !errors.As(err, &rerr) {
			return "", fmt.Errorf("codex: turn/steer: %w", err)
		}
		// The turn finished, changed, or is not steerable (review/compaction):
		// fall through and queue for the next turn boundary.
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || s.cancelRequested {
		return "", errors.New("codex: session has ended")
	}
	s.queued = append(s.queued, text)
	return "queued", nil
}

func (s *session) ResolveApproval(ctx context.Context, id string, d providers.ApprovalDecision) error {
	p, err := s.take(id)
	if err != nil {
		return err
	}
	var result any
	switch p.kind {
	case pendCommand, pendFileChange:
		dec := decisionDecline
		if d.Allow {
			dec = decisionAccept
		}
		result = approvalResponse{Decision: dec}
	case pendPermissions:
		granted := permissionProfile{}
		if d.Allow {
			granted = p.perms
		}
		result = permissionsApprovalResponse{Permissions: granted, Scope: "turn"}
	case pendMCPApproval:
		if d.Allow {
			result = elicitationResponse{Action: "accept", Content: map[string]any{}}
		} else {
			result = elicitationResponse{Action: "decline"}
		}
	case pendLegacyExec, pendLegacyPatch:
		if d.Allow {
			result = approvalResponse{Decision: "approved"}
		} else {
			reason := d.Reason
			if reason == "" {
				reason = "Denied in yip"
			}
			result = approvalResponse{Decision: map[string]any{"denied": map[string]string{"rejection": reason}}}
		}
	default:
		return fmt.Errorf("codex: %s is not an approval request", id)
	}
	if err := s.c.respond(p.rpcID, result); err != nil {
		return fmt.Errorf("codex: deliver approval decision: %w", err)
	}
	return nil
}

func (s *session) AnswerQuestion(ctx context.Context, id string, a providers.QuestionAnswer) error {
	p, err := s.take(id)
	if err != nil {
		return err
	}
	var result any
	switch p.kind {
	case pendUserInput:
		answers := map[string]userInputAnswer{}
		if !a.Declined {
			for i, q := range p.questions {
				// Free text wins; otherwise the selected option of the first
				// question (the one whose options were offered).
				ans := a.Text
				if ans == "" && i == 0 && a.Selected >= 0 && a.Selected < len(q.Options) {
					ans = q.Options[a.Selected].Label
				}
				answers[q.ID] = userInputAnswer{Answers: []string{ans}}
			}
		}
		result = userInputResponse{Answers: answers}
	case pendElicitForm:
		if a.Declined {
			result = elicitationResponse{Action: "decline"}
		} else {
			result = elicitationResponse{Action: "accept", Content: map[string]string{p.formField: a.Text}}
		}
	default:
		return fmt.Errorf("codex: %s is not a question", id)
	}
	if err := s.c.respond(p.rpcID, result); err != nil {
		return fmt.Errorf("codex: deliver answer: %w", err)
	}
	return nil
}

// Cancel interrupts the active turn through the protocol (turn/interrupt),
// waits briefly for Codex to confirm, then terminates the process group.
func (s *session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return nil
	}
	s.cancelRequested = true
	thread, turnID := s.threadID, s.activeTurn
	s.mu.Unlock()

	if thread != "" && turnID != "" {
		ictx, cancel := context.WithTimeout(ctx, s.a.timeouts.Interrupt)
		err := s.c.call(ictx, methodTurnInterrupt, turnInterruptParams{ThreadID: thread, TurnID: turnID}, nil)
		cancel()
		if err == nil {
			select {
			case <-s.done:
				return nil
			case <-time.After(s.a.timeouts.Interrupt):
			case <-ctx.Done():
			}
		}
	}
	// No active turn, interrupt failed, or Codex did not confirm in time:
	// terminate the process group; the session loop then finishes.
	s.srv.proc.Terminate(s.a.timeouts.Grace)
	select {
	case <-s.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (s *session) take(id string) (*pendingRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	if !ok {
		return nil, fmt.Errorf("codex: request %s is no longer pending", id)
	}
	delete(s.pending, id)
	delete(s.byRPC, idKey(p.rpcID))
	return p, nil
}

// ---- event plumbing

func (s *session) emit(ev providers.Event) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	s.evq.push(ev)
}

// pump forwards queued events to the public channel and closes it after
// the terminal event.
func (s *session) pump() {
	for {
		ev, ok := s.evq.pop(nil)
		if !ok {
			close(s.events)
			return
		}
		s.events <- ev
	}
}

func (s *session) warn(text string) {
	s.emit(providers.Event{Kind: providers.EventWarning, Text: text})
}

// ---- lifecycle

func (s *session) run() {
	if s.spec.Timeout > 0 {
		t := time.AfterFunc(s.spec.Timeout, func() {
			s.mu.Lock()
			s.timedOut = true
			s.mu.Unlock()
			_ = s.Cancel(context.Background())
		})
		defer t.Stop()
	}
	if res, ok := s.setup(); !ok {
		s.finish(res)
		return
	}
	s.loop()
}

// setup performs auth check, startup inspection, thread start/resume and
// the first turn. On failure it returns the terminal result.
func (s *session) setup() (providers.Result, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), s.a.timeouts.Handshake)
	defer cancel()

	var acct getAccountResponse
	if err := s.c.call(ctx, methodAccountRead, getAccountParams{RefreshToken: false}, &acct); err != nil {
		return s.setupFailure("account/read", err), false
	}
	state, detail, _, billing := classifyConfiguredAccount(acct, s.cfg, s.spec.Env)
	s.mu.Lock()
	s.billing = billing
	s.mu.Unlock()
	if state == protocol.AuthNeedsSignIn {
		return providers.Result{Outcome: protocol.OutcomeAuthRequired, Error: detail}, false
	}

	disable, err := s.inspectStartup(ctx)
	if err != nil {
		return s.setupFailure("startup inspection", err), false
	}

	params := s.threadParams(disable)
	method := methodThreadStart
	if s.spec.ResumeSessionID != "" {
		method = methodThreadResume
		params.ThreadID = s.spec.ResumeSessionID
	}
	var ts threadStartResponse
	if err := s.c.call(ctx, method, params, &ts); err != nil {
		return s.setupFailure(method, err), false
	}
	if ts.Thread.ID == "" {
		return providers.Result{Outcome: protocol.OutcomeFailed, Error: "codex: " + method + " returned no thread id"}, false
	}
	s.mu.Lock()
	s.threadID = ts.Thread.ID
	s.mu.Unlock()
	s.emit(providers.Event{Kind: providers.EventVendorSession, VendorSessionID: ts.Thread.ID})

	if err := s.verifyThread(ts); err != nil {
		return providers.Result{Outcome: protocol.OutcomeFailed, Error: err.Error(), VendorSessionID: ts.Thread.ID}, false
	}
	if err := s.startTurn(ctx, s.spec.Prompt); err != nil {
		return s.setupFailure(methodTurnStart, err), false
	}
	return providers.Result{}, true
}

func (s *session) setupFailure(step string, err error) providers.Result {
	s.mu.Lock()
	cancelled, timedOut, thread := s.cancelRequested, s.timedOut, s.threadID
	s.mu.Unlock()
	if timedOut {
		return providers.Result{Outcome: protocol.OutcomeFailed, Error: "codex: timed out during " + step, VendorSessionID: thread}
	}
	if cancelled {
		return providers.Result{Outcome: protocol.OutcomeCancelled, VendorSessionID: thread}
	}
	return providers.Result{Outcome: protocol.OutcomeFailed, Error: fmt.Sprintf("codex: %s: %s", step, s.srv.describe(err)), VendorSessionID: thread}
}

// threadParams builds thread/start (or thread/resume) parameters.
func (s *session) threadParams(disableMCP []string) threadParams {
	p := threadParams{
		Model:                 s.spec.Model,
		DeveloperInstructions: s.spec.Instructions,
		ApprovalPolicy:        approvalPolicy(s.spec.Mode),
		ApprovalsReviewer:     approvalsReviewerUser,
		Sandbox:               sandboxMode(s.spec.Mode),
		Config:                threadConfig(s.spec, disableMCP, trustKeys(s.spec.Workdir)),
	}
	// thread/start deliberately omits cwd: the app-server process already
	// runs in the workdir, and app-server only records a project as trusted
	// in the user's config.toml when thread/start *includes* a cwd (and no
	// trust entry exists). The explicit "untrusted" entry in Config also
	// suppresses that write. Resume must name the (possibly new) workdir.
	if s.spec.ResumeSessionID != "" {
		p.Cwd = s.spec.Workdir
	}
	return p
}

func sandboxMode(mode string) string {
	if mode == protocol.ModeEdit {
		return sandboxWorkspaceWrite
	}
	return sandboxReadOnly
}

// approvalPolicy never combines "never ask" with full access.
//
//   - edit: "on-request". Commands run inside Codex's workspace-write
//     sandbox; anything that must leave it (escalation, network, writes
//     outside the workdir) becomes an approval request answered by yip.
//   - readonly / conversation: granular policy that forbids sandbox
//     escalation, exec-policy prompts, permission grants and skill
//     approvals outright (Codex reports them to the model as forbidden), so
//     nothing can leave the read-only sandbox even if someone would approve
//     it. MCP elicitations stay enabled so MCP prompts still reach yip.
func approvalPolicy(mode string) any {
	if mode == protocol.ModeEdit {
		return approvalOnRequest
	}
	return map[string]any{"granular": map[string]any{
		"sandbox_approval":    false,
		"rules":               false,
		"request_permissions": false,
		"skill_approval":      false,
		"mcp_elicitations":    true,
	}}
}

// threadConfig is the per-thread config override map (applied by Codex as
// session-flag overrides, the same layer as `-c`). It
//   - adds the yip MCP server (auto-approved at the Codex level: the hub
//     authorizes every yip tool call against the run lease),
//   - disables every other MCP server found in any config layer,
//   - marks the workdir (and its git roots) untrusted so repository
//     `.codex/` config, hooks and rules are not loaded, and
//   - pins the workspace-write sandbox to Codex's defaults (no network, no
//     extra writable roots) regardless of the user's config.
func threadConfig(spec providers.StartSpec, disableMCP, trust []string) map[string]any {
	name := mcpName(spec)
	server := map[string]any{
		"command":                     spec.MCP.Command,
		"args":                        nonNil(spec.MCP.Args),
		"enabled":                     true,
		"required":                    true,
		"default_tools_approval_mode": "approve",
		"startup_timeout_sec":         30,
		// yip tools such as work_run_check can legitimately run for a while.
		"tool_timeout_sec": 3600,
	}
	if len(spec.MCP.Env) > 0 {
		server["env"] = spec.MCP.Env
	}
	// One nested key (not dotted keys): names may contain dots, and Codex
	// applies the override map in unspecified order, so keys must not
	// overlap. Tables deep-merge with the user's config, so a disabled
	// server keeps its definition but does not start.
	servers := map[string]any{name: server}
	for _, n := range disableMCP {
		if n != name && !(spec.InheritUserConfig && spec.Mode == protocol.ModeEdit) {
			servers[n] = map[string]any{"enabled": false}
		}
	}
	cfg := map[string]any{"mcp_servers": servers}
	if len(trust) > 0 {
		// Nested (not dotted) because the paths contain dots.
		projects := map[string]any{}
		for _, k := range trust {
			projects[k] = map[string]any{"trust_level": "untrusted"}
		}
		cfg["projects"] = projects
	}
	cfg["sandbox_workspace_write"] = map[string]any{
		"network_access":         false,
		"writable_roots":         []string{},
		"exclude_tmpdir_env_var": false,
		"exclude_slash_tmp":      false,
	}
	return cfg
}

func mcpName(spec providers.StartSpec) string {
	if spec.MCP.Name != "" {
		return spec.MCP.Name
	}
	return "yip"
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// trustKeys lists the paths Codex consults for project trust: the workdir,
// its project root (nearest ancestor with .git), and for a linked worktree
// the main checkout root — each as given and canonicalized.
func trustKeys(workdir string) []string {
	seen := map[string]bool{}
	var keys []string
	add := func(p string) {
		if p == "" {
			return
		}
		for _, k := range []string{filepath.Clean(p), canonical(p)} {
			if k != "" && !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	add(workdir)
	for dir := filepath.Clean(workdir); ; dir = filepath.Dir(dir) {
		gitPath := filepath.Join(dir, ".git")
		if st, err := os.Lstat(gitPath); err == nil {
			add(dir)
			if !st.IsDir() {
				add(mainWorktreeRoot(gitPath))
			}
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return keys
}

// mainWorktreeRoot resolves a linked worktree's `.git` file to the main
// checkout root via gitdir + commondir. It returns "" when not a worktree.
func mainWorktreeRoot(gitFile string) string {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(b))
	gitdir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(gitFile), gitdir)
	}
	common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return ""
	}
	cd := strings.TrimSpace(string(common))
	if !filepath.IsAbs(cd) {
		cd = filepath.Join(gitdir, cd)
	}
	cd = filepath.Clean(cd)
	if filepath.Base(cd) != ".git" {
		return ""
	}
	return filepath.Dir(cd)
}

func canonical(p string) string {
	c, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return c
}

// inspectStartup reviews the config layers read in Start and the hooks Codex
// discovered for the workdir, before any thread exists. It returns the MCP
// server names to disable and refuses to continue when a runnable,
// non-managed hook exists.
func (s *session) inspectStartup(ctx context.Context) ([]string, error) {
	cfg := s.cfg
	// The effective config can include other servers' env values; keep only
	// what is needed (names) and drop the rest.
	s.cfg = configReadResponse{}
	if len(s.rules) > 0 {
		// Only reachable in edit mode (Start refuses read-only modes).
		s.warn("Codex exec-policy rules apply to this run: commands they allow run outside the sandbox without asking yip (" + strings.Join(s.rules, ", ") + ")")
	}
	names := map[string]bool{}
	collectMCPNames(cfg.Config, names)
	var projectDirs []string
	for _, l := range cfg.Layers {
		// Include disabled (e.g. untrusted project) layers too: their
		// servers must stay off even if trust changes.
		collectMCPNames(l.Config, names)
		if l.Name.Type == "project" {
			projectDirs = append(projectDirs, l.Name.DotCodexFolder)
		}
	}
	if len(projectDirs) > 0 {
		s.emit(providers.Event{Kind: providers.EventStatus, Text: "Ignoring repository Codex config (" + strings.Join(projectDirs, ", ") + "): yip runs treat the checkout as untrusted"})
	}
	if names[mcpName(s.spec)] {
		s.warn("Codex config already defines an MCP server named " + mcpName(s.spec) + "; yip's definition overrides its command, args and env for this run")
	}
	var disable []string
	for n := range names {
		if n != mcpName(s.spec) {
			disable = append(disable, n)
		}
	}
	sort.Strings(disable)
	if len(disable) > 0 {
		s.emit(providers.Event{Kind: providers.EventStatus, Text: "Disabled Codex MCP servers for this run: " + strings.Join(disable, ", ")})
	}

	var hooks hooksListResponse
	if err := s.c.call(ctx, methodHooksList, hooksListParams{Cwds: []string{s.spec.Workdir}}, &hooks); err != nil {
		return nil, err
	}
	var blocked []string
	for _, e := range hooks.Data {
		for _, h := range e.Hooks {
			if !h.Enabled {
				continue
			}
			if h.IsManaged || h.TrustStatus == hookTrustManaged {
				s.emit(providers.Event{Kind: providers.EventStatus, Text: fmt.Sprintf("Codex managed %s hook is active (%s)", h.EventName, h.SourcePath)})
				continue
			}
			if h.TrustStatus == hookTrustTrusted {
				blocked = append(blocked, fmt.Sprintf("%s hook from %s (%s)", h.EventName, h.SourcePath, h.Source))
			}
		}
	}
	if len(blocked) > 0 {
		return nil, fmt.Errorf("refusing to start: Codex would run hooks yip has not approved: %s. Disable them in Codex (hooks.state) or remove them", strings.Join(blocked, "; "))
	}
	return disable, nil
}

func collectMCPNames(raw json.RawMessage, into map[string]bool) {
	if len(raw) == 0 {
		return
	}
	var c struct {
		MCPServers map[string]json.RawMessage `json:"mcp_servers"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return
	}
	for n := range c.MCPServers {
		into[n] = true
	}
}

// verifyThread checks that Codex applied the boundary yip asked for.
func (s *session) verifyThread(ts threadStartResponse) error {
	if ts.Cwd != "" && !samePath(ts.Cwd, s.spec.Workdir) {
		return fmt.Errorf("codex: thread cwd %q does not match the workdir %q; refusing to run", ts.Cwd, s.spec.Workdir)
	}
	if ts.ApprovalsReviewer != "" && ts.ApprovalsReviewer != approvalsReviewerUser {
		return fmt.Errorf("codex: approvals would be reviewed by %q instead of yip; refusing to run", ts.ApprovalsReviewer)
	}
	want := "workspaceWrite"
	if s.spec.Mode != protocol.ModeEdit {
		want = "readOnly"
	}
	sb := ts.Sandbox
	if sb.Type != "" && sb.Type != want {
		return fmt.Errorf("codex: requested %s sandbox but Codex applied %q; refusing to run", want, sb.Type)
	}
	if string(sb.NetworkAccess) == "true" {
		return fmt.Errorf("codex: sandbox would allow network access; refusing to run")
	}
	var extra []string
	for _, r := range sb.WritableRoots {
		// Temp dirs are writable by design in workspace-write; yip pins
		// writable_roots to [], so anything else comes from Codex itself or
		// managed policy. Report it rather than hide it.
		if !within(r, s.spec.Workdir) && !within(r, "/tmp") && !within(r, os.TempDir()) {
			extra = append(extra, r)
		}
	}
	if len(extra) > 0 {
		s.warn("Codex's sandbox also allows writes to: " + strings.Join(extra, ", "))
	}
	summary := "Codex sandbox: read-only, network off"
	if want == "workspaceWrite" {
		summary = "Codex sandbox: workspace-write (workdir and temp dirs; .git read-only), network off"
	}
	s.emit(providers.Event{Kind: providers.EventStatus, Text: summary})
	if len(ts.InstructionSources) > 0 {
		s.emit(providers.Event{Kind: providers.EventStatus, Text: "Codex loaded instructions from " + strings.Join(ts.InstructionSources, ", ")})
	}
	return nil
}

// within reports whether p is dir or below it.
func within(p, dir string) bool {
	if samePath(p, dir) {
		return true
	}
	cp, cd := canonical(p), canonical(dir)
	if cp == "" {
		cp = filepath.Clean(p)
	}
	if cd == "" {
		cd = filepath.Clean(dir)
	}
	r, err := filepath.Rel(cd, cp)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	ca, err1 := filepath.EvalSymlinks(a)
	cb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ca == cb
}

func (s *session) startTurn(ctx context.Context, text string) error {
	s.mu.Lock()
	thread := s.threadID
	s.mu.Unlock()
	var resp turnStartResponse
	if err := s.c.call(ctx, methodTurnStart, turnStartParams{ThreadID: thread, Input: []userInput{{Type: "text", Text: text}}}, &resp); err != nil {
		return err
	}
	s.mu.Lock()
	s.ourTurns[resp.Turn.ID] = true
	if resp.Turn.Status == "" || resp.Turn.Status == turnInProgress {
		s.activeTurn = resp.Turn.ID
	}
	s.mu.Unlock()
	return nil
}

// loop consumes server notifications and requests until the session ends.
func (s *session) loop() {
	for {
		msg, ok := s.c.inbound.pop(s.stop)
		if !ok {
			s.finish(s.connectionLost())
			return
		}
		if len(msg.ID) > 0 && msg.Method != "" {
			s.handleRequest(msg)
			continue
		}
		if res, end := s.handleNotification(msg); end {
			s.finish(res)
			return
		}
	}
}

// connectionLost maps an unexpected app-server exit to a result.
func (s *session) connectionLost() providers.Result {
	s.mu.Lock()
	cancelled, timedOut, thread := s.cancelRequested, s.timedOut, s.threadID
	s.mu.Unlock()
	switch {
	case timedOut:
		return providers.Result{Outcome: protocol.OutcomeFailed, Error: "codex: run timed out", VendorSessionID: thread}
	case cancelled:
		return providers.Result{Outcome: protocol.OutcomeCancelled, VendorSessionID: thread}
	}
	// Wait for the exit status so the message is accurate.
	select {
	case <-s.srv.proc.Done():
	case <-time.After(2 * time.Second):
	}
	msg := "codex app-server exited unexpectedly"
	if s.srv.proc.Exited() {
		msg = fmt.Sprintf("%s (exit code %d)", msg, providers.ExitCode(s.srv.proc.Err()))
	}
	if tail := strings.TrimSpace(s.srv.stderr.String()); tail != "" {
		msg += ": " + lastLines(tail, 12)
	}
	return providers.Result{Outcome: protocol.OutcomeFailed, Error: msg, VendorSessionID: thread}
}

// finish records the terminal result, shuts the process group down, emits
// terminal events and closes the event stream. It runs exactly once.
func (s *session) finish(res providers.Result) {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.ended = true
		if res.VendorSessionID == "" {
			res.VendorSessionID = s.threadID
		}
		if res.FinalText == "" {
			res.FinalText = s.lastAgentText
		}
		if res.Usage == nil {
			res.Usage = s.usageLocked()
		}
		// Anything still pending can no longer be answered.
		s.pending = map[string]*pendingRequest{}
		s.byRPC = map[string]string{}
		s.mu.Unlock()

		switch res.Outcome {
		case protocol.OutcomeRateLimited:
			s.emit(providers.Event{Kind: providers.EventRateLimited, Text: res.Error, RetryAfter: res.RetryAfter})
		case protocol.OutcomeAuthRequired:
			s.emit(providers.Event{Kind: providers.EventAuthRequired, Text: res.Error})
		case protocol.OutcomeFailed:
			if res.Error != "" {
				s.emit(providers.Event{Kind: providers.EventError, Text: res.Error})
			}
		}

		close(s.stop)
		res.ExitConfirmed = s.srv.shutdown(s.a.timeouts.Grace)
		s.result = res
		s.evq.close()
		close(s.done)
	})
}

// ---- usage

func (s *session) usageLocked() *protocol.Usage {
	if s.usageTotal == nil {
		return nil
	}
	in, out := s.usageTotal.InputTokens, s.usageTotal.OutputTokens
	if b := s.usageBaseline; b != nil {
		in -= b.InputTokens
		out -= b.OutputTokens
	}
	if in < 0 {
		in = 0
	}
	if out < 0 {
		out = 0
	}
	return &protocol.Usage{Source: "vendor", InputTokens: &in, OutputTokens: &out, Billing: s.billing}
}

// ---- notifications

func (s *session) ours(threadID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return threadID == "" || threadID == s.threadID
}

func (s *session) handleNotification(msg rpcMessage) (providers.Result, bool) {
	switch msg.Method {
	case ntfAgentDelta:
		var n agentMessageDelta
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) && n.Delta != "" {
			s.emit(providers.Event{Kind: providers.EventMessageDelta, Text: n.Delta})
		}
	case ntfItemStarted:
		var n itemNotification
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			s.itemStarted(n.Item)
		}
	case ntfItemCompleted:
		var n itemNotification
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			s.itemCompleted(n.Item)
		}
	case ntfTokenUsage:
		var n tokenUsageUpdated
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			s.tokenUsage(n)
		}
	case ntfTurnStarted:
		var n turnNotification
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			s.mu.Lock()
			if s.ourTurns[n.Turn.ID] {
				s.activeTurn = n.Turn.ID
			}
			s.mu.Unlock()
		}
	case ntfTurnCompleted:
		var n turnNotification
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			return s.turnCompleted(n.Turn)
		}
	case ntfError:
		var n errorNotification
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			if n.WillRetry {
				s.warn("Codex is retrying: " + n.Error.Message)
			} else {
				e := n.Error
				s.mu.Lock()
				s.lastErr = &e
				s.mu.Unlock()
			}
		}
	case ntfRateLimits:
		var n accountRateLimitsUpdated
		if json.Unmarshal(msg.Params, &n) == nil {
			s.mu.Lock()
			s.rateLimits = mergeSnapshot(s.rateLimits, n.RateLimits)
			s.mu.Unlock()
		}
	case ntfTurnPlan:
		var n planUpdated
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			for _, st := range n.Plan {
				if st.Status == "inProgress" {
					s.emit(providers.Event{Kind: providers.EventStatus, Text: truncate(st.Step, 160)})
					break
				}
			}
		}
	case ntfWarning, ntfGuardianWarning:
		var n warningNotification
		if json.Unmarshal(msg.Params, &n) == nil && (n.ThreadID == nil || s.ours(*n.ThreadID)) {
			s.warn(n.Message)
		}
	case ntfDeprecation:
		var n struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal(msg.Params, &n) == nil && n.Summary != "" {
			s.warn("Codex: " + n.Summary)
		}
	case ntfConfigWarning:
		var n configWarningNotification
		if json.Unmarshal(msg.Params, &n) == nil {
			text := "Codex config: " + n.Summary
			if n.Path != nil {
				text += " (" + *n.Path + ")"
			}
			s.warn(text)
		}
	case ntfMCPStartup:
		var n mcpStartupNotification
		if json.Unmarshal(msg.Params, &n) == nil && (n.ThreadID == nil || s.ours(*n.ThreadID)) {
			name := s.spec.MCP.Name
			if name == "" {
				name = "yip"
			}
			switch {
			case n.Name == name && n.Status == "failed":
				detail := ""
				if n.Error != nil {
					detail = ": " + *n.Error
				}
				s.warn("yip tools are unavailable to Codex: MCP server failed to start" + detail)
			case n.Name != name && (n.Status == "starting" || n.Status == "ready"):
				// Should not happen: every other server is disabled.
				s.warn("Codex started an MCP server yip did not configure: " + n.Name)
			}
		}
	case ntfHookStarted:
		var n hookStartedNotification
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			s.warn(fmt.Sprintf("Codex ran a %s hook from %s", n.Run.EventName, n.Run.SourcePath))
		}
	case ntfModelRerouted:
		var n modelReroutedNotification
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) {
			s.warn(fmt.Sprintf("Codex rerouted the request from %s to %s (%s)", n.FromModel, n.ToModel, n.Reason))
		}
	case ntfThreadStatus:
		var n threadStatusNotification
		if json.Unmarshal(msg.Params, &n) == nil && s.ours(n.ThreadID) && n.Status.Type == "systemError" {
			s.warn("Codex reported a system error on the thread")
		}
	case ntfRequestResolved:
		var n requestResolvedNotification
		if json.Unmarshal(msg.Params, &n) == nil {
			s.mu.Lock()
			if id, ok := s.byRPC[idKey(n.RequestID)]; ok {
				delete(s.byRPC, idKey(n.RequestID))
				delete(s.pending, id)
				s.mu.Unlock()
				s.emit(providers.Event{Kind: providers.EventStatus, Text: "Codex withdrew request " + id})
			} else {
				s.mu.Unlock()
			}
		}
	case "yip/invalidLine":
		s.warn("codex app-server wrote a non-JSON line on stdout")
	}
	return providers.Result{}, false
}

func mergeSnapshot(prev *rateLimitSnapshot, n rateLimitSnapshot) *rateLimitSnapshot {
	if prev == nil {
		return &n
	}
	m := *prev
	if n.Primary != nil {
		m.Primary = n.Primary
	}
	if n.Secondary != nil {
		m.Secondary = n.Secondary
	}
	if n.RateLimitReachedType != nil {
		m.RateLimitReachedType = n.RateLimitReachedType
	}
	return &m
}

func (s *session) tokenUsage(n tokenUsageUpdated) {
	s.mu.Lock()
	total := n.TokenUsage.Total
	if !s.ourTurns[n.TurnID] {
		// Replayed usage of a resumed thread: it becomes the baseline so the
		// run reports only its own consumption.
		if len(s.ourTurns) == 0 || s.usageTotal == nil {
			s.usageBaseline = &total
		}
		s.mu.Unlock()
		return
	}
	s.usageTotal = &total
	u := s.usageLocked()
	s.mu.Unlock()
	s.emit(providers.Event{Kind: providers.EventUsage, Usage: u})
}

func (s *session) turnCompleted(t turn) (providers.Result, bool) {
	s.mu.Lock()
	if !s.ourTurns[t.ID] {
		s.mu.Unlock()
		return providers.Result{}, false
	}
	if s.activeTurn == t.ID {
		s.activeTurn = ""
	}
	cancelled, timedOut := s.cancelRequested, s.timedOut
	lastErr := s.lastErr
	s.lastErr = nil
	var next []string
	if t.Status == turnCompleted && !cancelled {
		next, s.queued = s.queued, nil
	}
	if len(next) == 0 {
		// This turn ends the session. Mark it now, under the same lock that
		// guards the queue, so a concurrent SendInput cannot report
		// "queued" for input that would never be delivered.
		s.ended = true
	}
	s.mu.Unlock()

	switch t.Status {
	case turnCompleted:
		if cancelled {
			return providers.Result{Outcome: protocol.OutcomeCancelled}, true
		}
		if len(next) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), s.a.timeouts.Request)
			err := s.startTurn(ctx, strings.Join(next, "\n\n"))
			cancel()
			if err != nil {
				return providers.Result{Outcome: protocol.OutcomeFailed, Error: "codex: start queued turn: " + s.srv.describe(err)}, true
			}
			s.emit(providers.Event{Kind: providers.EventStatus, Text: "Delivered queued input as a new turn"})
			return providers.Result{}, false
		}
		return providers.Result{Outcome: protocol.OutcomeSucceeded}, true
	case turnInterrupted:
		if timedOut {
			return providers.Result{Outcome: protocol.OutcomeFailed, Error: "codex: run timed out"}, true
		}
		res := providers.Result{Outcome: protocol.OutcomeCancelled}
		if !cancelled {
			res.Error = "Codex interrupted the turn"
		}
		return res, true
	default: // failed
		e := t.Error
		if e == nil {
			e = lastErr
		}
		return s.classifyFailure(e), true
	}
}

// classifyFailure maps a Codex turn error to an outcome. Rate limits carry
// a RetryAfter only when Codex supplied reset data.
func (s *session) classifyFailure(e *turnError) providers.Result {
	if e == nil {
		return providers.Result{Outcome: protocol.OutcomeFailed, Error: "Codex reported a failed turn without details"}
	}
	msg := e.Message
	if e.AdditionalDetails != nil && *e.AdditionalDetails != "" {
		msg += " (" + *e.AdditionalDetails + ")"
	}
	kind, status := errorInfo(e.CodexErrorInfo)
	switch {
	case kind == errUsageLimitExceeded || status == 429:
		res := providers.Result{Outcome: protocol.OutcomeRateLimited, Error: msg}
		res.RetryAfter = s.retryAfter()
		return res
	case kind == errUnauthorized || status == 401:
		return providers.Result{Outcome: protocol.OutcomeAuthRequired, Error: msg}
	}
	return providers.Result{Outcome: protocol.OutcomeFailed, Error: msg}
}

// errorInfo decodes CodexErrorInfo: either a bare string variant or a
// single-key object carrying an optional httpStatusCode.
func errorInfo(raw json.RawMessage) (kind string, httpStatus int) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", 0
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str, 0
	}
	var obj map[string]struct {
		HTTPStatusCode *int `json:"httpStatusCode"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		for k, v := range obj {
			if v.HTTPStatusCode != nil {
				return k, *v.HTTPStatusCode
			}
			return k, 0
		}
	}
	return "", 0
}

// retryAfter derives a wait from vendor-reported reset times: the latest
// snapshot seen on account/rateLimits/updated, else a fresh
// account/rateLimits/read. It returns 0 when Codex supplied nothing usable.
func (s *session) retryAfter() time.Duration {
	s.mu.Lock()
	snap := s.rateLimits
	s.mu.Unlock()
	if d := resetWait(snap, time.Now()); d > 0 {
		return d
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var resp getAccountRateLimitsResponse
	if err := s.c.call(ctx, methodRateLimitsRead, nil, &resp); err != nil {
		return 0
	}
	best := resetWait(&resp.RateLimits, time.Now())
	for _, snap := range resp.RateLimitsByLimitID {
		snap := snap
		if d := resetWait(&snap, time.Now()); d > best {
			best = d
		}
	}
	return best
}

// resetWait returns the time until the exhausted window resets. Only
// windows at (or above) 100% count; with a reached-limit marker and no
// exhausted window, the latest future reset is used.
func resetWait(snap *rateLimitSnapshot, now time.Time) time.Duration {
	if snap == nil {
		return 0
	}
	var best time.Duration
	consider := func(w *rateLimitWindow, force bool) {
		if w == nil || w.ResetsAt == nil {
			return
		}
		if w.UsedPercent < 100 && !force {
			return
		}
		if d := time.Unix(*w.ResetsAt, 0).Sub(now); d > best {
			best = d
		}
	}
	consider(snap.Primary, false)
	consider(snap.Secondary, false)
	if best == 0 && snap.RateLimitReachedType != nil {
		consider(snap.Primary, true)
		consider(snap.Secondary, true)
	}
	return best
}

// ---- items → tool events

// rel shows p relative to the workdir when it lies inside it. Codex reports
// canonical paths, so both the given and the canonical workdir are tried.
func (s *session) rel(p string) string {
	if p == "" {
		return p
	}
	for _, base := range []string{s.spec.Workdir, canonical(s.spec.Workdir)} {
		if base == "" {
			continue
		}
		r, err := filepath.Rel(base, p)
		if err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return r
		}
	}
	return p
}

func (s *session) paths(changes []fileUpdateChange) string {
	var ps []string
	for _, c := range changes {
		ps = append(ps, s.rel(c.Path))
	}
	if len(ps) > 4 {
		return strings.Join(ps[:4], ", ") + fmt.Sprintf(" (+%d more)", len(ps)-4)
	}
	return strings.Join(ps, ", ")
}

func toolName(it threadItem) string {
	switch it.Type {
	case "commandExecution":
		return "shell"
	case "fileChange":
		return "edit"
	case "mcpToolCall":
		return it.Server + "." + it.Tool
	case "dynamicToolCall":
		return it.Tool
	case "webSearch":
		return "web_search"
	}
	return it.Type
}

func (s *session) itemStarted(it threadItem) {
	s.mu.Lock()
	s.items[it.ID] = it
	s.mu.Unlock()
	data := rawJSON(map[string]any{"itemId": it.ID, "type": it.Type})
	switch it.Type {
	case "commandExecution":
		s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: "shell", Text: "Running `" + truncate(it.Command, 160) + "`", Data: data})
	case "fileChange":
		s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: "edit", Text: "Editing " + s.paths(it.Changes), Data: data})
	case "mcpToolCall", "dynamicToolCall":
		s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: toolName(it), Text: "Calling " + toolName(it), Data: data})
	case "webSearch":
		s.emit(providers.Event{Kind: providers.EventToolStarted, Tool: "web_search", Text: "Searching the web", Data: data})
	}
}

func (s *session) itemCompleted(it threadItem) {
	s.mu.Lock()
	delete(s.items, it.ID)
	s.mu.Unlock()
	fields := map[string]any{"itemId": it.ID, "type": it.Type, "status": it.Status}
	if it.ExitCode != nil {
		fields["exitCode"] = *it.ExitCode
	}
	data := rawJSON(fields)
	switch it.Type {
	case "agentMessage":
		s.mu.Lock()
		s.lastAgentText = it.Text
		s.mu.Unlock()
		s.emit(providers.Event{Kind: providers.EventMessage, Text: it.Text})
	case "commandExecution":
		cmd := "`" + truncate(it.Command, 160) + "`"
		var text string
		switch it.Status {
		case "declined":
			text = "Declined " + cmd
		case "failed":
			text = "Failed " + cmd
		default:
			text = "Ran " + cmd
		}
		if it.ExitCode != nil {
			text += fmt.Sprintf(" (exit %d)", *it.ExitCode)
		}
		s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: "shell", Text: text, Data: data})
	case "fileChange":
		verb := "Edited "
		switch it.Status {
		case "declined":
			verb = "Edit declined: "
		case "failed":
			verb = "Edit failed: "
		}
		s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: "edit", Text: verb + s.paths(it.Changes), Data: data})
	case "mcpToolCall", "dynamicToolCall":
		name := toolName(it)
		text := name + " finished"
		if it.Status == "failed" {
			text = name + " failed"
			if it.Err != nil && it.Err.Message != "" {
				text += ": " + truncate(it.Err.Message, 200)
			}
		}
		s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: name, Text: text, Data: data})
	case "webSearch":
		text := "Searched the web"
		if it.Query != "" {
			text += " for " + truncate(it.Query, 120)
		}
		s.emit(providers.Event{Kind: providers.EventToolFinished, Tool: "web_search", Text: text, Data: data})
	}
}

// ---- server requests

func (s *session) readOnly() bool { return s.spec.Mode != protocol.ModeEdit }

// register records a pending request and returns its adapter-scoped id.
func (s *session) register(p *pendingRequest) string {
	id := "codex-" + idKey(p.rpcID)
	s.mu.Lock()
	s.pending[id] = p
	s.byRPC[idKey(p.rpcID)] = id
	s.mu.Unlock()
	return id
}

func (s *session) askApproval(p *pendingRequest, action protocol.ApprovalAction, raw json.RawMessage) {
	id := s.register(p)
	s.emit(providers.Event{
		Kind:     providers.EventApprovalRequest,
		Text:     action.Summary,
		Approval: &providers.ApprovalRequest{ID: id, Action: action, Raw: raw},
	})
}

// autoDecline answers a request that the run's mode forbids without asking.
func (s *session) autoDecline(msg rpcMessage, result any, why string) {
	_ = s.c.respond(msg.ID, result)
	s.warn(why)
}

func (s *session) handleRequest(msg rpcMessage) {
	switch msg.Method {
	case reqCommandApproval:
		var p commandApprovalParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			_ = s.c.respond(msg.ID, approvalResponse{Decision: decisionDecline})
			s.warn("Declined an unreadable Codex command approval request")
			return
		}
		action := s.commandAction(p)
		if s.readOnly() && action.Kind == "exec" {
			s.autoDecline(msg, approvalResponse{Decision: decisionDecline},
				"Declined "+action.Summary+": this run is read-only, so commands may not leave Codex's read-only sandbox")
			return
		}
		s.askApproval(&pendingRequest{rpcID: msg.ID, kind: pendCommand}, action, msg.Params)
	case reqFileChangeApproval:
		var p fileChangeApprovalParams
		_ = json.Unmarshal(msg.Params, &p)
		action := s.fileChangeAction(p)
		if s.readOnly() {
			s.autoDecline(msg, approvalResponse{Decision: decisionDecline}, "Declined "+action.Summary+": this run is read-only")
			return
		}
		s.askApproval(&pendingRequest{rpcID: msg.ID, kind: pendFileChange}, action, msg.Params)
	case reqPermissions:
		var p permissionsApprovalParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			_ = s.c.respond(msg.ID, permissionsApprovalResponse{Permissions: permissionProfile{}, Scope: "turn"})
			s.warn("Declined an unreadable Codex permission request")
			return
		}
		action := permissionsAction(p)
		if s.readOnly() && p.Permissions.FileSystem != nil && len(p.Permissions.FileSystem.Write) > 0 {
			s.autoDecline(msg, permissionsApprovalResponse{Permissions: permissionProfile{}, Scope: "turn"},
				"Declined Codex request for write access: this run is read-only")
			return
		}
		s.askApproval(&pendingRequest{rpcID: msg.ID, kind: pendPermissions, perms: p.Permissions}, action, msg.Params)
	case reqElicitation:
		s.handleElicitation(msg)
	case reqUserInput:
		var p userInputParams
		if err := json.Unmarshal(msg.Params, &p); err != nil || len(p.Questions) == 0 {
			_ = s.c.respond(msg.ID, userInputResponse{Answers: map[string]userInputAnswer{}})
			return
		}
		q := &providers.Question{Kind: "ask", Raw: msg.Params}
		var parts []string
		for _, qq := range p.Questions {
			t := qq.Question
			if qq.Header != "" && !strings.Contains(t, qq.Header) {
				t = qq.Header + ": " + t
			}
			parts = append(parts, t)
		}
		q.Text = strings.Join(parts, "\n")
		for _, o := range p.Questions[0].Options {
			q.Options = append(q.Options, o.Label)
		}
		id := s.register(&pendingRequest{rpcID: msg.ID, kind: pendUserInput, questions: p.Questions})
		q.ID = id
		s.emit(providers.Event{Kind: providers.EventQuestion, Text: q.Text, Question: q})
	case reqDynamicToolCall:
		// yip registers no dynamic tools; answer explicitly.
		_ = s.c.respond(msg.ID, dynamicToolCallResponse{
			ContentItems: []dynamicToolContentItem{{Type: "inputText", Text: "This tool is not provided by yip."}},
			Success:      false,
		})
		s.warn("Codex called a dynamic tool that yip does not provide")
	case reqLegacyExecCommand:
		var p legacyExecApprovalParams
		_ = json.Unmarshal(msg.Params, &p)
		cmd := strings.Join(p.Command, " ")
		action := protocol.ApprovalAction{Kind: "exec", Command: cmd, Target: p.Cwd, Summary: "Run `" + truncate(cmd, 160) + "`"}
		if p.Reason != nil {
			action.Detail = *p.Reason
		}
		if s.readOnly() {
			s.autoDecline(msg, approvalResponse{Decision: map[string]any{"denied": map[string]string{"rejection": "read-only run"}}}, "Declined "+action.Summary+": this run is read-only")
			return
		}
		s.askApproval(&pendingRequest{rpcID: msg.ID, kind: pendLegacyExec}, action, msg.Params)
	case reqLegacyApplyPatch:
		var p legacyPatchApprovalParams
		_ = json.Unmarshal(msg.Params, &p)
		var ps []string
		for path := range p.FileChanges {
			ps = append(ps, s.rel(path))
		}
		sort.Strings(ps)
		action := protocol.ApprovalAction{Kind: "edit", Target: strings.Join(ps, ", "), Summary: fmt.Sprintf("Edit %d file(s): %s", len(ps), truncate(strings.Join(ps, ", "), 200))}
		if p.Reason != nil {
			action.Detail = *p.Reason
		}
		if s.readOnly() {
			s.autoDecline(msg, approvalResponse{Decision: map[string]any{"denied": map[string]string{"rejection": "read-only run"}}}, "Declined "+action.Summary+": this run is read-only")
			return
		}
		s.askApproval(&pendingRequest{rpcID: msg.ID, kind: pendLegacyPatch}, action, msg.Params)
	case reqAuthRefresh:
		// Only sent when a client owns ChatGPT tokens; yip never does and
		// never handles credentials.
		_ = s.c.respondError(msg.ID, jsonrpcMethodNotFound, "yip does not manage Codex credentials; sign in with `codex login`")
		s.warn("Codex asked yip to refresh ChatGPT tokens; yip does not handle credentials")
	default:
		// attestation/generate (not opted in), or anything newer than the
		// pinned schema: answer explicitly so nothing hangs.
		_ = s.c.respondError(msg.ID, jsonrpcMethodNotFound, defaultNotFoundMessage)
		s.warn("Codex sent an unsupported request (" + msg.Method + "); yip answered that it is not supported")
	}
}

func (s *session) commandAction(p commandApprovalParams) protocol.ApprovalAction {
	a := protocol.ApprovalAction{Kind: "exec"}
	if p.Reason != nil {
		a.Detail = *p.Reason
	}
	if p.NetworkApprovalContext != nil {
		a.Kind = "network"
		a.Target = p.NetworkApprovalContext.Host
		a.Summary = fmt.Sprintf("Allow network access to %s (%s)", p.NetworkApprovalContext.Host, p.NetworkApprovalContext.Protocol)
		if p.Command != nil {
			a.Command = *p.Command
		}
		return a
	}
	if p.Command != nil {
		a.Command = *p.Command
	} else {
		s.mu.Lock()
		if it, ok := s.items[p.ItemID]; ok {
			a.Command = it.Command
		}
		s.mu.Unlock()
	}
	if p.Cwd != nil {
		a.Target = *p.Cwd
	} else {
		a.Target = s.spec.Workdir
	}
	a.Summary = "Run `" + truncate(a.Command, 160) + "`"
	if a.Target != "" && !samePath(a.Target, s.spec.Workdir) {
		a.Summary += " in " + a.Target
	}
	return a
}

func (s *session) fileChangeAction(p fileChangeApprovalParams) protocol.ApprovalAction {
	a := protocol.ApprovalAction{Kind: "edit"}
	s.mu.Lock()
	it, ok := s.items[p.ItemID]
	s.mu.Unlock()
	var detail []string
	if p.Reason != nil && *p.Reason != "" {
		detail = append(detail, *p.Reason)
	}
	if ok && len(it.Changes) > 0 {
		var ps []string
		for _, c := range it.Changes {
			ps = append(ps, s.rel(c.Path))
			detail = append(detail, c.Diff)
		}
		a.Target = strings.Join(ps, ", ")
		a.Summary = fmt.Sprintf("Edit %d file(s): %s", len(ps), s.paths(it.Changes))
	} else if p.GrantRoot != nil && *p.GrantRoot != "" {
		a.Target = *p.GrantRoot
		a.Summary = "Allow file edits under " + *p.GrantRoot
	} else {
		a.Target = s.spec.Workdir
		a.Summary = "Apply file edits"
	}
	if p.GrantRoot != nil && *p.GrantRoot != "" && ok {
		detail = append(detail, "Codex also asks for write access under "+*p.GrantRoot)
	}
	a.Detail = truncate(strings.Join(detail, "\n"), 4000)
	return a
}

func permissionsAction(p permissionsApprovalParams) protocol.ApprovalAction {
	var parts, targets []string
	kind := "other"
	if fs := p.Permissions.FileSystem; fs != nil {
		if len(fs.Write) > 0 {
			parts = append(parts, "write access to "+strings.Join(fs.Write, ", "))
			targets = append(targets, fs.Write...)
			kind = "edit"
		}
		if len(fs.Read) > 0 {
			parts = append(parts, "read access to "+strings.Join(fs.Read, ", "))
			targets = append(targets, fs.Read...)
		}
	}
	if n := p.Permissions.Network; n != nil && n.Enabled != nil && *n.Enabled {
		parts = append(parts, "network access")
		if kind == "other" {
			kind = "network"
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "additional permissions")
	}
	a := protocol.ApprovalAction{Kind: kind, Summary: "Grant Codex " + strings.Join(parts, " and ") + " for this turn", Target: strings.Join(targets, ", ")}
	if a.Target == "" {
		a.Target = p.Cwd
	}
	if p.Reason != nil {
		a.Detail = *p.Reason
	}
	return a
}

func (s *session) handleElicitation(msg rpcMessage) {
	var p elicitationParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		_ = s.c.respond(msg.ID, elicitationResponse{Action: "decline"})
		return
	}
	var meta map[string]any
	_ = json.Unmarshal(p.Meta, &meta)
	if kind, _ := meta["codex_approval_kind"].(string); kind == "mcp_tool_call" {
		action := protocol.ApprovalAction{Kind: "mcp", Target: p.ServerName, Summary: truncate(p.Message, 300)}
		if action.Summary == "" {
			action.Summary = "Allow a tool call on MCP server " + p.ServerName
		}
		s.askApproval(&pendingRequest{rpcID: msg.ID, kind: pendMCPApproval}, action, msg.Params)
		return
	}
	if p.Mode == "form" {
		if field, ok := singleStringField(p.RequestedSchema); ok {
			id := s.register(&pendingRequest{rpcID: msg.ID, kind: pendElicitForm, formField: field})
			q := &providers.Question{ID: id, Kind: "ask", Text: p.Message, Raw: msg.Params}
			s.emit(providers.Event{Kind: providers.EventQuestion, Text: p.Message, Question: q})
			return
		}
	}
	_ = s.c.respond(msg.ID, elicitationResponse{Action: "decline"})
	s.warn(fmt.Sprintf("Declined a %s request from MCP server %s that yip cannot present: %s", p.Mode, p.ServerName, truncate(p.Message, 200)))
}

// singleStringField reports the property name when the elicitation schema
// asks for exactly one string value.
func singleStringField(raw json.RawMessage) (string, bool) {
	var sch struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &sch) != nil || len(sch.Properties) != 1 {
		return "", false
	}
	for k, v := range sch.Properties {
		if v.Type == "string" {
			return k, true
		}
	}
	return "", false
}
