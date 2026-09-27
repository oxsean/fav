-- A user's own webhook for what needs them, and each notice delivered once per user.
ALTER TABLE users ADD COLUMN webhook TEXT NOT NULL DEFAULT '';

CREATE TABLE deliveries (
  seq INTEGER NOT NULL,
  user_id TEXT NOT NULL,
  event TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL,
  PRIMARY KEY (seq, user_id, event)
);
