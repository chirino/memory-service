PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS conversation_groups (
    id TEXT PRIMARY KEY,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    title BLOB,
    owner_user_id TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    forked_at_entry_id TEXT,
    forked_at_conversation_id TEXT REFERENCES conversations(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    vectorized_at DATETIME,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_conversations_group ON conversations(conversation_group_id);
CREATE INDEX IF NOT EXISTS idx_conversations_not_deleted ON conversations(deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_forked_at_conversation ON conversations(forked_at_conversation_id);
CREATE INDEX IF NOT EXISTS idx_conversations_forked_at_entry ON conversations(forked_at_entry_id);

CREATE TABLE IF NOT EXISTS conversation_memberships (
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    access_level TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (conversation_group_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_conversation_memberships_user
    ON conversation_memberships(user_id, conversation_group_id);
CREATE INDEX IF NOT EXISTS idx_conversation_memberships_group
    ON conversation_memberships(conversation_group_id);
CREATE INDEX IF NOT EXISTS idx_conversation_groups_not_deleted
    ON conversation_groups(deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_conversation_groups_deleted
    ON conversation_groups(deleted_at) WHERE deleted_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS entries (
    id TEXT NOT NULL,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    user_id TEXT,
    client_id TEXT,
    channel TEXT NOT NULL,
    epoch INTEGER,
    content_type TEXT NOT NULL,
    content BLOB NOT NULL,
    indexed_content TEXT,
    indexed_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, conversation_group_id)
);

CREATE INDEX IF NOT EXISTS idx_entries_unindexed
    ON entries(channel, created_at) WHERE indexed_content IS NULL;
CREATE INDEX IF NOT EXISTS idx_entries_pending_vector_indexing
    ON entries(indexed_at) WHERE indexed_content IS NOT NULL AND indexed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_entries_conversation_created_at
    ON entries(conversation_id, created_at);
CREATE INDEX IF NOT EXISTS idx_entries_group_created_at
    ON entries(conversation_group_id, created_at);
CREATE INDEX IF NOT EXISTS idx_entries_conversation_channel_client_epoch_created_at
    ON entries(conversation_id, channel, client_id, epoch, created_at);

CREATE TABLE IF NOT EXISTS conversation_ownership_transfers (
    id TEXT PRIMARY KEY,
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    from_user_id TEXT NOT NULL,
    to_user_id TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (conversation_group_id)
);

CREATE INDEX IF NOT EXISTS idx_ownership_transfers_to_user
    ON conversation_ownership_transfers(to_user_id);
CREATE INDEX IF NOT EXISTS idx_ownership_transfers_from_user
    ON conversation_ownership_transfers(from_user_id);

CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    task_name TEXT UNIQUE,
    task_type TEXT NOT NULL,
    task_body TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    retry_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_tasks_ready
    ON tasks(task_type, retry_at);

CREATE TABLE IF NOT EXISTS attachments (
    id TEXT PRIMARY KEY,
    storage_key TEXT,
    filename TEXT,
    content_type TEXT NOT NULL,
    size INTEGER,
    sha256 TEXT,
    user_id TEXT NOT NULL,
    entry_id TEXT,
    status TEXT NOT NULL DEFAULT 'ready',
    source_url TEXT,
    expires_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_attachments_expires_at
    ON attachments(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_entry_id
    ON attachments(entry_id) WHERE entry_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_storage_key
    ON attachments(storage_key) WHERE storage_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_deleted_at
    ON attachments(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_status
    ON attachments(status) WHERE status != 'ready';
CREATE INDEX IF NOT EXISTS idx_attachments_user_id
    ON attachments(user_id);
CREATE INDEX IF NOT EXISTS idx_attachments_created_at_id
    ON attachments(created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    key TEXT NOT NULL,
    value_encrypted BLOB,
    policy_attributes TEXT,
    indexed_content TEXT,
    kind INTEGER NOT NULL DEFAULT 0,
    deleted_reason INTEGER,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    deleted_at DATETIME,
    indexed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_memories_active_lookup
    ON memories(namespace, key, deleted_at);
CREATE INDEX IF NOT EXISTS idx_memories_pending_index
    ON memories(indexed_at, deleted_at);
CREATE INDEX IF NOT EXISTS idx_memories_expires_at
    ON memories(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_memories_deleted_at
    ON memories(deleted_at) WHERE deleted_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS memory_usage_stats (
    namespace TEXT NOT NULL,
    key TEXT NOT NULL,
    fetch_count INTEGER NOT NULL DEFAULT 0,
    last_fetched_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (namespace, key)
);

CREATE INDEX IF NOT EXISTS idx_memory_usage_last_fetched
    ON memory_usage_stats(last_fetched_at DESC);

CREATE TABLE IF NOT EXISTS memory_vectors (
    memory_id TEXT NOT NULL,
    field_name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    policy_attributes TEXT,
    embedding BLOB NOT NULL,
    PRIMARY KEY (memory_id, field_name)
);

CREATE INDEX IF NOT EXISTS idx_memory_vectors_namespace
    ON memory_vectors(namespace);
