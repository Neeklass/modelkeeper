-- 0003: remember when thumbnail generation was tried, so models without any
-- usable source are not retried on every scan (I3).
ALTER TABLE model ADD COLUMN thumb_tried_at INTEGER;
CREATE INDEX model_thumb_pending ON model (id) WHERE thumb_source IS NULL AND thumb_tried_at IS NULL;
