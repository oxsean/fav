-- Push devices: each browser (Web Push) that shows a user's notices, found again by its endpoint's hash; target is
-- sealed by the server's key.
CREATE TABLE push_devices (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  target BLOB NOT NULL,
  target_hash TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  renewed_at INTEGER NOT NULL,
  last_ok_at INTEGER NOT NULL DEFAULT 0,
  failures INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX push_devices_user ON push_devices (user_id);

-- deliveries becomes the outbox: one row per notice × recipient × device ('' is the user's webhook), with its state
-- and when it goes next. The server's own notices (seq 0) go each time, so only the journal's are unique. SQLite
-- cannot change a primary key: the table is copied. What an older build delivered is kept as done or failed and never
-- goes again.
CREATE TABLE deliveries_new (
  id INTEGER PRIMARY KEY,
  seq INTEGER NOT NULL,
  user_id TEXT NOT NULL,
  event TEXT NOT NULL,
  device_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_at INTEGER NOT NULL DEFAULT 0,
  notice TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT '',
  at INTEGER NOT NULL
);
INSERT INTO deliveries_new (seq, user_id, event, device_id, status, result, at)
  SELECT seq, user_id, event, '', CASE WHEN status GLOB '2[0-9][0-9]' THEN 'ok' ELSE 'failed' END, status, at FROM deliveries;
DROP TABLE deliveries;
ALTER TABLE deliveries_new RENAME TO deliveries;
CREATE UNIQUE INDEX deliveries_once ON deliveries (seq, user_id, event, device_id) WHERE seq > 0;
CREATE INDEX deliveries_due ON deliveries (next_at) WHERE status = 'pending';
