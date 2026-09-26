package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// ErrInputClosed is returned by SendInput once the session has stopped
// accepting input (its last turn finished, or it is being cancelled).
var ErrInputClosed = errors.New("claude: session no longer accepts input")

// Steering delivery mode reported by SendInput. Claude Code queues user
// messages written to stdin while a turn runs: the docs state it may pick a
// queued message up between tool calls of the running turn, otherwise it
// answers it in the next turn. The adapter cannot know which will happen when
// it writes the message, so it reports the guaranteed behaviour: queued.
const steeringMode = "queued"

const (
	defaultDrainWait = 3 * time.Second // after the leader exits, wait for stdout EOF
	defaultExitWait  = 3 * time.Second // after stdin closes, wait for a clean exit
)

type session struct {
	a       *Adapter
	spec    providers.StartSpec
	plan    *launchPlan
	mcpName string

	proc    *providers.Process
	stdin   io.WriteCloser
	stdoutR *os.File // our read end; exec.Cmd.Wait never closes it
	events  chan providers.Event
	stderr  *providers.TailBuffer

	emitMu       sync.Mutex
	eventsClosed bool

	st *streamState // owned by the reader goroutine until readerDone

	writeMu sync.Mutex

	mu           sync.Mutex
	inputClosed  bool
	pending      []string      // uuids of user messages not yet answered
	cancelled    bool          // Cancel was requested
	timedOut     time.Duration // non-zero when the spec timeout fired
	turnWaiter   chan struct{} // closed on the next result (used by Cancel)
	interruptCap bool          // CLI advertised interrupt_cancel_queued_v1
	resultsSeen  int           // result messages received so far
	finished     bool

	stopOnce sync.Once

	readerDone chan struct{}
	done       chan struct{}
	result     providers.Result
}

// Start launches one Claude Code print-mode session.
func (a *Adapter) Start(ctx context.Context, spec providers.StartSpec) (providers.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if missing := a.missingFlags(); len(missing) > 0 {
		return nil, fmt.Errorf("claude: installed Claude Code lacks %s: %w", strings.Join(missing, ", "), providers.ErrUnsupported)
	}
	exe, err := a.resolveExecutable(spec.Executable)
	if err != nil {
		return nil, fmt.Errorf("claude: executable not found: %w", err)
	}
	plan, err := buildLaunch(spec, a.opts.TempDir)
	if err != nil {
		return nil, err
	}
	mcpName := spec.MCP.Name
	if mcpName == "" {
		mcpName = "yip"
	}

	cmd := exec.Command(exe, plan.Args...)
	cmd.Dir = spec.Workdir
	env := a.launchEnv(spec.Env)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(plan.TempDir)
		return nil, err
	}
	// Not cmd.StdoutPipe: exec.Cmd.Wait (started immediately by
	// providers.StartProcess) closes that pipe as soon as the process exits,
	// which can discard a final result line not yet read. With our own pipe
	// the reader sees EOF only once every writer has closed it.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = os.RemoveAll(plan.TempDir)
		return nil, err
	}
	cmd.Stdout = stdoutW
	stderrBuf := providers.NewTailBuffer(16 << 10)
	cmd.Stderr = stderrBuf

	billing := a.probeBilling()
	if hasAPIKey(env) {
		billing = protocol.BillingAPI
	}
	s := &session{
		a:          a,
		spec:       spec,
		plan:       plan,
		mcpName:    mcpName,
		stdin:      stdin,
		stdoutR:    stdoutR,
		events:     make(chan providers.Event, 1024),
		stderr:     stderrBuf,
		st:         newStreamState(plan.Resumed, billing),
		readerDone: make(chan struct{}),
		done:       make(chan struct{}),
	}
	proc, err := providers.StartProcess(cmd)
	_ = stdoutW.Close() // the child holds its own copy
	if err != nil {
		_ = stdoutR.Close()
		_ = os.RemoveAll(plan.TempDir)
		return nil, fmt.Errorf("claude: start: %w", err)
	}
	s.proc = proc

	go s.readLoop(stdoutR)
	go s.supervise()

	// The first user turn travels over stdin like every later one.
	id := uuid.NewString()
	s.mu.Lock()
	s.pending = append(s.pending, id)
	s.mu.Unlock()
	if err := s.writeJSON(userMessage(id, spec.Prompt)); err != nil {
		s.emit(providers.Event{Kind: providers.EventWarning, Text: "could not deliver the prompt to Claude Code: " + err.Error()})
	}
	if spec.Timeout > 0 {
		t := time.AfterFunc(spec.Timeout, func() {
			s.mu.Lock()
			if s.finished || s.cancelled {
				s.mu.Unlock()
				return
			}
			s.timedOut = spec.Timeout
			s.mu.Unlock()
			_ = s.stop(context.Background())
		})
		go func() { <-s.done; t.Stop() }()
	}
	return s, nil
}

