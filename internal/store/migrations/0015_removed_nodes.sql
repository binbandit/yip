-- Keep historical references and revoked credentials; removal only hides the machine.
ALTER TABLE nodes ADD COLUMN removed_at TEXT CHECK (removed_at IS NULL OR revoked_at IS NOT NULL);
