package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chirino/memory-service/internal/config"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const defaultTTL = 10 * time.Minute

func init() {
	registrycache.Register(registrycache.Plugin{
		Name:   "redis",
		Loader: load,
	})
}

func load(ctx context.Context) (registrycache.MemoryEntriesCache, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil || cfg.RedisURL == "" {
		return nil, fmt.Errorf("redis cache: MEMORY_SERVICE_REDIS_URL is required")
	}
	ttl := cfg.CacheEpochTTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return LoadFromURLWithTTL(ctx, cfg.RedisURL, ttl)
}

// LoadFromURL creates a MemoryEntriesCache from a Redis-compatible URL.
// This is exported so other plugins (e.g. Infinispan RESP) can reuse the implementation.
func LoadFromURL(ctx context.Context, redisURL string) (registrycache.MemoryEntriesCache, error) {
	return LoadFromURLWithTTL(ctx, redisURL, defaultTTL)
}

// LoadFromURLWithTTL creates a cache with an explicit memory-entry TTL.
func LoadFromURLWithTTL(ctx context.Context, redisURL string, ttl time.Duration) (registrycache.MemoryEntriesCache, error) {
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis cache: invalid URL: %w", err)
	}
	return LoadFromOptionsWithTTL(ctx, opts, ttl)
}

// LoadFromOptions creates a MemoryEntriesCache from go-redis Options.
// This allows callers to customize options (e.g. Protocol for RESP2).
func LoadFromOptions(ctx context.Context, opts *goredis.Options) (registrycache.MemoryEntriesCache, error) {
	return LoadFromOptionsWithTTL(ctx, opts, defaultTTL)
}

func LoadFromOptionsWithTTL(ctx context.Context, opts *goredis.Options, ttl time.Duration) (registrycache.MemoryEntriesCache, error) {
	client := goredis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis cache: ping failed: %w", err)
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &redisEntriesCache{client: client, ttl: ttl}, nil
}

type redisEntriesCache struct {
	client *goredis.Client
	ttl    time.Duration
}

func entriesKey(convID uuid.UUID, clientID string) string {
	return fmt.Sprintf("mem-entries:%s:%s", convID.String(), clientID)
}

func (c *redisEntriesCache) Available() bool {
	return true
}

func (c *redisEntriesCache) Get(ctx context.Context, conversationID uuid.UUID, clientID string) (*registrycache.CachedMemoryEntries, error) {
	data, err := c.client.Get(ctx, entriesKey(conversationID, clientID)).Bytes()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cached registrycache.CachedMemoryEntries
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}
	return &cached, nil
}

func (c *redisEntriesCache) Set(ctx context.Context, conversationID uuid.UUID, clientID string, entries registrycache.CachedMemoryEntries, ttl time.Duration) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	if ttl == 0 {
		ttl = c.ttl
	}
	return c.client.Set(ctx, entriesKey(conversationID, clientID), data, ttl).Err()
}

func (c *redisEntriesCache) Remove(ctx context.Context, conversationID uuid.UUID, clientID string) error {
	return c.client.Del(ctx, entriesKey(conversationID, clientID)).Err()
}

var _ registrycache.MemoryEntriesCache = (*redisEntriesCache)(nil)
