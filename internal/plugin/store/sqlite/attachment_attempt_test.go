//go:build !nosqlite

package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func sqliteAttachmentAttemptColumnCount(t *testing.T, ctx context.Context, handle *sharedHandle) int {
	t.Helper()
	var count int
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('attachments') WHERE name = 'attempt_id'").Scan(&count))
	return count
}

func TestFreshSchemaHasAttachmentAttemptIDColumn(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "fresh.db"), DatastoreMigrateAtStart: true, EncryptionDBDisabled: true}
	ctx := config.WithContext(context.Background(), cfg)
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	handle, err := getSharedHandle(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, sqliteAttachmentAttemptColumnCount(t, ctx, handle))

	var columnType string
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx,
		"SELECT type FROM pragma_table_info('attachments') WHERE name = 'attempt_id'").Scan(&columnType))
	require.Equal(t, "TEXT", columnType)
}

func TestAttachmentAttemptIDMigrationPreservesExistingAttachments(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "attempt-upgrade.db"), DatastoreMigrateAtStart: true, EncryptionDBDisabled: true}
	ctx := config.WithContext(context.Background(), cfg)
	handle, err := getSharedHandle(ctx)
	require.NoError(t, err)

	// Build the schema as it existed before attempt_id was added.
	oldSchema := strings.Replace(schemaSQL, "    attempt_id TEXT,\n", "", 1)
	_, err = handle.sqlDB.ExecContext(ctx, oldSchema)
	require.NoError(t, err)
	require.Zero(t, sqliteAttachmentAttemptColumnCount(t, ctx, handle), "fixture must model a database without attempt_id")

	legacyID := uuid.New()
	_, err = handle.sqlDB.ExecContext(ctx,
		"INSERT INTO attachments(id, content_type, user_id, status) VALUES (?, 'text/plain', 'user1', 'ready')", legacyID.String())
	require.NoError(t, err)

	// Running the migration twice proves the ALTER is idempotent.
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	require.Equal(t, 1, sqliteAttachmentAttemptColumnCount(t, ctx, handle))

	var version string
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx, "SELECT value FROM schema_metadata WHERE key='core_schema_version'").Scan(&version))
	require.Equal(t, "2", version, "adding attempt_id must not bump the schema version")

	var nullCount int
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM attachments WHERE id = ? AND attempt_id IS NULL", legacyID.String()).Scan(&nullCount))
	require.Equal(t, 1, nullCount, "a pre-existing row must read NULL for attempt_id")

	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	var legacy, created *model.Attachment
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		legacy, err = store.GetAttachment(txCtx, "user1", "", legacyID)
		if err != nil {
			return err
		}
		created, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{ContentType: "text/plain"})
		return err
	}))
	require.Nil(t, legacy.AttemptID)
	require.Nil(t, created.AttemptID, "a create without a caller-supplied ID must not get an attempt ID")

	var createdNull int
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM attachments WHERE id = ? AND attempt_id IS NULL", created.ID.String()).Scan(&createdNull))
	require.Equal(t, 1, createdNull)
}

func TestAttachmentAttemptIDRoundTripsThroughStoreReads(t *testing.T) {
	store, ctx := newAttachmentTaskSQLiteStore(t)
	handle, err := getSharedHandle(ctx)
	require.NoError(t, err)

	id, attempt := uuid.New(), uuid.New()
	_, err = handle.sqlDB.ExecContext(ctx,
		"INSERT INTO attachments(id, content_type, user_id, status, attempt_id) VALUES (?, 'text/plain', 'user1', 'uploading', ?)",
		id.String(), attempt.String())
	require.NoError(t, err)

	var attachment *model.Attachment
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		attachment, err = store.GetAttachment(txCtx, "user1", "", id)
		return err
	}))
	require.NotNil(t, attachment.AttemptID)
	require.Equal(t, attempt, *attachment.AttemptID)

	var admin *registrystore.AdminAttachment
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		admin, err = store.AdminGetAttachment(txCtx, id)
		return err
	}))
	require.NotNil(t, admin.AttemptID)
	require.Equal(t, attempt, *admin.AttemptID)
}

