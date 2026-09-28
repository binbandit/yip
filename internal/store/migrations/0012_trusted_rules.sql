-- Providers whose own always-allow rules the owner lets apply to reviews and
-- conversations on a machine (a JSON array of provider names).
ALTER TABLE nodes ADD COLUMN trusted_rules TEXT NOT NULL DEFAULT '[]';
