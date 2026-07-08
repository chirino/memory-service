package noop

import (
	"context"
	"time"

	"github.com/chirino/memory-service/internal/registry/cache"
)

func init() {
	cache.Register(cache.Plugin{
		Name: "none",
		Loader: func(ctx context.Context) (cache.MemoryEntriesCache, error) {
			return &noopEntriesCache{}, nil
		},
	})
}

type noopEntriesCache struct{}

func (n *noopEntriesCache) Available() bool { return false }
func (n *noopEntriesCache) Get(_ context.Context, _ string, _ string) (*cache.CachedMemoryEntries, error) {
	return nil, nil
}
func (n *noopEntriesCache) Set(_ context.Context, _ string, _ string, _ cache.CachedMemoryEntries, _ time.Duration) error {
	return nil
}
func (n *noopEntriesCache) Remove(_ context.Context, _ string, _ string) error {
	return nil
}

var _ cache.MemoryEntriesCache = (*noopEntriesCache)(nil)
