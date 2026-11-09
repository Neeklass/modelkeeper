-- 0004: checkpoints of large files that were being hashed when a scan was
-- interrupted, so the next scan continues inside the file instead of reading
-- it again from the start. Valid only while size and mtime are unchanged.
CREATE TABLE hash_progress (
    location_id INTEGER PRIMARY KEY REFERENCES location (id) ON DELETE CASCADE,
    size        INTEGER NOT NULL,
    mtime       INTEGER NOT NULL,
    offset      INTEGER NOT NULL,                       -- bytes already hashed
    data        BLOB    NOT NULL,                       -- JSON, see scanner/resume.go
    updated_at  INTEGER NOT NULL
);
