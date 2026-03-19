//go:build !nopostgresql

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPostgresEpisodicStore(t *testing.T) (registryepisodic.EpisodicStore, context.Context) {
	t.Helper()

	dbURL := testpg.StartPostgres(t)

	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "postgres"
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)

	_ = postgres.ForceImport

	err := registrymigrate.RunAll(ctx)
	require.NoError(t, err)

	loader, err := registryepisodic.Select("postgres")
	require.NoError(t, err)

	store, err := loader(ctx)
	require.NoError(t, err)
	return store, ctx
}

func TestIncrementMemoryLoads_DedupesPerRequest_Postgres(t *testing.T) {
	store, ctx := setupPostgresEpisodicStore(t)

	ns := []string{"user", "alice", "prefs"}
	key := "theme"
	otherKey := "lang"

	t1 := time.Now().UTC().Truncate(time.Microsecond)
	err := store.IncrementMemoryLoads(ctx, []registryepisodic.MemoryKey{
		{Namespace: ns, Key: key},
		{Namespace: ns, Key: key}, // duplicate in same request
		{Namespace: ns, Key: otherKey},
	}, t1)
	require.NoError(t, err)

	u1, err := store.GetMemoryUsage(ctx, ns, key)
	require.NoError(t, err)
	require.NotNil(t, u1)
	assert.Equal(t, int64(1), u1.FetchCount)
	assert.WithinDuration(t, t1, u1.LastFetchedAt.UTC(), time.Second)

	uOther, err := store.GetMemoryUsage(ctx, ns, otherKey)
	require.NoError(t, err)
	require.NotNil(t, uOther)
	assert.Equal(t, int64(1), uOther.FetchCount)

	t2 := t1.Add(2 * time.Minute)
	err = store.IncrementMemoryLoads(ctx, []registryepisodic.MemoryKey{
		{Namespace: ns, Key: key},
	}, t2)
	require.NoError(t, err)

	u1, err = store.GetMemoryUsage(ctx, ns, key)
	require.NoError(t, err)
	require.NotNil(t, u1)
	assert.Equal(t, int64(2), u1.FetchCount)
	assert.WithinDuration(t, t2, u1.LastFetchedAt.UTC(), time.Second)
}

func TestListTopMemoryUsage_SortAndPrefix_Postgres(t *testing.T) {
	store, ctx := setupPostgresEpisodicStore(t)

	aliceNS := []string{"user", "alice", "notes"}
	bobNS := []string{"user", "bob", "notes"}

	base := time.Now().UTC().Truncate(time.Microsecond)

	require.NoError(t, store.IncrementMemoryLoads(ctx, []registryepisodic.MemoryKey{{Namespace: aliceNS, Key: "k1"}}, base))
	require.NoError(t, store.IncrementMemoryLoads(ctx, []registryepisodic.MemoryKey{{Namespace: aliceNS, Key: "k1"}}, base.Add(time.Minute)))
	require.NoError(t, store.IncrementMemoryLoads(ctx, []registryepisodic.MemoryKey{{Namespace: aliceNS, Key: "k2"}}, base.Add(2*time.Minute)))
	require.NoError(t, store.IncrementMemoryLoads(ctx, []registryepisodic.MemoryKey{{Namespace: bobNS, Key: "k3"}}, base.Add(3*time.Minute)))
	require.NoError(t, store.IncrementMemoryLoads(ctx, []registryepisodic.MemoryKey{{Namespace: aliceNS, Key: "k2"}}, base.Add(4*time.Minute)))

	byCount, err := store.ListTopMemoryUsage(ctx, registryepisodic.ListTopMemoryUsageRequest{
		Prefix: []string{"user", "alice"},
		Sort:   registryepisodic.MemoryUsageSortFetchCount,
		Limit:  10,
	})
	require.NoError(t, err)
	require.Len(t, byCount, 2)
	assert.Equal(t, "k2", byCount[0].Key) // tie broken by last_fetched_at DESC
	assert.Equal(t, int64(2), byCount[0].Usage.FetchCount)
	assert.Equal(t, aliceNS, byCount[0].Namespace)
	assert.Equal(t, "k1", byCount[1].Key)
	assert.Equal(t, int64(2), byCount[1].Usage.FetchCount)

	byFetched, err := store.ListTopMemoryUsage(ctx, registryepisodic.ListTopMemoryUsageRequest{
		Prefix: []string{"user", "alice"},
		Sort:   registryepisodic.MemoryUsageSortLastFetchedAt,
		Limit:  10,
	})
	require.NoError(t, err)
	require.Len(t, byFetched, 2)
	assert.Equal(t, "k2", byFetched[0].Key)
	assert.Equal(t, "k1", byFetched[1].Key)
}
