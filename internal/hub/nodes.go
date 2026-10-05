package hub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/events"
	"github.com/binbandit/yip/internal/redact"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

type sqlTx = sql.Tx
type eventsTransient = events.Transient

const (
	enrollmentTTL = 15 * time.Minute
	nodeCertTTL   = 365 * 24 * time.Hour
)

// ListNodes returns paired machines.
func (h *Hub) ListNodes(ctx context.Context) ([]protocol.Node, error) {
	rows, err := store.ListNodes(ctx, h.st.R())
	if err != nil {
		return nil, err
	}
	out := make([]protocol.Node, len(rows))
	for i, r := range rows {
		out[i] = r.Node
	}
	return out, nil
}

// CreateEnrollment issues a high-entropy, single-use, short-lived token and
// the command to pair a machine. The owner sees the hub fingerprint the
// runner will pin.
func (h *Hub) CreateEnrollment(ctx context.Context, userID string, req protocol.CreateEnrollmentRequest) (protocol.Enrollment, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "New machine"
	}
	token := "yipe_" + domain.RandomToken(32)
	en := protocol.Enrollment{ID: domain.NewID(), Token: token, Name: name, ExpiresAt: h.now().Add(enrollmentTTL),
		HubURL: h.cfg.RunnerURL, HubFingerprint: h.ca.Fingerprint()}
	// Each workspace needs its own runner identity and process. Keep the root
	// on the default state directory for existing installations.
	stateArg := ""
	if h.cfg.SessionHub != nil {
		stateArg = " --state ~/.yip/runners/" + shellQuote(h.Org().ID)
	}
	en.Command = "yip runner pair" + stateArg + " --hub " + h.cfg.RunnerURL + " --fingerprint " + en.HubFingerprint + " --name " + shellQuote(name) + " --token " + token
	en.RunCommand = "yip runner" + stateArg
	err := h.do(ctx, func(t *txn) error {
		if err := store.InsertEnrollment(ctx, t.tx, h.Org().ID, en.ID, name, domain.HashToken(token), en.ExpiresAt); err != nil {
			return err
		}
		return t.audit(userActor(userID), "owner", "node.enrollment", en.ID, "ok", name)
	})
	return en, err
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Pair redeems an enrollment token and signs the node's locally generated key.
func (h *Hub) Pair(ctx context.Context, req protocol.PairRequest) (protocol.PairResponse, error) {
	nodeID := domain.NewID()
	var resp protocol.PairResponse
	err := h.do(ctx, func(t *txn) error {
		name, ok, err := store.RedeemEnrollment(ctx, t.tx, domain.HashToken(strings.TrimSpace(req.Token)), nodeID, h.now())
		if err != nil {
			return err
		}
		if !ok {
			return domain.Forbidden("That enrollment token is invalid, expired, or already used. Create a new one in Machines.")
		}
		certPEM, serial, fingerprint, err := h.ca.SignNodeCSR([]byte(req.CSRPEM), nodeID, nodeCertTTL)
		if err != nil {
			return domain.Invalid("The machine's certificate request is invalid: %s", err.Error())
		}
		if strings.TrimSpace(req.Name) != "" {
			name = strings.TrimSpace(req.Name)
		}
		n := store.NodeRow{Node: protocol.Node{ID: nodeID, Name: name, Hostname: req.Hostname, OS: req.OS, Arch: req.Arch,
			Fingerprint: fingerprint, CreatedAt: h.now()}, CertSerial: serial}
		if err := store.InsertNode(ctx, t.tx, h.Org().ID, n); err != nil {
			return err
		}
		if err := t.audit(protocol.Actor{Kind: protocol.ActorNode, ID: nodeID}, "enrollment_token", "node.pair", nodeID, "ok", name+" "+fingerprint); err != nil {
			return err
		}
		resp = protocol.PairResponse{NodeID: nodeID, CertPEM: string(certPEM), CAPEM: string(h.ca.CertPEM), Fingerprint: fingerprint}
		return h.emitNode(ctx, t, nodeID)
	})
	return resp, err
}

