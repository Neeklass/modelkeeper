-- 0009: scans can be paused by the user. A paused root is not scanned
-- automatically (live watching, NAS schedule, resume at start) until the user
-- resumes it; work done so far (hashes, checkpoints inside large files) stays.
ALTER TABLE root ADD COLUMN scan_paused INTEGER NOT NULL DEFAULT 0;
