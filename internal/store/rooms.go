package store

import (
	"context"

	"github.com/binbandit/yip/protocol"
)

const roomCols = `id, org_id, name, kind, purpose, private, reply_mode, COALESCE(steward_id, ''), archived, last_seq, created_at, version`

func scanRoom(s scanner) (protocol.Room, error) {
	var r protocol.Room
	var private, archived int
	var created string
	err := s.Scan(&r.ID, &r.OrgID, &r.Name, &r.Kind, &r.Purpose, &private, &r.ReplyMode, &r.StewardID,
		&archived, &r.LastSeq, &created, &r.Version)
	r.Private, r.Archived = private == 1, archived == 1
	r.CreatedAt = parseTS(created)
	r.Members, r.ProjectIDs = []protocol.Member{}, []string{}
	return r, err
}

func InsertRoom(ctx context.Context, q Q, r protocol.Room) error {
	_, err := q.ExecContext(ctx, `INSERT INTO rooms(id, org_id, name, kind, purpose, private, reply_mode, steward_id, archived, last_seq, created_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, 1)`, r.ID, r.OrgID, r.Name, r.Kind, r.Purpose, b2i(r.Private), r.ReplyMode,
		nullStr(r.StewardID), ts(r.CreatedAt))
	return err
}

// UpdateRoom writes mutable room fields under an optimistic version check.
func UpdateRoom(ctx context.Context, q Q, r protocol.Room, expectVersion int64) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE rooms SET name = ?, purpose = ?, private = ?, reply_mode = ?, steward_id = ?, archived = ?, version = version + 1
		WHERE id = ? AND version = ?`, r.Name, r.Purpose, b2i(r.Private), r.ReplyMode, nullStr(r.StewardID), b2i(r.Archived), r.ID, expectVersion)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func GetRoom(ctx context.Context, q Q, id string) (protocol.Room, error) {
	r, err := scanRoom(q.QueryRowContext(ctx, `SELECT `+roomCols+` FROM rooms WHERE id = ?`, id))
	if err != nil {
		return r, notFound(err)
	}
	return r, fillRoom(ctx, q, &r)
}

// ListRoomsForUser returns non-archived rooms the user belongs to, with
// per-user unread and mention counts.
func ListRoomsForUser(ctx context.Context, q Q, userID string) ([]protocol.Room, error) {
	rooms, err := list(ctx, q, scanRoom, `SELECT `+roomCols+` FROM rooms r
		WHERE archived = 0 AND EXISTS (SELECT 1 FROM room_memberships m WHERE m.room_id = r.id AND m.member_kind = 'user' AND m.member_id = ?)
		ORDER BY kind = 'overview' DESC, kind, name`, userID)
	if err != nil {
		return nil, err
	}
	for i := range rooms {
		if err := fillRoom(ctx, q, &rooms[i]); err != nil {
			return nil, err
		}
		if err := fillReadState(ctx, q, &rooms[i], userID); err != nil {
			return nil, err
		}
	}
	return rooms, nil
}

func fillRoom(ctx context.Context, q Q, r *protocol.Room) error {
	members, err := list(ctx, q, func(s scanner) (protocol.Member, error) {
		var m protocol.Member
		err := s.Scan(&m.Kind, &m.ID)
		return m, err
	}, `SELECT member_kind, member_id FROM room_memberships WHERE room_id = ? ORDER BY member_kind DESC, created_at`, r.ID)
	if err != nil {
		return err
	}
	r.Members = members
	r.ProjectIDs, err = stringsCol(ctx, q, `SELECT project_id FROM room_projects WHERE room_id = ?`, r.ID)
	return err
}

func fillReadState(ctx context.Context, q Q, r *protocol.Room, userID string) error {
	_ = q.QueryRowContext(ctx, `SELECT last_read_seq FROM room_reads WHERE room_id = ? AND user_id = ?`, r.ID, userID).Scan(&r.LastReadSeq)
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE room_id = ? AND thread_id IS NULL AND seq > ?
		AND deleted_at IS NULL AND NOT (author_kind = 'user' AND author_id = ?)`, r.ID, r.LastReadSeq, userID).Scan(&r.UnreadCount); err != nil {
		return err
	}
	return q.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages m JOIN mentions n ON n.message_id = m.id
		WHERE m.room_id = ? AND m.seq > ? AND n.member_kind = 'user' AND n.member_id = ? AND m.deleted_at IS NULL`,
		r.ID, r.LastReadSeq, userID).Scan(&r.MentionCount)
}

// FillReadState populates per-user read counters on a room.
func FillReadState(ctx context.Context, q Q, r *protocol.Room, userID string) error {
	return fillReadState(ctx, q, r, userID)
}

func AddMember(ctx context.Context, q Q, roomID string, m protocol.Member) (bool, error) {
	res, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO room_memberships(room_id, member_kind, member_id, created_at) VALUES (?, ?, ?, ?)`,
		roomID, m.Kind, m.ID, ts(nowUTC()))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		_, err = q.ExecContext(ctx, `UPDATE rooms SET members_version = members_version + 1 WHERE id = ?`, roomID)
	}
	return n == 1, err
}

