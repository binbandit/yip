package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/protocol"
)

// Workspaces manages repository replicas and per-job worktrees. Git worktrees
// isolate working files, not processes or secrets; the provider's own
// sandbox and the execution profile provide the process boundary.
type Workspaces struct {
	paths Paths
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newWorkspaces(p Paths) *Workspaces {
	_ = os.MkdirAll(p.replicas(), 0o700)
	_ = os.MkdirAll(p.work(), 0o700)
	return &Workspaces{paths: p, locks: map[string]*sync.Mutex{}}
}

func (w *Workspaces) lock(key string) func() {
	w.mu.Lock()
	l := w.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		w.locks[key] = l
	}
	w.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func gitEnv() []string {
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_ASKPASS=true"}
	// Besides git's own needs, what the GitHub CLI reads to find its sign-in
	// when git runs it as the credential helper (config dir, token variables,
	// and the session bus of the keyring it may store the token in).
	for _, k := range []string{"PATH", "HOME", "SSH_AUTH_SOCK", "LANG", "TMPDIR",
		"USER", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "GH_CONFIG_DIR", "GH_TOKEN", "GITHUB_TOKEN"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// gitSafe precedes every runner git command: the runner uses the owner's
// credentials, so hooks and fsmonitor commands from a repository (which an
// agent can edit) must never run under it.
var gitSafe = []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}

func gitArgs(args []string) []string { return append(append([]string{}, gitSafe...), args...) }

// git runs a git command and returns trimmed stdout.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", gitArgs(args)...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// replica ensures a bare replica of the repository exists and is fresh.
// Remote branches are fetched into refs/remotes/origin/*; local job branches
// (refs/heads/yip/...) live beside them and are never pruned by a fetch.
func (w *Workspaces) replica(ctx context.Context, repo protocol.RepoSpec, fetch bundleFetcher) (string, error) {
	path := filepath.Join(w.paths.replicas(), repo.RepoID+".git")
	unlock := w.lock("replica:" + repo.RepoID)
	defer unlock()
	if repo.RemoteURL == "" && repo.SourceBundle != nil {
		return path, bundleReplica(ctx, path, repo, fetch)
	}
	helper := ghCredentialHelper(repo.RemoteURL)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		args := []string{"clone", "--quiet", "--bare", repo.RemoteURL, path}
		if helper != "" {
			args = append([]string{"-c", ghCredentialKey + "=" + helper}, args...)
		}
		if _, err := git(ctx, w.paths.replicas(), args...); err != nil {
			return "", fmt.Errorf("clone %s: %w%s", repo.Name, err, cloneHint(repo.RemoteURL, helper, err))
		}
		if _, err := git(ctx, path, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return "", err
		}
	}
	if helper != "" {
		// Kept in the replica so its fetches, and pushes from worktrees an
		// engineer is allowed to make, authenticate the same way. Checks
		// clear every credential helper (checkEnv), so they never get it.
		if cur, _ := git(ctx, path, "config", "--local", "--get-all", ghCredentialKey); cur != helper {
			if _, err := git(ctx, path, "config", "--local", "--replace-all", ghCredentialKey, helper); err != nil {
				return "", err
			}
		}
	}
	// A stale replica is still usable for a recorded base, so a failed fetch
	// is not an error.
	_, _ = git(ctx, path, "fetch", "--quiet", "--prune", "origin")
	return path, nil
}

// ghCredentialKey scopes the GitHub CLI credential helper to github.com.
const ghCredentialKey = "credential.https://github.com.helper"

// ghCredentialHelper returns the git credential helper that authenticates a
// github.com HTTPS remote with the GitHub CLI's sign-in on this machine (as
// `gh auth setup-git` would), so a private repository the signed-in account
// can see clones without a token in its URL. It is empty for other remotes
// or when gh isn't installed. Helpers configured by the owner still run
// first; gh answers only when none of them has a credential.
func ghCredentialHelper(remote string) string {
	if !strings.HasPrefix(strings.ToLower(remote), "https://github.com/") {
		return ""
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(gh); err == nil {
		gh = abs
	}
	return "!'" + strings.ReplaceAll(gh, "'", `'\''`) + "' auth git-credential"
}

// cloneHint explains how to reach a GitHub repository that refused an
// HTTPS clone for lack of a credential.
func cloneHint(remote, helper string, err error) string {
	msg := err.Error()
	if !strings.HasPrefix(strings.ToLower(remote), "https://github.com/") ||
		!(strings.Contains(msg, "could not read Username") || strings.Contains(msg, "Repository not found") ||
			strings.Contains(msg, "Authentication failed") || strings.Contains(msg, "403")) {
		return ""
	}
	if helper == "" {
		return ". If it's private, install the GitHub CLI on this machine and sign in with `gh auth login` as an account that can see it"
	}
	return ". If it's private, check `gh auth status` on this machine: the signed-in account needs access to it"
}

// bundleReplica builds (or refreshes) the replica of a repository imported
// from a git bundle: its branches become refs/remotes/origin/*, exactly as a
// clone's would. A replica already at this bundle is left alone.
func bundleReplica(ctx context.Context, path string, repo protocol.RepoSpec, fetch bundleFetcher) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if _, err := git(ctx, filepath.Dir(path), "init", "--quiet", "--bare", path); err != nil {
			return fmt.Errorf("create replica for %s: %w", repo.Name, err)
		}
	}
	if cur, _ := git(ctx, path, "config", "--get", "yip.sourceBundle"); cur == repo.SourceBundle.Hash {
		return nil
	}
	if fetch == nil {
		return fmt.Errorf("%s was imported from a bundle this machine hasn't received", repo.Name)
	}
	file, err := fetch(ctx, *repo.SourceBundle)
	if err != nil {
		return fmt.Errorf("fetch the imported bundle for %s: %w", repo.Name, err)
	}
	defer os.Remove(file)
	if _, err := git(ctx, path, "bundle", "verify", "--quiet", file); err != nil {
		return fmt.Errorf("the imported bundle for %s didn't verify: %w", repo.Name, err)
	}
	if _, err := git(ctx, path, "fetch", "--quiet", "--prune", file, "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return err
	}
	_, err = git(ctx, path, "config", "yip.sourceBundle", repo.SourceBundle.Hash)
	return err
}

