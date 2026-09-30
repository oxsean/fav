-- When each machine was last connected, while it is not: tend-server writes it as a connection ends and deletes it once
-- the machine is connected again, so a restart still says how long a machine has been offline.
CREATE TABLE machine_seen (
	name TEXT PRIMARY KEY,
	at INTEGER NOT NULL
);
