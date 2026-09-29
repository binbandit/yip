package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

type record struct {
	Type, ID, Text, Tool, Outcome, Error, Billing string
	Success                                       bool
	Args, Data                                    json.RawMessage
	Input, Output                                 int64
	Cost                                          float64
	Models                                        []protocol.Model
}

type session struct {
	process       *providers.Process
	in            io.WriteCloser
	events        chan providers.Event
	done          chan struct{}
	probe         chan record
	grace         time.Duration
	writeMu       sync.Mutex
	mu            sync.Mutex
	cancelled     bool
	approvals     map[string]bool
	pending       map[string]chan bool
	next          int
	result        providers.Result
	stopOnce      sync.Once
	exitConfirmed bool
}

func startSession(cmd *exec.Cmd, cleanup func(), grace time.Duration) (*session, error) {
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	cmd.Stdout = writer
	// Never forward provider diagnostics: an auth helper could print secrets.
	cmd.Stderr = io.Discard
	p, err := providers.StartProcess(cmd)
	_ = writer.Close()
	if err != nil {
		_ = reader.Close()
		_ = in.Close()
		return nil, err
	}
	s := &session{
		process: p, in: in, grace: grace, events: make(chan providers.Event, 256),
		done: make(chan struct{}), probe: make(chan record, 1),
		approvals: map[string]bool{}, pending: map[string]chan bool{},
	}
	go func() {
		<-p.Done()
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
			_ = reader.Close()
		}
	}()
	go s.read(reader, cleanup)
	return s, nil
}
func (s *session) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.in.Write(append(b, '\n'))
	return err
}

func (s *session) writeContext(ctx context.Context, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	written := make(chan error, 1)
	go func() { written <- s.write(value) }()
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
		// A partially written command cannot be retracted safely. Stop the
		// attempt rather than deliver steering/approval after its caller left.
		_ = s.Cancel(context.Background())
		return ctx.Err()
	case <-s.done:
		return errors.New("Pi session ended during command delivery")
	}
}

func (s *session) stop() {
	s.stopOnce.Do(func() {
		_ = s.in.Close()
		s.exitConfirmed = s.process.Terminate(s.grace)
	})
}
func (s *session) emit(e providers.Event) {
	e.At = time.Now().UTC()
	// Preserve stream events; process exit unblocks a stalled consumer.
	select {
	case s.events <- e:
	case <-s.process.Done():
	}
}
func (s *session) read(reader *os.File, cleanup func()) {
	defer cleanup()
	defer close(s.done)
	defer close(s.events)
	defer reader.Close()
	sc := bufio.NewScanner(reader)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	result := providers.Result{Outcome: protocol.OutcomeFailed, Error: "Pi SDK exited without a final result"}
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			result.Error = "Invalid Pi host JSON"
			break
		}
		switch r.Type {
		case "probe":
			s.probe <- r
		case "text":
			s.emit(providers.Event{Kind: providers.EventMessageDelta, Text: r.Text})
		case "tool_execution_start", "tool_execution_end":
			kind := providers.EventToolStarted
			if r.Type == "tool_execution_end" {
				kind = providers.EventToolFinished
			}
			s.emit(providers.Event{Kind: kind, Tool: r.Tool, Data: r.Data})
		case "approval":
			s.mu.Lock()
			s.approvals[r.ID] = true
			s.mu.Unlock()
			kind := "edit"
			if r.Tool == "bash" {
				kind = "exec"
			}
			var args struct{ Command, Path string }
			_ = json.Unmarshal(r.Args, &args)
			// No dropping blocking requests. Cancellation still unblocks this send.
			e := providers.Event{Kind: providers.EventApprovalRequest, Approval: &providers.ApprovalRequest{
				ID: r.ID, Action: protocol.ApprovalAction{Kind: kind, Summary: "Pi " + r.Tool, Command: args.Command, Target: args.Path, Detail: string(r.Args)}, Raw: r.Args,
			}, At: time.Now().UTC()}
			select {
			case s.events <- e:
			case <-s.process.Done():
			}
		case "response":
			s.mu.Lock()
			ch := s.pending[r.ID]
			delete(s.pending, r.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- r.Success
			}
		case "result":
			result.Outcome, result.FinalText, result.Error = r.Outcome, r.Text, r.Error
			if result.Outcome != protocol.OutcomeSucceeded && result.Outcome != protocol.OutcomeFailed {
				result.Outcome = protocol.OutcomeFailed
				result.Error = "Invalid Pi host outcome"
			}
			billing := r.Billing
			if billing == "" {
				billing = protocol.BillingUnknown
			}
			result.Usage = &protocol.Usage{Source: "vendor", InputTokens: &r.Input, OutputTokens: &r.Output, CostUSD: &r.Cost, Billing: billing}
			s.emit(providers.Event{Kind: providers.EventUsage, Usage: result.Usage})
			goto finished
		}
	}
	if err := sc.Err(); err != nil {
		result.Error = fmt.Sprintf("Read Pi host: %v", err)
	}
finished:
	s.stop()
	result.ExitConfirmed = s.exitConfirmed
	s.mu.Lock()
	if s.cancelled {
		result.Outcome = protocol.OutcomeCancelled
		result.Error = ""
	}
	s.result = result
	s.mu.Unlock()
}
func (s *session) Events() <-chan providers.Event { return s.events }
func (s *session) Wait() providers.Result {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}
func (s *session) Cancel(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	default:
	}
	s.mu.Lock()
	s.cancelled = true
	s.mu.Unlock()
	// Request an SDK abort before group termination; do not wait indefinitely
	// for blocked tools, OAuth, or descendants to cooperate.
	aborted := make(chan struct{})
	go func() {
		_ = s.write(map[string]any{"type": "abort"})
		close(aborted)
	}()
	select {
	case <-aborted:
	case <-time.After(100 * time.Millisecond):
	}
	// Give an acknowledged SDK abort a short chance to release its bridge.
	// Non-cooperating tools are still bounded by the process-group supervisor.
	select {
	case <-s.done:
		return nil
	case <-time.After(100 * time.Millisecond):
	}
	s.stop()
	select {
	case <-s.done:
		if !s.exitConfirmed {
			return errors.New("Pi process group exit could not be confirmed")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *session) ResolveApproval(ctx context.Context, id string, d providers.ApprovalDecision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	exists := s.approvals[id]
	delete(s.approvals, id)
	s.mu.Unlock()
	if !exists {
		return errors.New("unknown Pi approval")
	}
	return s.writeContext(ctx, map[string]any{"type": "approval", "id": id, "allow": d.Allow})
}
func (*session) AnswerQuestion(context.Context, string, providers.QuestionAnswer) error {
	return providers.ErrUnsupported
}
func (s *session) SendInput(ctx context.Context, text string) (string, error) {
	s.mu.Lock()
	s.next++
	id := fmt.Sprintf("steer-%d", s.next)
	ch := make(chan bool, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if err := s.writeContext(ctx, map[string]any{"type": "steer", "id": id, "text": text}); err != nil {
		return "", err
	}
	select {
	case ok := <-ch:
		if ok {
			return "queued", nil
		}
		return "", errors.New("Pi rejected steering")
	case <-ctx.Done():
		return "", ctx.Err()
	case <-s.done:
		return "", errors.New("Pi session ended before steering was acknowledged")
	}
}
