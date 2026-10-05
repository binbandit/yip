package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/binbandit/yip/protocol"
)

func TestRoomOrderMigrationPreservesPreferences(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hub.db")
	db, err := sql.Open("sqlite", dsn(path, "immediate"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		version, err := migrationVersion(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if version >= 17 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations VALUES (?, ?, ?)`, version, entry.Name(), TS(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO orgs(id, name, created_at) VALUES ('org', 'Test', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	preferences := protocol.Preferences{Theme: "night", Notify: "mentions", MutedRoomIDs: []string{"muted-room"}}
	if err := InsertUser(ctx, db, UserRow{User: protocol.User{ID: "user", OrgID: "org", Name: "User", Handle: "user", CreatedAt: time.Now()}, Preferences: preferences}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fmt.Sprintf("%s.pre-schema-%d.bak", path, version)); err != nil {
		t.Fatalf("missing upgrade backup: %v", err)
	}
	u, err := GetUser(ctx, s.R(), "user")
	if err != nil || !reflect.DeepEqual(u.Preferences, preferences) {
		t.Fatalf("upgrade changed preferences: %+v, %v", u.Preferences, err)
	}
	for _, kind := range []string{"room", "dm"} {
		order, err := GetRoomOrderSection(ctx, s.R(), "user", kind)
		if err != nil || order.Version != 0 || order.RoomIDs == nil || len(order.RoomIDs) != 0 {
			t.Fatalf("initial %s order: %+v, %v", kind, order, err)
		}
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			ok, err := SetRoomOrderSection(ctx, tx, "user", kind, 0, []string{"first", "second"})
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("first save was rejected")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := IntegrityCheck(ctx, s.R()); err != nil {
		t.Fatal(err)
	}
}
