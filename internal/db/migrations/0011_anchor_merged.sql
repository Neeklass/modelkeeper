-- 0011: places (anchors) a model only has because the user merged another
-- model into it (F4d). When that merge is removed again (undo, reset of the
-- corrections), such a place no longer keeps the model: its folder becomes a
-- model of its own again. Places of existing merges are marked from their
-- merge corrections.
ALTER TABLE model_anchor ADD COLUMN merged INTEGER NOT NULL DEFAULT 0;

UPDATE model_anchor SET merged = 1 WHERE EXISTS (
    SELECT 1 FROM override o
    WHERE o.kind = 'merge' AND o.root_id = model_anchor.root_id AND o.path = model_anchor.path
);
