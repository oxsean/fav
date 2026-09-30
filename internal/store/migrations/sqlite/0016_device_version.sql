-- A device's registration: it goes up when the browser changes hands, and a delivery is for the one it was written
-- for. The rows still to go are for the registration there is.
ALTER TABLE push_devices ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE deliveries ADD COLUMN device_version INTEGER NOT NULL DEFAULT 0;
UPDATE deliveries SET device_version = 1 WHERE device_id != '';
