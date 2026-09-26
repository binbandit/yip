-- Responses to state-changing requests sent with an Idempotency-Key, so a
-- retried or double-sent request replays its first result instead of acting
-- twice. Kept for a day.
CREATE TABLE idempotency_keys (
  user_id      TEXT NOT NULL,
  key          TEXT NOT NULL,
  method       TEXT NOT NULL,
  path         TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  state        TEXT NOT NULL,              -- pending | done
  status       INTEGER NOT NULL DEFAULT 0,
  body         BLOB,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (user_id, key)
);
CREATE INDEX idempotency_keys_created ON idempotency_keys(created_at);
