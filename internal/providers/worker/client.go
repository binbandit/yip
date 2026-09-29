package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

// Start launches an already configured worker command. The caller owns any
// external resource (such as a container) and must clean it up after Wait,
// including when ExitConfirmed is false. Stdout and stdin are reserved for RPC;
// stderr is discarded unless the caller explicitly supplied a destination.
func Start(ctx context.Context, cmd *exec.Cmd, spec providers.StartSpec, handler bridge.Handler) (providers.Session, error) {
	ctx, cancel := sessionContext(ctx, spec.Timeout)
	s := &session{
		ctx: ctx, cancel: cancel, handler: handler,
		events: make(chan providers.Event), wake: make(chan struct{}, 1),
		resultReady: make(chan struct{}), done: make(chan struct{}),
	}
	t, err := launch(cmd, dispatcher{request: s.request, notify: s.notification})
	if err != nil {
		cancel()
		return nil, err
	}
	s.transport = t
	t.conn.OnMalformed = func([]byte, error) { t.closePipes() }
	go t.conn.Serve()
	go s.deliverEvents()
	go s.run()
	if err := call(ctx, t.conn, "start", spec, nil); err != nil {
		cancel()
		t.closePipes()
		<-s.done
		go func() {
			for range s.events {
			}
		}()
		return nil, fmt.Errorf("start worker: %w", err)
	}
	return s, nil
}

// Probe launches a worker, obtains its installation, closes stdin and reaps the
// command. It never sends authentication-file contents across the transport.
func Probe(ctx context.Context, cmd *exec.Cmd) (protocol.ProviderInstallation, error) {
	var installation protocol.ProviderInstallation
	t, err := launch(cmd, dispatcher{request: func(r *acp.Request) { _ = r.MethodNotFound() }})
	if err != nil {
		return installation, err
	}
	defer t.cleanup()
	t.conn.OnMalformed = func([]byte, error) { t.closePipes() }
	go t.conn.Serve()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			t.closePipes()
		case <-done:
		}
	}()
	err = call(ctx, t.conn, "probe", struct{}{}, &installation)
	return installation, err
}

func sessionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

type transport struct {
	conn      *acp.Conn
	process   *providers.Process
	in, out   *os.File
	closeOnce sync.Once
}

