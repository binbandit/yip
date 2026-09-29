package hub

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/internal/forge/github"
	"github.com/binbandit/yip/protocol"
)

// repoLookupTimeout bounds asking GitHub about a repository being added.
const repoLookupTimeout = 20 * time.Second

// resolveRepo turns a repository as a person described it into what is
// stored. A GitHub owner/name (or any github.com URL) is enough: it becomes
// the HTTPS clone URL, the forge link, the name, and — unless given — the
// default branch GitHub reports, which also confirms the repository exists
// and is visible (a private one through the GitHub CLI signed in on the hub).
// Machines clone it with their own sign-in. Any other remote keeps its URL;
// its name defaults to the URL's last path segment and its branch to main.
func (h *Hub) resolveRepo(ctx context.Context, req protocol.PutRepoRequest) (protocol.PutRepoRequest, error) {
	req.Name, req.RemoteURL = strings.TrimSpace(req.Name), strings.TrimSpace(req.RemoteURL)
	req.DefaultBranch, req.Forge, req.ForgeRepo = strings.TrimSpace(req.DefaultBranch), strings.TrimSpace(req.Forge), strings.TrimSpace(req.ForgeRepo)
	if req.RemoteURL == "" {
		return req, domain.Invalid("A repository needs a GitHub owner/name or a remote URL reachable from your machines, or import it from a bundle.")
	}
	gh, isGitHub := github.ParseRepoInput(req.RemoteURL)
	if isGitHub {
		req.RemoteURL = gh.RemoteURL
	}
	if req.Forge == "" {
		req.Forge = "none"
		if isGitHub {
			req.Forge = "github"
		}
	}
	if req.Forge != "none" && req.Forge != "github" {
		return req, domain.Invalid("Supported forges: github, or none.")
	}
	var owner, name string
	derived := false
	if req.Forge == "github" {
		switch fr, ok := github.ParseRepoInput(req.ForgeRepo); {
		case ok:
			owner, name = fr.Owner, fr.Name
		case req.ForgeRepo == "" && isGitHub:
			owner, name, derived = gh.Owner, gh.Name, true
		default:
			return req, domain.Invalid("GitHub repositories are written as owner/name.")
		}
		req.ForgeRepo = owner + "/" + name
	} else {
		req.ForgeRepo = ""
	}
	if req.Forge == "github" && req.DefaultBranch == "" && h.cfg.GitHubRepo != nil {
		lctx, cancel := context.WithTimeout(ctx, repoLookupTimeout)
		info, err := h.cfg.GitHubRepo(lctx, h, owner, name)
		cancel()
		switch {
		case errors.Is(err, forge.ErrNotFound):
			return req, domain.Invalid("GitHub has no repository %s/%s that this hub can see. Check the spelling. If it's private, sign in on the hub's machine with `gh auth login` as an account that can see it, or enter its default branch to add it without asking GitHub.", owner, name)
		case err != nil:
			return req, domain.Unavailable("forge", "Couldn't ask GitHub about %s/%s: %s. Enter its default branch to add it anyway.", owner, name, strings.TrimRight(err.Error(), ". "))
		}
		req.DefaultBranch = info.DefaultBranch
		if info.Owner != "" && info.Name != "" {
			// GitHub's spelling, so links and forge lookups match exactly.
			owner, name = info.Owner, info.Name
			req.ForgeRepo = owner + "/" + name
			if derived && gh.Shorthand {
				req.RemoteURL = github.CloneURL(owner, name)
			}
		}
	}
	if req.Name == "" {
		req.Name = name
	}
	if req.Name == "" {
		req.Name = repoNameFromURL(req.RemoteURL)
	}
	if req.Name == "" {
		return req, domain.Invalid("Name the repository.")
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	return req, nil
}

// repoNameFromURL is the last path segment of a git remote without ".git":
// atlas for git@host:acme/atlas.git, https://host/acme/atlas or /srv/atlas.git.
func repoNameFromURL(remote string) string {
	s := strings.TrimRight(remote, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	if s == "." || s == ".." {
		return ""
	}
	return s
}
