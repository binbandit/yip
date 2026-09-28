package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/bridge"
	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/providers"
	"github.com/binbandit/yip/protocol"
)

// execute runs one attempt from workspace preparation to terminal report.
func (r *Runner) execute(parent context.Context, ar *activeRun) {
	defer close(ar.done)
	m := ar.m
	ctx, cancel := context.WithCancel(context.Background())
	if m.TimeoutMs > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(m.TimeoutMs)*time.Millisecond)
	}
	ar.mu.Lock()
	ar.cancel = cancel
	ar.mu.Unlock()
	defer cancel()

	finish := func(t protocol.RunTerminal) {
		switch {
		case ar.leaseLost.Load():
			t.Outcome = protocol.OutcomeLeaseLost
		case ar.cancelled.Load() && t.Outcome != protocol.OutcomeSucceeded:
			t.Outcome = protocol.OutcomeCancelled
		case r.shutdown.Load() && t.Outcome != protocol.OutcomeSucceeded:
			// Not a user cancellation: the runner itself stopped mid-attempt.
			t.Outcome = protocol.OutcomeFailed
			t.Error = "the runner on " + r.id.Name + " shut down during this attempt"
		}
		if ar.ws != nil && !ar.ws.ReadOnly && !ar.ws.Scratch {
			t.Checkpoint = r.checkpoint(context.Background(), ar)
		}
		t.LastSeq = r.journal.LastSeq(m.RunID)
		if err := r.journal.SetTerminal(m.RunID, t); err != nil {
			r.log.Error("journal terminal failed", "run", m.RunID, "err", err)
		}
		// Clean up and free the slot before reporting: the hub offers the
		// next run as soon as it hears, and a busy machine would decline it.
		r.ws.Release(context.Background(), ar.ws)
		_ = os.RemoveAll(r.scratchDir(m.RunID))
		r.mu.Lock()
		delete(r.runs, m.RunID)
		if ar.token != "" {
			delete(r.tokens, ar.token)
		}
		r.mu.Unlock()
		_ = r.sendTyped(protocol.EvRunTerminal, m.RunID, ar.epoch, t)
		r.log.Info("run finished", "run", m.RunID, "outcome", t.Outcome)
	}

	r.emit(m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvStatus, Text: "Preparing the workspace"})
	ws, err := r.ws.Prepare(ctx, m, r.fetchArtifact)
	if err != nil {
		finish(protocol.RunTerminal{Outcome: protocol.OutcomeFailed, Error: "workspace: " + err.Error(), ExitConfirmed: true})
		return
	}
	ar.mu.Lock()
	ar.ws = ws
	ar.mu.Unlock()
	_ = r.journal.SetState(m.RunID, "running", ws.Dir)
	startData, _ := json.Marshal(map[string]string{"branch": ws.Branch, "base": ws.Base, "workspace": ws.Dir})
	r.emit(m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvStarted, Text: "Started on " + r.id.Name, Data: startData})

	adapter := r.opts.Adapters[m.Provider]
	token := domain.RandomToken(32)
	r.mu.Lock()
	r.tokens[token] = m.RunID
	ar.token = token
	r.mu.Unlock()
	spec := providers.StartSpec{
		RunID: m.RunID, Workdir: ws.Dir, Mode: m.Mode, Model: m.Model, Instructions: m.Instructions, Prompt: m.Prompt,
		ResumeSessionID: m.ResumeSessionID, PermissionTool: bridge.PermissionPrompt, FakeScript: m.FakeScript,
		TrustProviderRules: m.TrustProviderRules,
		MCP: providers.MCPServer{Name: "yip", Command: r.opts.BridgeExe, Args: []string{"bridge", "--mode", m.Mode},
			Env: map[string]string{bridge.EnvSocket: r.paths.socketPath(), bridge.EnvToken: token}},
		// Left empty so each adapter builds its own allowlisted environment,
		// including the keys that select an existing sign-in (CODEX_HOME,
		// CLAUDE_CONFIG_DIR) — the same environment its sign-in probe used.
		Env: nil,
	}
	if m.TimeoutMs > 0 {
		spec.Timeout = time.Duration(m.TimeoutMs) * time.Millisecond
	}
	sess, err := adapter.Start(ctx, spec)
	if err != nil {
		outcome := protocol.OutcomeFailed
		if errors.Is(err, providers.ErrUnsupported) {
			outcome = protocol.OutcomeRejected
		}
		finish(protocol.RunTerminal{Outcome: outcome, Error: adapter.Label() + ": " + err.Error(), ExitConfirmed: true})
		return
	}
	ar.mu.Lock()
	ar.session = sess
	queued := ar.queued
	ar.queued = nil
	ar.mu.Unlock()
	if ar.leaseLost.Load() || ar.cancelled.Load() {
		r.stopRun(ar)
	}
	for _, in := range queued {
		go r.deliver(ar, in)
	}

	var final strings.Builder
	var delta strings.Builder
	lastFlush := time.Now()
	flush := func(force bool) {
		if delta.Len() == 0 || (!force && time.Since(lastFlush) < 300*time.Millisecond) {
			return
		}
		// Transient chunks are incremental: clients append them and replace
		// the preview when the complete message arrives.
		r.transient(m.RunID, ar.epoch, protocol.RunEvMessageDelta, delta.String())
		delta.Reset()
		lastFlush = time.Now()
	}
	for ev := range sess.Events() {
		switch ev.Kind {
		case providers.EventMessageDelta:
			delta.WriteString(ev.Text)
			flush(false)
		case providers.EventMessage:
			flush(true)
			delta.Reset()
			if strings.TrimSpace(ev.Text) != "" {
				if final.Len() > 0 {
					final.WriteString("\n\n")
				}
				final.WriteString(ev.Text)
				r.emit(m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvMessage, Text: truncate(ev.Text, 20000)})
			}
		case providers.EventApprovalRequest:
			if ev.Approval != nil {
				go r.adapterApproval(ar, sess, *ev.Approval)
			}
		case providers.EventQuestion:
			if ev.Question != nil {
				// No synchronous human answer exists: genuine questions go to the
				// conversation through yip's human_ask tool.
				q := *ev.Question
				go func() {
					actx, c := context.WithTimeout(context.Background(), 30*time.Second)
					defer c()
					_ = sess.AnswerQuestion(actx, q.ID, providers.QuestionAnswer{Declined: true, Selected: -1,
						Text: "No one can answer synchronously. Decide routine matters yourself; for a genuine missing fact, use the yip human_ask tool to ask in the conversation and continue independent work."})
				}()
				r.emit(m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvWarning, Text: "Declined a blocking provider question; directed it to human_ask"})
			}
		case providers.EventVendorSession:
			id := firstNonEmpty(ev.VendorSessionID, ev.Text)
			r.emit(m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvVendorSession, Text: id})
		case providers.EventUsage:
			if ev.Usage != nil {
				data, _ := json.Marshal(ev.Usage)
				r.emit(m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvUsage, Data: data})
			}
		default:
			text := ev.Text
			if ev.Tool != "" && text == "" {
				text = ev.Tool
			}
			ar.mu.Lock()
			if ev.Kind == providers.EventStatus || ev.Kind == providers.EventToolStarted {
				ar.activity = text
			}
			ar.mu.Unlock()
			r.emit(m.RunID, ar.epoch, protocol.RunEvent{Kind: ev.Kind, Text: truncate(text, 2000), Tool: ev.Tool, Data: ev.Data})
		}
	}
	flush(true)
	res := sess.Wait()
	// The final text and error leave the machine like any event: redacted.
	t := protocol.RunTerminal{Outcome: res.Outcome, FinalText: Redact(firstNonEmpty(strings.TrimSpace(res.FinalText), strings.TrimSpace(final.String()))),
		Error: Redact(res.Error), VendorSessionID: res.VendorSessionID, Usage: res.Usage, ExitConfirmed: res.ExitConfirmed}
	if res.RetryAfter > 0 {
		t.RetryAfterMs = res.RetryAfter.Milliseconds()
	}
	if t.Outcome == "" {
		t.Outcome = protocol.OutcomeFailed
	}
	finish(t)
}

