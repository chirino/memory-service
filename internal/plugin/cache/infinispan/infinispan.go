//go:build !noinfinispan

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
	"github.com/urfave/cli/v3"
)

func init() {
	registrycache.Register(registrycache.Plugin{
		Name:   "infinispan",
		Loader: load,
		Flags: func(cfg *config.Config) []cli.Flag {
			return []cli.Flag{
				&cli.StringFlag{
					Name:        "infinispan-host",
					Category:    "Cache:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_INFINISPAN_HOST"),
					Destination: &cfg.InfinispanHost,
					Usage:       "Infinispan RESP host:port (e.g. localhost:11222)",
				},
				&cli.StringFlag{
					Name:        "infinispan-username",
					Category:    "Cache:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_INFINISPAN_USERNAME"),
					Destination: &cfg.InfinispanUsername,
					Usage:       "Infinispan username",
				},
				&cli.StringFlag{
					Name:        "infinispan-password",
					Category:    "Cache:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_INFINISPAN_PASSWORD"),
					Destination: &cfg.InfinispanPassword,
					Usage:       "Infinispan password",
				},
			}
		},
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
