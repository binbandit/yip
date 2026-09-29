package runner

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func TestArtifactSnapshotIsRootedAndImmutable(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	file := filepath.Join(root, "report.md")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	copy, err := snapshotArtifact(root, "report.md")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(copy)
	if err := os.WriteFile(file, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(copy); err != nil || string(b) != "original" {
		t.Fatalf("not an immutable snapshot: %q %v", b, err)
	}
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("host-only-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../private", outside, "escape/private", ".", "pipe"} {
		if copy, err := snapshotArtifact(root, path); err == nil {
			os.Remove(copy)
			t.Errorf("accepted unsafe artifact %q", path)
		}
	}
}

func TestArtifactSnapshotRejectsConcurrentSymlinkSwap(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	safe := filepath.Join(root, "safe")
	if err := os.WriteFile(safe, []byte("workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(outside, "private")
	if err := os.WriteFile(private, []byte("host-only-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			temp, target := filepath.Join(root, "next"), filepath.Join(root, "target")
			_ = os.Symlink("safe", temp)
			_ = os.Rename(temp, target)
			_ = os.Symlink(private, temp)
			_ = os.Rename(temp, target)
		}
	}()
	defer func() { close(stop); wg.Wait() }()
	for range 200 {
		path, err := snapshotArtifact(root, "target")
		if err != nil {
			continue
		}
		b, err := os.ReadFile(path)
		os.Remove(path)
		if err != nil || string(b) != "workspace" {
			t.Fatalf("artifact escaped workspace: %q %v", b, err)
		}
	}
}
