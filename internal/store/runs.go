package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/binbandit/yip/protocol"
)

// RunRow is a run with hub-internal fields.
type RunRow struct {
	protocol.Run
	ExecutionProfile string
	Manifest         json.RawMessage
	ScopeFingerprint string
	FinalText        string
	LastSeq          int64
	OfferCommandID   string
	PostedReply      bool
	RetryAt          *time.Time
	UnconfirmedSince *time.Time
}

const runCols = `id, job_id, attempt, engineer_id, engineer_version_id, COALESCE(node_id, ''), provider, model, profile_id, mode, execution_profile,
	state, lease_epoch, lease_expires_at, heartbeat_at, vendor_session_id, dest_room_id, COALESCE(dest_thread_id, ''), COALESCE(dest_message_id, ''),
	cause_id, root_request_id, COALESCE(previous_run_id, ''), branch, base_rev, result_rev, terminal_reason, final_text, last_activity,
	last_activity_at, manifest, scope_fingerprint, usage, last_seq, COALESCE(offer_command_id, ''), posted_reply, retry_at, unconfirmed_since,
	created_at, started_at, ended_at`

func scanRun(s scanner) (RunRow, error) {
	var r RunRow
	var leaseExp, hb, lastAt, usage, retryAt, unconf, started, ended sql.NullString
	var manifest, created string
	var posted int
	err := s.Scan(&r.ID, &r.JobID, &r.Attempt, &r.EngineerID, &r.EngineerVersionID, &r.NodeID, &r.Provider, &r.Model, &r.ProfileID,
		&r.Mode, &r.ExecutionProfile, &r.State, &r.LeaseEpoch, &leaseExp, &hb, &r.VendorSessionID, &r.Destination.RoomID,
		&r.Destination.ThreadID, &r.Destination.MessageID, &r.CauseID, &r.RootRequestID, &r.PreviousRunID, &r.Branch, &r.BaseRev,
		&r.ResultRev, &r.TerminalReason, &r.FinalText, &r.LastActivity, &lastAt, &manifest, &r.ScopeFingerprint, &usage, &r.LastSeq,
		&r.OfferCommandID, &posted, &retryAt, &unconf, &created, &started, &ended)
	r.LeaseExpiresAt, r.HeartbeatAt, r.LastActivityAt = parseTSP(leaseExp), parseTSP(hb), parseTSP(lastAt)
	r.RetryAt, r.UnconfirmedSince = parseTSP(retryAt), parseTSP(unconf)
	r.StartedAt, r.EndedAt = parseTSP(started), parseTSP(ended)
	r.CreatedAt = parseTS(created)
	r.Manifest = json.RawMessage(manifest)
	r.PostedReply = posted == 1
	if usage.Valid && usage.String != "" {
		r.Usage = &protocol.Usage{}
		unjs(usage.String, r.Usage)
	}
	return r, err
}

func InsertRun(ctx context.Context, q Q, r RunRow) error {
	now := ts(nowUTC())
	_, err := q.ExecContext(ctx, `INSERT INTO runs(id, job_id, attempt, engineer_id, engineer_version_id, provider, model, profile_id, mode,
		execution_profile, state, lease_epoch, dest_room_id, dest_thread_id, dest_message_id, cause_id, root_request_id, previous_run_id,
		branch, base_rev, manifest, scope_fingerprint, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.JobID, r.Attempt, r.EngineerID, r.EngineerVersionID, r.Provider, r.Model, r.ProfileID, r.Mode, r.ExecutionProfile,
		string(r.State), r.Destination.RoomID, nullStr(r.Destination.ThreadID), nullStr(r.Destination.MessageID), r.CauseID,
		r.RootRequestID, nullStr(r.PreviousRunID), r.Branch, r.BaseRev, string(orEmptyJSON(r.Manifest)), r.ScopeFingerprint, now, now)
	return err
}

func orEmptyJSON(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return b
}

func GetRun(ctx context.Context, q Q, id string) (RunRow, error) {
	r, err := scanRun(q.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	return r, notFound(err)
}

func ListJobRuns(ctx context.Context, q Q, jobID string) ([]RunRow, error) {
	return list(ctx, q, scanRun, `SELECT `+runCols+` FROM runs WHERE job_id = ? ORDER BY attempt`, jobID)
}

func NextAttempt(ctx context.Context, q Q, jobID string) (int, error) {
	var n sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT MAX(attempt) FROM runs WHERE job_id = ?`, jobID).Scan(&n)
	return int(n.Int64) + 1, err
}

// RunsInStates lists runs in any of the given states, oldest first.
func RunsInStates(ctx context.Context, q Q, states ...protocol.RunState) ([]RunRow, error) {
	ph := make([]string, len(states))
	args := make([]any, len(states))
	for i, s := range states {
		ph[i], args[i] = "?", string(s)
	}
	return list(ctx, q, scanRun, `SELECT `+runCols+` FROM runs WHERE state IN (`+strings.Join(ph, ",")+`) ORDER BY created_at`, args...)
}

