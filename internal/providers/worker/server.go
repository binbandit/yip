package worker

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/internal/providers/acp"
	"github.com/binbandit/yip/protocol"
)

// Serve exposes one adapter session (or a probe) over newline-delimited JSON
// RPC. EOF cancels the adapter. Production callers should supply closable
// streams, so cancellation can interrupt blocked reads and writes.
func Serve(ctx context.Context, in io.Reader, out io.Writer, adapter providers.Adapter, version string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &server{ctx: ctx, adapter: adapter, version: version, finished: make(chan struct{})}
	s.conn = acp.NewConn(frames(in), out, dispatcher{request: s.request})
	s.conn.OnMalformed = func([]byte, error) { cancel() }
	go s.conn.Serve()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-s.conn.Done():
		err = s.conn.Err()
		if errors.Is(err, io.EOF) {
			err = nil
		}
	case <-s.finished:
	}
	cancel()
	// Release blocked RPC writers before asking the adapter to shut down.
	if c, ok := in.(io.Closer); ok {
		_ = c.Close()
	}
	if c, ok := out.(io.Closer); ok {
		_ = c.Close()
	}
	s.mu.Lock()
	s.stopped = true
	session, local, complete := s.session, s.local, s.complete
	s.mu.Unlock()
	if local != nil {
		local.close()
	}
	if session != nil && !complete {
		cancelSession(session)
	}
	return err
}

type server struct {
	ctx                        context.Context
	adapter                    providers.Adapter
	version                    string
	conn                       *acp.Conn
	mu                         sync.Mutex
	started, stopped, complete bool
	session                    providers.Session
	local                      *localBridge
	finished                   chan struct{}
}

func (s *server) request(r *acp.Request) {
	switch r.Method {
	case "probe":
		_ = r.Reply(s.adapter.Probe(s.ctx))
		return
	case "start":
		s.start(r)
		return
	}
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session == nil {
		_ = r.ReplyError(acp.CodeInvalidRequest, "worker session has not started")
		return
	}
	switch r.Method {
	case "session/sendInput":
		var p inputRequest
		if decode(r, &p) {
			mode, err := session.SendInput(s.ctx, p.Text)
			reply(r, mode, err)
		}
	case "session/resolveApproval":
		var p approvalRequest
		if decode(r, &p) {
			reply(r, struct{}{}, session.ResolveApproval(s.ctx, p.ID, p.Decision))
		}
	case "session/answerQuestion":
		var p questionRequest
		if decode(r, &p) {
			reply(r, struct{}{}, session.AnswerQuestion(s.ctx, p.ID, p.Answer))
		}
	case "session/cancel":
		ctx, cancel := context.WithTimeout(s.ctx, stopGrace)
		defer cancel()
		reply(r, struct{}{}, session.Cancel(ctx))
	default:
		_ = r.MethodNotFound()
	}
}

func (s *server) start(r *acp.Request) {
	var spec providers.StartSpec
	if !decode(r, &spec) {
		return
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		_ = r.ReplyError(acp.CodeInvalidRequest, "worker already started")
		return
	}
	s.started = true
	s.mu.Unlock()

	local, err := newLocalBridge()
	if err != nil {
		reply(r, nil, err)
		return
	}
	defer local.close()
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.local = local
	s.mu.Unlock()
	spec.MCP = providers.MCPServer{
		Name: "yip", Command: "/usr/local/bin/yip",
		Args: []string{"bridge", "--mode", spec.Mode},
		Env:  map[string]string{bridge.EnvSocket: local.path, bridge.EnvToken: spec.MCP.Env[bridge.EnvToken]},
	}
	// The launcher owns the environment isolation boundary. A nil Env means
	// use that worker-local environment, never ship it back over the wire.
	if spec.Env == nil {
		spec.Env = os.Environ()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	if spec.Timeout > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(s.ctx, spec.Timeout)
	}
	defer cancel()
	go bridge.Serve(local, func(req bridge.LocalRequest) bridge.LocalResponse {
		var resp bridge.LocalResponse
		if err := call(ctx, s.conn, "bridge/call", req, &resp); err != nil {
			return bridge.LocalResponse{ID: req.ID, Error: &protocol.APIError{Code: "unavailable", Message: "Worker bridge connection closed.", Recoverable: true}}
		}
		resp.ID = req.ID
		return resp
	})
	session, err := s.adapter.Start(ctx, spec)
	if err != nil {
		reply(r, nil, err)
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		cancelSession(session)
		return
	}
	s.session, s.local = session, local
	s.mu.Unlock()
	if err := r.Reply(struct{ Version string }{s.version}); err != nil {
		cancelSession(session)
		return
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			cancelSession(session)
		case <-done:
		}
	}()
	// Drain events before publishing the terminal result. This is essential:
	// the client's ordered notification handler observes all events first.
	for {
		select {
		case event, ok := <-session.Events():
			if !ok {
				result := session.Wait()
				s.mu.Lock()
				s.complete = true
				s.mu.Unlock()
				_ = s.conn.Notify("session/result", result)
				close(s.finished)
				return
			}
			if err := s.conn.Notify("session/event", event); err != nil {
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// bridge.Serve owns accepted connections, but not their lifecycle after the
// listener is closed. Track them so EOF also closes idle MCP connections.
type localBridge struct {
	net.Listener
	path, dir string
	mu        sync.Mutex
	closed    bool
	conns     map[net.Conn]struct{}
}

func newLocalBridge() (*localBridge, error) {
	dir, err := os.MkdirTemp("/tmp", "yip-worker-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "bridge.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &localBridge{Listener: ln, dir: dir, path: path, conns: make(map[net.Conn]struct{})}, nil
}

func (b *localBridge) Accept() (net.Conn, error) {
	conn, err := b.Listener.Accept()
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	c := &trackedConn{Conn: conn, owner: b}
	b.conns[c] = struct{}{}
	return c, nil
}

func (b *localBridge) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	conns := make([]net.Conn, 0, len(b.conns))
	for c := range b.conns {
		conns = append(conns, c)
	}
	b.mu.Unlock()
	_ = b.Listener.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	_ = os.RemoveAll(b.dir)
}

type trackedConn struct {
	net.Conn
	owner *localBridge
}

func (c *trackedConn) Close() error {
	c.owner.mu.Lock()
	delete(c.owner.conns, c)
	c.owner.mu.Unlock()
	return c.Conn.Close()
}
