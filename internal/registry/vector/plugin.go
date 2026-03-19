package vector

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/config"
	"github.com/google/uuid"
	"github.com/urfave/cli/v3"
)

// VectorSearchResult represents a single vector search result.
type VectorSearchResult struct {
	EntryID        uuid.UUID `json:"entryId"`
	ConversationID uuid.UUID `json:"conversationId"`
	Score          float64   `json:"score"`
}

// UpsertRequest holds the data for a single vector upsert operation.
type UpsertRequest struct {
	ConversationGroupID uuid.UUID
	ConversationID      uuid.UUID
	EntryID             uuid.UUID
	Embedding           []float32
	ModelName           string
}

// VectorStore defines the interface for vector search backends.
type VectorStore interface {
	// Search performs a semantic vector search.
	Search(ctx context.Context, embedding []float32, conversationGroupIDs []uuid.UUID, limit int) ([]VectorSearchResult, error)
	// Upsert stores or updates vector embeddings for a batch of entries.
	Upsert(ctx context.Context, entries []UpsertRequest) error
	// DeleteByConversationGroupID removes all embeddings for a conversation group.
	DeleteByConversationGroupID(ctx context.Context, conversationGroupID uuid.UUID) error
	// IsEnabled returns true if the vector store is configured and operational.
	IsEnabled() bool
	// Name returns the plugin name (e.g. "qdrant", "pgvector").
	Name() string
}

// Loader creates a VectorStore from config.
type Loader func(ctx context.Context) (VectorStore, error)

// Plugin represents a vector store plugin.
type Plugin struct {
	Name   string
	Loader Loader
	Flags  func(cfg *config.Config) []cli.Flag
	Apply  func(cfg *config.Config, cmd *cli.Command)
}

var plugins []Plugin

// Register adds a vector store plugin.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

// Names returns all registered vector store plugin names.
func Names() []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

// Select returns the loader for the named vector store plugin.
func Select(name string) (Loader, error) {
	for _, p := range plugins {
		if p.Name == name {
			return p.Loader, nil
		}
	}
	return nil, fmt.Errorf("unknown vector store %q; valid: %v", name, Names())
}

// PluginFlags returns CLI flags contributed by all registered vector store plugins.
func PluginFlags(cfg *config.Config) []cli.Flag {
	var flags []cli.Flag
	for _, p := range plugins {
		if p.Flags != nil {
			flags = append(flags, p.Flags(cfg)...)
		}
	}
	return flags
}

// ApplyAll calls Apply on all registered plugins that define one.
func ApplyAll(cfg *config.Config, cmd *cli.Command) {
	for _, p := range plugins {
		if p.Apply != nil {
			p.Apply(cfg, cmd)
		}
	}
}
