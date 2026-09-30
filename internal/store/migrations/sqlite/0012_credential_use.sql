-- Where a credential is used from: the browser a session signed in with (its User-Agent) and the address it last
-- connected from. What was used before has neither.
ALTER TABLE credentials ADD COLUMN agent TEXT NOT NULL DEFAULT '';
ALTER TABLE credentials ADD COLUMN last_ip TEXT NOT NULL DEFAULT '';
