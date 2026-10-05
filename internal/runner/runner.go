package runner

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/binbandit/yip/internal/auth"
	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// Options configures a runner.
type Options struct {
	StateDir string
	Slots    int
	// Adapters available to this runner, keyed by provider name.
	Adapters map[string]providers.Adapter
	// BridgeExe is the yip executable providers launch as the MCP bridge.
	BridgeExe string
	// ExecutionProfile is native, container (the entire runner is already
	// containerized), or docker (a fresh restricted container per attempt).
	ExecutionProfile string
	Docker           DockerOptions
	Version          string
	Logger           *slog.Logger
	// ServerName overrides TLS verification of the hub name (e.g. 127.0.0.1).
	ServerName string
}

// Runner is a paired, running machine.
type Runner struct {
	opts    Options
	id      Identity
	paths   Paths
	journal *Journal
	ws      *Workspaces
	tlsConf *tls.Config
	httpc   *http.Client
	log     *slog.Logger

	mu         sync.Mutex
	conn       *websocket.Conn
	connCtx    context.Context
	runs       map[string]*activeRun
	tokens     map[string]string
	tools      map[string]*pendingCall
	approvals  map[string]*pendingApproval
	draining   bool
	heartbeat  atomic.Int64 // nanoseconds; set by the hub's welcome
	stopMargin atomic.Int64
	sendMu     sync.Mutex
	bridgeLn   net.Listener
	caps       atomic.Value // protocol.RunnerCapabilities
	shutdown   atomic.Bool
	uncertain  atomic.Bool // a container may still be using a workspace
}

// pendingCall is a tool call forwarded to the hub and not yet answered. It
// is re-sent on reconnect; the hub returns the recorded result for a call
// that already committed.
type pendingCall struct {
	frame protocol.Frame
	ch    chan protocol.ToolResult
}

type pendingApproval struct {
	runID string
	frame protocol.Frame
	ch    chan protocol.ResolveApproval
}

type activeRun struct {
	mu         sync.Mutex
	m          protocol.ExecutionManifest
	epoch      int64
	leaseUntil time.Time
	started    bool
	dropped    bool // removed before it started; must never start
	session    providers.Session
	container  *dockerContainer
	checks     map[string]*dockerContainer
	ws         *Workspace
	token      string
	cancel     context.CancelFunc
	admit      atomic.Bool
	leaseLost  atomic.Bool
	cancelled  atomic.Bool
	done       chan struct{}
	queued     []protocol.DeliverInput
	activity   string
}

// New opens a paired runner's state.
func New(opts Options) (*Runner, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Slots <= 0 {
		opts.Slots = 2
	}
	if opts.ExecutionProfile == "" {
		opts.ExecutionProfile = "native"
	}
	switch opts.ExecutionProfile {
	case "native", "container", "docker":
	default:
		return nil, fmt.Errorf("unknown execution profile %q (use native, container, or docker)", opts.ExecutionProfile)
	}
	if opts.Docker.Image == "" {
		opts.Docker.Image = DefaultDockerImage
	}
	if opts.BridgeExe == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		opts.BridgeExe = exe
	}
	id, err := LoadIdentity(opts.StateDir)
	if err != nil {
		return nil, err
	}
	p := Paths{opts.StateDir}
	keyPEM, err := os.ReadFile(p.key())
	if err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(p.cert())
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(p.ca())
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(id.HubURL)
	if err != nil {
		return nil, err
	}
	serverName := opts.ServerName
	if serverName == "" {
		serverName = u.Hostname()
	}
	tlsConf, err := auth.ClientTLS(caPEM, certPEM, keyPEM, serverName)
	if err != nil {
		return nil, err
	}
	j, err := OpenJournal(p.journal())
	if err != nil {
		return nil, err
	}
	r := &Runner{opts: opts, id: id, paths: p, journal: j, ws: newWorkspaces(p), tlsConf: tlsConf, log: opts.Logger,
		httpc: &http.Client{Timeout: 10 * time.Minute, Transport: &http.Transport{TLSClientConfig: tlsConf}},
		runs:  map[string]*activeRun{}, tokens: map[string]string{}, tools: map[string]*pendingCall{},
		approvals: map[string]*pendingApproval{}}
	r.ws.isolated = opts.ExecutionProfile == "docker"
	r.heartbeat.Store(int64(10 * time.Second))
	r.stopMargin.Store(int64(10 * time.Second))
	r.recoverJournal()
	return r, nil
}

