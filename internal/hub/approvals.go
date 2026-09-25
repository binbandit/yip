package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// policyDecision is the outcome of evaluating existing grants.
type policyDecision int

const (
	policyAllow policyDecision = iota
	policyDeny
	policyAsk // exceptional: needs an exact-action decision from the owner
)

var (
	rePush    = regexp.MustCompile(`\bgit\s+push\b`)
	reOpenPR  = regexp.MustCompile(`\bgh\s+pr\s+create\b`)
	reMerge   = regexp.MustCompile(`\bgh\s+pr\s+merge\b|\bgit\s+merge\b.*\b(main|master)\b`)
	reReview  = regexp.MustCompile(`\bgh\s+pr\s+review\b`)
	reDanger  = regexp.MustCompile(`\bsudo\b|\brm\s+-rf\s+/|\bmkfs\b|\bdd\s+if=|\bchmod\s+-R\s+777\s+/|\bnpm\s+publish\b|\bcargo\s+publish\b|\bdocker\s+push\b|\bkubectl\b|\bterraform\s+apply\b|\bssh\b|\bscp\b`)
	reNetwork = regexp.MustCompile(`\bcurl\b|\bwget\b|\bnc\b|\bhttp(s)?://`)
)

// evaluatePolicy maps a provider permission request onto existing grants.
// Authorized routine actions proceed; actions outside the run's scope are
// denied or become an exceptional, exact-action request.
func (h *Hub) evaluatePolicy(ctx context.Context, q store.Q, run store.RunRow, job store.JobRow, a protocol.ApprovalAction) (policyDecision, string, string) {
	cmd := strings.ToLower(a.Command)
	var grant protocol.Grant
	if job.ProjectID != "" {
		grant, _ = store.GetGrant(ctx, q, job.ProjectID, run.EngineerID)
	}
	has := func(action string) bool { return contains(grant.Actions, action) }
	readOnly := run.Mode != protocol.ModeEdit
	switch a.Kind {
	case "mcp":
		return policyAllow, "yip tools are authorized per call by the hub", ""
	case "read":
		return policyAllow, "reading the workspace is within scope", ""
	case "edit":
		if readOnly {
			return policyDeny, "this run is read-only", ""
		}
		if grant.Access != "write" && job.ProjectID != "" {
			return policyDeny, "no write access to this project", ""
		}
		return policyAllow, "editing the job's own worktree is within the write grant", ""
	case "push":
		if has("push") {
			return policyAllow, "the project grant allows pushing", ""
		}
		return policyAsk, "", "push"
	case "merge":
		if has("merge") {
			return policyAllow, "the project grant allows merging", ""
		}
		return policyAsk, "", "merge"
	case "publish":
		if has("publish_review") || has("open_pr") {
			return policyAllow, "the project grant allows publication", ""
		}
		return policyAsk, "", "publish"
	case "exec":
		switch {
		case rePush.MatchString(cmd):
			if has("push") {
				return policyAllow, "the project grant allows pushing", ""
			}
			return policyAsk, "", "push"
		case reMerge.MatchString(cmd):
			if has("merge") {
				return policyAllow, "the project grant allows merging", ""
			}
			return policyAsk, "", "merge"
		case reOpenPR.MatchString(cmd):
			if has("open_pr") {
				return policyAllow, "the project grant allows opening pull requests", ""
			}
			return policyAsk, "", "open_pr"
		case reReview.MatchString(cmd):
			return policyDeny, "publish reviews through forge_publish_review so they stay revision-bound", ""
		case reDanger.MatchString(cmd):
			return policyAsk, "", "exec"
		case reNetwork.MatchString(cmd):
			return policyAsk, "", "network"
		}
		if readOnly {
			return policyAllow, "running read-only checks in the snapshot", ""
		}
		return policyAllow, "running commands in the job's own worktree is within scope", ""
	case "network":
		return policyAsk, "", "network"
	}
	return policyAsk, "", "other"
}

