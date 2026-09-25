package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/binbandit/yip/protocol"
)

// NodeRow is a machine with its certificate identity.
type NodeRow struct {
	protocol.Node
	CertSerial string
}

const nodeCols = `id, name, hostname, os, arch, fingerprint, cert_serial, status, draining, revoked_at, last_seen_at, capacity, profiles,
	toolchains, runner_version, service_state, last_activity, created_at`

func scanNode(s scanner) (NodeRow, error) {
	var n NodeRow
	var draining int
	var revoked, seen sql.NullString
	var capacity, profiles, toolchains, created string
	err := s.Scan(&n.ID, &n.Name, &n.Hostname, &n.OS, &n.Arch, &n.Fingerprint, &n.CertSerial, &n.Status, &draining, &revoked, &seen,
		&capacity, &profiles, &toolchains, &n.RunnerVersion, &n.ServiceState, &n.LastActivity, &created)
	n.Draining = draining == 1
	n.RevokedAt, n.LastSeenAt = parseTSP(revoked), parseTSP(seen)
	unjs(capacity, &n.Capacity)
	unjs(profiles, &n.Profiles)
	unjs(toolchains, &n.Toolchains)
	if n.Profiles == nil {
		n.Profiles = []protocol.ExecutionProfile{}
	}
	if n.Toolchains == nil {
		n.Toolchains = map[string]string{}
	}
	n.CreatedAt = parseTS(created)
	n.Providers, n.ActiveRunIDs = []protocol.ProviderInstallation{}, []string{}
	return n, err
}

func InsertNode(ctx context.Context, q Q, orgID string, n NodeRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO nodes(id, org_id, name, hostname, os, arch, fingerprint, cert_serial, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'offline', ?)`, n.ID, orgID, n.Name, n.Hostname, n.OS, n.Arch, n.Fingerprint, n.CertSerial, ts(n.CreatedAt))
	return err
}

func GetNode(ctx context.Context, q Q, id string) (NodeRow, error) {
	n, err := scanNode(q.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id))
	if err != nil {
		return n, notFound(err)
	}
	return n, fillNode(ctx, q, &n)
}

func ListNodes(ctx context.Context, q Q) ([]NodeRow, error) {
	ns, err := list(ctx, q, scanNode, `SELECT `+nodeCols+` FROM nodes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	for i := range ns {
		if err := fillNode(ctx, q, &ns[i]); err != nil {
			return nil, err
		}
	}
	return ns, nil
}

func fillNode(ctx context.Context, q Q, n *NodeRow) error {
	var err error
	if n.Providers, err = ListInstallations(ctx, q, n.ID); err != nil {
		return err
	}
	n.ActiveRunIDs, err = stringsCol(ctx, q, `SELECT id FROM runs WHERE node_id = ? AND state IN ('offered','preparing','running','awaiting_input','stopping')`, n.ID)
	return err
}

func SetNodeSeen(ctx context.Context, q Q, id, status string, at time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET status = ?, last_seen_at = ? WHERE id = ? AND revoked_at IS NULL`, status, ts(at), id)
	return err
}

func SetNodeStatus(ctx context.Context, q Q, id, status string) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET status = ? WHERE id = ? AND revoked_at IS NULL`, status, id)
	return err
}

func SetNodeCapabilities(ctx context.Context, q Q, id string, c protocol.RunnerCapabilities, hostname, runnerVersion string) error {
	capacity := protocol.NodeCapacity{Slots: c.Slots, CPUs: c.CPUs, MemMB: c.MemMB, DiskFreeMB: c.DiskFreeMB, DiskPressure: c.DiskFreeMB > 0 && c.DiskFreeMB < 2048}
	_, err := q.ExecContext(ctx, `UPDATE nodes SET os = ?, arch = ?, hostname = COALESCE(NULLIF(?, ''), hostname), capacity = ?, profiles = ?,
		toolchains = ?, runner_version = COALESCE(NULLIF(?, ''), runner_version), service_state = ? WHERE id = ?`, c.OS, c.Arch, hostname,
		js(capacity), js(c.Profiles), js(c.Toolchains), runnerVersion, c.ServiceState, id)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM provider_installations WHERE node_id = ?`, id); err != nil {
		return err
	}
	for _, p := range c.Providers {
		if _, err := q.ExecContext(ctx, `INSERT INTO provider_installations(node_id, provider, version, path, auth_state, auth_detail, account,
			billing, profile_id, capabilities, models, tested, tested_version, limitations, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, p.Provider, p.Version, p.Path, p.AuthState, p.AuthDetail, p.Account, p.Billing, p.ProfileID, js(p.Capabilities),
			js(p.Models), b2i(p.Tested), p.TestedVersion, js(strs(p.Limitations)), ts(nowUTC())); err != nil {
			return err
		}
	}
	return nil
}

