package runner

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// countingAdapter counts provider starts; each session runs until release
// is closed.
type countingAdapter struct {
	starts  atomic.Int32
	release chan struct{}
}

func (a *countingAdapter) Name() string  { return "codex" }
func (a *countingAdapter) Label() string { return "Codex" }
func (a *countingAdapter) Probe(context.Context) protocol.ProviderInstallation {
	return protocol.ProviderInstallation{}
}

func (a *countingAdapter) Start(context.Context, providers.StartSpec) (providers.Session, error) {
	a.starts.Add(1)
	events := make(chan providers.Event)
	go func() {
		<-a.release
		close(events)
	}()
	return &heldSession{events: events}, nil
}

type heldSession struct{ events chan providers.Event }

func (s *heldSession) Events() <-chan providers.Event { return s.events }
func (s *heldSession) SendInput(context.Context, string) (string, error) {
	return "", nil
}
func (s *heldSession) ResolveApproval(context.Context, string, providers.ApprovalDecision) error {
	return nil
}
func (s *heldSession) AnswerQuestion(context.Context, string, providers.QuestionAnswer) error {
	return nil
}
func (s *heldSession) Cancel(context.Context) error { return nil }
func (s *heldSession) Wait() providers.Result {
	return providers.Result{Outcome: protocol.OutcomeSucceeded, ExitConfirmed: true}
}

// hubPeer is the hub end of a runner connection: it collects every frame
// the runner writes.
func hubPeer(t *testing.T) (*websocket.Conn, <-chan protocol.Frame) {
	t.Helper()
	frames := make(chan protocol.Frame, 256)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, b, err := c.Read(req.Context())
			if err != nil {
				return
			}
			var f protocol.Frame
			if json.Unmarshal(b, &f) == nil {
				frames <- f
			}
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c, frames
}

func testRunner(t *testing.T, dir string, adapter providers.Adapter) *Runner {
	t.Helper()
	p := Paths{dir}
	j, err := OpenJournal(filepath.Join(dir, "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	r := &Runner{opts: Options{Slots: 2, Adapters: map[string]providers.Adapter{"codex": adapter}, BridgeExe: "yip"},
		paths: p, journal: j, ws: newWorkspaces(p), log: slog.New(slog.DiscardHandler),
		runs: map[string]*activeRun{}, tokens: map[string]string{}, tools: map[string]*pendingCall{},
		approvals: map[string]*pendingApproval{}}
	r.recoverJournal()
	return r
}

// nextOfType returns the next frame of the given type the runner wrote.
func nextOfType(t *testing.T, frames <-chan protocol.Frame, typ string) protocol.Frame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f := <-frames:
			if f.Type == typ {
				return f
			}
		case <-deadline:
			t.Fatalf("the runner never sent %s", typ)
		}
	}
}

// A start command the hub redelivers (its outbox resends until acknowledged,
// including across either side restarting) must never start the provider a
// second time for the same attempt.
func TestRedeliveredStartRunsTheProviderOnce(t *testing.T) {
	dir := t.TempDir()
	adapter := &countingAdapter{release: make(chan struct{})}
	r := testRunner(t, dir, adapter)
	c, frames := hubPeer(t)
	ctx := context.Background()

	m := protocol.ExecutionManifest{RunID: "run-1", JobID: "job-1", Provider: "codex", Mode: protocol.ModeConversation}
	offerPayload, _ := json.Marshal(protocol.OfferRun{Manifest: m, LeaseMs: 60_000})
	offer := protocol.Frame{Type: protocol.CmdOfferRun, ID: "offer:run-1", RunID: "run-1", LeaseEpoch: 1, Payload: offerPayload}
	start := protocol.Frame{Type: protocol.CmdStartRun, ID: "start:run-1", RunID: "run-1", LeaseEpoch: 1, Payload: []byte(`{"runId":"run-1"}`)}

	if err := r.onOffer(c, offer); err != nil {
		t.Fatal(err)
	}
	var ack protocol.RunAck
	_ = json.Unmarshal(nextOfType(t, frames, protocol.EvRunAck).Payload, &ack)
	if !ack.Accepted || ack.Duplicate {
		t.Fatalf("the first offer should be accepted: %+v", ack)
	}
	if err := r.onStart(ctx, c, start); err != nil {
		t.Fatal(err)
	}
	nextOfType(t, frames, protocol.EvCommandAck)
	waitFor(t, "the provider to start", func() bool { return adapter.starts.Load() == 1 })

	// The outbox resends the same command.
	if err := r.onStart(ctx, c, start); err != nil {
		t.Fatal(err)
	}
	var cmd protocol.CommandAck
	_ = json.Unmarshal(nextOfType(t, frames, protocol.EvCommandAck).Payload, &cmd)
	if !cmd.OK {
		t.Fatalf("the redelivered start should be acknowledged: %+v", cmd)
	}
	// A start under a new command ID for an attempt that already started.
	again := start
	again.ID = "start:run-1:again"
	if err := r.onStart(ctx, c, again); err != nil {
		t.Fatal(err)
	}
	nextOfType(t, frames, protocol.EvCommandAck)
	// The offer itself, resent after the start.
	if err := r.onOffer(c, offer); err != nil {
		t.Fatal(err)
	}
	ack = protocol.RunAck{}
	_ = json.Unmarshal(nextOfType(t, frames, protocol.EvRunAck).Payload, &ack)
	if !ack.Accepted || !ack.Duplicate {
		t.Fatalf("the repeated offer should get the journaled acceptance, marked duplicate: %+v", ack)
	}
	if n := adapter.starts.Load(); n != 1 {
		t.Fatalf("the provider started %d times for one attempt", n)
	}

	close(adapter.release)
	waitFor(t, "the attempt to finish", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.runs) == 0
	})
	if err := r.journal.Close(); err != nil {
		t.Fatal(err)
	}

	// The runner restarts on the same journal and the hub redelivers both.
	restarted := testRunner(t, dir, adapter)
	c2, frames2 := hubPeer(t)
	if err := restarted.onOffer(c2, offer); err != nil {
		t.Fatal(err)
	}
	ack = protocol.RunAck{}
	_ = json.Unmarshal(nextOfType(t, frames2, protocol.EvRunAck).Payload, &ack)
	if !ack.Duplicate {
		t.Fatalf("after a restart the offer should still be answered from the journal: %+v", ack)
	}
	if err := restarted.onStart(ctx, c2, start); err != nil {
		t.Fatal(err)
	}
	nextOfType(t, frames2, protocol.EvCommandAck)
	time.Sleep(200 * time.Millisecond)
	if n := adapter.starts.Load(); n != 1 {
		t.Fatalf("after a restart the provider started again: %d starts", n)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
