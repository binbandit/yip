package store

import (
	"context"
	"database/sql"

	"github.com/binbandit/yip/protocol"
)

const reviewCols = `id, job_id, author_id, reviewer_id, source_room_id, COALESCE(source_thread_id, ''), COALESCE(source_message_id, ''),
	target_kind, criteria, state, current_round, COALESCE(pull_request_id, ''), created_at, updated_at`

func scanReview(s scanner) (protocol.Review, error) {
	var r protocol.Review
	var created, updated string
	err := s.Scan(&r.ID, &r.JobID, &r.AuthorID, &r.ReviewerID, &r.Source.RoomID, &r.Source.ThreadID, &r.Source.MessageID,
		&r.TargetKind, &r.Criteria, &r.State, &r.CurrentRound, &r.PullRequestID, &created, &updated)
	r.CreatedAt, r.UpdatedAt = parseTS(created), parseTS(updated)
	r.Rounds = []protocol.ReviewRound{}
	return r, err
}

func InsertReview(ctx context.Context, q Q, orgID string, r protocol.Review) error {
	_, err := q.ExecContext(ctx, `INSERT INTO reviews(id, org_id, job_id, author_id, reviewer_id, source_room_id, source_thread_id, source_message_id,
		target_kind, criteria, state, current_round, pull_request_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, orgID, r.JobID, r.AuthorID, r.ReviewerID, r.Source.RoomID, nullStr(r.Source.ThreadID), nullStr(r.Source.MessageID),
		r.TargetKind, r.Criteria, string(r.State), r.CurrentRound, nullStr(r.PullRequestID), ts(r.CreatedAt), ts(r.UpdatedAt))
	return err
}

func GetReview(ctx context.Context, q Q, id string) (protocol.Review, error) {
	r, err := scanReview(q.QueryRowContext(ctx, `SELECT `+reviewCols+` FROM reviews WHERE id = ?`, id))
	if err != nil {
		return r, notFound(err)
	}
	return r, fillReview(ctx, q, &r)
}

// GetReviewForJob returns the review of a job by a given reviewer.
func GetReviewForJob(ctx context.Context, q Q, jobID, reviewerID string) (protocol.Review, error) {
	r, err := scanReview(q.QueryRowContext(ctx, `SELECT `+reviewCols+` FROM reviews WHERE job_id = ? AND reviewer_id = ?`, jobID, reviewerID))
	if err != nil {
		return r, notFound(err)
	}
	return r, fillReview(ctx, q, &r)
}

func ListJobReviews(ctx context.Context, q Q, jobID string) ([]protocol.Review, error) {
	rs, err := list(ctx, q, scanReview, `SELECT `+reviewCols+` FROM reviews WHERE job_id = ? ORDER BY created_at`, jobID)
	if err != nil {
		return nil, err
	}
	for i := range rs {
		if err := fillReview(ctx, q, &rs[i]); err != nil {
			return nil, err
		}
	}
	return rs, nil
}

// ReviewByRoundJob finds the review whose round is served by a reviewer job.
func ReviewByReviewJob(ctx context.Context, q Q, reviewJobID string) (protocol.Review, protocol.ReviewRound, error) {
	var reviewID, roundID string
	err := q.QueryRowContext(ctx, `SELECT review_id, id FROM review_rounds WHERE review_job_id = ? ORDER BY number DESC LIMIT 1`, reviewJobID).Scan(&reviewID, &roundID)
	if err != nil {
		return protocol.Review{}, protocol.ReviewRound{}, notFound(err)
	}
	r, err := GetReview(ctx, q, reviewID)
	if err != nil {
		return r, protocol.ReviewRound{}, err
	}
	for _, rd := range r.Rounds {
		if rd.ID == roundID {
			return r, rd, nil
		}
	}
	return r, protocol.ReviewRound{}, ErrNotFound
}

func SetReviewState(ctx context.Context, q Q, id string, state protocol.ReviewState, round int) error {
	_, err := q.ExecContext(ctx, `UPDATE reviews SET state = ?, current_round = ?, updated_at = ? WHERE id = ?`, string(state), round, ts(nowUTC()), id)
	return err
}

func SetReviewPR(ctx context.Context, q Q, id, prID string) error {
	_, err := q.ExecContext(ctx, `UPDATE reviews SET pull_request_id = ? WHERE id = ?`, nullStr(prID), id)
	return err
}

const roundCols = `id, review_id, number, target_kind, COALESCE(repo_id, ''), base_rev, head_rev, COALESCE(artifact_id, ''), target_hash,
	COALESCE(pull_request_id, ''), state, summary, COALESCE(reviewer_run_id, ''), COALESCE(review_job_id, ''), COALESCE(superseded_by, ''),
	created_at, decided_at`

func scanRound(s scanner) (protocol.ReviewRound, error) {
	var r protocol.ReviewRound
	var created string
	var decided sql.NullString
	err := s.Scan(&r.ID, &r.ReviewID, &r.Number, &r.Target.Kind, &r.Target.RepoID, &r.Target.Base, &r.Target.Head, &r.Target.ArtifactID,
		&r.Target.Hash, &r.Target.PullRequestID, &r.State, &r.Summary, &r.ReviewerRunID, &r.ReviewJobID, &r.SupersededBy, &created, &decided)
	r.CreatedAt, r.DecidedAt = parseTS(created), parseTSP(decided)
	r.Findings = []protocol.Finding{}
	return r, err
}

// InsertRound creates a review round. causeKey is unique: a replayed mention,
// PR mirror, or delivery of the same logical request cannot dispatch twice.
func InsertRound(ctx context.Context, q Q, r protocol.ReviewRound, causeKey string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO review_rounds(id, review_id, number, target_kind, repo_id, base_rev, head_rev, artifact_id, target_hash,
		pull_request_id, state, summary, review_job_id, cause_key, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)`,
		r.ID, r.ReviewID, r.Number, r.Target.Kind, nullStr(r.Target.RepoID), r.Target.Base, r.Target.Head, nullStr(r.Target.ArtifactID),
		r.Target.Hash, nullStr(r.Target.PullRequestID), string(r.State), nullStr(r.ReviewJobID), causeKey, ts(r.CreatedAt))
	return err
}

