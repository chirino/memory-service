-- Timestamp pairs store Unix seconds and normalized nanoseconds, decoded as UTC.
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at INTEGER_SECONDS CHECK (updated_at IS NULL OR typeof(updated_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    updated_at_ns INTEGER CHECK ((updated_at IS NULL AND updated_at_ns IS NULL) OR (updated_at IS NOT NULL AND updated_at_ns IS NOT NULL AND typeof(updated_at_ns) = 'integer' AND updated_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0
);

INSERT INTO schema_metadata (key, value, updated_at, updated_at_ns)
VALUES ('core_schema_version', '2', unixepoch(), 0)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = unixepoch(), updated_at_ns = 0;

CREATE TABLE IF NOT EXISTS conversation_groups (
    id TEXT PRIMARY KEY,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    archived_at INTEGER_SECONDS CHECK (archived_at IS NULL OR typeof(archived_at) = 'integer'),
    archived_at_ns INTEGER CHECK ((archived_at IS NULL AND archived_at_ns IS NULL) OR (archived_at IS NOT NULL AND archived_at_ns IS NOT NULL AND typeof(archived_at_ns) = 'integer' AND archived_at_ns BETWEEN 0 AND 999999999))
);

CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    title BLOB,
    owner_user_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    agent_id TEXT,
    metadata TEXT NOT NULL DEFAULT '{}',
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    -- Canonical logical lineage lives on the original conversation in a fork group.
    -- Reads project it onto every branch without copying these columns to fork rows.
    -- Soft reference: the starting conversation may be hard-evicted independently.
    started_by_conversation_id TEXT,
    started_by_entry_id TEXT,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    updated_at INTEGER_SECONDS CHECK (updated_at IS NULL OR typeof(updated_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    updated_at_ns INTEGER CHECK ((updated_at IS NULL AND updated_at_ns IS NULL) OR (updated_at IS NOT NULL AND updated_at_ns IS NOT NULL AND typeof(updated_at_ns) = 'integer' AND updated_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    vectorized_at INTEGER_SECONDS CHECK (vectorized_at IS NULL OR typeof(vectorized_at) = 'integer'),
    vectorized_at_ns INTEGER CHECK ((vectorized_at IS NULL AND vectorized_at_ns IS NULL) OR (vectorized_at IS NOT NULL AND vectorized_at_ns IS NOT NULL AND typeof(vectorized_at_ns) = 'integer' AND vectorized_at_ns BETWEEN 0 AND 999999999)),
    archived_at INTEGER_SECONDS CHECK (archived_at IS NULL OR typeof(archived_at) = 'integer'),
    archived_at_ns INTEGER CHECK ((archived_at IS NULL AND archived_at_ns IS NULL) OR (archived_at IS NOT NULL AND archived_at_ns IS NOT NULL AND typeof(archived_at_ns) = 'integer' AND archived_at_ns BETWEEN 0 AND 999999999))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_conversations_group_id_id ON conversations(conversation_group_id, id);
CREATE INDEX IF NOT EXISTS idx_conversations_created_at_id ON conversations(created_at, created_at_ns, id);
CREATE INDEX IF NOT EXISTS idx_conversations_updated_at_id ON conversations(updated_at, updated_at_ns, id);
CREATE INDEX IF NOT EXISTS idx_conversations_group ON conversations(conversation_group_id);
CREATE INDEX IF NOT EXISTS idx_conversations_not_archived ON conversations(archived_at, archived_at_ns) WHERE archived_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_started_by_conversation ON conversations(started_by_conversation_id, created_at, created_at_ns, id);
CREATE INDEX IF NOT EXISTS idx_conversations_started_by_entry ON conversations(started_by_entry_id);

CREATE TABLE IF NOT EXISTS conversation_ancestry (
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    descendant_conversation_id TEXT NOT NULL,
    ancestor_conversation_id TEXT NOT NULL,
    depth INTEGER NOT NULL CHECK (depth >= 0),
    before_entry_id TEXT,
    forked_at_entry_id TEXT,
    PRIMARY KEY (conversation_group_id, descendant_conversation_id, ancestor_conversation_id),
    UNIQUE (conversation_group_id, descendant_conversation_id, depth),
    FOREIGN KEY (conversation_group_id, descendant_conversation_id)
        REFERENCES conversations(conversation_group_id, id) ON DELETE CASCADE,
    FOREIGN KEY (conversation_group_id, ancestor_conversation_id)
        REFERENCES conversations(conversation_group_id, id) ON DELETE CASCADE,
    CHECK (
        (
            depth = 0
            AND descendant_conversation_id = ancestor_conversation_id
            AND before_entry_id IS NULL
            AND forked_at_entry_id IS NULL
        )
        OR
        (
            depth = 1
            AND descendant_conversation_id <> ancestor_conversation_id
        )
        OR
        (
            depth > 1
            AND descendant_conversation_id <> ancestor_conversation_id
            AND forked_at_entry_id IS NULL
        )
    )
);

CREATE INDEX IF NOT EXISTS idx_conversation_ancestry_ancestor
    ON conversation_ancestry(conversation_group_id, ancestor_conversation_id, depth, descendant_conversation_id);

CREATE TABLE IF NOT EXISTS conversation_memberships (
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    access_level TEXT NOT NULL,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    PRIMARY KEY (conversation_group_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_conversation_memberships_user
    ON conversation_memberships(user_id, conversation_group_id);
CREATE INDEX IF NOT EXISTS idx_conversation_memberships_group
    ON conversation_memberships(conversation_group_id);
CREATE INDEX IF NOT EXISTS idx_conversation_groups_not_archived
    ON conversation_groups(archived_at, archived_at_ns) WHERE archived_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_conversation_groups_archived
    ON conversation_groups(archived_at, archived_at_ns) WHERE archived_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS entries (
    append_batch BLOB,
    id TEXT NOT NULL,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    user_id TEXT,
    client_id TEXT,
    agent_id TEXT,
    channel TEXT NOT NULL,
    epoch INTEGER CHECK (channel = 'context' OR epoch IS NULL),
    seq INTEGER CHECK (seq IS NULL OR (seq >= 0 AND seq <= 4294967295)),
    content_type TEXT NOT NULL,
    content BLOB NOT NULL,
    indexed_content TEXT,
    indexed_at INTEGER_SECONDS CHECK (indexed_at IS NULL OR typeof(indexed_at) = 'integer'),
    indexed_at_ns INTEGER CHECK ((indexed_at IS NULL AND indexed_at_ns IS NULL) OR (indexed_at IS NOT NULL AND indexed_at_ns IS NOT NULL AND typeof(indexed_at_ns) = 'integer' AND indexed_at_ns BETWEEN 0 AND 999999999)),
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    PRIMARY KEY (id, conversation_group_id)
);

CREATE INDEX IF NOT EXISTS idx_entries_created_at_id ON entries(created_at, created_at_ns, id);

CREATE INDEX IF NOT EXISTS idx_entries_unindexed
    ON entries(channel, created_at, created_at_ns) WHERE indexed_content IS NULL;
CREATE INDEX IF NOT EXISTS idx_entries_pending_vector_indexing
    ON entries(indexed_at, indexed_at_ns) WHERE indexed_content IS NOT NULL AND indexed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_entries_conversation_created_at
    ON entries(conversation_id, created_at, created_at_ns);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entries_conversation_seq
    ON entries(conversation_id, seq) WHERE seq IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_entries_group_created_seq_id
    ON entries(conversation_group_id, created_at, created_at_ns, seq, id);
CREATE INDEX IF NOT EXISTS idx_entries_group_channel_created_seq_id
    ON entries(conversation_group_id, channel, created_at, created_at_ns, seq, id);
CREATE INDEX IF NOT EXISTS idx_entries_group_channel_seq_id
    ON entries(conversation_group_id, channel, seq, id) WHERE seq IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_entries_conv_channel_created_seq_id
    ON entries(conversation_id, channel, created_at, created_at_ns, seq, id);
CREATE INDEX IF NOT EXISTS idx_entries_conversation_channel_client_epoch_created_at
    ON entries(conversation_id, channel, client_id, epoch, created_at, created_at_ns);
CREATE INDEX IF NOT EXISTS idx_entries_conversation_channel_client_agent_epoch_created_at
    ON entries(conversation_id, channel, client_id, agent_id, epoch, created_at, created_at_ns);

CREATE TABLE IF NOT EXISTS conversation_ownership_transfers (
    id TEXT PRIMARY KEY,
    conversation_group_id TEXT NOT NULL REFERENCES conversation_groups(id) ON DELETE CASCADE,
    from_user_id TEXT NOT NULL,
    to_user_id TEXT NOT NULL,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
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
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    retry_at INTEGER_SECONDS CHECK (retry_at IS NULL OR typeof(retry_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    retry_at_ns INTEGER CHECK ((retry_at IS NULL AND retry_at_ns IS NULL) OR (retry_at IS NOT NULL AND retry_at_ns IS NOT NULL AND typeof(retry_at_ns) = 'integer' AND retry_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    last_error TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_tasks_ready
    ON tasks(task_type, retry_at, retry_at_ns);

CREATE TABLE IF NOT EXISTS admin_checkpoints (
    client_id TEXT PRIMARY KEY,
    content_type TEXT NOT NULL,
    value BLOB NOT NULL,
	 revision INTEGER NOT NULL DEFAULT 1,
	 lease_token_hash BLOB,
	 lease_generation INTEGER NOT NULL DEFAULT 0,
	 lease_expires_at INTEGER_SECONDS CHECK (lease_expires_at IS NULL OR typeof(lease_expires_at) = 'integer'),
	 lease_expires_at_ns INTEGER CHECK ((lease_expires_at IS NULL AND lease_expires_at_ns IS NULL) OR (lease_expires_at IS NOT NULL AND lease_expires_at_ns IS NOT NULL AND typeof(lease_expires_at_ns) = 'integer' AND lease_expires_at_ns BETWEEN 0 AND 999999999)),
    updated_at INTEGER_SECONDS CHECK (updated_at IS NULL OR typeof(updated_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    updated_at_ns INTEGER CHECK ((updated_at IS NULL AND updated_at_ns IS NULL) OR (updated_at IS NOT NULL AND updated_at_ns IS NOT NULL AND typeof(updated_at_ns) = 'integer' AND updated_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS outbox_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    event TEXT NOT NULL,
    kind TEXT NOT NULL,
    data TEXT NOT NULL,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_created_at
    ON outbox_events(created_at, created_at_ns);
CREATE INDEX IF NOT EXISTS idx_outbox_events_kind_seq
    ON outbox_events(kind, seq);

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
    expires_at INTEGER_SECONDS CHECK (expires_at IS NULL OR typeof(expires_at) = 'integer'),
    expires_at_ns INTEGER CHECK ((expires_at IS NULL AND expires_at_ns IS NULL) OR (expires_at IS NOT NULL AND expires_at_ns IS NOT NULL AND typeof(expires_at_ns) = 'integer' AND expires_at_ns BETWEEN 0 AND 999999999)),
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    archived_at INTEGER_SECONDS CHECK (archived_at IS NULL OR typeof(archived_at) = 'integer'),
    archived_at_ns INTEGER CHECK ((archived_at IS NULL AND archived_at_ns IS NULL) OR (archived_at IS NOT NULL AND archived_at_ns IS NOT NULL AND typeof(archived_at_ns) = 'integer' AND archived_at_ns BETWEEN 0 AND 999999999))
);

CREATE INDEX IF NOT EXISTS idx_attachments_expires_at
    ON attachments(expires_at, expires_at_ns) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_entry_id
    ON attachments(entry_id) WHERE entry_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_storage_key
    ON attachments(storage_key) WHERE storage_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_archived_at
    ON attachments(archived_at, archived_at_ns) WHERE archived_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_status
    ON attachments(status) WHERE status != 'ready';
CREATE INDEX IF NOT EXISTS idx_attachments_user_id
    ON attachments(user_id);
CREATE INDEX IF NOT EXISTS idx_attachments_created_at_id
    ON attachments(created_at DESC, created_at_ns DESC, id DESC);

------------------------------------------------------------
-- Memory Schema Versions, Defaults, and Migrations (Enhancement 115)
-- Defined BEFORE memories and memory_vectors so FK references resolve on fresh boot.
------------------------------------------------------------

CREATE TABLE IF NOT EXISTS memory_kind_versions (
    name            TEXT PRIMARY KEY,
    attribute_types TEXT NOT NULL DEFAULT '{}',
    attributes_rego TEXT,
    writable        INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0
);

-- Mutable per-family defaults were removed. Clean up the obsolete version-2 table.
DROP TABLE IF EXISTS memory_kind_defaults;

CREATE TABLE IF NOT EXISTS memory_kind_migrations (
    id                      TEXT PRIMARY KEY,
    source                  TEXT NOT NULL REFERENCES memory_kind_versions(name),
    target                  TEXT NOT NULL REFERENCES memory_kind_versions(name),
    namespace_prefix        TEXT,    -- serialized namespace array JSON (NULL = no prefix restriction)
    state                   TEXT NOT NULL,
    cancel_requested        INTEGER NOT NULL DEFAULT 0,
    migrated_count          INTEGER NOT NULL DEFAULT 0,
    skipped_tombstone_count INTEGER NOT NULL DEFAULT 0,
    vector_pending_count    INTEGER NOT NULL DEFAULT 0,
    retry_count             INTEGER NOT NULL DEFAULT 0,
    last_error_code         TEXT,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    started_at INTEGER_SECONDS CHECK (started_at IS NULL OR typeof(started_at) = 'integer'),
    started_at_ns INTEGER CHECK ((started_at IS NULL AND started_at_ns IS NULL) OR (started_at IS NOT NULL AND started_at_ns IS NOT NULL AND typeof(started_at_ns) = 'integer' AND started_at_ns BETWEEN 0 AND 999999999)),
    completed_at INTEGER_SECONDS CHECK (completed_at IS NULL OR typeof(completed_at) = 'integer'),
    completed_at_ns INTEGER CHECK ((completed_at IS NULL AND completed_at_ns IS NULL) OR (completed_at IS NOT NULL AND completed_at_ns IS NOT NULL AND typeof(completed_at_ns) = 'integer' AND completed_at_ns BETWEEN 0 AND 999999999)),
    CHECK (source <> target),
    CHECK (state IN ('queued', 'running', 'canceling', 'succeeded', 'failed', 'canceled'))
);

-- Partial unique index: at most one active migration per source version.
CREATE UNIQUE INDEX IF NOT EXISTS memory_kind_migrations_active_source_idx
    ON memory_kind_migrations (source)
    WHERE state IN ('queued', 'running', 'canceling');

------------------------------------------------------------
-- Namespaced Episodic Memory
------------------------------------------------------------

CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    key TEXT NOT NULL,
    value_encrypted BLOB,
    policy_attributes TEXT,
    indexed_content TEXT,
    kind INTEGER NOT NULL DEFAULT 0,
    revision INTEGER NOT NULL DEFAULT 1,
    deleted_reason INTEGER,
    created_at INTEGER_SECONDS CHECK (created_at IS NULL OR typeof(created_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    created_at_ns INTEGER CHECK ((created_at IS NULL AND created_at_ns IS NULL) OR (created_at IS NOT NULL AND created_at_ns IS NOT NULL AND typeof(created_at_ns) = 'integer' AND created_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    expires_at INTEGER_SECONDS CHECK (expires_at IS NULL OR typeof(expires_at) = 'integer'),
    expires_at_ns INTEGER CHECK ((expires_at IS NULL AND expires_at_ns IS NULL) OR (expires_at IS NOT NULL AND expires_at_ns IS NOT NULL AND typeof(expires_at_ns) = 'integer' AND expires_at_ns BETWEEN 0 AND 999999999)),
    archived_at INTEGER_SECONDS CHECK (archived_at IS NULL OR typeof(archived_at) = 'integer'),
    archived_at_ns INTEGER CHECK ((archived_at IS NULL AND archived_at_ns IS NULL) OR (archived_at IS NOT NULL AND archived_at_ns IS NOT NULL AND typeof(archived_at_ns) = 'integer' AND archived_at_ns BETWEEN 0 AND 999999999)),
    indexed_at INTEGER_SECONDS CHECK (indexed_at IS NULL OR typeof(indexed_at) = 'integer'),
    indexed_at_ns INTEGER CHECK ((indexed_at IS NULL AND indexed_at_ns IS NULL) OR (indexed_at IS NOT NULL AND indexed_at_ns IS NOT NULL AND typeof(indexed_at_ns) = 'integer' AND indexed_at_ns BETWEEN 0 AND 999999999)),
    memory_kind TEXT NOT NULL REFERENCES memory_kind_versions(name) DEFAULT 'default/v1'
);

CREATE INDEX IF NOT EXISTS idx_memories_active_lookup
    ON memories(namespace, key, archived_at, archived_at_ns);
CREATE UNIQUE INDEX IF NOT EXISTS memories_active_idx
    ON memories(namespace, key) WHERE archived_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_memories_pending_index
    ON memories(indexed_at, indexed_at_ns, archived_at, archived_at_ns);
CREATE INDEX IF NOT EXISTS idx_memories_expires_at
    ON memories(expires_at, expires_at_ns) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_memories_archived_at
    ON memories(archived_at, archived_at_ns) WHERE archived_at IS NOT NULL;

-- Index for migration scans and kind-filtered queries.
CREATE INDEX IF NOT EXISTS memories_kind_version_idx
    ON memories (memory_kind, namespace, id);
CREATE INDEX IF NOT EXISTS memories_kind_cursor_idx
    ON memories (memory_kind, created_at, created_at_ns, id);

CREATE TABLE IF NOT EXISTS memory_usage_stats (
    namespace TEXT NOT NULL,
    key TEXT NOT NULL,
    fetch_count INTEGER NOT NULL DEFAULT 0,
    last_fetched_at INTEGER_SECONDS CHECK (last_fetched_at IS NULL OR typeof(last_fetched_at) = 'integer') NOT NULL DEFAULT (unixepoch()),
    last_fetched_at_ns INTEGER CHECK ((last_fetched_at IS NULL AND last_fetched_at_ns IS NULL) OR (last_fetched_at IS NOT NULL AND last_fetched_at_ns IS NOT NULL AND typeof(last_fetched_at_ns) = 'integer' AND last_fetched_at_ns BETWEEN 0 AND 999999999)) NOT NULL DEFAULT 0,
    PRIMARY KEY (namespace, key)
);

CREATE INDEX IF NOT EXISTS idx_memory_usage_last_fetched
    ON memory_usage_stats(last_fetched_at DESC, last_fetched_at_ns DESC);

-- memory_vectors stores vector embeddings for episodic memories.
-- memory_kind is NOT NULL: every indexed vector carries an explicit schema reference.
-- memory_revision is positive and always compared exactly with the primary row.
CREATE TABLE IF NOT EXISTS memory_vectors (
    memory_id TEXT NOT NULL,
    field_name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    policy_attributes TEXT,
    embedding BLOB NOT NULL,
    memory_kind TEXT NOT NULL,  -- canonical schema name at time of indexing
    memory_revision INTEGER NOT NULL CHECK (memory_revision > 0), -- exact primary revision at indexing
    PRIMARY KEY (memory_id, field_name)
);

CREATE INDEX IF NOT EXISTS idx_memory_vectors_namespace
    ON memory_vectors(namespace);
