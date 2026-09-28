package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/protocol"
)

// ---- checks ----

func InsertCheck(ctx context.Context, q Q, c protocol.Check) error {
	_, err := q.ExecContext(ctx, `INSERT INTO checks(id, job_id, run_id, node_id, name, command, exit_code, passed, revision, log_artifact_id,
		duration_ms, summary, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, c.ID, c.JobID, c.RunID, c.NodeID, c.Name, c.Command,
		c.ExitCode, b2i(c.Passed), c.Revision, nullStr(c.LogArtifactID), c.DurationMs, c.Summary, ts(c.CreatedAt))
	return err
}

func ListChecks(ctx context.Context, q Q, jobID string) ([]protocol.Check, error) {
	return list(ctx, q, func(s scanner) (protocol.Check, error) {
		var c protocol.Check
		var passed int
		var created string
		err := s.Scan(&c.ID, &c.JobID, &c.RunID, &c.NodeID, &c.Name, &c.Command, &c.ExitCode, &passed, &c.Revision, &c.LogArtifactID,
			&c.DurationMs, &c.Summary, &created)
		c.Passed, c.CreatedAt = passed == 1, parseTS(created)
		return c, err
	}, `SELECT id, job_id, run_id, node_id, name, command, exit_code, passed, revision, COALESCE(log_artifact_id, ''), duration_ms, summary, created_at
		FROM checks WHERE job_id = ? ORDER BY created_at`, jobID)
}

// ---- artifacts ----

const artifactCols = `id, hash, name, content_type, size, kind, COALESCE(run_id, ''), COALESCE(job_id, ''), COALESCE(room_id, ''),
	COALESCE(project_id, ''), revision, created_at`

func scanArtifact(s scanner) (protocol.Artifact, error) {
	var a protocol.Artifact
	var created string
	err := s.Scan(&a.ID, &a.Hash, &a.Name, &a.ContentType, &a.Size, &a.Kind, &a.RunID, &a.JobID, &a.RoomID, &a.ProjectID, &a.Revision, &created)
	a.CreatedAt = parseTS(created)
	return a, err
}

// InsertArtifact records an artifact only after the hub verified its hash and size.
func InsertArtifact(ctx context.Context, q Q, orgID string, a protocol.Artifact) error {
	_, err := q.ExecContext(ctx, `INSERT INTO artifacts(id, org_id, hash, name, content_type, size, kind, run_id, job_id, room_id, project_id,
		revision, verified, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`, a.ID, orgID, a.Hash, a.Name, a.ContentType, a.Size,
		a.Kind, nullStr(a.RunID), nullStr(a.JobID), nullStr(a.RoomID), nullStr(a.ProjectID), a.Revision, ts(a.CreatedAt))
	return err
}

func GetArtifact(ctx context.Context, q Q, id string) (protocol.Artifact, error) {
	a, err := scanArtifact(q.QueryRowContext(ctx, `SELECT `+artifactCols+` FROM artifacts WHERE id = ? AND verified = 1`, id))
	return a, notFound(err)
}

func ListJobArtifacts(ctx context.Context, q Q, jobID string) ([]protocol.Artifact, error) {
	return list(ctx, q, scanArtifact, `SELECT `+artifactCols+` FROM artifacts WHERE job_id = ? AND verified = 1 ORDER BY created_at`, jobID)
}

func ListArtifacts(ctx context.Context, q Q) ([]protocol.Artifact, error) {
	return list(ctx, q, scanArtifact, `SELECT `+artifactCols+` FROM artifacts WHERE verified = 1 ORDER BY created_at`)
}

// ---- checkpoints ----

func InsertCheckpoint(ctx context.Context, q Q, id, runID, jobID, nodeID string, c protocol.CheckpointInfo) error {
	_, err := q.ExecContext(ctx, `INSERT INTO checkpoints(id, run_id, job_id, node_id, head, artifact_id, dirty, untracked, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, runID, jobID, nodeID, c.Head, nullStr(c.ArtifactID), b2i(c.Dirty), c.Untracked, ts(nowUTC()))
	return err
}

// ---- approvals ----

type ApprovalRow struct {
	protocol.Approval
	RequestID string
}

const approvalCols = `id, run_id, job_id, engineer_id, request_id, action, args_digest, scope, target_rev, status, expires_at,
	decided_by_kind, decided_by_id, decided_at, source_room_id, COALESCE(source_thread_id, ''), COALESCE(message_id, ''), version, created_at`

func scanApproval(s scanner) (ApprovalRow, error) {
	var a ApprovalRow
	var action, expires, created string
	var dk, did, dat sql.NullString
	err := s.Scan(&a.ID, &a.RunID, &a.JobID, &a.EngineerID, &a.RequestID, &action, &a.ArgsDigest, &a.Scope, &a.TargetRev, &a.Status, &expires,
		&dk, &did, &dat, &a.Source.RoomID, &a.Source.ThreadID, &a.Source.MessageID, &a.Version, &created)
	unjs(action, &a.Action)
	a.ExpiresAt, a.CreatedAt, a.DecidedAt = parseTS(expires), parseTS(created), parseTSP(dat)
	if dk.Valid {
		a.DecidedBy = &protocol.Actor{Kind: dk.String, ID: did.String}
	}
	return a, err
}

func InsertApproval(ctx context.Context, q Q, orgID string, a ApprovalRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO approvals(id, org_id, run_id, job_id, engineer_id, request_id, action, args_digest, scope, target_rev,
		status, expires_at, source_room_id, source_thread_id, message_id, version, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		a.ID, orgID, a.RunID, a.JobID, a.EngineerID, a.RequestID, js(a.Action), a.ArgsDigest, a.Scope, a.TargetRev, a.Status, ts(a.ExpiresAt),
		a.Source.RoomID, nullStr(a.Source.ThreadID), nullStr(a.Source.MessageID), ts(a.CreatedAt))
	return err
}

func GetApproval(ctx context.Context, q Q, id string) (ApprovalRow, error) {
	a, err := scanApproval(q.QueryRowContext(ctx, `SELECT `+approvalCols+` FROM approvals WHERE id = ?`, id))
	return a, notFound(err)
}

func GetApprovalByRequest(ctx context.Context, q Q, runID, requestID string) (ApprovalRow, error) {
	a, err := scanApproval(q.QueryRowContext(ctx, `SELECT `+approvalCols+` FROM approvals WHERE run_id = ? AND request_id = ?`, runID, requestID))
	return a, notFound(err)
}

func ListApprovals(ctx context.Context, q Q, where string, args ...any) ([]ApprovalRow, error) {
	return list(ctx, q, scanApproval, `SELECT `+approvalCols+` FROM approvals`+whereClause(where)+` ORDER BY created_at`, args...)
}

// DecideApproval applies an exact-action decision under an optimistic version.
func DecideApproval(ctx context.Context, q Q, id string, expectVersion int64, status string, by protocol.Actor, now time.Time) (bool, error) {
	return oneRow(q.ExecContext(ctx, `UPDATE approvals SET status = ?, decided_by_kind = ?, decided_by_id = ?, decided_at = ?, version = version + 1
		WHERE id = ? AND version = ? AND status = 'pending' AND expires_at > ?`, status, by.Kind, by.ID, ts(now), id, expectVersion, ts(now)))
}

func SetApprovalStatus(ctx context.Context, q Q, id, from, to string) (bool, error) {
	var consumed any
	if to == "consumed" {
		consumed = ts(nowUTC())
	}
	return oneRow(q.ExecContext(ctx, `UPDATE approvals SET status = ?, consumed_at = COALESCE(?, consumed_at), version = version + 1 WHERE id = ? AND status = ?`,
		to, consumed, id, from))
}

func SetApprovalMessage(ctx context.Context, q Q, id, messageID string) error {
	_, err := q.ExecContext(ctx, `UPDATE approvals SET message_id = ? WHERE id = ?`, messageID, id)
	return err
}

// ---- questions ----

const questionCols = `id, job_id, asker_id, recipient_kind, recipient_id, missing_fact, context_checked, dependent_step, continuing_with, status,
	source_room_id, COALESCE(source_thread_id, ''), message_id, COALESCE(answer_message_id, ''), created_at, answered_at`

func scanQuestion(s scanner) (protocol.Question, error) {
	var x protocol.Question
	var created string
	var answered sql.NullString
	err := s.Scan(&x.ID, &x.JobID, &x.AskerID, &x.Recipient.Kind, &x.Recipient.ID, &x.MissingFact, &x.ContextChecked, &x.DependentStep,
		&x.ContinuingWith, &x.Status, &x.Source.RoomID, &x.Source.ThreadID, &x.MessageID, &x.AnswerMessageID, &created, &answered)
	x.CreatedAt, x.AnsweredAt = parseTS(created), parseTSP(answered)
	x.Source.MessageID = x.MessageID
	return x, err
}

func InsertQuestion(ctx context.Context, q Q, orgID, runID string, x protocol.Question) error {
	_, err := q.ExecContext(ctx, `INSERT INTO questions(id, org_id, job_id, run_id, asker_id, recipient_kind, recipient_id, missing_fact, context_checked,
		dependent_step, continuing_with, status, source_room_id, source_thread_id, message_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.ID, orgID, x.JobID, nullStr(runID), x.AskerID, x.Recipient.Kind, x.Recipient.ID, x.MissingFact, x.ContextChecked, x.DependentStep,
		x.ContinuingWith, x.Status, x.Source.RoomID, nullStr(x.Source.ThreadID), x.MessageID, ts(x.CreatedAt))
	return err
}

func GetQuestion(ctx context.Context, q Q, id string) (protocol.Question, error) {
	x, err := scanQuestion(q.QueryRowContext(ctx, `SELECT `+questionCols+` FROM questions WHERE id = ?`, id))
	return x, notFound(err)
}

func ListQuestions(ctx context.Context, q Q, where string, args ...any) ([]protocol.Question, error) {
	return list(ctx, q, scanQuestion, `SELECT `+questionCols+` FROM questions`+whereClause(where)+` ORDER BY created_at`, args...)
}

// AnswerQuestion resolves an open question exactly once. Duplicate or late
// replies (after answer or cancellation) change nothing.
func AnswerQuestion(ctx context.Context, q Q, id, answerMessageID string, now time.Time) (bool, error) {
	return oneRow(q.ExecContext(ctx, `UPDATE questions SET status = 'answered', answer_message_id = ?, answered_at = ? WHERE id = ? AND status = 'open'`,
		answerMessageID, ts(now), id))
}

func CancelJobQuestions(ctx context.Context, q Q, jobID string) ([]string, error) {
	ids, err := stringsCol(ctx, q, `SELECT id FROM questions WHERE job_id = ? AND status = 'open'`, jobID)
	if err != nil {
		return nil, err
	}
	_, err = q.ExecContext(ctx, `UPDATE questions SET status = 'cancelled' WHERE job_id = ? AND status = 'open'`, jobID)
	return ids, err
}

// ---- pull requests ----

const prCols = `id, repo_id, COALESCE(job_id, ''), forge, host, owner, name, number, url, title, state, base_rev, head_rev, remote_author,
	viewer_actor, checks, merge, remote_reviews, last_synced_at`

func scanPR(s scanner) (protocol.PullRequest, error) {
	var p protocol.PullRequest
	var checks, merge, reviews string
	var synced sql.NullString
	err := s.Scan(&p.ID, &p.RepoID, &p.JobID, &p.Forge, &p.Host, &p.Owner, &p.Name, &p.Number, &p.URL, &p.Title, &p.State, &p.Base, &p.Head,
		&p.RemoteAuthor, &p.ViewerActor, &checks, &merge, &reviews, &synced)
	unjs(checks, &p.Checks)
	unjs(merge, &p.Merge)
	unjs(reviews, &p.RemoteReviews)
	if p.RemoteReviews == nil {
		p.RemoteReviews = []protocol.RemoteReview{}
	}
	p.Merge.Reasons = strs(p.Merge.Reasons)
	p.LastSyncedAt = parseTSP(synced)
	return p, err
}

func UpsertPR(ctx context.Context, q Q, orgID string, p protocol.PullRequest) (string, error) {
	var existing string
	err := q.QueryRowContext(ctx, `SELECT id FROM pull_requests WHERE forge = ? AND host = ? AND owner = ? AND name = ? AND number = ?`,
		p.Forge, p.Host, p.Owner, p.Name, p.Number).Scan(&existing)
	if err == nil {
		p.ID = existing
	} else if err != sql.ErrNoRows {
		return "", err
	} else if p.ID == "" {
		// Allocate only for a new remote identity. Existing IDs, including
		// legacy empty IDs, may already be referenced by immutable reviews.
		p.ID = domain.NewID()
	}
	_, err = q.ExecContext(ctx, `INSERT INTO pull_requests(id, org_id, repo_id, job_id, forge, host, owner, name, number, url, title, state, base_rev,
		head_rev, remote_author, viewer_actor, checks, merge, remote_reviews, last_synced_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET job_id = COALESCE(excluded.job_id, pull_requests.job_id), url = excluded.url, title = excluded.title,
			state = excluded.state, base_rev = excluded.base_rev, head_rev = excluded.head_rev, remote_author = excluded.remote_author,
			viewer_actor = excluded.viewer_actor, checks = excluded.checks, merge = excluded.merge, remote_reviews = excluded.remote_reviews,
			last_synced_at = excluded.last_synced_at`,
		p.ID, orgID, p.RepoID, nullStr(p.JobID), p.Forge, p.Host, p.Owner, p.Name, p.Number, p.URL, p.Title, p.State, p.Base, p.Head,
		p.RemoteAuthor, p.ViewerActor, js(p.Checks), js(p.Merge), js(p.RemoteReviews), tsp(p.LastSyncedAt), ts(nowUTC()))
	return p.ID, err
}

func GetPR(ctx context.Context, q Q, id string) (protocol.PullRequest, error) {
	p, err := scanPR(q.QueryRowContext(ctx, `SELECT `+prCols+` FROM pull_requests WHERE id = ?`, id))
	return p, notFound(err)
}

func ListPRs(ctx context.Context, q Q, where string, args ...any) ([]protocol.PullRequest, error) {
	return list(ctx, q, scanPR, `SELECT `+prCols+` FROM pull_requests`+whereClause(where)+` ORDER BY created_at`, args...)
}

// ---- forge deliveries ----

type ForgeDelivery struct {
	ID            string
	PRID          string
	Kind          string
	DedupeKey     string
	ExternalID    string
	Status        string
	EngineerID    string
	ReviewRoundID string
	Payload       string
	Error         string
}

// InsertForgeDelivery records an outgoing publication or inbound webhook.
// It reports false when the dedupe key already exists.
func InsertForgeDelivery(ctx context.Context, q Q, d ForgeDelivery) (bool, error) {
	return oneRow(q.ExecContext(ctx, `INSERT OR IGNORE INTO forge_deliveries(id, pr_id, kind, dedupe_key, external_id, status, engineer_id, review_round_id,
		payload, attempted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.ID, nullStr(d.PRID), d.Kind, d.DedupeKey, d.ExternalID, d.Status,
		nullStr(d.EngineerID), nullStr(d.ReviewRoundID), d.Payload, ts(nowUTC())))
}

func GetForgeDelivery(ctx context.Context, q Q, dedupeKey string) (ForgeDelivery, error) {
	var d ForgeDelivery
	err := q.QueryRowContext(ctx, `SELECT id, COALESCE(pr_id, ''), kind, dedupe_key, external_id, status, COALESCE(engineer_id, ''),
		COALESCE(review_round_id, ''), payload, error FROM forge_deliveries WHERE dedupe_key = ?`, dedupeKey).Scan(&d.ID, &d.PRID, &d.Kind,
		&d.DedupeKey, &d.ExternalID, &d.Status, &d.EngineerID, &d.ReviewRoundID, &d.Payload, &d.Error)
	return d, notFound(err)
}

func SetForgeDelivery(ctx context.Context, q Q, id, status, externalID, errText string) error {
	_, err := q.ExecContext(ctx, `UPDATE forge_deliveries SET status = ?, external_id = CASE WHEN ? = '' THEN external_id ELSE ? END, error = ?,
		completed_at = ? WHERE id = ?`, status, externalID, externalID, errText, ts(nowUTC()), id)
	return err
}

// ---- decisions ----

const decisionCols = `id, scope_kind, scope_id, title, body, status, COALESCE(supersedes_id, ''), COALESCE(superseded_by_id, ''),
	created_by_kind, created_by_id, accepted_by_kind, accepted_by_id, visible_room_ids, version, created_at, accepted_at`

func scanDecision(s scanner) (protocol.Decision, error) {
	var d protocol.Decision
	var abk, abi, visible, accepted sql.NullString
	var created string
	err := s.Scan(&d.ID, &d.Scope.Kind, &d.Scope.ID, &d.Title, &d.Body, &d.Status, &d.SupersedesID, &d.SupersededByID,
		&d.CreatedBy.Kind, &d.CreatedBy.ID, &abk, &abi, &visible, &d.Version, &created, &accepted)
	if abk.Valid {
		d.AcceptedBy = &protocol.Actor{Kind: abk.String, ID: abi.String}
	}
	if visible.Valid {
		unjs(visible.String, &d.VisibleRoomIDs)
		d.VisibleRoomIDs = strs(d.VisibleRoomIDs)
	}
	d.CreatedAt, d.AcceptedAt = parseTS(created), parseTSP(accepted)
	d.Sources = []protocol.Source{}
	return d, err
}

func InsertDecision(ctx context.Context, q Q, orgID string, d protocol.Decision) error {
	var visible any
	if d.VisibleRoomIDs != nil {
		visible = js(d.VisibleRoomIDs)
	}
	var abk, abi any
	if d.AcceptedBy != nil {
		abk, abi = d.AcceptedBy.Kind, d.AcceptedBy.ID
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO decisions(id, org_id, scope_kind, scope_id, title, body, status, supersedes_id, created_by_kind,
		created_by_id, accepted_by_kind, accepted_by_id, visible_room_ids, version, created_at, accepted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, d.ID, orgID, d.Scope.Kind, d.Scope.ID, d.Title, d.Body, d.Status,
		nullStr(d.SupersedesID), d.CreatedBy.Kind, d.CreatedBy.ID, abk, abi, visible, ts(d.CreatedAt), tsp(d.AcceptedAt)); err != nil {
		return err
	}
	for _, s := range d.Sources {
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO decision_sources(decision_id, source_kind, source_id, room_id) VALUES (?, ?, ?, ?)`,
			d.ID, s.Kind, s.ID, nullStr(s.RoomID)); err != nil {
			return err
		}
	}
	_, err := q.ExecContext(ctx, `INSERT INTO decisions_fts(title, body, decision_id) VALUES (?, ?, ?)`, d.Title, d.Body, d.ID)
	return err
}

func GetDecision(ctx context.Context, q Q, id string) (protocol.Decision, error) {
	return getFilled(ctx, q, scanDecision, fillDecision, `SELECT `+decisionCols+` FROM decisions WHERE id = ?`, id)
}

func ListDecisions(ctx context.Context, q Q, where string, args ...any) ([]protocol.Decision, error) {
	return listFilled(ctx, q, scanDecision, fillDecision, `SELECT `+decisionCols+` FROM decisions`+whereClause(where)+` ORDER BY created_at DESC`, args...)
}

func fillDecision(ctx context.Context, q Q, d *protocol.Decision) error {
	var err error
	d.Sources, err = list(ctx, q, func(s scanner) (protocol.Source, error) {
		var x protocol.Source
		err := s.Scan(&x.Kind, &x.ID, &x.RoomID)
		return x, err
	}, `SELECT source_kind, source_id, COALESCE(room_id, '') FROM decision_sources WHERE decision_id = ?`, d.ID)
	return err
}

func SetDecisionStatus(ctx context.Context, q Q, id string, expectVersion int64, status string, by *protocol.Actor, supersededBy string) (bool, error) {
	var abk, abi, at any
	if by != nil && status == "accepted" {
		abk, abi, at = by.Kind, by.ID, ts(nowUTC())
	}
	query := `UPDATE decisions SET status = ?, accepted_by_kind = COALESCE(?, accepted_by_kind), accepted_by_id = COALESCE(?, accepted_by_id),
		accepted_at = COALESCE(?, accepted_at), superseded_by_id = COALESCE(?, superseded_by_id), version = version + 1 WHERE id = ?`
	args := []any{status, abk, abi, at, nullStr(supersededBy), id}
	if expectVersion > 0 {
		query += ` AND version = ?`
		args = append(args, expectVersion)
	}
	return oneRow(q.ExecContext(ctx, query, args...))
}

// SearchDecisions ranks accepted-or-proposed decisions by text, limited to
// those whose visibility allows at least one of rooms (or that carry no room
// restriction). Visibility is applied before ranking and the limit, so
// private decisions never crowd out ones the reader may see.
func SearchDecisions(ctx context.Context, q Q, match string, rooms []string, limit int) ([]string, error) {
	args := []any{match}
	cond := `d.visible_room_ids IS NULL`
	if len(rooms) > 0 {
		in, roomArgs := inList(rooms)
		cond += ` OR EXISTS (SELECT 1 FROM json_each(d.visible_room_ids) WHERE value IN ` + in + `)`
		args = append(args, roomArgs...)
	}
	args = append(args, limit)
	return stringsCol(ctx, q, `SELECT f.decision_id FROM decisions_fts f JOIN decisions d ON d.id = f.decision_id
		WHERE decisions_fts MATCH ? AND (`+cond+`) ORDER BY f.rank LIMIT ?`, args...)
}

// ---- usage ----

func InsertUsage(ctx context.Context, q Q, id, runID, provider string, u protocol.Usage) error {
	_, err := q.ExecContext(ctx, `INSERT INTO usage_samples(id, run_id, provider, source, input_tokens, output_tokens, cost_usd, billing, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, runID, provider, u.Source, u.InputTokens, u.OutputTokens, u.CostUSD, u.Billing, ts(nowUTC()))
	return err
}

// ---- credentials ----

func PutCredential(ctx context.Context, q Q, id, orgID, kind, label, host string, secret []byte) error {
	_, err := q.ExecContext(ctx, `INSERT INTO credentials(id, org_id, kind, label, host, secret, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, orgID, kind, label, host, secret, ts(nowUTC()))
	return err
}

func GetCredential(ctx context.Context, q Q, kind, host string) (id string, secret []byte, err error) {
	err = q.QueryRowContext(ctx, `SELECT id, secret FROM credentials WHERE kind = ? AND host = ? ORDER BY created_at DESC LIMIT 1`, kind, host).Scan(&id, &secret)
	return id, secret, notFound(err)
}

// ---- tool call idempotency ----

// GetToolCall returns a recorded tool result for (run, call).
func GetToolCall(ctx context.Context, q Q, runID, callID string) (ok bool, result string, found bool, err error) {
	var okInt int
	err = q.QueryRowContext(ctx, `SELECT ok, result FROM tool_calls WHERE run_id = ? AND call_id = ?`, runID, callID).Scan(&okInt, &result)
	if err == sql.ErrNoRows {
		return false, "", false, nil
	}
	return okInt == 1, result, err == nil, err
}

func InsertToolCall(ctx context.Context, q Q, runID, callID, tool string, ok bool, result string) error {
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO tool_calls(run_id, call_id, tool, ok, result, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		runID, callID, tool, b2i(ok), result, ts(nowUTC()))
	return err
}

// ---- run intents ----

func SetRunIntent(ctx context.Context, q Q, runID, wait, detail string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO run_intents(run_id, wait, detail, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET wait = excluded.wait, detail = excluded.detail`, runID, wait, detail, ts(nowUTC()))
	return err
}

func GetRunIntent(ctx context.Context, q Q, runID string) (wait, detail string, err error) {
	err = q.QueryRowContext(ctx, `SELECT wait, detail FROM run_intents WHERE run_id = ?`, runID).Scan(&wait, &detail)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return wait, detail, err
}
