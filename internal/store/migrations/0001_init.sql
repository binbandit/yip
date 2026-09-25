-- yip hub schema v1.
-- Relational rows are current state; the events table is an append-oriented
-- stream for replay, debugging, and audit. Timestamps are UTC RFC 3339 text
-- with millisecond precision. JSON columns hold vendor payloads and versioned
-- manifests, never replacements for important relational constraints.

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE orgs (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  org_id        TEXT NOT NULL REFERENCES orgs(id),
  name          TEXT NOT NULL,
  handle        TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  preferences   TEXT NOT NULL DEFAULT '{}',
  created_at    TEXT NOT NULL,
  version       INTEGER NOT NULL DEFAULT 1,
  UNIQUE (org_id, handle)
);

CREATE TABLE sessions (
  id           TEXT PRIMARY KEY,           -- SHA-256 of the cookie token
  user_id      TEXT NOT NULL REFERENCES users(id),
  csrf_token   TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  revoked_at   TEXT,
  user_agent   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE bootstrap_secrets (
  hash       TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT
);

CREATE TABLE engineers (
  id                 TEXT PRIMARY KEY,
  org_id             TEXT NOT NULL REFERENCES orgs(id),
  handle             TEXT NOT NULL,
  current_version_id TEXT,
  hue                INTEGER NOT NULL DEFAULT 200,
  archived           INTEGER NOT NULL DEFAULT 0,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL,
  version            INTEGER NOT NULL DEFAULT 1,
  UNIQUE (org_id, handle)
);

-- Immutable configuration snapshots. Every run records the version it used.
CREATE TABLE engineer_versions (
  id              TEXT PRIMARY KEY,
  engineer_id     TEXT NOT NULL REFERENCES engineers(id),
  version_no      INTEGER NOT NULL,
  name            TEXT NOT NULL,
  role            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  instructions    TEXT NOT NULL DEFAULT '',
  capability_tags TEXT NOT NULL DEFAULT '[]',
  provider        TEXT NOT NULL DEFAULT '{}',
  created_at      TEXT NOT NULL,
  UNIQUE (engineer_id, version_no)
);

CREATE TABLE rooms (
  id              TEXT PRIMARY KEY,
  org_id          TEXT NOT NULL REFERENCES orgs(id),
  name            TEXT NOT NULL,
  kind            TEXT NOT NULL,           -- room | dm | overview
  purpose         TEXT NOT NULL DEFAULT '',
  private         INTEGER NOT NULL DEFAULT 0,
  reply_mode      TEXT NOT NULL DEFAULT 'quiet',
  steward_id      TEXT REFERENCES engineers(id),
  archived        INTEGER NOT NULL DEFAULT 0,
  last_seq        INTEGER NOT NULL DEFAULT 0,
  members_version INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT NOT NULL,
  version         INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE room_memberships (
  room_id     TEXT NOT NULL REFERENCES rooms(id),
  member_kind TEXT NOT NULL,               -- user | engineer
  member_id   TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (room_id, member_kind, member_id)
);
CREATE INDEX room_memberships_member ON room_memberships(member_kind, member_id);

CREATE TABLE room_reads (
  room_id       TEXT NOT NULL REFERENCES rooms(id),
  user_id       TEXT NOT NULL REFERENCES users(id),
  last_read_seq INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (room_id, user_id)
);

CREATE TABLE projects (
  id           TEXT PRIMARY KEY,
  org_id       TEXT NOT NULL REFERENCES orgs(id),
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  instructions TEXT NOT NULL DEFAULT '',
  policy       TEXT NOT NULL DEFAULT '{}',
  created_at   TEXT NOT NULL,
  version      INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE repos (
  id             TEXT PRIMARY KEY,
  project_id     TEXT NOT NULL REFERENCES projects(id),
  name           TEXT NOT NULL,
  remote_url     TEXT NOT NULL,
  default_branch TEXT NOT NULL DEFAULT 'main',
  forge          TEXT NOT NULL DEFAULT 'none',
  forge_repo     TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL
);

CREATE TABLE project_grants (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id),
  engineer_id TEXT NOT NULL REFERENCES engineers(id),
  access      TEXT NOT NULL,               -- read | write
  actions     TEXT NOT NULL DEFAULT '[]',  -- push | open_pr | publish_review | merge
  created_at  TEXT NOT NULL,
  UNIQUE (project_id, engineer_id)
);

CREATE TABLE room_projects (
  room_id    TEXT NOT NULL REFERENCES rooms(id),
  project_id TEXT NOT NULL REFERENCES projects(id),
  PRIMARY KEY (room_id, project_id)
);

CREATE TABLE messages (
  id          TEXT PRIMARY KEY,
  org_id      TEXT NOT NULL REFERENCES orgs(id),
  room_id     TEXT NOT NULL REFERENCES rooms(id),
  thread_id   TEXT,                        -- root message ID; NULL for top level
  seq         INTEGER NOT NULL,
  author_kind TEXT NOT NULL,
  author_id   TEXT NOT NULL,
  body        TEXT NOT NULL,
  kind        TEXT NOT NULL DEFAULT 'text',
  refs        TEXT NOT NULL DEFAULT '[]',
  project_ids TEXT NOT NULL DEFAULT '[]',
  reply_to_id TEXT,
  run_id      TEXT,
  job_id      TEXT,
  client_key  TEXT,
  revision    INTEGER NOT NULL DEFAULT 1,
  edited_at   TEXT,
  deleted_at  TEXT,
  created_at  TEXT NOT NULL,
  UNIQUE (room_id, seq)
);
CREATE UNIQUE INDEX messages_client_key ON messages(org_id, client_key) WHERE client_key IS NOT NULL;
CREATE INDEX messages_thread ON messages(thread_id, seq);
CREATE INDEX messages_job ON messages(job_id);

CREATE TABLE message_revisions (
  id         TEXT PRIMARY KEY,
  message_id TEXT NOT NULL REFERENCES messages(id),
  revision   INTEGER NOT NULL,
  body       TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE mentions (
  message_id  TEXT NOT NULL REFERENCES messages(id),
  member_kind TEXT NOT NULL,
  member_id   TEXT NOT NULL,
  PRIMARY KEY (message_id, member_kind, member_id)
);
CREATE INDEX mentions_member ON mentions(member_kind, member_id);

CREATE TABLE reactions (
  message_id TEXT NOT NULL REFERENCES messages(id),
  actor_kind TEXT NOT NULL,
  actor_id   TEXT NOT NULL,
  emoji      TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (message_id, actor_kind, actor_id, emoji)
);

CREATE TABLE threads (
  id            TEXT PRIMARY KEY,          -- equals the root message ID
  room_id       TEXT NOT NULL REFERENCES rooms(id),
  reply_count   INTEGER NOT NULL DEFAULT 0,
  last_reply_at TEXT,
  owner_id      TEXT,                      -- engineer who owns the thread's work
  created_at    TEXT NOT NULL
);

CREATE VIRTUAL TABLE messages_fts USING fts5(
  body, message_id UNINDEXED, room_id UNINDEXED, tokenize = 'porter unicode61'
);

CREATE TABLE jobs (
  id                    TEXT PRIMARY KEY,
  org_id                TEXT NOT NULL REFERENCES orgs(id),
  kind                  TEXT NOT NULL,
  title                 TEXT NOT NULL,
  objective             TEXT NOT NULL,
  acceptance            TEXT NOT NULL DEFAULT '[]',
  state                 TEXT NOT NULL,
  waiting_reason        TEXT NOT NULL DEFAULT '',
  state_detail          TEXT NOT NULL DEFAULT '',
  owner_id              TEXT NOT NULL REFERENCES engineers(id),
  parent_id             TEXT REFERENCES jobs(id),
  root_request_id       TEXT NOT NULL,
  source_room_id        TEXT NOT NULL REFERENCES rooms(id),
  source_thread_id      TEXT,
  source_message_id     TEXT,
  project_id            TEXT REFERENCES projects(id),
  repo_id               TEXT REFERENCES repos(id),
  branch                TEXT NOT NULL DEFAULT '',
  base_rev              TEXT NOT NULL DEFAULT '',
  head_rev              TEXT NOT NULL DEFAULT '',
  diff_artifact_id      TEXT,
  requires_peer_review  INTEGER NOT NULL DEFAULT 0,
  requires_human_review INTEGER NOT NULL DEFAULT 0,
  completion_requested  INTEGER NOT NULL DEFAULT 0,
  summary               TEXT NOT NULL DEFAULT '',
  depth                 INTEGER NOT NULL DEFAULT 0,
  priority              INTEGER NOT NULL DEFAULT 0,
  current_run_id        TEXT,
  retry_at              TEXT,
  auto_retries          INTEGER NOT NULL DEFAULT 0,
  policy_version        INTEGER NOT NULL DEFAULT 1,
  last_activity         TEXT NOT NULL DEFAULT '',
  last_activity_at      TEXT,
  created_at            TEXT NOT NULL,
  updated_at            TEXT NOT NULL,
  completed_at          TEXT,
  version               INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX jobs_state ON jobs(state);
CREATE INDEX jobs_owner ON jobs(owner_id, state);
CREATE INDEX jobs_parent ON jobs(parent_id);
CREATE INDEX jobs_source ON jobs(source_room_id);

CREATE VIRTUAL TABLE jobs_fts USING fts5(title, objective, job_id UNINDEXED);

CREATE TABLE job_dependencies (
  job_id          TEXT NOT NULL REFERENCES jobs(id),
  depends_on_kind TEXT NOT NULL,           -- job | question | review | approval
  depends_on_id   TEXT NOT NULL,
  created_at      TEXT NOT NULL,
  resolved_at     TEXT,
  PRIMARY KEY (job_id, depends_on_kind, depends_on_id)
);

CREATE TABLE job_assignments (
  job_id      TEXT NOT NULL REFERENCES jobs(id),
  engineer_id TEXT NOT NULL REFERENCES engineers(id),
  role        TEXT NOT NULL,               -- owner | contributor | reviewer
  created_at  TEXT NOT NULL,
  PRIMARY KEY (job_id, engineer_id, role)
);

CREATE TABLE job_inputs (
  id           TEXT PRIMARY KEY,
  job_id       TEXT NOT NULL REFERENCES jobs(id),
  run_id       TEXT,
  body         TEXT NOT NULL,
  delivery     TEXT NOT NULL,              -- pending | immediate | queued
  message_id   TEXT,
  client_key   TEXT UNIQUE,
  created_at   TEXT NOT NULL,
  delivered_at TEXT,
  consumed_at  TEXT
);
CREATE INDEX job_inputs_job ON job_inputs(job_id);

-- Bounded causal chains: every automatic wakeup counts against its root request.
CREATE TABLE root_budgets (
  root_request_id TEXT PRIMARY KEY,
  wakeups         INTEGER NOT NULL DEFAULT 0,
  max_wakeups     INTEGER NOT NULL,
  exhausted_at    TEXT,
  created_at      TEXT NOT NULL
);

CREATE TABLE wakeups (
  id              TEXT PRIMARY KEY,
  root_request_id TEXT NOT NULL,
  cause_id        TEXT NOT NULL,
  engineer_id     TEXT NOT NULL,
  purpose         TEXT NOT NULL,
  delivery_key    TEXT NOT NULL UNIQUE,
  job_id          TEXT,
  automatic       INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT NOT NULL
);

CREATE TABLE runs (
  id                  TEXT PRIMARY KEY,
  job_id              TEXT NOT NULL REFERENCES jobs(id),
  attempt             INTEGER NOT NULL,
  engineer_id         TEXT NOT NULL REFERENCES engineers(id),
  engineer_version_id TEXT NOT NULL REFERENCES engineer_versions(id),
  node_id             TEXT,
  provider            TEXT NOT NULL,
  model               TEXT NOT NULL DEFAULT '',
  profile_id          TEXT NOT NULL DEFAULT '',
  mode                TEXT NOT NULL,
  execution_profile   TEXT NOT NULL DEFAULT 'native',
  state               TEXT NOT NULL,
  lease_epoch         INTEGER NOT NULL DEFAULT 0,
  lease_expires_at    TEXT,
  heartbeat_at        TEXT,
  vendor_session_id   TEXT NOT NULL DEFAULT '',
  dest_room_id        TEXT NOT NULL,
  dest_thread_id      TEXT,
  dest_message_id     TEXT,
  cause_id            TEXT NOT NULL,
  root_request_id     TEXT NOT NULL,
  previous_run_id     TEXT,
  branch              TEXT NOT NULL DEFAULT '',
  base_rev            TEXT NOT NULL DEFAULT '',
  result_rev          TEXT NOT NULL DEFAULT '',
  terminal_reason     TEXT NOT NULL DEFAULT '',
  final_text          TEXT NOT NULL DEFAULT '',
  last_activity       TEXT NOT NULL DEFAULT '',
  last_activity_at    TEXT,
  manifest            TEXT NOT NULL DEFAULT '{}',
  scope_fingerprint   TEXT NOT NULL DEFAULT '',
  usage               TEXT,
  last_seq            INTEGER NOT NULL DEFAULT 0,
  offer_command_id    TEXT,
  posted_reply        INTEGER NOT NULL DEFAULT 0,
  retry_at            TEXT,
  unconfirmed_since   TEXT,
  created_at          TEXT NOT NULL,
  started_at          TEXT,
  ended_at            TEXT,
  updated_at          TEXT NOT NULL,
  UNIQUE (job_id, attempt)
);
CREATE INDEX runs_state ON runs(state);
CREATE INDEX runs_node ON runs(node_id, state);
CREATE INDEX runs_engineer ON runs(engineer_id, state);

CREATE TABLE run_events (
  run_id TEXT NOT NULL REFERENCES runs(id),
  seq    INTEGER NOT NULL,
  kind   TEXT NOT NULL,
  text   TEXT NOT NULL DEFAULT '',
  tool   TEXT NOT NULL DEFAULT '',
  data   TEXT,
  at     TEXT NOT NULL,
  PRIMARY KEY (run_id, seq)
);

CREATE TABLE provider_sessions (
  id                TEXT PRIMARY KEY,
  engineer_id       TEXT NOT NULL REFERENCES engineers(id),
  provider          TEXT NOT NULL,
  profile_id        TEXT NOT NULL DEFAULT '',
  scope_fingerprint TEXT NOT NULL,
  context_key       TEXT NOT NULL,         -- thread or job the session belongs to
  node_id           TEXT NOT NULL,
  workspace         TEXT NOT NULL DEFAULT '',
  vendor_session_id TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  last_used_at      TEXT NOT NULL,
  invalidated_at    TEXT,
  invalidated_why   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX provider_sessions_key ON provider_sessions(engineer_id, provider, context_key);

CREATE TABLE checks (
  id              TEXT PRIMARY KEY,
  job_id          TEXT NOT NULL REFERENCES jobs(id),
  run_id          TEXT NOT NULL REFERENCES runs(id),
  node_id         TEXT NOT NULL DEFAULT '',
  name            TEXT NOT NULL,
  command         TEXT NOT NULL,
  exit_code       INTEGER NOT NULL,
  passed          INTEGER NOT NULL,
  revision        TEXT NOT NULL DEFAULT '',
  log_artifact_id TEXT,
  duration_ms     INTEGER NOT NULL DEFAULT 0,
  summary         TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL
);
CREATE INDEX checks_job ON checks(job_id, revision);

CREATE TABLE reviews (
  id                TEXT PRIMARY KEY,
  org_id            TEXT NOT NULL REFERENCES orgs(id),
  job_id            TEXT NOT NULL REFERENCES jobs(id),
  author_id         TEXT NOT NULL REFERENCES engineers(id),
  reviewer_id       TEXT NOT NULL REFERENCES engineers(id),
  source_room_id    TEXT NOT NULL,
  source_thread_id  TEXT,
  source_message_id TEXT,
  target_kind       TEXT NOT NULL,
  criteria          TEXT NOT NULL DEFAULT '',
  state             TEXT NOT NULL,
  current_round     INTEGER NOT NULL DEFAULT 1,
  pull_request_id   TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  CHECK (author_id <> reviewer_id)
);
CREATE UNIQUE INDEX reviews_job_reviewer ON reviews(job_id, reviewer_id);

CREATE TABLE review_rounds (
  id              TEXT PRIMARY KEY,
  review_id       TEXT NOT NULL REFERENCES reviews(id),
  number          INTEGER NOT NULL,
  target_kind     TEXT NOT NULL,
  repo_id         TEXT,
  base_rev        TEXT NOT NULL DEFAULT '',
  head_rev        TEXT NOT NULL DEFAULT '',
  artifact_id     TEXT,
  target_hash     TEXT NOT NULL DEFAULT '',
  pull_request_id TEXT,
  state           TEXT NOT NULL,
  summary         TEXT NOT NULL DEFAULT '',
  reviewer_run_id TEXT,
  review_job_id   TEXT,
  superseded_by   TEXT,
  cause_key       TEXT NOT NULL UNIQUE,
  created_at      TEXT NOT NULL,
  decided_at      TEXT,
  UNIQUE (review_id, number)
);

CREATE TABLE review_findings (
  id                TEXT PRIMARY KEY,
  round_id          TEXT NOT NULL REFERENCES review_rounds(id),
  review_id         TEXT NOT NULL REFERENCES reviews(id),
  severity          TEXT NOT NULL,         -- blocking | suggestion | note
  body              TEXT NOT NULL,
  file              TEXT NOT NULL DEFAULT '',
  line              INTEGER NOT NULL DEFAULT 0,
  evidence          TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL,         -- open | addressed | resolved | disputed | withdrawn
  resolved_in_round INTEGER,
  created_at        TEXT NOT NULL
);
CREATE INDEX review_findings_review ON review_findings(review_id);

CREATE TABLE finding_replies (
  id          TEXT PRIMARY KEY,
  finding_id  TEXT NOT NULL REFERENCES review_findings(id),
  author_kind TEXT NOT NULL,
  author_id   TEXT NOT NULL,
  body        TEXT NOT NULL,
  evidence    TEXT NOT NULL DEFAULT '',
  revision    TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);

CREATE TABLE pull_requests (
  id             TEXT PRIMARY KEY,
  org_id         TEXT NOT NULL REFERENCES orgs(id),
  repo_id        TEXT NOT NULL REFERENCES repos(id),
  job_id         TEXT,
  forge          TEXT NOT NULL,
  host           TEXT NOT NULL,
  owner          TEXT NOT NULL,
  name           TEXT NOT NULL,
  number         INTEGER NOT NULL,
  url            TEXT NOT NULL,
  title          TEXT NOT NULL DEFAULT '',
  state          TEXT NOT NULL,
  base_rev       TEXT NOT NULL DEFAULT '',
  head_rev       TEXT NOT NULL DEFAULT '',
  remote_author  TEXT NOT NULL DEFAULT '',
  viewer_actor   TEXT NOT NULL DEFAULT '',
  checks         TEXT NOT NULL DEFAULT '{}',
  merge          TEXT NOT NULL DEFAULT '{}',
  remote_reviews TEXT NOT NULL DEFAULT '[]',
  last_synced_at TEXT,
  created_at     TEXT NOT NULL,
  UNIQUE (forge, host, owner, name, number)
);

CREATE TABLE forge_deliveries (
  id              TEXT PRIMARY KEY,
  pr_id           TEXT,
  kind            TEXT NOT NULL,           -- publish_review | webhook
  dedupe_key      TEXT NOT NULL UNIQUE,
  external_id     TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL,           -- pending | published | unknown | failed | received
  engineer_id     TEXT,
  review_round_id TEXT,
  payload         TEXT NOT NULL DEFAULT '{}',
  error           TEXT NOT NULL DEFAULT '',
  attempted_at    TEXT NOT NULL,
  completed_at    TEXT
);

CREATE TABLE approvals (
  id               TEXT PRIMARY KEY,
  org_id           TEXT NOT NULL REFERENCES orgs(id),
  run_id           TEXT NOT NULL REFERENCES runs(id),
  job_id           TEXT NOT NULL REFERENCES jobs(id),
  engineer_id      TEXT NOT NULL,
  request_id       TEXT NOT NULL,
  action           TEXT NOT NULL,
  args_digest      TEXT NOT NULL,
  scope            TEXT NOT NULL,
  target_rev       TEXT NOT NULL DEFAULT '',
  status           TEXT NOT NULL,
  expires_at       TEXT NOT NULL,
  decided_by_kind  TEXT,
  decided_by_id    TEXT,
  decided_at       TEXT,
  consumed_at      TEXT,
  source_room_id   TEXT NOT NULL,
  source_thread_id TEXT,
  message_id       TEXT,
  version          INTEGER NOT NULL DEFAULT 1,
  created_at       TEXT NOT NULL,
  UNIQUE (run_id, request_id)
);
CREATE INDEX approvals_status ON approvals(status);

CREATE TABLE questions (
  id                TEXT PRIMARY KEY,
  org_id            TEXT NOT NULL REFERENCES orgs(id),
  job_id            TEXT NOT NULL REFERENCES jobs(id),
  run_id            TEXT,
  asker_id          TEXT NOT NULL REFERENCES engineers(id),
  recipient_kind    TEXT NOT NULL,
  recipient_id      TEXT NOT NULL,
  missing_fact      TEXT NOT NULL,
  context_checked   TEXT NOT NULL DEFAULT '',
  dependent_step    TEXT NOT NULL DEFAULT '',
  continuing_with   TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL,         -- open | answered | cancelled
  source_room_id    TEXT NOT NULL,
  source_thread_id  TEXT,
  message_id        TEXT NOT NULL,
  answer_message_id TEXT,
  created_at        TEXT NOT NULL,
  answered_at       TEXT
);
CREATE INDEX questions_open ON questions(source_room_id, status);

CREATE TABLE nodes (
  id             TEXT PRIMARY KEY,
  org_id         TEXT NOT NULL REFERENCES orgs(id),
  name           TEXT NOT NULL,
  hostname       TEXT NOT NULL DEFAULT '',
  os             TEXT NOT NULL DEFAULT '',
  arch           TEXT NOT NULL DEFAULT '',
  fingerprint    TEXT NOT NULL,
  cert_serial    TEXT NOT NULL,
  status         TEXT NOT NULL DEFAULT 'offline',
  draining       INTEGER NOT NULL DEFAULT 0,
  revoked_at     TEXT,
  last_seen_at   TEXT,
  capacity       TEXT NOT NULL DEFAULT '{}',
  profiles       TEXT NOT NULL DEFAULT '[]',
  toolchains     TEXT NOT NULL DEFAULT '{}',
  runner_version TEXT NOT NULL DEFAULT '',
  service_state  TEXT NOT NULL DEFAULT '',
  last_activity  TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL
);

CREATE TABLE node_enrollments (
  id         TEXT PRIMARY KEY,
  org_id     TEXT NOT NULL REFERENCES orgs(id),
  name       TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT,
  node_id    TEXT
);

CREATE TABLE provider_installations (
  node_id        TEXT NOT NULL REFERENCES nodes(id),
  provider       TEXT NOT NULL,
  version        TEXT NOT NULL DEFAULT '',
  path           TEXT NOT NULL DEFAULT '',
  auth_state     TEXT NOT NULL,
  auth_detail    TEXT NOT NULL DEFAULT '',
  account        TEXT NOT NULL DEFAULT '',
  billing        TEXT NOT NULL DEFAULT 'unknown',
  profile_id     TEXT NOT NULL DEFAULT '',
  capabilities   TEXT NOT NULL DEFAULT '{}',
  models         TEXT NOT NULL DEFAULT '[]',
  tested         INTEGER NOT NULL DEFAULT 0,
  tested_version TEXT NOT NULL DEFAULT '',
  limitations    TEXT NOT NULL DEFAULT '[]',
  updated_at     TEXT NOT NULL,
  PRIMARY KEY (node_id, provider)
);

-- Account pools: installations sharing one vendor account share its allowance.
CREATE TABLE provider_profiles (
  id              TEXT PRIMARY KEY,
  org_id          TEXT NOT NULL REFERENCES orgs(id),
  provider        TEXT NOT NULL,
  label           TEXT NOT NULL,
  billing         TEXT NOT NULL DEFAULT 'unknown',
  max_concurrency INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT NOT NULL
);

CREATE TABLE artifacts (
  id           TEXT PRIMARY KEY,
  org_id       TEXT NOT NULL REFERENCES orgs(id),
  hash         TEXT NOT NULL,
  name         TEXT NOT NULL,
  content_type TEXT NOT NULL,
  size         INTEGER NOT NULL,
  kind         TEXT NOT NULL,
  run_id       TEXT,
  job_id       TEXT,
  room_id      TEXT,
  project_id   TEXT,
  revision     TEXT NOT NULL DEFAULT '',
  verified     INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL
);
CREATE INDEX artifacts_hash ON artifacts(hash);
CREATE INDEX artifacts_job ON artifacts(job_id);

CREATE TABLE artifact_links (
  artifact_id TEXT NOT NULL REFERENCES artifacts(id),
  target_kind TEXT NOT NULL,
  target_id   TEXT NOT NULL,
  PRIMARY KEY (artifact_id, target_kind, target_id)
);

CREATE TABLE checkpoints (
  id          TEXT PRIMARY KEY,
  run_id      TEXT NOT NULL REFERENCES runs(id),
  job_id      TEXT NOT NULL REFERENCES jobs(id),
  node_id     TEXT NOT NULL,
  head        TEXT NOT NULL,
  artifact_id TEXT,
  dirty       INTEGER NOT NULL DEFAULT 0,
  untracked   INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL
);

CREATE TABLE decisions (
  id               TEXT PRIMARY KEY,
  org_id           TEXT NOT NULL REFERENCES orgs(id),
  scope_kind       TEXT NOT NULL,
  scope_id         TEXT NOT NULL,
  title            TEXT NOT NULL,
  body             TEXT NOT NULL,
  status           TEXT NOT NULL,
  supersedes_id    TEXT,
  superseded_by_id TEXT,
  created_by_kind  TEXT NOT NULL,
  created_by_id    TEXT NOT NULL,
  accepted_by_kind TEXT,
  accepted_by_id   TEXT,
  visible_room_ids TEXT,                   -- NULL = visible wherever the scope is
  version          INTEGER NOT NULL DEFAULT 1,
  created_at       TEXT NOT NULL,
  accepted_at      TEXT
);
CREATE INDEX decisions_scope ON decisions(scope_kind, scope_id, status);

CREATE TABLE decision_sources (
  decision_id TEXT NOT NULL REFERENCES decisions(id),
  source_kind TEXT NOT NULL,
  source_id   TEXT NOT NULL,
  room_id     TEXT,
  PRIMARY KEY (decision_id, source_kind, source_id)
);

CREATE VIRTUAL TABLE decisions_fts USING fts5(title, body, decision_id UNINDEXED);

CREATE TABLE events (
  seq             INTEGER PRIMARY KEY AUTOINCREMENT,
  id              TEXT NOT NULL UNIQUE,
  org_id          TEXT NOT NULL,
  type            TEXT NOT NULL,
  actor_kind      TEXT NOT NULL,
  actor_id        TEXT NOT NULL,
  cause_id        TEXT,
  root_request_id TEXT,
  room_id         TEXT,
  thread_id       TEXT,
  job_id          TEXT,
  run_id          TEXT,
  visibility      TEXT NOT NULL,           -- room:<id> | owner | org
  payload         TEXT NOT NULL DEFAULT '{}',
  producer_key    TEXT UNIQUE,
  occurred_at     TEXT NOT NULL
);
CREATE INDEX events_job ON events(job_id);
CREATE INDEX events_room ON events(room_id, seq);

CREATE TABLE outbox (
  id              TEXT PRIMARY KEY,
  kind            TEXT NOT NULL,           -- runner_command | internal
  node_id         TEXT,
  run_id          TEXT,
  command_id      TEXT NOT NULL UNIQUE,
  frame           TEXT NOT NULL,
  status          TEXT NOT NULL,           -- pending | sent | acked | dead | cancelled
  attempts        INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL,
  created_at      TEXT NOT NULL,
  sent_at         TEXT,
  acked_at        TEXT
);
CREATE INDEX outbox_pending ON outbox(status, node_id, next_attempt_at);

CREATE TABLE audit_entries (
  id         TEXT PRIMARY KEY,
  org_id     TEXT NOT NULL,
  actor_kind TEXT NOT NULL,
  actor_id   TEXT NOT NULL,
  authority  TEXT NOT NULL,
  action     TEXT NOT NULL,
  target     TEXT NOT NULL,
  result     TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE usage_samples (
  id            TEXT PRIMARY KEY,
  run_id        TEXT NOT NULL,
  provider      TEXT NOT NULL,
  source        TEXT NOT NULL,             -- vendor | unknown
  input_tokens  INTEGER,
  output_tokens INTEGER,
  cost_usd      REAL,
  billing       TEXT NOT NULL,
  created_at    TEXT NOT NULL
);

-- Forge and other third-party credentials, encrypted with the hub key.
CREATE TABLE credentials (
  id         TEXT PRIMARY KEY,
  org_id     TEXT NOT NULL REFERENCES orgs(id),
  kind       TEXT NOT NULL,                -- github
  label      TEXT NOT NULL,
  host       TEXT NOT NULL DEFAULT '',
  secret     BLOB NOT NULL,
  created_at TEXT NOT NULL
);

-- Agent tool calls are idempotent per (run, call): a retried call after a
-- lost reply returns the recorded result instead of acting twice.
CREATE TABLE tool_calls (
  run_id     TEXT NOT NULL REFERENCES runs(id),
  call_id    TEXT NOT NULL,
  tool       TEXT NOT NULL,
  ok         INTEGER NOT NULL,
  result     TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (run_id, call_id)
);

-- Per-run intent recorded by work_wait: when the attempt ends, the job waits
-- on this reason instead of being treated as stalled.
CREATE TABLE run_intents (
  run_id     TEXT PRIMARY KEY REFERENCES runs(id),
  wait       TEXT NOT NULL DEFAULT '',
  detail     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
