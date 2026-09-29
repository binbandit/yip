package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/redact"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

func jsonMarshal(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	return json.RawMessage(b), err
}

// ---- runner connections ----

// NodeConn is one live runner connection.
type NodeConn struct {
	NodeID      string
	Serial      string
	Send        func(protocol.Frame) error
	CloseFn     func(reason string)
	ConnectedAt time.Time
	sendMu      sync.Mutex
}

func (c *NodeConn) send(f protocol.Frame) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.Send(f)
}

type nodeRegistry struct {
	mu    sync.Mutex
	conns map[string]*NodeConn
}

func newNodeRegistry() *nodeRegistry { return &nodeRegistry{conns: map[string]*NodeConn{}} }

func (r *nodeRegistry) put(c *NodeConn) *NodeConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.conns[c.NodeID]
	r.conns[c.NodeID] = c
	return old
}

func (r *nodeRegistry) get(id string) *NodeConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conns[id]
}

func (r *nodeRegistry) remove(c *NodeConn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conns[c.NodeID] == c {
		delete(r.conns, c.NodeID)
		return true
	}
	return false
}

func (r *nodeRegistry) ids() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.conns))
	for id := range r.conns {
		out = append(out, id)
	}
	return out
}

func (r *nodeRegistry) closeAll(reason string) {
	r.mu.Lock()
	conns := r.conns
	r.conns = map[string]*NodeConn{}
	r.mu.Unlock()
	for _, c := range conns {
		if c.CloseFn != nil {
			c.CloseFn(reason)
		}
	}
}

// Connected reports whether a node has a live connection.
func (h *Hub) Connected(nodeID string) bool { return h.nodes.get(nodeID) != nil }

// ---- outbox ----

