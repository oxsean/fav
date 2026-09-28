-- synced: when tend last brought this issue and its task in step (UnixNano, 0 = never).
ALTER TABLE tracker_issues ADD COLUMN synced INTEGER NOT NULL DEFAULT 0;