// defaultHead resolves the remote default branch in a replica.
func defaultHead(ctx context.Context, rep, branch string) (string, error) {
	if branch == "" {
		branch = "main"
	}
	for _, ref := range []string{"refs/remotes/origin/" + branch, "refs/heads/" + branch} {
		if rev, err := git(ctx, rep, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil && rev != "" {
			return rev, nil
		}
	}
	return "", fmt.Errorf("branch %s not found in the replica", branch)
}

func hasCommit(ctx context.Context, repoDir, rev string) bool {
	_, err := git(ctx, repoDir, "cat-file", "-e", rev+"^{commit}")
	return err == nil
}

// importBundle fetches a verified bundle into the replica.
func importBundle(ctx context.Context, repoDir, bundlePath, head string) error {
	if _, err := git(ctx, repoDir, "bundle", "verify", bundlePath); err != nil {
		return err
	}
	_, err := git(ctx, repoDir, "fetch", "--quiet", bundlePath, "+refs/*:refs/yip/imported/*")
	if err != nil {
		return err
	}
	if !hasCommit(ctx, repoDir, head) {
		return fmt.Errorf("bundle does not contain %s", head)
	}
	return nil
}

// Workspace is a prepared working directory for one run.
type Workspace struct {
	Dir      string
	Replica  string
	Branch   string
	Base     string
	ReadOnly bool
	Scratch  bool
}

// bundleFetcher downloads and verifies an artifact to a temporary file.
type bundleFetcher func(ctx context.Context, a protocol.Artifact) (string, error)

// Prepare creates or reuses the workspace for a run.
func (w *Workspaces) Prepare(ctx context.Context, m protocol.ExecutionManifest, fetch bundleFetcher) (*Workspace, error) {
	if m.ReviewArtifact != nil {
		if fetch == nil {
			return nil, errors.New("no artifact fetcher for document review")
		}
		source, err := fetch(ctx, *m.ReviewArtifact)
		if err != nil {
			return nil, err
		}
		defer os.Remove(source)
		dir := filepath.Join(w.paths.work(), "review-"+domain.Short(m.RunID))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		in, err := os.Open(source)
		if err != nil {
			return nil, err
		}
		defer in.Close()
		out, err := os.Create(filepath.Join(dir, "review-artifact"))
		if err != nil {
			return nil, err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if err := makeReadOnly(dir); err != nil {
			return nil, err
		}
		return &Workspace{Dir: dir, ReadOnly: true, Scratch: true}, nil
	}

	if m.Repo == nil {
		dir := filepath.Join(w.paths.work(), "scratch-"+domain.Short(m.RunID))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		return &Workspace{Dir: dir, Scratch: true}, nil
	}
	rep, err := w.replica(ctx, *m.Repo, fetch)
	if err != nil {
		return nil, err
	}
	ensure := func(rev string) error {
		if rev == "" || hasCommit(ctx, rep, rev) {
			return nil
		}
		for i, ref := range m.Repo.FetchRefs {
			if !strings.HasPrefix(ref, "refs/") || strings.ContainsAny(ref, " :^~") {
				continue
			}
			_, _ = git(ctx, rep, "fetch", "--quiet", "origin", fmt.Sprintf("+%s:refs/yip/fetched/%d", ref, i))
			if hasCommit(ctx, rep, rev) {
				return nil
			}
		}
		if m.Repo.CheckpointArtifact == nil || fetch == nil {
			return fmt.Errorf("revision %s is not available on this machine and no verified bundle was provided", rev)
		}
		bundle, err := fetch(ctx, *m.Repo.CheckpointArtifact)
		if err != nil {
			return err
		}
		defer os.Remove(bundle)
		return importBundle(ctx, rep, bundle, rev)
	}
	if m.Repo.SnapshotRev != "" {
		// Reviewer: a read-only, detached snapshot of exactly this revision.
		if err := ensure(m.Repo.SnapshotRev); err != nil {
			return nil, err
		}
		dir := filepath.Join(w.paths.work(), "review-"+domain.Short(m.RunID))
		_ = makeWritable(dir)
		_ = os.RemoveAll(dir)
		if _, err := git(ctx, rep, "worktree", "add", "--quiet", "--detach", dir, m.Repo.SnapshotRev); err != nil {
			return nil, err
		}
		if err := makeReadOnly(dir); err != nil {
			return nil, err
		}
		return &Workspace{Dir: dir, Replica: rep, Base: m.Repo.BaseRev, ReadOnly: true}, nil
	}
	// Editing work: one worktree and branch per job, reused across attempts.
	dir := filepath.Join(w.paths.work(), "job-"+domain.Short(m.JobID))
	unlock := w.lock("job:" + m.JobID)
	defer unlock()
	base := m.Repo.BaseRev
	if base == "" {
		if base, err = defaultHead(ctx, rep, m.Repo.DefaultBranch); err != nil {
			return nil, err
		}
	}
	branch := firstNonEmpty(m.Repo.Branch, "yip/"+domain.Short(m.JobID))
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		if _, err := git(ctx, dir, "rev-parse", "--verify", "HEAD"); err != nil {
			return nil, fmt.Errorf("the workspace for this job exists but its branch %s is missing; it was kept for inspection at %s", branch, dir)
		}
		return &Workspace{Dir: dir, Replica: rep, Branch: branch, Base: base}, nil
	}
	if err := ensure(base); err != nil {
		return nil, err
	}
	start := base
	if m.Repo.CheckpointArtifact != nil && m.Repo.CheckpointArtifact.Revision != "" {
		// Resuming on a new machine from the verified bundle of the last published revision.
		if err := ensure(m.Repo.CheckpointArtifact.Revision); err != nil {
			return nil, err
		}
		start = m.Repo.CheckpointArtifact.Revision
	}
	if _, err := git(ctx, rep, "worktree", "add", "--quiet", "-B", branch, dir, start); err != nil {
		return nil, err
	}
	return &Workspace{Dir: dir, Replica: rep, Branch: branch, Base: base}, nil
}

// Release cleans up a read-only snapshot. Editing worktrees are kept: dirty
// or unreviewed work is never deleted automatically.
func (w *Workspaces) Release(ctx context.Context, ws *Workspace) {
	if ws == nil || !ws.ReadOnly {
		return
	}
	_ = makeWritable(ws.Dir)
	if ws.Replica != "" {
		_, _ = git(ctx, ws.Replica, "worktree", "remove", "--force", ws.Dir)
	}
	_ = os.RemoveAll(ws.Dir)
}

func makeReadOnly(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" && p != dir {
			return fs.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		return os.Chmod(p, info.Mode().Perm()&^0o222)
	})
}

