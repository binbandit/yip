package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

type pendingPerm struct {
	req     *acp.Request
	options []acp.PermissionOption
	action  protocol.ApprovalAction
}

type pendingQuestion struct {
	req  *acp.Request
	kind string // ask | plan
	ask  askQuestionParams
	flat []flatOption
}

// session is one Cursor ACP run attempt. It implements providers.Session and
// acp.Handler (agent -> client traffic).
type session struct {
	a        *Adapter
	spec     providers.StartSpec
	ap       *agentProc
	root     string // symlink-resolved workdir
	readOnly bool

	events       chan providers.Event
	emitMu       sync.RWMutex
	eventsClosed bool

	init acp.InitializeResult

	mu            sync.Mutex
	sessionID     string
	replaying     bool
	wantMode      string // Cursor mode that must stay active (read-only), "" when not enforced
	currentMode   string
	modeConfigID  string
	modeViaLegacy bool
	configOptions []acp.ConfigOption
	reasserting   bool

	turnActive  bool
	queue       []string
	ended       bool
	cancelReq   bool
	abortResult *failure

	perms     map[string]*pendingPerm
	questions map[string]*pendingQuestion
	byRPC     map[string]string // agent request id -> pending approval/question id
	seq       int

	tools         map[string]*acp.ToolCall
	startedTools  map[string]bool
	finishedTools map[string]bool

	msg         strings.Builder
	msgID       string
	turnTail    []string
	lastMessage string
	usage       *protocol.Usage

	result    providers.Result
	runCtx    context.Context
	runCancel context.CancelFunc
	timer     *time.Timer
	done      chan struct{}
}

func newSession(a *Adapter, spec providers.StartSpec, root string) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		a:             a,
		spec:          spec,
		root:          root,
		readOnly:      spec.Mode != protocol.ModeEdit,
		events:        make(chan providers.Event, 1024),
		perms:         map[string]*pendingPerm{},
		questions:     map[string]*pendingQuestion{},
		byRPC:         map[string]string{},
		tools:         map[string]*acp.ToolCall{},
		startedTools:  map[string]bool{},
		finishedTools: map[string]bool{},
		runCtx:        ctx,
		runCancel:     cancel,
		done:          make(chan struct{}),
	}
}

// ---------- providers.Session ----------

func (s *session) Events() <-chan providers.Event { return s.events }

func (s *session) Wait() providers.Result {
	<-s.done
	return s.result
}

// SendInput queues steering input. ACP prompt turns are sequential and have
// no way to inject text into a running turn, so input received during a turn
// is sent as the next session/prompt once the current turn's stop reason
// arrives ("queued"). Input received while idle between turns is sent as the
// next prompt right away ("immediate").
func (s *session) SendInput(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("cursor: empty input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || s.cancelReq || s.abortResult != nil {
		return "", errors.New("cursor: session has ended; input was not delivered")
	}
	if s.turnActive {
		s.queue = append(s.queue, text)
		return "queued", nil
	}
	s.queue = append([]string{text}, s.queue...)
	return "immediate", nil
}

// ResolveApproval answers a pending session/request_permission with the
// matching one-time option. yip never selects "allow always": a persistent
// grant inside Cursor would bypass later yip approvals.
func (s *session) ResolveApproval(ctx context.Context, id string, d providers.ApprovalDecision) error {
	s.mu.Lock()
	pp, ok := s.perms[id]
	if ok {
		delete(s.perms, id)
		delete(s.byRPC, pp.req.Key())
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("cursor: no pending approval %q (already resolved, withdrawn by Cursor, or cancelled)", id)
	}
	if d.Allow {
		if opt := findOption(pp.options, acp.AllowOnce, "allow-once"); opt != "" {
			return pp.req.Reply(acp.Selected(opt))
		}
		// Only a persistent grant is on offer: refuse rather than widen it.
		err := pp.req.Reply(rejectReply(pp.options))
		s.emit(providers.Event{Kind: providers.EventWarning, Text: "Cursor offered only a persistent \"allow always\" grant for: " + pp.action.Summary + ". yip grants one action at a time, so the request was rejected."})
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: Cursor offered no one-time allow option for approval %s; it was rejected", providers.ErrUnsupported, id)
	}
	return pp.req.Reply(rejectReply(pp.options))
}

