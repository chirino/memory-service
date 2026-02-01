------------------------------------------------------------
-- Semantic search (pgvector-backed)
-- This schema is only applied when pgvector extension is available.
------------------------------------------------------------

-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Embeddings are associated with individual entries.
-- Uses all-MiniLM-L6-v2 model which produces 384-dimensional vectors.
CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id              UUID PRIMARY KEY REFERENCES entries (id) ON DELETE CASCADE,
    conversation_id       UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    embedding             vector(384) NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for filtering by conversation group (for access control via JOIN)
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_group
    ON entry_embeddings (conversation_group_id);

-- HNSW index for fast approximate nearest neighbor search
-- HNSW is preferred over IVFFlat for better query performance
CREATE INDEX IF NOT EXISTS idx_entry_embeddings_hnsw
    ON entry_embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
