package hub

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"

	"github.com/binbandit/yip/internal/domain"
	"github.com/binbandit/yip/internal/store"
	"github.com/binbandit/yip/protocol"
)

var knownProviders = map[string]string{
	"codex":    "Codex",
	"claude":   "Claude Code",
	"cursor":   "Cursor",
	"opencode": "OpenCode",
	"pi":       "Pi Agent Harness",
}

// ProviderLabel returns the user-facing provider name.
func ProviderLabel(p string) string {
	if l, ok := knownProviders[p]; ok {
		return l
	}
	return p
}

func hueFor(name string) int {
	f := fnv.New32a()
	f.Write([]byte(strings.ToLower(name)))
	return int(f.Sum32() % 360)
}

// ---- engineers ----

func (h *Hub) ListEngineers(ctx context.Context) ([]protocol.Engineer, error) {
	return store.ListEngineers(ctx, h.st.R())
}

func (h *Hub) GetEngineer(ctx context.Context, id string) (protocol.Engineer, []protocol.EngineerVersion, error) {
	e, err := store.GetEngineer(ctx, h.st.R(), id)
	if errors.Is(err, store.ErrNotFound) {
		return e, nil, domain.NotFound("That engineer doesn't exist.")
	}
	if err != nil {
		return e, nil, err
	}
	vs, err := store.ListEngineerVersions(ctx, h.st.R(), id)
	return e, vs, err
}

func validateProvider(p protocol.ProviderPreference) error {
	if _, ok := knownProviders[p.Provider]; !ok {
		return domain.Invalid("Choose a supported tool from Connections.")
	}
	for _, alt := range p.Alternatives {
		if _, ok := knownProviders[alt]; !ok || alt == p.Provider {
			return domain.Invalid("Alternative provider %q is not valid.", alt)
		}
	}
	return nil
}

// CreateEngineer creates a persistent engineer identity with its first
// configuration version.
func (h *Hub) CreateEngineer(ctx context.Context, userID string, req protocol.CreateEngineerRequest) (protocol.Engineer, error) {
	req.Name, req.Role = strings.TrimSpace(req.Name), strings.TrimSpace(req.Role)
	if req.Name == "" || req.Role == "" {
		return protocol.Engineer{}, domain.Invalid("An engineer needs a name and a role.")
	}
	handle := domain.Handle(req.Handle)
	if handle == "" {
		handle = domain.Handle(req.Name)
	}
	if handle == "" {
		return protocol.Engineer{}, domain.Invalid("Choose a handle made of letters or numbers.")
	}
	if err := validateProvider(req.Provider); err != nil {
		return protocol.Engineer{}, err
	}
	var e protocol.Engineer
	err := h.do(ctx, func(t *txn) error {
		if _, err := store.GetEngineerByHandle(ctx, t.tx, handle); err == nil {
			return domain.Conflict("Another engineer already uses @%s.", handle)
		}
		now := h.now()
		e = protocol.Engineer{ID: domain.NewID(), OrgID: h.Org().ID, Name: req.Name, Handle: handle, Role: req.Role,
			Description: strings.TrimSpace(req.Description), Instructions: strings.TrimSpace(req.Instructions),
			CapabilityTags: nonNil(req.CapabilityTags), Provider: req.Provider, Hue: hueFor(req.Name), VersionID: domain.NewID(),
			VersionNo: 1, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.InsertEngineer(ctx, t.tx, e); err != nil {
			return err
		}
		var err error
		if e, err = store.GetEngineer(ctx, t.tx, e.ID); err != nil {
			return err
		}
		return t.emit(ev{Type: "engineer.created", Actor: userActor(userID), Payload: e})
	})
	return e, err
}

// UpdateEngineer writes a new configuration version. Active runs keep the
// snapshot they started with; the ID and attribution history are preserved
// across renames.
func (h *Hub) UpdateEngineer(ctx context.Context, userID, id string, req protocol.UpdateEngineerRequest) (protocol.Engineer, error) {
	var e protocol.Engineer
	err := h.do(ctx, func(t *txn) error {
		cur, err := store.GetEngineer(ctx, t.tx, id)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NotFound("That engineer doesn't exist.")
		}
		if err != nil {
			return err
		}
		if cur.Version != req.Version {
			return domain.Conflict("%s's profile changed since you opened it. Reload and try again.", cur.Name)
		}
		if req.Archived != nil {
			ok, err := store.SetEngineerArchived(ctx, t.tx, id, *req.Archived, req.Version)
			if err != nil {
				return err
			}
			if !ok {
				return domain.Conflict("%s's profile changed since you opened it.", cur.Name)
			}
			req.Version++
		}
		changed := false
		next := protocol.EngineerVersion{ID: domain.NewID(), EngineerID: id, VersionNo: cur.VersionNo + 1, Name: cur.Name, Role: cur.Role,
			Description: cur.Description, Instructions: cur.Instructions, CapabilityTags: cur.CapabilityTags, Provider: cur.Provider, CreatedAt: h.now()}
		set := func(dst *string, v *string) {
			if v != nil && strings.TrimSpace(*v) != *dst {
				*dst = strings.TrimSpace(*v)
				changed = true
			}
		}
		set(&next.Name, req.Name)
		set(&next.Role, req.Role)
		set(&next.Description, req.Description)
		set(&next.Instructions, req.Instructions)
		if req.CapabilityTags != nil {
			next.CapabilityTags, changed = nonNil(*req.CapabilityTags), true
		}
		if req.Provider != nil {
			if err := validateProvider(*req.Provider); err != nil {
				return err
			}
			next.Provider, changed = *req.Provider, true
		}
		if next.Name == "" || next.Role == "" {
			return domain.Invalid("An engineer needs a name and a role.")
		}
		if changed {
			if err := store.InsertEngineerVersion(ctx, t.tx, next); err != nil {
				return err
			}
			ok, err := store.SetEngineerVersion(ctx, t.tx, id, next.ID, cur.Handle, req.Version)
			if err != nil {
				return err
			}
			if !ok {
				return domain.Conflict("%s's profile changed since you opened it.", cur.Name)
			}
			if req.Provider != nil {
				if err := store.InvalidateProviderSessions(ctx, t.tx, id, "provider changed"); err != nil {
					return err
				}
				if err := store.RepointQueuedRuns(ctx, t.tx, id, next.ID, next.Provider.Provider, next.Provider.Model, next.Provider.ProfileID); err != nil {
					return err
				}
				t.kickAfter()
			}
		}
		if e, err = store.GetEngineer(ctx, t.tx, id); err != nil {
			return err
		}
		return t.emit(ev{Type: "engineer.updated", Actor: userActor(userID), Payload: e})
	})
	return e, err
}

