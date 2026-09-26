// Package store owns the hub's SQLite database: connection setup, explicit
// migrations, transactions, and the SQL for each aggregate.
//
// The hub is the single writer. A dedicated one-connection pool serializes
// writes with BEGIN IMMEDIATE; a separate pool serves concurrent WAL readers.
// The database must stay on the hub's local disk — never on a network
// filesystem shared with runners.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Q is satisfied by *sql.DB and *sql.Tx.
type Q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store is the hub database.
type Store struct {
	path string
	w    *sql.DB
	r    *sql.DB
}

func dsn(path string, txlock string) string {
	v := url.Values{}
	v.Add("_pragma", "journal_mode(WAL)")
	v.Add("_pragma", "busy_timeout(10000)")
	v.Add("_pragma", "foreign_keys(1)")
	v.Add("_pragma", "synchronous(NORMAL)")
	if txlock != "" {
		v.Add("_txlock", txlock)
	}
	return "file:" + path + "?" + v.Encode()
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", dsn(path, "immediate"))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	r, err := sql.Open("sqlite", dsn(path, ""))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	s := &Store{path: path, w: w, r: r}
	if err := s.migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return s, nil
}

func (s *Store) Close() error {
	var errs []error
	if s.r != nil {
		errs = append(errs, s.r.Close())
	}
	if s.w != nil {
		errs = append(errs, s.w.Close())
	}
	return errors.Join(errs...)
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// R returns the read pool.
func (s *Store) R() Q { return s.r }

// Tx runs fn in a write transaction. Every mutation, its event, and any
// outbox item are persisted together or not at all.
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	latest := 0
	for _, e := range entries {
		if n, err := migrationVersion(e.Name()); err == nil && n > latest {
			latest = n
		}
	}
	var cur int
	if err := s.w.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&cur); err != nil {
		return err
	}
	// Never open a database a newer yip has already migrated: this build
	// doesn't know that schema and could damage it.
	if cur > latest {
		return fmt.Errorf("this database uses schema %d, but this yip only knows up to %d; upgrade yip before opening it", cur, latest)
	}
	// Before changing an existing database's schema, keep a consistent copy
	// next to it so an upgrade can be rolled back.
	if cur > 0 && cur < latest {
		var file string
		_ = s.w.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file)
		if file != "" {
			bak := fmt.Sprintf("%s.pre-schema-%d.bak", file, latest)
			if _, err := os.Stat(bak); errors.Is(err, os.ErrNotExist) {
				if _, err := s.w.ExecContext(ctx, `VACUUM INTO ?`, bak); err != nil {
					return fmt.Errorf("back up before migrating: %w", err)
				}
			}
		}
	}
	for _, e := range entries {
		name := e.Name()
		n, err := migrationVersion(name)
		if err != nil {
			return fmt.Errorf("migration %s: bad version prefix", name)
		}
		var exists int
		if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, n).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		err = s.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`, n, name, ts(nowUTC()))
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// migrationVersion reads the numeric prefix of a migration file name.
func migrationVersion(name string) (int, error) {
	v, _, _ := strings.Cut(name, "_")
	return strconv.Atoi(v)
}

// SchemaVersion returns the highest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}

// Backup writes a transactionally consistent copy of the live database to
// dest using SQLite's VACUUM INTO, which is safe while WAL is active. Copying
// only the main database file is not a valid backup.
func (s *Store) Backup(ctx context.Context, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("backup destination %s already exists", dest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `VACUUM INTO ?`, dest)
	return err
}

// IntegrityCheck runs PRAGMA integrity_check.
func IntegrityCheck(ctx context.Context, q Q) error {
	rows, err := q.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("integrity check failed: %s", strings.Join(problems, "; "))
	}
	return rows.Err()
}

// Setting reads a hub setting.
func Setting(ctx context.Context, q Q, key string) (string, bool, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetSetting writes a hub setting.
func SetSetting(ctx context.Context, q Q, key, value string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
