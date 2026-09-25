package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// peer is the "agent" end of an in-memory connection.
type peer struct {
	t   *testing.T
	in  *bufio.Scanner // what the client wrote
	out io.WriteCloser // what the agent writes to the client
}

func (p *peer) read() Message {
	p.t.Helper()
	if !p.in.Scan() {
		p.t.Fatalf("peer: no message: %v", p.in.Err())
	}
	var m Message
	if err := json.Unmarshal(p.in.Bytes(), &m); err != nil {
		p.t.Fatalf("peer: bad json %q: %v", p.in.Text(), err)
	}
	return m
}

func (p *peer) write(s string) {
	p.t.Helper()
	if _, err := io.WriteString(p.out, s+"\n"); err != nil {
		p.t.Fatalf("peer write: %v", err)
	}
}

type recHandler struct {
	mu       sync.Mutex
	reqs     []*Request
	notes    []string
	onReq    func(*Request)
	gotNotes chan string
}

func (h *recHandler) HandleRequest(r *Request) {
	h.mu.Lock()
	h.reqs = append(h.reqs, r)
	h.mu.Unlock()
	if h.onReq != nil {
		h.onReq(r)
	}
}

func (h *recHandler) HandleNotification(method string, params json.RawMessage) {
	h.mu.Lock()
	h.notes = append(h.notes, method+" "+string(params))
	h.mu.Unlock()
	if h.gotNotes != nil {
		h.gotNotes <- method
	}
}

func pipeConn(t *testing.T, h Handler) (*Conn, *peer) {
	cr, aw := io.Pipe() // agent -> client
	ar, cw := io.Pipe() // client -> agent
	c := NewConn(cr, cw, h)
	go c.Serve()
	sc := bufio.NewScanner(ar)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	t.Cleanup(func() { aw.Close(); ar.Close() })
	return c, &peer{t: t, in: sc, out: aw}
}

func TestCallResponseAndNotificationOrder(t *testing.T) {
	h := &recHandler{}
	c, p := pipeConn(t, h)
	done := make(chan error, 1)
	var res PromptResult
	go func() {
		done <- c.Call(context.Background(), MethodSessionPrompt, PromptParams{SessionID: "s", Prompt: []ContentBlock{TextBlock("hi")}}, &res)
	}()
	m := p.read()
	if m.Method != MethodSessionPrompt || string(m.ID) != "1" || m.JSONRPC != "2.0" {
		t.Fatalf("request = %+v", m)
	}
	p.write(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"a"}}}}`)
	p.write(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if res.StopReason != StopEndTurn {
		t.Errorf("stopReason = %q", res.StopReason)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notes) != 1 {
		t.Errorf("notification must be handled before the response is delivered; notes = %v", h.notes)
	}
}

func TestCallErrorAndClose(t *testing.T) {
	c, p := pipeConn(t, &recHandler{})
	done := make(chan error, 1)
	go func() {
		done <- c.Call(context.Background(), MethodSessionNew, NewSessionParams{Cwd: "/x", MCPServers: []MCPServerStdio{}}, nil)
	}()
	m := p.read()
	var np map[string]any
	_ = json.Unmarshal(m.Params, &np)
	if b, _ := json.Marshal(np["mcpServers"]); string(b) != "[]" {
		t.Errorf("mcpServers must be an empty array, got %s", b)
	}
	p.write(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"Authentication required"}}`)
	err := <-done
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeAuthRequired {
		t.Fatalf("err = %v", err)
	}

	go func() { done <- c.Call(context.Background(), MethodSessionPrompt, nil, nil) }()
	p.read()
	p.out.Close()
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("pending call after close: %v", err)
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after close: %v", err)
	}
}

func TestIncomingRequestsAnsweredLaterAndOnce(t *testing.T) {
	h := &recHandler{}
	reqCh := make(chan *Request, 1)
	h.onReq = func(r *Request) { reqCh <- r }
	_, p := pipeConn(t, h)
	// String IDs must be echoed exactly.
	p.write(`{"jsonrpc":"2.0","id":"perm-7","method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"c1"},"options":[{"optionId":"allow-once","name":"Allow","kind":"allow_once"}]}}`)
	r := <-reqCh
	go func() {
		time.Sleep(20 * time.Millisecond) // a human deciding
		_ = r.Reply(Selected("allow-once"))
	}()
	m := p.read()
	if string(m.ID) != `"perm-7"` || string(m.Result) != `{"outcome":{"outcome":"selected","optionId":"allow-once"}}` {
		t.Fatalf("reply = %s / %s", m.ID, m.Result)
	}
	if err := r.Reply(CancelledPermission()); err == nil {
		t.Error("second reply must be refused")
	}
	if !r.Replied() {
		t.Error("Replied() = false")
	}
}

