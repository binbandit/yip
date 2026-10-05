package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

const draftInstructions = `Draft the configuration of an AI engineer from the owner's description. Return only one JSON object with exactly these fields: role (1–120 characters), description (1–1000 characters), capabilityTags (1–12 nonempty strings, each at most 40 characters), instructions (1–8000 characters). All four fields are required. Describe responsibilities, useful capabilities and concrete standing instructions. Do not invent a biography, credentials or experience. Do not include a name, handle, provider, permissions or any other fields. Do not use tools. The owner will review and edit the result before explicitly creating the engineer.`

// CreateEngineerDraft admits one bounded attempt on the exact selected machine
// and account. It shares runner slots and account limits with ordinary work.
func (h *Hub) CreateEngineerDraft(ctx context.Context, userID string, req protocol.CreateEngineerDraftRequest) (protocol.EngineerDraft, error) {
	var out protocol.EngineerDraft
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" || utf8.RuneCountInString(req.Description) > 4000 {
		return out, domain.Invalid("Describe the engineer in 1–4000 characters.")
	}
	if err := validateProvider(req.Provider); err != nil {
		return out, err
	}
	if req.NodeID == "" || req.Provider.ProfileID == "" || len(req.Provider.Alternatives) != 0 {
		return out, domain.Invalid("Choose one machine and configured provider account for this draft.")
	}
	err := h.do(ctx, func(t *txn) error {
		if err := h.requireWorkspaceOwner(ctx, t.tx, userID); err != nil {
			return err
		}
		n, err := store.GetNode(ctx, t.tx, req.NodeID)
		if err != nil || n.RevokedAt != nil || n.Status != protocol.NodeOnline || !h.Connected(req.NodeID) || n.Draining || n.Capacity.DiskPressure {
			return domain.Unavailable("machine_unavailable", "The selected machine is unavailable for new work. Choose an online machine and retry.")
		}
		if !slices.ContainsFunc(n.Profiles, func(p protocol.ExecutionProfile) bool { return p.Name == "native" && p.Available && p.EngineerDrafts }) {
			return domain.Invalid("This runner’s execution profile does not support engineer drafting.")
		}
		var inst *protocol.ProviderInstallation
		for i := range n.Providers {
			p := &n.Providers[i]
			if p.Provider == req.Provider.Provider && p.ProfileID == req.Provider.ProfileID {
				inst = p
				break
			}
		}
		if inst == nil || inst.AuthState != protocol.AuthReady {
			return domain.Invalid("The selected provider is not configured on this machine. Check Connections and retry.")
		}
		if !inst.Capabilities.ReadOnly || !inst.Capabilities.EngineerDrafts {
			return domain.Invalid("The selected provider cannot enforce tool-free drafting on this machine.")
		}
		if inst.Billing == protocol.BillingAPI && !req.Provider.AllowAPIBilling {
			return domain.Invalid("This provider uses API billing. Explicitly allow it before drafting.")
		}
		if req.Provider.Model != "" && !slices.ContainsFunc(inst.Models, func(m protocol.Model) bool { return m.ID == req.Provider.Model }) {
			return domain.Invalid("Choose a model reported by the selected provider.")
		}
		active, err := store.ActiveEngineerDrafts(ctx, t.tx)
		if err != nil {
			return err
		}
		for _, d := range active {
			if d.UserID == userID {
				return domain.Conflict("Another draft is still running or stopping. Wait for it to finish before retrying.")
			}
		}
		counts, err := h.countsWith(ctx, t.tx)
		if err != nil {
			return err
		}
		if counts.org >= h.lim.ActiveRunsPerOrg || counts.node[n.ID] >= max(1, n.Capacity.Slots) || counts.profile[inst.ProfileID] >= h.profileMax(ctx, inst.ProfileID) {
			return domain.Unavailable("capacity", "The selected machine or provider account is busy. Retry when a slot is available.")
		}
		if h.now().Before(h.profilePausedUntil(ctx, inst.ProfileID)) {
			return domain.Unavailable("provider_limit", "This provider account is paused after reaching its allowance. Retry after its pause ends.")
		}
		timeout := min(2*time.Minute, h.lim.RunTimeout)
		if timeout <= 0 {
			timeout = 2 * time.Minute
		}
		now := h.now()
		d := store.EngineerDraftRow{EngineerDraft: protocol.EngineerDraft{ID: domain.NewID(), NodeID: n.ID, Provider: req.Provider,
			State: protocol.RunOffered, Detail: "Starting on " + n.Name, CreatedAt: now}, UserID: userID, Request: req,
			LeaseExpiresAt: now.Add(h.lim.OfferAckTimeout), DeadlineAt: now.Add(timeout)}
		if err := store.InsertEngineerDraft(ctx, t.tx, d); err != nil {
			return err
		}
		m := protocol.ExecutionManifest{RunID: d.ID, Attempt: 1, Provider: req.Provider.Provider, ProfileID: req.Provider.ProfileID, Model: req.Provider.Model,
			Mode: protocol.ModeConversation, ExecutionProfile: "native", EngineerDraft: true, Instructions: draftInstructions, Prompt: req.Description,
			Tools: []string{}, Checks: []string{}, TimeoutMs: timeout.Milliseconds()}
		if err := h.queueCommand(ctx, t, n.ID, d.ID, 1, protocol.CmdOfferRun, "offer:"+d.ID, protocol.OfferRun{Manifest: m,
			LeaseMs: h.lim.LeaseDuration.Milliseconds(), StopMarginMs: h.lim.StopMargin.Milliseconds(), DeadlineAt: d.LeaseExpiresAt}); err != nil {
			return err
		}
		out = d.EngineerDraft
		if err := h.emitNode(ctx, t, n.ID); err != nil {
			return err
		}
		return t.audit(userActor(userID), "owner", "engineer.draft", d.ID, "started", n.ID+"/"+req.Provider.Provider)
	})
	return out, err
}

