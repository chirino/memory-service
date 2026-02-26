package resumer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chirino/memory-service/internal/config"
	goredis "github.com/redis/go-redis/v9"
)

const locatorKeyPrefix = "resumer:locator:"

// LocatorStore stores and resolves response recorder ownership across service instances.
type LocatorStore interface {
	Available() bool
	Get(ctx context.Context, conversationID string) (*Locator, error)
	Upsert(ctx context.Context, conversationID string, locator Locator, ttl time.Duration) error
	Remove(ctx context.Context, conversationID string) error
	Exists(ctx context.Context, conversationID string) (bool, error)
}

func NewLocatorStore(ctx context.Context, cfg *config.Config) (LocatorStore, error) {
	if cfg == nil {
		return noopLocatorStore{}, nil
	}

	switch strings.ToLower(strings.TrimSpace(cfg.CacheType)) {
	case "", "none":
		return noopLocatorStore{}, nil
	case "redis":
		if strings.TrimSpace(cfg.RedisURL) == "" {
			return nil, fmt.Errorf("response resumer: redis cache enabled but MEMORY_SERVICE_REDIS_URL is not set")
		}
		opts, err := goredis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("response resumer: invalid redis url: %w", err)
		}
		return newRedisLocatorStore(ctx, opts)
	case "infinispan":
		if strings.TrimSpace(cfg.InfinispanHost) == "" {
			return nil, fmt.Errorf("response resumer: infinispan cache enabled but MEMORY_SERVICE_INFINISPAN_HOST is not set")
		}
		timeout := cfg.InfinispanStartupTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		opts := &goredis.Options{
			Addr:     cfg.InfinispanHost,
			Username: cfg.InfinispanUsername,
			Password: cfg.InfinispanPassword,
			Protocol: 2,
		}
		return newRedisLocatorStore(timeoutCtx, opts)
	default:
		return nil, fmt.Errorf("response resumer: unsupported cache type %q", cfg.CacheType)
	}
}

type redisLocatorStore struct {
	client *goredis.Client
}

func newRedisLocatorStore(ctx context.Context, opts *goredis.Options) (*redisLocatorStore, error) {
	client := goredis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("response resumer: cache ping failed: %w", err)
	}
	return &redisLocatorStore{client: client}, nil
}

func (s *redisLocatorStore) Available() bool {
	return true
}

func (s *redisLocatorStore) Get(ctx context.Context, conversationID string) (*Locator, error) {
	value, err := s.client.Get(ctx, locatorKey(conversationID)).Result()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	locator, ok := DecodeLocator(value)
	if !ok {
		return nil, nil
	}
	return &locator, nil
}

func (s *redisLocatorStore) Upsert(ctx context.Context, conversationID string, locator Locator, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	return s.client.Set(ctx, locatorKey(conversationID), locator.Encode(), ttl).Err()
}

func (s *redisLocatorStore) Remove(ctx context.Context, conversationID string) error {
	return s.client.Del(ctx, locatorKey(conversationID)).Err()
}

func (s *redisLocatorStore) Exists(ctx context.Context, conversationID string) (bool, error) {
	exists, err := s.client.Exists(ctx, locatorKey(conversationID)).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

type noopLocatorStore struct{}

func (noopLocatorStore) Available() bool { return false }

func (noopLocatorStore) Get(_ context.Context, _ string) (*Locator, error) {
	return nil, nil
}

func (noopLocatorStore) Upsert(_ context.Context, _ string, _ Locator, _ time.Duration) error {
	return nil
}

func (noopLocatorStore) Remove(_ context.Context, _ string) error {
	return nil
}

func (noopLocatorStore) Exists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func locatorKey(conversationID string) string {
	return locatorKeyPrefix + strings.TrimSpace(conversationID)
}
