-- 0005: favorite models (a list the user keeps). User data like tags and notes.
ALTER TABLE model ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0;
CREATE INDEX model_favorite ON model (id) WHERE favorite = 1;
