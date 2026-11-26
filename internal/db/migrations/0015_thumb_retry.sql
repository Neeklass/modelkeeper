-- 0015: thumbnails can now also be drawn from OBJ files and from the mesh of
-- a 3MF without a preview picture. Models that got none so far are tried
-- once more (only the "tried" mark is reset; nothing else changes).
UPDATE model SET thumb_tried_at = NULL WHERE thumb_source IS NULL;
