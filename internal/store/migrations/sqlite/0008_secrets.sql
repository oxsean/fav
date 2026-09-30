-- The server's own secrets, sealed by its key (the Web Push key pair): name, the sealed value, when it was made (UnixNano).
CREATE TABLE secrets (
  name TEXT PRIMARY KEY,
  value BLOB NOT NULL,
  at INTEGER NOT NULL
);