func RoundByCauseKey(ctx context.Context, q Q, causeKey string) (protocol.ReviewRound, error) {
	r, err := scanRound(q.QueryRowContext(ctx, `SELECT `+roundCols+` FROM review_rounds WHERE cause_key = ?`, causeKey))
	return r, notFound(err)
}

func GetRound(ctx context.Context, q Q, id string) (protocol.ReviewRound, error) {
	r, err := scanRound(q.QueryRowContext(ctx, `SELECT `+roundCols+` FROM review_rounds WHERE id = ?`, id))
	if err != nil {
		return r, notFound(err)
	}
	r.Findings, err = listFindings(ctx, q, `round_id = ?`, r.ID)
	return r, err
}

func SetRoundState(ctx context.Context, q Q, id string, state protocol.ReviewState, summary string, decided bool) error {
	var at any
	if decided {
		at = ts(nowUTC())
	}
	_, err := q.ExecContext(ctx, `UPDATE review_rounds SET state = ?, summary = CASE WHEN ? = '' THEN summary ELSE ? END,
		decided_at = COALESCE(?, decided_at) WHERE id = ?`, string(state), summary, summary, at, id)
	return err
}

func SetRoundReviewer(ctx context.Context, q Q, id, reviewJobID, runID string) error {
	_, err := q.ExecContext(ctx, `UPDATE review_rounds SET review_job_id = COALESCE(?, review_job_id), reviewer_run_id = COALESCE(?, reviewer_run_id) WHERE id = ?`,
		nullStr(reviewJobID), nullStr(runID), id)
	return err
}

