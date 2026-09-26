package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binbandit/yip/internal/demo"
	"github.com/binbandit/yip/internal/hub"
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
}
