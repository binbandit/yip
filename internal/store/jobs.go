package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/binbandit/yip/protocol"
)

// JobRow is a job with hub-internal bookkeeping fields.
type JobRow struct {
	protocol.Job
	DiffArtifactID string
	RetryAt        *time.Time
	AutoRetries    int
	HelpRequest    bool
}

const jobCols = `id, org_id, kind, title, objective, acceptance, state, waiting_reason, state_detail, owner_id, COALESCE(parent_id, ''),
	root_request_id, source_room_id, COALESCE(source_thread_id, ''), COALESCE(source_message_id, ''), COALESCE(project_id, ''), COALESCE(repo_id, ''),
	branch, base_rev, head_rev, COALESCE(diff_artifact_id, ''), requires_peer_review, requires_human_review, completion_requested, summary,
	depth, priority, COALESCE(current_run_id, ''), retry_at, auto_retries, last_activity, last_activity_at, created_at, updated_at, completed_at, version, help_request,
	COALESCE(follows_job_id, '')`

func scanJob(s scanner) (JobRow, error) {
	var j JobRow
	var acceptance, created, updated, branch, base, head string
	var peer, human, compReq, help int
	var retryAt, lastAt, completed sql.NullString
	err := s.Scan(&j.ID, &j.OrgID, &j.Kind, &j.Title, &j.Objective, &acceptance, &j.State, &j.WaitingReason, &j.StateDetail,
		&j.OwnerID, &j.ParentID, &j.RootRequestID, &j.Source.RoomID, &j.Source.ThreadID, &j.Source.MessageID, &j.ProjectID, &j.RepoID,
		&branch, &base, &head, &j.DiffArtifactID, &peer, &human, &compReq, &j.Summary,
		&j.Depth, &j.Priority, &j.CurrentRunID, &retryAt, &j.AutoRetries, &j.LastActivity, &lastAt, &created, &updated, &completed, &j.Version, &help,
		&j.FollowsID)
	j.HelpRequest = help == 1
	unjs(acceptance, &j.Acceptance)
	j.Acceptance = strs(j.Acceptance)
	j.RequiresPeerReview, j.RequiresHumanReview, j.CompletionRequested = peer == 1, human == 1, compReq == 1
	if j.RepoID != "" {
		j.Revision = &protocol.Revision{RepoID: j.RepoID, Branch: branch, Base: base, Head: head}
	}
	j.RetryAt, j.LastActivityAt, j.CompletedAt = parseTSP(retryAt), parseTSP(lastAt), parseTSP(completed)
	j.CreatedAt, j.UpdatedAt = parseTS(created), parseTS(updated)
	j.Contributors, j.ReviewerIDs = []string{}, []string{}
	return j, err
}

