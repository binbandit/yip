package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binbandit/yip/protocol"
)

// fakeGH puts a `gh` on PATH that answers `gh auth git-credential get` with
// a token, as a signed-in GitHub CLI does.
func fakeGH(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1 $2 $3\" = \"auth git-credential get\" ]; then cat >/dev/null; printf 'username=x-access-token\\npassword=gh-token\\n'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(bin, "gh")
}

func run(t *testing.T, dir string, env []string, stdin string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// A private GitHub repository added as owner/name clones over HTTPS with the
// GitHub CLI's sign-in on the machine, without `gh auth setup-git`; its
// fetches and pushes use it too, while checks still get no credential.
func TestGitHubReplicaUsesGHSignIn(t *testing.T) {
	gh := fakeGH(t)
	tmp := t.TempDir()
	// The "GitHub" remote is a local repository reached through insteadOf in
	// the owner's git configuration, so nothing leaves the machine.
	src := filepath.Join(tmp, "src")
	run(t, tmp, nil, "", "git", "init", "--quiet", "--initial-branch=main", src)
	run(t, src, nil, "", "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "--allow-empty", "-m", "first")
	origin := filepath.Join(tmp, "private.git")
	run(t, tmp, nil, "", "git", "clone", "--quiet", "--bare", src, origin)
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	remote := "https://github.com/acme/private.git"
	gitconfig := "[url \"file://" + origin + "\"]\n\tinsteadOf = " + remote + "\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	w := newWorkspaces(Paths{Dir: filepath.Join(tmp, "state")})
	rep, err := w.replica(context.Background(), protocol.RepoSpec{RepoID: "r1", Name: "private", RemoteURL: remote, DefaultBranch: "main"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := defaultHead(context.Background(), rep, "main"); err != nil {
		t.Fatalf("the replica should hold main: %v", err)
	}
	helper := strings.TrimSpace(run(t, rep, nil, "", "git", "config", "--local", "--get-all", ghCredentialKey))
	if want := "!'" + gh + "' auth git-credential"; helper != want {
		t.Fatalf("replica credential helper = %q, want %q", helper, want)
	}
	// A second preparation leaves exactly one helper.
	if _, err := w.replica(context.Background(), protocol.RepoSpec{RepoID: "r1", Name: "private", RemoteURL: remote, DefaultBranch: "main"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run(t, rep, nil, "", "git", "config", "--local", "--get-all", ghCredentialKey)); got != helper {
		t.Fatalf("helper should be set once, got %q", got)
	}

	ask := "protocol=https\nhost=github.com\npath=acme/private.git\n\n"
	if out := run(t, rep, gitEnv(), ask, "git", "credential", "fill"); !strings.Contains(out, "password=gh-token") {
		t.Fatalf("git in the replica should get the gh credential:\n%s", out)
	}
	env, err := (&Runner{paths: Paths{Dir: filepath.Join(tmp, "state")}}).checkEnv("run1")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "credential", "fill")
	cmd.Dir, cmd.Env, cmd.Stdin = rep, env, strings.NewReader(ask)
	if out, _ := cmd.CombinedOutput(); strings.Contains(string(out), "gh-token") {
		t.Fatalf("a check must not reach the gh credential:\n%s", out)
	}
}

func TestGHCredentialHelperScope(t *testing.T) {
	gh := fakeGH(t)
	if got, want := ghCredentialHelper("https://github.com/acme/atlas.git"), "!'"+gh+"' auth git-credential"; got != want {
		t.Fatalf("github.com HTTPS remote: %q, want %q", got, want)
	}
	for _, remote := range []string{"git@github.com:acme/atlas.git", "https://gitlab.com/acme/atlas.git", "/srv/git/atlas.git", "https://github.example.com/acme/atlas.git"} {
		if got := ghCredentialHelper(remote); got != "" {
			t.Errorf("%s should not use the gh helper, got %q", remote, got)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if got := ghCredentialHelper("https://github.com/acme/atlas.git"); got != "" {
		t.Fatalf("without gh installed there is no helper, got %q", got)
	}
}

func TestCloneHint(t *testing.T) {
	denied := errors.New("git clone: exit status 128: fatal: could not read Username for 'https://github.com': terminal prompts disabled")
	if h := cloneHint("https://github.com/acme/atlas.git", "", denied); !strings.Contains(h, "gh auth login") {
		t.Errorf("without gh, the hint should say how to sign in: %q", h)
	}
	if h := cloneHint("https://github.com/acme/atlas.git", "!gh auth git-credential", denied); !strings.Contains(h, "gh auth status") {
		t.Errorf("with gh, the hint should point at its account: %q", h)
	}
	if h := cloneHint("https://github.com/acme/atlas.git", "", errors.New("git clone: Could not resolve host: github.com")); h != "" {
		t.Errorf("a network failure is not a sign-in problem: %q", h)
	}
	if h := cloneHint("git@github.com:acme/atlas.git", "", denied); h != "" {
		t.Errorf("SSH remotes don't use gh: %q", h)
	}
}

func isolatedFixture(t *testing.T) (*Workspaces, protocol.ExecutionManifest) {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tmp, "src")
	run(t, tmp, nil, "", "git", "init", "--quiet", "--initial-branch=main", src)
	run(t, src, nil, "", "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "--allow-empty", "-m", "base")
	base := strings.TrimSpace(run(t, src, nil, "", "git", "rev-parse", "HEAD"))
	w := newWorkspaces(Paths{Dir: filepath.Join(tmp, "state")})
	w.isolated = true
	return w, protocol.ExecutionManifest{JobID: "job12345678", RunID: "run12345678",
		Repo: &protocol.RepoSpec{RepoID: "repo", RemoteURL: src, DefaultBranch: "main", BaseRev: base}}
}

func TestIsolatedWorkspaceIndependentAndReusable(t *testing.T) {
	ctx := context.Background()
	w, m := isolatedFixture(t)
	rep, err := w.replica(ctx, *m.Repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	run(t, rep, nil, "", "git", "config", ghCredentialKey, "!secret-helper")
	ws, err := w.Prepare(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(ws.Dir), "docker-job-") {
		t.Fatal(ws.Dir)
	}
	config := run(t, ws.Dir, nil, "", "git", "config", "--local", "--list")
	if strings.Contains(config, rep) || strings.Contains(config, "credential.") || strings.Contains(config, "remote.") {
		t.Fatalf("host configuration leaked: %s", config)
	}
	if err := os.WriteFile(filepath.Join(ws.Dir, "dirty.txt"), []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.RunID = "second-attempt"
	reused, err := w.Prepare(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Dir != ws.Dir {
		t.Fatal("job workspace changed")
	}
	// Removing both sources proves all subsequent git operations use only the
	// standalone workspace, not a linked gitdir or alternates.
	if err := os.RemoveAll(rep); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(m.Repo.RemoteURL); err != nil {
		t.Fatal(err)
	}
	_, dirty, untracked, err := reused.Head(ctx)
	if err != nil || !dirty || untracked != 1 {
		t.Fatalf("dirty reuse: %v %v %d", err, dirty, untracked)
	}
	checkpoint, _, err := reused.Checkpoint(ctx, m.RunID)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := reused.Bundle(ctx, reused.Base, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bundle)
	run(t, reused.Dir, nil, "", "git", "bundle", "verify", bundle)
	head, err := reused.Commit(ctx, "test", "test@example.com", "change")
	if err != nil {
		t.Fatal(err)
	}
	_, files, _, _, err := reused.Diff(ctx, reused.Base, head)
	if err != nil || files != 1 {
		t.Fatalf("diff: %v, %d files", err, files)
	}
	run(t, reused.Dir, nil, "", "git", "fsck", "--full")
	w.Release(ctx, reused)
	if _, err := os.Stat(reused.Dir); err != nil {
		t.Fatal("editing workspace removed", err)
	}
}

func TestIsolatedReviewAndNativeWorkspace(t *testing.T) {
	ctx := context.Background()
	w, m := isolatedFixture(t)
	w.isolated = false
	native, err := w.Prepare(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(native.Dir, ".git"))
	if err != nil || !info.Mode().IsRegular() || !strings.HasPrefix(filepath.Base(native.Dir), "job-") {
		t.Fatalf("native workspace is no longer linked: %v", err)
	}
	w.isolated = true
	m.Repo.SnapshotRev = m.Repo.BaseRev
	review, err := w.Prepare(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !review.ReadOnly || !strings.HasPrefix(filepath.Base(review.Dir), "docker-review-") {
		t.Fatal("invalid review workspace", review)
	}
	before := run(t, native.Replica, nil, "", "git", "worktree", "list", "--porcelain")
	if err := os.Rename(native.Replica, native.Replica+".away"); err != nil {
		t.Fatal(err)
	}
	head, _, _, err := review.Head(ctx)
	if err != nil || head != m.Repo.SnapshotRev {
		t.Fatalf("snapshot without replica: %q %v", head, err)
	}
	w.Release(ctx, review)
	if _, err := os.Lstat(review.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("review not removed: %v", err)
	}
	if err := os.Rename(native.Replica+".away", native.Replica); err != nil {
		t.Fatal(err)
	}
	after := run(t, native.Replica, nil, "", "git", "worktree", "list", "--porcelain")
	if before != after {
		t.Fatal("isolated release changed replica worktrees")
	}
}

func TestIsolatedRejectsExternalMetadata(t *testing.T) {
	for _, kind := range []string{"workspace-symlink", "gitfile", "metadata-symlink", "alternates", "commondir", "worktree-config", "filter-config", "include-config"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			w, m := isolatedFixture(t)
			ws, err := w.Prepare(ctx, m, nil)
			if err != nil {
				t.Fatal(err)
			}
			meta := filepath.Join(ws.Dir, ".git")
			switch kind {
			case "workspace-symlink":
				err = os.Rename(ws.Dir, ws.Dir+".away")
				if err == nil {
					err = os.Symlink(ws.Dir+".away", ws.Dir)
				}
			case "gitfile":
				err = os.RemoveAll(meta)
				if err == nil {
					err = os.WriteFile(meta, []byte("gitdir: "+ws.Replica+"\n"), 0o600)
				}
			case "metadata-symlink":
				err = os.RemoveAll(filepath.Join(meta, "objects"))
				if err == nil {
					err = os.Symlink(filepath.Join(ws.Replica, "objects"), filepath.Join(meta, "objects"))
				}
			case "alternates":
				err = os.MkdirAll(filepath.Join(meta, "objects", "info"), 0o700)
				if err == nil {
					err = os.WriteFile(filepath.Join(meta, "objects", "info", "alternates"), []byte(filepath.Join(ws.Replica, "objects")), 0o600)
				}
			case "commondir":
				err = os.WriteFile(filepath.Join(meta, "commondir"), []byte(ws.Replica), 0o600)
			case "worktree-config":
				run(t, ws.Dir, nil, "", "git", "config", "core.worktree", m.Repo.RemoteURL)
			case "filter-config":
				run(t, ws.Dir, nil, "", "git", "config", "filter.host.clean", "!unexpected-command")
			case "include-config":
				run(t, ws.Dir, nil, "", "git", "config", "include.path", "/unexpected/config")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Prepare(ctx, m, nil); err == nil {
				t.Fatal("accepted non-standalone workspace")
			}
		})
	}
}

func TestIsolatedScratchAndDocumentReview(t *testing.T) {
	ctx := context.Background()
	w, m := isolatedFixture(t)
	m.Repo = nil
	scratch, err := w.Prepare(ctx, m, nil)
	if err != nil || !scratch.Scratch || !strings.HasPrefix(filepath.Base(scratch.Dir), "docker-scratch-") {
		t.Fatalf("scratch: %v %v", scratch, err)
	}
	m.ReviewArtifact = &protocol.Artifact{}
	fetch := func(context.Context, protocol.Artifact) (string, error) {
		f, err := os.CreateTemp(t.TempDir(), "review")
		if err != nil {
			return "", err
		}
		_, err = f.WriteString("review this")
		f.Close()
		return f.Name(), err
	}
	review, err := w.Prepare(ctx, m, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if !review.ReadOnly || !review.Scratch {
		t.Fatal(review)
	}
	w.Release(ctx, review)
	if _, err := os.Stat(review.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("document review not removed", err)
	}
}

func TestIsolatedResumesNonBranchCheckpoint(t *testing.T) {
	ctx := context.Background()
	w, m := isolatedFixture(t)
	rep, err := w.replica(ctx, *m.Repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(run(t, rep, nil, "", "git", "rev-parse", m.Repo.BaseRev+"^{tree}"))
	rev := strings.TrimSpace(run(t, rep, nil, "", "git", "-c", "user.name=t", "-c", "user.email=t@example.com",
		"commit-tree", tree, "-p", m.Repo.BaseRev, "-m", "checkpoint"))
	run(t, rep, nil, "", "git", "update-ref", "refs/yip/checkpoints/saved", rev)
	m.Repo.CheckpointArtifact = &protocol.Artifact{Revision: rev}
	ws, err := w.Prepare(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(rep); err != nil {
		t.Fatal(err)
	}
	head, _, _, err := ws.Head(ctx)
	if err != nil || head != rev {
		t.Fatalf("checkpoint head = %q, %v; want %q", head, err, rev)
	}
	run(t, ws.Dir, nil, "", "git", "merge-base", "--is-ancestor", ws.Base, head)
	run(t, ws.Dir, nil, "", "git", "gc", "--prune=now")
	if !hasCommit(ctx, ws.Dir, ws.Base) {
		t.Fatal("lost base history")
	}
}

func TestIsolatedGitIgnoresGlobalExecutableConfig(t *testing.T) {
	ctx := context.Background()
	w, m := isolatedFixture(t)
	src := m.Repo.RemoteURL
	for name, content := range map[string]string{
		".gitattributes": "*.txt filter=host diff=host\n",
		"file.txt":       "initial\n",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run(t, src, nil, "", "git", "add", ".")
	run(t, src, nil, "", "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "-m", "attributes")
	m.Repo.BaseRev = strings.TrimSpace(run(t, src, nil, "", "git", "rev-parse", "HEAD"))

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	marker := filepath.Join(home, "executed")
	command := "touch '" + marker + "'; cat"
	for _, key := range []string{"filter.host.clean", "filter.host.smudge", "diff.host.textconv"} {
		run(t, src, nil, "", "git", "config", "--global", key, command)
	}
	run(t, src, nil, "", "git", "config", "--global", "credential.helper", "!touch '"+marker+"'")
	ws, err := w.Prepare(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ws.Isolated {
		t.Fatal("isolated workspace missing marker")
	}
	if err := os.WriteFile(filepath.Join(ws.Dir, "file.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ws.Head(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := ws.Checkpoint(ctx, m.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := ws.Diff(ctx, ws.Base, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Commit(ctx, "test", "test@example.com", "change"); err != nil {
		t.Fatal(err)
	}
	bundle, err := ws.Bundle(ctx, ws.Base, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bundle)
	helper, err := ws.git(ctx, "config", "--get-all", "credential.helper")
	if err != nil || helper != "" {
		t.Fatalf("credential helpers not cleared: %q %v", helper, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated git executed global configuration: %v", err)
	}
	// The native path still reads the owner's global configuration.
	native := &Workspace{Dir: src}
	helper, err = native.git(ctx, "config", "--get-all", "credential.helper")
	if err != nil || !strings.Contains(helper, marker) {
		t.Fatalf("native git lost global config: %q %v", helper, err)
	}
}

func TestIsolatedCheckpointDoesNotExecuteNestedGitConfig(t *testing.T) {
	ctx := context.Background()
	w, m := isolatedFixture(t)
	ws, err := w.Prepare(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	outside := m.Repo.RemoteURL
	marker := filepath.Join(t.TempDir(), "executed")
	monitor := filepath.Join(t.TempDir(), "monitor")
	if err := os.WriteFile(monitor, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run(t, outside, nil, "", "git", "config", "core.fsmonitor", monitor)
	nested := filepath.Join(ws.Dir, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, ".git"), filepath.Join(nested, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws.Dir, "directory-link")); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := ws.Checkpoint(ctx, m.RunID)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := ws.git(ctx, "ls-tree", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tree, "120000 blob") || !strings.Contains(tree, "160000 commit") {
		t.Fatalf("expected symlink and gitlink, not traversed file contents: %s", tree)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested git configuration executed: %v", err)
	}
}

func TestIsolatedGitDoesNotRunTrackedSubmoduleFilters(t *testing.T) {
	ctx := context.Background()
	w, m := isolatedFixture(t)
	ws, err := w.Prepare(ctx, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(ws.Dir, "nested")
	run(t, ws.Dir, nil, "", "git", "init", "--quiet", nested)
	for name, content := range map[string]string{
		".gitattributes": "tracked filter=proof\n",
		"tracked":        "base\n",
	} {
		if err := os.WriteFile(filepath.Join(nested, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run(t, nested, nil, "", "git", "add", ".")
	run(t, nested, nil, "", "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "-m", "nested")
	marker := filepath.Join(t.TempDir(), "executed")
	run(t, nested, nil, "", "git", "config", "filter.proof.clean", "touch '"+marker+"'; cat")
	// Equal-size changes force content comparison, which would select the
	// clean filter from the nested repository's agent-writable configuration.
	if err := os.WriteFile(filepath.Join(nested, "tracked"), []byte("next\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Dir, ".gitmodules"), []byte("[submodule \"nested\"]\n\tpath = nested\n\tignore = none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSafe := func(stage string) {
		t.Helper()
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s executed tracked submodule filter: %v", stage, err)
		}
	}
	// The first add discovers an untracked embedded repository; subsequent
	// adds must exclude the gitlink once it exists in the protected index.
	if _, err := ws.Commit(ctx, "test", "test@example.com", "track gitlink"); err != nil {
		t.Fatal(err)
	}
	assertSafe("initial embedded repository commit")
	_, dirty, _, err := ws.Head(ctx)
	if err != nil || dirty {
		t.Fatalf("nested dirtiness should be ignored: %v, dirty=%v", err, dirty)
	}
	assertSafe("Head")
	if err := os.WriteFile(filepath.Join(ws.Dir, "change.txt"), []byte("parent change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := ws.Checkpoint(ctx, m.RunID)
	if err != nil {
		t.Fatal(err)
	}
	assertSafe("Checkpoint")
	if _, _, _, _, err := ws.Diff(ctx, ws.Base, snapshot); err != nil {
		t.Fatal(err)
	}
	assertSafe("Diff")
	if _, err := ws.Commit(ctx, "test", "test@example.com", "parent change"); err != nil {
		t.Fatal(err)
	}
	assertSafe("Commit")
	// Prove the fixture exercises the vulnerability rather than depending on
	// the nested index's stat cache to skip the filter.
	run(t, nested, nil, "", "git", "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control status did not exercise nested filter: %v", err)
	}
	// Deleting nested work must not stage gitlink removal either.
	before, err := ws.git(ctx, "ls-tree", "HEAD", "--", "nested")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(nested); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Dir, "change.txt"), []byte("another parent change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Commit(ctx, "test", "test@example.com", "keep gitlink"); err != nil {
		t.Fatal(err)
	}
	after, err := ws.git(ctx, "ls-tree", "HEAD", "--", "nested")
	if err != nil || after != before {
		t.Fatalf("gitlink should remain frozen: before=%q after=%q err=%v", before, after, err)
	}
}