func userMessage(id, text string) stdinUserMessage {
	return stdinUserMessage{
		Type:    "user",
		UUID:    id,
		Message: stdinMessageArg{Role: "user", Content: text},
	}
}

func (s *session) Events() <-chan providers.Event { return s.events }

// emit delivers an event unless the stream has already been closed.
func (s *session) emit(e providers.Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if !s.eventsClosed {
		s.events <- e
	}
}

func (s *session) closeEvents() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if !s.eventsClosed {
		s.eventsClosed = true
		close(s.events)
	}
}

func (s *session) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(b)
	return err
}

// closeInput ends stdin once; Claude Code exits after its current work.
func (s *session) closeInput() {
	s.mu.Lock()
	already := s.inputClosed
	s.inputClosed = true
	s.mu.Unlock()
	if !already {
		s.writeMu.Lock()
		_ = s.stdin.Close()
		s.writeMu.Unlock()
	}
}

// SendInput queues a steering message for the running session.
func (s *session) SendInput(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("claude: empty input")
	}
	id := uuid.NewString()
	s.mu.Lock()
	if s.inputClosed || s.cancelled || s.finished {
		s.mu.Unlock()
		return "", ErrInputClosed
	}
	// Registering before writing keeps stdin open until this message has
	// been answered (see onResult).
	s.pending = append(s.pending, id)
	s.mu.Unlock()

	errc := make(chan error, 1)
	go func() { errc <- s.writeJSON(userMessage(id, text)) }()
	select {
	case err := <-errc:
		if err != nil {
			s.dropPending(id)
			return "", fmt.Errorf("claude: deliver input: %w", err)
		}
		return steeringMode, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *session) dropPending(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.pending {
		if p == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			break
		}
	}
	if len(s.pending) == 0 && !s.inputClosed && s.resultsSeen > 0 {
		go s.closeInput()
	}
}

// ResolveApproval is unsupported: Claude Code sends permission prompts to the
// yip bridge's MCP permission tool (--permission-prompt-tool), and the bridge
// answers them directly. No approval request ever reaches this adapter.
func (s *session) ResolveApproval(context.Context, string, providers.ApprovalDecision) error {
	return fmt.Errorf("claude: approvals are answered by the yip bridge permission tool: %w", providers.ErrUnsupported)
}

// AnswerQuestion is unsupported: AskUserQuestion is not enabled for yip runs.
func (s *session) AnswerQuestion(context.Context, string, providers.QuestionAnswer) error {
	return fmt.Errorf("claude: provider questions are not enabled: %w", providers.ErrUnsupported)
}

// Cancel interrupts the turn through the documented stdin control request,
// ends input, then terminates the whole process group.
func (s *session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil
	}
	s.cancelled = true
	s.mu.Unlock()
	return s.stop(ctx)
}

func (s *session) stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		inputOpen := !s.inputClosed
		cancelQueued := s.interruptCap
		waiter := make(chan struct{})
		s.turnWaiter = waiter
		s.mu.Unlock()

		if inputOpen {
			// Documented control request: ends the running turn so the
			// transcript records it (SIGTERM alone leaves it unfinished).
			req := map[string]any{"subtype": "interrupt"}
			if cancelQueued {
				req["cancel_queued"] = true
			}
			if err := s.writeJSON(controlRequest{Type: "control_request", RequestID: "yip-interrupt-" + uuid.NewString(), Request: req}); err == nil {
				wait(ctx, s.a.opts.InterruptWait, waiter, s.proc.Done())
			}
			// Ending input also cancels any pending permission prompt.
			s.closeInput()
		}
		wait(ctx, s.a.exitWait, s.proc.Done())
		// Always runs, even when ctx is done: bounded by Grace (+5s).
		s.proc.Terminate(s.a.opts.Grace)
	})
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// wait blocks until any of chs closes, d elapses, or ctx is done.
func wait(ctx context.Context, d time.Duration, chs ...<-chan struct{}) {
	t := time.NewTimer(d)
	defer t.Stop()
	fired := make(chan struct{})
	quit := make(chan struct{})
	defer close(quit)
	var once sync.Once
	for _, c := range chs {
		go func(c <-chan struct{}) {
			select {
			case <-c:
				once.Do(func() { close(fired) })
			case <-quit:
			}
		}(c)
	}
	select {
	case <-fired:
	case <-t.C:
	case <-ctx.Done():
	}
}

// Wait blocks until the session has ended and the process group is gone (or
// could not be confirmed gone).
func (s *session) Wait() providers.Result {
	<-s.done
	return s.result
}

