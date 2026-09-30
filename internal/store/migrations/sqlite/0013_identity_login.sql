-- When each sign-in account last signed its user in; 0 for one that has not since.
ALTER TABLE identities ADD COLUMN last_login INTEGER NOT NULL DEFAULT 0;