// PairLocal pairs an in-process runner through the same enrollment path.
func (h *Hub) PairLocal(ctx context.Context, name string, csrPEM []byte) (protocol.PairResponse, error) {
	en, err := h.CreateEnrollment(ctx, "", protocol.CreateEnrollmentRequest{Name: name})
	if err != nil {
		return protocol.PairResponse{}, err
	}
	host, _ := os.Hostname()
	return h.Pair(ctx, protocol.PairRequest{Token: en.Token, CSRPEM: string(csrPEM), Name: name, Hostname: host})
}

// SetDraining stops new assignments to a machine; active work finishes.
func (h *Hub) SetDraining(ctx context.Context, userID, nodeID string, draining bool) (protocol.Node, error) {
	var out protocol.Node
	err := h.do(ctx, func(t *txn) error {
		if _, err := store.GetNode(ctx, t.tx, nodeID); err != nil {
			return domain.NotFound("That machine doesn't exist.")
		}
		if err := store.SetNodeDraining(ctx, t.tx, nodeID, draining); err != nil {
			return err
		}
		if err := h.queueCommand(ctx, t, nodeID, "", 0, protocol.CmdDrain, "drain:"+domain.NewID(), map[string]bool{"draining": draining}); err != nil {
			return err
		}
		if err := t.audit(userActor(userID), "owner", "node.drain", nodeID, "ok", boolStr(draining)); err != nil {
			return err
		}
		n, _ := store.GetNode(ctx, t.tx, nodeID)
		out = n.Node
		t.kickAfter()
		return h.emitNode(ctx, t, nodeID)
	})
	return out, err
}

