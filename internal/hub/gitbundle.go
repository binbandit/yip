package hub

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

// bundleHeader is what a git bundle declares before its pack: the refs it
// carries and any prerequisite commits it needs from elsewhere.
type bundleHeader struct {
	Heads   map[string]string // branch name → commit
	HEAD    string            // commit HEAD pointed at, if recorded
	Prereqs int
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// parseBundleHeader reads a v2 or v3 git bundle header without needing git.
func parseBundleHeader(r io.Reader) (bundleHeader, error) {
	h := bundleHeader{Heads: map[string]string{}}
	br := bufio.NewReaderSize(r, 64<<10)
	sig, err := br.ReadString('\n')
	if err != nil || (sig != "# v2 git bundle\n" && sig != "# v3 git bundle\n") {
		return h, errors.New("this isn't a git bundle (create one with `git bundle create <file> --all`)")
	}
	for i := 0; ; i++ {
		if i > 100_000 {
			return h, errors.New("the bundle header is too large")
		}
		line, err := br.ReadString('\n')
		if err != nil {
			return h, errors.New("the bundle header is truncated")
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
			return h, nil // the pack follows
		case strings.HasPrefix(line, "@"):
			continue // v3 capability
		case strings.HasPrefix(line, "-"):
			h.Prereqs++
		default:
			sha, ref, ok := strings.Cut(line, " ")
			if !ok || !shaRe.MatchString(sha) {
				return h, errors.New("the bundle header is malformed")
			}
			if ref == "HEAD" {
				h.HEAD = sha
			} else if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
				h.Heads[name] = sha
			}
		}
	}
}

// defaultBranch picks the requested branch, else the one HEAD points at,
// else main or master, else the first branch.
func (b bundleHeader) defaultBranch(requested string) (string, error) {
	if requested != "" {
		if _, ok := b.Heads[requested]; !ok {
			return "", fmt.Errorf("the bundle has no branch %q (it has %s)", requested, strings.Join(b.branches(), ", "))
		}
		return requested, nil
	}
	names := b.branches()
	for _, n := range names {
		if b.HEAD != "" && b.Heads[n] == b.HEAD {
			return n, nil
		}
	}
	for _, n := range []string{"main", "master"} {
		if _, ok := b.Heads[n]; ok {
			return n, nil
		}
	}
	return names[0], nil
}

func (b bundleHeader) branches() []string { return slices.Sorted(maps.Keys(b.Heads)) }

// ImportRepo adds (or refreshes) a repository from a git bundle the owner
// uploads, for code with no remote the machines can reach (spec: register a
// reachable remote or explicitly import a snapshot/bundle). Machines build
// their replica from the bundle; nothing can be pushed anywhere from it.
func (h *Hub) ImportRepo(ctx context.Context, userID, projectID, repoID, name, branch string, body io.Reader) (protocol.Project, error) {
	name, branch = strings.TrimSpace(name), strings.TrimSpace(branch)
	var p protocol.Project
	if repoID == "" && name == "" {
		return p, domain.Invalid("Name the repository.")
	}
	if _, err := store.GetProject(ctx, h.st.R(), projectID); err != nil {
		return p, domain.NotFound("That project doesn't exist.")
	}
	var existing *protocol.Repo
	if repoID != "" {
		r, err := store.GetRepo(ctx, h.st.R(), repoID)
		if err != nil || r.ProjectID != projectID {
			return p, domain.NotFound("That repository isn't in this project.")
		}
		if r.RemoteURL != "" {
			return p, domain.Conflict("%s uses a remote (%s); its machines fetch from there, so it can't be replaced by a bundle.", r.Name, r.RemoteURL)
		}
		existing = &r
	}
	hash, size, err := h.artifacts.PutStream(body, MaxArtifactBytes)
	if err != nil {
		return p, domain.Invalid("%s", err.Error())
	}
	f, err := h.artifacts.Open(hash)
	if err != nil {
		return p, err
	}
	hdr, err := parseBundleHeader(f)
	f.Close()
	if err != nil {
		return p, domain.Invalid("%s", err.Error())
	}
	if hdr.Prereqs > 0 {
		return p, domain.Invalid("This bundle needs %d commits it doesn't include (it was made from a partial history). Create it with `git bundle create <file> --all`.", hdr.Prereqs)
	}
	if len(hdr.Heads) == 0 {
		return p, domain.Invalid("This bundle has no branches. Create it with `git bundle create <file> --all`.")
	}
	if existing != nil && branch == "" {
		if _, ok := hdr.Heads[existing.DefaultBranch]; ok {
			branch = existing.DefaultBranch
		}
	}
	def, err := hdr.defaultBranch(branch)
	if err != nil {
		return p, domain.Invalid("%s", err.Error())
	}
	now := h.now()
	err = h.do(ctx, func(t *txn) error {
		repo := protocol.Repo{ID: domain.NewID(), ProjectID: projectID, Name: name, DefaultBranch: def, Forge: "none", CreatedAt: now}
		if existing != nil {
			repo = *existing
			repo.DefaultBranch = def
			if name != "" {
				repo.Name = name
			}
		}
		art := protocol.Artifact{ID: domain.NewID(), Hash: hash, Name: repo.Name + ".bundle", ContentType: "application/x-git-bundle", Size: size,
			Kind: "source_bundle", ProjectID: projectID, Revision: hdr.Heads[def], CreatedAt: now}
		if err := store.InsertArtifact(ctx, t.tx, h.Org().ID, art); err != nil {
			return err
		}
		repo.SourceBundleID, repo.ImportedAt = art.ID, &now
		if err := store.PutRepo(ctx, t.tx, repo); err != nil {
			return err
		}
		if err := t.audit(userActor(userID), "owner", "repo.import", repo.ID, "ok",
			fmt.Sprintf("%s from a bundle (%d branches, %s at %s)", repo.Name, len(hdr.Heads), def, shortRev(hdr.Heads[def]))); err != nil {
			return err
		}
		if p, err = store.GetProject(ctx, t.tx, projectID); err != nil {
			return err
		}
		return t.emit(ev{Type: "project.updated", Actor: userActor(userID), Payload: p})
	})
	return p, err
}
