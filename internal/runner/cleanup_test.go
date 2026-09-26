package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A workspace's size is reported in bytes with whether it was measured and
// whether it is only a lower bound, so a tiny workspace is not "0 MB" and an
// unmeasured one is not zero.
func TestMeasureDir(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, n int) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Repeat("x", n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.txt", 1500)
	write("sub/deeper/a.go", 500)

	total, known, approx := measureDir(dir)
	if total != 2000 || !known || approx {
		t.Fatalf("a small tree should be measured exactly: %d known=%v approx=%v", total, known, approx)
	}

	// Stopping early leaves a lower bound, flagged as such.
	old := sizeWalkLimit
	sizeWalkLimit = 2
	defer func() { sizeWalkLimit = old }()
	total, known, approx = measureDir(dir)
	if !known || !approx || total >= 2000 {
		t.Fatalf("a capped walk should be a flagged lower bound: %d known=%v approx=%v", total, known, approx)
	}
	sizeWalkLimit = old

	// Nothing to measure is unknown, not zero.
	if total, known, _ := measureDir(filepath.Join(dir, "missing")); known || total != 0 {
		t.Fatalf("a missing directory must not be reported as measured: %d known=%v", total, known)
	}

	// A subdirectory that can't be read makes the total a lower bound.
	if os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		write("locked/secret.bin", 4096)
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(locked, 0o755)
		total, known, approx = measureDir(dir)
		if !known || !approx || total != 2000 {
			t.Fatalf("an unreadable part should make the size a lower bound: %d known=%v approx=%v", total, known, approx)
		}
	}
}

func TestListWorkspacesReportsKindsAndSizes(t *testing.T) {
	r := &Runner{paths: Paths{t.TempDir()}}
	for _, name := range []string{"scratch-aaaaaaaaaaaa", "review-bbbbbbbbbbbb", "not-a-workspace"} {
		if err := os.MkdirAll(filepath.Join(r.paths.work(), name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.paths.work(), "scratch-aaaaaaaaaaaa", "draft.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range r.listWorkspaces(t.Context()) {
		got[w.Name] = w.Kind
		if w.Name == "scratch-aaaaaaaaaaaa" && (w.SizeBytes != 5 || !w.SizeKnown || w.SizeApprox || w.SizeMB != 0) {
			t.Fatalf("a five-byte scratch space: %+v", w)
		}
	}
	if len(got) != 2 || got["scratch-aaaaaaaaaaaa"] != "scratch" || got["review-bbbbbbbbbbbb"] != "review" {
		t.Fatalf("workspace kinds: %v", got)
	}
}
