-- A follow-up links to the work it follows (spec §8: "a follow-up links to
-- the original job"), separately from parent/child delegation.
ALTER TABLE jobs ADD COLUMN follows_job_id TEXT REFERENCES jobs(id);
CREATE INDEX jobs_follows ON jobs(follows_job_id) WHERE follows_job_id IS NOT NULL;
