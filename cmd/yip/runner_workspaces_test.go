package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binbandit/yip/internal/runner"
)

func TestHistoricalIdentityWorkspaceManagement(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "identity-old")
	current := filepath.Join(root, "identity-current")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "node.json"), `{"nodeId":"current","stateDir":"identity-current"}`)
	write(filepath.Join(old, "node.json"), `{"nodeId":"old","stateRoot":".."}`)
	write(filepath.Join(current, "node.json"), `{"nodeId":"current","stateRoot":".."}`)
	write(filepath.Join(old, "work", "selected", "changes.txt"), "old unpublished work")
	write(filepath.Join(old, "work", "other", "changes.txt"), "other unpublished work")
	write(filepath.Join(current, "work", "current", "changes.txt"), "current unpublished work")
	journal := []byte("historical journal must not be opened or recovered")
	write(filepath.Join(old, "journal.db"), string(journal))
	if got, err := activeRunnerState(root); err != nil || got != current {
		t.Fatalf("root selected wrong storage: %q, %v", got, err)
	}
	if err := runWorkspaces([]string{"--state", old}); err != nil {
		t.Fatalf("cannot inspect preserved historical work: %v", err)
	}
	for _, name := range []string{".", ".."} {
		if err := runCleanup([]string{"--state", old, "--workspace", name, "--confirm", name}); err == nil {
			t.Fatalf("cleanup accepted %q, which selects a whole storage directory", name)
		}
	}
	if err := runCleanup([]string{"--state", old, "--workspace", "selected"}); err == nil {
		t.Fatal("historical cleanup must still require explicit confirmation")
	}
	if err := runCleanup([]string{"--state", old, "--workspace", "selected", "--confirm", "selected"}); err != nil {
		t.Fatalf("cannot explicitly clean selected historical workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(old, "work", "selected")); !os.IsNotExist(err) {
		t.Fatal("selected historical workspace was not removed")
	}
	for _, path := range []string{filepath.Join(old, "work", "other", "changes.txt"), filepath.Join(current, "work", "current", "changes.txt")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cleanup touched another workspace: %v", err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(old, "journal.db")); err != nil || !bytes.Equal(got, journal) {
		t.Fatal("workspace management changed the historical journal")
	}
	if _, err := runner.New(runner.Options{StateDir: old}); err == nil || !strings.Contains(err.Error(), "generated identity storage") {
		t.Fatalf("historical inspection must not permit direct runner construction: %v", err)
	}
	if _, err := runner.RePair(t.Context(), old, "https://unused.invalid", "unused", "unused", "old"); err == nil || !strings.Contains(err.Error(), "generated identity storage") {
		t.Fatalf("historical inspection must not permit pairing in generated storage: %v", err)
	}
}