func InsertJob(ctx context.Context, q Q, j JobRow) error {
	var branch, base string
	if j.Revision != nil {
		branch, base = j.Revision.Branch, j.Revision.Base
	}
	_, err := q.ExecContext(ctx, `INSERT INTO jobs(id, org_id, kind, title, objective, acceptance, state, waiting_reason, state_detail, owner_id,
		parent_id, root_request_id, source_room_id, source_thread_id, source_message_id, project_id, repo_id, branch, base_rev,
		requires_peer_review, requires_human_review, depth, priority, last_activity, last_activity_at, created_at, updated_at, version, follows_job_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		j.ID, j.OrgID, j.Kind, j.Title, j.Objective, js(strs(j.Acceptance)), j.State, j.WaitingReason, j.StateDetail, j.OwnerID,
		nullStr(j.ParentID), j.RootRequestID, j.Source.RoomID, nullStr(j.Source.ThreadID), nullStr(j.Source.MessageID),
		nullStr(j.ProjectID), nullStr(j.RepoID), branch, base, b2i(j.RequiresPeerReview), b2i(j.RequiresHumanReview),
		j.Depth, j.Priority, j.LastActivity, tsp(j.LastActivityAt), ts(j.CreatedAt), ts(j.UpdatedAt), nullStr(j.FollowsID))
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO job_assignments(job_id, engineer_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		j.ID, j.OwnerID, ts(j.CreatedAt)); err != nil {
		return err
	}
	if j.Kind != protocol.JobKindReply {
		_, err = q.ExecContext(ctx, `INSERT INTO jobs_fts(title, objective, job_id) VALUES (?, ?, ?)`, j.Title, j.Objective, j.ID)
	}
	return err
}

func GetJob(ctx context.Context, q Q, id string) (JobRow, error) {
	return getFilled(ctx, q, scanJob, fillJob, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id)
}

func fillJob(ctx context.Context, q Q, j *JobRow) error {
	var err error
	if j.Contributors, err = stringsCol(ctx, q, `SELECT engineer_id FROM job_assignments WHERE job_id = ? AND role = 'contributor'`, j.ID); err != nil {
		return err
	}
	if j.ReviewerIDs, err = stringsCol(ctx, q, `SELECT reviewer_id FROM reviews WHERE job_id = ? ORDER BY created_at`, j.ID); err != nil {
		return err
	}
	var node sql.NullString
	if j.CurrentRunID != "" {
		_ = q.QueryRowContext(ctx, `SELECT node_id FROM runs WHERE id = ?`, j.CurrentRunID).Scan(&node)
	}
	j.NodeID = node.String
	return nil
}

// JobFilter selects jobs for the work ledger.
type JobFilter struct {
	States       []protocol.JobState
	OwnerID      string
	ProjectID    string
	RoomIDs      []string // restrict to jobs whose source room is in this set
	ParentID     string
	IncludeReply bool
	Since        *time.Time
	Limit        int
}

func ListJobs(ctx context.Context, q Q, f JobFilter) ([]JobRow, error) {
	var where []string
	var args []any
	if !f.IncludeReply {
		where = append(where, "kind <> 'reply'")
	}
	if len(f.States) > 0 {
		in, states := inList(f.States)
		where = append(where, "state IN "+in)
		args = append(args, states...)
	}
	if f.OwnerID != "" {
		where = append(where, "owner_id = ?")
		args = append(args, f.OwnerID)
	}
	if f.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.ParentID != "" {
		where = append(where, "parent_id = ?")
		args = append(args, f.ParentID)
	}
	if f.RoomIDs != nil {
		if len(f.RoomIDs) == 0 {
			return []JobRow{}, nil
		}
		in, rooms := inList(f.RoomIDs)
		where = append(where, "source_room_id IN "+in)
		args = append(args, rooms...)
	}
	if f.Since != nil {
		where = append(where, "updated_at >= ?")
		args = append(args, ts(*f.Since))
	}
	query := `SELECT ` + jobCols + ` FROM jobs`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY updated_at DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	return listFilled(ctx, q, scanJob, fillJob, query, args...)
}

// SetJobState applies a validated state change under an optimistic version.
func SetJobState(ctx context.Context, q Q, id string, expectVersion int64, state protocol.JobState, waiting, detail string) (bool, error) {
	now := nowUTC()
	var completed any
	if state == protocol.JobCompleted || state == protocol.JobCancelled {
		completed = ts(now)
	}
	return oneRow(q.ExecContext(ctx, `UPDATE jobs SET state = ?, waiting_reason = ?, state_detail = ?, updated_at = ?,
		completed_at = COALESCE(?, completed_at), version = version + 1 WHERE id = ? AND version = ?`,
		string(state), waiting, detail, ts(now), completed, id, expectVersion))
}

func SetJobCurrentRun(ctx context.Context, q Q, id, runID string) error {
	_, err := q.ExecContext(ctx, `UPDATE jobs SET current_run_id = ?, updated_at = ?, version = version + 1 WHERE id = ?`, nullStr(runID), ts(nowUTC()), id)
	return err
}

func SetJobActivity(ctx context.Context, q Q, id, text string, at time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE jobs SET last_activity = ?, last_activity_at = ?, updated_at = ? WHERE id = ?`, text, ts(at), ts(at), id)
	return err
}

func SetJobRevision(ctx context.Context, q Q, id, branch, base, head, diffArtifactID string) error {
	_, err := q.ExecContext(ctx, `UPDATE jobs SET branch = ?, base_rev = CASE WHEN base_rev = '' THEN ? ELSE base_rev END, head_rev = ?,
		diff_artifact_id = ?, updated_at = ?, version = version + 1 WHERE id = ?`, branch, base, head, nullStr(diffArtifactID), ts(nowUTC()), id)
	return err
}

