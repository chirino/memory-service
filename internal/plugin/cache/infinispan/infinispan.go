// Package infinispan provides a cache plugin that connects to Infinispan
// via its RESP (Redis protocol) endpoint, reusing the Redis cache implementation.
package infinispan

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/plugin/cache/redis"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	goredis "github.com/redis/go-redis/v9"
)

func init() {
	registrycache.Register(registrycache.Plugin{
		Name:   "infinispan",
		Loader: load,
	})
}

func load(ctx context.Context) (registrycache.MemoryEntriesCache, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil || cfg.InfinispanHost == "" {
		return nil, fmt.Errorf("infinispan cache: MEMORY_SERVICE_INFINISPAN_HOST is required")
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, cfg.InfinispanStartupTimeout)
	defer cancel()

	// Infinispan's RESP endpoint does not support the RESP3 HELLO command,
	// so we must use Protocol 2 (RESP2) to avoid a handshake hang.
	opts := &goredis.Options{
		Addr:     cfg.InfinispanHost,
		Username: cfg.InfinispanUsername,
		Password: cfg.InfinispanPassword,
		Protocol: 2,
	}
	return redis.LoadFromOptionsWithTTL(timeoutCtx, opts, cfg.CacheEpochTTL)
}
