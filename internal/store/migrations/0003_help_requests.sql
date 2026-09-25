-- Help requests are marked explicitly: only these may be answered with
-- work_respond, and only by the helper's own run.
ALTER TABLE jobs ADD COLUMN help_request INTEGER NOT NULL DEFAULT 0;