// ID returns the node identity.
func (r *Runner) ID() Identity { return r.id }

// recoverJournal marks attempts that were running when the runner stopped.
// Their provider process may have outlived the runner, so the outcome is
// reported as unknown rather than as a safe failure.
func (r *Runner) recoverJournal() {
	runs, err := r.journal.Runs()
	if err != nil {
		return
	}
	for _, jr := range runs {
		if jr.Terminal != nil {
			continue
		}
		t := protocol.RunTerminal{Outcome: protocol.OutcomeUnknown, ExitConfirmed: false, LastSeq: jr.LastSeq,
			Error: "the runner restarted while this attempt was " + jr.State + "; its provider process may still have been running"}
		if jr.State == "accepted" {
			// Accepted but never started: nothing ran.
			t = protocol.RunTerminal{Outcome: protocol.OutcomeFailed, ExitConfirmed: true, LastSeq: jr.LastSeq,
				Error: "the runner restarted before this attempt started"}
		}
		_ = r.journal.SetTerminal(jr.RunID, t)
	}
}

// Run connects to the hub and serves until ctx ends, reconnecting with backoff.
func (r *Runner) Run(ctx context.Context) error {
	if r.opts.ExecutionProfile == "docker" {
		cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := dockerEngineReady(cctx)
		if err == nil {
			err = r.reapDocker(cctx)
		}
		if err == nil {
			err = r.dockerReady(cctx)
		}
		cancel()
		if err != nil {
			return err
		}
	}
	ln, err := r.listenBridge()
	if err != nil {
		return err
	}
	r.bridgeLn = ln
	defer ln.Close()
	go bridge.Serve(ln, r.handleBridge)
	go r.leaseMonitor(ctx)
	go r.probeLoop(ctx)
	backoff := time.Second
	for {
		start := time.Now()
		err := r.session(ctx)
		if ctx.Err() != nil {
			r.stopAll()
			return nil
		}
		if errors.Is(err, errRevoked) {
			r.stopAll()
			return err
		}
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		r.log.Warn("hub connection ended; reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			r.stopAll()
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

var errRevoked = errors.New("this machine's credential was revoked by the hub")

func (r *Runner) listenBridge() (net.Listener, error) {
	sock := r.paths.socketPath()
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("bridge socket: %w", err)
	}
	_ = os.Chmod(sock, 0o600)
	return ln, nil
}

func (r *Runner) wsURL() string {
	u := strings.TrimRight(r.id.HubURL, "/")
	return "wss" + strings.TrimPrefix(u, "https") + "/v1/runner/connect"
}

// session runs one connection until it fails.
func (r *Runner) session(ctx context.Context) error {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	c, resp, err := websocket.Dial(dctx, r.wsURL(), &websocket.DialOptions{HTTPClient: r.httpc, CompressionMode: websocket.CompressionDisabled})
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("hub refused this machine's certificate: %w", err)
		}
		return err
	}
	c.SetReadLimit(32 << 20)
	cctx, ccancel := context.WithCancel(ctx)
	defer ccancel()
	r.mu.Lock()
	r.conn, r.connCtx = c, cctx
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.conn == c {
			r.conn = nil
		}
		r.mu.Unlock()
		c.CloseNow()
	}()

	host, _ := os.Hostname()
	hello := protocol.Hello{NodeID: r.id.NodeID, ProtocolVersion: protocol.RunnerProtocolVersion, RunnerVersion: r.opts.Version,
		Hostname: host, OS: runtimeOS(), Arch: runtimeArch(), BootTime: bootTime}
	jruns, _ := r.journal.Runs()
	for _, jr := range jruns {
		js := protocol.JournalRunState{RunID: jr.RunID, LeaseEpoch: jr.Epoch, State: jr.State, Terminal: jr.Terminal, LastSeq: jr.LastSeq,
			UnackedFrom: jr.AckedSeq + 1, Workspace: jr.Workspace, LastActivity: jr.Activity}
		hello.Runs = append(hello.Runs, js)
	}
	if err := r.writeFrame(c, protocol.EvHello, "", 0, hello); err != nil {
		return err
	}
	if caps, ok := r.caps.Load().(protocol.RunnerCapabilities); ok {
		_ = r.writeFrame(c, protocol.EvCapabilities, "", 0, caps)
	} else {
		go r.probeOnce(ctx)
	}
	// Replay journaled but unacknowledged events and terminals.
	for _, jr := range jruns {
		frames, _ := r.journal.Unacked(jr.RunID)
		for _, f := range frames {
			if err := r.sendRaw(c, f); err != nil {
				return err
			}
		}
		if jr.Terminal != nil && !jr.TermAcked {
			t := *jr.Terminal
			t.LastSeq = r.journal.LastSeq(jr.RunID)
			_ = r.writeFrame(c, protocol.EvRunTerminal, jr.RunID, jr.Epoch, t)
		}
	}
	// Re-send approval requests and tool calls still waiting for an answer.
	r.mu.Lock()
	var pend []protocol.Frame
	for _, p := range r.approvals {
		pend = append(pend, p.frame)
	}
	for _, p := range r.tools {
		pend = append(pend, p.frame)
	}
	r.mu.Unlock()
	for _, f := range pend {
		_ = r.sendRaw(c, f)
	}
	go r.heartbeats(cctx, c)
	for {
		_, data, err := c.Read(cctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusPolicyViolation && strings.Contains(err.Error(), "revoked") {
				return errRevoked
			}
			return err
		}
		var f protocol.Frame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		if err := r.handle(ctx, c, f); err != nil {
			if errors.Is(err, errRevoked) {
				return err
			}
			r.log.Warn("command failed", "type", f.Type, "run", f.RunID, "err", err)
		}
	}
}

