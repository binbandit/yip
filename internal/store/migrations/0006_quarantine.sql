-- Output that arrives from an attempt after it lost its lease (a stale
-- epoch) never changes the work; it is kept here as diagnostic evidence.
CREATE TABLE quarantined_output (
  id            TEXT PRIMARY KEY,
  run_id        TEXT NOT NULL REFERENCES runs(id),
  job_id        TEXT NOT NULL REFERENCES jobs(id),
  node_id       TEXT NOT NULL,
  epoch         INTEGER NOT NULL,
  current_epoch INTEGER NOT NULL,
  kind          TEXT NOT NULL,           -- event | terminal | tool_call
  dedupe_key    TEXT NOT NULL UNIQUE,
  summary       TEXT NOT NULL,
  received_at   TEXT NOT NULL
);
CREATE INDEX quarantined_output_job ON quarantined_output(job_id, received_at);
