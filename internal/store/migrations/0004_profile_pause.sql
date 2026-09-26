-- An account whose allowance ran out is paused as a whole until it resets,
-- so other queued work doesn't keep hitting the same limit.
ALTER TABLE provider_profiles ADD COLUMN paused_until TEXT;