func TestUnhandledRequestGetsMethodNotFound(t *testing.T) {
	_, p := pipeConn(t, nil)
	p.write(`{"jsonrpc":"2.0","id":9,"method":"_vendor/thing","params":{}}`)
	m := p.read()
	if m.Error == nil || m.Error.Code != CodeMethodNotFound || string(m.ID) != "9" {
		t.Fatalf("reply = %+v", m)
	}
}

func TestMalformedLinesAreReportedNotFatal(t *testing.T) {
	h := &recHandler{gotNotes: make(chan string, 1)}
	c, p := pipeConn(t, h)
	var bad []string
	var mu sync.Mutex
	c.OnMalformed = func(line []byte, err error) {
		mu.Lock()
		bad = append(bad, string(line))
		mu.Unlock()
	}
	p.write(`this is a log line on stdout`)
	p.write(`{"jsonrpc":"2.0","method":"x/ping"}`)
	<-h.gotNotes
	mu.Lock()
	defer mu.Unlock()
	if len(bad) != 1 || !strings.Contains(bad[0], "log line") {
		t.Errorf("malformed = %v", bad)
	}
}

func TestSessionUpdateContentShapes(t *testing.T) {
	var n SessionNotification
	if err := json.Unmarshal([]byte(`{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"hi"}}}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.Update.Content == nil || n.Update.Content.Text != "hi" || n.Update.MessageID != "m1" {
		t.Errorf("chunk = %+v", n.Update)
	}
	if err := json.Unmarshal([]byte(`{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"t","status":"completed","kind":"edit","content":[{"type":"diff","path":"/a","oldText":null,"newText":"x"}],"rawInput":{"path":"/a"}}}`), &n); err != nil {
		t.Fatal(err)
	}
	u := n.Update
	if u.ToolCallID != "t" || u.Status != StatusCompleted || len(u.ToolCall.Content) != 1 || u.ToolCall.Content[0].Path != "/a" || u.ToolCall.Content[0].OldText != nil {
		t.Errorf("tool update = %+v", u)
	}
	if err := json.Unmarshal([]byte(`{"sessionId":"s","update":{"sessionUpdate":"usage_update","used":5,"size":10,"cost":{"amount":1.5,"currency":"USD"}}}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.Update.Cost == nil || n.Update.Cost.Amount != 1.5 || *n.Update.Used != 5 {
		t.Errorf("usage = %+v", n.Update)
	}
}

func TestConfigOptionValuesFlatAndGrouped(t *testing.T) {
	var opts []ConfigOption
	raw := `[{"id":"mode","name":"Mode","category":"mode","type":"select","currentValue":"agent","options":[{"value":"agent","name":"Agent"},{"value":"ask","name":"Ask"}]},
	{"id":"m","name":"Model","category":"model","type":"select","currentValue":"a","options":[{"group":"g1","name":"G1","options":[{"value":"a","name":"A"}]},{"group":"g2","name":"G2","options":[{"value":"b","name":"B"}]}]}]`
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		t.Fatal(err)
	}
	mode, ok := FindConfig(opts, "mode")
	if !ok || mode.Current() != "agent" || len(mode.Values()) != 2 {
		t.Errorf("mode = %+v", mode)
	}
	model, ok := FindConfig(opts, "model")
	if !ok || model.ID != "m" {
		t.Fatalf("model lookup by category failed")
	}
	vals := model.Values()
	if len(vals) != 2 || vals[0].Value != "a" || vals[1].Value != "b" {
		t.Errorf("grouped values = %+v", vals)
	}
}

func TestToolCallMerge(t *testing.T) {
	tc := ToolCall{ToolCallID: "t", Title: "Edit", Kind: KindEdit, Status: StatusPending, RawInput: json.RawMessage(`{"path":"a"}`)}
	tc.Merge(ToolCall{ToolCallID: "t", Status: StatusCompleted, RawInput: json.RawMessage(`null`)})
	if tc.Title != "Edit" || tc.Kind != KindEdit || tc.Status != StatusCompleted || string(tc.RawInput) != `{"path":"a"}` {
		t.Errorf("merged = %+v", tc)
	}
}
