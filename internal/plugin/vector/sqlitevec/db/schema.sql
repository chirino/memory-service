CREATE TABLE IF NOT EXISTS entry_embeddings (
    entry_id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    conversation_group_id TEXT NOT NULL,
    embedding BLOB NOT NULL,
    model TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK(typeof(embedding) = 'blob')
);

CREATE INDEX IF NOT EXISTS idx_entry_embeddings_group
    ON entry_embeddings (conversation_group_id);

CREATE INDEX IF NOT EXISTS idx_entry_embeddings_model
    ON entry_embeddings (model);
