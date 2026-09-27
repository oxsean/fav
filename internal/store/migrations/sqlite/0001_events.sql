-- Times are Unix nanoseconds; JSON is TEXT.
CREATE TABLE envelopes (
  seq INTEGER PRIMARY KEY,
  v INTEGER NOT NULL,
  at INTEGER NOT NULL,
  actor_kind TEXT,
  actor_id TEXT NOT NULL DEFAULT ''
);

CREATE TABLE events (
  seq INTEGER NOT NULL REFERENCES envelopes (seq),
  idx INTEGER NOT NULL,
  project TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  data TEXT NOT NULL,
  PRIMARY KEY (seq, idx)
);

CREATE TABLE receipts (
  principal TEXT NOT NULL,
  command_id TEXT NOT NULL,
  seq INTEGER NOT NULL UNIQUE REFERENCES envelopes (seq),
  method TEXT NOT NULL,
  digest TEXT NOT NULL DEFAULT '',
  result TEXT,
  PRIMARY KEY (principal, command_id)
);

CREATE TABLE imports (
  source TEXT NOT NULL,
  sum TEXT NOT NULL,
  last_seq INTEGER NOT NULL,
  at INTEGER NOT NULL
);