func SetJobBase(ctx context.Context, q Q, id, branch, base string) error {
	_, err := q.ExecContext(ctx, `UPDATE jobs SET branch = ?, base_rev = ? WHERE id = ? AND base_rev = ''`, branch, base, id)
	return err
}

func SetJobCompletion(ctx context.Context, q Q, id string, requested bool, summary string) error {
	_, err := q.ExecContext(ctx, `UPDATE jobs SET completion_requested = ?, summary = CASE WHEN ? = '' THEN summary ELSE ? END,
		updated_at = ?, version = version + 1 WHERE id = ?`, b2i(requested), summary, summary, ts(nowUTC()), id)
	return err
}

func SetJobRetry(ctx context.Context, q Q, id string, at *time.Time, autoRetries int) error {
	_, err := q.ExecContext(ctx, `UPDATE jobs SET retry_at = ?, auto_retries = ? WHERE id = ?`, tsp(at), autoRetries, id)
	return err
}

func AddAssignment(ctx context.Context, q Q, jobID, engineerID, role string) error {
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO job_assignments(job_id, engineer_id, role, created_at) VALUES (?, ?, ?, ?)`,
		jobID, engineerID, role, ts(nowUTC()))
	return err
}

// ---- dependencies ----

func AddDependency(ctx context.Context, q Q, jobID, kind, id string) error {
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO job_dependencies(job_id, depends_on_kind, depends_on_id, created_at) VALUES (?, ?, ?, ?)`,
		jobID, kind, id, ts(nowUTC()))
	return err
}

func ResolveDependency(ctx context.Context, q Q, kind, id string) ([]string, error) {
	jobs, err := stringsCol(ctx, q, `SELECT job_id FROM job_dependencies WHERE depends_on_kind = ? AND depends_on_id = ? AND resolved_at IS NULL`, kind, id)
	if err != nil {
		return nil, err
	}
	_, err = q.ExecContext(ctx, `UPDATE job_dependencies SET resolved_at = ? WHERE depends_on_kind = ? AND depends_on_id = ? AND resolved_at IS NULL`,
		ts(nowUTC()), kind, id)
	return jobs, err
}