func RemoveMember(ctx context.Context, q Q, roomID string, m protocol.Member) (bool, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM room_memberships WHERE room_id = ? AND member_kind = ? AND member_id = ?`, roomID, m.Kind, m.ID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		_, err = q.ExecContext(ctx, `UPDATE rooms SET members_version = members_version + 1 WHERE id = ?`, roomID)
	}
	return n == 1, err
}

func IsMember(ctx context.Context, q Q, roomID, kind, id string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_memberships WHERE room_id = ? AND member_kind = ? AND member_id = ?`, roomID, kind, id).Scan(&n)
	return n > 0, err
}

// MembersVersion is part of the scope fingerprint: membership changes
// invalidate cached context and provider-session reuse.
func MembersVersion(ctx context.Context, q Q, roomID string) (int64, error) {
	var v int64
	err := q.QueryRowContext(ctx, `SELECT members_version FROM rooms WHERE id = ?`, roomID).Scan(&v)
	return v, notFound(err)
}

// RoomIDsForMember lists rooms a member belongs to.
func RoomIDsForMember(ctx context.Context, q Q, kind, id string) ([]string, error) {
	return stringsCol(ctx, q, `SELECT room_id FROM room_memberships WHERE member_kind = ? AND member_id = ?`, kind, id)
}

func SetRoomProjects(ctx context.Context, q Q, roomID string, projectIDs []string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM room_projects WHERE room_id = ?`, roomID); err != nil {
		return err
	}
	for _, p := range projectIDs {
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO room_projects(room_id, project_id) VALUES (?, ?)`, roomID, p); err != nil {
			return err
		}
	}
	_, err := q.ExecContext(ctx, `UPDATE rooms SET members_version = members_version + 1 WHERE id = ?`, roomID)
	return err
}

// NextRoomSeq allocates the next message sequence number in a room.
func NextRoomSeq(ctx context.Context, q Q, roomID string) (int64, error) {
	var seq int64
	err := q.QueryRowContext(ctx, `UPDATE rooms SET last_seq = last_seq + 1 WHERE id = ? RETURNING last_seq`, roomID).Scan(&seq)
	return seq, notFound(err)
}

func SetRead(ctx context.Context, q Q, roomID, userID string, seq int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO room_reads(room_id, user_id, last_read_seq) VALUES (?, ?, ?)
		ON CONFLICT(room_id, user_id) DO UPDATE SET last_read_seq = MAX(last_read_seq, excluded.last_read_seq)`, roomID, userID, seq)
	return err
}

// OverviewRoomID returns the owner's personal Overview conversation.
func OverviewRoomID(ctx context.Context, q Q, userID string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT r.id FROM rooms r JOIN room_memberships m ON m.room_id = r.id
		WHERE r.kind = 'overview' AND m.member_kind = 'user' AND m.member_id = ? LIMIT 1`, userID).Scan(&id)
	return id, notFound(err)
}
