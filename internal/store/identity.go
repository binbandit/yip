package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/binbandit/yip/protocol"
)

// ErrNotFound is returned by Get* functions when no row matches.
var ErrNotFound = errors.New("not found")

type scanner interface{ Scan(dest ...any) error }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// each runs query and calls fn for every row.
func each(ctx context.Context, q Q, fn func(scanner) error, query string, args ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func list[T any](ctx context.Context, q Q, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	out := []T{}
	err := each(ctx, q, func(s scanner) error {
		v, err := scan(s)
		out = append(out, v)
		return err
	}, query, args...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// getFilled reads one row and completes it with fill.
func getFilled[T any](ctx context.Context, q Q, scan func(scanner) (T, error), fill func(context.Context, Q, *T) error, query string, args ...any) (T, error) {
	v, err := scan(q.QueryRowContext(ctx, query, args...))
	if err != nil {
		return v, notFound(err)
	}
	return v, fill(ctx, q, &v)
}

// listFilled lists rows and completes each one with fill.
func listFilled[T any](ctx context.Context, q Q, scan func(scanner) (T, error), fill func(context.Context, Q, *T) error, query string, args ...any) ([]T, error) {
	out, err := list(ctx, q, scan, query, args...)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err := fill(ctx, q, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// oneRow reports whether an exec changed exactly one row.
func oneRow(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// InClause returns an "(?,?,…)" placeholder list for n values.
func InClause(n int) string {
	return "(" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")"
}

// inList returns the placeholder list for vs, and vs as query arguments.
func inList[T ~string](vs []T) (string, []any) {
	args := make([]any, len(vs))
	for i, v := range vs {
		args[i] = string(v)
	}
	return InClause(len(vs)), args
}

// whereClause returns " WHERE cond", or nothing when cond is empty.
func whereClause(cond string) string {
	if cond == "" {
		return ""
	}
	return " WHERE " + cond
}

func stringsCol(ctx context.Context, q Q, query string, args ...any) ([]string, error) {
	return list(ctx, q, func(s scanner) (string, error) {
		var v string
		err := s.Scan(&v)
		return v, err
	}, query, args...)
}

// ---- orgs ----

func InsertOrg(ctx context.Context, q Q, o protocol.Org) error {
	_, err := q.ExecContext(ctx, `INSERT INTO orgs(id, name, created_at) VALUES (?, ?, ?)`, o.ID, o.Name, ts(o.CreatedAt))
	return err
}

func FirstOrg(ctx context.Context, q Q) (protocol.Org, error) {
	var o protocol.Org
	var created string
	err := q.QueryRowContext(ctx, `SELECT id, name, created_at FROM orgs ORDER BY created_at LIMIT 1`).Scan(&o.ID, &o.Name, &created)
	o.CreatedAt = parseTS(created)
	return o, notFound(err)
}

// ---- users ----

type UserRow struct {
	protocol.User
	PasswordHash string
	Preferences  protocol.Preferences
}

const userCols = `id, org_id, name, handle, COALESCE(avatar_id, ''), password_hash, preferences, created_at`

func scanUser(s scanner) (UserRow, error) {
	var u UserRow
	var prefs, created string
	err := s.Scan(&u.ID, &u.OrgID, &u.Name, &u.Handle, &u.AvatarID, &u.PasswordHash, &prefs, &created)
	unjs(prefs, &u.Preferences)
	u.CreatedAt = parseTS(created)
	return u, err
}

func InsertUser(ctx context.Context, q Q, u UserRow) error {
	_, err := q.ExecContext(ctx, `INSERT INTO users(id, org_id, name, handle, password_hash, preferences, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, u.ID, u.OrgID, u.Name, u.Handle, u.PasswordHash, js(u.Preferences), ts(u.CreatedAt))
	return err
}

func GetUser(ctx context.Context, q Q, id string) (UserRow, error) {
	u, err := scanUser(q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	return u, notFound(err)
}

func GetUserByHandle(ctx context.Context, q Q, handle string) (UserRow, error) {
	u, err := scanUser(q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE handle = ?`, handle))
	return u, notFound(err)
}

func ListUsers(ctx context.Context, q Q) ([]UserRow, error) {
	return list(ctx, q, scanUser, `SELECT `+userCols+` FROM users ORDER BY created_at`)
}

func CountUsers(ctx context.Context, q Q) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func SetUserPassword(ctx context.Context, q Q, id, hash string) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET password_hash = ?, version = version + 1 WHERE id = ?`, hash, id)
	return err
}

func SetUserName(ctx context.Context, q Q, id, name string) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET name = ?, version = version + 1 WHERE id = ?`, name, id)
	return err
}

func SetUserPreferences(ctx context.Context, q Q, id string, p protocol.Preferences) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET preferences = ?, version = version + 1 WHERE id = ?`, js(p), id)
	return err
}

// SetUserAvatar sets (or, with "", clears) the user's picture.
func SetUserAvatar(ctx context.Context, q Q, id, artifactID string) (bool, error) {
	return oneRow(q.ExecContext(ctx, `UPDATE users SET avatar_id = ?, version = version + 1 WHERE id = ?`, nullStr(artifactID), id))
}

// ---- sessions ----

type Session struct {
	ID         string
	UserID     string
	CSRFToken  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
}

func InsertSession(ctx context.Context, q Q, s Session, userAgent string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO sessions(id, user_id, csrf_token, created_at, expires_at, last_seen_at, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, s.ID, s.UserID, s.CSRFToken, ts(s.CreatedAt), ts(s.ExpiresAt), ts(s.LastSeenAt), userAgent)
	return err
}

func GetSession(ctx context.Context, q Q, id string) (Session, error) {
	var s Session
	var created, expires, seen string
	var revoked sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id, user_id, csrf_token, created_at, expires_at, last_seen_at, revoked_at
		FROM sessions WHERE id = ?`, id).Scan(&s.ID, &s.UserID, &s.CSRFToken, &created, &expires, &seen, &revoked)
	s.CreatedAt, s.ExpiresAt, s.LastSeenAt, s.RevokedAt = parseTS(created), parseTS(expires), parseTS(seen), parseTSP(revoked)
	return s, notFound(err)
}

func TouchSession(ctx context.Context, q Q, id string, seen, expires time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`, ts(seen), ts(expires), id)
	return err
}

func RevokeSession(ctx context.Context, q Q, id string) error {
	_, err := q.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, ts(nowUTC()), id)
	return err
}

func RevokeUserSessions(ctx context.Context, q Q, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, ts(nowUTC()), userID)
	return err
}

// ---- bootstrap secrets ----

func InsertBootstrapSecret(ctx context.Context, q Q, hash string, expires time.Time) error {
	_, err := q.ExecContext(ctx, `INSERT INTO bootstrap_secrets(hash, created_at, expires_at) VALUES (?, ?, ?)`, hash, ts(nowUTC()), ts(expires))
	return err
}

// ConsumeBootstrapSecret marks an unexpired, unused secret as used. It returns
// false when the secret is unknown, expired, or already used.
func ConsumeBootstrapSecret(ctx context.Context, q Q, hash string, now time.Time) (bool, error) {
	return oneRow(q.ExecContext(ctx, `UPDATE bootstrap_secrets SET used_at = ?
		WHERE hash = ? AND used_at IS NULL AND expires_at > ?`, ts(now), hash, ts(now)))
}

// ---- audit ----

type Audit struct {
	OrgID     string
	Actor     protocol.Actor
	Authority string
	Action    string
	Target    string
	Result    string
	Detail    string
}

func InsertAudit(ctx context.Context, q Q, id string, a Audit) error {
	_, err := q.ExecContext(ctx, `INSERT INTO audit_entries(id, org_id, actor_kind, actor_id, authority, action, target, result, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, a.OrgID, a.Actor.Kind, a.Actor.ID, a.Authority, a.Action, a.Target, a.Result, a.Detail, ts(nowUTC()))
	return err
}
