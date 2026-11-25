-- 0010: journal of catalog changes the user made (TODO 2.1), so every change
-- can be undone. Each row is one action (e.g. "3 tags added to 12 models")
-- with the user data of the changed models before the action (JSON, see
-- catalog/journal.go). Undo puts that state back. The newest rows are kept.
-- Model origin, purchase link and license count as user data (E6).
CREATE TABLE journal (
    id        INTEGER PRIMARY KEY,
    at        INTEGER NOT NULL,                       -- unix ms
    action    TEXT    NOT NULL,                       -- message key, e.g. 'history.tagsAdded'
    params    TEXT,                                   -- JSON object for the message
    models    INTEGER NOT NULL,                       -- number of models changed
    state     TEXT    NOT NULL,                       -- JSON: user data of the models before
    undone_at INTEGER                                 -- set when undone
);