// adapterApproval forwards a provider permission request to the hub and
// answers the provider with the decision.
func (r *Runner) adapterApproval(ar *activeRun, sess providers.Session, req providers.ApprovalRequest) {
	d := r.requestApproval(ar, req.ID, req.Action, req.Raw)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := sess.ResolveApproval(ctx, req.ID, providers.ApprovalDecision{Allow: d.Decision == "allow", Reason: d.Reason}); err != nil {
		r.emit(ar.m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvWarning, Text: "Could not deliver a permission decision: " + err.Error()})
	}
}

// requestApproval asks the hub's policy (and, for exceptional actions, the
// owner) and blocks until decided or the run stops.
func (r *Runner) requestApproval(ar *activeRun, requestID string, action protocol.ApprovalAction, raw json.RawMessage) protocol.ResolveApproval {
	if !ar.admit.Load() {
		return protocol.ResolveApproval{RequestID: requestID, Decision: "deny", Reason: "the run is stopping"}
	}
	payload, _ := json.Marshal(protocol.ApprovalRequest{RequestID: requestID, Action: action, Raw: raw})
	f := protocol.Frame{Type: protocol.EvApprovalRequest, ID: domain.NewID(), RunID: ar.m.RunID, LeaseEpoch: ar.epoch, Payload: payload}
	p := &pendingApproval{runID: ar.m.RunID, frame: f, ch: make(chan protocol.ResolveApproval, 1)}
	r.mu.Lock()
	r.approvals[requestID] = p
	r.mu.Unlock()
	r.emit(ar.m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvStatus, Text: "Checking permission: " + truncate(action.Summary, 120)})
	_ = r.send(f)
	select {
	case d := <-p.ch:
		return d
	case <-ar.done:
		return protocol.ResolveApproval{RequestID: requestID, Decision: "deny", Reason: "the run ended"}
	case <-waitStopped(ar):
		r.mu.Lock()
		delete(r.approvals, requestID)
		r.mu.Unlock()
		return protocol.ResolveApproval{RequestID: requestID, Decision: "deny", Reason: "the run is stopping"}
	}
}

