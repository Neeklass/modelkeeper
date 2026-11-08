-- 0001: roots, content-addressed files, locations, scan history.
-- Conventions: paths are '/'-separated and relative to the root (or to the
-- archive for archive members); *_at columns are unix milliseconds; mtime is
-- unix nanoseconds.

-- A root folder that is scanned recursively (E1/E2).
CREATE TABLE root (
    id            INTEGER PRIMARY KEY,
    path          TEXT    NOT NULL UNIQUE,                -- absolute: C:\..., \\fritz.nas\...
    kind          TEXT    NOT NULL CHECK (kind IN ('local', 'nas')),
    creator_level INTEGER NOT NULL DEFAULT 1,             -- F4a: first level = creator
    status        TEXT    NOT NULL DEFAULT 'unknown'
                          CHECK (status IN ('unknown', 'online', 'offline')), -- E3
    last_scan_at  INTEGER,
    created_at    INTEGER NOT NULL
);

-- Unique file content, identified by its hash (D4).
CREATE TABLE file (
    id     INTEGER PRIMARY KEY,
    sha256 BLOB    NOT NULL UNIQUE,                        -- 32 bytes
    size   INTEGER NOT NULL
);

-- One occurrence of a file: on disk below a root, or inside an archive (D3, D8).
CREATE TABLE location (
    id             INTEGER PRIMARY KEY,
    root_id        INTEGER NOT NULL REFERENCES root (id) ON DELETE CASCADE,
    archive_id     INTEGER REFERENCES location (id) ON DELETE CASCADE, -- NULL = on disk
    path           TEXT    NOT NULL,
    ext            TEXT    NOT NULL,                       -- lowercase, without dot
    category       TEXT    NOT NULL
                           CHECK (category IN ('model', 'slicer', 'archive', 'supplementary')),
    size           INTEGER NOT NULL,
    mtime          INTEGER NOT NULL,                       -- F2: path + size + mtime
    file_id        INTEGER REFERENCES file (id),           -- NULL = not hashed yet (F3)
    archive_status TEXT CHECK (archive_status IN ('pending', 'ok', 'broken', 'encrypted', 'unsupported')), -- F7
    archive_error  TEXT,                                   -- message key
    present        INTEGER NOT NULL DEFAULT 1,             -- 0 = gone in the last complete scan
    last_seen_scan INTEGER,                                -- scan_run.id
    first_seen_at  INTEGER NOT NULL,
    missing_since  INTEGER
);
CREATE UNIQUE INDEX location_disk   ON location (root_id, path)    WHERE archive_id IS NULL;
CREATE UNIQUE INDEX location_member ON location (archive_id, path) WHERE archive_id IS NOT NULL;
CREATE INDEX location_file          ON location (file_id);
CREATE INDEX location_unhashed      ON location (root_id) WHERE file_id IS NULL AND present = 1;
CREATE INDEX location_archive_todo  ON location (root_id) WHERE archive_status = 'pending';

-- One scan of a root (F1).
CREATE TABLE scan_run (
    id            INTEGER PRIMARY KEY,
    root_id       INTEGER NOT NULL REFERENCES root (id) ON DELETE CASCADE,
    mode          TEXT    NOT NULL CHECK (mode IN ('incremental', 'full')),
    status        TEXT    NOT NULL CHECK (status IN ('running', 'done', 'cancelled', 'failed', 'offline')),
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER,
    files_seen    INTEGER NOT NULL DEFAULT 0,
    files_new     INTEGER NOT NULL DEFAULT 0,
    files_changed INTEGER NOT NULL DEFAULT 0,
    files_missing INTEGER NOT NULL DEFAULT 0,
    files_hashed  INTEGER NOT NULL DEFAULT 0,
    bytes_hashed  INTEGER NOT NULL DEFAULT 0,
    error_key     TEXT
);
CREATE INDEX scan_run_root ON scan_run (root_id, id);

-- Problems found during a scan (F7 broken archives, F8 changed hashes, read errors).
CREATE TABLE scan_issue (
    id          INTEGER PRIMARY KEY,
    scan_run_id INTEGER NOT NULL REFERENCES scan_run (id) ON DELETE CASCADE,
    location_id INTEGER REFERENCES location (id) ON DELETE CASCADE,
    kind        TEXT    NOT NULL
                        CHECK (kind IN ('archive_broken', 'archive_encrypted', 'read_error', 'hash_changed')),
    msg_key     TEXT    NOT NULL,
    params      TEXT,                                      -- JSON object
    created_at  INTEGER NOT NULL
);
CREATE INDEX scan_issue_run ON scan_issue (scan_run_id);
