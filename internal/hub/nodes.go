package hub

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/events"
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
	en.Command = "yip runner pair --hub " + h.cfg.RunnerURL + " --fingerprint " + en.HubFingerprint + " --name " + shellQuote(name) + " --token " + token
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