func waitStopped(ar *activeRun) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ar.done:
				return
			case <-t.C:
				if !ar.admit.Load() {
					close(ch)
					return
				}
			}
		}
	}()
	return ch
}

// checkpoint records uncommitted work at the end of an editing attempt so it
// can be recovered, without moving the branch.
func (r *Runner) checkpoint(ctx context.Context, ar *activeRun) *protocol.CheckpointInfo {
	commit, untracked, err := ar.ws.Checkpoint(ctx, ar.m.RunID)
	if err != nil || commit == "" {
		return nil
	}
	head, dirty, _, _ := ar.ws.Head(ctx)
	cp := &protocol.CheckpointInfo{Head: commit, Dirty: dirty || commit != head, Untracked: untracked}
	if cp.Dirty {
		if path, err := ar.ws.Bundle(ctx, ar.ws.Base, commit); err == nil {
			defer os.Remove(path)
			if a, err := r.uploadAndRecord(ctx, ar, path, "checkpoint", "checkpoint "+shortRev(commit)+".bundle", "application/octet-stream", commit); err == nil {
				cp.ArtifactID, cp.Hash = a.ID, a.Hash
			}
		}
	}
	data, _ := json.Marshal(cp)
	r.emit(ar.m.RunID, ar.epoch, protocol.RunEvent{Kind: protocol.RunEvCheckpoint, Text: "Checkpointed uncommitted work", Data: data})
	return cp
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func shortRev(r string) string {
	if len(r) > 8 {
		return r[:8]
	}
	return r
}
