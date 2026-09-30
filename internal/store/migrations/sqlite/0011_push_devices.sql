-- What each device wants of the notices: which events, whether the lock screen hides what they say, how long a push
-- waits for the page. '{}' is the defaults.
ALTER TABLE push_devices ADD COLUMN prefs TEXT NOT NULL DEFAULT '{}';
-- The session (or token) that registered or last renewed the device: it pushes only while that lets someone in. A
-- device from before has none and goes at the next sweep; its page registers it again when it opens.
ALTER TABLE push_devices ADD COLUMN credential_id TEXT NOT NULL DEFAULT '';
-- A delivery's result names no address (a push endpoint's path is its device's secret): what was recorded before keeps
-- only the error's class.
UPDATE deliveries SET result = CASE
	WHEN result LIKE '%timeout%' OR result LIKE '%deadline exceeded%' THEN 'Post: timeout'
	WHEN result LIKE '%connection refused%' THEN 'dial: connect: connection refused'
	WHEN result LIKE '%no such host%' THEN 'dial: no such host'
	ELSE 'Post: unreachable'
END WHERE result LIKE '%://%';
