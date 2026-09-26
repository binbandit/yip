package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// IdemRecord is a stored request under an idempotency key.
type IdemRecord struct {
	Method, Path, RequestHash, State string
	Status                           int
	Body                             []byte
}

// BeginIdempotent claims key for a request. It returns (nil, nil) when the
// caller should run the request, or the existing record when the key was
// already used (by this or a different request).
func BeginIdempotent(ctx context.Context, q Q, userID, key, method, path, hash string, now time.Time) (*IdemRecord, error) {
	_, _ = q.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at < ?`, ts(now.Add(-24*time.Hour)))
	res, err := q.ExecContext(ctx, `INSERT INTO idempotency_keys(user_id, key, method, path, request_hash, state, created_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?) ON CONFLICT(user_id, key) DO NOTHING`, userID, key, method, path, hash, ts(now))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil, nil
	}
	var r IdemRecord
	var body []byte
	err = q.QueryRowContext(ctx, `SELECT method, path, request_hash, state, status, body FROM idempotency_keys WHERE user_id = ? AND key = ?`,
		userID, key).Scan(&r.Method, &r.Path, &r.RequestHash, &r.State, &r.Status, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	r.Body = body
	return &r, err
}

// FinishIdempotent records the response for a claimed key.
func FinishIdempotent(ctx context.Context, q Q, userID, key string, status int, body []byte) error {
	_, err := q.ExecContext(ctx, `UPDATE idempotency_keys SET state = 'done', status = ?, body = ? WHERE user_id = ? AND key = ?`,
		status, body, userID, key)
	return err
}

// ReleaseIdempotent forgets a claimed key (the request failed on the
// server's side and may be retried as new).
func ReleaseIdempotent(ctx context.Context, q Q, userID, key string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE user_id = ? AND key = ? AND state = 'pending'`, userID, key)
	return err
}