// RunsOnNode lists lease-holding runs assigned to a node.
func RunsOnNode(ctx context.Context, q Q, nodeID string) ([]RunRow, error) {
	return list(ctx, q, scanRun, `SELECT `+runCols+` FROM runs WHERE node_id = ?
		AND state IN ('offered','preparing','running','awaiting_input','stopping','unknown') ORDER BY created_at`, nodeID)
}

// ActiveRunForJob returns the job's run that is not yet terminal, if any.
func ActiveRunForJob(ctx context.Context, q Q, jobID string) (RunRow, error) {
	r, err := scanRun(q.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE job_id = ?
		AND state IN ('created','offered','preparing','running','awaiting_input','stopping','unknown') ORDER BY attempt DESC LIMIT 1`, jobID))
	return r, notFound(err)
}

// CountActiveRuns counts non-terminal runs matching optional filters.
func CountActiveRuns(ctx context.Context, q Q, engineerID, provider, profileID string, leaseOnly bool) (int, error) {
	states := `'created','offered','preparing','running','awaiting_input','stopping'`
	if leaseOnly {
		states = `'offered','preparing','running','awaiting_input','stopping'`
	}
	query := `SELECT COUNT(*) FROM runs WHERE state IN (` + states + `)`
	var args []any
	if engineerID != "" {
		query += ` AND engineer_id = ?`
		args = append(args, engineerID)
	}
	if provider != "" {
		query += ` AND provider = ?`
		args = append(args, provider)
	}
	if profileID != "" {
		query += ` AND profile_id = ?`
		args = append(args, profileID)
	}
	var n int
	err := q.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// SetRunState changes state; epoch guards stale runner reports.
func SetRunState(ctx context.Context, q Q, id string, state protocol.RunState, reason string) error {
	now := nowUTC()
	var ended any
	if state == protocol.RunSucceeded || state == protocol.RunFailed || state == protocol.RunCancelled {
		ended = ts(now)
	}
	var started any
	if state == protocol.RunRunning {
		started = ts(now)
	}
	var unconfirmed any
	if state == protocol.RunUnknown {
		unconfirmed = ts(now)
	}
	_, err := q.ExecContext(ctx, `UPDATE runs SET state = ?, terminal_reason = CASE WHEN ? = '' THEN terminal_reason ELSE ? END,
		ended_at = COALESCE(?, ended_at), started_at = COALESCE(started_at, ?), unconfirmed_since = CASE WHEN ? IS NULL THEN unconfirmed_since ELSE ? END,
		updated_at = ? WHERE id = ?`, string(state), reason, reason, ended, started, unconfirmed, unconfirmed, ts(now), id)
	return err
}

// OfferRun assigns a run to a node under a new lease epoch.
func OfferRun(ctx context.Context, q Q, id, nodeID string, epoch int64, expires time.Time, commandID, manifest string) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET state = 'offered', node_id = ?, lease_epoch = ?, lease_expires_at = ?, offer_command_id = ?,
		manifest = ?, updated_at = ? WHERE id = ?`, nodeID, epoch, ts(expires), commandID, manifest, ts(nowUTC()), id)
	return err
}

// ResetOffer returns an unacknowledged offer to created (it never started).
func ResetOffer(ctx context.Context, q Q, id string) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET state = 'created', node_id = NULL, lease_expires_at = NULL, updated_at = ? WHERE id = ? AND state = 'offered'`,
		ts(nowUTC()), id)
	return err
}

func RenewLease(ctx context.Context, q Q, id string, epoch int64, expires, heartbeat time.Time) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE runs SET lease_expires_at = ?, heartbeat_at = ? WHERE id = ? AND lease_epoch = ?
		AND state IN ('offered','preparing','running','awaiting_input','stopping')`, ts(expires), ts(heartbeat), id, epoch)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func SetRunActivity(ctx context.Context, q Q, id, text string, at time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET last_activity = ?, last_activity_at = ?, updated_at = ? WHERE id = ?`, text, ts(at), ts(at), id)
	return err
}

func SetRunVendorSession(ctx context.Context, q Q, id, vendorID string) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET vendor_session_id = ? WHERE id = ?`, vendorID, id)
	return err
}

func SetRunResult(ctx context.Context, q Q, id, finalText, resultRev string, usage *protocol.Usage) error {
	var u any
	if usage != nil {
		u = js(usage)
	}
	_, err := q.ExecContext(ctx, `UPDATE runs SET final_text = ?, result_rev = CASE WHEN ? = '' THEN result_rev ELSE ? END,
		usage = COALESCE(?, usage), updated_at = ? WHERE id = ?`, finalText, resultRev, resultRev, u, ts(nowUTC()), id)
	return err
}

