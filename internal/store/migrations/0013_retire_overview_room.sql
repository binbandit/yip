-- The per-owner Overview conversation is gone. Archive it rather than delete
-- it, so its messages stay in backups and exports like any archived room.
UPDATE rooms SET archived = 1 WHERE kind = 'overview';