func OpenDependencies(ctx context.Context, q Q, jobID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_dependencies WHERE job_id = ? AND resolved_at IS NULL`, jobID).Scan(&n)
	return n, err
}

// JobAncestors returns the parent chain of a job (nearest first), used to
// reject dependency cycles.
func JobAncestors(ctx context.Context, q Q, jobID string) ([]string, error) {
	return stringsCol(ctx, q, `WITH RECURSIVE chain(id, parent) AS (
		SELECT id, parent_id FROM jobs WHERE id = ?
		UNION ALL SELECT j.id, j.parent_id FROM jobs j JOIN chain c ON j.id = c.parent)
		SELECT id FROM chain WHERE id <> ?`, jobID, jobID)
}

// JobDescendants returns all jobs below a job in the tree.
func JobDescendants(ctx context.Context, q Q, jobID string) ([]string, error) {
	return stringsCol(ctx, q, `WITH RECURSIVE tree(id) AS (
		SELECT id FROM jobs WHERE parent_id = ?
		UNION ALL SELECT j.id FROM jobs j JOIN tree t ON j.parent_id = t.id)
		SELECT id FROM tree`, jobID)
}

// ---- inputs ----

func InsertJobInput(ctx context.Context, q Q, in protocol.JobInput, clientKey string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO job_inputs(id, job_id, run_id, body, delivery, message_id, client_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, in.ID, in.JobID, nullStr(in.RunID), in.Body, in.Delivery, nullStr(in.MessageID),
		nullStr(clientKey), ts(in.CreatedAt))
	return err
}

func scanInput(s scanner) (protocol.JobInput, error) {
	var in protocol.JobInput
	var created string
	var delivered sql.NullString
	err := s.Scan(&in.ID, &in.JobID, &in.RunID, &in.Body, &in.Delivery, &in.MessageID, &created, &delivered)
	in.CreatedAt, in.DeliveredAt = parseTS(created), parseTSP(delivered)
	return in, err
}

const inputCols = `id, job_id, COALESCE(run_id, ''), body, delivery, COALESCE(message_id, ''), created_at, delivered_at`

func GetJobInputByClientKey(ctx context.Context, q Q, key string) (protocol.JobInput, error) {
	in, err := scanInput(q.QueryRowContext(ctx, `SELECT `+inputCols+` FROM job_inputs WHERE client_key = ?`, key))
	return in, notFound(err)
}

func GetJobInput(ctx context.Context, q Q, id string) (protocol.JobInput, error) {
	in, err := scanInput(q.QueryRowContext(ctx, `SELECT `+inputCols+` FROM job_inputs WHERE id = ?`, id))
	return in, notFound(err)
}

func ListJobInputs(ctx context.Context, q Q, jobID string) ([]protocol.JobInput, error) {
	return list(ctx, q, scanInput, `SELECT `+inputCols+` FROM job_inputs WHERE job_id = ? ORDER BY created_at`, jobID)
}

// UnconsumedInputs are inputs not yet folded into a run's context.
func UnconsumedInputs(ctx context.Context, q Q, jobID string) ([]protocol.JobInput, error) {
	return list(ctx, q, scanInput, `SELECT `+inputCols+` FROM job_inputs WHERE job_id = ? AND consumed_at IS NULL
		AND delivery IN ('pending','queued') ORDER BY created_at`, jobID)
}

func SetInputDelivery(ctx context.Context, q Q, id, runID, delivery string) error {
	_, err := q.ExecContext(ctx, `UPDATE job_inputs SET delivery = ?, run_id = COALESCE(?, run_id), delivered_at = ? WHERE id = ?`,
		delivery, nullStr(runID), ts(nowUTC()), id)
	return err
}

func ConsumeInputs(ctx context.Context, q Q, ids []string) error {
	for _, id := range ids {
		if _, err := q.ExecContext(ctx, `UPDATE job_inputs SET consumed_at = ? WHERE id = ?`, ts(nowUTC()), id); err != nil {
			return err
		}
	}
	return nil
}

// ---- wakeup budgets ----

// ClaimWakeup records one automatic wakeup against a root request. It returns
// false (without recording) when the budget is exhausted. The delivery key
// makes replays of the same wakeup free.
func ClaimWakeup(ctx context.Context, q Q, id, rootID, causeID, engineerID, purpose, deliveryKey string, automatic bool, max int) (claimed bool, duplicate bool, err error) {
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM wakeups WHERE delivery_key = ?`, deliveryKey).Scan(&exists); err != nil {
		return false, false, err
	}
	if exists > 0 {
		return false, true, nil
	}
	if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO root_budgets(root_request_id, wakeups, max_wakeups, created_at) VALUES (?, 0, ?, ?)`,
		rootID, max, ts(nowUTC())); err != nil {
		return false, false, err
	}
	if automatic {
		res, err := q.ExecContext(ctx, `UPDATE root_budgets SET wakeups = wakeups + 1 WHERE root_request_id = ? AND wakeups < max_wakeups`, rootID)
		if err != nil {
			return false, false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			_, _ = q.ExecContext(ctx, `UPDATE root_budgets SET exhausted_at = COALESCE(exhausted_at, ?) WHERE root_request_id = ?`, ts(nowUTC()), rootID)
			return false, false, nil
		}
	}
	_, err = q.ExecContext(ctx, `INSERT INTO wakeups(id, root_request_id, cause_id, engineer_id, purpose, delivery_key, automatic, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, rootID, causeID, engineerID, purpose, deliveryKey, b2i(automatic), ts(nowUTC()))
	return err == nil, false, err
}

func WakeupsUsed(ctx context.Context, q Q, rootID string) (used, max int, err error) {
	err = q.QueryRowContext(ctx, `SELECT wakeups, max_wakeups FROM root_budgets WHERE root_request_id = ?`, rootID).Scan(&used, &max)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return used, max, err
}

// scanJobResult reads (id, title, snippet, room, updated_at) as a job search hit.
func scanJobResult(s scanner) (protocol.SearchResult, error) {
	var r protocol.SearchResult
	var updated string
	err := s.Scan(&r.ID, &r.Title, &r.Snippet, &r.RoomID, &updated)
	r.Kind, r.JobID = "job", r.ID
	t := parseTS(updated)
	r.At = &t
	return r, err
}

