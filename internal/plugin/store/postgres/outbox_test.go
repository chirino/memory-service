//go:build !nopostgresql

package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	localbus "github.com/chirino/memory-service/internal/plugin/eventbus/local"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/chirino/memory-service/internal/txscope"
	"github.com/google/uuid"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func setupPostgresOutboxStore(t *testing.T) (*PostgresStore, context.Context) {
	t.Helper()

	dbURL := testpg.StartPostgres(t)
	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "postgres"
	cfg.OutboxEnabled = true
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)

	require.NoError(t, registrymigrate.RunAll(ctx))

	loader, err := registrystore.Select("postgres")
	require.NoError(t, err)

	store, err := loader(ctx)
	require.NoError(t, err)

	pgStore, ok := store.(*PostgresStore)
	require.True(t, ok)
	return pgStore, ctx
}

func TestPostgresOutboxCursorRoundTrip(t *testing.T) {
	lsn := pglogrepl.LSN(0x16B3740)
	cursor := formatPostgresOutboxCursor(lsn, 42)

	parsedLSN, parsedTxSeq, err := parsePostgresOutboxCursor(cursor)
	require.NoError(t, err)
	require.Equal(t, lsn, parsedLSN)
	require.Equal(t, int64(42), parsedTxSeq)
}

func TestPostgresOutboxReplayUsesCommitOrder(t *testing.T) {
	store, ctx := setupPostgresOutboxStore(t)

	bus := localbus.New(16)
	t.Cleanup(func() { _ = bus.Close() })
	require.NoError(t, store.StartOutboxRelay(ctx, bus))

	groupID := uuid.New()
	firstPayload, err := json.Marshal(map[string]any{
		"conversation":       uuid.New().String(),
		"conversation_group": groupID.String(),
	})
	require.NoError(t, err)
	secondPayload, err := json.Marshal(map[string]any{
		"conversation":       uuid.New().String(),
		"conversation_group": groupID.String(),
	})
	require.NoError(t, err)

	tx1 := store.db.Begin()
	require.NoError(t, tx1.Error)
	defer tx1.Rollback()
	_, err = store.AppendOutboxEvents(withScope(ctx, tx1, txscope.IntentWrite), []registrystore.OutboxWrite{{
		Event: "created",
		Kind:  "conversation",
		Data:  firstPayload,
	}})
	require.NoError(t, err)

	tx2 := store.db.Begin()
	require.NoError(t, tx2.Error)
	defer tx2.Rollback()
	_, err = store.AppendOutboxEvents(withScope(ctx, tx2, txscope.IntentWrite), []registrystore.OutboxWrite{{
		Event: "updated",
		Kind:  "conversation",
		Data:  secondPayload,
	}})
	require.NoError(t, err)

	require.NoError(t, tx2.Commit().Error)
	require.NoError(t, tx1.Commit().Error)

	var page *registrystore.OutboxPage
	require.Eventually(t, func() bool {
		err := store.InReadTx(ctx, func(txCtx context.Context) error {
			var listErr error
			page, listErr = store.ListOutboxEvents(txCtx, registrystore.OutboxQuery{
				AfterCursor: "start",
				Limit:       10,
			})
			return listErr
		})
		return err == nil && page != nil && len(page.Events) == 2
	}, 15*time.Second, 200*time.Millisecond)

	require.Len(t, page.Events, 2)
	require.JSONEq(t, string(secondPayload), string(page.Events[0].Data))
	require.JSONEq(t, string(firstPayload), string(page.Events[1].Data))
	require.Contains(t, page.Events[0].Cursor, ":")
	require.Contains(t, page.Events[1].Cursor, ":")
}

func TestExplainOutboxRelaySetupErrorWalLevel(t *testing.T) {
	err := explainOutboxRelaySetupError("create replication slot", &pgconn.PgError{
		Code:    "55000",
		Message: `logical decoding requires "wal_level" >= "logical"`,
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "PostgreSQL is not configured for logical replication")
	require.ErrorContains(t, err, "wal_level=logical")
	require.ErrorContains(t, err, "max_replication_slots >= 1")
	require.ErrorContains(t, err, "max_wal_senders >= 1")
	require.ErrorContains(t, err, "disable MEMORY_SERVICE_OUTBOX_ENABLED")
}
