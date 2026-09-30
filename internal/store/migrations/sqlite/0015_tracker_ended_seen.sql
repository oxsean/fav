-- ended_seen: the issue's updated time (the tracker's clock, UnixNano) at the last read that found its task finished;
-- 0 when it was not. A reopen after it reopens the task even when the issue was closed again in between.
ALTER TABLE tracker_issues ADD COLUMN ended_seen INTEGER NOT NULL DEFAULT 0;