var bootTime = time.Now().UTC()

func (r *Runner) writeFrame(c *websocket.Conn, typ, runID string, epoch int64, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.sendRaw(c, protocol.Frame{Type: typ, ID: domain.NewID(), RunID: runID, LeaseEpoch: epoch, Payload: b})
}

func (r *Runner) sendRaw(c *websocket.Conn, f protocol.Frame) error {
	if c == nil {
		return errors.New("not connected")
	}
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b)
}

// send writes to the current connection, if any. Durable frames are also
// journaled by their callers and replayed on reconnect.
func (r *Runner) send(f protocol.Frame) error {
	r.mu.Lock()
	c := r.conn
	r.mu.Unlock()
	return r.sendRaw(c, f)
}

func (r *Runner) sendTyped(typ, runID string, epoch int64, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return r.send(protocol.Frame{Type: typ, ID: domain.NewID(), RunID: runID, LeaseEpoch: epoch, Payload: b})
}

func (r *Runner) heartbeats(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(time.Duration(r.heartbeat.Load()))
	defer t.Stop()
	for n := 1; ; n++ {
		r.sendHeartbeat(c)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n%3 == 0 {
			r.resendUnacked(c)
		}
	}
}

// resendUnacked re-sends finished attempts' events and terminal reports the
// hub has not committed (for example after a transient hub error), so a
// result is never stranded until the next reconnect.
func (r *Runner) resendUnacked(c *websocket.Conn) {
	jruns, err := r.journal.Runs()
	if err != nil {
		return
	}
	for _, jr := range jruns {
		if jr.Terminal == nil || jr.TermAcked {
			continue
		}
		r.mu.Lock()
		_, active := r.runs[jr.RunID]
		r.mu.Unlock()
		if active {
			continue // still finishing; its own terminal send is in flight
		}
		frames, _ := r.journal.Unacked(jr.RunID)
		for _, f := range frames {
			if err := r.sendRaw(c, f); err != nil {
				return
			}
		}
		t := *jr.Terminal
		t.LastSeq = r.journal.LastSeq(jr.RunID)
		if err := r.writeFrame(c, protocol.EvRunTerminal, jr.RunID, jr.Epoch, t); err != nil {
			return
		}
	}
}

