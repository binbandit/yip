package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/binbandit/yip/protocol"
)

const noteCols = `id, engineer_id, kind, scope_kind, scope_id, body, status, COALESCE(supersedes_id, ''), COALESCE(superseded_by_id, ''),
	created_by_kind, created_by_id, accepted_by_kind, accepted_by_id, visible_room_ids, review_after, version, created_at, accepted_at`

func scanNote(s scanner) (protocol.EngineerNote, error) {
	var n protocol.EngineerNote
	var abk, abi, visible, accepted sql.NullString
	var review, created string
	err := s.Scan(&n.ID, &n.EngineerID, &n.Kind, &n.Scope.Kind, &n.Scope.ID, &n.Body, &n.Status, &n.SupersedesID, &n.SupersededByID,
		&n.CreatedBy.Kind, &n.CreatedBy.ID, &abk, &abi, &visible, &review, &n.Version, &created, &accepted)
	if abk.Valid {
		n.AcceptedBy = &protocol.Actor{Kind: abk.String, ID: abi.String}
	}
	if visible.Valid {
		unjs(visible.String, &n.VisibleRoomIDs)
		n.VisibleRoomIDs = strs(n.VisibleRoomIDs)
	}
	n.ReviewAfter, n.CreatedAt, n.AcceptedAt = parseTS(review), parseTS(created), parseTSP(accepted)
	n.Sources = []protocol.Source{}
	return n, err
}

// InsertNote records a note with its sources.
func InsertNote(ctx context.Context, q Q, n protocol.EngineerNote) error {
	var visible any
	if n.VisibleRoomIDs != nil {
		visible = js(n.VisibleRoomIDs)
	}
	var abk, abi any
	if n.AcceptedBy != nil {
		abk, abi = n.AcceptedBy.Kind, n.AcceptedBy.ID
	}
	if n.Kind == "" {
		n.Kind = "note"
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO engineer_notes(id, engineer_id, kind, scope_kind, scope_id, body, status, supersedes_id,
		created_by_kind, created_by_id, accepted_by_kind, accepted_by_id, visible_room_ids, review_after, version, created_at, accepted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, n.ID, n.EngineerID, n.Kind, n.Scope.Kind, n.Scope.ID, n.Body, n.Status,
		nullStr(n.SupersedesID), n.CreatedBy.Kind, n.CreatedBy.ID, abk, abi, visible, ts(n.ReviewAfter), ts(n.CreatedAt), tsp(n.AcceptedAt)); err != nil {
		return err
	}
	for _, s := range n.Sources {
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO engineer_note_sources(note_id, source_kind, source_id, room_id) VALUES (?, ?, ?, ?)`,
			n.ID, s.Kind, s.ID, nullStr(s.RoomID)); err != nil {
			return err
		}
	}
	return nil
}

// GetNote loads one note with its sources.
func GetNote(ctx context.Context, q Q, id string) (protocol.EngineerNote, error) {
	n, err := scanNote(q.QueryRowContext(ctx, `SELECT `+noteCols+` FROM engineer_notes WHERE id = ?`, id))
	if err != nil {
		return n, notFound(err)
	}
	return n, fillNote(ctx, q, &n)
}

// ListNotes returns notes matching where, newest first.
func ListNotes(ctx context.Context, q Q, where string, args ...any) ([]protocol.EngineerNote, error) {
	query := `SELECT ` + noteCols + ` FROM engineer_notes`
	if where != "" {
		query += " WHERE " + where
	}
	ns, err := list(ctx, q, scanNote, query+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	for i := range ns {
		if err := fillNote(ctx, q, &ns[i]); err != nil {
			return nil, err
		}
	}
	return ns, nil
}

func fillNote(ctx context.Context, q Q, n *protocol.EngineerNote) error {
	var err error
	n.Sources, err = list(ctx, q, func(s scanner) (protocol.Source, error) {
		var x protocol.Source
		err := s.Scan(&x.Kind, &x.ID, &x.RoomID)
		return x, err
	}, `SELECT source_kind, source_id, COALESCE(room_id, '') FROM engineer_note_sources WHERE note_id = ?`, n.ID)
	return err
}

// SetNoteStatus changes a note's status under an optimistic version (0
// skips the check). Accepting records who accepted it.
func SetNoteStatus(ctx context.Context, q Q, id string, expectVersion int64, status string, by *protocol.Actor, supersededBy string) (bool, error) {
	var abk, abi, at any
	if by != nil && status == "accepted" {
		abk, abi, at = by.Kind, by.ID, ts(nowUTC())
	}
	query := `UPDATE engineer_notes SET status = ?, accepted_by_kind = COALESCE(?, accepted_by_kind), accepted_by_id = COALESCE(?, accepted_by_id),
		accepted_at = COALESCE(?, accepted_at), superseded_by_id = COALESCE(?, superseded_by_id), version = version + 1 WHERE id = ?`
	args := []any{status, abk, abi, at, nullStr(supersededBy), id}
	if expectVersion > 0 {
		query += ` AND version = ?`
		args = append(args, expectVersion)
	}
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RenewNote moves a note's review date forward.
func RenewNote(ctx context.Context, q Q, id string, expectVersion int64, until time.Time) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE engineer_notes SET review_after = ?, version = version + 1 WHERE id = ? AND version = ?`,
		ts(until), id, expectVersion)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
