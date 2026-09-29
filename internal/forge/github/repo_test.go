package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/binbandit/yip/internal/forge"
)

func TestParseRepoInput(t *testing.T) {
	ok := []struct {
		in, owner, name, remote string
		shorthand               bool
	}{
		{"acme/atlas", "acme", "atlas", "https://github.com/acme/atlas.git", true},
		{"  acme/atlas.git ", "acme", "atlas", "https://github.com/acme/atlas.git", true},
		{"github.com/acme/atlas", "acme", "atlas", "https://github.com/acme/atlas.git", true},
		{"acme-co/atlas_v2.js", "acme-co", "atlas_v2.js", "https://github.com/acme-co/atlas_v2.js.git", true},
		{"https://github.com/acme/atlas", "acme", "atlas", "https://github.com/acme/atlas", false},
		{"https://github.com/acme/atlas.git", "acme", "atlas", "https://github.com/acme/atlas.git", false},
		{"https://GitHub.com/acme/atlas/", "acme", "atlas", "https://GitHub.com/acme/atlas/", false},
		{"git@github.com:acme/atlas.git", "acme", "atlas", "git@github.com:acme/atlas.git", false},
		{"ssh://git@github.com/acme/atlas.git", "acme", "atlas", "ssh://git@github.com/acme/atlas.git", false},
	}
	for _, tc := range ok {
		got, found := ParseRepoInput(tc.in)
		if !found || got.Owner != tc.owner || got.Name != tc.name || got.RemoteURL != tc.remote || got.Shorthand != tc.shorthand {
			t.Errorf("ParseRepoInput(%q) = %+v, %v; want %s/%s %s shorthand=%v", tc.in, got, found, tc.owner, tc.name, tc.remote, tc.shorthand)
		}
	}
	for _, in := range []string{
		"", "atlas", "acme/atlas/extra", "/srv/git/atlas.git", "./acme/atlas", "../acme/atlas", "~/acme/atlas",
		"https://gitlab.com/acme/atlas.git", "git@gitlab.com:acme/atlas.git", "gitlab.com/acme/atlas",
		"https://github.com/acme/atlas/pull/42", "https://github.com/acme", "http://github.com/acme/atlas",
		"https://token@github.com/acme/atlas.git", "https://x:y@github.com/acme/atlas.git", "ssh://root@github.com/acme/atlas.git",
		"git://github.com/acme/atlas.git", "acme/.", "acme/..", "-acme/atlas", "acme/at las", "acme/atlas?x=1",
	} {
		if got, found := ParseRepoInput(in); found {
			t.Errorf("ParseRepoInput(%q) = %+v; want not a GitHub repository", in, got)
		}
	}
}

func TestRepo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/atlas", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"Atlas","owner":{"login":"Acme"},"default_branch":"trunk","private":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ref := forge.RepoRef{Host: "github.com", Owner: "acme", Name: "atlas"}

	c := New(Options{APIBase: srv.URL, Token: func(context.Context) (string, error) { return "t", nil }})
	info, err := c.Repo(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if info != (RepoInfo{Owner: "Acme", Name: "Atlas", DefaultBranch: "trunk", Private: true}) {
		t.Fatalf("Repo = %+v", info)
	}
	// Without access, a private repository is not found, as GitHub reports it.
	if _, err := New(Options{APIBase: srv.URL}).Repo(context.Background(), ref); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("anonymous lookup of a private repository: %v, want ErrNotFound", err)
	}
}

// ghOnPath installs a fake gh answering with stdout, stderr and exit
// status, and returns the file recording its arguments.
func ghOnPath(t *testing.T, stdout, stderr string, status int) string {
	t.Helper()
	bin := t.TempDir()
	for name, body := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := filepath.Join(bin, "args")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" > '%[1]s/args'\ncat '%[1]s/stdout'\ncat '%[1]s/stderr' >&2\nexit %[2]d\n", bin, status)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return args
}

func TestViewWithCLI(t *testing.T) {
	ctx := context.Background()
	args := ghOnPath(t, `{"name":"Atlas","owner":{"login":"Acme"},"defaultBranchRef":{"name":"develop"},"isPrivate":true}`, "", 0)
	info, err := ViewWithCLI(ctx, "acme", "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if info != (RepoInfo{Owner: "Acme", Name: "Atlas", DefaultBranch: "develop", Private: true}) {
		t.Fatalf("ViewWithCLI = %+v", info)
	}
	got, _ := os.ReadFile(args)
	if want := "repo view github.com/acme/atlas --json name,owner,defaultBranchRef,isPrivate\n"; string(got) != want {
		t.Fatalf("gh was asked %q, want %q", got, want)
	}

	ghOnPath(t, "", "GraphQL: Could not resolve to a Repository with the name 'acme/atlas'. (repository)", 1)
	if _, err := ViewWithCLI(ctx, "acme", "atlas"); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("a repository gh can't see: %v, want ErrNotFound", err)
	}
	ghOnPath(t, "", "To get started with GitHub CLI, please run:  gh auth login", 4)
	if _, err := ViewWithCLI(ctx, "acme", "atlas"); !errors.Is(err, ErrCLIUnavailable) {
		t.Fatalf("gh not signed in: %v, want ErrCLIUnavailable", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := ViewWithCLI(ctx, "acme", "atlas"); !errors.Is(err, ErrCLIUnavailable) {
		t.Fatalf("gh not installed: %v, want ErrCLIUnavailable", err)
	}
}

func TestCLIToken(t *testing.T) {
	args := ghOnPath(t, "gho_abc\n", "", 0)
	token, err := CLIToken(context.Background(), "github.com")
	if err != nil || token != "gho_abc" {
		t.Fatalf("CLIToken = %q, %v", token, err)
	}
	if got, _ := os.ReadFile(args); string(got) != "auth token --hostname github.com\n" {
		t.Fatalf("gh was asked %q", got)
	}
	ghOnPath(t, "", "no oauth token found for github.com", 1)
	if _, err := CLIToken(context.Background(), "github.com"); !errors.Is(err, ErrCLIUnavailable) {
		t.Fatalf("gh not signed in: %v, want ErrCLIUnavailable", err)
	}
}
