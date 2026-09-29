package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"embed"
	"github.com/binbandit/yip/internal/hub"
	"github.com/binbandit/yip/protocol"
)

//go:embed all:testdata/repos
var fixtureRepos embed.FS

// Result describes the seeded workspace.
type Result struct {
	Handle   string
	Password string
	Rooms    map[string]string
	Engines  map[string]string
	Projects map[string]string
}

// MaterializeRepos writes the fixture repositories into dir as bare git
// repositories and returns name → file:// remote URL.
func MaterializeRepos(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := fs.ReadDir(fixtureRepos, "testdata/repos")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		src := filepath.Join(dir, "src", name)
		bare := filepath.Join(dir, name+".git")
		if _, err := os.Stat(bare); err == nil {
			out[name] = "file://" + bare
			continue
		}
		root := "testdata/repos/" + name
		err := fs.WalkDir(fixtureRepos, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
			if rel == "" {
				return os.MkdirAll(src, 0o755)
			}
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(src, rel), 0o755)
			}
			b, err := fs.ReadFile(fixtureRepos, p)
			if err != nil {
				return err
			}
			if filepath.Base(rel) == "_go.mod" {
				rel = filepath.Join(filepath.Dir(rel), "go.mod")
			}
			return os.WriteFile(filepath.Join(src, rel), b, 0o644)
		})
		if err != nil {
			return nil, err
		}
		for _, args := range [][]string{
			{"init", "-q", "-b", "main"},
			{"add", "-A"},
			{"-c", "user.name=yip fixtures", "-c", "user.email=fixtures@yip.invalid", "commit", "-q", "-m", "Initial " + name + " fixture"},
		} {
			if err := git(src, args...); err != nil {
				return nil, err
			}
		}
		if err := git(dir, "clone", "-q", "--bare", src, bare); err != nil {
			return nil, err
		}
		out[name] = "file://" + bare
	}
	return out, nil
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

// Seed creates the owner and browser fixture workspace in an empty hub.
func Seed(ctx context.Context, h *hub.Hub, repos map[string]string, ownerName, handle, password string) (Result, error) {
	res := Result{Handle: handle, Password: password, Rooms: map[string]string{}, Engines: map[string]string{}, Projects: map[string]string{}}
	secret, _, err := h.IssueBootstrapSecret(ctx)
	if err != nil {
		return res, err
	}
	user, err := h.Setup(ctx, protocol.SetupRequest{BootstrapSecret: secret, OrgName: ownerName + "'s workspace", Name: ownerName, Handle: handle, Password: password})
	if err != nil {
		return res, err
	}
	preference := protocol.ProviderPreference{Provider: "codex"}
	engineers := []protocol.CreateEngineerRequest{
		{Name: "Mira", Role: "Platform engineer", Provider: preference, CapabilityTags: []string{"go", "backend", "sessions", "platform"},
			Description:  "Builds the foundation, follows the edges, and leaves the next person a clear path.",
			Instructions: "Preserve established contracts. Prefer small, well-tested changes. Get an independent review for security-sensitive code."},
		{Name: "Oren", Role: "Security engineer", Provider: preference, CapabilityTags: []string{"security", "review", "auth"},
			Description:  "Looks for the assumption that could break, and backs every finding with evidence.",
			Instructions: "Review the actual revision and surrounding code. Distinguish blocking defects from suggestions. Never approve what you could not check."},
		{Name: "Pip", Role: "Reverse engineer", Provider: preference, CapabilityTags: []string{"reverse-engineering", "tracing", "documentation"},
			Description:  "Makes unfamiliar systems understandable, one trace and useful diagram at a time.",
			Instructions: "Map request flows from the code itself and cite source locations. Ask only for what you genuinely cannot find."},
	}
	for _, req := range engineers {
		e, err := h.CreateEngineer(ctx, user.ID, req)
		if err != nil {
			return res, err
		}
		res.Engines[e.Handle] = e.ID
	}
	atlas, err := h.CreateProject(ctx, user.ID, protocol.CreateProjectRequest{Name: "Atlas", Description: "Identity and session services.",
		Instructions: "Session validity is a server-side contract; see docs/sessions.md. Run `go test ./...` before publishing.",
		Policy:       protocol.ProjectPolicy{RequirePeerReview: true, Checks: []string{"go test ./..."}, ExecutionProfile: "native"}})
	if err != nil {
		return res, err
	}
	beacon, err := h.CreateProject(ctx, user.ID, protocol.CreateProjectRequest{Name: "Beacon", Description: "Request gateway and background workers.",
		Instructions: "Document request flow with source locations. The retry worker is deployed separately.",
		Policy:       protocol.ProjectPolicy{ExecutionProfile: "native"}})
	if err != nil {
		return res, err
	}
	res.Projects["atlas"], res.Projects["beacon"] = atlas.ID, beacon.ID
	if _, err := h.PutRepo(ctx, user.ID, atlas.ID, "", protocol.PutRepoRequest{Name: "atlas", RemoteURL: repos["atlas"], DefaultBranch: "main"}); err != nil {
		return res, err
	}
	if _, err := h.PutRepo(ctx, user.ID, beacon.ID, "", protocol.PutRepoRequest{Name: "beacon-gateway", RemoteURL: repos["beacon-gateway"], DefaultBranch: "main"}); err != nil {
		return res, err
	}
	grants := []struct{ project, eng, access string }{
		{atlas.ID, "mira", "write"}, {atlas.ID, "oren", "read"}, {beacon.ID, "mira", "read"}, {beacon.ID, "pip", "write"},
	}
	for _, g := range grants {
		if _, err := h.PutGrant(ctx, user.ID, g.project, res.Engines[g.eng], protocol.PutGrantRequest{Access: g.access}); err != nil {
			return res, err
		}
	}
	rooms := []protocol.CreateRoomRequest{
		{Name: "Security", Purpose: "Small details. Strong foundations.", EngineerIDs: []string{res.Engines["mira"], res.Engines["oren"]},
			ProjectIDs: []string{atlas.ID}, ReplyMode: protocol.ReplyModeQuiet},
		{Name: "Engineering", Purpose: "Build it, understand it, make it better.", EngineerIDs: []string{res.Engines["mira"], res.Engines["pip"]},
			ProjectIDs: []string{atlas.ID, beacon.ID}, ReplyMode: protocol.ReplyModeSteward, StewardID: res.Engines["mira"]},
		{Name: "Reverse engineering", Purpose: "Follow the path. Explain what is really happening.", EngineerIDs: []string{res.Engines["pip"]},
			ProjectIDs: []string{beacon.ID}, ReplyMode: protocol.ReplyModeQuiet},
	}
	for _, rq := range rooms {
		r, err := h.CreateRoom(ctx, user.ID, rq)
		if err != nil {
			return res, err
		}
		res.Rooms[strings.ToLower(strings.Fields(r.Name)[0])] = r.ID
	}
	return res, nil
}
