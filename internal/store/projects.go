package store

import (
	"context"
	"database/sql"

	"github.com/binbandit/yip/protocol"
)

const projectCols = `id, org_id, name, description, instructions, policy, created_at, version`

func scanProject(s scanner) (protocol.Project, error) {
	var p protocol.Project
	var policy, created string
	err := s.Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.Instructions, &policy, &created, &p.Version)
	unjs(policy, &p.Policy)
	p.Policy.Checks = strs(p.Policy.Checks)
	p.CreatedAt = parseTS(created)
	p.Repos, p.Grants, p.RoomIDs = []protocol.Repo{}, []protocol.Grant{}, []string{}
	return p, err
}

func InsertProject(ctx context.Context, q Q, p protocol.Project) error {
	_, err := q.ExecContext(ctx, `INSERT INTO projects(id, org_id, name, description, instructions, policy, created_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)`, p.ID, p.OrgID, p.Name, p.Description, p.Instructions, js(p.Policy), ts(p.CreatedAt))
	return err
}

func UpdateProject(ctx context.Context, q Q, p protocol.Project, expectVersion int64) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE projects SET name = ?, description = ?, instructions = ?, policy = ?, version = version + 1
		WHERE id = ? AND version = ?`, p.Name, p.Description, p.Instructions, js(p.Policy), p.ID, expectVersion)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func GetProject(ctx context.Context, q Q, id string) (protocol.Project, error) {
	p, err := scanProject(q.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
	if err != nil {
		return p, notFound(err)
	}
	return p, fillProject(ctx, q, &p)
}

func ListProjects(ctx context.Context, q Q) ([]protocol.Project, error) {
	ps, err := list(ctx, q, scanProject, `SELECT `+projectCols+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	for i := range ps {
		if err := fillProject(ctx, q, &ps[i]); err != nil {
			return nil, err
		}
	}
	return ps, nil
}

func fillProject(ctx context.Context, q Q, p *protocol.Project) error {
	var err error
	if p.Repos, err = list(ctx, q, scanRepo, `SELECT `+repoCols+` FROM repos WHERE project_id = ? ORDER BY name`, p.ID); err != nil {
		return err
	}
	if p.Grants, err = list(ctx, q, scanGrant, `SELECT `+grantCols+` FROM project_grants WHERE project_id = ?`, p.ID); err != nil {
		return err
	}
	p.RoomIDs, err = stringsCol(ctx, q, `SELECT room_id FROM room_projects WHERE project_id = ?`, p.ID)
	return err
}

const repoCols = `id, project_id, name, remote_url, default_branch, forge, forge_repo, created_at, COALESCE(source_bundle_id, ''), imported_at`

func scanRepo(s scanner) (protocol.Repo, error) {
	var r protocol.Repo
	var created string
	var imported sql.NullString
	err := s.Scan(&r.ID, &r.ProjectID, &r.Name, &r.RemoteURL, &r.DefaultBranch, &r.Forge, &r.ForgeRepo, &created, &r.SourceBundleID, &imported)
	r.CreatedAt = parseTS(created)
	r.ImportedAt = parseTSP(imported)
	return r, err
}

func PutRepo(ctx context.Context, q Q, r protocol.Repo) error {
	_, err := q.ExecContext(ctx, `INSERT INTO repos(id, project_id, name, remote_url, default_branch, forge, forge_repo, created_at, source_bundle_id, imported_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, remote_url = excluded.remote_url, default_branch = excluded.default_branch,
			forge = excluded.forge, forge_repo = excluded.forge_repo, source_bundle_id = excluded.source_bundle_id, imported_at = excluded.imported_at`,
		r.ID, r.ProjectID, r.Name, r.RemoteURL, r.DefaultBranch, r.Forge, r.ForgeRepo, ts(r.CreatedAt), nullStr(r.SourceBundleID), tsp(r.ImportedAt))
	return err
}

func GetRepo(ctx context.Context, q Q, id string) (protocol.Repo, error) {
	r, err := scanRepo(q.QueryRowContext(ctx, `SELECT `+repoCols+` FROM repos WHERE id = ?`, id))
	return r, notFound(err)
}

func ListRepos(ctx context.Context, q Q) ([]protocol.Repo, error) {
	return list(ctx, q, scanRepo, `SELECT `+repoCols+` FROM repos ORDER BY name`)
}

const grantCols = `id, project_id, engineer_id, access, actions`

func scanGrant(s scanner) (protocol.Grant, error) {
	var g protocol.Grant
	var actions string
	err := s.Scan(&g.ID, &g.ProjectID, &g.EngineerID, &g.Access, &actions)
	unjs(actions, &g.Actions)
	g.Actions = strs(g.Actions)
	return g, err
}

func PutGrant(ctx context.Context, q Q, g protocol.Grant) error {
	_, err := q.ExecContext(ctx, `INSERT INTO project_grants(id, project_id, engineer_id, access, actions, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, engineer_id) DO UPDATE SET access = excluded.access, actions = excluded.actions`,
		g.ID, g.ProjectID, g.EngineerID, g.Access, js(strs(g.Actions)), ts(nowUTC()))
	return err
}

func DeleteGrant(ctx context.Context, q Q, projectID, engineerID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM project_grants WHERE project_id = ? AND engineer_id = ?`, projectID, engineerID)
	return err
}

// GetGrant returns the engineer's grant on a project, or ErrNotFound.
func GetGrant(ctx context.Context, q Q, projectID, engineerID string) (protocol.Grant, error) {
	g, err := scanGrant(q.QueryRowContext(ctx, `SELECT `+grantCols+` FROM project_grants WHERE project_id = ? AND engineer_id = ?`, projectID, engineerID))
	return g, notFound(err)
}

// GrantsVersion summarises an engineer's grants for scope fingerprints.
func GrantsDigest(ctx context.Context, q Q, engineerID string) (string, error) {
	gs, err := list(ctx, q, scanGrant, `SELECT `+grantCols+` FROM project_grants WHERE engineer_id = ? ORDER BY project_id`, engineerID)
	if err != nil {
		return "", err
	}
	return js(gs), nil
}