func (h *Hub) GetEngineerDraft(ctx context.Context, userID, id string) (protocol.EngineerDraft, error) {
	d, err := store.GetEngineerDraft(ctx, h.st.R(), id)
	if errors.Is(err, store.ErrNotFound) || err == nil && d.UserID != userID {
		return protocol.EngineerDraft{}, domain.NotFound("That draft doesn't exist.")
	}
	return d.EngineerDraft, err
}

func (h *Hub) CancelEngineerDraft(ctx context.Context, userID, id string) (protocol.EngineerDraft, error) {
	var out protocol.EngineerDraft
	err := h.do(ctx, func(t *txn) error {
		d, err := store.GetEngineerDraft(ctx, t.tx, id)
		if errors.Is(err, store.ErrNotFound) || err == nil && d.UserID != userID {
			return domain.NotFound("That draft doesn't exist.")
		}
		if err != nil {
			return err
		}
		if domain.RunHoldsLease(d.State) {
			if err := h.stopEngineerDraft(ctx, t, &d, "Draft cancelled. Your editable fields are unchanged."); err != nil {
				return err
			}
		}
		out = d.EngineerDraft
		return nil
	})
	return out, err
}

func (h *Hub) stopEngineerDraft(ctx context.Context, t *txn, d *store.EngineerDraftRow, reason string) error {
	neverStarted := d.State == protocol.RunOffered
	d.State, d.Detail, d.CancelRequested, d.Fields = protocol.RunStopping, reason, true, nil
	// The hub has not issued start_run for an unacknowledged offer. A late
	// acceptance is dropped below; no provider process can have started.
	if neverStarted {
		d.State = protocol.RunCancelled
	}
	if err := store.UpdateEngineerDraft(ctx, t.tx, *d); err != nil {
		return err
	}
	if err := store.CancelOutboxForRun(ctx, t.tx, d.ID, "offer:"); err != nil {
		return err
	}
	if err := store.CancelOutboxForRun(ctx, t.tx, d.ID, "start:"); err != nil {
		return err
	}
	if err := h.queueCommand(ctx, t, d.NodeID, d.ID, 1, protocol.CmdCancelRun, "cancel:"+d.ID, protocol.CancelRun{Reason: reason, GraceMs: 0}); err != nil {
		return err
	}
	return h.emitNode(ctx, t, d.NodeID)
}

