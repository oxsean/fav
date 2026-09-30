-- What each device wants of the notices: which events, whether the lock screen hides what they say, how long a push
-- waits for the page. '{}' is the defaults.
ALTER TABLE push_devices ADD COLUMN prefs TEXT NOT NULL DEFAULT '{}';
