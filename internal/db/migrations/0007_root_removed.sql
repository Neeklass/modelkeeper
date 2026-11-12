-- 0007: removing a root folder deactivates it (soft remove): it is no longer
-- scanned or watched, but its locations, anchors, overrides and hashes stay, so
-- adding the same path again links everything up again. NULL = active;
-- otherwise unix milliseconds of the deactivation.
ALTER TABLE root ADD COLUMN removed_at INTEGER;