// queueCommand persists a runner command in the outbox within the caller's
// transaction; it is delivered only after commit and redelivered until
// acknowledged. Runners deduplicate by command ID.
func (h *Hub) queueCommand(ctx context.Context, t *txn, nodeID, runID string, epoch int64, typ, commandID string, payload any) error {
	if nodeID == "" {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if epoch > 0 && !strings.HasPrefix(commandID, "input:") {
		commandID = fmt.Sprintf("%s:%d", commandID, epoch)
	}
	frame, _ := json.Marshal(protocol.Frame{Type: typ, ID: commandID, RunID: runID, LeaseEpoch: epoch, Payload: body})
	if err := store.InsertOutbox(ctx, t.tx, store.OutboxItem{ID: domain.NewID(), Kind: "runner_command", NodeID: nodeID, RunID: runID,
		CommandID: commandID, Frame: string(frame), NextAttemptAt: h.now()}); err != nil {
		return err
	}
	t.afterCommit(func() { go h.flushOutbox(context.Background(), nodeID) })
	return nil
}

var flushMu sync.Map // nodeID → *sync.Mutex

func (h *Hub) flushOutbox(ctx context.Context, nodeID string) {
	c := h.nodes.get(nodeID)
	if c == nil {
		return
	}
	muAny, _ := flushMu.LoadOrStore(nodeID, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	items, err := store.PendingOutbox(ctx, h.st.R(), nodeID, h.now(), 200)
	if err != nil {
		h.log.Warn("outbox read failed", "err", err)
		return
	}
	for _, it := range items {
		var f protocol.Frame
		if err := json.Unmarshal([]byte(it.Frame), &f); err != nil {
			continue
		}
		if err := c.send(f); err != nil {
			return // connection gone; redelivered on reconnect
		}
		_ = h.st.Tx(ctx, func(tx *sqlTx) error { return store.MarkOutboxSent(ctx, tx, it.ID, h.now().Add(15*time.Second)) })
	}
}

// sendDirect sends a non-durable frame (tool results, lease renewals).
func (h *Hub) sendDirect(nodeID string, f protocol.Frame) {
	if c := h.nodes.get(nodeID); c != nil {
		_ = c.send(f)
	}
}

// ---- runner frames ----

// ConnectRunner registers a verified runner connection after hello.
func (h *Hub) ConnectRunner(ctx context.Context, conn *NodeConn, hello protocol.Hello) error {
	node, err := store.GetNode(ctx, h.st.R(), conn.NodeID)
	if err != nil {
		return domain.Forbidden("Unknown machine.")
	}
	if node.RevokedAt != nil {
		return domain.Forbidden("This machine's credential was revoked. Pair it again to reconnect.")
	}
	if node.CertSerial != conn.Serial {
		return domain.Forbidden("This machine presented a superseded certificate.")
	}
	if hello.ProtocolVersion != protocol.RunnerProtocolVersion {
		return domain.Unavailable("runner_protocol", "The runner speaks protocol %d but this hub speaks %d. Upgrade the runner and hub together.",
			hello.ProtocolVersion, protocol.RunnerProtocolVersion)
	}
	if old := h.nodes.put(conn); old != nil && old.CloseFn != nil {
		old.CloseFn("replaced by a newer connection")
	}
	defer func() {
		if err != nil {
			// Don't leave a registered connection the caller is about to close.
			h.nodes.remove(conn)
		}
	}()
	err = h.do(ctx, func(t *txn) error {
		if err := store.SetNodeSeen(ctx, t.tx, conn.NodeID, protocol.NodeOnline, h.now()); err != nil {
			return err
		}
		if _, err := t.tx.ExecContext(ctx, `UPDATE nodes SET hostname = ?, os = ?, arch = ?, runner_version = ? WHERE id = ?`,
			hello.Hostname, hello.OS, hello.Arch, hello.RunnerVersion, conn.NodeID); err != nil {
			return err
		}
		if err := h.reconcileJournal(ctx, t, conn.NodeID, hello.Runs); err != nil {
			return err
		}
		return h.emitNode(ctx, t, conn.NodeID)
	})
	if err != nil {
		return err
	}
	welcome, _ := json.Marshal(protocol.Welcome{NodeID: conn.NodeID, HeartbeatMs: h.lim.HeartbeatInterval.Milliseconds(),
		LeaseMs: h.lim.LeaseDuration.Milliseconds(), StopMarginMs: h.lim.StopMargin.Milliseconds(),
		ProtocolVersion: protocol.RunnerProtocolVersion, Draining: node.Draining})
	_ = conn.send(protocol.Frame{Type: protocol.CmdWelcome, ID: domain.NewID(), Payload: welcome})
	go h.flushOutbox(context.Background(), conn.NodeID)
	h.Kick()
	return nil
}

// DropRunnerConnection closes a runner's live connection (operator action and
// partition testing). The runner reconnects on its own; its leases continue
// only if it does so before they expire.
func (h *Hub) DropRunnerConnection(nodeID string) bool {
	c := h.nodes.get(nodeID)
	if c == nil {
		return false
	}
	if c.CloseFn != nil {
		c.CloseFn("connection dropped by the hub")
	}
	h.DisconnectRunner(context.Background(), c)
	return true
}

// DisconnectRunner records a dropped connection. Leases are not revoked
// immediately: a runner that reconnects within its lease continues.
func (h *Hub) DisconnectRunner(ctx context.Context, conn *NodeConn) {
	if !h.nodes.remove(conn) {
		return
	}
	_ = h.do(ctx, func(t *txn) error {
		if err := store.SetNodeStatus(ctx, t.tx, conn.NodeID, protocol.NodeOffline); err != nil {
			return err
		}
		return h.emitNode(ctx, t, conn.NodeID)
	})
}

func (h *Hub) emitNode(ctx context.Context, t *txn, nodeID string) error {
	n, err := store.GetNode(ctx, t.tx, nodeID)
	if err != nil {
		return err
	}
	return t.emit(ev{Type: "node.updated", Actor: protocol.Actor{Kind: protocol.ActorNode, ID: nodeID}, Payload: n.Node})
}

// RunnerFrame handles one frame from a connected runner.
func (h *Hub) RunnerFrame(ctx context.Context, conn *NodeConn, f protocol.Frame) {
	var err error
	switch f.Type {
	case protocol.EvCapabilities:
		var c protocol.RunnerCapabilities
		if err = json.Unmarshal(f.Payload, &c); err == nil {
			err = h.onCapabilities(ctx, conn.NodeID, c)
		}
	case protocol.EvHeartbeat:
		var hb protocol.Heartbeat
		if err = json.Unmarshal(f.Payload, &hb); err == nil {
			err = h.onHeartbeat(ctx, conn, hb)
		}
	case protocol.EvRunAck:
		var a protocol.RunAck
		if err = json.Unmarshal(f.Payload, &a); err == nil {
			err = h.onRunAck(ctx, conn.NodeID, f, a)
		}
	case protocol.EvCommandAck:
		var a protocol.CommandAck
		if err = json.Unmarshal(f.Payload, &a); err == nil {
			err = h.onCommandAck(ctx, conn.NodeID, f, a)
		}
	case protocol.EvRunEvent:
		var e protocol.RunEvent
		if err = json.Unmarshal(f.Payload, &e); err == nil {
			err = h.onRunEvent(ctx, conn, f, e)
		}
	case protocol.EvRunTerminal:
		var term protocol.RunTerminal
		if err = json.Unmarshal(f.Payload, &term); err == nil {
			err = h.onRunTerminal(ctx, conn.NodeID, f.RunID, f.LeaseEpoch, term, true)
			// Acknowledge a committed report, and deliberately discard one
			// for a run the hub doesn't know or that belongs elsewhere, so
			// the runner stops re-sending it. Other errors are transient:
			// the runner re-sends until acknowledged.
			if code := domain.AsError(err).Code; err == nil || code == "not_found" || code == "forbidden" {
				if err != nil {
					h.log.Warn("discarding a terminal report", "node", conn.NodeID, "run", f.RunID, "err", err)
					err = nil
				}
				ack, _ := json.Marshal(protocol.Ack{RunID: f.RunID, UpToSeq: term.LastSeq, Terminal: true})
				_ = conn.send(protocol.Frame{Type: protocol.CmdAck, ID: f.ID, RunID: f.RunID, LeaseEpoch: f.LeaseEpoch, Payload: ack})
			}
		}
	case protocol.EvToolCall:
		var call protocol.ToolCall
		if err = json.Unmarshal(f.Payload, &call); err == nil {
			nodeID := conn.NodeID
			go func() {
				res := h.HandleToolCall(context.Background(), nodeID, f.RunID, f.LeaseEpoch, call)
				b, _ := json.Marshal(res)
				// The runner may have reconnected while the call ran; deliver
				// to whichever connection is current. A result that still
				// misses is recovered when the runner re-sends the call.
				h.sendDirect(nodeID, protocol.Frame{Type: protocol.CmdToolResult, ID: call.CallID, RunID: f.RunID, LeaseEpoch: f.LeaseEpoch, Payload: b})
			}()
		}
	case protocol.EvApprovalRequest:
		var req protocol.ApprovalRequest
		if err = json.Unmarshal(f.Payload, &req); err == nil {
			err = h.onApprovalRequest(ctx, conn.NodeID, f.RunID, f.LeaseEpoch, req)
		}
	default:
		err = fmt.Errorf("unknown frame type %q", f.Type)
	}
	if err != nil {
		h.log.Warn("runner frame rejected", "node", conn.NodeID, "type", f.Type, "run", f.RunID, "err", err)
	}
}

func (h *Hub) onCapabilities(ctx context.Context, nodeID string, c protocol.RunnerCapabilities) error {
	for i := range c.Providers {
		if c.Providers[i].ProfileID == "" {
			c.Providers[i].ProfileID = c.Providers[i].Provider + ":default"
		}
	}
	return h.do(ctx, func(t *txn) error {
		if err := store.SetNodeCapabilities(ctx, t.tx, nodeID, c, "", ""); err != nil {
			return err
		}
		ready := map[string]bool{}
		for _, p := range c.Providers {
			if err := h.ensureProfile(ctx, t, p); err != nil {
				return err
			}
			if p.AuthState == protocol.AuthReady {
				ready[p.Provider] = true
			}
		}
		if err := h.resumeSignInWaits(ctx, t, nodeID, ready); err != nil {
			return err
		}
		t.kickAfter()
		n, err := store.GetNode(ctx, t.tx, nodeID)
		if err != nil {
			return err
		}
		// A report with no enabled providers is still a completed report.
		// Keep the receipt on the event, separate from durable machine state.
		return t.emit(ev{Type: "node.updated", Actor: protocol.Actor{Kind: protocol.ActorNode, ID: nodeID}, Payload: struct {
			protocol.Node
			CapabilitiesReported bool `json:"capabilitiesReported"`
		}{Node: n.Node, CapabilitiesReported: true}})
	})
}

// resumeSignInWaits continues work that stopped because a provider needed
// sign-in, once a machine reports that provider signed in again.
func (h *Hub) resumeSignInWaits(ctx context.Context, t *txn, nodeID string, ready map[string]bool) error {
	if len(ready) == 0 {
		return nil
	}
	jobs, err := store.ListJobs(ctx, t.tx, store.JobFilter{States: []protocol.JobState{protocol.JobWaiting}, IncludeReply: true})
	if err != nil {
		return err
	}
	nodeName := h.nodeName(ctx, t.tx, nodeID)
	for _, j := range jobs {
		if j.WaitingReason != protocol.WaitProviderSignIn {
			continue
		}
		if _, err := store.ActiveRunForJob(ctx, t.tx, j.ID); err == nil {
			continue // a queued attempt is picked up by the scheduler
		}
		runs, err := store.ListJobRuns(ctx, t.tx, j.ID)
		if err != nil || len(runs) == 0 || !ready[runs[len(runs)-1].Provider] {
			continue
		}
		if _, err := h.enqueueRun(ctx, t, j, runReason{Purpose: "continue", Cause: j.ID,
			Note: ProviderLabel(runs[len(runs)-1].Provider) + " is signed in again on " + nodeName + "; continue where you left off."}); err != nil {
			h.log.Warn("could not resume work after sign-in", "job", j.ID, "err", err)
		}
	}
	return nil
}

// ensureProfile records an account pool for a provider profile. Fake and
// unknown profiles default to one concurrent run, as the spec requires for
// shared accounts, except the fake provider which has no real allowance.
func (h *Hub) ensureProfile(ctx context.Context, t *txn, p protocol.ProviderInstallation) error {
	var n int
	if err := t.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_profiles WHERE id = ?`, p.ProfileID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	max := 1
	if p.Provider == "fake" {
		max = h.lim.ActiveRunsPerOrg
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO provider_profiles(id, org_id, provider, label, billing, max_concurrency, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ProfileID, h.Org().ID, p.Provider, firstNonEmpty(p.Account, ProviderLabel(p.Provider)), p.Billing, max, store.TS(h.now()))
	return err
}

func (h *Hub) onHeartbeat(ctx context.Context, conn *NodeConn, hb protocol.Heartbeat) error {
	var renewal protocol.LeaseRenewal
	err := h.do(ctx, func(t *txn) error {
		now := h.now()
		if err := store.SetNodeSeen(ctx, t.tx, conn.NodeID, protocol.NodeOnline, now); err != nil {
			return err
		}
		if hb.DiskFreeMB > 0 {
			_ = store.SetNodeDiskFree(ctx, t.tx, conn.NodeID, hb.DiskFreeMB)
		}
		for _, ar := range hb.ActiveRuns {
			run, err := store.GetRun(ctx, t.tx, ar.RunID)
			lease := protocol.Lease{RunID: ar.RunID, LeaseEpoch: ar.LeaseEpoch}
			switch {
			case err != nil:
				lease.Revoked, lease.Reason = true, "unknown run"
			case run.NodeID != conn.NodeID || run.LeaseEpoch != ar.LeaseEpoch:
				lease.Revoked, lease.Reason = true, "stale lease epoch"
			case !domain.RunHoldsLease(run.State):
				lease.Revoked, lease.Reason = true, "run is "+string(run.State)
			default:
				ok, err := store.RenewLease(ctx, t.tx, ar.RunID, ar.LeaseEpoch, now.Add(h.lim.LeaseDuration), now)
				if err != nil {
					return err
				}
				if !ok {
					lease.Revoked, lease.Reason = true, "lease not renewable"
				} else {
					lease.ExpiresInMs = h.lim.LeaseDuration.Milliseconds()
				}
			}
			renewal.Leases = append(renewal.Leases, lease)
		}
		return nil
	})
	if err != nil {
		return err
	}
	b, _ := json.Marshal(renewal)
	return conn.send(protocol.Frame{Type: protocol.CmdLease, ID: domain.NewID(), Payload: b})
}

// requireLease loads a run and verifies the frame's node and epoch. Stale
// epochs are rejected, including late results from a reassigned attempt.
func requireLease(ctx context.Context, q store.Q, nodeID, runID string, epoch int64) (store.RunRow, error) {
	run, err := store.GetRun(ctx, q, runID)
	if err != nil {
		return run, domain.NotFound("unknown run")
	}
	if run.NodeID != nodeID {
		return run, domain.Forbidden("run is not assigned to this machine")
	}
	if run.LeaseEpoch != epoch {
		return run, domain.Conflict("stale lease epoch %d (current %d)", epoch, run.LeaseEpoch)
	}
	return run, nil
}

// quarantineIfStale keeps output that a machine sent for its own run under
// an old lease epoch (spec §7: stale output is quarantined diagnostic
// evidence). It never touches the work; redelivery is recorded once.
func (h *Hub) quarantineIfStale(ctx context.Context, nodeID, runID string, epoch int64, kind, ref, summary string) {
	run, err := store.GetRun(ctx, h.st.R(), runID)
	if err != nil || run.NodeID != nodeID || epoch == 0 || run.LeaseEpoch == epoch {
		return
	}
	o := protocol.QuarantinedOutput{ID: domain.NewID(), RunID: run.ID, NodeID: nodeID, Epoch: epoch, CurrentEpoch: run.LeaseEpoch,
		Kind: kind, Summary: truncate(redact.String(summary), 500), ReceivedAt: h.now()}
	key := fmt.Sprintf("%s:%s:%d:%s", kind, run.ID, epoch, ref)
	_ = h.do(ctx, func(t *txn) error {
		if err := store.Quarantine(ctx, t.tx, o, run.JobID, key); err != nil {
			return err
		}
		return t.emit(ev{Type: "run.stale_report", Room: run.Destination.RoomID, Job: run.JobID, Run: run.ID,
			Payload: map[string]any{"epoch": epoch, "currentEpoch": run.LeaseEpoch, "kind": kind, "summary": o.Summary}})
	})
}

func (h *Hub) onRunAck(ctx context.Context, nodeID string, f protocol.Frame, a protocol.RunAck) error {
	return h.do(ctx, func(t *txn) error {
		if err := store.AckOutbox(ctx, t.tx, a.CommandID); err != nil {
			return err
		}
		run, err := store.GetRun(ctx, t.tx, f.RunID)
		if err != nil {
			return domain.NotFound("unknown run")
		}
		stale := run.NodeID != nodeID || run.LeaseEpoch != f.LeaseEpoch || !(run.State == protocol.RunOffered || domain.RunHoldsLease(run.State))
		if stale {
			// Cancelled, reset, or reassigned while the offer was in flight: the
			// runner must not keep a slot for it.
			if a.Accepted {
				return h.queueCommand(ctx, t, nodeID, run.ID, f.LeaseEpoch, protocol.CmdCancelRun, "drop:"+run.ID,
					protocol.CancelRun{Reason: "this attempt is no longer current", GraceMs: 0})
			}
			return nil
		}
		if run.State != protocol.RunOffered {
			return nil // duplicate ack
		}
		if !a.Accepted {
			h.mu.Lock()
			h.excluded[run.ID+"|"+nodeID] = h.now().Add(5 * time.Minute)
			h.mu.Unlock()
			if err := store.ResetOffer(ctx, t.tx, run.ID); err != nil {
				return err
			}
			if _, err := t.jobChanged(ctx, run.JobID); err != nil {
				return err
			}
			t.kickAfter()
			if err := t.audit(protocol.Actor{Kind: protocol.ActorNode, ID: nodeID}, "runner", "run.offer_rejected", run.ID, "rejected", a.Reason); err != nil {
				return err
			}
			return h.runChanged(ctx, t, run.ID)
		}
		now := h.now()
		if err := store.SetRunState(ctx, t.tx, run.ID, protocol.RunPreparing, ""); err != nil {
			return err
		}
		if _, err := store.RenewLease(ctx, t.tx, run.ID, run.LeaseEpoch, now.Add(h.lim.LeaseDuration), now); err != nil {
			return err
		}
		if err := h.queueCommand(ctx, t, nodeID, run.ID, run.LeaseEpoch, protocol.CmdStartRun, "start:"+run.ID, map[string]string{"runId": run.ID}); err != nil {
			return err
		}
		return h.runChanged(ctx, t, run.ID)
	})
}

func (h *Hub) onCommandAck(ctx context.Context, nodeID string, f protocol.Frame, a protocol.CommandAck) error {
	if w, ok := h.ackWaiters.LoadAndDelete(nodeID + "|" + a.CommandID); ok {
		select {
		case w.(chan protocol.CommandAck) <- a:
		default:
		}
		return nil
	}
	return h.do(ctx, func(t *txn) error {
		if err := store.AckOutbox(ctx, t.tx, a.CommandID); err != nil {
			return err
		}
		// Consumption of an exact-action approval is recorded when the
		// runner confirms it delivered the decision.
		if strings.HasPrefix(a.CommandID, "approval:") {
			parts := strings.Split(a.CommandID, ":")
			if len(parts) >= 2 {
				if ap, err := store.GetApproval(ctx, t.tx, parts[1]); err == nil && ap.Status == "approved" {
					if _, err := store.SetApprovalStatus(ctx, t.tx, ap.ID, "approved", "consumed"); err != nil {
						return err
					}
					ap, _ = store.GetApproval(ctx, t.tx, ap.ID)
					return t.emit(ev{Type: "approval.updated", Room: ap.Source.RoomID, Job: ap.JobID, Payload: ap.Approval})
				}
			}
		}
		return nil
	})
}

func (h *Hub) runChanged(ctx context.Context, t *txn, runID string) error {
	run, err := store.GetRun(ctx, t.tx, runID)
	if err != nil {
		return err
	}
	return t.emit(ev{Type: "run.updated", Room: run.Destination.RoomID, Thread: run.Destination.ThreadID, Job: run.JobID, Run: run.ID,
		Root: run.RootRequestID, Payload: run.Run})
}

// onRunEvent persists a runner event. Seq 0 marks a transient update (for
// example coalesced streaming text) that is fanned out but never stored.
func (h *Hub) onRunEvent(ctx context.Context, conn *NodeConn, f protocol.Frame, e protocol.RunEvent) error {
	if e.Seq == 0 {
		run, err := requireLease(ctx, h.st.R(), conn.NodeID, f.RunID, f.LeaseEpoch)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"kind": e.Kind, "text": e.Text, "at": e.At})
		h.bus.Publish(transient("run.stream", run, payload))
		return nil
	}
	err := h.do(ctx, func(t *txn) error {
		run, err := requireLease(ctx, t.tx, conn.NodeID, f.RunID, f.LeaseEpoch)
		if err != nil {
			return err
		}
		fresh, err := store.InsertRunEvent(ctx, t.tx, run.ID, e)
		if err != nil || !fresh {
			return err // duplicate delivery is harmless
		}
		return h.applyRunEvent(ctx, t, run, e)
	})
	if err != nil {
		h.quarantineIfStale(ctx, conn.NodeID, f.RunID, f.LeaseEpoch, "event", strconv.FormatInt(e.Seq, 10), firstNonEmpty(e.Kind+": "+e.Text, e.Kind))
		return err
	}
	ack, _ := json.Marshal(protocol.Ack{RunID: f.RunID, UpToSeq: e.Seq})
	return conn.send(protocol.Frame{Type: protocol.CmdAck, ID: f.ID, RunID: f.RunID, Payload: ack})
}

func (h *Hub) applyRunEvent(ctx context.Context, t *txn, run store.RunRow, e protocol.RunEvent) error {
	at := e.At
	if at.IsZero() {
		at = h.now()
	}
	activity := func(text string) error {
		if text == "" {
			return nil
		}
		text = truncate(text, 160)
		if err := store.SetRunActivity(ctx, t.tx, run.ID, text, at); err != nil {
			return err
		}
		if err := store.SetJobActivity(ctx, t.tx, run.JobID, text, at); err != nil {
			return err
		}
		if err := store.SetNodeActivity(ctx, t.tx, run.NodeID, text); err != nil {
			return err
		}
		if _, err := t.jobChanged(ctx, run.JobID); err != nil {
			return err
		}
		return h.runChanged(ctx, t, run.ID)
	}
	switch e.Kind {
	case protocol.RunEvStarted:
		if run.State == protocol.RunPreparing || run.State == protocol.RunOffered {
			if err := store.SetRunState(ctx, t.tx, run.ID, protocol.RunRunning, ""); err != nil {
				return err
			}
			if err := store.ConsumeStartedInputs(ctx, t.tx, run.ID); err != nil {
				return err
			}
			var d struct {
				Branch string `json:"branch"`
				Base   string `json:"base"`
			}
			_ = json.Unmarshal(e.Data, &d)
			if d.Base != "" {
				_ = store.SetJobBase(ctx, t.tx, run.JobID, d.Branch, d.Base)
				_ = store.SetRunRevision(ctx, t.tx, run.ID, d.Branch, d.Base, "")
			}
			job, err := store.GetJob(ctx, t.tx, run.JobID)
			if err != nil {
				return err
			}
			if job.State == protocol.JobQueued {
				if _, err := h.setJobState(ctx, t, job.ID, protocol.JobRunning, "", ""); err != nil {
					return err
				}
			}
		}
		return activity(firstNonEmpty(e.Text, "Started on "+h.nodeName(ctx, t.tx, run.NodeID)))
	case protocol.RunEvStatus, protocol.RunEvToolStarted:
		return activity(e.Text)
	case protocol.RunEvToolFinished, protocol.RunEvMessage:
		return h.runChanged(ctx, t, run.ID)
	case protocol.RunEvVendorSession:
		return store.SetRunVendorSession(ctx, t.tx, run.ID, e.Text)
	case protocol.RunEvUsage:
		var u protocol.Usage
		if json.Unmarshal(e.Data, &u) == nil {
			if u.Billing == "" {
				u.Billing = protocol.BillingUnknown
			}
			return store.InsertUsage(ctx, t.tx, domain.NewID(), run.ID, run.Provider, u)
		}
	case protocol.RunEvInputDelivered:
		var d protocol.InputDelivered
		if json.Unmarshal(e.Data, &d) == nil {
			mode := d.Mode
			if mode == "unsupported" {
				mode = "queued" // held for the next attempt; never claimed as delivered
			}
			if err := store.SetInputDelivery(ctx, t.tx, d.InputID, run.ID, mode); err != nil {
				return err
			}
			if mode == "immediate" {
				_ = store.ConsumeInputs(ctx, t.tx, []string{d.InputID})
			}
			in, err := store.GetJobInput(ctx, t.tx, d.InputID)
			if err == nil {
				return t.emit(ev{Type: "input.updated", Room: run.Destination.RoomID, Job: run.JobID, Run: run.ID, Payload: in})
			}
		}
	case protocol.RunEvWarning, protocol.RunEvError:
		return activity(e.Text)
	case protocol.RunEvCheckpoint:
		var c protocol.CheckpointInfo
		if json.Unmarshal(e.Data, &c) == nil && c.Head != "" {
			return store.InsertCheckpoint(ctx, t.tx, domain.NewID(), run.ID, run.JobID, run.NodeID, c)
		}
	}
	return nil
}

func transient(typ string, run store.RunRow, payload json.RawMessage) eventsTransient {
	return eventsTransient{Type: typ, RoomID: run.Destination.RoomID, ThreadID: run.Destination.ThreadID, JobID: run.JobID,
		RunID: run.ID, EngineerID: run.EngineerID, Payload: payload}
}

// onRunTerminal applies a run's final outcome. fromRunner is false when the
// hub itself declares the outcome (lease expiry).
func (h *Hub) onRunTerminal(ctx context.Context, nodeID, runID string, epoch int64, term protocol.RunTerminal, fromRunner bool) error {
	staleTerminal := false
	defer func() {
		if staleTerminal {
			h.quarantineIfStale(ctx, nodeID, runID, epoch, "terminal", "", term.Outcome+": "+firstNonEmpty(term.Summary, term.Error, term.FinalText))
		}
	}()
	return h.do(ctx, func(t *txn) error {
		run, err := store.GetRun(ctx, t.tx, runID)
		if err != nil {
			return domain.NotFound("unknown run")
		}
		if fromRunner {
			if run.NodeID != nodeID {
				return domain.Forbidden("run is not assigned to this machine")
			}
			if run.LeaseEpoch != epoch {
				// A stale epoch cannot publish results; it is kept as
				// quarantined diagnostic evidence only (after this returns).
				staleTerminal = true
				return nil
			}
		}
		if domain.RunTerminal(run.State) {
			return nil // duplicate terminal report
		}
		return h.applyTerminal(ctx, t, run, term)
	})
}

func (h *Hub) applyTerminal(ctx context.Context, t *txn, run store.RunRow, term protocol.RunTerminal) error {
	state := protocol.RunFailed
	reason := term.Outcome
	switch term.Outcome {
	case protocol.OutcomeSucceeded:
		state = protocol.RunSucceeded
	case protocol.OutcomeCancelled:
		state = protocol.RunCancelled
		if !term.ExitConfirmed {
			state = protocol.RunUnknown
			reason = "stop requested but termination was not confirmed"
		}
	case protocol.OutcomeUnknown:
		state = protocol.RunUnknown
	case protocol.OutcomeLeaseLost:
		if !term.ExitConfirmed {
			state = protocol.RunUnknown
		}
		reason = "stopped after losing its lease"
	}
	if term.Error != "" {
		reason = firstNonEmpty(reason, "") + ": " + truncate(term.Error, 300)
	}
	if !domain.CanTransitionRun(run.State, state) {
		state = protocol.RunUnknown
	}
	if err := store.SetRunState(ctx, t.tx, run.ID, state, reason); err != nil {
		return err
	}
	if err := store.SetRunResult(ctx, t.tx, run.ID, term.FinalText, "", term.Usage); err != nil {
		return err
	}
	if state == protocol.RunFailed {
		// A failed attempt's session (possibly one that failed to resume) is
		// not resumed again: the next attempt starts a fresh one.
		_ = store.InvalidateProviderSessionFor(ctx, t.tx, run.EngineerID, run.Provider, contextKey(run), run.NodeID, "the last attempt failed")
	} else if term.VendorSessionID != "" {
		_ = store.SetRunVendorSession(ctx, t.tx, run.ID, term.VendorSessionID)
		var fp string
		_ = t.tx.QueryRowContext(ctx, `SELECT scope_fingerprint FROM runs WHERE id = ?`, run.ID).Scan(&fp)
		_ = store.UpsertProviderSession(ctx, t.tx, store.ProviderSession{ID: domain.NewID(), EngineerID: run.EngineerID, Provider: run.Provider,
			ProfileID: run.ProfileID, ScopeFingerprint: fp, ContextKey: contextKey(run), NodeID: run.NodeID, VendorSessionID: term.VendorSessionID})
	}
	if term.Checkpoint != nil && term.Checkpoint.Head != "" {
		_ = store.InsertCheckpoint(ctx, t.tx, domain.NewID(), run.ID, run.JobID, run.NodeID, *term.Checkpoint)
	}
	if term.Usage != nil {
		_ = store.InsertUsage(ctx, t.tx, domain.NewID(), run.ID, run.Provider, *term.Usage)
	}
	if err := h.runChanged(ctx, t, run.ID); err != nil {
		return err
	}
	t.kickAfter()
	run, _ = store.GetRun(ctx, t.tx, run.ID)
	// The attempt's outcome is recorded no matter what; applying it to the
	// job happens in a savepoint so a job-level failure can't roll it back
	// (which would otherwise wedge the engineer's slot and the machine).
	if _, err := t.tx.ExecContext(ctx, `SAVEPOINT after_run`); err != nil {
		return err
	}
	if err := h.afterRun(ctx, t, run, term); err != nil {
		h.log.Error("could not apply a run outcome to its job", "run", run.ID, "job", run.JobID, "err", err)
		if _, rerr := t.tx.ExecContext(ctx, `ROLLBACK TO after_run`); rerr != nil {
			return rerr
		}
	}
	_, err := t.tx.ExecContext(ctx, `RELEASE after_run`)
	return err
}

// contextKey identifies the conversation or job a vendor session belongs to.
func contextKey(r store.RunRow) string {
	var p struct {
		Purpose string `json:"purpose"`
	}
	_ = json.Unmarshal(r.Manifest, &p)
	return "job:" + r.JobID
}

// humanReason turns a raw failure into one readable clause for the room.
// A structured tool error contributes its message; anything long or
// technical is left to the job's details.
func humanReason(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i >= 0 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(s[i:]), &e) == nil && strings.TrimSpace(e.Message) != "" {
			s = strings.TrimSpace(e.Message)
		} else {
			s = strings.TrimSpace(strings.TrimRight(s[:i], ": "))
		}
	}
	s = strings.TrimRight(s, ". ")
	if s == "" || len(s) > 160 || strings.ContainsAny(s, "{}[]\n") {
		return "it stopped before finishing (the details are in the work)"
	}
	return s
}

// afterRun decides what the job does once an attempt ends. It runs inside a
// savepoint (see applyTerminal): a failure here never prevents the attempt's
// own outcome from being recorded.
func (h *Hub) afterRun(ctx context.Context, t *txn, run store.RunRow, term protocol.RunTerminal) error {
	job, err := store.GetJob(ctx, t.tx, run.JobID)
	if err != nil {
		return err
	}
	// Completed, cancelled, or failed work is not revived by a late attempt.
	if !domain.JobLive(job.State) {
		return nil
	}
	nodeName := h.nodeName(ctx, t.tx, run.NodeID)
	engName := h.engineerName(ctx, t.tx, run.EngineerID)
	eng := protocol.Actor{Kind: protocol.ActorEngineer, ID: run.EngineerID}
	// The room gets one readable line, as a colleague would say it; the job
	// keeps the full technical detail for its drawer.
	status := func(body string) error {
		_, err := t.postMessage(newMessage{Room: job.Source.RoomID, Thread: job.Source.ThreadID, Author: systemActor,
			Kind: protocol.MessageStatus, Body: body, Refs: []protocol.Ref{{Kind: "job", ID: job.ID}}, JobID: job.ID, RunID: run.ID})
		return err
	}
	switch run.State {
	case protocol.RunUnknown:
		last := firstNonEmpty(run.LastActivity, "starting")
		at := ""
		if run.LastActivityAt != nil {
			at = " at " + run.LastActivityAt.Format("15:04")
		}
		detail := fmt.Sprintf("Last heard from %s%s. Last confirmed: %s. The run's outcome is not yet confirmed.", nodeName, at, strings.ToLower(last[:1])+last[1:])
		if _, err := h.setJobState(ctx, t, job.ID, protocol.JobWaiting, protocol.WaitRecovery, detail); err != nil {
			return err
		}
		return status(fmt.Sprintf("%s went quiet on %s%s while working on %s. yip can't confirm how it ended yet.", engName, nodeName, at, job.Title))
	case protocol.RunCancelled:
		// An explicit interrupt-and-restart starts the next attempt now.
		if pw, err := store.TakePendingWake(ctx, t.tx, job.ID); err != nil {
			return err
		} else if pw != nil && pw.Purpose == "restart" {
			_, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "restart", Cause: pw.Cause, Note: pw.Note})
			return err
		} else if pw != nil {
			return store.SetPendingWake(ctx, t.tx, job.ID, pw) // not ours: keep it for whoever resumes the work
		}
		return nil
	}
	// Completion already requested and waiting on review: a later attempt
	// ending badly doesn't undo that; the review still decides.
	if job.State == protocol.JobReviewReady && term.Outcome != protocol.OutcomeSucceeded {
		return nil
	}
	switch term.Outcome {
	case protocol.OutcomeRateLimited:
		at := h.now().Add(retryDelay)
		detail := "This account's allowance is exhausted. Work is saved. yip will check again at " + at.Format("15:04") + "."
		if term.RetryAfterMs > 0 {
			at = h.now().Add(time.Duration(term.RetryAfterMs) * time.Millisecond)
			detail = "This account's allowance is exhausted. Work is saved. The provider reports it resets at " + at.Format("15:04") + "."
		}
		if err := store.SetJobRetry(ctx, t.tx, job.ID, &at, job.AutoRetries); err != nil {
			return err
		}
		// The whole account shares the allowance: pause it, so other queued
		// work waits for the reset instead of hitting the same limit.
		if run.ProfileID != "" {
			if _, err := t.tx.ExecContext(ctx, `UPDATE provider_profiles SET paused_until = ? WHERE id = ? AND (paused_until IS NULL OR paused_until < ?)`,
				store.TS(at), run.ProfileID, store.TS(at)); err != nil {
				return err
			}
			if run.NodeID != "" {
				_ = h.emitNode(ctx, t, run.NodeID) // machine views re-read the account's state
			}
		}
		if _, err := h.setJobState(ctx, t, job.ID, protocol.JobWaiting, protocol.WaitProviderLimit, detail); err != nil {
			return err
		}
		return status(fmt.Sprintf("%s paused %s: this account's allowance ran out. Work is saved, and it picks up again at %s.", engName, job.Title, at.Format("15:04")))
	case protocol.OutcomeAuthRequired:
		_, _ = t.tx.ExecContext(ctx, `UPDATE provider_installations SET auth_state = 'needs_signin', auth_detail = ? WHERE node_id = ? AND provider = ?`,
			truncate(term.Error, 200), run.NodeID, run.Provider)
		_ = h.emitNode(ctx, t, run.NodeID)
		detail := ProviderLabel(run.Provider) + " needs sign-in on " + nodeName + ". Other eligible work continues."
		if _, err := h.setJobState(ctx, t, job.ID, protocol.JobWaiting, protocol.WaitProviderSignIn, detail); err != nil {
			return err
		}
		return status(fmt.Sprintf("%s is waiting: %s needs signing in again on %s. Other work carries on.", engName, ProviderLabel(run.Provider), nodeName))
	case protocol.OutcomeFailed, protocol.OutcomeRejected, protocol.OutcomeLeaseLost:
		reason := firstNonEmpty(term.Error, run.TerminalReason, "unknown error")
		// A provider crash is retried automatically, a bounded number of
		// times with backoff and jitter, only when the attempt did nothing
		// outside the workspace (no push, publish, merge, or network write
		// was allowed); otherwise an explicit retry decides.
		if term.Outcome == protocol.OutcomeFailed && job.AutoRetries < h.lim.MaxAutoRetries && !h.hadExternalEffects(ctx, t.tx, run.ID) {
			at := h.now().Add(retryBackoff(job.AutoRetries))
			if err := store.SetJobRetry(ctx, t.tx, job.ID, &at, job.AutoRetries+1); err != nil {
				return err
			}
			detail := fmt.Sprintf("%s's run on %s stopped unexpectedly (%s). Nothing outside the workspace was changed, so yip tries again at %s (attempt %d of %d).",
				engName, nodeName, truncate(humanReason(reason), 160), at.Format("15:04"), job.AutoRetries+2, h.lim.MaxAutoRetries+1)
			if _, err := h.setJobState(ctx, t, job.ID, protocol.JobWaiting, protocol.WaitRecovery, detail); err != nil {
				return err
			}
			return nil
		}
		detail := fmt.Sprintf("%s's run on %s ended without finishing: %s. The workspace and partial transcript are kept.",
			engName, nodeName, truncate(reason, 200))
		if _, err := h.setJobState(ctx, t, job.ID, protocol.JobFailed, "", detail); err != nil {
			return err
		}
		return status(fmt.Sprintf("%s couldn't finish %s: %s.", engName, job.Title, humanReason(reason)))
	}

	// Succeeded.
	if job.Kind == protocol.JobKindReply {
		text := strings.TrimSpace(term.FinalText)
		// A reply is only posted if the engineer still belongs to the conversation.
		if !run.PostedReply && text != "" && h.checkRunAccess(ctx, t.tx, run) == nil {
			if _, err := t.postMessage(newMessage{Room: run.Destination.RoomID, Thread: run.Destination.ThreadID, Author: eng, Body: text,
				ReplyTo: replyTarget(run), RunID: run.ID, JobID: job.ID, Cause: run.CauseID, Root: run.RootRequestID,
				Mentions: h.resolveTextMentions(ctx, t.tx, run.Destination.RoomID, text)}); err != nil {
				return err
			}
		}
		return h.finishJob(ctx, t, job)
	}
	// A trigger that arrived during this attempt (e.g. review feedback) runs next.
	if pw, err := store.TakePendingWake(ctx, t.tx, job.ID); err != nil {
		return err
	} else if pw != nil {
		_, err := h.enqueueRun(ctx, t, job, runReason{Purpose: pw.Purpose, Cause: pw.Cause, Note: pw.Note, Automatic: pw.Automatic})
		if isLimit(err) {
			return nil
		}
		return err
	}
	if job.State == protocol.JobReviewReady {
		return nil
	}
	// Before parking, re-check: an answer or a dependency may have arrived
	// after this attempt was planned, so it isn't in the attempt's context.
	since := contextBuiltAt(run)
	answeredDuring := h.answeredSince(ctx, t.tx, job.ID, since)
	resolvedDuring := h.resolvedSince(ctx, t.tx, job.ID, since)
	openQuestions, _ := h.openQuestionsFor(ctx, t.tx, job.ID)
	openDeps, _ := store.OpenDependencies(ctx, t.tx, job.ID)
	resume := func(purpose string, automatic bool) error {
		_, err := h.enqueueRun(ctx, t, job, runReason{Purpose: purpose, Cause: run.ID, Automatic: automatic})
		if isLimit(err) {
			return nil
		}
		return err
	}
	if wait, detail, _ := store.GetRunIntent(ctx, t.tx, run.ID); wait != "" {
		switch {
		case wait == protocol.WaitMissingInfo && openQuestions == 0 && answeredDuring:
			return resume("answer", true)
		case wait == protocol.WaitDependency && openDeps == 0 && resolvedDuring:
			return resume("dependency_resolved", true)
		case wait == "review" && job.RequiresPeerReview:
			if outstanding, _ := h.outstandingReview(ctx, t.tx, job); !outstanding {
				break // nothing to wait for; fall through to the checks below
			}
			_, err := h.setJobState(ctx, t, job.ID, protocol.JobReviewReady, "", firstNonEmpty(detail, "Waiting for peer review"))
			return err
		default:
			if wait == "review" {
				wait = protocol.WaitDependency
			}
			_, err := h.setJobState(ctx, t, job.ID, protocol.JobWaiting, wait, detail)
			return err
		}
	}
	if openQuestions > 0 {
		_, err := h.setJobState(ctx, t, job.ID, protocol.JobWaiting, protocol.WaitMissingInfo, "Waiting for an answer in the conversation")
		return err
	}
	if answeredDuring {
		return resume("answer", true)
	}
	if outstanding, who := h.outstandingReview(ctx, t.tx, job); outstanding {
		_, err := h.setJobState(ctx, t, job.ID, protocol.JobReviewReady, "", who+" is reviewing")
		return err
	}
	if openDeps > 0 {
		_, err := h.setJobState(ctx, t, job.ID, protocol.JobWaiting, protocol.WaitDependency, "Waiting for a colleague's help")
		return err
	}
	if resolvedDuring {
		return resume("dependency_resolved", true)
	}
	// The attempt ended without completing or saying why.
	missing, _, _ := h.completionMissing(ctx, t.tx, job)
	detail := "Stopped without completing."
	if len(missing) > 0 {
		detail += " Missing: " + strings.Join(missing, "; ") + "."
	}
	if job.AutoRetries < h.lim.MaxAutoRetries {
		if err := store.SetJobRetry(ctx, t.tx, job.ID, nil, job.AutoRetries+1); err != nil {
			return err
		}
		if _, err := h.setJobState(ctx, t, job.ID, protocol.JobQueued, "", ""); err != nil {
			return err
		}
		job, _ = store.GetJob(ctx, t.tx, job.ID)
		if _, err := h.enqueueRun(ctx, t, job, runReason{Purpose: "continue", Cause: run.ID, Automatic: true, Note: detail}); err != nil && !isLimit(err) {
			return err
		}
		return nil
	}
	_, err = h.setJobState(ctx, t, job.ID, protocol.JobWaiting, protocol.WaitStalled, detail)
	return err
}

// contextBuiltAt is when an attempt's manifest was built, at its offer. An
// answer or outcome from then on is not in the context the attempt was
// planned from, even when it reaches the provider mid-run: one that lands
// while the machine is still preparing the workspace is handed over only as
// the session starts, after the attempt was planned without it.
func contextBuiltAt(run store.RunRow) time.Time {
	var m struct {
		Now time.Time `json:"now"`
	}
	if json.Unmarshal(run.Manifest, &m) == nil && !m.Now.IsZero() {
		return m.Now
	}
	return run.CreatedAt
}

// answeredSince reports whether one of the job's questions was answered after t0.
func (h *Hub) answeredSince(ctx context.Context, q store.Q, jobID string, t0 time.Time) bool {
	var n int
	_ = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM questions WHERE job_id = ? AND status = 'answered' AND answered_at >= ?`, jobID, store.TS(t0)).Scan(&n)
	return n > 0
}

// resolvedSince reports whether one of the job's dependencies resolved after t0.
func (h *Hub) resolvedSince(ctx context.Context, q store.Q, jobID string, t0 time.Time) bool {
	var n int
	_ = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_dependencies WHERE job_id = ? AND resolved_at >= ?`, jobID, store.TS(t0)).Scan(&n)
	return n > 0
}

func replyTarget(run store.RunRow) string {
	if run.Destination.ThreadID == "" {
		return run.Destination.MessageID
	}
	return ""
}

func (h *Hub) openQuestionsFor(ctx context.Context, q store.Q, jobID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM questions WHERE job_id = ? AND status = 'open'`, jobID).Scan(&n)
	return n, err
}

