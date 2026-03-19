//go:build !nosqlite && sqlite_fts5

package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newSQLiteEpisodicStore(t *testing.T) (registryepisodic.EpisodicStore, context.Context) {
	t.Helper()

	cfg := &config.Config{
		DatastoreType:           "sqlite",
		DBURL:                   filepath.Join(t.TempDir(), "memory.db"),
		DatastoreMigrateAtStart: true,
		EncryptionDBDisabled:    true,
	}
	ctx := config.WithContext(context.Background(), cfg)

	require.NoError(t, (&sqliteMigrator{}).Migrate(ctx))

	loader, err := registryepisodic.Select("sqlite")
	require.NoError(t, err)

	store, err := loader(ctx)
	require.NoError(t, err)
	return store, ctx
}

func TestSQLiteEpisodicStoreRequiresScope(t *testing.T) {
	t.Parallel()

	store, ctx := newSQLiteEpisodicStore(t)

	require.PanicsWithError(t,
		"sqlite: sqlite episodic store requires InReadTx or InWriteTx scope",
		func() {
			_, _ = store.GetMemory(ctx, []string{"users", "alice"}, "profile")
		},
	)
}

func TestSQLiteEpisodicStoreCRUDUsageSearchAndEvents(t *testing.T) {
	t.Parallel()

	store, ctx := newSQLiteEpisodicStore(t)

	var firstID string
	var updatedID string
	err := store.InWriteTx(ctx, func(writeCtx context.Context) error {
		result, err := store.PutMemory(writeCtx, registryepisodic.PutMemoryRequest{
			Namespace:        []string{"users", "alice"},
			Key:              "profile",
			Value:            map[string]interface{}{"name": "Alice", "role": "admin"},
			Index:            map[string]string{"summary": "Alice profile"},
			TTLSeconds:       60,
			PolicyAttributes: map[string]interface{}{"tenant": "acme", "score": 10.0, "enabled": true},
		})
		if err != nil {
			return err
		}
		firstID = result.ID.String()

		result, err = store.PutMemory(writeCtx, registryepisodic.PutMemoryRequest{
			Namespace:        []string{"users", "alice"},
			Key:              "profile",
			Value:            map[string]interface{}{"name": "Alice", "role": "owner"},
			Index:            map[string]string{"summary": "Alice updated profile"},
			PolicyAttributes: map[string]interface{}{"tenant": "acme", "score": 12.0, "enabled": true},
		})
		if err != nil {
			return err
		}
		updatedID = result.ID.String()
		return nil
	})
	require.NoError(t, err)
	require.NotEqual(t, firstID, updatedID)

	err = store.InWriteTx(ctx, func(writeCtx context.Context) error {
		return store.IncrementMemoryLoads(writeCtx, []registryepisodic.MemoryKey{{
			Namespace: []string{"users", "alice"},
			Key:       "profile",
		}}, time.Now().UTC())
	})
	require.NoError(t, err)

	err = store.InReadTx(ctx, func(readCtx context.Context) error {
		item, err := store.GetMemory(readCtx, []string{"users", "alice"}, "profile")
		require.NoError(t, err)
		require.NotNil(t, item)
		require.Equal(t, "owner", item.Value["role"])
		require.Equal(t, "acme", item.Attributes["tenant"])

		usage, err := store.GetMemoryUsage(readCtx, []string{"users", "alice"}, "profile")
		require.NoError(t, err)
		require.NotNil(t, usage)
		require.EqualValues(t, 1, usage.FetchCount)

		items, err := store.SearchMemories(readCtx, []string{"users"}, map[string]interface{}{
			"tenant":  "acme",
			"enabled": true,
			"score":   map[string]interface{}{"gte": 11.0},
		}, 10, 0)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, "profile", items[0].Key)

		events, err := store.ListMemoryEvents(readCtx, registryepisodic.ListEventsRequest{
			NamespacePrefix: []string{"users"},
			Limit:           10,
		})
		require.NoError(t, err)
		require.Len(t, events.Events, 2)
		require.Equal(t, registryepisodic.EventKindAdd, events.Events[0].Kind)
		require.Equal(t, registryepisodic.EventKindUpdate, events.Events[1].Kind)
		require.Empty(t, events.AfterCursor)
		return nil
	})
	require.NoError(t, err)

	err = store.InWriteTx(ctx, func(writeCtx context.Context) error {
		return store.DeleteMemory(writeCtx, []string{"users", "alice"}, "profile")
	})
	require.NoError(t, err)

	err = store.InReadTx(ctx, func(readCtx context.Context) error {
		item, err := store.GetMemory(readCtx, []string{"users", "alice"}, "profile")
		require.NoError(t, err)
		require.Nil(t, item)

		events, err := store.ListMemoryEvents(readCtx, registryepisodic.ListEventsRequest{
			NamespacePrefix: []string{"users"},
			Limit:           10,
		})
		require.NoError(t, err)
		require.Len(t, events.Events, 3)
		require.Equal(t, registryepisodic.EventKindDelete, events.Events[2].Kind)
		require.Nil(t, events.Events[2].Value)
		return nil
	})
	require.NoError(t, err)
}

