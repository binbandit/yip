package hub

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

const validDraft = `{"role":"Frontend engineer","description":"Builds accessible interfaces.","capabilityTags":["ui","accessibility"],"instructions":"Check keyboard navigation and test changes."}`

func draftTestEnv(t *testing.T) (*Hub, protocol.User, *NodeConn, protocol.CreateEngineerDraftRequest) {
	t.Helper()
	ctx := context.Background()
	h, err := Open(ctx, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	secret, _, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := h.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: "Test", Name: "Owner", Handle: "owner", Password: "a-long-test-password"})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := domain.NewID()
	conn := &NodeConn{NodeID: nodeID, Send: func(protocol.Frame) error { return nil }}
	err = h.do(ctx, func(tx *txn) error {
		if err := store.InsertNode(ctx, tx.tx, h.Org().ID, store.NodeRow{Node: protocol.Node{ID: nodeID, Name: "Selected machine", CreatedAt: h.now()}}); err != nil {
			return err
		}
		return store.SetNodeSeen(ctx, tx.tx, nodeID, protocol.NodeOnline, h.now())
	})
	if err != nil {
		t.Fatal(err)
	}
	h.nodes.put(conn)
	err = h.onCapabilities(ctx, nodeID, protocol.RunnerCapabilities{Slots: 2, Profiles: []protocol.ExecutionProfile{{Name: "native", Available: true, EngineerDrafts: true}}, Providers: []protocol.ProviderInstallation{{Provider: "codex", ProfileID: "codex:fixture", AuthState: protocol.AuthReady, Billing: protocol.BillingSubscription, Capabilities: protocol.ProviderCapabilities{ReadOnly: true, EngineerDrafts: true}, Models: []protocol.Model{{ID: "test"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return h, owner, conn, protocol.CreateEngineerDraftRequest{Description: "An engineer who builds accessible interfaces", NodeID: nodeID, Provider: protocol.ProviderPreference{Provider: "codex", ProfileID: "codex:fixture"}}
}

func draftSend(t *testing.T, h *Hub, conn *NodeConn, id, typ string, payload any) {
	t.Helper()
	b, _ := json.Marshal(payload)
	if handled, err := h.draftFrame(context.Background(), conn, protocol.Frame{Type: typ, RunID: id, LeaseEpoch: 1, Payload: b}); !handled || err != nil {
		t.Fatalf("draft frame handled=%v err=%v", handled, err)
	}
}

func TestEngineerDraftLifecycleAndIsolation(t *testing.T) {
	ctx := context.Background()
	h, owner, conn, req := draftTestEnv(t)
	d, err := h.CreateEngineerDraft(ctx, owner.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.PendingOutbox(ctx, h.st.R(), conn.NodeID, h.now().Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	var offer protocol.OfferRun
	for _, item := range items {
		var f protocol.Frame
		_ = json.Unmarshal([]byte(item.Frame), &f)
		if f.Type == protocol.CmdOfferRun {
			_ = json.Unmarshal(f.Payload, &offer)
		}
	}
	if !offer.Manifest.EngineerDraft || offer.Manifest.EngineerID != "" || offer.Manifest.JobID != "" || offer.Manifest.Repo != nil || offer.Manifest.Mode != protocol.ModeConversation || len(offer.Manifest.Tools) != 0 || offer.Manifest.TimeoutMs > 120000 {
		t.Fatalf("unsafe draft scope: %+v", offer.Manifest)
	}
	if _, err := h.GetEngineerDraft(ctx, "another-owner", d.ID); err == nil {
		t.Fatal("draft disclosed to another user")
	}
	if _, err := h.CancelEngineerDraft(ctx, "another-owner", d.ID); err == nil {
		t.Fatal("another user cancelled draft")
	}
	for _, table := range []string{"engineers", "jobs"} {
		var count int
		if err := h.st.R().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("draft created %s: %d %v", table, count, err)
		}
	}
	if _, err := h.CreateEngineerDraft(ctx, "not-owner", req); err == nil {
		t.Fatal("non-owner draft admitted")
	}
	if _, err := h.CreateEngineerDraft(ctx, owner.ID, req); err == nil {
		t.Fatal("duplicate active draft admitted")
	}
	draftSend(t, h, conn, d.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + d.ID + ":1", Accepted: true})
	draftSend(t, h, conn, d.ID, protocol.EvRunEvent, protocol.RunEvent{Seq: 1, Kind: protocol.RunEvStarted})
	if err := h.do(ctx, func(tx *txn) error {
		lease, found, err := h.renewDraftLease(ctx, tx, conn.NodeID, d.ID, 1)
		if !found || lease.Revoked {
			t.Fatal("valid draft lease rejected")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var reply protocol.Frame
	replyConn := &NodeConn{NodeID: conn.NodeID, Send: func(f protocol.Frame) error { reply = f; return nil }}
	draftSend(t, h, replyConn, d.ID, protocol.EvToolCall, protocol.ToolCall{CallID: "call", Tool: "room_post"})
	var tool protocol.ToolResult
	_ = json.Unmarshal(reply.Payload, &tool)
	if tool.OK || tool.Error == nil || tool.Error.Code != "forbidden" {
		t.Fatalf("tool admitted: %+v", tool)
	}
	draftSend(t, h, replyConn, d.ID, protocol.EvApprovalRequest, protocol.ApprovalRequest{RequestID: "permission"})
	var approval protocol.ResolveApproval
	_ = json.Unmarshal(reply.Payload, &approval)
	if approval.Decision != "deny" {
		t.Fatal("approval admitted")
	}
	draftSend(t, h, conn, d.ID, protocol.EvRunTerminal, protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, ExitConfirmed: true, FinalText: validDraft})
	got, _ := h.GetEngineerDraft(ctx, owner.ID, d.ID)
	if got.State != protocol.RunSucceeded || got.Fields == nil || got.Fields.Role != "Frontend engineer" {
		t.Fatalf("draft = %+v", got)
	}
	draftSend(t, h, conn, d.ID, protocol.EvRunTerminal, protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, ExitConfirmed: true, FinalText: `{}`})
	got, _ = h.GetEngineerDraft(ctx, owner.ID, d.ID)
	if got.Fields == nil {
		t.Fatal("duplicate terminal replaced validated fields")
	}
	counts, _ := h.counts(ctx)
	if counts.org != 0 {
		t.Fatal("completed draft still reserves capacity")
	}
}

func TestEngineerDraftAdmission(t *testing.T) {
	for _, scenario := range []string{"offline", "draining", "revoked", "unsupported runtime", "unconfigured provider", "no readonly", "native tools", "api billing", "wrong account", "unknown model", "paused", "org capacity", "account capacity", "node capacity"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			h, owner, conn, req := draftTestEnv(t)
			if err := h.do(ctx, func(tx *txn) error {
				switch scenario {
				case "offline":
					h.nodes.remove(conn)
				case "draining":
					return store.SetNodeDraining(ctx, tx.tx, conn.NodeID, true)
				case "revoked":
					return store.RevokeNode(ctx, tx.tx, conn.NodeID)
				case "unsupported runtime":
					_, err := tx.tx.ExecContext(ctx, `UPDATE nodes SET profiles = '[]' WHERE id = ?`, conn.NodeID)
					return err
				case "unconfigured provider":
					_, err := tx.tx.ExecContext(ctx, `UPDATE provider_installations SET auth_state = 'unknown' WHERE node_id = ?`, conn.NodeID)
					return err
				case "no readonly":
					_, err := tx.tx.ExecContext(ctx, `UPDATE provider_installations SET capabilities = '{}' WHERE node_id = ?`, conn.NodeID)
					return err
				case "native tools":
					_, err := tx.tx.ExecContext(ctx, `UPDATE provider_installations SET capabilities = '{"readOnly":true}' WHERE node_id = ?`, conn.NodeID)
					return err
				case "api billing":
					_, err := tx.tx.ExecContext(ctx, `UPDATE provider_installations SET billing = 'api' WHERE node_id = ?`, conn.NodeID)
					return err
				case "wrong account":
					req.Provider.ProfileID = "different"
				case "unknown model":
					req.Provider.Model = "unreported"
				case "paused":
					_, err := tx.tx.ExecContext(ctx, `UPDATE provider_profiles SET paused_until = ?`, store.TS(h.now().Add(time.Hour)))
					return err
				case "org capacity":
					h.lim.ActiveRunsPerOrg = 0
				case "account capacity", "node capacity":
					other := protocol.User{ID: domain.NewID(), OrgID: h.Org().ID, Name: "Other", Handle: "other", CreatedAt: h.now()}
					if err := store.InsertUser(ctx, tx.tx, store.UserRow{User: other, PasswordHash: "unused"}); err != nil {
						return err
					}
					provider := req.Provider
					if scenario == "node capacity" {
						provider.ProfileID = "other"
						_, err := tx.tx.ExecContext(ctx, `UPDATE nodes SET capacity = '{"slots":1}' WHERE id = ?`, conn.NodeID)
						if err != nil {
							return err
						}
					}
					return store.InsertEngineerDraft(ctx, tx.tx, store.EngineerDraftRow{EngineerDraft: protocol.EngineerDraft{ID: domain.NewID(), NodeID: conn.NodeID, Provider: provider, State: protocol.RunRunning, CreatedAt: h.now()}, UserID: other.ID, Request: protocol.CreateEngineerDraftRequest{Provider: provider}, LeaseExpiresAt: h.now().Add(time.Minute), DeadlineAt: h.now().Add(time.Minute)})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.CreateEngineerDraft(ctx, owner.ID, req); err == nil {
				t.Fatal("inadmissible draft was accepted")
			}
		})
	}
}

func TestEngineerDraftCancellationExpiryAndRetry(t *testing.T) {
	for _, action := range []string{"cancel", "stop machine", "revoke", "lease expiry", "timeout"} {
		t.Run(action, func(t *testing.T) {
			ctx := context.Background()
			h, owner, conn, req := draftTestEnv(t)
			d, err := h.CreateEngineerDraft(ctx, owner.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			draftSend(t, h, conn, d.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + d.ID + ":1", Accepted: true})
			switch action {
			case "cancel":
				_, err = h.CancelEngineerDraft(ctx, owner.ID, d.ID)
			case "stop machine":
				err = h.StopNodeWork(ctx, owner.ID, conn.NodeID)
			case "revoke":
				err = h.RevokeNode(ctx, owner.ID, conn.NodeID)
			case "lease expiry", "timeout":
				err = h.do(ctx, func(tx *txn) error {
					row, err := store.GetEngineerDraft(ctx, tx.tx, d.ID)
					if err != nil {
						return err
					}
					if action == "lease expiry" {
						row.LeaseExpiresAt = h.now().Add(-time.Second)
					} else {
						_, err = tx.tx.ExecContext(ctx, `UPDATE engineer_drafts SET deadline_at = ? WHERE id = ?`, store.TS(h.now().Add(-time.Second)), d.ID)
						if err != nil {
							return err
						}
					}
					return store.UpdateEngineerDraft(ctx, tx.tx, row)
				})
				h.expireEngineerDrafts(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			draftSend(t, h, conn, d.ID, protocol.EvRunTerminal, protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, ExitConfirmed: true, FinalText: validDraft})
			got, _ := h.GetEngineerDraft(ctx, owner.ID, d.ID)
			if got.Fields != nil || got.State == protocol.RunSucceeded {
				t.Fatalf("late success published after %s: %+v", action, got)
			}
			if action == "cancel" {
				next, err := h.CreateEngineerDraft(ctx, owner.ID, req)
				if err != nil || next.ID == d.ID {
					t.Fatalf("explicit retry: %+v %v", next, err)
				}
			}
		})
	}
}

func TestEngineerDraftConcurrentAdmission(t *testing.T) {
	h, owner, _, req := draftTestEnv(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := h.CreateEngineerDraft(context.Background(), owner.ID, req); results <- err })
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent attempts", accepted)
	}
}

func TestEngineerDraftOutputValidation(t *testing.T) {
	for _, raw := range []string{`{}`, `{"role":"Only a role"}`, "```json\n" + validDraft + "\n```", validDraft + " {}", strings.Replace(validDraft, `"role":`, `"name":"Invented","role":`, 1), strings.Replace(validDraft, `"ui","accessibility"`, `""`, 1), strings.Repeat("x", 24001)} {
		if fields, err := validateDraftFields(raw); err == nil || fields != nil {
			t.Errorf("accepted malformed draft: %.80s", raw)
		}
	}
	if _, err := validateDraftFields(validDraft); err != nil {
		t.Fatal(err)
	}
}

func TestEngineerDraftDurableReconnectAndStaleLease(t *testing.T) {
	ctx := context.Background()
	h, owner, conn, req := draftTestEnv(t)
	d, err := h.CreateEngineerDraft(ctx, owner.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	draftSend(t, h, conn, d.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + d.ID + ":1", Accepted: true})
	dataDir := h.cfg.DataDir
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	term := protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, FinalText: validDraft, ExitConfirmed: true}
	if err := reopened.do(ctx, func(tx *txn) error {
		return reopened.reconcileJournal(ctx, tx, "wrong-node", []protocol.JournalRunState{{RunID: d.ID, LeaseEpoch: 1, Terminal: &term}})
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(term)
	if _, err := reopened.draftFrame(ctx, conn, protocol.Frame{Type: protocol.EvRunTerminal, RunID: d.ID, LeaseEpoch: 2, Payload: raw}); err == nil {
		t.Fatal("stale lease published draft")
	}
	if err := reopened.do(ctx, func(tx *txn) error {
		return reopened.reconcileJournal(ctx, tx, conn.NodeID, []protocol.JournalRunState{{RunID: d.ID, LeaseEpoch: 1, Terminal: &term}})
	}); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetEngineerDraft(ctx, owner.ID, d.ID)
	if err != nil || got.State != protocol.RunSucceeded || got.Fields == nil {
		t.Fatalf("journaled result not recovered: %+v %v", got, err)
	}
}

func TestEngineerDraftLateAckAndResultDoNotRaceDeadline(t *testing.T) {
	for _, ack := range []bool{true, false} {
		t.Run(map[bool]string{true: "late ack", false: "late result"}[ack], func(t *testing.T) {
			ctx := context.Background()
			h, owner, conn, req := draftTestEnv(t)
			d, err := h.CreateEngineerDraft(ctx, owner.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.do(ctx, func(tx *txn) error {
				_, err := tx.tx.ExecContext(ctx, `UPDATE engineer_drafts SET deadline_at = ? WHERE id = ?`, store.TS(h.now().Add(-time.Second)), d.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if ack {
				draftSend(t, h, conn, d.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + d.ID + ":1", Accepted: true})
			} else {
				draftSend(t, h, conn, d.ID, protocol.EvRunTerminal, protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, FinalText: validDraft, ExitConfirmed: true})
			}
			got, _ := h.GetEngineerDraft(ctx, owner.ID, d.ID)
			if got.Fields != nil || got.State == protocol.RunPreparing || got.State == protocol.RunSucceeded {
				t.Fatalf("expired attempt continued: %+v", got)
			}
		})
	}
}

func TestEngineerDraftExistingNonOwnerIsForbidden(t *testing.T) {
	h, owner, _, req := draftTestEnv(t)
	ctx := context.Background()
	other := protocol.User{ID: domain.NewID(), OrgID: h.Org().ID, Name: "Other", Handle: "other", CreatedAt: h.now().Add(time.Second)}
	if err := h.do(ctx, func(tx *txn) error { return store.InsertUser(ctx, tx.tx, store.UserRow{User: other}) }); err != nil {
		t.Fatal(err)
	}
	if _, err := h.CreateEngineerDraft(ctx, other.ID, req); domain.AsError(err).Code != "forbidden" {
		t.Fatalf("non-owner admitted: %v", err)
	}
	draft, err := h.CreateEngineerDraft(ctx, owner.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.StopNodeWork(ctx, other.ID, req.NodeID); domain.AsError(err).Code != "forbidden" {
		t.Fatalf("non-owner stopped draft: %v", err)
	}
	got, err := h.GetEngineerDraft(ctx, owner.ID, draft.ID)
	if err != nil || got.State != protocol.RunOffered {
		t.Fatalf("draft changed: %+v %v", got, err)
	}
}

func TestEngineerDraftBillingOptInAndAllowancePause(t *testing.T) {
	ctx := context.Background()
	h, owner, conn, req := draftTestEnv(t)
	if err := h.do(ctx, func(tx *txn) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE provider_installations SET billing = 'api' WHERE node_id = ?`, conn.NodeID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.CreateEngineerDraft(ctx, owner.ID, req); err == nil {
		t.Fatal("API billing enabled without consent")
	}
	req.Provider.AllowAPIBilling = true
	d, err := h.CreateEngineerDraft(ctx, owner.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	draftSend(t, h, conn, d.ID, protocol.EvRunTerminal, protocol.RunTerminal{Outcome: protocol.OutcomeRateLimited, ExitConfirmed: true, RetryAfterMs: 60000, Usage: &protocol.Usage{Billing: protocol.BillingAPI}})
	if !h.now().Before(h.profilePausedUntil(ctx, req.Provider.ProfileID)) {
		t.Fatal("provider allowance pause was bypassed")
	}
	if _, err := h.CreateEngineerDraft(ctx, owner.ID, req); err == nil {
		t.Fatal("retry bypassed allowance pause")
	}
	var usage int
	if err := h.st.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_samples WHERE run_id = ?`, d.ID).Scan(&usage); err != nil || usage != 1 {
		t.Fatalf("draft usage not recorded: %d %v", usage, err)
	}
}

func TestEngineerDraftRechecksSelectionBeforeStart(t *testing.T) {
	for _, update := range []string{
		`UPDATE provider_installations SET auth_state = 'unknown'`,
		`UPDATE provider_installations SET profile_id = 'another-account'`,
		`UPDATE provider_installations SET billing = 'api'`,
		`UPDATE nodes SET draining = 1`,
		`UPDATE nodes SET profiles = '[]'`,
	} {
		t.Run(update, func(t *testing.T) {
			ctx := context.Background()
			h, owner, conn, req := draftTestEnv(t)
			d, err := h.CreateEngineerDraft(ctx, owner.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.do(ctx, func(tx *txn) error { _, err := tx.tx.ExecContext(ctx, update); return err }); err != nil {
				t.Fatal(err)
			}
			draftSend(t, h, conn, d.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + d.ID + ":1", Accepted: true})
			got, _ := h.GetEngineerDraft(ctx, owner.ID, d.ID)
			if got.State != protocol.RunCancelled {
				t.Fatalf("changed selection started: %+v", got)
			}
			var starts int
			if err := h.st.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE run_id = ? AND command_id LIKE 'start:%'`, d.ID).Scan(&starts); err != nil || starts != 0 {
				t.Fatalf("start command emitted: %d %v", starts, err)
			}
		})
	}
}

func TestEngineerDraftCancelBeforeOfferAck(t *testing.T) {
	ctx := context.Background()
	h, owner, conn, req := draftTestEnv(t)
	d, err := h.CreateEngineerDraft(ctx, owner.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := h.CancelEngineerDraft(ctx, owner.ID, d.ID)
	if err != nil || stopped.State != protocol.RunCancelled {
		t.Fatalf("unstarted draft did not cancel: %+v %v", stopped, err)
	}
	draftSend(t, h, conn, d.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + d.ID + ":1", Accepted: true})
	var starts int
	if err := h.st.R().QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE run_id = ? AND command_id LIKE 'start:%'`, d.ID).Scan(&starts); err != nil || starts != 0 {
		t.Fatalf("cancelled offer started: %d %v", starts, err)
	}
	if _, err := h.CreateEngineerDraft(ctx, owner.ID, req); err != nil {
		t.Fatalf("unstarted cancellation blocked explicit retry: %v", err)
	}
}

func draftNodeSnapshot(t *testing.T, h *Hub, nodeID string, after int64) protocol.Node {
	t.Helper()
	events, err := store.EventsAfter(context.Background(), h.st.R(), after, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var latest protocol.Node
	for _, event := range events {
		if event.Type != "node.updated" {
			continue
		}
		var node protocol.Node
		if err := json.Unmarshal(event.Payload, &node); err != nil {
			t.Fatal(err)
		}
		if node.ID == nodeID {
			latest = node
		}
	}
	if latest.ID == "" {
		t.Fatal("no live machine snapshot was published")
	}
	return latest
}

func TestEngineerDraftPublishesOccupancy(t *testing.T) {
	for _, finish := range []string{"success", "cancel offer", "cancel running", "expiry", "timeout", "revoke", "reconciliation"} {
		t.Run(finish, func(t *testing.T) {
			ctx := context.Background()
			h, owner, conn, req := draftTestEnv(t)
			before, _ := store.MaxEventSeq(ctx, h.st.R())
			draft, err := h.CreateEngineerDraft(ctx, owner.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := draftNodeSnapshot(t, h, conn.NodeID, before)
			if len(snapshot.ActiveRunIDs) != 1 || snapshot.ActiveRunIDs[0] != draft.ID {
				t.Fatalf("admission snapshot: %+v", snapshot.ActiveRunIDs)
			}
			before, _ = store.MaxEventSeq(ctx, h.st.R())
			term := protocol.RunTerminal{Outcome: protocol.OutcomeSucceeded, FinalText: validDraft, ExitConfirmed: true}
			if finish != "cancel offer" {
				draftSend(t, h, conn, draft.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + draft.ID + ":1", Accepted: true})
			}
			switch finish {
			case "cancel offer", "cancel running":
				if _, err := h.CancelEngineerDraft(ctx, owner.ID, draft.ID); err != nil {
					t.Fatal(err)
				}
				if finish == "cancel running" {
					snapshot = draftNodeSnapshot(t, h, conn.NodeID, before)
					if len(snapshot.ActiveRunIDs) != 1 {
						t.Fatal("stopping draft released its slot before exit")
					}
					before, _ = store.MaxEventSeq(ctx, h.st.R())
					term.Outcome = protocol.OutcomeCancelled
					draftSend(t, h, conn, draft.ID, protocol.EvRunTerminal, term)
				}
			case "expiry":
				if err := h.do(ctx, func(tx *txn) error {
					_, err := tx.tx.ExecContext(ctx, `UPDATE engineer_drafts SET lease_expires_at = ? WHERE id = ?`, store.TS(h.now().Add(-time.Second)), draft.ID)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				h.expireEngineerDrafts(ctx)
			case "timeout":
				if err := h.do(ctx, func(tx *txn) error {
					_, err := tx.tx.ExecContext(ctx, `UPDATE engineer_drafts SET deadline_at = ? WHERE id = ?`, store.TS(h.now().Add(-time.Second)), draft.ID)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				h.expireEngineerDrafts(ctx)
				if snapshot := draftNodeSnapshot(t, h, conn.NodeID, before); len(snapshot.ActiveRunIDs) != 1 {
					t.Fatal("timeout released slot before exit")
				}
				before, _ = store.MaxEventSeq(ctx, h.st.R())
				term.Outcome = protocol.OutcomeCancelled
				draftSend(t, h, conn, draft.ID, protocol.EvRunTerminal, term)
			case "revoke":
				if err := h.RevokeNode(ctx, owner.ID, conn.NodeID); err != nil {
					t.Fatal(err)
				}
			case "reconciliation":
				if err := h.do(ctx, func(tx *txn) error {
					return h.reconcileJournal(ctx, tx, conn.NodeID, []protocol.JournalRunState{{RunID: draft.ID, LeaseEpoch: 1, Terminal: &term}})
				}); err != nil {
					t.Fatal(err)
				}
			default:
				draftSend(t, h, conn, draft.ID, protocol.EvRunTerminal, term)
			}
			snapshot = draftNodeSnapshot(t, h, conn.NodeID, before)
			if len(snapshot.ActiveRunIDs) != 0 {
				t.Fatalf("terminal snapshot retained draft: %+v", snapshot.ActiveRunIDs)
			}
		})
	}
}

func TestEngineerDraftAuthFailureInvalidatesSelectedInstallation(t *testing.T) {
	ctx := context.Background()
	h, owner, conn, req := draftTestEnv(t)
	node, err := store.GetNode(ctx, h.st.R(), conn.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	caps := protocol.RunnerCapabilities{Slots: 2, Profiles: node.Profiles, Providers: append([]protocol.ProviderInstallation{}, node.Providers...)}
	other := caps.Providers[0]
	other.Provider, other.ProfileID = "pi", "pi:other"
	caps.Providers = append(caps.Providers, other)
	if err := h.onCapabilities(ctx, conn.NodeID, caps); err != nil {
		t.Fatal(err)
	}
	otherNodeID := domain.NewID()
	if err := h.do(ctx, func(tx *txn) error {
		if err := store.InsertNode(ctx, tx.tx, h.Org().ID, store.NodeRow{Node: protocol.Node{ID: otherNodeID, Name: "Other machine", CreatedAt: h.now()}}); err != nil {
			return err
		}
		return store.SetNodeCapabilities(ctx, tx.tx, otherNodeID, caps, "", "")
	}); err != nil {
		t.Fatal(err)
	}
	draft, err := h.CreateEngineerDraft(ctx, owner.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	draftSend(t, h, conn, draft.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + draft.ID + ":1", Accepted: true})
	before, _ := store.MaxEventSeq(ctx, h.st.R())
	draftSend(t, h, conn, draft.ID, protocol.EvRunTerminal, protocol.RunTerminal{Outcome: protocol.OutcomeAuthRequired, ExitConfirmed: true})
	node, err = store.GetNode(ctx, h.st.R(), conn.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range node.Providers {
		want := protocol.AuthReady
		if provider.Provider == req.Provider.Provider {
			want = protocol.AuthNeedsSignIn
		}
		if provider.AuthState != want {
			t.Fatalf("provider readiness: %+v", provider)
		}
	}
	otherNode, err := store.GetNode(ctx, h.st.R(), otherNodeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range otherNode.Providers {
		if provider.AuthState != protocol.AuthReady {
			t.Fatalf("another machine's auth changed: %+v", provider)
		}
	}
	snapshot := draftNodeSnapshot(t, h, conn.NodeID, before)
	if snapshot.Providers[0].AuthState != protocol.AuthNeedsSignIn {
		t.Fatalf("stale live auth: %+v", snapshot.Providers)
	}
	if _, err := h.CreateEngineerDraft(ctx, owner.ID, req); err == nil {
		t.Fatal("draft accepted after authentication failure")
	}
	run := store.RunRow{Run: protocol.Run{Provider: req.Provider.Provider, ProfileID: req.Provider.ProfileID, Mode: protocol.ModeConversation}, ExecutionProfile: "native"}
	counts, _ := h.counts(ctx)
	if placed, why, _ := h.place(ctx, run, store.JobRow{}, []store.NodeRow{node}, counts); placed != nil || !strings.Contains(why, "sign-in") {
		t.Fatalf("ordinary work admitted after auth failure: %+v %s", placed, why)
	}
	if err := h.onCapabilities(ctx, conn.NodeID, caps); err != nil {
		t.Fatal(err)
	}
	node, _ = store.GetNode(ctx, h.st.R(), conn.NodeID)
	if placed, why, _ := h.place(ctx, run, store.JobRow{}, []store.NodeRow{node}, counts); placed == nil {
		t.Fatalf("restored ordinary placement blocked: %s", why)
	}
	if _, err := h.CreateEngineerDraft(ctx, owner.ID, req); err != nil {
		t.Fatalf("restored draft blocked: %v", err)
	}
}

func TestEngineerDraftAuthFailurePreservesReplacementProfile(t *testing.T) {
	ctx := context.Background()
	h, owner, conn, req := draftTestEnv(t)
	draft, err := h.CreateEngineerDraft(ctx, owner.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	draftSend(t, h, conn, draft.ID, protocol.EvRunAck, protocol.RunAck{CommandID: "offer:" + draft.ID + ":1", Accepted: true})
	if err := h.do(ctx, func(tx *txn) error {
		_, err := tx.tx.ExecContext(ctx, `UPDATE provider_installations SET profile_id = 'replacement' WHERE node_id = ? AND provider = ?`, conn.NodeID, req.Provider.Provider)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	draftSend(t, h, conn, draft.ID, protocol.EvRunTerminal, protocol.RunTerminal{Outcome: protocol.OutcomeAuthRequired, ExitConfirmed: true})
	node, err := store.GetNode(ctx, h.st.R(), conn.NodeID)
	if err != nil || node.Providers[0].AuthState != protocol.AuthReady {
		t.Fatalf("replacement account invalidated: %+v %v", node.Providers, err)
	}
}
