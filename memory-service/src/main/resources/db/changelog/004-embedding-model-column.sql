-- Add model column to entry_embeddings and remove hardcoded vector dimension.
-- For pre-release: existing embeddings get a default model value.

-- Add model column (nullable first for migration)
ALTER TABLE entry_embeddings ADD COLUMN IF NOT EXISTS model VARCHAR(128);

-- Backfill existing rows with the local model identifier
UPDATE entry_embeddings SET model = 'local/all-MiniLM-L6-v2' WHERE model IS NULL;

-- Make model column NOT NULL
ALTER TABLE entry_embeddings ALTER COLUMN model SET NOT NULL;

-- Index for filtering by model (for selective re-indexing)
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_model
    ON entry_embeddings (model);

-- Drop the old HNSW index (it was created for vector(384) and is incompatible
-- with the unparameterized vector column after provider migration)
DROP INDEX IF EXISTS idx_entry_embeddings_hnsw;
