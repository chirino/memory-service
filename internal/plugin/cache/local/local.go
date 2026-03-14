package local

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	"github.com/dgraph-io/ristretto/v2"
	"github.com/google/uuid"
)

const defaultTTL = 10 * time.Minute

func init() {
	registrycache.Register(registrycache.Plugin{
		Name:   "local",
		Loader: load,
	})
}

func load(ctx context.Context) (registrycache.MemoryEntriesCache, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("local cache: config is required")
	}

	ttl := cfg.CacheEpochTTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if cfg.CacheLocalMaxBytes <= 0 {
		return nil, fmt.Errorf("local cache: MEMORY_SERVICE_CACHE_LOCAL_MAX_BYTES must be > 0")
	}
	if cfg.CacheLocalNumCounters <= 0 {
		return nil, fmt.Errorf("local cache: MEMORY_SERVICE_CACHE_LOCAL_NUM_COUNTERS must be > 0")
	}
	if cfg.CacheLocalBufferItems <= 0 {
		return nil, fmt.Errorf("local cache: MEMORY_SERVICE_CACHE_LOCAL_BUFFER_ITEMS must be > 0")
	}

	cache, err := ristretto.NewCache(&ristretto.Config[string, []byte]{
		NumCounters: cfg.CacheLocalNumCounters,
		MaxCost:     cfg.CacheLocalMaxBytes,
		BufferItems: cfg.CacheLocalBufferItems,
	})
	if err != nil {
		return nil, fmt.Errorf("local cache: initialize ristretto: %w", err)
	}

	return &entriesCache{
		cache: cache,
		ttl:   ttl,
	}, nil
}

type entriesCache struct {
	cache *ristretto.Cache[string, []byte]
	ttl   time.Duration
}

func cacheKey(conversationID uuid.UUID, clientID string) string {
	return fmt.Sprintf("mem-entries:%s:%s", conversationID.String(), strings.TrimSpace(clientID))
}

func (c *entriesCache) Available() bool {
	return true
}

func (c *entriesCache) Get(_ context.Context, conversationID uuid.UUID, clientID string) (*registrycache.CachedMemoryEntries, error) {
	key := cacheKey(conversationID, clientID)
	data, ok := c.cache.Get(key)
	if !ok || len(data) == 0 {
		return nil, nil
	}
	var cached registrycache.CachedMemoryEntries
	if err := json.Unmarshal(data, &cached); err != nil {
		c.cache.Del(key)
		c.cache.Wait()
		return nil, err
	}
	return &cached, nil
}

func (c *entriesCache) Set(_ context.Context, conversationID uuid.UUID, clientID string, entries registrycache.CachedMemoryEntries, ttl time.Duration) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	if ttl == 0 {
		ttl = c.ttl
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	c.cache.SetWithTTL(cacheKey(conversationID, clientID), data, int64(len(data)), ttl)
	c.cache.Wait()
	return nil
}

func (c *entriesCache) Remove(_ context.Context, conversationID uuid.UUID, clientID string) error {
	c.cache.Del(cacheKey(conversationID, clientID))
	c.cache.Wait()
	return nil
}

var _ registrycache.MemoryEntriesCache = (*entriesCache)(nil)
