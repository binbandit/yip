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