func SetRunRevision(ctx context.Context, q Q, id, branch, base, head string) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET branch = ?, base_rev = CASE WHEN base_rev = '' THEN ? ELSE base_rev END, result_rev = ? WHERE id = ?`,
		branch, base, head, id)
	return err
}

func SetRunPostedReply(ctx context.Context, q Q, id string) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET posted_reply = 1 WHERE id = ?`, id)
	return err
}

func SetRunRetryAt(ctx context.Context, q Q, id string, at *time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE runs SET retry_at = ? WHERE id = ?`, tsp(at), id)
	return err
}

// ---- run events ----

// InsertRunEvent stores one runner event. The (run, seq) key makes replays
// of the same producer event harmless; it reports whether the row was new.
func InsertRunEvent(ctx context.Context, q Q, runID string, e protocol.RunEvent) (bool, error) {
	var data any
	if len(e.Data) > 0 {
		data = string(e.Data)
	}
	res, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO run_events(run_id, seq, kind, text, tool, data, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		runID, e.Seq, e.Kind, e.Text, e.Tool, data, ts(e.At))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		_, err = q.ExecContext(ctx, `UPDATE runs SET last_seq = MAX(last_seq, ?) WHERE id = ?`, e.Seq, runID)
	}
	return n == 1, err
}

func ListRunEvents(ctx context.Context, q Q, runID string, limit int) ([]protocol.RunActivity, error) {
	return list(ctx, q, func(s scanner) (protocol.RunActivity, error) {
		var a protocol.RunActivity
		var data sql.NullString
		var at string
		err := s.Scan(&a.RunID, &a.Seq, &a.Kind, &a.Text, &a.Tool, &data, &at)
		if data.Valid {
			a.Data = json.RawMessage(data.String)
		}
		a.At = parseTS(at)
		return a, err
	}, `SELECT run_id, seq, kind, text, tool, data, at FROM run_events WHERE run_id = ? AND kind <> 'message_delta' ORDER BY seq LIMIT ?`, runID, limit)
}

// ---- provider sessions ----

type ProviderSession struct {
	ID               string
	EngineerID       string
	Provider         string
	ProfileID        string
	ScopeFingerprint string
	ContextKey       string
	NodeID           string
	Workspace        string
	VendorSessionID  string
}

func UpsertProviderSession(ctx context.Context, q Q, s ProviderSession) error {
	now := ts(nowUTC())
	_, err := q.ExecContext(ctx, `INSERT INTO provider_sessions(id, engineer_id, provider, profile_id, scope_fingerprint, context_key, node_id,
		workspace, vendor_session_id, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.EngineerID, s.Provider, s.ProfileID, s.ScopeFingerprint, s.ContextKey, s.NodeID, s.Workspace, s.VendorSessionID, now, now)
	return err
}

// FindProviderSession returns a reusable vendor session only when every
// boundary matches: engineer, provider account, context, node, and the
// security-scope fingerprint.
func FindProviderSession(ctx context.Context, q Q, engineerID, provider, profileID, contextKey, nodeID, fingerprint string) (ProviderSession, error) {
	var s ProviderSession
	err := q.QueryRowContext(ctx, `SELECT id, engineer_id, provider, profile_id, scope_fingerprint, context_key, node_id, workspace, vendor_session_id
		FROM provider_sessions WHERE engineer_id = ? AND provider = ? AND profile_id = ? AND context_key = ? AND node_id = ?
		AND scope_fingerprint = ? AND invalidated_at IS NULL ORDER BY last_used_at DESC LIMIT 1`,
		engineerID, provider, profileID, contextKey, nodeID, fingerprint).Scan(&s.ID, &s.EngineerID, &s.Provider, &s.ProfileID,
		&s.ScopeFingerprint, &s.ContextKey, &s.NodeID, &s.Workspace, &s.VendorSessionID)
	return s, notFound(err)
}

// InvalidateProviderSessions prevents reuse after an access change.
// InvalidateProviderSessionFor retires the one session a later attempt would
// resume (same engineer, provider, conversation and machine).
func InvalidateProviderSessionFor(ctx context.Context, q Q, engineerID, provider, contextKey, nodeID, why string) error {
	_, err := q.ExecContext(ctx, `UPDATE provider_sessions SET invalidated_at = ?, invalidated_why = ?
		WHERE invalidated_at IS NULL AND engineer_id = ? AND provider = ? AND context_key = ? AND node_id = ?`,
		ts(nowUTC()), why, engineerID, provider, contextKey, nodeID)
	return err
}

func InvalidateProviderSessions(ctx context.Context, q Q, engineerID, why string) error {
	query := `UPDATE provider_sessions SET invalidated_at = ?, invalidated_why = ? WHERE invalidated_at IS NULL`
	args := []any{ts(nowUTC()), why}
	if engineerID != "" {
		query += ` AND engineer_id = ?`
		args = append(args, engineerID)
	}
	_, err := q.ExecContext(ctx, query, args...)
	return err
}