func SetNodeDiskFree(ctx context.Context, q Q, id string, freeMB int64) error {
	var c protocol.NodeCapacity
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT capacity FROM nodes WHERE id = ?`, id).Scan(&raw); err != nil {
		return notFound(err)
	}
	unjs(raw, &c)
	c.DiskFreeMB = freeMB
	c.DiskPressure = freeMB > 0 && freeMB < 2048
	_, err := q.ExecContext(ctx, `UPDATE nodes SET capacity = ? WHERE id = ?`, js(c), id)
	return err
}

func SetNodeActivity(ctx context.Context, q Q, id, text string) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET last_activity = ? WHERE id = ?`, text, id)
	return err
}

func SetNodeDraining(ctx context.Context, q Q, id string, draining bool) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET draining = ? WHERE id = ?`, b2i(draining), id)
	return err
}

func RevokeNode(ctx context.Context, q Q, id string) error {
	_, err := q.ExecContext(ctx, `UPDATE nodes SET revoked_at = ?, status = 'revoked' WHERE id = ? AND revoked_at IS NULL`, ts(nowUTC()), id)
	return err
}

func ListInstallations(ctx context.Context, q Q, nodeID string) ([]protocol.ProviderInstallation, error) {
	return list(ctx, q, func(s scanner) (protocol.ProviderInstallation, error) {
		var p protocol.ProviderInstallation
		var caps, models, limits, updated string
		var tested int
		err := s.Scan(&p.Provider, &p.Version, &p.Path, &p.AuthState, &p.AuthDetail, &p.Account, &p.Billing, &p.ProfileID, &caps, &models,
			&tested, &p.TestedVersion, &limits, &updated)
		unjs(caps, &p.Capabilities)
		unjs(models, &p.Models)
		unjs(limits, &p.Limitations)
		if p.Models == nil {
			p.Models = []protocol.Model{}
		}
		p.Limitations = strs(p.Limitations)
		p.Tested, p.UpdatedAt = tested == 1, parseTS(updated)
		return p, err
	}, `SELECT provider, version, path, auth_state, auth_detail, account, billing, profile_id, capabilities, models, tested, tested_version,
		limitations, updated_at FROM provider_installations WHERE node_id = ? ORDER BY provider`, nodeID)
}

// ---- enrollments ----

func InsertEnrollment(ctx context.Context, q Q, orgID, id, name, tokenHash string, expires time.Time) error {
	_, err := q.ExecContext(ctx, `INSERT INTO node_enrollments(id, org_id, name, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, orgID, name, tokenHash, ts(nowUTC()), ts(expires))
	return err
}

// RedeemEnrollment consumes a single-use, unexpired enrollment token.
func RedeemEnrollment(ctx context.Context, q Q, tokenHash, nodeID string, now time.Time) (name string, ok bool, err error) {
	var id string
	err = q.QueryRowContext(ctx, `SELECT id, name FROM node_enrollments WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, ts(now)).Scan(&id, &name)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	res, err := q.ExecContext(ctx, `UPDATE node_enrollments SET used_at = ?, node_id = ? WHERE id = ? AND used_at IS NULL`, ts(now), nodeID, id)
	if err != nil {
		return "", false, err
	}
	n, _ := res.RowsAffected()
	return name, n == 1, nil
}

// ---- events ----

// EventRow is a stored event with its visibility scope.
type EventRow struct {
	protocol.Event
	Visibility  string
	ProducerKey string
}

// InsertEvent appends an event and returns its hub sequence. A repeated
// producer key is a duplicate delivery: nothing is written and dup is true.
func InsertEvent(ctx context.Context, q Q, e EventRow) (seq int64, dup bool, err error) {
	payload := "{}"
	if len(e.Payload) > 0 {
		payload = string(e.Payload)
	}
	res, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO events(id, org_id, type, actor_kind, actor_id, cause_id, root_request_id, room_id, thread_id,
		job_id, run_id, visibility, payload, producer_key, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.EventID, e.OrgID, e.Type, e.Actor.Kind, e.Actor.ID, nullStr(e.CauseID), nullStr(e.RootRequestID), nullStr(e.RoomID),
		nullStr(e.ThreadID), nullStr(e.JobID), nullStr(e.RunID), e.Visibility, payload, nullStr(e.ProducerKey), ts(e.OccurredAt))
	if err != nil {
		return 0, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, true, nil
	}
	seq, err = res.LastInsertId()
	return seq, false, err
}

const eventCols = `seq, id, org_id, type, actor_kind, actor_id, COALESCE(cause_id, ''), COALESCE(root_request_id, ''), COALESCE(room_id, ''),
	COALESCE(thread_id, ''), COALESCE(job_id, ''), COALESCE(run_id, ''), visibility, payload, occurred_at`

func scanEvent(s scanner) (EventRow, error) {
	var e EventRow
	var payload, at string
	err := s.Scan(&e.Sequence, &e.EventID, &e.OrgID, &e.Type, &e.Actor.Kind, &e.Actor.ID, &e.CauseID, &e.RootRequestID, &e.RoomID,
		&e.ThreadID, &e.JobID, &e.RunID, &e.Visibility, &payload, &at)
	e.SchemaVersion = protocol.SchemaVersion
	e.Payload = []byte(payload)
	e.OccurredAt = parseTS(at)
	return e, err
}

func EventsAfter(ctx context.Context, q Q, after int64, limit int) ([]EventRow, error) {
	return list(ctx, q, scanEvent, `SELECT `+eventCols+` FROM events WHERE seq > ? ORDER BY seq LIMIT ?`, after, limit)
}

func EventsForJob(ctx context.Context, q Q, jobID string, limit int) ([]EventRow, error) {
	return list(ctx, q, scanEvent, `SELECT `+eventCols+` FROM events WHERE job_id = ? ORDER BY seq LIMIT ?`, jobID, limit)
}

func EventsSince(ctx context.Context, q Q, since time.Time, types []string, limit int) ([]EventRow, error) {
	args := []any{ts(since)}
	for _, t := range types {
		args = append(args, t)
	}
	args = append(args, limit)
	return list(ctx, q, scanEvent, `SELECT `+eventCols+` FROM events WHERE occurred_at >= ? AND type IN `+InClause(len(types))+`
		ORDER BY seq DESC LIMIT ?`, args...)
}

func MaxEventSeq(ctx context.Context, q Q) (int64, error) {
	var n sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT MAX(seq) FROM events`).Scan(&n)
	return n.Int64, err
}

