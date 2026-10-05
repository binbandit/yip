package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/binbandit/yip/protocol"
)

func TestNodeRemovalMigrationPreservesExistingMachines(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hub.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
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
	// Restore the previous schema while retaining its existing machines.
	for _, query := range []string{`ALTER TABLE nodes DROP COLUMN removed_at`, `DELETE FROM schema_migrations WHERE version = 15`} {
		if _, err := s.w.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".pre-schema-15.bak"); err != nil {
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
