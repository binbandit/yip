-- A repository can be imported from a git bundle instead of a remote (a
-- folder on the owner's laptop with no reachable remote). Runners build
-- their replica from the bundle; a newer import replaces it.
ALTER TABLE repos ADD COLUMN source_bundle_id TEXT REFERENCES artifacts(id);
ALTER TABLE repos ADD COLUMN imported_at TEXT;
