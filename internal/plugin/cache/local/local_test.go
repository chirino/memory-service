package local

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestLoadAndRoundTrip(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CacheLocalMaxBytes = 1 << 20
	cache, err := load(config.WithContext(context.Background(), &cfg))
	require.NoError(t, err)

	conversationID := uuid.New()
	expected := sampleEntries()
	err = cache.Set(context.Background(), conversationID, "agent-a", expected, 0)
	require.NoError(t, err)

	got, err := cache.Get(context.Background(), conversationID, "agent-a")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, expected, *got)
}

func TestTTLExpiry(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CacheLocalMaxBytes = 1 << 20
	cache, err := load(config.WithContext(context.Background(), &cfg))
	require.NoError(t, err)

	conversationID := uuid.New()
	err = cache.Set(context.Background(), conversationID, "agent-a", sampleEntries(), 50*time.Millisecond)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		got, getErr := cache.Get(context.Background(), conversationID, "agent-a")
		require.NoError(t, getErr)
		return got == nil
	}, time.Second, 25*time.Millisecond)
}

func TestRemove(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CacheLocalMaxBytes = 1 << 20
	cache, err := load(config.WithContext(context.Background(), &cfg))
	require.NoError(t, err)

	conversationID := uuid.New()
	err = cache.Set(context.Background(), conversationID, "agent-a", sampleEntries(), 0)
	require.NoError(t, err)
	err = cache.Remove(context.Background(), conversationID, "agent-a")
	require.NoError(t, err)

	got, err := cache.Get(context.Background(), conversationID, "agent-a")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestConcurrentAccess(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CacheLocalMaxBytes = 1 << 20
	cache, err := load(config.WithContext(context.Background(), &cfg))
	require.NoError(t, err)

	conversationID := uuid.New()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(clientID string) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				setErr := cache.Set(context.Background(), conversationID, clientID, sampleEntries(), 0)
				require.NoError(t, setErr)
				_, getErr := cache.Get(context.Background(), conversationID, clientID)
				require.NoError(t, getErr)
			}
		}(uuid.NewString())
	}
	wg.Wait()
}

func TestOversizedEntryIsSkipped(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.CacheLocalMaxBytes = 64
	cache, err := load(config.WithContext(context.Background(), &cfg))
	require.NoError(t, err)

	conversationID := uuid.New()
	err = cache.Set(context.Background(), conversationID, "agent-a", registrycache.CachedMemoryEntries{
		Entries: []model.Entry{{
			ID:             uuid.New(),
			ConversationID: conversationID,
			Channel:        model.ChannelContext,
			ContentType:    "test.v1",
			Content:        []byte(`["this payload is intentionally much larger than sixty four bytes"]`),
		}},
	}, 0)
	require.NoError(t, err)

	got, err := cache.Get(context.Background(), conversationID, "agent-a")
	require.NoError(t, err)
	require.Nil(t, got)
}

func sampleEntries() registrycache.CachedMemoryEntries {
	epoch := int64(7)
	return registrycache.CachedMemoryEntries{
		Epoch: &epoch,
		Entries: []model.Entry{{
			ID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ConversationID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			Channel:        model.ChannelContext,
			ContentType:    "test.v1",
			Content:        []byte(`[{"type":"text","text":"hello"}]`),
		}},
	}
}
