package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// MCPClient speaks MCP over stdio to a server process, so tests can call yip
// tools through the real `yip bridge` exactly as a provider CLI would.
type MCPClient struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcResponse
	done    chan struct{}
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// StartMCP launches cmd and completes the MCP initialize handshake.
func StartMCP(cmd *exec.Cmd) (*MCPClient, error) {
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &MCPClient{cmd: cmd, in: in, pending: map[int]chan rpcResponse{}, done: make(chan struct{})}
	go c.read(out)
	if _, err := c.request("initialize", map[string]any{
		"protocolVersion": mcpVersions[0],
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "yip-bridge-test", "version": "1"},
	}); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	c.notify("notifications/initialized", map[string]any{})
	return c, nil
}

func (c *MCPClient) read(out io.Reader) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		var m struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.ID == nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- rpcResponse{Result: m.Result, Error: m.Error}
		}
	}
	c.mu.Lock()
	for id, ch := range c.pending {
		ch <- rpcResponse{Error: &rpcError{Code: -32000, Message: "server exited"}}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

func (c *MCPClient) request(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	_, err := c.in.Write(append(b, '\n'))
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	r := <-ch
	if r.Error != nil {
		return nil, errors.New(r.Error.Message)
	}
	return r.Result, nil
}

func (c *MCPClient) notify(method string, params any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	c.mu.Lock()
	_, _ = c.in.Write(append(b, '\n'))
	c.mu.Unlock()
}

// ListTools returns the advertised tool names.
func (c *MCPClient) ListTools() ([]string, error) {
	raw, err := c.request("tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var r struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	names := make([]string, len(r.Tools))
	for i, t := range r.Tools {
		names[i] = t.Name
	}
	return names, nil
}

// CallTool invokes a tool and returns its text content and error flag.
func (c *MCPClient) CallTool(name string, args any) (string, bool, error) {
	raw, err := c.request("tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", true, err
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", true, err
	}
	text := ""
	for _, ct := range r.Content {
		if ct.Type == "text" {
			text += ct.Text
		}
	}
	return text, r.IsError, nil
}

// Close ends the session and waits for the server to exit.
func (c *MCPClient) Close() error {
	_ = c.in.Close()
	<-c.done
	return c.cmd.Wait()
}

var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