func TestSQLiteEpisodicStoreVectorSearch(t *testing.T) {
	t.Parallel()

	store, ctx := newSQLiteEpisodicStore(t)

	var firstID, secondID string
	err := store.InWriteTx(ctx, func(writeCtx context.Context) error {
		result, err := store.PutMemory(writeCtx, registryepisodic.PutMemoryRequest{
			Namespace:        []string{"users", "alice"},
			Key:              "first",
			Value:            map[string]interface{}{"name": "First"},
			Index:            map[string]string{"summary": "first"},
			PolicyAttributes: map[string]interface{}{"tenant": "acme", "rank": 9.0},
		})
		if err != nil {
			return err
		}
		firstID = result.ID.String()

		result, err = store.PutMemory(writeCtx, registryepisodic.PutMemoryRequest{
			Namespace:        []string{"users", "alice"},
			Key:              "second",
			Value:            map[string]interface{}{"name": "Second"},
			Index:            map[string]string{"summary": "second"},
			PolicyAttributes: map[string]interface{}{"tenant": "other", "rank": 1.0},
		})
		if err != nil {
			return err
		}
		secondID = result.ID.String()
		return nil
	})
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID)

	var pending []registryepisodic.PendingMemory
	err = store.InReadTx(ctx, func(readCtx context.Context) error {
		var err error
		pending, err = store.FindMemoriesPendingIndexing(readCtx, 10)
		return err
	})
	require.NoError(t, err)
	require.Len(t, pending, 2)

	embeddings := map[string][]float32{
		pending[0].ID.String(): []float32{1, 0},
		pending[1].ID.String(): []float32{-1, 0},
	}
	if pending[0].ID.String() != firstID {
		embeddings[pending[0].ID.String()] = []float32{-1, 0}
		embeddings[pending[1].ID.String()] = []float32{1, 0}
	}

	err = store.InWriteTx(ctx, func(writeCtx context.Context) error {
		for _, item := range pending {
			if err := store.UpsertMemoryVectors(writeCtx, []registryepisodic.MemoryVectorUpsert{{
				MemoryID:         item.ID,
				FieldName:        "summary",
				Namespace:        item.Namespace,
				PolicyAttributes: item.PolicyAttributes,
				Embedding:        embeddings[item.ID.String()],
			}}); err != nil {
				return err
			}
			if err := store.SetMemoryIndexedAt(writeCtx, item.ID, time.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)

	namespacePrefix, err := episodic.EncodeNamespace([]string{"users", "alice"}, 0)
	require.NoError(t, err)

	err = store.InReadTx(ctx, func(readCtx context.Context) error {
		results, err := store.SearchMemoryVectors(readCtx, namespacePrefix, []float32{1, 0}, map[string]interface{}{
			"tenant": "acme",
			"rank":   map[string]interface{}{"gte": 5.0},
		}, 10)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, firstID, results[0].MemoryID.String())
		require.Greater(t, results[0].Score, 0.99)

		items, err := store.GetMemoriesByIDs(readCtx, []uuid.UUID{results[0].MemoryID})
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, "first", items[0].Key)
		return nil
	})
	require.NoError(t, err)
}
