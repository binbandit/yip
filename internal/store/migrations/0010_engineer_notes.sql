-- An engineer's short notes from earlier work (spec §8, context layer 5):
-- scoped to a project or room, visible only where their sources are,
-- proposed until accepted (or covered by the narrow auto-accept policy),
-- due for review after a while, and corrected by supersession.
CREATE TABLE engineer_notes (
  id               TEXT PRIMARY KEY,
  engineer_id      TEXT NOT NULL REFERENCES engineers(id),
  scope_kind       TEXT NOT NULL,          -- project | room
  scope_id         TEXT NOT NULL,
  body             TEXT NOT NULL,
  status           TEXT NOT NULL,          -- proposed | accepted | superseded | rejected
  supersedes_id    TEXT,
  superseded_by_id TEXT,
  created_by_kind  TEXT NOT NULL,
  created_by_id    TEXT NOT NULL,
  accepted_by_kind TEXT,
  accepted_by_id   TEXT,
  visible_room_ids TEXT,                   -- NULL = visible wherever the scope is
  review_after     TEXT NOT NULL,
  version          INTEGER NOT NULL DEFAULT 1,
  created_at       TEXT NOT NULL,
  accepted_at      TEXT
);
CREATE INDEX engineer_notes_engineer ON engineer_notes(engineer_id, status);
CREATE TABLE engineer_note_sources (
  note_id     TEXT NOT NULL REFERENCES engineer_notes(id),
  source_kind TEXT NOT NULL,
  source_id   TEXT NOT NULL,
  room_id     TEXT,
  PRIMARY KEY (note_id, source_kind, source_id)
);