func (h *Hub) outstandingReview(ctx context.Context, q store.Q, job store.JobRow) (bool, string) {
	reviews, _ := store.ListJobReviews(ctx, q, job.ID)
	for _, r := range reviews {
		if len(r.Rounds) > 0 && domain.ReviewOpen(r.Rounds[len(r.Rounds)-1].State) {
			return true, h.engineerName(ctx, q, r.ReviewerID)
		}
	}
	return false, ""
}

// ---- lease supervision ----

// expireLeases turns lost runs into truthful uncertainty. An offer that was
// never acknowledged never started, so it safely returns to the queue.
func (h *Hub) expireLeases(ctx context.Context) {
	runs, err := store.RunsInStates(ctx, h.st.R(), protocol.RunOffered, protocol.RunPreparing, protocol.RunRunning,
		protocol.RunAwaitingInput, protocol.RunStopping)
	if err != nil {
		return
	}
	now := h.now()
	for _, r := range runs {
		if r.LeaseExpiresAt == nil || now.Before(*r.LeaseExpiresAt) {
			continue
		}
		_ = h.do(ctx, func(t *txn) error {
			cur, err := store.GetRun(ctx, t.tx, r.ID)
			if err != nil || cur.LeaseEpoch != r.LeaseEpoch || cur.State != r.State {
				return err
			}
			// The snapshot may be stale (e.g. a heartbeat renewed it meanwhile).
			if cur.LeaseExpiresAt == nil || h.now().Before(*cur.LeaseExpiresAt) {
				return nil
			}
			if cur.State == protocol.RunOffered {
				if err := store.ResetOffer(ctx, t.tx, cur.ID); err != nil {
					return err
				}
				if err := store.CancelOutboxForRun(ctx, t.tx, cur.ID, "offer:"); err != nil {
					return err
				}
				if err := h.queueCommand(ctx, t, cur.NodeID, cur.ID, cur.LeaseEpoch, protocol.CmdCancelRun, "drop:"+cur.ID,
					protocol.CancelRun{Reason: "the offer expired before it was acknowledged", GraceMs: 0}); err != nil {
					return err
				}
				t.kickAfter()
				return h.runChanged(ctx, t, cur.ID)
			}
			return h.applyTerminal(ctx, t, cur, protocol.RunTerminal{Outcome: protocol.OutcomeUnknown,
				Error: "lease expired: no heartbeat from " + h.nodeName(ctx, t.tx, cur.NodeID)})
		})
	}
}