// ---- projects ----

func (h *Hub) ListProjects(ctx context.Context) ([]protocol.Project, error) {
	return store.ListProjects(ctx, h.st.R())
}

func (h *Hub) GetProject(ctx context.Context, id string) (protocol.Project, error) {
	p, err := store.GetProject(ctx, h.st.R(), id)
	if errors.Is(err, store.ErrNotFound) {
		return p, domain.NotFound("That project doesn't exist.")
	}
	return p, err
}

func (h *Hub) CreateProject(ctx context.Context, userID string, req protocol.CreateProjectRequest) (protocol.Project, error) {
	// Repositories are resolved first (possibly asking GitHub), so a project
	// made from a mistyped owner/name isn't left behind half created.
	repos := make([]protocol.PutRepoRequest, 0, len(req.Repos))
	for _, r := range req.Repos {
		r, err := h.resolveRepo(ctx, r)
		if err != nil {
			return protocol.Project{}, err
		}
		repos = append(repos, r)
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" && len(repos) > 0 {
		req.Name = repos[0].Name
	}
	if req.Name == "" {
		return protocol.Project{}, domain.Invalid("Give the project a name.")
	}
	if req.Policy.ExecutionProfile == "" {
		req.Policy.ExecutionProfile = "native"
	}
	reqs, err := normalizeRequires(req.Policy.Requires)
	if err != nil {
		return protocol.Project{}, err
	}
	req.Policy.Requires = reqs
	var p protocol.Project
	err = h.do(ctx, func(t *txn) error {
		p = protocol.Project{ID: domain.NewID(), OrgID: h.Org().ID, Name: req.Name, Description: strings.TrimSpace(req.Description),
			Instructions: strings.TrimSpace(req.Instructions), Policy: req.Policy, CreatedAt: h.now()}
		p.Policy.Checks = nonNil(p.Policy.Checks)
		if err := store.InsertProject(ctx, t.tx, p); err != nil {
			return err
		}
		for _, r := range repos {
			if err := store.PutRepo(ctx, t.tx, protocol.Repo{ID: domain.NewID(), ProjectID: p.ID, Name: r.Name, RemoteURL: r.RemoteURL,
				DefaultBranch: r.DefaultBranch, Forge: r.Forge, ForgeRepo: r.ForgeRepo, CreatedAt: h.now()}); err != nil {
				return err
			}
		}
		for _, roomID := range req.RoomIDs {
			r, err := h.requireRoom(ctx, t.tx, userID, roomID)
			if err != nil {
				return err
			}
			if err := store.SetRoomProjects(ctx, t.tx, roomID, append(r.ProjectIDs, p.ID)); err != nil {
				return err
			}
		}
		var err error
		if p, err = store.GetProject(ctx, t.tx, p.ID); err != nil {
			return err
		}
		return t.emit(ev{Type: "project.created", Actor: userActor(userID), Payload: p})
	})
	return p, err
}