func validateDraftFields(text string) (*protocol.EngineerDraftFields, error) {
	invalid := func() (*protocol.EngineerDraftFields, error) {
		return nil, domain.Invalid("The provider returned an incomplete or invalid draft. Your editable fields are unchanged; retry or fill them in yourself.")
	}
	if len(text) > 24000 {
		return invalid()
	}
	var fields protocol.EngineerDraftFields
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fields); err != nil {
		return invalid()
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return invalid()
	}
	valid := func(s *string, limit int) bool {
		*s = strings.TrimSpace(*s)
		return *s != "" && utf8.RuneCountInString(*s) <= limit
	}
	if !valid(&fields.Role, 120) || !valid(&fields.Description, 1000) || !valid(&fields.Instructions, 8000) || len(fields.CapabilityTags) == 0 || len(fields.CapabilityTags) > 12 {
		return invalid()
	}
	for i := range fields.CapabilityTags {
		if !valid(&fields.CapabilityTags[i], 40) || strings.Contains(fields.CapabilityTags[i], ",") {
			return invalid()
		}
	}
	return &fields, nil
}

func (h *Hub) finishEngineerDraft(ctx context.Context, t *txn, d *store.EngineerDraftRow, term protocol.RunTerminal) error {
	if d.State == protocol.RunUnknown && term.ExitConfirmed {
		// Reconciliation can account for a late outcome, but must never
		// publish a result after the owner has been told to retry.
		d.CancelRequested, d.Detail = true, "The interrupted draft has stopped. Its late result was discarded."
	} else if !domain.RunHoldsLease(d.State) {
		return nil
	}
	if !h.now().Before(d.DeadlineAt) && !d.CancelRequested {
		d.CancelRequested, d.Detail = true, "Drafting timed out. Retry or fill in the fields yourself."
	}
	d.Fields, d.State = nil, protocol.RunFailed
	switch {
	case !term.ExitConfirmed:
		d.State, d.Detail = protocol.RunUnknown, "Draft outcome is not confirmed. Check the selected machine before explicitly retrying."
	case d.CancelRequested || term.Outcome == protocol.OutcomeCancelled:
		d.State = protocol.RunCancelled
		if d.Detail == "" {
			d.Detail = "Draft cancelled."
		}
	case term.Outcome == protocol.OutcomeSucceeded:
		fields, err := validateDraftFields(term.FinalText)
		if err != nil {
			d.Detail = domain.AsError(err).Message
		} else {
			d.Fields, d.State, d.Detail = fields, protocol.RunSucceeded, "Draft ready. Review and edit the fields, then supply a name and create the engineer."
		}
	case term.Outcome == protocol.OutcomeAuthRequired:
		d.Detail = "The selected provider needs attention. Check Connections, then retry."
	case term.Outcome == protocol.OutcomeRateLimited:
		d.Detail = "The selected provider reached its allowance. Retry after its pause ends."
	default:
		d.Detail = "Drafting failed. Check the selected provider in Connections, then retry or fill in the fields yourself."
	}
	if term.Outcome == protocol.OutcomeRateLimited {
		pause := h.now().Add(max(time.Minute, time.Duration(term.RetryAfterMs)*time.Millisecond))
		if _, err := t.tx.ExecContext(ctx, `UPDATE provider_profiles SET paused_until = ? WHERE id = ? AND (paused_until IS NULL OR paused_until < ?)`, store.TS(pause), d.Provider.ProfileID, store.TS(pause)); err != nil {
			return err
		}
	}
	if term.Outcome == protocol.OutcomeAuthRequired {
		if _, err := t.tx.ExecContext(ctx, `UPDATE provider_installations SET auth_state = 'needs_signin', auth_detail = ? WHERE node_id = ? AND provider = ? AND profile_id = ?`,
			"Authentication failed during drafting. Check this provider’s local configuration.", d.NodeID, d.Provider.Provider, d.Provider.ProfileID); err != nil {
			return err
		}
	}
	if term.Usage != nil {
		if err := store.InsertUsage(ctx, t.tx, domain.NewID(), d.ID, d.Provider.Provider, *term.Usage); err != nil {
			return err
		}
	}
	if err := store.CancelOutboxForRun(ctx, t.tx, d.ID, "offer:"); err != nil {
		return err
	}
	if err := store.CancelOutboxForRun(ctx, t.tx, d.ID, "start:"); err != nil {
		return err
	}
	t.kickAfter()
	if err := store.UpdateEngineerDraft(ctx, t.tx, *d); err != nil {
		return err
	}
	return h.emitNode(ctx, t, d.NodeID)
}

