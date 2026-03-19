//go:build !noredis

package redis

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/config"
	registrycache "github.com/chirino/memory-service/internal/registry/cache"
	"github.com/urfave/cli/v3"
)

func init() {
	registrycache.Register(registrycache.Plugin{
		Name:   "redis",
		Loader: load,
		Flags: func(cfg *config.Config) []cli.Flag {
			return []cli.Flag{
				&cli.StringFlag{
					Name:        "redis-hosts",
					Category:    "Cache:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_REDIS_HOSTS"),
					Destination: &cfg.RedisURL,
					Usage:       "Redis connection URL",
				},
			}
		},
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
