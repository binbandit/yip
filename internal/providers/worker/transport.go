// Package worker runs a provider adapter over a private, bidirectional stdio
// JSON-RPC connection. It does not create containers or transport credentials.
package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
)

const (
	maxFrame            = 16 << 20
	maxQueuedEvents     = 1024
	maxQueuedEventBytes = 32 << 20
	maxRequests         = 64
	maxRequestBytes     = 32 << 20
	stopGrace           = 2 * time.Second
	unsupportedCode     = -32010
)

// acp preserves notification order, but its request callbacks run on the read
// loop. Always dispatch requests off that loop: bridge permissions may block
// while the peer makes another call to resolve them.
type dispatcher struct {
	request func(*acp.Request)
	notify  func(string, json.RawMessage)
	budget  *requestBudget
}

// Count both executing handlers and blocked reply writers. Never wait for a
// slot on the read loop: permission resolutions and replies need that loop.
type requestBudget struct {
	mu           sync.Mutex
	count, bytes int
	stopped      bool
	overflow     func()
}

func (d dispatcher) HandleRequest(r *acp.Request) {
	size := len(r.Params) + len(r.ID) + len(r.Method)
	if d.budget != nil {
		b := d.budget
		b.mu.Lock()
		if b.stopped {
			b.mu.Unlock()
			return
		}
		if b.count >= maxRequests || size > maxRequestBytes-b.bytes {
			b.stopped = true
			b.mu.Unlock()
			b.overflow()
			return
		}
		b.count++
		b.bytes += size
		b.mu.Unlock()
	}
	go func() {
		if d.budget != nil {
			defer func() {
				b := d.budget
				b.mu.Lock()
				b.count--
				b.bytes -= size
				b.mu.Unlock()
			}()
		}
		d.request(r)
	}()
}
func (d dispatcher) HandleNotification(method string, p json.RawMessage) {
	if d.notify != nil {
		d.notify(method, p)
	}
}

// framedReader places the existing bridge's 16 MiB ceiling on acp's otherwise
// unbounded ReadBytes. It also supports lines well beyond Scanner's default.
type framedReader struct {
	scanner *bufio.Scanner
	pending []byte
}

func frames(r io.Reader) io.Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxFrame)
	return &framedReader{scanner: sc}
}

func (r *framedReader) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		r.pending = append(append([]byte(nil), r.scanner.Bytes()...), '\n')
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func reply(r *acp.Request, value any, err error) {
	if err == nil {
		_ = r.Reply(value)
	} else if errors.Is(err, providers.ErrUnsupported) {
		_ = r.ReplyError(unsupportedCode, "operation not supported by this provider")
	} else {
		// Adapter errors can contain launch arguments or environment values.
		// Do not echo those across this boundary or log protocol payloads.
		_ = r.ReplyError(acp.CodeInternalError, "worker adapter operation failed")
	}
}

func call(ctx context.Context, c *acp.Conn, method string, in, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Conn.Call's write can block before it reaches its context select. Keep
	// that writer off the caller's goroutine; transport teardown closes its
	// underlying pipe. Decode only after receiving, so a timeout cannot race
	// a late response mutating the caller's destination.
	type response struct {
		raw json.RawMessage
		err error
	}
	done := make(chan response, 1)
	go func() {
		var raw json.RawMessage
		err := c.Call(ctx, method, in, &raw)
		done <- response{raw: raw, err: err}
	}()
	var res response
	select {
	case res = <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	err := res.err
	var rpcErr *acp.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == unsupportedCode {
		return providers.ErrUnsupported
	}
	if err == nil && out != nil && len(res.raw) > 0 {
		return json.Unmarshal(res.raw, out)
	}
	return err
}

type inputRequest struct{ Text string }
type approvalRequest struct {
	ID       string
	Decision providers.ApprovalDecision
}
type questionRequest struct {
	ID     string
	Answer providers.QuestionAnswer
}

func decode(r *acp.Request, v any) bool {
	if len(r.Params) == 0 || string(r.Params) == "null" || json.Unmarshal(r.Params, v) != nil {
		_ = r.ReplyError(acp.CodeInvalidParams, "invalid worker parameters")
		return false
	}
	return true
}

func cancelSession(s providers.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), stopGrace)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = s.Cancel(ctx)
		_ = s.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
