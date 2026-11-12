-- 0008: content of models that lost their last location when a root folder
-- was deleted permanently (E9): the SHA-256 of their content files. A later
-- scan that finds the same content anywhere links such a model up again, so
-- its tags and notes are not separated from the files for good (F6, D6).
-- Rows are removed when the model is linked up again.
CREATE TABLE model_fingerprint (
    model_id INTEGER NOT NULL REFERENCES model (id) ON DELETE CASCADE,
    sha256   BLOB    NOT NULL,
    PRIMARY KEY (model_id, sha256)
) WITHOUT ROWID;
CREATE INDEX model_fingerprint_sha ON model_fingerprint (sha256);