// reconcileJournal compares a reconnecting runner's journal with hub state
// before any new work is launched there.
func (h *Hub) reconcileJournal(ctx context.Context, t *txn, nodeID string, runs []protocol.JournalRunState) error {
	var reconcile protocol.Reconcile
	for _, jr := range runs {
		run, err := store.GetRun(ctx, t.tx, jr.RunID)
		if err != nil || run.NodeID != nodeID || run.LeaseEpoch != jr.LeaseEpoch {
			reconcile.Runs = append(reconcile.Runs, protocol.Lease{RunID: jr.RunID, LeaseEpoch: jr.LeaseEpoch, Revoked: true, Reason: "not the current attempt"})
			continue
		}
		if jr.Terminal != nil && (run.State == protocol.RunUnknown || domain.RunHoldsLease(run.State)) {
			if _, err := t.tx.ExecContext(ctx, `SAVEPOINT reconcile_run`); err != nil {
				return err
			}
			if err := h.applyTerminal(ctx, t, run, *jr.Terminal); err != nil {
				// One bad record must not keep the machine from reconnecting,
				// nor leave a half-applied outcome behind.
				h.log.Error("reconcile: could not apply a journaled outcome", "run", run.ID, "err", err)
				if _, err := t.tx.ExecContext(ctx, `ROLLBACK TO reconcile_run`); err != nil {
					return err
				}
			}
			if _, err := t.tx.ExecContext(ctx, `RELEASE reconcile_run`); err != nil {
				return err
			}
			continue
		}
		if run.State == protocol.RunUnknown {
			reconcile.Runs = append(reconcile.Runs, protocol.Lease{RunID: jr.RunID, LeaseEpoch: jr.LeaseEpoch, Revoked: true,
				Reason: "lease expired while disconnected; stop and report"})
			continue
		}
		if domain.RunHoldsLease(run.State) {
			now := h.now()
			_, _ = store.RenewLease(ctx, t.tx, run.ID, run.LeaseEpoch, now.Add(h.lim.LeaseDuration), now)
			reconcile.Runs = append(reconcile.Runs, protocol.Lease{RunID: run.ID, LeaseEpoch: run.LeaseEpoch, ExpiresInMs: h.lim.LeaseDuration.Milliseconds()})
		}
	}
	if len(reconcile.Runs) > 0 {
		b, _ := json.Marshal(reconcile)
		t.afterCommit(func() {
			h.sendDirect(nodeID, protocol.Frame{Type: protocol.CmdReconcile, ID: domain.NewID(), Payload: b})
		})
	}
	return nil
}

