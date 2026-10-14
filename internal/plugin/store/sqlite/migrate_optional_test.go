//go:build !nosqlite

package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteMigratorRejectsVersionOneAndRequiresReset(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "memory.db")
	legacy, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = legacy.Exec(`
		CREATE TABLE schema_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO schema_metadata(key, value) VALUES ('core_schema_version', '1');
	`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	cfg := &config.Config{DatastoreType: "sqlite", DBURL: dbPath, DatastoreMigrateAtStart: true}
	err = (&sqliteMigrator{}).Migrate(config.WithContext(context.Background(), cfg))
	require.ErrorContains(t, err, "reset the datastore")
}

func TestSQLiteMigratorCreatesCoreTablesWithoutOptionalExtensions(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "missing", "parent", "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)

	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))

	db, _, err := SharedDB(ctx)
	require.NoError(t, err)
	var schemaVersion string
	require.NoError(t, db.Raw("SELECT value FROM schema_metadata WHERE key = ?", "core_schema_version").Scan(&schemaVersion).Error)
	require.Equal(t, "2", schemaVersion)

	for _, table := range []string{
		"schema_metadata",
		"conversation_groups",
		"conversations",
		"conversation_ancestry",
		"conversation_memberships",
		"entries",
		"conversation_ownership_transfers",
		"tasks",
		"attachments",
		"memories",
		"memory_usage_stats",
		"memory_vectors",
	} {
		var count int64
		err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE (type = 'table' OR type = 'view') AND name = ?", table).Scan(&count).Error
		require.NoError(t, err, table)
		require.Equal(t, int64(1), count, table)
	}
}

//go:embed testdata/schema_v2_text.sql
var legacyTextSchemaSQL string

func TestSQLiteMigratorBackfillsEntryCreatedAtNanoseconds(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memory.db")
	legacy, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	legacySchema := strings.Replace(legacyTextSchemaSQL, "    created_at_unix_ms INTEGER,\n", "", 1)
	require.NotEqual(t, legacyTextSchemaSQL, legacySchema)
	_, err = legacy.Exec(legacySchema)
	require.NoError(t, err)

	createdAt := time.Date(2026, time.January, 2, 3, 4, 5, 678999999, time.FixedZone("UTC-5", -5*60*60))
	_, err = legacy.Exec(`
		INSERT INTO conversation_groups (id) VALUES ('group-1');
		INSERT INTO conversations (
			id, owner_user_id, client_id, conversation_group_id
		) VALUES ('conversation-1', 'alice', 'test-client', 'group-1');
		INSERT INTO entries (
			id, conversation_id, conversation_group_id, channel, content_type, content, created_at
		) VALUES ('entry-1', 'conversation-1', 'group-1', 'history', 'history', '[]', ?);
	`, createdAt)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   dbPath,
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))

	db, _, err := SharedDB(ctx)
	require.NoError(t, err)
	var seconds, nanos int64
	require.NoError(t, db.Raw(
		"SELECT CAST(created_at AS INTEGER) AS raw_seconds, CAST(created_at_ns AS INTEGER) AS raw_nanos FROM entries WHERE id = ?",
		"entry-1",
	).Row().Scan(&seconds, &nanos))
	require.Equal(t, createdAt.Unix(), seconds)
	require.Equal(t, int64(createdAt.Nanosecond()), nanos)

	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	var secondsAfterSecondMigration, nanosAfterSecondMigration int64
	require.NoError(t, db.Raw(
		"SELECT CAST(created_at AS INTEGER) AS raw_seconds, CAST(created_at_ns AS INTEGER) AS raw_nanos FROM entries WHERE id = ?",
		"entry-1",
	).Row().Scan(&secondsAfterSecondMigration, &nanosAfterSecondMigration))
	require.Equal(t, seconds, secondsAfterSecondMigration)
	require.Equal(t, nanos, nanosAfterSecondMigration)
}