func MinEventSeq(ctx context.Context, q Q) (int64, error) {
	var n sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT MIN(seq) FROM events`).Scan(&n)
	return n.Int64, err
}

// ---- outbox ----

type OutboxItem struct {
	ID            string
	Kind          string
	NodeID        string
	RunID         string
	CommandID     string
	Frame         string
	Status        string
	Attempts      int
	NextAttemptAt time.Time
}

func InsertOutbox(ctx context.Context, q Q, o OutboxItem) error {
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO outbox(id, kind, node_id, run_id, command_id, frame, status, attempts, next_attempt_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`, o.ID, o.Kind, nullStr(o.NodeID), nullStr(o.RunID), o.CommandID, o.Frame,
		ts(o.NextAttemptAt), ts(nowUTC()))
	return err
}

// PendingOutbox returns deliverable items for a node (committed rows only).
func PendingOutbox(ctx context.Context, q Q, nodeID string, now time.Time, limit int) ([]OutboxItem, error) {
	return list(ctx, q, func(s scanner) (OutboxItem, error) {
		var o OutboxItem
		var next string
		err := s.Scan(&o.ID, &o.Kind, &o.NodeID, &o.RunID, &o.CommandID, &o.Frame, &o.Status, &o.Attempts, &next)
		o.NextAttemptAt = parseTS(next)
		return o, err
	}, `SELECT id, kind, COALESCE(node_id, ''), COALESCE(run_id, ''), command_id, frame, status, attempts, next_attempt_at FROM outbox
		WHERE node_id = ? AND status IN ('pending','sent') AND next_attempt_at <= ? ORDER BY created_at LIMIT ?`, nodeID, ts(now), limit)
}

func MarkOutboxSent(ctx context.Context, q Q, id string, retryAt time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE outbox SET status = 'sent', attempts = attempts + 1, sent_at = ?, next_attempt_at = ? WHERE id = ? AND status IN ('pending','sent')`,
		ts(nowUTC()), ts(retryAt), id)
	return err
}

func AckOutbox(ctx context.Context, q Q, commandID string) error {
	_, err := q.ExecContext(ctx, `UPDATE outbox SET status = 'acked', acked_at = ? WHERE command_id = ? AND status IN ('pending','sent')`, ts(nowUTC()), commandID)
	return err
}

// CancelOutboxForRun stops redelivery of commands for a run (e.g. an offer
// superseded by a new epoch).
func CancelOutboxForRun(ctx context.Context, q Q, runID string, commandPrefix string) error {
	_, err := q.ExecContext(ctx, `UPDATE outbox SET status = 'cancelled' WHERE run_id = ? AND status IN ('pending','sent') AND command_id LIKE ?`,
		runID, commandPrefix+"%")
	return err
}

func CountOutbox(ctx context.Context, q Q) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE status IN ('pending','sent')`).Scan(&n)
	return n, err
}