// draftFrame consumes only frames belonging to a persisted draft. The normal
// runner journal and outbox still provide command/event deduplication.
func (h *Hub) draftFrame(ctx context.Context, conn *NodeConn, f protocol.Frame) (bool, error) {
	if f.RunID == "" || f.Type == protocol.EvCommandAck {
		return false, nil
	}
	_, err := store.GetEngineerDraft(ctx, h.st.R(), f.RunID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	var reply *protocol.Frame
	err = h.do(ctx, func(t *txn) error {
		d, err := store.GetEngineerDraft(ctx, t.tx, f.RunID)
		if err != nil {
			return err
		}
		if d.NodeID != conn.NodeID || f.LeaseEpoch != 1 {
			return domain.Forbidden("This draft is not assigned to this machine and lease.")
		}
		respond := func(typ string, payload any) {
			b, _ := json.Marshal(payload)
			reply = &protocol.Frame{Type: typ, ID: f.ID, RunID: d.ID, LeaseEpoch: 1, Payload: b}
		}
		switch f.Type {
		case protocol.EvRunAck:
			var a protocol.RunAck
			if err := json.Unmarshal(f.Payload, &a); err != nil {
				return err
			}
			if a.CommandID != "offer:"+d.ID+":1" {
				return domain.Invalid("Unexpected draft offer acknowledgement.")
			}
			if err := store.AckOutbox(ctx, t.tx, a.CommandID); err != nil {
				return err
			}
			if d.State != protocol.RunOffered {
				if a.Accepted && d.State != protocol.RunPreparing && d.State != protocol.RunRunning {
					return h.queueCommand(ctx, t, d.NodeID, d.ID, 1, protocol.CmdCancelRun, "drop:"+d.ID, protocol.CancelRun{Reason: "draft is no longer active"})
				}
				return nil
			}
			if !h.now().Before(d.LeaseExpiresAt) || !h.now().Before(d.DeadlineAt) {
				return h.stopEngineerDraft(ctx, t, &d, "Draft offer expired. Retry when the machine is available.")
			}
			if !a.Accepted {
				return h.finishEngineerDraft(ctx, t, &d, protocol.RunTerminal{Outcome: protocol.OutcomeRejected, ExitConfirmed: true})
			}
			node, err := store.GetNode(ctx, t.tx, d.NodeID)
			if err != nil {
				return err
			}
			eligible := node.RevokedAt == nil && node.Status == protocol.NodeOnline && h.Connected(node.ID) && !node.Draining && !node.Capacity.DiskPressure
			eligible = eligible && slices.ContainsFunc(node.Profiles, func(p protocol.ExecutionProfile) bool { return p.Name == "native" && p.Available && p.EngineerDrafts })
			eligible = eligible && slices.ContainsFunc(node.Providers, func(p protocol.ProviderInstallation) bool {
				return p.Provider == d.Provider.Provider && p.ProfileID == d.Provider.ProfileID && p.AuthState == protocol.AuthReady && p.Capabilities.ReadOnly && p.Capabilities.EngineerDrafts && (p.Billing != protocol.BillingAPI || d.Provider.AllowAPIBilling)
			})
			if !eligible || h.now().Before(h.profilePausedUntil(ctx, d.Provider.ProfileID)) {
				return h.stopEngineerDraft(ctx, t, &d, "The selected machine or provider changed before drafting started. Check Connections and retry.")
			}
			d.State, d.LeaseExpiresAt = protocol.RunPreparing, h.now().Add(h.lim.LeaseDuration)
			if err := store.UpdateEngineerDraft(ctx, t.tx, d); err != nil {
				return err
			}
			return h.queueCommand(ctx, t, d.NodeID, d.ID, 1, protocol.CmdStartRun, "start:"+d.ID, map[string]string{"runId": d.ID})
		case protocol.EvRunEvent:
			var event protocol.RunEvent
			if err := json.Unmarshal(f.Payload, &event); err != nil {
				return err
			}
			if event.Seq == 0 {
				return nil
			}
			respond(protocol.CmdAck, protocol.Ack{RunID: d.ID, UpToSeq: event.Seq})
			if event.Seq <= d.LastSeq || !domain.RunHoldsLease(d.State) {
				return nil
			}
			d.LastSeq = event.Seq
			if event.Kind == protocol.RunEvUsage {
				var usage protocol.Usage
				if json.Unmarshal(event.Data, &usage) == nil {
					if err := store.InsertUsage(ctx, t.tx, domain.NewID(), d.ID, d.Provider.Provider, usage); err != nil {
						return err
					}
				}
			}
			if event.Kind == protocol.RunEvStarted && d.State == protocol.RunPreparing {
				d.State, d.Detail = protocol.RunRunning, "Drafting with the selected provider…"
			}
			return store.UpdateEngineerDraft(ctx, t.tx, d)
		case protocol.EvRunTerminal:
			var term protocol.RunTerminal
			if err := json.Unmarshal(f.Payload, &term); err != nil {
				return err
			}
			if err := h.finishEngineerDraft(ctx, t, &d, term); err != nil {
				return err
			}
			respond(protocol.CmdAck, protocol.Ack{RunID: d.ID, UpToSeq: term.LastSeq, Terminal: true})
		case protocol.EvToolCall:
			var call protocol.ToolCall
			if err := json.Unmarshal(f.Payload, &call); err != nil {
				return err
			}
			respond(protocol.CmdToolResult, protocol.ToolResult{CallID: call.CallID, Error: domain.Forbidden("Tools are unavailable while drafting an engineer.").API(call.CallID)})
		case protocol.EvApprovalRequest:
			var req protocol.ApprovalRequest
			if err := json.Unmarshal(f.Payload, &req); err != nil {
				return err
			}
			respond(protocol.CmdResolveApproval, protocol.ResolveApproval{RequestID: req.RequestID, Decision: "deny", Reason: "Actions are unavailable while drafting an engineer."})
		}
		return nil
	})
	if err == nil && reply != nil {
		err = conn.send(*reply)
	}
	return true, err
}

func (h *Hub) renewDraftLease(ctx context.Context, t *txn, nodeID, id string, epoch int64) (protocol.Lease, bool, error) {
	d, err := store.GetEngineerDraft(ctx, t.tx, id)
	if errors.Is(err, store.ErrNotFound) {
		return protocol.Lease{}, false, nil
	}
	lease := protocol.Lease{RunID: id, LeaseEpoch: epoch, Revoked: true, Reason: "draft is no longer active on this machine"}
	if err != nil {
		return lease, true, err
	}
	if d.NodeID == nodeID && epoch == 1 && domain.RunHoldsLease(d.State) && h.now().Before(d.DeadlineAt) {
		d.LeaseExpiresAt = h.now().Add(h.lim.LeaseDuration)
		lease.Revoked, lease.Reason, lease.ExpiresInMs = false, "", h.lim.LeaseDuration.Milliseconds()
		err = store.UpdateEngineerDraft(ctx, t.tx, d)
	}
	return lease, true, err
}

func (h *Hub) expireEngineerDrafts(ctx context.Context) {
	_ = h.do(ctx, func(t *txn) error {
		drafts, err := store.ActiveEngineerDrafts(ctx, t.tx)
		if err != nil {
			return err
		}
		for _, d := range drafts {
			switch {
			case !h.now().Before(d.LeaseExpiresAt):
				if err := h.finishEngineerDraft(ctx, t, &d, protocol.RunTerminal{Outcome: protocol.OutcomeUnknown}); err != nil {
					return err
				}
			case !h.now().Before(d.DeadlineAt) && !d.CancelRequested:
				if err := h.stopEngineerDraft(ctx, t, &d, "Drafting timed out. Retry or fill in the fields yourself."); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
