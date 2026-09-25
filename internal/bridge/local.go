package bridge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/binbandit/yip/protocol"
)

// Environment variables the runner sets when launching `yip bridge`.
const (
	EnvSocket = "YIP_BRIDGE_SOCKET"
	EnvToken  = "YIP_BRIDGE_TOKEN"
)

// LocalRequest is one tool call from the bridge process to the runner. The
// token identifies the run; it is a per-run secret held only by the provider
// process the runner launched.
type LocalRequest struct {
	Token string          `json:"token"`
	ID    string          `json:"id"`
	Tool  string          `json:"tool"`
	Args  json.RawMessage `json:"args"`
}

type LocalResponse struct {
	ID     string             `json:"id"`
	OK     bool               `json:"ok"`
	Result json.RawMessage    `json:"result,omitempty"`
	Error  *protocol.APIError `json:"error,omitempty"`
}

// Client is the bridge's connection to the runner socket.
type Client struct {
	token   string
	conn    net.Conn
	mu      sync.Mutex
	pending map[string]chan LocalResponse
	seq     int
	closed  chan struct{}
	err     error
}

// Dial connects to the runner's bridge socket.
func Dial(socket, token string) (*Client, error) {
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to runner: %w", err)
	}
	c := &Client{token: token, conn: conn, pending: map[string]chan LocalResponse{}, closed: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *Client) readLoop() {
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r LocalResponse
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[r.ID]
		delete(c.pending, r.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- r
		}
	}
	c.mu.Lock()
	c.err = errors.New("runner connection closed")
	for id, ch := range c.pending {
		ch <- LocalResponse{ID: id, Error: &protocol.APIError{Code: "unavailable", Message: "The runner connection closed.", Recoverable: true}}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.closed)
}

// Call sends a tool call and waits for its result. Calls that need a human
// decision (permission prompts) may wait a long time.
func (c *Client) Call(tool string, args json.RawMessage) LocalResponse {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return LocalResponse{Error: &protocol.APIError{Code: "unavailable", Message: c.err.Error(), Recoverable: true}}
	}
	c.seq++
	id := fmt.Sprintf("b%d-%d", time.Now().UnixNano(), c.seq)
	ch := make(chan LocalResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(LocalRequest{Token: c.token, ID: id, Tool: tool, Args: args})
	c.mu.Lock()
	_, err := c.conn.Write(append(b, '\n'))
	c.mu.Unlock()
	if err != nil {
		return LocalResponse{ID: id, Error: &protocol.APIError{Code: "unavailable", Message: "Could not reach the runner.", Recoverable: true}}
	}
	return <-ch
}

func (c *Client) Close() error { return c.conn.Close() }

// Handler executes a local request on the runner side.
type Handler func(req LocalRequest) LocalResponse

// Serve accepts bridge connections on a unix socket and dispatches each
// request concurrently to h.
func Serve(ln net.Listener, h Handler) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			var wmu sync.Mutex
			sc := bufio.NewScanner(conn)
			sc.Buffer(make([]byte, 1<<20), 16<<20)
			for sc.Scan() {
				var req LocalRequest
				if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
					continue
				}
				go func(req LocalRequest) {
					resp := h(req)
					resp.ID = req.ID
					b, _ := json.Marshal(resp)
					wmu.Lock()
					_, _ = conn.Write(append(b, '\n'))
					wmu.Unlock()
				}(req)
			}
		}(conn)
	}
}
