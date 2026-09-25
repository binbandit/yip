package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// errConnClosed is returned for requests still pending when the app-server
// closes stdout (exit, crash, or kill).
var errConnClosed = errors.New("codex app-server connection closed")

// conn is a newline-delimited JSON-RPC 2.0 connection to `codex app-server`
// over stdio. Responses are routed to their waiting callers; server requests
// and notifications are queued in arrival order for a single consumer.
type conn struct {
	w   io.WriteCloser
	wmu sync.Mutex

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[string]chan rpcMessage
	closed  chan struct{}
	readErr error

	inbound *queue[rpcMessage]
}

func newConn(r io.Reader, w io.WriteCloser) *conn {
	c := &conn{
		w:       w,
		pending: map[string]chan rpcMessage{},
		closed:  make(chan struct{}),
		inbound: newQueue[rpcMessage](),
	}
	go c.readLoop(r)
	return c
}

func (c *conn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	var err error
	for {
		var line []byte
		line, err = br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var msg rpcMessage
			if jerr := json.Unmarshal(line, &msg); jerr != nil {
				// Not JSON-RPC (should not happen on stdout); surface it as a
				// synthetic notification so it is visible, not silently lost.
				c.inbound.push(rpcMessage{Method: "yip/invalidLine", Params: mustJSON(map[string]string{"line": truncate(string(line), 500)})})
			} else {
				c.dispatch(msg)
			}
		}
		if err != nil {
			break
		}
	}
	if errors.Is(err, io.EOF) {
		err = errConnClosed
	}
	c.mu.Lock()
	c.readErr = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.closed)
	c.inbound.close()
}

func (c *conn) dispatch(msg rpcMessage) {
	if msg.Method == "" && len(msg.ID) > 0 {
		// Response to one of our requests.
		key := idKey(msg.ID)
		c.mu.Lock()
		ch, ok := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ok {
			ch <- msg
		}
		return
	}
	c.inbound.push(msg)
}

// call sends a request and waits for its response, decoding the result into
// out (which may be nil). JSON-RPC errors are returned as *rpcError.
func (c *conn) call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	rawID := json.RawMessage(strconv.FormatInt(id, 10))
	ch := make(chan rpcMessage, 1)
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return errConnClosed
	default:
	}
	c.pending[idKey(rawID)] = ch
	c.mu.Unlock()

	if err := c.write(rpcMessage{ID: rawID, Method: method, Params: mustJSON(params)}); err != nil {
		c.mu.Lock()
		delete(c.pending, idKey(rawID))
		c.mu.Unlock()
		return err
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return errConnClosed
		}
		if msg.Error != nil {
			return msg.Error
		}
		if out != nil && len(msg.Result) > 0 {
			if err := json.Unmarshal(msg.Result, out); err != nil {
				return fmt.Errorf("decode %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, idKey(rawID))
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return errConnClosed
	}
}

func (c *conn) notify(method string, params any) error {
	msg := rpcMessage{Method: method}
	if params != nil {
		msg.Params = mustJSON(params)
	}
	return c.write(msg)
}

func (c *conn) respond(id json.RawMessage, result any) error {
	return c.write(rpcMessage{ID: id, Result: mustJSON(result)})
}

func (c *conn) respondError(id json.RawMessage, code int, message string) error {
	return c.write(rpcMessage{ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (c *conn) write(msg rpcMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.closed:
		return errConnClosed
	default:
	}
	_, err = c.w.Write(b)
	return err
}

// closeWrite closes the server's stdin, which asks app-server to exit.
func (c *conn) closeWrite() {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.w.Close()
}

func idKey(id json.RawMessage) string {
	s := strings.TrimSpace(string(id))
	// Normalise "7" and 7 so string and numeric IDs cannot collide oddly.
	return strings.Trim(s, `"`)
}

func mustJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("{}")
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// queue is an unbounded FIFO with a single blocking consumer. Producers never
// block, so the transport reader can never deadlock on a slow consumer.
type queue[T any] struct {
	mu     sync.Mutex
	items  []T
	signal chan struct{}
	done   bool
}

func newQueue[T any]() *queue[T] {
	return &queue[T]{signal: make(chan struct{}, 1)}
}

func (q *queue[T]) push(v T) {
	q.mu.Lock()
	if q.done {
		q.mu.Unlock()
		return
	}
	q.items = append(q.items, v)
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *queue[T]) close() {
	q.mu.Lock()
	q.done = true
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// pop blocks until an item is available, the queue is closed and drained
// (ok=false), or stop is closed (ok=false).
func (q *queue[T]) pop(stop <-chan struct{}) (T, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			v := q.items[0]
			var zero T
			q.items[0] = zero
			q.items = q.items[1:]
			q.mu.Unlock()
			return v, true
		}
		done := q.done
		q.mu.Unlock()
		if done {
			var zero T
			return zero, false
		}
		select {
		case <-q.signal:
		case <-stop:
			var zero T
			return zero, false
		}
	}
}

// tailBuffer keeps the last max bytes written to it (stderr tail).
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
