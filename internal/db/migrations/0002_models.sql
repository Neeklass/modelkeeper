-- 0002: models, creators, variants, tags, detection anchors, overrides, search.
-- Descriptive fields live in model; detection internals in model_anchor,
-- override and location.model_id/variant_id (A5).

CREATE TABLE creator (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE
);

CREATE TABLE model (
    id              INTEGER PRIMARY KEY,
    uid             TEXT    NOT NULL UNIQUE,              -- UUIDv7, stable external ID (D6)
    name            TEXT    NOT NULL,
    creator_id      INTEGER REFERENCES creator (id) ON DELETE SET NULL,
    notes           TEXT    NOT NULL DEFAULT '',
    origin          TEXT,                                 -- e.g. 'myminifactory' (post-MVP)
    origin_url      TEXT,
    license         TEXT,
    print_status    TEXT    NOT NULL DEFAULT 'not_printed'
                            CHECK (print_status IN ('not_printed', 'printed', 'failed')), -- L1
    supports        TEXT CHECK (supports IN ('presupported', 'unsupported', 'both')),      -- L2
    supports_manual INTEGER NOT NULL DEFAULT 0,
    name_manual     INTEGER NOT NULL DEFAULT 0,           -- 1 = user renamed, detection keeps it
    creator_manual  INTEGER NOT NULL DEFAULT 0,
    thumb_source    TEXT CHECK (thumb_source IN ('manual', 'image', 'embedded', 'rendered')),
    thumb_file      TEXT,                                 -- file name in data/thumbnails
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX model_creator ON model (creator_id);
CREATE INDEX model_name    ON model (name COLLATE NOCASE);

-- Where detection found a model: a folder, an archive, or a single loose file.
-- A model can have several anchors (local + NAS copy, merged models).
CREATE TABLE model_anchor (
    id       INTEGER PRIMARY KEY,
    model_id INTEGER NOT NULL REFERENCES model (id) ON DELETE CASCADE,
    root_id  INTEGER NOT NULL REFERENCES root (id) ON DELETE CASCADE,
    path     TEXT    NOT NULL,
    kind     TEXT    NOT NULL CHECK (kind IN ('folder', 'archive', 'file')),
    origin   TEXT    NOT NULL DEFAULT 'auto' CHECK (origin IN ('auto', 'manual')),
    UNIQUE (root_id, path)
);
CREATE INDEX model_anchor_model ON model_anchor (model_id);

-- Manual corrections of model detection; survive rescans (F4d, F4e).
CREATE TABLE override (
    id         INTEGER PRIMARY KEY,
    kind       TEXT    NOT NULL CHECK (kind IN ('is_model', 'not_model', 'merge', 'split')),
    root_id    INTEGER NOT NULL REFERENCES root (id) ON DELETE CASCADE,
    path       TEXT    NOT NULL,
    model_id   INTEGER REFERENCES model (id) ON DELETE CASCADE,
    data       TEXT,                                      -- JSON
    created_at INTEGER NOT NULL
);
CREATE INDEX override_root ON override (root_id, path);

-- Size variants (F4f), e.g. S, M, 32mm.
CREATE TABLE variant (
    id       INTEGER PRIMARY KEY,
    model_id INTEGER NOT NULL REFERENCES model (id) ON DELETE CASCADE,
    label    TEXT    NOT NULL COLLATE NOCASE,
    sort     INTEGER NOT NULL DEFAULT 0,
    UNIQUE (model_id, label)
);

ALTER TABLE location ADD COLUMN model_id   INTEGER REFERENCES model (id) ON DELETE SET NULL;
ALTER TABLE location ADD COLUMN variant_id INTEGER REFERENCES variant (id) ON DELETE SET NULL;
CREATE INDEX location_model   ON location (model_id);
CREATE INDEX location_variant ON location (variant_id);

-- Tags (G1) and suggestions (F4g, F5) - suggestions are never applied automatically.
CREATE TABLE tag (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE
);

CREATE TABLE model_tag (
    model_id INTEGER NOT NULL REFERENCES model (id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tag (id) ON DELETE CASCADE,
    PRIMARY KEY (model_id, tag_id)
) WITHOUT ROWID;
CREATE INDEX model_tag_tag ON model_tag (tag_id);

CREATE TABLE tag_suggestion (
    model_id INTEGER NOT NULL REFERENCES model (id) ON DELETE CASCADE,
    name     TEXT    NOT NULL COLLATE NOCASE,
    source   TEXT    NOT NULL CHECK (source IN ('folder', 'filename')),
    state    TEXT    NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'dismissed')),
    PRIMARY KEY (model_id, name)
) WITHOUT ROWID;

-- Full-text search (H1); rowid = model.id, maintained by the app.
CREATE VIRTUAL TABLE model_fts USING fts5(
    name, creator, tags, notes, paths,
    tokenize = 'unicode61 remove_diacritics 2',
    prefix = '2 3'
);
