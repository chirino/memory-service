package sqlitevec

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	sqlitestore "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSQLiteVectorStoreRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DatastoreType:        "sqlite",
		DBURL:                filepath.Join(t.TempDir(), "vectors.db"),
		VectorType:           "sqlite",
		VectorMigrateAtStart: true,
	}
	ctx := config.WithContext(context.Background(), cfg)

	require.NoError(t, (&migrator{}).Migrate(ctx))
	db, _, err := sqlitestore.SharedDB(ctx)
	require.NoError(t, err)
	require.NotNil(t, db)

	store, err := load(ctx)
	require.NoError(t, err)
	if !store.IsEnabled() {
		t.Skip("sqlite vector extension unavailable in this build")
	}

	groupID := uuid.New()
	conversationID := uuid.New()
	entryA := uuid.New()
	entryB := uuid.New()

	require.NoError(t, store.Upsert(ctx, []registryvector.UpsertRequest{
		{
			ConversationGroupID: groupID,
			ConversationID:      conversationID,
			EntryID:             entryA,
			Embedding:           []float32{1, 0, 0},
			ModelName:           "test",
		},
		{
			ConversationGroupID: groupID,
			ConversationID:      conversationID,
			EntryID:             entryB,
			Embedding:           []float32{0, 1, 0},
			ModelName:           "test",
		},
	}))

	results, err := store.Search(ctx, []float32{1, 0, 0}, []uuid.UUID{groupID}, 2)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, entryA, results[0].EntryID)

	require.NoError(t, store.DeleteByConversationGroupID(ctx, groupID))
	results, err = store.Search(ctx, []float32{1, 0, 0}, []uuid.UUID{groupID}, 2)
	require.NoError(t, err)
	require.Len(t, results, 0)
}

func TestDisabledSQLiteVectorStoreNoOps(t *testing.T) {
	t.Parallel()

	store := &Store{enabled: false}

	results, err := store.Search(context.Background(), []float32{1, 0}, []uuid.UUID{uuid.New()}, 10)
	require.NoError(t, err)
	require.Empty(t, results)
	require.NoError(t, store.Upsert(context.Background(), []registryvector.UpsertRequest{{
		ConversationGroupID: uuid.New(),
		ConversationID:      uuid.New(),
		EntryID:             uuid.New(),
		Embedding:           []float32{1, 0},
		ModelName:           "test",
	}}))
	require.NoError(t, store.DeleteByConversationGroupID(context.Background(), uuid.New()))
}