func digest(a protocol.ApprovalAction, raw json.RawMessage) string {
	h := sha256.New()
	b, _ := json.Marshal(a)
	h.Write(b)
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// onApprovalRequest answers a provider's blocking permission request.
func (h *Hub) onApprovalRequest(ctx context.Context, nodeID, runID string, epoch int64, req protocol.ApprovalRequest) error {
	return h.do(ctx, func(t *txn) error {
		run, err := requireLease(ctx, t.tx, nodeID, runID, epoch)
		if err != nil {
			return err
		}
		if existing, err := store.GetApprovalByRequest(ctx, t.tx, run.ID, req.RequestID); err == nil {
			// Replayed request: re-send the decision if one exists.
			if existing.Status == "approved" || existing.Status == "rejected" || existing.Status == "expired" || existing.Status == "consumed" {
				d := "deny"
				if existing.Status == "approved" || existing.Status == "consumed" {
					d = "allow"
				}
				return h.queueCommand(ctx, t, nodeID, run.ID, run.LeaseEpoch, protocol.CmdResolveApproval, "approval:"+existing.ID,
					protocol.ResolveApproval{RequestID: req.RequestID, ApprovalID: existing.ID, Decision: d})
			}
			return nil
		}
		job, err := store.GetJob(ctx, t.tx, run.JobID)
		if err != nil {
			return err
		}
		decision, why, class := h.evaluatePolicy(ctx, t.tx, run, job, req.Action)
		actor := protocol.Actor{Kind: protocol.ActorEngineer, ID: run.EngineerID}
		switch decision {
		case policyAllow, policyDeny:
			d := "allow"
			if decision == policyDeny {
				d = "deny"
			}
			if err := t.audit(actor, "policy", "permission."+d, run.ID, d, req.Action.Summary+" — "+why); err != nil {
				return err
			}
			if err := t.emit(ev{Type: "permission.auto", Actor: actor, Room: run.Destination.RoomID, Job: run.JobID, Run: run.ID,
				Payload: map[string]any{"action": req.Action, "decision": d, "reason": why}}); err != nil {
				return err
			}
			// Policy answers are direct (not an owner decision) and single-use.
			b, _ := json.Marshal(protocol.ResolveApproval{RequestID: req.RequestID, Decision: d, Reason: why})
			t.afterCommit(func() {
				h.sendDirect(nodeID, protocol.Frame{Type: protocol.CmdResolveApproval, ID: "policy:" + req.RequestID, RunID: run.ID, LeaseEpoch: run.LeaseEpoch, Payload: b})
			})
			return nil
		}
		// Exceptional authority: show the exact action inline in the source conversation.
		scope := "this action once, in job " + shortRev(job.ID)
		if job.ProjectID != "" {
			if p, err := store.GetProject(ctx, t.tx, job.ProjectID); err == nil {
				scope = "this action once, for " + p.Name
			}
		}
		ap := store.ApprovalRow{Approval: protocol.Approval{ID: domain.NewID(), RunID: run.ID, JobID: job.ID, EngineerID: run.EngineerID,
			Action: req.Action, ArgsDigest: digest(req.Action, req.Raw), Scope: scope, Status: "pending", ExpiresAt: h.now().Add(h.lim.ApprovalTTL),
			Source: job.Source, CreatedAt: h.now()}, RequestID: req.RequestID}
		if job.Revision != nil {
			ap.TargetRev = job.Revision.Head
		}
		if err := store.InsertApproval(ctx, t.tx, h.Org().ID, ap); err != nil {
			return err
		}
		eng, _ := store.GetEngineer(ctx, t.tx, run.EngineerID)
		ownerID, _ := h.ownerID(ctx, t.tx)
		owner, _ := store.GetUser(ctx, t.tx, ownerID)
		body := "@" + owner.Handle + " " + eng.Name + " needs permission for something outside the current grants: " + req.Action.Summary
		msg, err := t.postMessage(newMessage{Room: job.Source.RoomID, Thread: job.Source.ThreadID, Author: systemActor, Kind: protocol.MessageApproval,
			Body: body, Mentions: []protocol.Mention{{Kind: protocol.ActorUser, ID: ownerID}},
			Refs: []protocol.Ref{{Kind: "approval", ID: ap.ID}, {Kind: "job", ID: job.ID}}, JobID: job.ID, RunID: run.ID})
		if err != nil {
			return err
		}
		if err := store.SetApprovalMessage(ctx, t.tx, ap.ID, msg.ID); err != nil {
			return err
		}
		if run.State == protocol.RunRunning {
			if err := store.SetRunState(ctx, t.tx, run.ID, protocol.RunAwaitingInput, ""); err != nil {
				return err
			}
			if err := h.runChanged(ctx, t, run.ID); err != nil {
				return err
			}
		}
		if err := store.SetJobActivity(ctx, t.tx, job.ID, "Waiting for permission: "+truncate(req.Action.Summary, 120), h.now()); err != nil {
			return err
		}
		if _, err := t.jobChanged(ctx, job.ID); err != nil {
			return err
		}
		_ = class
		a2, _ := store.GetApproval(ctx, t.tx, ap.ID)
		return t.emit(ev{Type: "approval.created", Actor: actor, Room: job.Source.RoomID, Job: job.ID, Run: run.ID, Payload: a2.Approval})
	})
}

// DecideApproval applies the owner's exact-action decision. Stale versions
// and expired or changed requests are rejected; a decision is single-use.
func (h *Hub) DecideApproval(ctx context.Context, userID, approvalID string, req protocol.ApprovalDecisionRequest) (protocol.Approval, error) {
	var out protocol.Approval
	err := h.do(ctx, func(t *txn) error {
		ap, err := store.GetApproval(ctx, t.tx, approvalID)
		if err != nil {
			return domain.NotFound("That permission request doesn't exist.")
		}
		if _, err := h.requireRoom(ctx, t.tx, userID, ap.Source.RoomID); err != nil {
			return err
		}
		if ap.Version != req.Version {
			return domain.Conflict("This request changed since you saw it. Review the current request.")
		}
		if ap.Status != "pending" {
			return domain.Conflict("This request is already %s.", ap.Status)
		}
		if !h.now().Before(ap.ExpiresAt) {
			return domain.Conflict("This request expired. If the action is still needed, a new exact request will appear.")
		}
		status := "rejected"
		if req.Decision == "approve" {
			status = "approved"
		} else if req.Decision != "reject" {
			return domain.Invalid("Decide approve or reject.")
		}
		ok, err := store.DecideApproval(ctx, t.tx, ap.ID, req.Version, status, userActor(userID), h.now())
		if err != nil {
			return err
		}
		if !ok {
			return domain.Conflict("This request changed or expired. Review the current request.")
		}
		run, err := store.GetRun(ctx, t.tx, ap.RunID)
		if err != nil {
			return err
		}
		if err := t.audit(userActor(userID), "owner", "approval."+status, ap.ID, "ok", ap.Action.Summary+" digest "+ap.ArgsDigest[:12]); err != nil {
			return err
		}
		if domain.RunHoldsLease(run.State) {
			d := "deny"
			if status == "approved" {
				d = "allow"
			}
			if err := h.queueCommand(ctx, t, run.NodeID, run.ID, run.LeaseEpoch, protocol.CmdResolveApproval, "approval:"+ap.ID,
				protocol.ResolveApproval{RequestID: ap.RequestID, ApprovalID: ap.ID, Decision: d, Reason: strings.TrimSpace(req.Note)}); err != nil {
				return err
			}
			if run.State == protocol.RunAwaitingInput {
				if err := store.SetRunState(ctx, t.tx, run.ID, protocol.RunRunning, ""); err != nil {
					return err
				}
				_ = h.runChanged(ctx, t, run.ID)
			}
		}
		a2, _ := store.GetApproval(ctx, t.tx, ap.ID)
		out = a2.Approval
		if m, err := store.GetMessage(ctx, t.tx, ap.Source.MessageID); err == nil {
			_ = t.emit(ev{Type: "message.updated", Room: m.RoomID, Payload: m})
		}
		return t.emit(ev{Type: "approval.updated", Actor: userActor(userID), Room: ap.Source.RoomID, Job: ap.JobID, Payload: out})
	})
	return out, err
}

// GetApproval returns one approval for display.
func (h *Hub) GetApproval(ctx context.Context, userID, id string) (protocol.Approval, error) {
	ap, err := store.GetApproval(ctx, h.st.R(), id)
	if err != nil {
		return protocol.Approval{}, domain.NotFound("That permission request doesn't exist.")
	}
	if _, err := h.requireRoom(ctx, h.st.R(), userID, ap.Source.RoomID); err != nil {
		return protocol.Approval{}, err
	}
	return ap.Approval, nil
}

// expireApprovals denies requests nobody decided in time. There is no
// automatic renewal; a still-needed action produces a new exact request.
func (h *Hub) expireApprovals(ctx context.Context) {
	pending, err := store.ListApprovals(ctx, h.st.R(), "status = 'pending' AND expires_at <= ?", store.TS(h.now()))
	if err != nil {
		return
	}
	for _, ap := range pending {
		_ = h.do(ctx, func(t *txn) error {
			ok, err := store.SetApprovalStatus(ctx, t.tx, ap.ID, "pending", "expired")
			if err != nil || !ok {
				return err
			}
			run, err := store.GetRun(ctx, t.tx, ap.RunID)
			if err == nil && domain.RunHoldsLease(run.State) {
				if err := h.queueCommand(ctx, t, run.NodeID, run.ID, run.LeaseEpoch, protocol.CmdResolveApproval, "approval:"+ap.ID,
					protocol.ResolveApproval{RequestID: ap.RequestID, ApprovalID: ap.ID, Decision: "deny", Reason: "The permission request expired."}); err != nil {
					return err
				}
				if run.State == protocol.RunAwaitingInput {
					_ = store.SetRunState(ctx, t.tx, run.ID, protocol.RunRunning, "")
				}
			}
			a2, _ := store.GetApproval(ctx, t.tx, ap.ID)
			if m, err := store.GetMessage(ctx, t.tx, ap.Source.MessageID); err == nil {
				_ = t.emit(ev{Type: "message.updated", Room: m.RoomID, Payload: m})
			}
			return t.emit(ev{Type: "approval.updated", Room: ap.Source.RoomID, Job: ap.JobID, Payload: a2.Approval})
		})
	}
}
