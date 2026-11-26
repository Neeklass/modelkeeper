-- 0012: print log per model, collections (ordered lists of models) and
-- saved searches. All of it is user data: kept by the cleanup, moved by a
-- merge, part of the export (J6) and of the sync between PCs.
CREATE TABLE print_log (
    id         INTEGER PRIMARY KEY,
    model_id   INTEGER NOT NULL REFERENCES model (id) ON DELETE CASCADE,
    printed_at INTEGER NOT NULL,                 -- unix ms, the day of the print
    result     TEXT    NOT NULL CHECK (result IN ('ok', 'failed')),
    printer    TEXT    NOT NULL DEFAULT '',
    settings   TEXT    NOT NULL DEFAULT '',     -- e.g. resin, layer height, exposure
    notes      TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX print_log_model ON print_log (model_id, printed_at);

CREATE TABLE collection (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    created_at INTEGER NOT NULL
);
CREATE TABLE collection_model (
    collection_id INTEGER NOT NULL REFERENCES collection (id) ON DELETE CASCADE,
    model_id      INTEGER NOT NULL REFERENCES model (id) ON DELETE CASCADE,
    position      INTEGER NOT NULL,
    PRIMARY KEY (collection_id, model_id)
);
CREATE INDEX collection_model_model ON collection_model (model_id);

CREATE TABLE saved_search (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    query      TEXT    NOT NULL,                 -- the frontend's query as JSON
    created_at INTEGER NOT NULL
);
