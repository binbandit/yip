package store

import (
	"context"
	"time"

	"github.com/binbandit/yip/protocol"
)

type EngineerDraftRow struct {
	protocol.EngineerDraft
	UserID          string
	Request         protocol.CreateEngineerDraftRequest
	LeaseExpiresAt  time.Time
	DeadlineAt      time.Time
	CancelRequested bool
	LastSeq         int64
}

const draftCols = `id, user_id, node_id, request, state, detail, fields, lease_expires_at, deadline_at, cancel_requested, last_seq, created_at`

func scanEngineerDraft(s scanner) (EngineerDraftRow, error) {
	var d EngineerDraftRow
	var request, fields, lease, deadline, created string
	err := s.Scan(&d.ID, &d.UserID, &d.NodeID, &request, &d.State, &d.Detail, &fields, &lease, &deadline, &d.CancelRequested, &d.LastSeq, &created)
	unjs(request, &d.Request)
	unjs(fields, &d.Fields)
	d.Provider = d.Request.Provider
	d.LeaseExpiresAt, d.DeadlineAt, d.CreatedAt = parseTS(lease), parseTS(deadline), parseTS(created)
	return d, notFound(err)
}

func InsertEngineerDraft(ctx context.Context, q Q, d EngineerDraftRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO engineer_drafts(id, user_id, node_id, provider, profile_id, request, state, detail, lease_expires_at, deadline_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.ID, d.UserID, d.NodeID, d.Provider.Provider, d.Provider.ProfileID,
		js(d.Request), d.State, d.Detail, ts(d.LeaseExpiresAt), ts(d.DeadlineAt), ts(d.CreatedAt))
	return err
}

func GetEngineerDraft(ctx context.Context, q Q, id string) (EngineerDraftRow, error) {
	return scanEngineerDraft(q.QueryRowContext(ctx, `SELECT `+draftCols+` FROM engineer_drafts WHERE id = ?`, id))
}

func ActiveEngineerDrafts(ctx context.Context, q Q) ([]EngineerDraftRow, error) {
	return list(ctx, q, scanEngineerDraft, `SELECT `+draftCols+` FROM engineer_drafts WHERE state IN ('offered','preparing','running','stopping')`)
}

func UpdateEngineerDraft(ctx context.Context, q Q, d EngineerDraftRow) error {
	_, err := q.ExecContext(ctx, `UPDATE engineer_drafts SET state = ?, detail = ?, fields = ?, lease_expires_at = ?, cancel_requested = ?, last_seq = ? WHERE id = ?`,
		d.State, d.Detail, js(d.Fields), ts(d.LeaseExpiresAt), d.CancelRequested, d.LastSeq, d.ID)
	return err
}
