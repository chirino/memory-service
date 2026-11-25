//go:build !nopostgresql

package postgres

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func postgresAttachmentAttemptColumn(t *testing.T, store *PostgresStore) (dataType string, nullable string, count int64) {
	t.Helper()
	type column struct {
		DataType   string
		IsNullable string
	}
	var rows []column
	require.NoError(t, store.db.Raw(
		"SELECT data_type, is_nullable FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'attachments' AND column_name = 'attempt_id'").
		Scan(&rows).Error)
	if len(rows) == 0 {
		return "", "", 0
	}
	return rows[0].DataType, rows[0].IsNullable, int64(len(rows))
}

func TestFreshSchemaHasAttachmentAttemptIDColumn(t *testing.T) {
	store, _ := setupPostgresOutboxStore(t)
	dataType, nullable, count := postgresAttachmentAttemptColumn(t, store)
	require.EqualValues(t, 1, count)
	require.Equal(t, "uuid", dataType)
	require.Equal(t, "YES", nullable)
}

func TestAttachmentAttemptIDMigrationPreservesExistingAttachments(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)

	// Model a database created before attempt_id existed.
	require.NoError(t, store.db.Exec("ALTER TABLE attachments DROP COLUMN IF EXISTS attempt_id").Error)
	_, _, count := postgresAttachmentAttemptColumn(t, store)
	require.Zero(t, count, "fixture must model a database without attempt_id")

	legacyID := uuid.New()
	require.NoError(t, store.db.Exec(
		"INSERT INTO attachments(id, content_type, user_id, status) VALUES (?, 'text/plain', 'user1', 'ready')", legacyID).Error)

	// Running the migration twice proves the ALTER is idempotent.
	require.NoError(t, (&postgresMigrator{}).Migrate(ctx))
	require.NoError(t, (&postgresMigrator{}).Migrate(ctx))

	dataType, nullable, count := postgresAttachmentAttemptColumn(t, store)
	require.EqualValues(t, 1, count, "migration must add attempt_id")
	require.Equal(t, "uuid", dataType)
	require.Equal(t, "YES", nullable)

	var version string
	require.NoError(t, store.db.Raw("SELECT value FROM schema_metadata WHERE key='core_schema_version'").Scan(&version).Error)
	require.Equal(t, "2", version, "adding attempt_id must not bump the schema version")

	var nullCount int64
	require.NoError(t, store.db.Raw("SELECT COUNT(*) FROM attachments WHERE id = ? AND attempt_id IS NULL", legacyID).Scan(&nullCount).Error)
	require.EqualValues(t, 1, nullCount, "a pre-existing row must read NULL for attempt_id")

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

	var createdNull int64
	require.NoError(t, store.db.Raw("SELECT COUNT(*) FROM attachments WHERE id = ? AND attempt_id IS NULL", created.ID).Scan(&createdNull).Error)
	require.EqualValues(t, 1, createdNull)
}

func TestAttachmentAttemptIDRoundTripsThroughStoreReads(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)

	id, attempt := uuid.New(), uuid.New()
	require.NoError(t, store.db.Exec(
		"INSERT INTO attachments(id, content_type, user_id, status, attempt_id) VALUES (?, 'text/plain', 'user1', 'uploading', ?)",
		id, attempt).Error)

	var attachment *model.Attachment
	require.NoError(t, store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		attachment, err = store.GetAttachment(txCtx, "user1", "", id)
		return err
	}))
	require.NotNil(t, attachment.AttemptID)
	require.Equal(t, attempt, *attachment.AttemptID)

	admin, err := store.AdminGetAttachment(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, admin.AttemptID)
	require.Equal(t, attempt, *admin.AttemptID)
}

func TestCreateAttachmentIgnoresCallerSuppliedAttemptID(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)

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

	var nullCount int64
	require.NoError(t, store.db.Raw("SELECT COUNT(*) FROM attachments WHERE id = ? AND attempt_id IS NULL", created.ID).Scan(&nullCount).Error)
	require.EqualValues(t, 1, nullCount, "the stored row must have a NULL attempt_id")
}