func rejectReply(opts []acp.PermissionOption) acp.RequestPermissionResult {
	if opt := findOption(opts, acp.RejectOnce, "reject-once"); opt != "" {
		return acp.Selected(opt)
	}
	if opt := findOption(opts, acp.RejectAlways, "reject-always"); opt != "" {
		return acp.Selected(opt)
	}
	// No reject option offered: "cancelled" is the only well-formed refusal.
	return acp.CancelledPermission()
}

// AnswerQuestion replies to a pending cursor/ask_question or
// cursor/create_plan request.
func (s *session) AnswerQuestion(ctx context.Context, id string, a providers.QuestionAnswer) error {
	s.mu.Lock()
	pq, ok := s.questions[id]
	if ok {
		delete(s.questions, id)
		delete(s.byRPC, pq.req.Key())
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("cursor: no pending question %q (already answered, withdrawn by Cursor, or cancelled)", id)
	}
	in := answerInput{Text: a.Text, Selected: a.Selected, Declined: a.Declined}
	if pq.kind == "plan" {
		return pq.req.Reply(answerPlan(in))
	}
	return pq.req.Reply(answerAsk(pq.ask, pq.flat, in))
}

// Cancel interrupts the turn with session/cancel, answers every pending
// blocking request with its documented cancelled form, waits briefly for the
// "cancelled" stop reason, then terminates the process group.
func (s *session) Cancel(ctx context.Context) error {
	s.requestCancel()
	select {
	case <-s.done:
		return nil
	case <-time.After(s.a.cancelGrace):
	case <-ctx.Done():
	}
	// The agent did not acknowledge in time: abandon the in-flight prompt so
	// the run loop finishes, which terminates the process group
	// (SIGTERM, then SIGKILL after the grace period) and confirms exit.
	s.runCancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) requestCancel() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	first := !s.cancelReq
	s.cancelReq = true
	s.queue = nil
	perms, qs := s.drainPendingLocked()
	active := s.turnActive
	sid := s.sessionID
	s.mu.Unlock()
	answerCancelled(perms, qs)
	if first && active && sid != "" {
		_ = s.ap.conn.Notify(acp.MethodSessionCancel, acp.CancelParams{SessionID: sid})
	}
}

// abort stops the run with a specific outcome (timeout, boundary violation).
func (s *session) abort(f failure) {
	s.mu.Lock()
	if s.abortResult == nil && !s.ended {
		s.abortResult = &f
	}
	s.mu.Unlock()
	s.emit(providers.Event{Kind: providers.EventError, Text: f.message})
	go func() { _ = s.Cancel(context.Background()) }()
}

func (s *session) drainPendingLocked() ([]*pendingPerm, []*pendingQuestion) {
	var perms []*pendingPerm
	var qs []*pendingQuestion
	for id, p := range s.perms {
		perms = append(perms, p)
		delete(s.perms, id)
	}
	for id, q := range s.questions {
		qs = append(qs, q)
		delete(s.questions, id)
	}
	s.byRPC = map[string]string{}
	return perms, qs
}

func answerCancelled(perms []*pendingPerm, qs []*pendingQuestion) {
	for _, p := range perms {
		_ = p.req.Reply(acp.CancelledPermission())
	}
	for _, q := range qs {
		if q.kind == "plan" {
			_ = q.req.Reply(outcomeReply[planOutcome]{planOutcome{Outcome: "cancelled"}})
		} else {
			_ = q.req.Reply(outcomeReply[askOutcome]{askOutcome{Outcome: "cancelled"}})
		}
	}
}

// ---------- events ----------

func (s *session) emit(e providers.Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	s.emitMu.RLock()
	defer s.emitMu.RUnlock()
	if s.eventsClosed {
		return
	}
	s.events <- e
}

func (s *session) closeEvents() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if !s.eventsClosed {
		s.eventsClosed = true
		close(s.events)
	}
}

func (s *session) warn(text string) {
	s.emit(providers.Event{Kind: providers.EventWarning, Text: text})
}

// ---------- run loop ----------

