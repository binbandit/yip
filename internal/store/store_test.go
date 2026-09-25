package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndSupportsFTS(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := s.SchemaVersion(ctx)
	if err != nil || v < 2 {
		t.Fatalf("schema version %d err %v", v, err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO messages_fts(body, message_id, room_id) VALUES ('expired session refresh', 'm1', 'r1')`); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.r.QueryRowContext(ctx, `SELECT message_id FROM messages_fts WHERE messages_fts MATCH 'sessions'`).Scan(&id); err != nil || id != "m1" {
		t.Fatalf("fts porter match: %q %v", id, err)
	}
	if err := IntegrityCheck(ctx, s.R()); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := IntegrityCheck(ctx, b.R()); err != nil {
		t.Fatal(err)
	}
}