// readLoop parses stdout line by line.
func (s *session) readLoop(stdout io.Reader) {
	defer close(s.readerDone)
	r := bufio.NewReaderSize(stdout, 256<<10)
	warnedParse := false
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				var m wireMsg
				if jerr := json.Unmarshal(line, &m); jerr != nil {
					if !warnedParse {
						warnedParse = true
						s.emit(providers.Event{Kind: providers.EventWarning, Text: "unparsed Claude Code output: " + clip(string(line), 200)})
					}
				} else {
					s.onMessage(&m)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *session) onMessage(m *wireMsg) {
	switch m.Type {
	case "control_request":
		// The CLI only sends control requests to an SDK host; yip answers
		// permissions through the MCP tool. Reply with an error so nothing
		// waits on a hidden prompt, and surface it.
		var req struct {
			Subtype string `json:"subtype"`
		}
		_ = json.Unmarshal(m.Request, &req)
		_ = s.writeJSON(controlResponse{Type: "control_response", Response: map[string]any{
			"subtype": "error", "request_id": m.RequestID,
			"error": "yip does not handle " + req.Subtype + " control requests",
		}})
		s.emit(providers.Event{Kind: providers.EventWarning, Text: "Claude Code sent an unsupported control request (" + req.Subtype + "); it was declined"})
		return
	case "control_response":
		// A successful interrupt ack precedes the interrupted turn's result;
		// keep waiting for that result. An error means no turn will end.
		var resp struct {
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal(m.Response, &resp) == nil && resp.Subtype == "error" {
			s.signalTurn()
		}
		return
	}
	s.st.handle(m, s.emit, s.mcpName)
	switch {
	case m.Type == "system" && m.Subtype == "init":
		s.mu.Lock()
		s.interruptCap = s.st.caps["interrupt_cancel_queued_v1"]
		s.mu.Unlock()
	case m.Type == "result":
		s.onResult(m)
	}
}

func (s *session) signalTurn() {
	s.mu.Lock()
	w := s.turnWaiter
	s.turnWaiter = nil
	s.mu.Unlock()
	if w != nil {
		close(w)
	}
}

// onResult updates the set of unanswered user messages and ends stdin when
// none remain, which lets Claude Code exit after its final turn.
func (s *session) onResult(m *wireMsg) {
	s.signalTurn()
	s.mu.Lock()
	s.resultsSeen++
	answered := m.UserMessageUUIDs
	if len(answered) == 0 && m.UserMessageUUID != "" {
		answered = []string{m.UserMessageUUID}
	}
	switch {
	case m.Origin != nil && m.Origin.Kind == "task-notification":
		// A background-task follow-up turn, not an answer to our input.
	case len(answered) > 0:
		// Merged turns name the last message; everything sent before the
		// last answered message has been consumed too.
		last := -1
		for i, p := range s.pending {
			for _, a := range answered {
				if p == a {
					last = i
				}
			}
		}
		if last >= 0 {
			s.pending = append([]string{}, s.pending[last+1:]...)
		}
	default:
		// No echo to correlate (older CLI or a startup failure): assume the
		// queue drained rather than hold the process open forever.
		s.pending = nil
	}
	closeNow := len(s.pending) == 0 && !s.inputClosed
	s.mu.Unlock()
	if closeNow {
		s.closeInput()
	}
}

// supervise waits for the process, reaps the group, and finalizes the result.
func (s *session) supervise() {
	<-s.proc.Done()
	select {
	case <-s.readerDone:
	case <-time.After(s.a.drainWait):
	}
	// Reap anything the CLI left in its process group (tool shells, the
	// bridge) and confirm the group is gone.
	confirmed := s.proc.Terminate(s.a.opts.Grace)
	select {
	case <-s.readerDone:
	case <-time.After(s.a.drainWait):
		// A process outside the group still holds stdout; stop reading so
		// the stream state is final before classification.
		_ = s.stdoutR.Close()
		<-s.readerDone
	}
	_ = s.stdoutR.Close()
	s.closeInput()

	s.mu.Lock()
	cancelled := s.cancelled
	timedOut := s.timedOut
	s.mu.Unlock()

	oi := classify(s.st, s.proc.Err(), tailClip(s.stderr.String(), 2000), cancelled, timedOut, time.Now())
	res := providers.Result{
		Outcome:         oi.Outcome,
		FinalText:       oi.FinalText,
		Error:           oi.Error,
		RetryAfter:      oi.RetryAfter,
		VendorSessionID: s.st.sessionID,
		Usage:           s.st.usage,
		ExitConfirmed:   confirmed,
	}
	switch res.Outcome {
	case protocol.OutcomeRateLimited:
		s.emit(providers.Event{Kind: providers.EventRateLimited, Text: res.Error, RetryAfter: res.RetryAfter})
	case protocol.OutcomeAuthRequired:
		s.emit(providers.Event{Kind: providers.EventAuthRequired, Text: res.Error})
	case protocol.OutcomeFailed:
		s.emit(providers.Event{Kind: providers.EventError, Text: res.Error})
	}
	if !confirmed {
		s.emit(providers.Event{Kind: providers.EventWarning, Text: "Claude Code process group could not be confirmed stopped"})
	}
	_ = os.RemoveAll(s.plan.TempDir)

	s.mu.Lock()
	s.finished = true
	s.result = res
	s.mu.Unlock()
	s.closeEvents()
	close(s.done)
}
