-- Profile pictures: the owner's own and each engineer's. The image is an
-- artifact (kind 'avatar'), so backups and exports carry it like any other
-- file. Empty means the initial on its tint.
ALTER TABLE users ADD COLUMN avatar_id TEXT REFERENCES artifacts(id);
ALTER TABLE engineers ADD COLUMN avatar_id TEXT REFERENCES artifacts(id);