// activeRuns snapshots the runs this runner currently holds.
func (r *Runner) activeRuns() []*activeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Collect(maps.Values(r.runs))
}

func (r *Runner) sendHeartbeat(c *websocket.Conn) {
	all := r.activeRuns()
	hb := protocol.Heartbeat{DiskFreeMB: diskFreeMB(r.paths.Dir), FreeSlots: r.opts.Slots - len(all)}
	for _, ar := range all {
		ar.mu.Lock()
		state := "accepted"
		if ar.started {
			state = "running"
		}
		hb.ActiveRuns = append(hb.ActiveRuns, protocol.ActiveRun{RunID: ar.m.RunID, LeaseEpoch: ar.epoch, State: state, LastActivity: ar.activity})
		ar.mu.Unlock()
	}
	_ = r.writeFrame(c, protocol.EvHeartbeat, "", 0, hb)
}

// leaseMonitor enforces leases: when a renewal is overdue, the runner stops
// admitting tool calls and terminates the provider before the safe deadline.
func (r *Runner) leaseMonitor(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		margin := time.Duration(r.stopMargin.Load())
		for _, ar := range r.activeRuns() {
			ar.mu.Lock()
			expired := now.After(ar.leaseUntil.Add(-margin))
			ar.mu.Unlock()
			if !expired || ar.leaseLost.Load() {
				continue
			}
			r.log.Warn("lease not renewed; stopping run", "run", ar.m.RunID)
			ar.leaseLost.Store(true)
			ar.admit.Store(false)
			if !r.dropUnstarted(ar, protocol.OutcomeLeaseLost, "the lease expired before this attempt started") {
				r.stopRun(ar)
			}
		}
	}
}

func (r *Runner) stopRun(ar *activeRun) {
	ar.mu.Lock()
	sess, cancel := ar.session, ar.cancel
	ar.mu.Unlock()
	if sess != nil {
		go func() {
			ctx, c := context.WithTimeout(context.Background(), 30*time.Second)
			defer c()
			_ = sess.Cancel(ctx)
		}()
	}
	if cancel != nil && sess == nil {
		cancel()
	}
}

// dropUnstarted removes an accepted attempt that never started, freeing its
// slot, and reports it as finished (nothing ran, so the exit is confirmed).
// It returns false when the attempt already started and must be stopped.
func (r *Runner) dropUnstarted(ar *activeRun, outcome, reason string) bool {
	return r.drop(ar, outcome, reason, true)
}

