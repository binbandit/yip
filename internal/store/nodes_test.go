package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/binbandit/yip/protocol"
)

func TestNodeRemovalMigrationPreservesExistingMachines(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hub.db")
	db, err := sql.Open("sqlite", dsn(path, "immediate"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{w: db}
	defer func() { s.Close() }()
	// Build the actual schema before node removal instead of rolling back
	// one migration from the current schema and retaining newer records.
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
		if version >= 15 {
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
	if _, err := s.w.ExecContext(ctx, `INSERT INTO orgs(id, name, created_at) VALUES ('org', 'Test', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"active", "revoked"} {
		if err := InsertNode(ctx, s.w, "org", NodeRow{Node: protocol.Node{ID: id, Name: id, CreatedAt: time.Now()}, CertSerial: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := RevokeNode(ctx, s.w, "revoked"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fmt.Sprintf("%s.pre-schema-%d.bak", path, version)); err != nil {
		t.Fatalf("missing pre-upgrade backup: %v", err)
	}
	nodes, err := ListNodes(ctx, s.R())
	if err != nil || len(nodes) != 2 {
		t.Fatalf("upgrade hid existing machines: %+v %v", nodes, err)
	}
	for _, node := range nodes {
		if node.RemovedAt != nil || (node.ID == "revoked" && node.RevokedAt == nil) {
			t.Fatalf("upgrade changed lifecycle: %+v", node)
		}
	}
}
