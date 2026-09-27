-- Times are Unix nanoseconds. Secrets are stored as sha256 hex only.
CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0,
  created INTEGER NOT NULL
);

CREATE TABLE identities (
  provider TEXT NOT NULL,
  issuer TEXT NOT NULL,
  subject TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id),
  username TEXT NOT NULL DEFAULT '',
  email TEXT NOT NULL DEFAULT '',
  email_verified INTEGER NOT NULL DEFAULT 0,
  created INTEGER NOT NULL,
  PRIMARY KEY (provider, issuer, subject)
);

CREATE TABLE admits (
  kind TEXT NOT NULL,
  value TEXT NOT NULL,
  role TEXT NOT NULL,
  added_by TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL,
  PRIMARY KEY (kind, value)
);

CREATE TABLE invites (
  sum TEXT PRIMARY KEY,
  role TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created INTEGER NOT NULL,
  expires INTEGER NOT NULL,
  used_by TEXT NOT NULL DEFAULT '',
  used_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE credentials (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  owner TEXT NOT NULL REFERENCES users (id),
  sum TEXT NOT NULL UNIQUE,
  node_id TEXT NOT NULL DEFAULT '',
  host TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL,
  last_used INTEGER NOT NULL DEFAULT 0,
  expires INTEGER NOT NULL DEFAULT 0,
  revoked INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX credentials_node ON credentials (name) WHERE kind = 'node' AND revoked = 0;

CREATE TABLE audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  actor TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT ''
);

INSERT INTO users (id, name, role, created) VALUES ('local', 'server host', 'admin', 0);
