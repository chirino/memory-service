package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
)

type entriesCacheKey struct{}

// WithEntriesCacheContext returns a new context carrying the given MemoryEntriesCache.
func WithEntriesCacheContext(ctx context.Context, c MemoryEntriesCache) context.Context {
	return context.WithValue(ctx, entriesCacheKey{}, c)
}

// EntriesCacheFromContext retrieves the MemoryEntriesCache from the context.
// Returns nil if none was set.
func EntriesCacheFromContext(ctx context.Context) MemoryEntriesCache {
	c, _ := ctx.Value(entriesCacheKey{}).(MemoryEntriesCache)
	return c
}

// CachedMemoryEntries holds cached memory entries for a conversation/client pair.
type CachedMemoryEntries struct {
	Entries []model.Entry
	Epoch   *int64
}

// MemoryEntriesCache caches memory entries for sync operations.
type MemoryEntriesCache interface {
	Available() bool
	Get(ctx context.Context, conversationID uuid.UUID, clientID string) (*CachedMemoryEntries, error)
	Set(ctx context.Context, conversationID uuid.UUID, clientID string, entries CachedMemoryEntries, ttl time.Duration) error
	Remove(ctx context.Context, conversationID uuid.UUID, clientID string) error
}

// Loader creates a cache from config.
type Loader func(ctx context.Context) (MemoryEntriesCache, error)

// Plugin represents a cache plugin.
type Plugin struct {
	Name   string
	Loader Loader
}

var plugins []Plugin

// Register adds a cache plugin.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

// Names returns all registered cache plugin names.
func Names() []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

// Select returns the loader for the named cache plugin.
func Select(name string) (Loader, error) {
	for _, p := range plugins {
		if p.Name == name {
			return p.Loader, nil
		}
	}
	return nil, fmt.Errorf("unknown cache %q; valid: %v", name, Names())
}