// SearchJobs runs a full-text query over the titles and objectives of work
// in the given rooms; projectID, when set, narrows it to that project before
// ranking.
func SearchJobs(ctx context.Context, q Q, match string, roomIDs []string, projectID string, limit int) ([]protocol.SearchResult, error) {
	if len(roomIDs) == 0 {
		return []protocol.SearchResult{}, nil
	}
	in, rooms := inList(roomIDs)
	args := append(append([]any{match}, rooms...), projectID, projectID, limit)
	return list(ctx, q, scanJobResult, `SELECT j.id, j.title, snippet(jobs_fts, 1, '[', ']', '…', 12), j.source_room_id, j.updated_at
		FROM jobs_fts f JOIN jobs j ON j.id = f.job_id WHERE jobs_fts MATCH ? AND j.source_room_id IN `+in+`
		AND (? = '' OR j.project_id = ?)
		ORDER BY rank LIMIT ?`, args...)
}

// FindJobsByID looks work up by its ID: the whole ID, or at least six
// characters from its start or end (the short work ID shown on the work).
// Only work from the given rooms is returned.
func FindJobsByID(ctx context.Context, q Q, frag string, roomIDs []string, limit int) ([]protocol.SearchResult, error) {
	frag = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(frag), "#"))
	if len(roomIDs) == 0 || len(frag) < 6 || strings.Trim(frag, "0123456789abcdef-") != "" {
		return []protocol.SearchResult{}, nil
	}
	in, rooms := inList(roomIDs)
	args := append(append([]any{frag, frag + "%", "%" + frag}, rooms...), limit)
	return list(ctx, q, scanJobResult, `SELECT id, title, objective, source_room_id, updated_at FROM jobs
		WHERE (id = ? OR id LIKE ? OR id LIKE ?) AND source_room_id IN `+in+`
		ORDER BY updated_at DESC LIMIT ?`, args...)
}

// FollowUps lists work started as a follow-up to jobID, oldest first.
func FollowUps(ctx context.Context, q Q, jobID string) ([]JobRow, error) {
	return list(ctx, q, scanJob, `SELECT `+jobCols+` FROM jobs WHERE follows_job_id = ? ORDER BY created_at`, jobID)
}

// PendingWake is a deferred continuation for a job with an active attempt.
type PendingWake struct {
	Purpose   string `json:"purpose"`
	Cause     string `json:"cause"`
	Note      string `json:"note,omitempty"`
	Automatic bool   `json:"automatic"`
}

// SetPendingWake records (or clears, with nil) a deferred continuation.
func SetPendingWake(ctx context.Context, q Q, jobID string, w *PendingWake) error {
	var v any
	if w != nil {
		v = js(w)
	}
	_, err := q.ExecContext(ctx, `UPDATE jobs SET pending_wake = ? WHERE id = ?`, v, jobID)
	return err
}

// TakePendingWake returns and clears a job's deferred continuation.
func TakePendingWake(ctx context.Context, q Q, jobID string) (*PendingWake, error) {
	var raw sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT pending_wake FROM jobs WHERE id = ?`, jobID).Scan(&raw); err != nil {
		return nil, notFound(err)
	}
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var w PendingWake
	unjs(raw.String, &w)
	_, err := q.ExecContext(ctx, `UPDATE jobs SET pending_wake = NULL WHERE id = ?`, jobID)
	return &w, err
}

// MarkHelpRequest flags a job as a colleague's help request.
func MarkHelpRequest(ctx context.Context, q Q, id string) error {
	_, err := q.ExecContext(ctx, `UPDATE jobs SET help_request = 1 WHERE id = ?`, id)
	return err
}

// ConsumeStartedInputs marks inputs included in a run's manifest as consumed
// once that run actually starts (a rejected or expired offer keeps them).
func ConsumeStartedInputs(ctx context.Context, q Q, runID string) error {
	_, err := q.ExecContext(ctx, `UPDATE job_inputs SET consumed_at = ? WHERE run_id = ? AND consumed_at IS NULL AND delivery = 'queued'`,
		ts(nowUTC()), runID)
	return err
}
