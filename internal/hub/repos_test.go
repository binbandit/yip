package hub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/protocol"
)

// A repository can be added by just its GitHub owner/name: the hub fills in
// the clone URL, forge link, name and default branch from GitHub (through the
// gh sign-in for a private repository), and says exactly what to do when
// GitHub can't show it.
func TestResolveRepo(t *testing.T) {
	var asked []string
	lookup := func(ctx context.Context, h *Hub, owner, name string) (github.RepoInfo, error) {
		asked = append(asked, owner+"/"+name)
		switch strings.ToLower(owner + "/" + name) {
		case "acme/atlas":
			return github.RepoInfo{Owner: "Acme", Name: "Atlas", DefaultBranch: "trunk", Private: true}, nil
		case "acme/missing":
			return github.RepoInfo{}, forge.ErrNotFound
		}
		return github.RepoInfo{}, errors.New("rate limited")
	}
	h := &Hub{cfg: Config{GitHubRepo: lookup}}
	ctx := context.Background()

	cases := []struct {
		in     protocol.PutRepoRequest
		want   protocol.PutRepoRequest
		lookup bool
	}{
		{in: protocol.PutRepoRequest{RemoteURL: "acme/atlas"}, lookup: true,
			want: protocol.PutRepoRequest{Name: "Atlas", RemoteURL: "https://github.com/Acme/Atlas.git", DefaultBranch: "trunk", Forge: "github", ForgeRepo: "Acme/Atlas"}},
		{in: protocol.PutRepoRequest{RemoteURL: " github.com/acme/atlas ", Name: "core"}, lookup: true,
			want: protocol.PutRepoRequest{Name: "core", RemoteURL: "https://github.com/Acme/Atlas.git", DefaultBranch: "trunk", Forge: "github", ForgeRepo: "Acme/Atlas"}},
		// An explicit URL is kept as typed (SSH here); the forge link is canonical.
		{in: protocol.PutRepoRequest{RemoteURL: "git@github.com:acme/atlas.git"}, lookup: true,
			want: protocol.PutRepoRequest{Name: "Atlas", RemoteURL: "git@github.com:acme/atlas.git", DefaultBranch: "trunk", Forge: "github", ForgeRepo: "Acme/Atlas"}},
		// A given default branch needs no lookup.
		{in: protocol.PutRepoRequest{RemoteURL: "acme/atlas", DefaultBranch: "main"},
			want: protocol.PutRepoRequest{Name: "atlas", RemoteURL: "https://github.com/acme/atlas.git", DefaultBranch: "main", Forge: "github", ForgeRepo: "acme/atlas"}},
		// The previous explicit form still works.
		{in: protocol.PutRepoRequest{Name: "atlas", RemoteURL: "https://github.com/acme/atlas.git", DefaultBranch: "main", Forge: "github", ForgeRepo: "acme/atlas"},
			want: protocol.PutRepoRequest{Name: "atlas", RemoteURL: "https://github.com/acme/atlas.git", DefaultBranch: "main", Forge: "github", ForgeRepo: "acme/atlas"}},
		{in: protocol.PutRepoRequest{RemoteURL: "acme/atlas", Forge: "none"},
			want: protocol.PutRepoRequest{Name: "atlas", RemoteURL: "https://github.com/acme/atlas.git", DefaultBranch: "main", Forge: "none"}},
		{in: protocol.PutRepoRequest{RemoteURL: "git@gitlab.com:acme/beacon.git"},
			want: protocol.PutRepoRequest{Name: "beacon", RemoteURL: "git@gitlab.com:acme/beacon.git", DefaultBranch: "main", Forge: "none"}},
		{in: protocol.PutRepoRequest{RemoteURL: "/srv/git/beacon.git/"},
			want: protocol.PutRepoRequest{Name: "beacon", RemoteURL: "/srv/git/beacon.git/", DefaultBranch: "main", Forge: "none"}},
		// A mirror elsewhere, linked to its GitHub repository.
		{in: protocol.PutRepoRequest{RemoteURL: "/srv/git/atlas.git", Forge: "github", ForgeRepo: "https://github.com/acme/atlas"}, lookup: true,
			want: protocol.PutRepoRequest{Name: "Atlas", RemoteURL: "/srv/git/atlas.git", DefaultBranch: "trunk", Forge: "github", ForgeRepo: "Acme/Atlas"}},
	}
	for _, tc := range cases {
		asked = nil
		got, err := h.resolveRepo(ctx, tc.in)
		if err != nil {
			t.Errorf("resolveRepo(%+v): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveRepo(%+v)\n got %+v\nwant %+v", tc.in, got, tc.want)
		}
		if (len(asked) > 0) != tc.lookup {
			t.Errorf("resolveRepo(%+v) asked GitHub %v, want lookup=%v", tc.in, asked, tc.lookup)
		}
	}

	failures := []struct {
		in   protocol.PutRepoRequest
		code string
		says string
	}{
		{protocol.PutRepoRequest{}, "invalid", "GitHub owner/name"},
		{protocol.PutRepoRequest{RemoteURL: "acme/missing"}, "invalid", "gh auth login"},
		{protocol.PutRepoRequest{RemoteURL: "acme/busy"}, "unavailable", "Enter its default branch"},
		{protocol.PutRepoRequest{RemoteURL: "/srv/git/atlas.git", Forge: "github"}, "invalid", "owner/name"},
		{protocol.PutRepoRequest{RemoteURL: "acme/atlas", Forge: "gitlab"}, "invalid", "Supported forges"},
	}
	for _, tc := range failures {
		_, err := h.resolveRepo(ctx, tc.in)
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != tc.code || !strings.Contains(de.Message, tc.says) {
			t.Errorf("resolveRepo(%+v) = %v; want %s mentioning %q", tc.in, err, tc.code, tc.says)
		}
	}

	// A hub that can't ask GitHub still accepts owner/name, on main.
	got, err := (&Hub{}).resolveRepo(ctx, protocol.PutRepoRequest{RemoteURL: "acme/atlas"})
	if err != nil || got.DefaultBranch != "main" || got.ForgeRepo != "acme/atlas" || got.Name != "atlas" {
		t.Fatalf("without a lookup: %+v, %v", got, err)
	}
}
