package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/binbandit/yip/internal/forge"
	"github.com/binbandit/yip/protocol"
)

type ghRepo struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	FullName      string  `json:"full_name"`
	Owner         *ghUser `json:"owner"`
	DefaultBranch string  `json:"default_branch"`
	Private       bool    `json:"private"`
}

type ghBranch struct {
	Ref  string  `json:"ref"`
	SHA  string  `json:"sha"`
	Repo *ghRepo `json:"repo"`
}

type ghPull struct {
	Number         int        `json:"number"`
	HTMLURL        string     `json:"html_url"`
	Title          string     `json:"title"`
	State          string     `json:"state"` // open | closed
	Draft          bool       `json:"draft"`
	Merged         bool       `json:"merged"`
	MergedAt       *time.Time `json:"merged_at"`
	Mergeable      *bool      `json:"mergeable"`
	MergeableState string     `json:"mergeable_state"`
	ChangedFiles   int        `json:"changed_files"`
	UpdatedAt      time.Time  `json:"updated_at"`
	User           *ghUser    `json:"user"`
	Head           ghBranch   `json:"head"`
	Base           ghBranch   `json:"base"`
}

func (p *ghPull) isMerged() bool { return p.Merged || p.MergedAt != nil }

func (p *ghPull) state() string {
	if p.isMerged() {
		return "merged"
	}
	return p.State
}

func (c *Connector) getPull(ctx context.Context, repo forge.RepoRef, number int) (*ghPull, error) {
	path, err := c.pullPath(repo, number)
	if err != nil {
		return nil, err
	}
	var p ghPull
	if err := c.getJSON(ctx, path, nil, &p); err != nil {
		return nil, err
	}
	if p.Head.SHA == "" || p.Base.SHA == "" {
		return nil, fmt.Errorf("github: GET %s returned no head/base SHA", path)
	}
	return &p, nil
}

func (c *Connector) toPR(repo forge.RepoRef, p *ghPull) forge.PR {
	ref := forge.RepoRef{Host: c.host, Owner: repo.Owner, Name: repo.Name}
	// Prefer GitHub's canonical names (repositories can be renamed or
	// transferred; GitHub redirects the old path).
	if br := p.Base.Repo; br != nil && br.Name != "" && br.Owner != nil && br.Owner.Login != "" {
		ref.Owner, ref.Name = br.Owner.Login, br.Name
	}
	return forge.PR{
		Repo:      ref,
		Number:    p.Number,
		URL:       p.HTMLURL,
		Title:     p.Title,
		State:     p.state(),
		Base:      p.Base.SHA,
		BaseRef:   p.Base.Ref,
		Head:      p.Head.SHA,
		HeadRef:   p.Head.Ref,
		Author:    p.User.login(),
		Draft:     p.Draft,
		UpdatedAt: p.UpdatedAt,
	}
}

// GetPR fetches GET /repos/{o}/{r}/pulls/{n}.
func (c *Connector) GetPR(ctx context.Context, repo forge.RepoRef, number int) (forge.PR, error) {
	p, err := c.getPull(ctx, repo, number)
	if err != nil {
		return forge.PR{}, err
	}
	return c.toPR(repo, p), nil
}

type ghFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

// Files lists the PR's changed files (GET .../pulls/{n}/files, paginated).
// It reads the PR head/base before and after listing and returns
// forge.ErrStale if either moved, so the result belongs to one revision.
// GitHub omits Patch for binary and very large files, and lists at most 3000
// files; a PR beyond that yields the partial list together with ErrTruncated.
func (c *Connector) Files(ctx context.Context, repo forge.RepoRef, number int) ([]forge.FileDiff, error) {
	return c.FilesAt(ctx, repo, number, "")
}

