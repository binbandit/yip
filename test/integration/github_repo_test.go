package integration

import (
	"context"
	"testing"

	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/protocol"
)

// A project can be created from nothing but a GitHub owner/name — a private
// one included, when the hub's gh sign-in can see it — and a repository can
// be added to a project the same way. A repository GitHub can't show leaves
// no half-created project behind.
func TestProjectFromGitHubOwnerName(t *testing.T) {
	e := newEnv(t, envOptions{noRunner: true, githubRepo: func(ctx context.Context, h *hub.Hub, owner, name string) (github.RepoInfo, error) {
		switch owner + "/" + name {
		case "acme/widgets":
			return github.RepoInfo{Owner: "acme", Name: "widgets", DefaultBranch: "develop", Private: true}, nil
		case "acme/gadgets":
			return github.RepoInfo{Owner: "acme", Name: "gadgets", DefaultBranch: "main"}, nil
		}
		return github.RepoInfo{}, forge.ErrNotFound
	}})

	var p protocol.Project
	e.c.must("POST", "/v1/projects", protocol.CreateProjectRequest{Repos: []protocol.PutRepoRequest{{RemoteURL: "acme/widgets"}}}, &p)
	if p.Name != "widgets" || len(p.Repos) != 1 {
		t.Fatalf("the project should be named for its repository and hold it: %+v", p)
	}
	want := protocol.Repo{Name: "widgets", RemoteURL: "https://github.com/acme/widgets.git", DefaultBranch: "develop", Forge: "github", ForgeRepo: "acme/widgets"}
	if r := p.Repos[0]; r.Name != want.Name || r.RemoteURL != want.RemoteURL || r.DefaultBranch != want.DefaultBranch || r.Forge != want.Forge || r.ForgeRepo != want.ForgeRepo {
		t.Fatalf("repository = %+v, want %+v", r, want)
	}

	e.c.must("PUT", "/v1/projects/"+p.ID+"/repos/new", protocol.PutRepoRequest{RemoteURL: "https://github.com/acme/gadgets"}, &p)
	if len(p.Repos) != 2 {
		t.Fatalf("the second repository should be added: %+v", p.Repos)
	}

	var before []protocol.Project
	e.c.must("GET", "/v1/projects", nil, &before)
	err := e.c.do("POST", "/v1/projects", protocol.CreateProjectRequest{Name: "Typo", Repos: []protocol.PutRepoRequest{{RemoteURL: "acme/widgest"}}}, nil)
	if !isStatus(err, 400) {
		t.Fatalf("a repository GitHub can't show should be refused: %v", err)
	}
	var after []protocol.Project
	e.c.must("GET", "/v1/projects", nil, &after)
	if len(after) != len(before) {
		t.Fatalf("a refused project must not be created (%d projects, then %d)", len(before), len(after))
	}
}
