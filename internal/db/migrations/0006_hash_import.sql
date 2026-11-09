-- 0006: hashes imported from another modelkeeper that scanned the same
-- folder (e.g. the same NAS). The next scan of the root uses an entry instead
-- of reading the file when path, size and mtime match exactly; the entries
-- are removed when that scan completes.
CREATE TABLE hash_import (
    root_id INTEGER NOT NULL REFERENCES root (id) ON DELETE CASCADE,
    path    TEXT    NOT NULL,                           -- relative to the root, '/'-separated
    size    INTEGER NOT NULL,
    mtime   INTEGER NOT NULL,
    sha256  BLOB    NOT NULL,
    archive TEXT,                                       -- JSON: status, error key, members (archives only)
    PRIMARY KEY (root_id, path)
) WITHOUT ROWID;
