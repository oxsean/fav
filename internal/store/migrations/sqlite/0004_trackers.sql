-- Issue tracker bindings (tokens sealed with the server's key), how each issue's write-back stands, and the webhook
-- deliveries already taken.
CREATE TABLE trackers (
  id TEXT PRIMARY KEY,
  project TEXT NOT NULL,
  kind TEXT NOT NULL,
  base TEXT NOT NULL,
  repo TEXT NOT NULL,
  repo_id INTEGER NOT NULL,
  bot TEXT NOT NULL,
  token BLOB NOT NULL,
  hook_secret BLOB NOT NULL,
  settings TEXT NOT NULL DEFAULT '{}',
  created_by TEXT NOT NULL,
  created INTEGER NOT NULL,
  cursor INTEGER NOT NULL DEFAULT 0,
  etag TEXT NOT NULL DEFAULT '',
  polled INTEGER NOT NULL DEFAULT 0,
  paused_until INTEGER NOT NULL DEFAULT 0,
  stopped TEXT NOT NULL DEFAULT '',
  last_ok INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  UNIQUE (kind, base, repo_id)
);

CREATE TABLE tracker_issues (
  tracker TEXT NOT NULL REFERENCES trackers (id) ON DELETE CASCADE,
  number INTEGER NOT NULL,
  task TEXT NOT NULL DEFAULT '',
  comment_id INTEGER NOT NULL DEFAULT 0,
  body_hash TEXT NOT NULL DEFAULT '',
  written INTEGER NOT NULL DEFAULT 0,
  closed INTEGER NOT NULL DEFAULT 0,
  dirty INTEGER NOT NULL DEFAULT 1,
  last_error TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (tracker, number)
);

CREATE TABLE tracker_deliveries (
  id TEXT PRIMARY KEY,
  at INTEGER NOT NULL
);