// drop is dropUnstarted; report=false skips the terminal report (used when a
// newer offer of the same attempt replaces this one).
func (r *Runner) drop(ar *activeRun, outcome, reason string, report bool) bool {
	ar.mu.Lock()
	if ar.started || ar.dropped {
		dropped := ar.dropped
		ar.mu.Unlock()
		return dropped
	}
	ar.dropped = true
	ar.mu.Unlock()
	r.mu.Lock()
	if r.runs[ar.m.RunID] == ar {
		delete(r.runs, ar.m.RunID)
	}
	r.mu.Unlock()
	close(ar.done)
	jr, ok := r.journal.Run(ar.m.RunID)
	if !report || !ok || jr.Epoch != ar.epoch {
		return true // a newer offer owns (or will own) the journal entry
	}
	t := protocol.RunTerminal{Outcome: outcome, ExitConfirmed: true, Error: reason, LastSeq: r.journal.LastSeq(ar.m.RunID)}
	if err := r.journal.SetTerminal(ar.m.RunID, t); err != nil {
		r.log.Error("journal terminal failed", "run", ar.m.RunID, "err", err)
	}
	_ = r.sendTyped(protocol.EvRunTerminal, ar.m.RunID, ar.epoch, t)
	r.log.Info("dropped an attempt that never started", "run", ar.m.RunID, "reason", reason)
	return true
}

func (r *Runner) stopAll() {
	r.shutdown.Store(true)
	all := r.activeRuns()
	for _, ar := range all {
		ar.admit.Store(false)
		if !r.dropUnstarted(ar, protocol.OutcomeFailed, "the runner stopped before this attempt started") {
			r.stopRun(ar)
		}
	}
	for _, ar := range all {
		select {
		case <-ar.done:
		case <-time.After(30 * time.Second):
		}
	}
}