func SupersedeRound(ctx context.Context, q Q, id, by string) error {
	_, err := q.ExecContext(ctx, `UPDATE review_rounds SET superseded_by = ? WHERE id = ? AND superseded_by IS NULL`, by, id)
	return err
}

func fillReview(ctx context.Context, q Q, r *protocol.Review) error {
	rounds, err := list(ctx, q, scanRound, `SELECT `+roundCols+` FROM review_rounds WHERE review_id = ? ORDER BY number`, r.ID)
	if err != nil {
		return err
	}
	for i := range rounds {
		if rounds[i].Findings, err = listFindings(ctx, q, `round_id = ?`, rounds[i].ID); err != nil {
			return err
		}
	}
	r.Rounds = rounds
	return nil
}

const findingCols = `id, round_id, severity, body, file, line, evidence, status, created_at`

func listFindings(ctx context.Context, q Q, where string, args ...any) ([]protocol.Finding, error) {
	fs, err := list(ctx, q, func(s scanner) (protocol.Finding, error) {
		var f protocol.Finding
		var created string
		err := s.Scan(&f.ID, &f.RoundID, &f.Severity, &f.Body, &f.File, &f.Line, &f.Evidence, &f.Status, &created)
		f.CreatedAt = parseTS(created)
		f.Replies = []protocol.FindingReply{}
		return f, err
	}, `SELECT `+findingCols+` FROM review_findings WHERE `+where+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	for i := range fs {
		fs[i].Replies, err = list(ctx, q, func(s scanner) (protocol.FindingReply, error) {
			var r protocol.FindingReply
			var created string
			err := s.Scan(&r.ID, &r.FindingID, &r.Author.Kind, &r.Author.ID, &r.Body, &r.Evidence, &r.Revision, &created)
			r.CreatedAt = parseTS(created)
			return r, err
		}, `SELECT id, finding_id, author_kind, author_id, body, evidence, revision, created_at FROM finding_replies WHERE finding_id = ? ORDER BY created_at`, fs[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return fs, nil
}

// OpenBlockingFindings lists unresolved blocking findings across all rounds.
func OpenBlockingFindings(ctx context.Context, q Q, reviewID string) ([]protocol.Finding, error) {
	return listFindings(ctx, q, `review_id = ? AND severity = 'blocking' AND status IN ('open','addressed','disputed')`, reviewID)
}

func GetFinding(ctx context.Context, q Q, id string) (protocol.Finding, string, error) {
	var reviewID string
	if err := q.QueryRowContext(ctx, `SELECT review_id FROM review_findings WHERE id = ?`, id).Scan(&reviewID); err != nil {
		return protocol.Finding{}, "", notFound(err)
	}
	fs, err := listFindings(ctx, q, `id = ?`, id)
	if err != nil || len(fs) == 0 {
		return protocol.Finding{}, "", ErrNotFound
	}
	return fs[0], reviewID, nil
}

func InsertFinding(ctx context.Context, q Q, reviewID string, f protocol.Finding) error {
	_, err := q.ExecContext(ctx, `INSERT INTO review_findings(id, round_id, review_id, severity, body, file, line, evidence, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, f.ID, f.RoundID, reviewID, f.Severity, f.Body, f.File, f.Line, f.Evidence, f.Status, ts(f.CreatedAt))
	return err
}

func SetFindingStatus(ctx context.Context, q Q, id, status string, round int) error {
	var r any
	if status == "resolved" {
		r = round
	}
	_, err := q.ExecContext(ctx, `UPDATE review_findings SET status = ?, resolved_in_round = COALESCE(?, resolved_in_round) WHERE id = ?`, status, r, id)
	return err
}

func InsertFindingReply(ctx context.Context, q Q, r protocol.FindingReply) error {
	_, err := q.ExecContext(ctx, `INSERT INTO finding_replies(id, finding_id, author_kind, author_id, body, evidence, revision, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, r.FindingID, r.Author.Kind, r.Author.ID, r.Body, r.Evidence, r.Revision, ts(r.CreatedAt))
	return err
}