func makeWritable(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		return os.Chmod(p, info.Mode().Perm()|0o200)
	})
}

// Head returns the workspace HEAD and whether the tree has uncommitted changes.
func (ws *Workspace) Head(ctx context.Context) (head string, dirty bool, untracked int, err error) {
	if ws.Scratch {
		return "", false, 0, nil
	}
	head, err = git(ctx, ws.Dir, "rev-parse", "HEAD")
	if err != nil {
		return "", false, 0, err
	}
	st, err := git(ctx, ws.Dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return head, false, 0, err
	}
	for _, line := range strings.Split(st, "\n") {
		if line == "" {
			continue
		}
		dirty = true
		if strings.HasPrefix(line, "??") {
			untracked++
		}
	}
	return head, dirty, untracked, nil
}

// Commit records all outstanding changes as the engineer.
func (ws *Workspace) Commit(ctx context.Context, name, email, message string) (string, error) {
	if _, err := git(ctx, ws.Dir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := git(ctx, ws.Dir, "-c", "user.name="+name, "-c", "user.email="+email, "-c", "commit.gpgsign=false",
		"commit", "--quiet", "--no-verify", "-m", message); err != nil {
		return "", err
	}
	return git(ctx, ws.Dir, "rev-parse", "HEAD")
}

// Diff returns the unified diff and stats between base and head.
func (ws *Workspace) Diff(ctx context.Context, base, head string) (diff string, files, ins, del int, err error) {
	diff, err = git(ctx, ws.Dir, "diff", "--no-color", "--no-ext-diff", base, head)
	if err != nil {
		return "", 0, 0, 0, err
	}
	stat, err := git(ctx, ws.Dir, "diff", "--numstat", base, head)
	if err != nil {
		return diff, 0, 0, 0, err
	}
	for _, line := range strings.Split(stat, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		files++
		a, _ := strconv.Atoi(parts[0])
		d, _ := strconv.Atoi(parts[1])
		ins += a
		del += d
	}
	return diff + "\n", files, ins, del, nil
}

// Bundle writes a git bundle of base..head so another machine (a reviewer's,
// or a recovery target) can reproduce exactly this revision.
func (ws *Workspace) Bundle(ctx context.Context, base, head string) (string, error) {
	f, err := os.CreateTemp("", "yip-bundle-*.bundle")
	if err != nil {
		return "", err
	}
	f.Close()
	tmpRef := "refs/yip/bundle/" + domain.Short(head)
	if _, err := git(ctx, ws.Dir, "update-ref", tmpRef, head); err != nil {
		return "", err
	}
	defer git(ctx, ws.Dir, "update-ref", "-d", tmpRef)
	args := []string{"bundle", "create", f.Name(), tmpRef}
	if base != "" && base != head {
		args = append(args, "^"+base)
	}
	if _, err := git(ctx, ws.Dir, args...); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// Checkpoint snapshots uncommitted work (including untracked files, subject
// to .gitignore) without moving the branch.
func (ws *Workspace) Checkpoint(ctx context.Context, runID string) (commit string, untracked int, err error) {
	head, dirty, untracked, err := ws.Head(ctx)
	if err != nil || !dirty {
		return head, untracked, err
	}
	idx, err := os.CreateTemp("", "yip-index-*")
	if err != nil {
		return "", 0, err
	}
	idx.Close()
	defer os.Remove(idx.Name())
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", gitArgs(args)...)
		cmd.Dir = ws.Dir
		cmd.Env = append(gitEnv(), "GIT_INDEX_FILE="+idx.Name())
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := run("read-tree", "HEAD"); err != nil {
		return "", 0, err
	}
	if _, err := run("add", "-A"); err != nil {
		return "", 0, err
	}
	tree, err := run("write-tree")
	if err != nil {
		return "", 0, err
	}
	commit, err = run("-c", "user.name=yip runner", "-c", "user.email=runner@yip.invalid", "commit-tree", tree, "-p", head, "-m", "yip checkpoint for run "+runID)
	if err != nil {
		return "", 0, err
	}
	_, err = git(ctx, ws.Dir, "update-ref", "refs/yip/checkpoints/"+domain.Short(runID), commit)
	return commit, untracked, err
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
