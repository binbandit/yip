// Package acp is a small, dependency-free client for the Agent Client
// Protocol (https://agentclientprotocol.com/protocol/v1/overview): JSON-RPC
// 2.0 over newline-delimited stdio, bidirectional (the agent also sends
// requests to the client, e.g. session/request_permission).
//
// The package only implements the client side of the wire protocol and the
// v1 message types yip uses. Provider specifics (Cursor extensions, mode and
// permission policy) live in the provider adapters.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// Standard JSON-RPC and ACP error codes
// (https://agentclientprotocol.com/protocol/v1/schema#errorcode).
const (
	CodeParseError       = -32700
	CodeInvalidRequest   = -32600
	CodeMethodNotFound   = -32601
	CodeInvalidParams    = -32602
	CodeInternalError    = -32603
	CodeRequestCancelled = -32800
	CodeAuthRequired     = -32000
	CodeResourceNotFound = -32002
)

// ErrClosed is returned for calls that cannot complete because the
// connection ended (the agent exited or closed stdout).
var ErrClosed = errors.New("acp: connection closed")

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("acp error %d: %s (%s)", e.Code, e.Message, truncate(string(e.Data), 300))
	}
	return fmt.Sprintf("acp error %d: %s", e.Code, e.Message)
}

// Message is one JSON-RPC 2.0 envelope (request, notification or response).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Handler receives agent-originated traffic.
//
// HandleRequest is called on the connection's read loop and must not block
// for long: blocking requests that need a human (permissions, questions)
// should be recorded and answered later with Request.Reply from any
// goroutine. Every request must eventually be answered; a handler that does
// not recognise a method should call Request.MethodNotFound.
//
// HandleNotification is called in wire order on the read loop, before any
// later response is delivered to its caller, so session/update streams are
// fully processed before the matching session/prompt call returns.
type Handler interface {
	HandleRequest(req *Request)
	HandleNotification(method string, params json.RawMessage)
}

// Request is an agent-originated JSON-RPC request awaiting a reply.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage

	conn    *Conn
	replied atomic.Bool
}

// Key returns a stable string form of the request ID.
func (r *Request) Key() string { return idKey(r.ID) }

// Replied reports whether a reply has been sent.
func (r *Request) Replied() bool { return r.replied.Load() }

// Reply sends a successful result. Only the first reply is sent; later calls
// return an error.
func (r *Request) Reply(result any) error {
	if !r.replied.CompareAndSwap(false, true) {
		return fmt.Errorf("acp: request %s already answered", r.Key())
	}
	raw, err := json.Marshal(result)
	if err != nil {
		// Never leave the agent waiting: fall back to an internal error.
		return r.conn.write(&Message{JSONRPC: "2.0", ID: r.ID, Error: &Error{Code: CodeInternalError, Message: "client failed to encode reply"}})
	}
	if string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	return r.conn.write(&Message{JSONRPC: "2.0", ID: r.ID, Result: raw})
}

// ReplyError sends a JSON-RPC error. Only the first reply is sent.
func (r *Request) ReplyError(code int, message string) error {
	if !r.replied.CompareAndSwap(false, true) {
		return fmt.Errorf("acp: request %s already answered", r.Key())
	}
	return r.conn.write(&Message{JSONRPC: "2.0", ID: r.ID, Error: &Error{Code: code, Message: message}})
}

// MethodNotFound answers with the standard -32601 error.
func (r *Request) MethodNotFound() error {
	return r.ReplyError(CodeMethodNotFound, "Method not found: "+r.Method)
}

// Conn is a bidirectional JSON-RPC 2.0 connection over newline-delimited
// JSON streams.
type Conn struct {
	r       *bufio.Reader
	w       io.Writer
	wmu     sync.Mutex
	handler Handler

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[string]chan *Message

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error

	// OnMalformed, if set, is called for stdout lines that are not valid
	// JSON-RPC (the agent should log to stderr only). Called on the read loop.
	OnMalformed func(line []byte, err error)
}