// handle processes one hub command.
func (r *Runner) handle(ctx context.Context, c *websocket.Conn, f protocol.Frame) error {
	switch f.Type {
	case protocol.CmdWelcome:
		var w protocol.Welcome
		if json.Unmarshal(f.Payload, &w) == nil {
			if w.HeartbeatMs > 0 {
				r.heartbeat.Store(int64(time.Duration(w.HeartbeatMs) * time.Millisecond))
			}
			if w.StopMarginMs > 0 {
				r.stopMargin.Store(int64(time.Duration(w.StopMarginMs) * time.Millisecond))
			}
			r.mu.Lock()
			r.draining = w.Draining
			r.mu.Unlock()
		}
		return nil
	case protocol.CmdOfferRun:
		return r.onOffer(c, f)
	case protocol.CmdStartRun:
		return r.onStart(ctx, c, f)
	case protocol.CmdDeliverInput:
		return r.onInput(c, f)
	case protocol.CmdResolveApproval:
		var ra protocol.ResolveApproval
		if err := json.Unmarshal(f.Payload, &ra); err != nil {
			return err
		}
		r.mu.Lock()
		p := r.approvals[ra.RequestID]
		delete(r.approvals, ra.RequestID)
		r.mu.Unlock()
		if p != nil {
			p.ch <- ra
		}
		if !strings.HasPrefix(f.ID, "policy:") {
			return r.writeFrame(c, protocol.EvCommandAck, f.RunID, f.LeaseEpoch, protocol.CommandAck{CommandID: f.ID, OK: true})
		}
		return nil
	case protocol.CmdCancelRun:
		r.mu.Lock()
		ar := r.runs[f.RunID]
		r.mu.Unlock()
		if ar != nil && ar.epoch == f.LeaseEpoch {
			ar.cancelled.Store(true)
			ar.admit.Store(false)
			if !r.dropUnstarted(ar, protocol.OutcomeCancelled, "cancelled before it started") {
				r.stopRun(ar)
			}
		}
		return r.writeFrame(c, protocol.EvCommandAck, f.RunID, f.LeaseEpoch, protocol.CommandAck{CommandID: f.ID, OK: true})
	case protocol.CmdToolResult:
		var res protocol.ToolResult
		if err := json.Unmarshal(f.Payload, &res); err != nil {
			return err
		}
		r.mu.Lock()
		p := r.tools[res.CallID]
		delete(r.tools, res.CallID)
		r.mu.Unlock()
		if p != nil {
			p.ch <- res
		}
		return nil
	case protocol.CmdAck:
		var a protocol.Ack
		if err := json.Unmarshal(f.Payload, &a); err != nil {
			return err
		}
		if err := r.journal.AckEvents(a.RunID, a.UpToSeq); err != nil {
			return err
		}
		if a.Terminal {
			// Only an explicit terminal ack settles the report; an event ack
			// at the same sequence does not mean the outcome was committed.
			// The epoch guards against an ack for an older offer landing
			// after a newer one reused the journal entry.
			if jr, ok := r.journal.Run(a.RunID); ok && jr.Epoch == f.LeaseEpoch {
				_ = r.journal.AckTerminal(a.RunID)
			}
		}
		return nil
	case protocol.CmdLease, protocol.CmdReconcile:
		var leases []protocol.Lease
		if f.Type == protocol.CmdLease {
			var lr protocol.LeaseRenewal
			if err := json.Unmarshal(f.Payload, &lr); err != nil {
				return err
			}
			leases = lr.Leases
		} else {
			var rc protocol.Reconcile
			if err := json.Unmarshal(f.Payload, &rc); err != nil {
				return err
			}
			leases = rc.Runs
		}
		for _, l := range leases {
			r.mu.Lock()
			ar := r.runs[l.RunID]
			r.mu.Unlock()
			if ar == nil {
				if l.Revoked {
					// A journaled attempt the hub no longer recognises.
					if jr, ok := r.journal.Run(l.RunID); ok && jr.Terminal != nil {
						_ = r.journal.AckTerminal(l.RunID)
					}
				}
				continue
			}
			if l.Revoked || l.LeaseEpoch != ar.epoch {
				r.log.Warn("hub revoked lease; stopping run", "run", l.RunID, "reason", l.Reason)
				ar.leaseLost.Store(true)
				ar.admit.Store(false)
				if !r.dropUnstarted(ar, protocol.OutcomeLeaseLost, "the hub revoked this attempt before it started") {
					r.stopRun(ar)
				}
				continue
			}
			ar.mu.Lock()
			ar.leaseUntil = time.Now().Add(time.Duration(l.ExpiresInMs) * time.Millisecond)
			ar.mu.Unlock()
		}
		return nil
	case protocol.CmdDrain:
		var d struct {
			Draining bool `json:"draining"`
		}
		_ = json.Unmarshal(f.Payload, &d)
		r.mu.Lock()
		r.draining = d.Draining
		r.mu.Unlock()
		return r.writeFrame(c, protocol.EvCommandAck, "", 0, protocol.CommandAck{CommandID: f.ID, OK: true})
	case protocol.CmdRevoke:
		return errRevoked
	case protocol.CmdProbe:
		go r.probeOnce(ctx)
		return nil
	case protocol.CmdCleanup:
		go func() { _ = r.onCleanup(ctx, f) }()
		return nil
	}
	return fmt.Errorf("unknown command %q", f.Type)
}