func launch(cmd *exec.Cmd, h acp.Handler) (*transport, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil {
		return nil, errors.New("worker command stdin and stdout must be unset")
	}
	childIn, in, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	out, childOut, err := os.Pipe()
	if err != nil {
		_ = childIn.Close()
		_ = in.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout = childIn, childOut
	// Bound exec's copying goroutines too, if stderr is a caller-owned writer.
	cmd.WaitDelay = stopGrace
	p, err := providers.StartProcess(cmd)
	_ = childIn.Close()
	_ = childOut.Close()
	if err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	t := &transport{process: p, in: in, out: out}
	t.conn = acp.NewConn(frames(out), in, h)
	// A dead worker may leave stdout inherited by a descendant. Do not wait
	// forever for EOF, but allow the read loop to consume its final messages.
	go func() {
		<-p.Done()
		select {
		case <-t.conn.Done():
		case <-time.After(stopGrace):
			t.closePipes()
		}
	}()
	return t, nil
}

func (t *transport) closePipes() {
	t.closeOnce.Do(func() { _ = t.in.Close(); _ = t.out.Close() })
}

func (t *transport) cleanup() bool {
	_ = t.in.Close() // EOF is the worker's graceful shutdown signal.
	cleanExit := false
	select {
	case <-t.process.Done():
		cleanExit = t.process.Err() == nil
	case <-time.After(stopGrace):
	}
	t.closePipes()
	// Reap stragglers even if the process leader already exited.
	groupExited := t.process.Terminate(stopGrace)
	return cleanExit && groupExited
}

type session struct {
	*transport
	ctx         context.Context
	cancel      context.CancelFunc
	handler     bridge.Handler
	events      chan providers.Event
	wake        chan struct{}
	mu          sync.Mutex
	queue       []providers.Event
	eventEnd    bool
	result      providers.Result
	haveResult  bool
	resultReady chan struct{}
	done        chan struct{}
}

func (s *session) request(r *acp.Request) {
	if r.Method != "bridge/call" {
		_ = r.MethodNotFound()
		return
	}
	var req bridge.LocalRequest
	if !decode(r, &req) {
		return
	}
	if s.handler == nil {
		_ = r.Reply(bridge.LocalResponse{ID: req.ID, Error: &protocol.APIError{Code: "unavailable", Message: "No bridge handler is available."}})
		return
	}
	// dispatcher runs this outside the read loop; a permission handler can
	// wait while more calls, events and permission resolutions pass through.
	resp := s.handler(req)
	resp.ID = req.ID
	_ = r.Reply(resp)
}

func (s *session) notification(method string, raw json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventEnd || s.haveResult {
		return
	}
	switch method {
	case "session/event":
		var event providers.Event
		if json.Unmarshal(raw, &event) != nil {
			s.closePipes()
			return
		}
		s.queue = append(s.queue, event)
		select {
		case s.wake <- struct{}{}:
		default:
		}
	case "session/result":
		var result providers.Result
		if json.Unmarshal(raw, &result) != nil || result.Outcome == "" {
			s.closePipes()
			return
		}
		s.result, s.haveResult = result, true
		close(s.resultReady)
	}
}

func (s *session) run() {
	select {
	case <-s.resultReady:
	case <-s.conn.Done():
	case <-s.ctx.Done():
		// Give the adapter a chance to confirm its process-group exit before
		// terminating the worker transport. Never block behind a stuck writer.
		ctx, cancel := context.WithTimeout(context.Background(), stopGrace)
		go func() { _ = call(ctx, s.conn, "session/cancel", struct{}{}, nil) }()
		select {
		case <-s.resultReady:
		case <-s.conn.Done():
		case <-ctx.Done():
		}
		cancel()
	}
	cleanExit := s.cleanup()
	s.mu.Lock()
	if !s.haveResult {
		s.result = providers.Result{Outcome: protocol.OutcomeFailed, Error: "Worker transport ended without a terminal result.", ExitConfirmed: false}
		if s.ctx.Err() != nil {
			s.result.Outcome = protocol.OutcomeCancelled
			s.result.Error = "Worker session cancelled without confirmed provider exit."
		}
	}
	// A terminal result alone is not enough if the worker subsequently crashes
	// or needs forced termination. The caller must confirm external cleanup.
	s.result.ExitConfirmed = s.result.ExitConfirmed && cleanExit
	s.eventEnd = true
	s.mu.Unlock()
	s.cancel()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	close(s.done)
}

// Never block the RPC read loop on the Events consumer. In particular, callers
// may call Wait before draining events, and permission replies must still flow.
func (s *session) deliverEvents() {
	defer close(s.events)
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			end := s.eventEnd
			s.mu.Unlock()
			if end {
				return
			}
			<-s.wake
			continue
		}
		event := s.queue[0]
		s.queue[0] = providers.Event{}
		s.queue = s.queue[1:]
		s.mu.Unlock()
		s.events <- event
	}
}

func (s *session) Events() <-chan providers.Event { return s.events }
func (s *session) Wait() providers.Result {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}
func (s *session) SendInput(ctx context.Context, text string) (string, error) {
	var mode string
	err := call(ctx, s.conn, "session/sendInput", inputRequest{Text: text}, &mode)
	return mode, err
}
func (s *session) ResolveApproval(ctx context.Context, id string, d providers.ApprovalDecision) error {
	return call(ctx, s.conn, "session/resolveApproval", approvalRequest{ID: id, Decision: d}, nil)
}
func (s *session) AnswerQuestion(ctx context.Context, id string, a providers.QuestionAnswer) error {
	return call(ctx, s.conn, "session/answerQuestion", questionRequest{ID: id, Answer: a}, nil)
}
func (s *session) Cancel(ctx context.Context) error {
	// Cancellation is a local guarantee as well as an RPC. Even a worker that
	// stops reading must be torn down within the bounded shutdown grace.
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ providers.Session = (*session)(nil)
