------------------------------------------------------------
-- Semantic search (pgvector-backed)
-- This schema is only applied when pgvector extension is available.
------------------------------------------------------------

-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Embeddings are associated with individual entries.
-- The embedding column is unparameterized to support any dimension.
-- The model column records which provider/model produced each vector.
CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id              UUID PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
    conversation_id       UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    embedding             vector NOT NULL,
    model                 VARCHAR(128) NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for filtering by conversation group (for access control via JOIN)
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_group
    ON entry_embeddings (conversation_group_id);

-- Index for filtering by model (for selective re-indexing)
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_model
    ON entry_embeddings (model);

-- Note: HNSW index requires a typed vector column with known dimensions.
-- When using an unparameterized vector column, create the HNSW index manually
-- after selecting your embedding provider:
--
--   CREATE INDEX idx_entry_embeddings_hnsw
--       ON entry_embeddings
--       USING hnsw ((embedding::vector(384)) vector_cosine_ops)
--       WITH (m = 16, ef_construction = 64);
--
-- Replace 384 with your embedding model's dimension (e.g., 1536 for OpenAI text-embedding-3-small).