func (r *Runner) onOffer(c *websocket.Conn, f protocol.Frame) error {
	if raw, ok := r.journal.CommandAck(f.ID); ok {
		var ack protocol.RunAck
		_ = json.Unmarshal(raw, &ack)
		ack.Duplicate = true
		return r.writeFrame(c, protocol.EvRunAck, f.RunID, f.LeaseEpoch, ack)
	}
	var offer protocol.OfferRun
	if err := json.Unmarshal(f.Payload, &offer); err != nil {
		return err
	}
	m := offer.Manifest
	reject := func(reason string) error {
		ack := protocol.RunAck{CommandID: f.ID, Accepted: false, Reason: reason, State: "rejected"}
		_ = r.journal.RecordCommand(f.ID, protocol.CmdOfferRun, f.RunID, ack)
		return r.writeFrame(c, protocol.EvRunAck, f.RunID, f.LeaseEpoch, ack)
	}
	r.mu.Lock()
	prev := r.runs[m.RunID]
	r.mu.Unlock()
	if prev != nil && prev.epoch < f.LeaseEpoch {
		// The hub re-offered this attempt under a newer epoch (the old offer
		// expired). An old copy that never started is simply replaced.
		r.drop(prev, protocol.OutcomeLeaseLost, "superseded by a newer offer", false)
	}
	r.mu.Lock()
	draining, busy := r.draining, len(r.runs) >= r.opts.Slots
	_, exists := r.runs[m.RunID]
	r.mu.Unlock()
	switch {
	case m.EngineerDraft && (m.Mode != protocol.ModeConversation || m.ExecutionProfile != "native" || m.Repo != nil || len(m.Tools) != 0 || m.TimeoutMs <= 0 || m.TimeoutMs > (2*time.Minute).Milliseconds()):
		return reject("invalid engineer draft execution scope")
	case m.EngineerDraft && !r.draftProviderAvailable(m.Provider, m.ProfileID):
		return reject("the selected provider does not support tool-free drafting")
	case exists:
		return reject("this attempt is already active here under another epoch")
	case draining || r.uncertain.Load():
		return reject("this machine is draining")
	case busy:
		return reject("no free slot on this machine")
	case r.opts.Adapters[m.Provider] == nil:
		return reject("provider " + m.Provider + " is not enabled on this runner")
	case m.ExecutionProfile == "container" && r.opts.ExecutionProfile == "native":
		return reject("this runner cannot provide container isolation")
	case m.ExecutionProfile == "native" && r.opts.ExecutionProfile != "native":
		return reject("this runner does not expose native host execution")
	}
	ack := protocol.RunAck{CommandID: f.ID, Accepted: true, State: "accepted"}
	if err := r.journal.AcceptRun(f.ID, m, f.LeaseEpoch, ack); err != nil {
		return err
	}
	ar := &activeRun{m: m, epoch: f.LeaseEpoch, leaseUntil: time.Now().Add(time.Duration(offer.LeaseMs) * time.Millisecond), done: make(chan struct{})}
	ar.admit.Store(true)
	r.mu.Lock()
	r.runs[m.RunID] = ar
	r.mu.Unlock()
	return r.writeFrame(c, protocol.EvRunAck, f.RunID, f.LeaseEpoch, ack)
}

func (r *Runner) draftProviderAvailable(provider, profile string) bool {
	caps, ok := r.caps.Load().(protocol.RunnerCapabilities)
	return ok && slices.ContainsFunc(caps.Providers, func(p protocol.ProviderInstallation) bool {
		return p.Provider == provider && p.ProfileID == profile && p.AuthState == protocol.AuthReady && p.Capabilities.EngineerDrafts
	})
}

func (r *Runner) onStart(ctx context.Context, c *websocket.Conn, f protocol.Frame) error {
	if _, ok := r.journal.CommandAck(f.ID); ok {
		return r.writeFrame(c, protocol.EvCommandAck, f.RunID, f.LeaseEpoch, protocol.CommandAck{CommandID: f.ID, OK: true})
	}
	r.mu.Lock()
	ar := r.runs[f.RunID]
	r.mu.Unlock()
	unknown := func() error {
		_ = r.journal.RecordCommand(f.ID, protocol.CmdStartRun, f.RunID, protocol.CommandAck{CommandID: f.ID, OK: false, Error: "unknown attempt"})
		return r.writeFrame(c, protocol.EvCommandAck, f.RunID, f.LeaseEpoch, protocol.CommandAck{CommandID: f.ID, OK: false, Error: "unknown attempt"})
	}
	if ar == nil || ar.epoch != f.LeaseEpoch {
		return unknown()
	}
	ar.mu.Lock()
	dropped, already := ar.dropped, ar.started
	if !dropped {
		ar.started = true
	}
	ar.mu.Unlock()
	if dropped {
		return unknown()
	}
	if err := r.journal.RecordCommand(f.ID, protocol.CmdStartRun, f.RunID, protocol.CommandAck{CommandID: f.ID, OK: true}); err != nil {
		return err
	}
	if !already {
		go r.execute(ctx, ar)
	}
	return r.writeFrame(c, protocol.EvCommandAck, f.RunID, f.LeaseEpoch, protocol.CommandAck{CommandID: f.ID, OK: true})
}