func (h *Hub) UpdateProject(ctx context.Context, userID, id string, req protocol.UpdateProjectRequest) (protocol.Project, error) {
	var p protocol.Project
	err := h.do(ctx, func(t *txn) error {
		cur, err := store.GetProject(ctx, t.tx, id)
		if err != nil {
			return domain.NotFound("That project doesn't exist.")
		}
		if cur.Version != req.Version {
			return domain.Conflict("This project changed since you opened it. Reload and try again.")
		}
		if req.Name != nil {
			cur.Name = strings.TrimSpace(*req.Name)
		}
		if req.Description != nil {
			cur.Description = strings.TrimSpace(*req.Description)
		}
		if req.Instructions != nil {
			cur.Instructions = strings.TrimSpace(*req.Instructions)
		}
		if req.Policy != nil {
			cur.Policy = *req.Policy
			cur.Policy.Checks = nonNil(cur.Policy.Checks)
			reqs, err := normalizeRequires(cur.Policy.Requires)
			if err != nil {
				return err
			}
			cur.Policy.Requires = reqs
		}
		ok, err := store.UpdateProject(ctx, t.tx, cur, req.Version)
		if err != nil {
			return err
		}
		if !ok {
			return domain.Conflict("This project changed since you opened it. Reload and try again.")
		}
		if p, err = store.GetProject(ctx, t.tx, id); err != nil {
			return err
		}
		return t.emit(ev{Type: "project.updated", Actor: userActor(userID), Payload: p})
	})
	return p, err
}

// PutRepo registers a repository by GitHub owner/name or reachable remote
// URL (see resolveRepo). There is no hidden synchronization from the laptop:
// uncommitted local files are not available to runners until deliberately
// pushed or imported.
func (h *Hub) PutRepo(ctx context.Context, userID, projectID, repoID string, req protocol.PutRepoRequest) (protocol.Project, error) {
	req, err := h.resolveRepo(ctx, req)
	if err != nil {
		return protocol.Project{}, err
	}
	var p protocol.Project
	err = h.do(ctx, func(t *txn) error {
		if _, err := store.GetProject(ctx, t.tx, projectID); err != nil {
			return domain.NotFound("That project doesn't exist.")
		}
		if repoID == "" || !domain.ValidID(repoID) {
			repoID = domain.NewID()
		}
		if existing, err := store.GetRepo(ctx, t.tx, repoID); err == nil && existing.ProjectID != projectID {
			return domain.Conflict("That repository belongs to another project.")
		}
		// Giving an imported repository a remote switches it to that remote.
		if err := store.PutRepo(ctx, t.tx, protocol.Repo{ID: repoID, ProjectID: projectID, Name: req.Name, RemoteURL: req.RemoteURL,
			DefaultBranch: req.DefaultBranch, Forge: req.Forge, ForgeRepo: req.ForgeRepo, CreatedAt: h.now()}); err != nil {
			return err
		}
		var err error
		if p, err = store.GetProject(ctx, t.tx, projectID); err != nil {
			return err
		}
		return t.emit(ev{Type: "project.updated", Actor: userActor(userID), Payload: p})
	})
	return p, err
}

// PutGrant sets an engineer's access to a project. Room membership grants
// conversation access only; repository access comes from these grants.
func (h *Hub) PutGrant(ctx context.Context, userID, projectID, engineerID string, req protocol.PutGrantRequest) (protocol.Project, error) {
	if req.Access != "read" && req.Access != "write" && req.Access != "none" {
		return protocol.Project{}, domain.Invalid("Access must be read, write, or none.")
	}
	for _, a := range req.Actions {
		if !contains([]string{"push", "open_pr", "publish_review", "merge"}, a) {
			return protocol.Project{}, domain.Invalid("Unknown action %q.", a)
		}
	}
	var p protocol.Project
	err := h.do(ctx, func(t *txn) error {
		if _, err := store.GetProject(ctx, t.tx, projectID); err != nil {
			return domain.NotFound("That project doesn't exist.")
		}
		if _, err := store.GetEngineer(ctx, t.tx, engineerID); err != nil {
			return domain.NotFound("That engineer doesn't exist.")
		}
		if req.Access == "none" {
			if err := store.DeleteGrant(ctx, t.tx, projectID, engineerID); err != nil {
				return err
			}
		} else if err := store.PutGrant(ctx, t.tx, protocol.Grant{ID: domain.NewID(), ProjectID: projectID, EngineerID: engineerID,
			Access: req.Access, Actions: nonNil(req.Actions)}); err != nil {
			return err
		}
		// Grant changes alter the engineer's scope fingerprint.
		if err := store.InvalidateProviderSessions(ctx, t.tx, engineerID, "project access changed"); err != nil {
			return err
		}
		if err := t.audit(userActor(userID), "owner", "grant.set", projectID+"/"+engineerID, "ok", req.Access+" "+strings.Join(req.Actions, ",")); err != nil {
			return err
		}
		var err error
		if p, err = store.GetProject(ctx, t.tx, projectID); err != nil {
			return err
		}
		return t.emit(ev{Type: "project.updated", Actor: userActor(userID), Payload: p})
	})
	return p, err
}

// normalizeRequires cleans a project's machine requirements: lower-case,
// de-duplicated tool names or os:<name>.
func normalizeRequires(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, r := range in {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" || seen[r] {
			continue
		}
		name := strings.TrimPrefix(r, "os:")
		if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-_.+") != "" {
			return nil, domain.Invalid("%q isn't a tool name or os:<name> (for example go, node, docker, os:darwin).", r)
		}
		seen[r] = true
		out = append(out, r)
	}
	return out, nil
}
