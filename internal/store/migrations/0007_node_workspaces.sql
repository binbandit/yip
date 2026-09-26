-- The workspaces each runner last reported (per-job worktrees and review
-- snapshots), so the owner can see and clean them up from Machines.
ALTER TABLE nodes ADD COLUMN workspaces TEXT NOT NULL DEFAULT '[]';