func TestSQLiteMigratorMakesStartedByConversationASoftReference(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memory.db")
	legacy, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	legacy.SetMaxOpenConns(1)
	legacySchema := strings.Replace(
		legacyTextSchemaSQL,
		"started_by_conversation_id TEXT,",
		"started_by_conversation_id TEXT REFERENCES conversations(id) ON DELETE CASCADE,",
		1,
	)
	require.NotEqual(t, legacyTextSchemaSQL, legacySchema)
	_, err = legacy.Exec(legacySchema)
	require.NoError(t, err)
	_, err = legacy.Exec(`
		INSERT INTO conversation_groups (id) VALUES ('parent-group'), ('child-group');
		INSERT INTO conversations (
			id, owner_user_id, client_id, conversation_group_id
		) VALUES ('parent-conversation', 'alice', 'test-client', 'parent-group');
		INSERT INTO conversations (
			id, owner_user_id, client_id, conversation_group_id, started_by_conversation_id
		) VALUES ('child-conversation', 'alice', 'test-client', 'child-group', 'parent-conversation');
	`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   dbPath,
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))

	db, _, err := SharedDB(ctx)
	require.NoError(t, err)
	require.NoError(t, db.Exec("DELETE FROM conversation_groups WHERE id = ?", "parent-group").Error)

	var childCount int64
	require.NoError(t, db.Raw(
		"SELECT COUNT(*) FROM conversations WHERE id = ? AND started_by_conversation_id = ?",
		"child-conversation", "parent-conversation",
	).Scan(&childCount).Error)
	require.Equal(t, int64(1), childCount)

	var startedByForeignKeys int64
	require.NoError(t, db.Raw(`
		SELECT COUNT(*)
		FROM pragma_foreign_key_list('conversations')
		WHERE "from" = 'started_by_conversation_id'
	`).Scan(&startedByForeignKeys).Error)
	require.Zero(t, startedByForeignKeys)
}

func TestSQLiteMigratorDropsObsoleteMemoryKindDefaults(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))

	db, _, err := SharedDB(ctx)
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE memory_kind_defaults (
		family TEXT PRIMARY KEY,
		memory_kind TEXT NOT NULL
	)`).Error)

	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	var count int64
	require.NoError(t, db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		"memory_kind_defaults",
	).Scan(&count).Error)
	require.Zero(t, count)
}

func TestSQLiteMigratorRejectsEarlierSchemaVersion(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))

	db, _, err := SharedDB(ctx)
	require.NoError(t, err)
	require.NoError(t, db.Exec("UPDATE schema_metadata SET value = ? WHERE key = ?", "110", "core_schema_version").Error)

	err = (&sqliteMigrator{}).Migrate(ctx)
	require.ErrorContains(t, err, "unsupported SQLite schema version 110")
	require.ErrorContains(t, err, "schema version 1")
}

func TestSQLiteSearchReturnsEmptyWhenFTS5IsUnavailable(t *testing.T) {
	t.Parallel()

	store := &SQLiteStore{handle: &sharedHandle{fts5Enabled: false}}

	results, err := store.SearchEntries(context.Background(), "user-1", "hello world", nil, 10, false, false)
	require.NoError(t, err)
	require.Empty(t, results.Data)
	require.Nil(t, results.AfterCursor)

	adminResults, err := store.AdminSearchEntries(context.Background(), registrystore.AdminSearchQuery{
		Query: "hello world",
		Limit: 10,
	})
	require.NoError(t, err)
	require.Empty(t, adminResults.Data)
	require.Nil(t, adminResults.AfterCursor)
}

func TestSQLiteEpisodicVectorsNoOpWhenExtensionUnavailable(t *testing.T) {
	t.Parallel()

	store := &sqliteEpisodicStore{handle: &sharedHandle{vecEnabled: false}}

	require.NoError(t, store.UpsertMemoryVectors(context.Background(), []registryepisodic.MemoryVectorUpsert{{
		MemoryID:  uuid.New(),
		FieldName: "body",
		Namespace: "users\x1e123",
		Embedding: []float32{1, 0},
	}}))
	require.NoError(t, store.DeleteMemoryVectors(context.Background(), uuid.New()))

	results, err := store.SearchMemoryVectors(context.Background(), "users\x1e123", []float32{1, 0}, registryepisodic.AttributeFilter{}, "", 10, registryepisodic.ArchiveFilterExclude)
	require.ErrorIs(t, err, registryepisodic.ErrSemanticSearchUnavailable)
	require.Empty(t, results)
}