func (r *Runner) onInput(c *websocket.Conn, f protocol.Frame) error {
	var in protocol.DeliverInput
	if err := json.Unmarshal(f.Payload, &in); err != nil {
		return err
	}
	fresh, err := r.journal.AddInput(in.InputID, f.RunID, in.Text)
	if err != nil {
		return err
	}
	ack := func() error {
		return r.writeFrame(c, protocol.EvCommandAck, f.RunID, f.LeaseEpoch, protocol.CommandAck{CommandID: f.ID, OK: true})
	}
	if !fresh {
		return ack()
	}
	r.mu.Lock()
	ar := r.runs[f.RunID]
	r.mu.Unlock()
	if ar == nil {
		r.emitInputDelivered(f.RunID, f.LeaseEpoch, in.InputID, "unsupported", "The attempt has ended; the update is kept for the next attempt.")
		return ack()
	}
	ar.mu.Lock()
	sess := ar.session
	if sess == nil {
		ar.queued = append(ar.queued, in)
	}
	ar.mu.Unlock()
	if sess != nil {
		go r.deliver(ar, in)
	}
	return ack()
}

func (r *Runner) deliver(ar *activeRun, in protocol.DeliverInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mode, err := ar.session.SendInput(ctx, in.Text)
	detail := ""
	if errors.Is(err, providers.ErrUnsupported) {
		mode, detail = "unsupported", "This provider can't accept input mid-run; it will be included in the next attempt."
	} else if err != nil {
		mode, detail = "unsupported", err.Error()
	}
	r.journal.MarkInputDelivered(in.InputID)
	r.emitInputDelivered(ar.m.RunID, ar.epoch, in.InputID, mode, detail)
}

func (r *Runner) emitInputDelivered(runID string, epoch int64, inputID, mode, detail string) {
	data, _ := json.Marshal(protocol.InputDelivered{InputID: inputID, Mode: mode, Detail: detail})
	text := map[string]string{"immediate": "Received an update from the owner", "queued": "Queued the owner's update for the next step"}[mode]
	r.emit(runID, epoch, protocol.RunEvent{Kind: protocol.RunEvInputDelivered, Text: text, Data: data})
}

// emit journals a run event and sends it; it is replayed until acknowledged.
func (r *Runner) emit(runID string, epoch int64, e protocol.RunEvent) {
	e.Text = Redact(e.Text)
	if len(e.Data) > 0 {
		e.Data = []byte(Redact(string(e.Data)))
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	f, err := r.journal.AppendEvent(runID, func(seq int64) protocol.Frame {
		e.Seq = seq
		b, _ := json.Marshal(e)
		return protocol.Frame{Type: protocol.EvRunEvent, ID: fmt.Sprintf("%s:%d", runID, seq), RunID: runID, LeaseEpoch: epoch, Payload: b}
	})
	if err != nil {
		r.log.Error("journal event failed", "run", runID, "err", err)
		return
	}
	if e.Kind == protocol.RunEvStatus || e.Kind == protocol.RunEvToolStarted {
		r.journal.SetActivity(runID, e.Text)
	}
	_ = r.send(f)
}

// transient sends a non-durable update (coalesced streaming text).
func (r *Runner) transient(runID string, epoch int64, kind, text string) {
	text = Redact(text)
	b, _ := json.Marshal(protocol.RunEvent{Seq: 0, Kind: kind, Text: text, At: time.Now().UTC()})
	_ = r.send(protocol.Frame{Type: protocol.EvRunEvent, ID: domain.NewID(), RunID: runID, LeaseEpoch: epoch, Payload: b})
}