// NewConn creates a connection reading agent output from r and writing
// client messages to w. Call Serve to start the read loop.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	return &Conn{
		r:       bufio.NewReaderSize(r, 1<<16),
		w:       w,
		handler: h,
		pending: map[string]chan *Message{},
		closed:  make(chan struct{}),
	}
}

// Done is closed when the read loop ends.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// Err returns why the read loop ended (io.EOF for a clean close).
func (c *Conn) Err() error {
	select {
	case <-c.closed:
		return c.closeErr
	default:
		return nil
	}
}

// Serve runs the read loop until the stream ends. It returns the terminal
// read error (io.EOF on a clean close).
func (c *Conn) Serve() error {
	var err error
	for {
		var line []byte
		line, err = c.r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			break
		}
	}
	c.shutdown(err)
	return err
}

func (c *Conn) shutdown(err error) {
	c.closeOnce.Do(func() {
		c.closeErr = err
		close(c.closed)
		c.mu.Lock()
		for k, ch := range c.pending {
			delete(c.pending, k)
			close(ch)
		}
		c.mu.Unlock()
	})
}

func (c *Conn) dispatch(line []byte) {
	var m Message
	if err := json.Unmarshal(line, &m); err != nil || m.JSONRPC != "2.0" {
		if err == nil {
			err = errors.New("missing jsonrpc 2.0 marker")
		}
		if c.OnMalformed != nil {
			c.OnMalformed(line, err)
		}
		return
	}
	switch {
	case m.Method != "" && hasID(m.ID):
		req := &Request{ID: m.ID, Method: m.Method, Params: m.Params, conn: c}
		if c.handler == nil {
			_ = req.MethodNotFound()
			return
		}
		c.handler.HandleRequest(req)
	case m.Method != "":
		if c.handler != nil {
			c.handler.HandleNotification(m.Method, m.Params)
		}
	case hasID(m.ID):
		key := idKey(m.ID)
		c.mu.Lock()
		ch, ok := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ok {
			ch <- &m
			close(ch)
		}
	default:
		if c.OnMalformed != nil {
			c.OnMalformed(line, errors.New("response without id"))
		}
	}
}

// Call sends a request and waits for its response. The result is decoded
// into out when out is non-nil. A JSON-RPC error is returned as *Error.
func (c *Conn) Call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	idRaw := json.RawMessage(strconv.FormatInt(id, 10))
	var p json.RawMessage
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("acp: encode %s params: %w", method, err)
		}
		p = raw
	}
	ch := make(chan *Message, 1)
	key := idKey(idRaw)
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return ErrClosed
	default:
	}
	c.pending[key] = ch
	c.mu.Unlock()

	if err := c.write(&Message{JSONRPC: "2.0", ID: idRaw, Method: method, Params: p}); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok || m == nil {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 && string(m.Result) != "null" {
			if err := json.Unmarshal(m.Result, out); err != nil {
				return fmt.Errorf("acp: decode %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Notify sends a notification (no response expected).
func (c *Conn) Notify(method string, params any) error {
	var p json.RawMessage
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("acp: encode %s params: %w", method, err)
		}
		p = raw
	}
	return c.write(&Message{JSONRPC: "2.0", Method: method, Params: p})
}

func (c *Conn) write(m *Message) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(raw); err != nil {
		return fmt.Errorf("acp: write: %w", err)
	}
	return nil
}

func hasID(id json.RawMessage) bool {
	s := string(bytes.TrimSpace(id))
	return s != "" && s != "null"
}

// idKey normalises numeric and string IDs so responses match requests.
func idKey(id json.RawMessage) string {
	s := string(bytes.TrimSpace(id))
	if len(s) > 0 && s[0] == '"' {
		var str string
		if json.Unmarshal(id, &str) == nil {
			return "s:" + str
		}
	}
	return "n:" + s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