// StopNodeWork cancels every job with an active run on a machine. Distinct
// from draining.
// ProviderProfiles lists provider accounts and their concurrency limits.
func (h *Hub) ProviderProfiles(ctx context.Context) ([]protocol.ProviderProfile, error) {
	rows, err := h.st.R().QueryContext(ctx, `SELECT id, provider, label, billing, max_concurrency, COALESCE(paused_until, '') FROM provider_profiles ORDER BY provider, label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.ProviderProfile{}
	for rows.Next() {
		var p protocol.ProviderProfile
		var paused string
		if err := rows.Scan(&p.ID, &p.Provider, &p.Label, &p.Billing, &p.MaxConcurrency, &paused); err != nil {
			return nil, err
		}
		if t := store.ParseTS(paused); !t.IsZero() && h.now().Before(t) {
			p.PausedUntil = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetProviderConcurrency sets how many runs one provider account may carry
// at once across all machines. The default is 1 (brief §7), because
// simultaneous runs share the account's allowance.
func (h *Hub) SetProviderConcurrency(ctx context.Context, userID, profileID string, max int) (protocol.ProviderProfile, error) {
	var out protocol.ProviderProfile
	if max < 1 || max > 16 {
		return out, domain.Invalid("Choose between 1 and 16 runs at once.")
	}
	err := h.do(ctx, func(t *txn) error {
		if err := h.requireWorkspaceOwner(ctx, t.tx, userID); err != nil {
			return err
		}
		res, err := t.tx.ExecContext(ctx, `UPDATE provider_profiles SET max_concurrency = ? WHERE id = ?`, max, profileID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.NotFound("That provider account doesn't exist.")
		}
		if err := t.audit(userActor(userID), "owner", "provider.concurrency", profileID, "ok", strconv.Itoa(max)); err != nil {
			return err
		}
		t.kickAfter()
		if err := t.tx.QueryRowContext(ctx, `SELECT id, provider, label, billing, max_concurrency FROM provider_profiles WHERE id = ?`, profileID).
			Scan(&out.ID, &out.Provider, &out.Label, &out.Billing, &out.MaxConcurrency); err != nil {
			return err
		}
		return t.emit(ev{Type: "provider_profile.updated", Actor: userActor(userID), Payload: out})
	})
	return out, err
}

// ProbeNode asks a connected machine to re-check its providers now, so a
// sign-in the owner just completed (codex login, claude auth login) shows up
// without waiting for the periodic probe. It changes nothing by itself.
func (h *Hub) ProbeNode(ctx context.Context, userID, nodeID string) error {
	n, err := store.GetNode(ctx, h.st.R(), nodeID)
	if err != nil || n.RevokedAt != nil {
		return domain.NotFound("That machine doesn't exist.")
	}
	if !h.Connected(nodeID) {
		return domain.Unavailable("runner", "%s isn't connected right now; it re-checks its providers when it reconnects.", n.Name)
	}
	h.sendDirect(nodeID, protocol.Frame{Type: protocol.CmdProbe, ID: domain.NewID()})
	return nil
}

// RemoveWorkspace deletes one workspace on a connected machine at the
// owner's explicit request (A24). Open work's workspace and one in use are
// refused; one that would lose unpublished or uncommitted work needs Force.
// It waits for the machine to confirm.
func (h *Hub) RemoveWorkspace(ctx context.Context, userID, nodeID, name string, req protocol.CleanupWorkspaceRequest) (protocol.Node, error) {
	n, err := store.GetNode(ctx, h.st.R(), nodeID)
	if err != nil || n.RevokedAt != nil {
		return protocol.Node{}, domain.NotFound("That machine doesn't exist.")
	}
	var ws *protocol.NodeWorkspace
	for i := range n.Workspaces {
		if n.Workspaces[i].Name == name {
			ws = &n.Workspaces[i]
		}
	}
	if ws == nil {
		return protocol.Node{}, domain.NotFound("%s has no workspace called %s (as of its last report).", n.Name, name)
	}
	if req.Confirm != name {
		return protocol.Node{}, domain.Invalid("Confirm by repeating the workspace name.")
	}
	if ws.Blocked != "" {
		return protocol.Node{}, domain.Conflict("%s can't be removed: %s.", name, ws.Blocked)
	}
	if !ws.Published && !req.Force {
		loses := "commits that were never published"
		if ws.Changes > 0 {
			loses = fmt.Sprintf("%d uncommitted changes", ws.Changes)
		}
		return protocol.Node{}, domain.Conflict("Removing %s loses %s. Confirm that this may be lost.", name, loses)
	}
	if !h.Connected(nodeID) {
		return protocol.Node{}, domain.Unavailable("runner", "%s isn't connected; workspaces are removed by the machine itself.", n.Name)
	}
	cmdID := "cleanup:" + domain.NewID()
	wait := make(chan protocol.CommandAck, 1)
	h.ackWaiters.Store(nodeID+"|"+cmdID, wait)
	defer h.ackWaiters.Delete(nodeID + "|" + cmdID)
	b, _ := json.Marshal(protocol.CleanupWorkspace{Workspace: name, Force: req.Force})
	h.sendDirect(nodeID, protocol.Frame{Type: protocol.CmdCleanup, ID: cmdID, Payload: b})
	var ack protocol.CommandAck
	select {
	case ack = <-wait:
	case <-time.After(60 * time.Second):
		return protocol.Node{}, domain.Unavailable("runner", "%s didn't confirm in time; check its workspaces again shortly.", n.Name)
	case <-ctx.Done():
		return protocol.Node{}, ctx.Err()
	}
	result, detail := "ok", name
	if !ack.OK {
		result, detail = "refused", name+": "+ack.Error
	}
	if req.Force {
		detail += " (forced)"
	}
	_ = h.do(ctx, func(t *txn) error {
		return t.audit(userActor(userID), "owner", "node.workspace.remove", nodeID, result, detail)
	})
	if !ack.OK {
		return protocol.Node{}, domain.Conflict("%s refused: %s.", n.Name, ack.Error)
	}
	// The machine re-reports its workspaces; until then, drop this one.
	err = h.do(ctx, func(t *txn) error {
		cur, err := store.GetNode(ctx, t.tx, nodeID)
		if err != nil {
			return err
		}
		keep := []protocol.WorkspaceInfo{}
		for _, w := range cur.RawWorkspaces {
			if w.Name != name {
				keep = append(keep, w)
			}
		}
		if err := store.SetNodeWorkspaces(ctx, t.tx, nodeID, keep); err != nil {
			return err
		}
		return h.emitNode(ctx, t, nodeID)
	})
	if err != nil {
		return protocol.Node{}, err
	}
	out, err := store.GetNode(ctx, h.st.R(), nodeID)
	return out.Node, err
}

func (h *Hub) StopNodeWork(ctx context.Context, userID, nodeID string) error {
	runs, err := store.RunsOnNode(ctx, h.st.R(), nodeID)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if _, err := h.CancelJob(ctx, userID, r.JobID, protocol.CancelJobRequest{Reason: "Stopped from Machines"}); err != nil {
			return err
		}
	}
	return nil
}

// RevokeNode revokes a machine's credential. It cannot regain authority by
// replaying its queue; its in-flight runs become unknown until reconciled.
func (h *Hub) RevokeNode(ctx context.Context, userID, nodeID string) error {
	err := h.do(ctx, func(t *txn) error {
		if _, err := store.GetNode(ctx, t.tx, nodeID); err != nil {
			return domain.NotFound("That machine doesn't exist.")
		}
		if err := store.RevokeNode(ctx, t.tx, nodeID); err != nil {
			return err
		}
		runs, _ := store.RunsOnNode(ctx, t.tx, nodeID)
		for _, r := range runs {
			if domain.RunHoldsLease(r.State) {
				if err := h.applyTerminal(ctx, t, r, protocol.RunTerminal{Outcome: protocol.OutcomeUnknown, Error: "machine credential revoked"}); err != nil {
					return err
				}
			}
		}
		if err := t.audit(userActor(userID), "owner", "node.revoke", nodeID, "ok", ""); err != nil {
			return err
		}
		return h.emitNode(ctx, t, nodeID)
	})
	if err == nil {
		if c := h.nodes.get(nodeID); c != nil {
			_ = c.send(protocol.Frame{Type: protocol.CmdRevoke, ID: domain.NewID()})
			if c.CloseFn != nil {
				c.CloseFn("credential revoked")
			}
			h.nodes.remove(c)
		}
	}
	return err
}

// Diagnostics reports local operating health.
func (h *Hub) Diagnostics(ctx context.Context) (protocol.Diagnostics, error) {
	q := h.st.R()
	d := protocol.Diagnostics{Version: h.cfg.Version, DataDir: h.cfg.DataDir, SSEClients: h.bus.Subscribers()}
	if fi, err := os.Stat(h.st.Path()); err == nil {
		d.DBSizeBytes = fi.Size()
	}
	d.ArtifactBytes = h.artifacts.Size()
	d.DiskFreeBytes = diskFree(h.cfg.DataDir)
	_ = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE state = 'created'`).Scan(&d.QueueDepth)
	_ = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE state IN ('offered','preparing','running','awaiting_input','stopping')`).Scan(&d.ActiveRuns)
	d.PendingOutbox, _ = store.CountOutbox(ctx, q)
	_ = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM approvals WHERE status = 'pending'`).Scan(&d.PendingApprovals)
	_ = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE state = 'failed' AND ended_at >= ?`, store.TS(h.now().Add(-24*time.Hour))).Scan(&d.FailedRuns24h)
	if v, ok, _ := store.Setting(ctx, q, "last_backup_at"); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			d.LastBackupAt = &t
		}
	}
	nodes, _ := h.ListNodes(ctx)
	d.Nodes = nodes
	integ := store.IntegrityCheck(ctx, q)
	d.Checks = append(d.Checks, protocol.HealthCheck{Name: "database integrity", OK: integ == nil, Detail: errText(integ, "ok")})
	d.Checks = append(d.Checks, protocol.HealthCheck{Name: "disk space", OK: d.DiskFreeBytes == 0 || d.DiskFreeBytes > 2<<30,
		Detail: humanBytes(d.DiskFreeBytes) + " free on the hub data volume"})
	d.Checks = append(d.Checks, protocol.HealthCheck{Name: "outbox", OK: d.PendingOutbox < 100, Detail: itoa(d.PendingOutbox) + " commands awaiting delivery"})
	online := 0
	for _, n := range nodes {
		if n.Status == protocol.NodeOnline {
			online++
		}
	}
	d.Checks = append(d.Checks, protocol.HealthCheck{Name: "machines", OK: online > 0, Detail: itoa(online) + " of " + itoa(len(nodes)) + " connected"})
	return d, nil
}

// DiagnosticBundle assembles the opt-in troubleshooting export. The caller
// previews it before saving; nothing is sent anywhere.
func (h *Hub) DiagnosticBundle(ctx context.Context) (protocol.DiagnosticBundle, error) {
	q := h.st.R()
	d, err := h.Diagnostics(ctx)
	if err != nil {
		return protocol.DiagnosticBundle{}, err
	}
	b := protocol.DiagnosticBundle{GeneratedAt: h.now().UTC(), HubVersion: h.cfg.Version, GoVersion: runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH, Health: d.Checks, Counts: map[string]int{},
		Work: map[string]int{"queued": d.QueueDepth, "active": d.ActiveRuns, "failedLast24h": d.FailedRuns24h,
			"pendingApprovals": d.PendingApprovals, "pendingOutbox": d.PendingOutbox, "openWindows": d.SSEClients},
		Machines: []protocol.DiagnosticNode{}, RecentFailures: []protocol.DiagnosticFailure{},
		Excluded: []string{"messages and threads", "prompts and model output", "repository contents and diffs", "account names and emails",
			"hostnames and fingerprints", "tokens, keys, and passwords (failure reasons are also redacted)"}}
	b.SchemaVersion, _ = h.st.SchemaVersion(ctx)
	for _, t := range []string{"rooms", "engineers", "projects", "messages", "jobs", "runs", "reviews", "decisions", "artifacts", "approvals"} {
		var n int
		_ = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n)
		b.Counts[t] = n
	}
	names := map[string]string{}
	for _, n := range d.Nodes {
		names[n.ID] = n.Name
		dn := protocol.DiagnosticNode{Name: n.Name, Status: n.Status, Platform: n.OS + "/" + n.Arch, RunnerVersion: n.RunnerVersion,
			LastSeenAt: n.LastSeenAt, Providers: []protocol.DiagnosticProvider{}}
		for _, p := range n.Providers {
			dn.Providers = append(dn.Providers, protocol.DiagnosticProvider{Provider: p.Provider, Version: p.Version, Tested: p.Tested,
				AuthState: p.AuthState, Billing: p.Billing})
		}
		b.Machines = append(b.Machines, dn)
	}
	rows, err := q.QueryContext(ctx, `SELECT COALESCE(ended_at, created_at), provider, COALESCE(node_id, ''), state, terminal_reason
		FROM runs WHERE state IN ('failed', 'unknown') ORDER BY COALESCE(ended_at, created_at) DESC LIMIT 20`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var at, prov, node, state, reason string
			if rows.Scan(&at, &prov, &node, &state, &reason) != nil {
				continue
			}
			b.RecentFailures = append(b.RecentFailures, protocol.DiagnosticFailure{At: store.ParseTS(at), Provider: prov,
				Machine: names[node], State: state, Reason: truncate(redact.String(reason), 300)})
		}
	}
	return b, nil
}

func errText(err error, ok string) string {
	if err == nil {
		return ok
	}
	return err.Error()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return itoa(int(n)) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strings.TrimRight(strings.TrimRight(ftoa(float64(n)/float64(div)), "0"), ".") + " " + string("KMGTPE"[exp]) + "B"
}

func ftoa(f float64) string {
	s := itoa(int(f*10 + 0.5))
	if len(s) == 1 {
		return "0." + s
	}
	return s[:len(s)-1] + "." + s[len(s)-1:]
}
