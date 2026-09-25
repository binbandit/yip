-- A wakeup that arrived while an attempt was still active (for example a
-- review verdict landing before the author's run finished). It is replayed
-- when the active attempt ends, so no trigger is lost.
ALTER TABLE jobs ADD COLUMN pending_wake TEXT;
