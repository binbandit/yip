package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/httpapi"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func sessionRunner(t *testing.T, handler http.HandlerFunc) *Runner {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	r := testRunner(t, t.TempDir(), &countingAdapter{release: make(chan struct{})})
	r.opts.StateDir = r.paths.Dir
	r.id.NodeID = "connection-test-node"
	r.id.HubURL = srv.URL
	if err := writeIdentity(r.paths.identity(), r.id); err != nil {
		t.Fatal(err)
	}
	r.httpc = srv.Client()
	r.heartbeat.Store(int64(10 * time.Second))
	r.stopMargin.Store(int64(10 * time.Second))
	// Avoid probing installed tools: this peer tests only the connection.
	r.caps.Store(protocol.RunnerCapabilities{})
	return r
}

func peerFrame(c *websocket.Conn, typ string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return peerEnvelope(c, protocol.Frame{Type: typ, Payload: b})
}

func peerEnvelope(c *websocket.Conn, f protocol.Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b)
}

func TestConnectionRecoversWithoutRestartingProvider(t *testing.T) {
	// The runner may probe toolchains, but this test must not run host tools.
	t.Setenv("PATH", t.TempDir())
	var connections atomic.Int32
	reconnected := make(chan protocol.Hello, 1)
	adapter := &countingAdapter{release: make(chan struct{})}
	manifest := protocol.ExecutionManifest{RunID: "run-reconnect", JobID: "job-reconnect", Provider: "codex", Mode: protocol.ModeConversation}
	offer, _ := json.Marshal(protocol.OfferRun{Manifest: manifest, LeaseMs: 10_000})
	start, _ := json.Marshal(map[string]string{"runId": manifest.RunID})
	r := sessionRunner(t, func(w http.ResponseWriter, req *http.Request) {
		c, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		n := connections.Add(1)
		var hello protocol.Hello
		redelivered := false
		for {
			_, b, err := c.Read(req.Context())
			if err != nil {
				return
			}
			var f protocol.Frame
			if json.Unmarshal(b, &f) != nil {
				return
			}
			switch f.Type {
			case protocol.EvHello:
				_ = json.Unmarshal(f.Payload, &hello)
				_ = peerFrame(c, protocol.CmdWelcome, protocol.Welcome{HeartbeatMs: 100, StopMarginMs: 1000})
				// Redeliver both commands on reconnect, as the real outbox can.
				_ = peerEnvelope(c, protocol.Frame{Type: protocol.CmdOfferRun, ID: "offer-reconnect", RunID: manifest.RunID, LeaseEpoch: 7, Payload: offer})
				_ = peerEnvelope(c, protocol.Frame{Type: protocol.CmdStartRun, ID: "start-reconnect", RunID: manifest.RunID, LeaseEpoch: 7, Payload: start})
			case protocol.EvCommandAck:
				var ack protocol.CommandAck
				_ = json.Unmarshal(f.Payload, &ack)
				redelivered = ack.CommandID == "start-reconnect" && ack.OK
			case protocol.EvHeartbeat:
				if n == 1 && adapter.starts.Load() > 0 {
					continue // lose only the return path once work is running
				}
				_ = peerFrame(c, protocol.CmdLease, protocol.LeaseRenewal{Leases: []protocol.Lease{{RunID: manifest.RunID, LeaseEpoch: 7, ExpiresInMs: 10_000}}})
				if n == 2 && redelivered {
					select {
					case reconnected <- hello:
					default:
					}
				}
			}
		}
	})
	r.opts.Adapters["codex"] = adapter
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	defer func() {
		close(adapter.release)
		cancel()
		if err := <-done; err != nil {
			t.Errorf("runner shutdown: %v", err)
		}
	}()
	select {
	case hello := <-reconnected:
		if len(hello.Runs) != 1 || hello.Runs[0].RunID != manifest.RunID || hello.Runs[0].LeaseEpoch != 7 || hello.Runs[0].Terminal != nil {
			t.Fatalf("reconnect lost the active journal state: %+v", hello.Runs)
		}
		if n := adapter.starts.Load(); n != 1 {
			t.Fatalf("provider started %d times across the reconnect", n)
		}
		r.mu.Lock()
		ar := r.runs[manifest.RunID]
		r.mu.Unlock()
		if ar == nil || ar.leaseLost.Load() || !ar.admit.Load() {
			t.Fatal("connection recovery interrupted the still-leased provider")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not reconnect after the return path stopped working")
	}
}

// Exercise the actual mutual-TLS endpoint and database heartbeat path. An
// idle hub must keep replying often enough to satisfy the silence timeout.
func TestIdleRealHubStaysConnectedAndRevocationStillStopsSession(t *testing.T) {
	ctx := context.Background()
	limits := domain.DefaultLimits()
	limits.HeartbeatInterval = 100 * time.Millisecond
	log := slog.New(slog.DiscardHandler)
	h, err := hub.Open(ctx, hub.Config{DataDir: t.TempDir(), Limits: limits, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	secret, _, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := h.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: "Connection test", Name: "Owner", Handle: "owner", Password: "connection-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(httpapi.NewRunnerServer(h, nil))
	srv.TLS, err = h.CA().ServerTLS([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	key, csr, err := auth.NewNodeKeyAndCSR("Connection test")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := h.PairLocal(ctx, "Connection test", csr)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "runner")
	_, err = SaveLocalIdentity(state, Identity{NodeID: pair.NodeID, HubURL: srv.URL}, key, []byte(pair.CertPEM), []byte(pair.CAPEM))
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Options{StateDir: state, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.journal.Close() })
	r.caps.Store(protocol.RunnerCapabilities{})
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- r.session(cctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "the idle runner to connect", func() bool {
		n, _ := store.GetNode(ctx, h.Store().R(), pair.NodeID)
		return n.Status == protocol.NodeOnline
	})
	started := time.Now()
	select {
	case err := <-done:
		done <- err
		t.Fatalf("healthy idle hub lost its connection: %v", err)
	case <-time.After(time.Second): // more than three full silence windows
	}
	n, err := store.GetNode(ctx, h.Store().R(), pair.NodeID)
	if err != nil || n.LastSeenAt == nil || !n.LastSeenAt.After(started.Add(500*time.Millisecond)) {
		t.Fatalf("idle heartbeats stopped: last seen %v, err %v", n.LastSeenAt, err)
	}
	if err := h.RevokeNode(ctx, owner.ID, pair.NodeID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		done <- err
		if !errors.Is(err, errRevoked) {
			t.Fatalf("revocation returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("revocation did not stop the session")
	}
}

// A connected TCP socket can still accept outbound heartbeats after the
// return path has failed. Reconnect without waiting for a transport error.
func TestSessionEndsWhenHubStopsResponding(t *testing.T) {
	var heartbeats atomic.Int32
	r := sessionRunner(t, func(w http.ResponseWriter, req *http.Request) {
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
			if json.Unmarshal(b, &f) != nil {
				return
			}
			switch f.Type {
			case protocol.EvHello:
				_ = peerFrame(c, protocol.CmdWelcome, protocol.Welcome{HeartbeatMs: 50})
			case protocol.EvHeartbeat:
				heartbeats.Add(1)
				// Swallow traffic without closing the socket or replying.
			}
		}
	})
	r.heartbeat.Store(int64(50 * time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.session(ctx) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "hub did not respond") {
			t.Fatalf("expected a useful connection timeout, got %v", err)
		}
		if heartbeats.Load() < 2 {
			t.Fatal("connection ended before exercising repeated outbound heartbeats")
		}
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("runner remained connected to a silent hub instead of reconnecting")
	}
}

func TestSessionUsesNegotiatedHeartbeatInterval(t *testing.T) {
	var heartbeats atomic.Int32
	r := sessionRunner(t, func(w http.ResponseWriter, req *http.Request) {
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
			if json.Unmarshal(b, &f) != nil {
				return
			}
			switch f.Type {
			case protocol.EvHello:
				_ = peerFrame(c, protocol.CmdWelcome, protocol.Welcome{HeartbeatMs: 50})
			case protocol.EvHeartbeat:
				heartbeats.Add(1)
				_ = peerFrame(c, protocol.CmdLease, protocol.LeaseRenewal{})
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.session(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case err := <-done:
		done <- err
		t.Fatalf("healthy idle connection ended: %v", err)
	case <-time.After(500 * time.Millisecond):
		if n := heartbeats.Load(); n < 3 {
			t.Fatalf("sent %d heartbeats; runner kept its old interval after welcome", n)
		}
	}
}
