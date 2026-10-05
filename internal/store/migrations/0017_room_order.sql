-- Personal navigation order is independent from replaceable UI preferences.
CREATE TABLE user_room_orders (
  user_id  TEXT NOT NULL REFERENCES users(id),
  kind     TEXT NOT NULL CHECK (kind IN ('room', 'dm')),
  version  INTEGER NOT NULL CHECK (version > 0),
  room_ids TEXT NOT NULL CHECK (json_valid(room_ids) AND json_type(room_ids) = 'array'),
  PRIMARY KEY (user_id, kind)
);
