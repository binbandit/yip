package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/binbandit/yip/protocol"
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

func TestSearchDecisionsFiltersVisibilityBeforeRanking(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.w.ExecContext(ctx, `INSERT INTO orgs(id, name, created_at) VALUES ('o1', 'Org', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	add := func(id string, rooms []string) {
		d := protocol.Decision{ID: id, Scope: protocol.DecisionScope{Kind: "org"}, Title: "Session expiry " + id, Body: "strict expiry",
			Status: "accepted", CreatedBy: protocol.Actor{Kind: protocol.ActorUser, ID: "u1"}, VisibleRoomIDs: rooms, CreatedAt: time.Now()}
		if err := InsertDecision(ctx, s.w, "o1", d); err != nil {
			t.Fatal(err)
		}
	}
	add("private", []string{"secret-room"})
	add("shared", []string{"room-a"})
	add("open", nil)
	ids, err := SearchDecisions(ctx, s.r, "expiry", []string{"room-a"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["shared"] || !got["open"] || got["private"] {
		t.Fatalf("visibility must apply inside the search: %v", ids)
	}
	// With the limit at 1 a private match can't take the only slot.
	ids, _ = SearchDecisions(ctx, s.r, "expiry", nil, 5)
	if len(ids) != 1 || ids[0] != "open" {
		t.Fatalf("without rooms only unrestricted decisions match: %v", ids)
	}
}

func TestOpenForgetsScriptedProviderAccounts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hub.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO orgs(id, name, created_at) VALUES ('o1', 'Org', '2026-01-01T00:00:00Z')`,
		`INSERT INTO provider_profiles(id, org_id, provider, label, created_at) VALUES ('fake:local', 'o1', 'fake', 'Scripted', '2026-01-01T00:00:00Z')`,
		`INSERT INTO provider_profiles(id, org_id, provider, label, created_at) VALUES ('codex:default', 'o1', 'codex', 'Codex', '2026-01-01T00:00:00Z')`,
		`DELETE FROM schema_migrations WHERE version = 13`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if s, err = Open(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var ids []string
	rows, err := s.r.QueryContext(ctx, `SELECT id FROM provider_profiles ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if strings.Join(ids, ",") != "codex:default" {
		t.Fatalf("only the scripted provider's account should be forgotten: %v", ids)
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hub.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.SchemaVersion(ctx)
	if _, err := s.w.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, 'from_the_future.sql', '2027-01-01T00:00:00Z')`, v+1); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "upgrade yip") {
		t.Fatalf("a newer schema must be refused, got %v", err)
	}
}