func (s *session) run(first string) {
	text := first
	for {
		res, err := s.turn(text)
		if s.afterTurn(res, err) {
			break
		}
		next, ok := s.dequeue()
		if !ok {
			s.setOutcome(failure{outcome: protocol.OutcomeSucceeded})
			break
		}
		s.emit(providers.Event{Kind: protocol.RunEvInputDelivered, Text: oneLine(next, 200)})
		text = next
	}
	s.finish()
}

func (s *session) dequeue() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelReq || s.abortResult != nil || len(s.queue) == 0 {
		s.ended = true
		return "", false
	}
	next := s.queue[0]
	s.queue = s.queue[1:]
	return next, true
}

func (s *session) turn(text string) (acp.PromptResult, error) {
	var res acp.PromptResult
	s.mu.Lock()
	if s.cancelReq || s.abortResult != nil {
		s.mu.Unlock()
		return res, context.Canceled
	}
	s.turnActive = true
	s.turnTail = nil
	sid := s.sessionID
	s.mu.Unlock()

	err := s.ap.conn.Call(s.runCtx, acp.MethodSessionPrompt, acp.PromptParams{SessionID: sid, Prompt: []acp.ContentBlock{acp.TextBlock(text)}}, &res)

	s.mu.Lock()
	final := s.takeMessageLocked()
	s.turnActive = false
	s.mu.Unlock()
	if final != "" {
		s.emit(providers.Event{Kind: providers.EventMessage, Text: final})
	}
	return res, err
}

// afterTurn records the turn's outcome and reports whether the run is over.
func (s *session) afterTurn(res acp.PromptResult, err error) bool {
	s.mu.Lock()
	cancelled, abort := s.cancelReq, s.abortResult
	s.mu.Unlock()
	if abort != nil {
		s.setOutcome(*abort)
		return true
	}
	if cancelled {
		s.setOutcome(failure{outcome: protocol.OutcomeCancelled, message: "cancelled"})
		return true
	}
	if err != nil {
		s.fail(s.classify(err))
		return true
	}
	if res.Usage != nil {
		s.addTurnUsage(res.Usage)
	}
	switch res.StopReason {
	case acp.StopEndTurn, "":
		return false
	case acp.StopCancelled:
		s.fail(failure{outcome: protocol.OutcomeFailed, message: "Cursor ended the turn as cancelled although yip did not cancel it"})
	case acp.StopMaxTokens, acp.StopMaxTurnRequests:
		s.fail(failure{outcome: protocol.OutcomeFailed, message: fmt.Sprintf("Cursor stopped the turn early (%s); the work may be incomplete", res.StopReason)})
	case acp.StopRefusal:
		s.fail(failure{outcome: protocol.OutcomeFailed, message: "Cursor refused to continue (stop reason: refusal)"})
	default:
		s.warn("Cursor ended the turn with an unrecognised stop reason: " + res.StopReason)
		return false
	}
	return true
}

// classify maps a failed request to an outcome.
func (s *session) classify(err error) failure {
	if e, ok := rpcError(err); ok {
		f := classifyRPC(e)
		if f.outcome == protocol.OutcomeFailed {
			if tail := strings.TrimSpace(s.ap.stderr.String()); tail != "" {
				f.message += "\nstderr: " + lastLines(tail, 8, 1500)
			}
		}
		return f
	}
	if errors.Is(err, acp.ErrClosed) || errors.Is(err, context.Canceled) {
		s.ap.waitExit(3 * time.Second)
		sum := s.ap.exitSummary()
		return classifyText("Cursor agent stopped unexpectedly. "+sum, s.ap.stderr.String())
	}
	return failure{outcome: protocol.OutcomeFailed, message: "Cursor ACP error: " + err.Error()}
}

// fail emits the matching event and records the outcome.
func (s *session) fail(f failure) {
	switch f.outcome {
	case protocol.OutcomeAuthRequired:
		s.emit(providers.Event{Kind: providers.EventAuthRequired, Text: authDetail(f.message)})
		f.message = authDetail(f.message)
	case protocol.OutcomeRateLimited:
		s.emit(providers.Event{Kind: providers.EventRateLimited, Text: f.message, RetryAfter: f.retryAfter})
	default:
		s.emit(providers.Event{Kind: providers.EventError, Text: f.message})
	}
	s.setOutcome(f)
}

