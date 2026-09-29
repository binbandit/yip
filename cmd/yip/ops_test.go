package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binbandit/yip/internal/demo"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/internal/store"
)

// An encrypted backup restores to the same verified state; the file holds no
// readable key material or database, and a wrong passphrase restores nothing.
func TestEncryptedBackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	data := filepath.Join(dir, "hub")
	h, err := hub.Open(ctx, hub.Config{DataDir: data, Version: "test", RunnerURL: "https://127.0.0.1:1", Demo: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	repos, err := demo.MaterializeRepos(filepath.Join(dir, "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := demo.Seed(ctx, h, repos, "Owner", "owner", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	h.Close()

	legacy := filepath.Join(dir, "root-only")
	if _, err := writeBackup(data, legacy); err != nil {
		t.Fatal(err)
	}
	legacyJSON, err := os.ReadFile(filepath.Join(legacy, "backup.json"))
	if err != nil || strings.Contains(string(legacyJSON), `"workspaces"`) {
		t.Fatalf("root-only format changed: %v", err)
	}
	if err := runRestore([]string{"--from", legacy, "--data", filepath.Join(dir, "root-only-restored")}); err != nil {
		t.Fatal(err)
	}
	childID := "19a18e09-1a43-4b8d-a3db-9be72c503261"
	child := filepath.Join(data, "workspaces", childID)
	for _, name := range []string{"hub.db", "hub.key", "artifacts"} {
		if err := copyTree(filepath.Join(data, name), filepath.Join(child, name)); err != nil {
			t.Fatal(err)
		}
	}
	live, err := store.Open(ctx, filepath.Join(child, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	artifacts, err := hub.NewArtifactStore(filepath.Join(child, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	hash, size, err := artifacts.PutStream(strings.NewReader("child artifact"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts
			(id, org_id, hash, name, content_type, size, kind, verified, created_at)
			SELECT 'backup-test', id, ?, 'test', 'text/plain', ?, 'test', 1, '2026-01-01T00:00:00Z' FROM orgs LIMIT 1`, hash, size); err != nil {
			return err
		}
		return store.SetSetting(ctx, tx, "workspace_marker", "child-only")
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "workspaces", "."+childID), 0o700); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain")
	m, err := writeBackup(data, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Workspaces) != 1 || len(m.Workspaces[childID].Artifacts) == 0 {
		t.Fatalf("missing child snapshot or artifacts: %+v", m.Workspaces)
	}
	if err := runRestore([]string{"--from", plain, "--data", filepath.Join(dir, "plain-restored")}); err != nil {
		t.Fatal(err)
	}

	pass := filepath.Join(dir, "pass")
	if err := os.WriteFile(pass, []byte("a long test passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(dir, "hub.yipenc")
	if err := runBackup([]string{"--data", data, "--out", enc, "--encrypt", "--passphrase-file", pass}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(enc)
	for _, leak := range []string{"PRIVATE KEY", "SQLite format", "correct-horse"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("encrypted backup contains %q in the clear", leak)
		}
	}
	if err := runBackup([]string{"--data", data, "--out", enc, "--encrypt", "--passphrase-file", pass}); err == nil {
		t.Fatalf("an existing backup file must not be overwritten")
	}

	wrong := filepath.Join(dir, "wrong")
	if err := os.WriteFile(wrong, []byte("not the passphrase at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRestore([]string{"--from", enc, "--data", filepath.Join(dir, "bad"), "--passphrase-file", wrong}); err == nil {
		t.Fatalf("a wrong passphrase must not restore")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad")); !os.IsNotExist(err) {
		t.Fatalf("a failed restore must leave nothing behind")
	}
	if err := runRestore([]string{"--from", enc, "--data", filepath.Join(dir, "restored"), "--passphrase-file", pass}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hub.db", "hub.key", "pki"} {
		if _, err := os.Stat(filepath.Join(dir, "restored", f)); err != nil {
			t.Fatalf("restored backup is missing %s: %v", f, err)
		}
	}
	restoredChild := filepath.Join(dir, "restored", "workspaces", childID)
	restored, err := store.Open(ctx, filepath.Join(restoredChild, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if value, ok, err := store.Setting(ctx, restored.R(), "workspace_marker"); err != nil || !ok || value != "child-only" {
		t.Fatalf("child WAL state was not restored: %q, %v, %v", value, ok, err)
	}
	for _, base := range []string{child, filepath.Join(plain, "workspaces", childID), restoredChild} {
		if _, err := os.Stat(filepath.Join(base, "pki")); !os.IsNotExist(err) {
			t.Fatalf("child CA must not be created: %s (%v)", base, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "restored", "workspaces", "."+childID)); !os.IsNotExist(err) {
		t.Fatal("staging workspace was included")
	}
	backupArtifacts, err := hub.NewArtifactStore(filepath.Join(plain, "workspaces", childID, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	artifactPath, err := backupArtifacts.Path(hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRestore([]string{"--from", plain, "--data", filepath.Join(dir, "bad-artifact")}); err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Fatalf("child artifact corruption not rejected: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("child artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	childManifest := m.Workspaces[childID]
	childManifest.Counts["rooms"]++
	m.Workspaces[childID] = childManifest
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "backup.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRestore([]string{"--from", plain, "--data", filepath.Join(dir, "bad-count")}); err == nil || !strings.Contains(err.Error(), "count") {
		t.Fatalf("child count corruption not rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plain, "workspaces", childID, "hub.db"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runRestore([]string{"--from", plain, "--data", filepath.Join(dir, "bad-db")}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("child database corruption not rejected: %v", err)
	}
}
