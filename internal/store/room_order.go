package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/binbandit/yip/protocol"
)

func GetRoomOrderSection(ctx context.Context, q Q, userID, kind string) (protocol.RoomOrderSection, error) {
	out := protocol.RoomOrderSection{RoomIDs: []string{}}
	var ids string
	err := q.QueryRowContext(ctx, `SELECT version, room_ids FROM user_room_orders WHERE user_id = ? AND kind = ?`, userID, kind).Scan(&out.Version, &ids)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(ids), &out.RoomIDs)
	return out, err
}

// SetRoomOrderSection updates only the section at its expected version.
func SetRoomOrderSection(ctx context.Context, q Q, userID, kind string, version int64, ids []string) (bool, error) {
	if ids == nil {
		ids = []string{}
	}
	if version == 0 {
		return oneRow(q.ExecContext(ctx, `INSERT OR IGNORE INTO user_room_orders(user_id, kind, version, room_ids) VALUES (?, ?, 1, ?)`, userID, kind, js(ids)))
	}
	return oneRow(q.ExecContext(ctx, `UPDATE user_room_orders SET room_ids = ?, version = version + 1 WHERE user_id = ? AND kind = ? AND version = ?`, js(ids), userID, kind, version))
}
