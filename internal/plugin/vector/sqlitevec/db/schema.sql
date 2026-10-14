-- Timestamp pairs store Unix seconds and normalized nanoseconds, decoded as UTC.
CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    conversation_group_id TEXT NOT NULL,
    embedding BLOB NOT NULL,
    model TEXT NOT NULL,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    CHECK(typeof(embedding) = 'blob')
);

CREATE INDEX IF NOT EXISTS idx_entry_embeddings_group
    ON entry_embeddings (conversation_group_id);

CREATE INDEX IF NOT EXISTS idx_entry_embeddings_model
    ON entry_embeddings (model);
