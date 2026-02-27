------------------------------------------------------------
-- Semantic search (pgvector-backed)
-- This schema is only applied when pgvector extension is available.
------------------------------------------------------------

-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Embeddings are associated with individual entries.
-- The embedding column is unparameterized to support any dimension.
-- The model column records which provider/model produced each vector.
-- Note: no FK to entries — PostgreSQL does not support FKs referencing partitioned tables.
CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id              UUID NOT NULL,
    conversation_id       UUID NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    conversation_group_id UUID NOT NULL REFERENCES conversation_groups (id) ON DELETE CASCADE,
    embedding             vector NOT NULL,
    model                 VARCHAR(128) NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (entry_id, conversation_group_id)
) PARTITION BY HASH (conversation_group_id);

CREATE TABLE IF NOT EXISTS entry_embeddings_p0  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 0);
CREATE TABLE IF NOT EXISTS entry_embeddings_p1  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 1);
CREATE TABLE IF NOT EXISTS entry_embeddings_p2  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 2);
CREATE TABLE IF NOT EXISTS entry_embeddings_p3  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 3);
CREATE TABLE IF NOT EXISTS entry_embeddings_p4  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 4);
CREATE TABLE IF NOT EXISTS entry_embeddings_p5  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 5);
CREATE TABLE IF NOT EXISTS entry_embeddings_p6  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 6);
CREATE TABLE IF NOT EXISTS entry_embeddings_p7  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 7);
CREATE TABLE IF NOT EXISTS entry_embeddings_p8  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 8);
CREATE TABLE IF NOT EXISTS entry_embeddings_p9  PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 9);
CREATE TABLE IF NOT EXISTS entry_embeddings_p10 PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 10);
CREATE TABLE IF NOT EXISTS entry_embeddings_p11 PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 11);
CREATE TABLE IF NOT EXISTS entry_embeddings_p12 PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 12);
CREATE TABLE IF NOT EXISTS entry_embeddings_p13 PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 13);
CREATE TABLE IF NOT EXISTS entry_embeddings_p14 PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 14);
CREATE TABLE IF NOT EXISTS entry_embeddings_p15 PARTITION OF entry_embeddings FOR VALUES WITH (MODULUS 16, REMAINDER 15);

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

------------------------------------------------------------
-- Memory vectors (episodic memory semantic search)
-- One row per (memory_id, field_name). Multi-field items produce multiple rows.
-- No FK to memories — primary DB and vector DB may be separate services.
------------------------------------------------------------

CREATE TABLE IF NOT EXISTS memory_vectors (
    memory_id         UUID  NOT NULL,
    field_name        TEXT  NOT NULL,  -- embedded field name, e.g. "text"
    namespace         TEXT  NOT NULL,  -- RS-encoded namespace (redundant copy for prefix filtering)
    policy_attributes JSONB,           -- redundant copy of OPA-extracted attributes for filtering
    embedding         vector NOT NULL, -- dimension from configured embedding model
    PRIMARY KEY (memory_id, field_name)
);

-- Namespace prefix filtering (for search scoping)
CREATE INDEX IF NOT EXISTS memory_vectors_ns_idx
    ON memory_vectors (namespace);

-- Attribute-based pre-filtering
CREATE INDEX IF NOT EXISTS memory_vectors_policy_attrs_gin_idx
    ON memory_vectors USING GIN (policy_attributes) WHERE policy_attributes IS NOT NULL;
