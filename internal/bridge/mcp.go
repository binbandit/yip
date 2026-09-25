package bridge

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// Supported MCP protocol revisions, newest first.
var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServeMCP runs the stdio MCP server until stdin closes. Tool calls go to
// the runner through client. mode restricts the advertised tools.
func ServeMCP(in io.Reader, out io.Writer, client *Client, mode, version string) error {
	var wmu sync.Mutex
	write := func(m rpcMessage) {
		m.JSONRPC = "2.0"
		b, _ := json.Marshal(m)
		wmu.Lock()
		out.Write(append(b, '\n'))
		wmu.Unlock()
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	var wg sync.WaitGroup
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			write(rpcMessage{ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		if msg.Method == "" {
			continue // a response to a request we never send
		}
		isRequest := len(msg.ID) > 0 && string(msg.ID) != "null"
		switch msg.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			v := mcpVersions[0]
			for _, s := range mcpVersions {
				if s == p.ProtocolVersion {
					v = s
				}
			}
			write(rpcMessage{ID: msg.ID, Result: map[string]any{
				"protocolVersion": v,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "yip", "version": version},
				"instructions":    "yip workspace tools. Your identity, run, and permissions are fixed by the runner; tool arguments cannot change them.",
			}})
		case "notifications/initialized", "notifications/cancelled":
		case "ping":
			if isRequest {
				write(rpcMessage{ID: msg.ID, Result: map[string]any{}})
			}
		case "tools/list":
			var tools []Tool
			for _, t := range Tools {
				if t.Allowed(mode) {
					tools = append(tools, t)
				}
			}
			write(rpcMessage{ID: msg.ID, Result: map[string]any{"tools": tools}})
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				write(rpcMessage{ID: msg.ID, Error: &rpcError{Code: -32602, Message: "invalid params"}})
				continue
			}
			t, ok := Lookup(p.Name)
			if !ok || !t.Allowed(mode) {
				write(rpcMessage{ID: msg.ID, Result: toolText(fmt.Sprintf("Tool %q is not available in this run.", p.Name), true)})
				continue
			}
			if len(p.Arguments) == 0 {
				p.Arguments = json.RawMessage("{}")
			}
			wg.Add(1)
			go func(id json.RawMessage, name string, args json.RawMessage) {
				defer wg.Done()
				resp := client.Call(name, args)
				if resp.OK {
					text := string(resp.Result)
					var s string
					if json.Unmarshal(resp.Result, &s) == nil {
						text = s // plain-text results (e.g. permission decisions)
					}
					write(rpcMessage{ID: id, Result: toolText(text, false)})
					return
				}
				msgText := "The tool call failed."
				if resp.Error != nil {
					b, _ := json.Marshal(resp.Error)
					msgText = string(b)
				}
				write(rpcMessage{ID: id, Result: toolText(msgText, true)})
			}(msg.ID, p.Name, p.Arguments)
		default:
			if isRequest {
				write(rpcMessage{ID: msg.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + msg.Method}})
			}
		}
	}
	wg.Wait()
	return sc.Err()
}

func toolText(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// RunFromEnv is the `yip bridge` entrypoint.
func RunFromEnv(mode, version string) error {
	socket, token := os.Getenv(EnvSocket), os.Getenv(EnvToken)
	if socket == "" || token == "" {
		return fmt.Errorf("yip bridge must be launched by a yip runner (%s and %s unset)", EnvSocket, EnvToken)
	}
	c, err := Dial(socket, token)
	if err != nil {
		return err
	}
	defer c.Close()
	return ServeMCP(os.Stdin, os.Stdout, c, mode, version)
}