func TestCreateAttachmentIgnoresCallerSuppliedAttemptID(t *testing.T) {
	store, ctx := newAttachmentTaskSQLiteStore(t)
	handle, err := getSharedHandle(ctx)
	require.NoError(t, err)

	forged := uuid.New()
	var created, read *model.Attachment
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		created, err = store.CreateAttachment(txCtx, "user1", "", model.Attachment{ContentType: "text/plain", AttemptID: &forged})
		if err != nil {
			return err
		}
		read, err = store.GetAttachment(txCtx, "user1", "", created.ID)
		return err
	}))
	require.Nil(t, created.AttemptID, "the returned attachment must not carry a caller-supplied attempt ID")
	require.Nil(t, read.AttemptID)

	var nullCount int
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM attachments WHERE id = ? AND attempt_id IS NULL", created.ID.String()).Scan(&nullCount))
	require.Equal(t, 1, nullCount, "the stored row must have a NULL attempt_id")
}

func TestAttachmentAttemptIDMigrationFromLegacyTextSchema(t *testing.T) {
	cfg := &config.Config{DatastoreType: "sqlite", DBURL: filepath.Join(t.TempDir(), "legacy-text.db"), DatastoreMigrateAtStart: true, EncryptionDBDisabled: true}
	ctx := config.WithContext(context.Background(), cfg)
	handle, err := getSharedHandle(ctx)
	require.NoError(t, err)

	_, err = handle.sqlDB.ExecContext(ctx, legacyTextSchemaSQL)
	require.NoError(t, err)

	// The fixture must be the legacy shape: DATETIME timestamps, no attempt_id, version 2.
	// Those timestamp columns are what force the table-rebuild path ahead of the ALTER.
	var attachmentsDDL string
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name='attachments'").Scan(&attachmentsDDL))
	require.Contains(t, attachmentsDDL, "created_at DATETIME", "fixture must use legacy text timestamps")
	require.Zero(t, sqliteAttachmentAttemptColumnCount(t, ctx, handle), "fixture must model a database without attempt_id")
	var versionBefore string
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx, "SELECT value FROM schema_metadata WHERE key='core_schema_version'").Scan(&versionBefore))
	require.Equal(t, "2", versionBefore)

	_, err = handle.sqlDB.ExecContext(ctx, `INSERT INTO attachments(id, storage_key, filename, content_type, size, sha256, user_id, status, source_url, created_at)
		VALUES ('legacy', 'blob-key', 'doc.txt', 'text/plain', 5, 'abc123', 'alice', 'ready', 'https://example.test/doc.txt', '2026-01-02 03:04:05')`)
	require.NoError(t, err)

	// Running the migration twice proves the rebuild plus ALTER is idempotent.
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))
	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))

	// The rebuild path ran: the timestamp is now numeric and has a companion _ns column.
	var createdType string
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx, "SELECT typeof(created_at) FROM attachments WHERE id = 'legacy'").Scan(&createdType))
	require.Equal(t, "integer", createdType, "the legacy timestamp rebuild must have run")

	require.Equal(t, 1, sqliteAttachmentAttemptColumnCount(t, ctx, handle))

	var versionAfter string
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx, "SELECT value FROM schema_metadata WHERE key='core_schema_version'").Scan(&versionAfter))
	require.Equal(t, "2", versionAfter, "adding attempt_id must not bump the schema version")

	var rowCount int
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM attachments").Scan(&rowCount))
	require.Equal(t, 1, rowCount, "migration must neither drop nor duplicate legacy rows")

	var nullCount int
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM attachments WHERE id = 'legacy' AND attempt_id IS NULL").Scan(&nullCount))
	require.Equal(t, 1, nullCount, "a legacy row must read NULL for attempt_id")

	// Existing fields are intact, read through the raw table.
	var storageKey, filename, contentType, sha, userID, status, sourceURL string
	var size int64
	require.NoError(t, handle.sqlDB.QueryRowContext(ctx,
		"SELECT storage_key, filename, content_type, size, sha256, user_id, status, source_url FROM attachments WHERE id = 'legacy'").
		Scan(&storageKey, &filename, &contentType, &size, &sha, &userID, &status, &sourceURL))
	require.Equal(t, "blob-key", storageKey)
	require.Equal(t, "doc.txt", filename)
	require.Equal(t, "text/plain", contentType)
	require.EqualValues(t, 5, size)
	require.Equal(t, "abc123", sha)
	require.Equal(t, "alice", userID)
	require.Equal(t, "ready", status)
	require.Equal(t, "https://example.test/doc.txt", sourceURL)

	// A create after the migration still works and gets no attempt ID.
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)
	var created *model.Attachment
	require.NoError(t, store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		created, err = store.CreateAttachment(txCtx, "alice", "", model.Attachment{ContentType: "text/plain"})
		return err
	}))
	require.Nil(t, created.AttemptID)
}