// FilesAt is Files pinned to an expected head SHA: it returns forge.ErrStale
// if the PR head is not expectHead (when non-empty) or moves while listing.
func (c *Connector) FilesAt(ctx context.Context, repo forge.RepoRef, number int, expectHead string) ([]forge.FileDiff, error) {
	before, err := c.getPull(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	if expectHead != "" && !strings.EqualFold(before.Head.SHA, expectHead) {
		return nil, fmt.Errorf("%w: head is %s, expected %s", forge.ErrStale, before.Head.SHA, expectHead)
	}
	path, _ := c.pullPath(repo, number) // validated by getPull
	files, more, err := getList[ghFile](ctx, c, path+"/files", nil, filesMaxPages)
	if err != nil {
		return nil, err
	}
	after, err := c.getPull(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	if after.Head.SHA != before.Head.SHA || after.Base.SHA != before.Base.SHA {
		return nil, fmt.Errorf("%w: pull request moved from %s..%s to %s..%s while listing files",
			forge.ErrStale, before.Base.SHA, before.Head.SHA, after.Base.SHA, after.Head.SHA)
	}
	out := make([]forge.FileDiff, 0, len(files))
	for _, f := range files {
		out = append(out, forge.FileDiff{
			Path:      f.Filename,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Patch:     f.Patch,
		})
	}
	if more || after.ChangedFiles > len(out) {
		return out, fmt.Errorf("%w: listed %d of %d changed files", ErrTruncated, len(out), after.ChangedFiles)
	}
	return out, nil
}

// MergeStatus reports GitHub's own view of mergeability from the PR's
// merged, mergeable and mergeable_state fields. GitHub computes mergeability
// lazily; while mergeable is null the PR is re-fetched a few times and then
// reported as "unknown" rather than guessed. Branch-protection specifics are
// not readable without admin access and are never invented.
func (c *Connector) MergeStatus(ctx context.Context, repo forge.RepoRef, number int) (protocol.MergeStatus, error) {
	var p *ghPull
	for attempt := 0; ; attempt++ {
		var err error
		p, err = c.getPull(ctx, repo, number)
		if err != nil {
			return protocol.MergeStatus{}, err
		}
		if p.Mergeable != nil || p.isMerged() || p.State != "open" || attempt >= c.mergePolls {
			break
		}
		if err := sleep(ctx, c.mergeBackoff); err != nil {
			return protocol.MergeStatus{}, err
		}
	}
	return mergeStatus(p), nil
}

// Mergeable values reported in protocol.MergeStatus.
const (
	MergeClean    = "clean"
	MergeBlocked  = "blocked"
	MergeDirty    = "dirty"
	MergeBehind   = "behind"
	MergeUnstable = "unstable"
	MergeUnknown  = "unknown"
)

func mergeStatus(p *ghPull) protocol.MergeStatus {
	st := protocol.MergeStatus{Merged: p.isMerged(), Reasons: []string{}}
	switch {
	case p.isMerged():
		st.Mergeable = MergeBlocked
		st.Reasons = append(st.Reasons, "pull request is already merged")
		return st
	case p.State != "open":
		st.Mergeable = MergeBlocked
		st.Reasons = append(st.Reasons, "pull request is closed")
		return st
	case p.Draft || p.MergeableState == "draft":
		st.Mergeable = MergeBlocked
		st.Reasons = append(st.Reasons, "pull request is a draft")
		return st
	case p.Mergeable == nil:
		st.Mergeable = MergeUnknown
		st.Reasons = append(st.Reasons, "GitHub has not finished computing mergeability; check again shortly")
		return st
	}
	switch p.MergeableState {
	case "clean":
		st.Mergeable = MergeClean
	case "has_hooks":
		st.Mergeable = MergeClean
		st.Reasons = append(st.Reasons, "mergeable; repository pre-receive hooks will run on merge")
	case "unstable":
		st.Mergeable = MergeUnstable
		st.Reasons = append(st.Reasons, "mergeable, but some commit statuses or checks are not passing")
	case "behind":
		st.Mergeable = MergeBehind
		st.Reasons = append(st.Reasons, "head branch is out of date with the base branch")
	case "dirty":
		st.Mergeable = MergeDirty
		st.Reasons = append(st.Reasons, "merge conflicts with the base branch")
	case "blocked":
		st.Mergeable = MergeBlocked
		st.Reasons = append(st.Reasons,
			"GitHub reports merging is blocked (required reviews, required checks, or other branch protection/ruleset requirements are not satisfied; the specific rule is not visible to this credential)")
	default:
		if !*p.Mergeable {
			// mergeable=false means GitHub could not create a test merge
			// commit, i.e. the branches conflict.
			st.Mergeable = MergeDirty
			st.Reasons = append(st.Reasons, "GitHub reports the pull request cannot be merged cleanly")
		} else {
			st.Mergeable = MergeUnknown
			if p.MergeableState == "" || p.MergeableState == "unknown" {
				st.Reasons = append(st.Reasons, "GitHub reports mergeability as unknown")
			} else {
				st.Reasons = append(st.Reasons, fmt.Sprintf("unrecognized mergeable_state %q", p.MergeableState))
			}
		}
	}
	if p.Mergeable != nil && !*p.Mergeable && st.Mergeable == MergeClean {
		// Contradictory response; do not claim clean.
		st.Mergeable = MergeUnknown
		st.Reasons = append(st.Reasons, "GitHub reports mergeable=false with mergeable_state clean")
	}
	return st
}
