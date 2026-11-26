-- 0013: photos of printed results (L1), e.g. the painted miniature or a
-- failed print. They are user data like the notes, so they live in the
-- catalog itself (resized JPEG, at most ~1600 px): snapshots, restores,
-- exports and the sync carry them without separate files.
CREATE TABLE print_photo (
    id         INTEGER PRIMARY KEY,
    print_id   INTEGER NOT NULL REFERENCES print_log (id) ON DELETE CASCADE,
    data       BLOB    NOT NULL,                       -- JPEG
    sha256     BLOB    NOT NULL,                       -- of data, to skip duplicates on import
    width      INTEGER NOT NULL,
    height     INTEGER NOT NULL,
    created_at INTEGER NOT NULL,                       -- unix ms
    UNIQUE (print_id, sha256)
);
CREATE INDEX print_photo_print ON print_photo (print_id);
