CREATE TABLE engineer_drafts (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  node_id TEXT NOT NULL REFERENCES nodes(id),
  provider TEXT NOT NULL,
  profile_id TEXT NOT NULL,
  request TEXT NOT NULL,
  state TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  fields TEXT NOT NULL DEFAULT 'null',
  lease_expires_at TEXT NOT NULL,
  deadline_at TEXT NOT NULL,
  cancel_requested INTEGER NOT NULL DEFAULT 0,
  last_seq INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX engineer_drafts_state ON engineer_drafts(state);
CREATE UNIQUE INDEX engineer_drafts_active_user ON engineer_drafts(user_id)
  WHERE state IN ('offered', 'preparing', 'running', 'stopping');
