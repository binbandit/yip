package github

import (
	"context"
	"net/url"
	"strings"

	"github.com/binbandit/yip/internal/forge"
)

// RepoInput is a GitHub repository as a person typed it.
type RepoInput struct {
	Owner string
	Name  string
	// RemoteURL is what machines clone: the URL as given, or
	// https://github.com/{owner}/{name}.git for owner/name shorthand.
	RemoteURL string
	// Shorthand is true for "owner/name" (and "github.com/owner/name").
	Shorthand bool
}

// ParseRepoInput reads a github.com repository written as owner/name,
// github.com/owner/name, https://github.com/owner/name(.git),
// git@github.com:owner/name(.git) or ssh://git@github.com/owner/name(.git).
// Anything else (another host, a local path, a pull request URL) is not a
// GitHub repository and ok is false.
func ParseRepoInput(raw string) (in RepoInput, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, " \t\r\n\\?#") {
		return RepoInput{}, false
	}
	var path string
	switch {
	case strings.HasPrefix(s, "git@"):
		host, p, found := strings.Cut(strings.TrimPrefix(s, "git@"), ":")
		if !found || !isGitHubHost(host) {
			return RepoInput{}, false
		}
		path, in.RemoteURL = p, s
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || !isGitHubHost(u.Host) {
			return RepoInput{}, false
		}
		switch {
		case u.Scheme == "https" && u.User == nil:
		case u.Scheme == "ssh" && u.User != nil && u.User.Username() == "git":
			if _, hasPass := u.User.Password(); hasPass {
				return RepoInput{}, false
			}
		default:
			// Plain http, git://, and URLs carrying credentials are refused:
			// a token belongs in the machine's credential helper, not here.
			return RepoInput{}, false
		}
		path, in.RemoteURL = u.Path, s
	default:
		// owner/name, optionally prefixed with the host.
		if host, rest, found := strings.Cut(s, "/"); found && isGitHubHost(host) {
			s = rest
		}
		path, in.Shorthand = s, true
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) != 2 {
		return RepoInput{}, false
	}
	owner, name := segs[0], strings.TrimSuffix(segs[1], ".git")
	if !validRepoName(owner, name) {
		return RepoInput{}, false
	}
	in.Owner, in.Name = owner, name
	if in.Shorthand {
		in.RemoteURL = CloneURL(owner, name)
	}
	return in, true
}

// CloneURL is the HTTPS clone URL of a github.com repository. Machines
// authenticate it with their own git credential helper (gh auth git-credential
// when the GitHub CLI is signed in), so private repositories need no token in
// the URL.
func CloneURL(owner, name string) string {
	return "https://github.com/" + owner + "/" + name + ".git"
}

func isGitHubHost(host string) bool {
	host = strings.ToLower(host)
	return host == defaultHost || host == "www.github.com"
}

// RepoInfo is what GitHub reports about a repository.
type RepoInfo struct {
	Owner         string // canonical spelling
	Name          string
	DefaultBranch string
	Private       bool
}

// Repo reads a repository (GET /repos/{owner}/{name}). A private repository
// the credential can't see is forge.ErrNotFound, as GitHub reports it.
func (c *Connector) Repo(ctx context.Context, repo forge.RepoRef) (RepoInfo, error) {
	path, err := c.repoPath(repo)
	if err != nil {
		return RepoInfo{}, err
	}
	var r ghRepo
	if err := c.getJSON(ctx, path, nil, &r); err != nil {
		return RepoInfo{}, err
	}
	info := RepoInfo{Owner: repo.Owner, Name: repo.Name, DefaultBranch: r.DefaultBranch, Private: r.Private}
	if r.Owner != nil && r.Owner.Login != "" && r.Name != "" {
		info.Owner, info.Name = r.Owner.Login, r.Name
	}
	return info, nil
}