func authDetail(msg string) string {
	const how = "Sign in on this machine with `agent login` in a terminal, or provide CURSOR_API_KEY through the runner's allowlisted environment. yip never automates the browser login."
	if strings.Contains(msg, "agent login") {
		return msg
	}
	if msg == "" {
		return "Cursor requires authentication. " + how
	}
	return "Cursor requires authentication: " + msg + ". " + how
}

func (s *session) setOutcome(f failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result.Outcome != "" {
		return
	}
	s.result.Outcome = f.outcome
	if f.outcome != protocol.OutcomeSucceeded && f.outcome != protocol.OutcomeCancelled {
		s.result.Error = f.message
	}
	s.result.RetryAfter = f.retryAfter
}

// finish stops the process, answers anything still pending, and publishes
// the result. Runs exactly once, from the run loop (or failed start).
func (s *session) finish() {
	s.mu.Lock()
	s.ended = true
	perms, qs := s.drainPendingLocked()
	s.mu.Unlock()
	answerCancelled(perms, qs)
	if s.timer != nil {
		s.timer.Stop()
	}
	exitOK := s.ap.stop(s.a.eofWait, s.a.termGrace)
	s.runCancel()

	s.mu.Lock()
	if s.result.Outcome == "" {
		s.result.Outcome = protocol.OutcomeUnknown
	}
	s.result.ExitConfirmed = exitOK
	s.result.VendorSessionID = s.sessionID
	s.result.Usage = s.usage
	final := strings.TrimSpace(strings.Join(s.turnTail, "\n\n"))
	if final == "" {
		final = s.lastMessage
	}
	s.result.FinalText = final
	s.mu.Unlock()
	if !exitOK {
		s.warn("Could not confirm that every Cursor agent process exited.")
	}
	s.closeEvents()
	close(s.done)
}

// ---------- message accumulation ----------

func (s *session) takeMessageLocked() string {
	text := s.msg.String()
	s.msg.Reset()
	s.msgID = ""
	if strings.TrimSpace(text) == "" {
		return ""
	}
	s.turnTail = append(s.turnTail, text)
	s.lastMessage = text
	return text
}

func (s *session) appendChunk(messageID, text string) {
	s.mu.Lock()
	var flushed string
	// ACP: chunks with the same messageId belong to one message; a changed
	// messageId starts a new one.
	if messageID != s.msgID && s.msg.Len() > 0 {
		flushed = s.takeMessageLocked()
	}
	s.msgID = messageID
	s.msg.WriteString(text)
	s.mu.Unlock()
	if flushed != "" {
		s.emit(providers.Event{Kind: providers.EventMessage, Text: flushed})
	}
	if text != "" {
		s.emit(providers.Event{Kind: providers.EventMessageDelta, Text: text})
	}
}

// ---------- usage ----------

func (s *session) addTurnUsage(u *acp.TurnUsage) {
	s.mu.Lock()
	if s.usage == nil {
		s.usage = &protocol.Usage{Source: "vendor", Billing: protocol.BillingUnknown}
	}
	add := func(dst **int64, v *int64) {
		if v == nil {
			return
		}
		if *dst == nil {
			*dst = new(int64)
		}
		**dst += *v
	}
	add(&s.usage.InputTokens, u.InputTokens)
	add(&s.usage.OutputTokens, u.OutputTokens)
	snap := *s.usage
	s.mu.Unlock()
	s.emit(providers.Event{Kind: providers.EventUsage, Usage: &snap, Data: mustJSON(u)})
}

func (s *session) setCost(c *acp.Cost, raw json.RawMessage) {
	s.mu.Lock()
	if s.usage == nil {
		s.usage = &protocol.Usage{Source: "vendor", Billing: protocol.BillingUnknown}
	}
	if strings.EqualFold(c.Currency, "USD") {
		v := c.Amount
		s.usage.CostUSD = &v
	}
	snap := *s.usage
	s.mu.Unlock()
	s.emit(providers.Event{Kind: providers.EventUsage, Usage: &snap, Data: raw})
}
