package store

import (
	"context"

	"github.com/binbandit/yip/protocol"
)

const engineerCols = `e.id, e.org_id, e.handle, e.hue, e.archived, e.created_at, e.updated_at, e.version,
	v.id, v.version_no, v.name, v.role, v.description, v.instructions, v.capability_tags, v.provider`

const engineerFrom = ` FROM engineers e JOIN engineer_versions v ON v.id = e.current_version_id`

func scanEngineer(s scanner) (protocol.Engineer, error) {
	var e protocol.Engineer
	var archived int
	var created, updated, tags, prov string
	err := s.Scan(&e.ID, &e.OrgID, &e.Handle, &e.Hue, &archived, &created, &updated, &e.Version,
		&e.VersionID, &e.VersionNo, &e.Name, &e.Role, &e.Description, &e.Instructions, &tags, &prov)
	e.Archived = archived == 1
	e.CreatedAt, e.UpdatedAt = parseTS(created), parseTS(updated)
	unjs(tags, &e.CapabilityTags)
	unjs(prov, &e.Provider)
	e.CapabilityTags = strs(e.CapabilityTags)
	e.RoomIDs, e.ActiveJobIDs = []string{}, []string{}
	return e, err
}

// InsertEngineer creates the engineer identity and its first config version.
func InsertEngineer(ctx context.Context, q Q, e protocol.Engineer) error {
	if _, err := q.ExecContext(ctx, `INSERT INTO engineers(id, org_id, handle, current_version_id, hue, archived, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, 1)`, e.ID, e.OrgID, e.Handle, e.VersionID, e.Hue, ts(e.CreatedAt), ts(e.UpdatedAt)); err != nil {
		return err
	}
	return InsertEngineerVersion(ctx, q, protocol.EngineerVersion{
		ID: e.VersionID, EngineerID: e.ID, VersionNo: 1, Name: e.Name, Role: e.Role,
		Description: e.Description, Instructions: e.Instructions, CapabilityTags: e.CapabilityTags,
		Provider: e.Provider, CreatedAt: e.CreatedAt,
	})
}

func InsertEngineerVersion(ctx context.Context, q Q, v protocol.EngineerVersion) error {
	_, err := q.ExecContext(ctx, `INSERT INTO engineer_versions(id, engineer_id, version_no, name, role, description, instructions, capability_tags, provider, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, v.ID, v.EngineerID, v.VersionNo, v.Name, v.Role, v.Description,
		v.Instructions, js(strs(v.CapabilityTags)), js(v.Provider), ts(v.CreatedAt))
	return err
}

// SetEngineerVersion points the engineer at a new config version. Active runs
// keep the snapshot they started with.
func SetEngineerVersion(ctx context.Context, q Q, id, versionID, handle string, expectVersion int64) (bool, error) {
	return oneRow(q.ExecContext(ctx, `UPDATE engineers SET current_version_id = ?, handle = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`, versionID, handle, ts(nowUTC()), id, expectVersion))
}

func SetEngineerArchived(ctx context.Context, q Q, id string, archived bool, expectVersion int64) (bool, error) {
	return oneRow(q.ExecContext(ctx, `UPDATE engineers SET archived = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`,
		b2i(archived), ts(nowUTC()), id, expectVersion))
}

func GetEngineer(ctx context.Context, q Q, id string) (protocol.Engineer, error) {
	return getFilled(ctx, q, scanEngineer, fillEngineer, `SELECT `+engineerCols+engineerFrom+` WHERE e.id = ?`, id)
}

func GetEngineerByHandle(ctx context.Context, q Q, handle string) (protocol.Engineer, error) {
	return getFilled(ctx, q, scanEngineer, fillEngineer, `SELECT `+engineerCols+engineerFrom+` WHERE e.handle = ?`, handle)
}

func ListEngineers(ctx context.Context, q Q) ([]protocol.Engineer, error) {
	return listFilled(ctx, q, scanEngineer, fillEngineer, `SELECT `+engineerCols+engineerFrom+` ORDER BY v.name`)
}

func fillEngineer(ctx context.Context, q Q, e *protocol.Engineer) error {
	var err error
	if e.RoomIDs, err = stringsCol(ctx, q, `SELECT m.room_id FROM room_memberships m JOIN rooms r ON r.id = m.room_id
		WHERE m.member_kind = 'engineer' AND m.member_id = ? AND r.archived = 0 ORDER BY r.name`, e.ID); err != nil {
		return err
	}
	e.ActiveJobIDs, err = stringsCol(ctx, q, `SELECT id FROM jobs WHERE owner_id = ? AND kind <> 'reply'
		AND state IN ('queued','running','waiting','review_ready') ORDER BY created_at`, e.ID)
	return err
}

func GetEngineerVersion(ctx context.Context, q Q, id string) (protocol.EngineerVersion, error) {
	return scanVersion(q.QueryRowContext(ctx, `SELECT id, engineer_id, version_no, name, role, description, instructions, capability_tags, provider, created_at
		FROM engineer_versions WHERE id = ?`, id))
}

func ListEngineerVersions(ctx context.Context, q Q, engineerID string) ([]protocol.EngineerVersion, error) {
	return list(ctx, q, scanVersion, `SELECT id, engineer_id, version_no, name, role, description, instructions, capability_tags, provider, created_at
		FROM engineer_versions WHERE engineer_id = ? ORDER BY version_no DESC`, engineerID)
}

func scanVersion(s scanner) (protocol.EngineerVersion, error) {
	var v protocol.EngineerVersion
	var tags, prov, created string
	err := s.Scan(&v.ID, &v.EngineerID, &v.VersionNo, &v.Name, &v.Role, &v.Description, &v.Instructions, &tags, &prov, &created)
	unjs(tags, &v.CapabilityTags)
	unjs(prov, &v.Provider)
	v.CapabilityTags = strs(v.CapabilityTags)
	v.CreatedAt = parseTS(created)
	return v, notFound(err)
}