// recoverOnStart gives connected-before-restart runners a full lease to
// reconnect: hub downtime is not evidence that a runner failed.
func (h *Hub) recoverOnStart(ctx context.Context) {
	_ = h.do(ctx, func(t *txn) error {
		if _, err := t.tx.ExecContext(ctx, `UPDATE nodes SET status = 'offline' WHERE status IN ('online','suspect')`); err != nil {
			return err
		}
		_, err := t.tx.ExecContext(ctx, `UPDATE runs SET lease_expires_at = ? WHERE state IN ('offered','preparing','running','awaiting_input','stopping')`,
			store.TS(h.now().Add(h.lim.LeaseDuration)))
		return err
	})
}

var errStale = errors.New("stale")

// retryBackoff is the wait before automatic retry n (0-based): 30s, then
// 2m, with up to 25% jitter so retries from many jobs don't align.
func retryBackoff(n int) time.Duration {
	base := 30 * time.Second
	for i := 0; i < n; i++ {
		base *= 4
	}
	return base + time.Duration(rand.Int64N(int64(base/4)+1))
}

// hadExternalEffects reports whether an attempt was allowed to act outside
// its workspace: a push, publish, merge, or network action, by policy or by
// an owner's approval. Such an attempt is never retried automatically.
func (h *Hub) hadExternalEffects(ctx context.Context, q store.Q, runID string) bool {
	rows, err := q.QueryContext(ctx, `SELECT action FROM approvals WHERE run_id = ? AND status IN ('approved', 'consumed')
		UNION ALL SELECT json_extract(payload, '$.action') FROM events WHERE run_id = ? AND type = 'permission.auto'
		AND json_extract(payload, '$.decision') = 'allow'`, runID, runID)
	if err != nil {
		return true // Uncertain authority is not safe to replay.
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var action protocol.ApprovalAction
		if err := rows.Scan(&raw); err != nil || json.Unmarshal([]byte(raw), &action) != nil {
			return true
		}
		switch action.Kind {
		case "mcp", "read", "edit":
		case "exec":
			// Shell actions retain their original kind in the permission log.
			// Any exceptional class can act outside the workspace, including
			// commands whose effects cannot be inspected statically.
			if len(classifyCommand(action.Command)) > 0 {
				return true
			}
		default:
			return true
		}
	}
	return rows.Err() != nil
}
