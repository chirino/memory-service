// Package infinispan provides a vector store implementation using Infinispan REST API v3.
// Requires Infinispan 16.1 or later for vector search support with @Vector annotation.
package infinispan

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
	"github.com/urfave/cli/v3"
)

//go:embed schemas/vector_chunk.proto
var vectorChunkProtoTemplate string

//go:embed schemas/cache_config.xml
var cacheConfigXMLTemplate string

// infinispanMigrator implements migrate.Migrator for Infinispan cache setup.
type infinispanMigrator struct{}

func (m *infinispanMigrator) Name() string { return "infinispan" }
func (m *infinispanMigrator) Migrate(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	if cfg == nil || cfg.VectorType != "infinispan" || !cfg.VectorMigrateAtStart {
		return nil
	}

	log.Info("Running migration", "name", m.Name())
	migrateCtx := ctx

	client, err := newInfinispanClient(cfg)
	if err != nil {
		return fmt.Errorf("infinispan migrate: connect: %w", err)
	}
	defer client.Close()

	cacheName := effectiveCacheName(cfg)
	dimension := effectiveEmbeddingDimension(cfg)

	// Check if cache exists
	exists, err := client.CacheExists(migrateCtx, cacheName)
	if err != nil {
		return fmt.Errorf("infinispan migrate: check cache: %w", err)
	}
	if exists {
		log.Info("Cache already exists", "name", cacheName)
		return nil
	}

	// Register Protobuf schema
	if err := client.RegisterSchema(migrateCtx, dimension); err != nil {
		return fmt.Errorf("infinispan migrate: register schema: %w", err)
	}

	// Create cache
	if err := client.CreateCache(migrateCtx, cacheName, dimension); err != nil {
		return fmt.Errorf("infinispan migrate: create cache: %w", err)
	}

	log.Info("Created Infinispan cache", "name", cacheName)
	return nil
}

func init() {
	registryvector.Register(registryvector.Plugin{
		Name:   "infinispan",
		Loader: load,
		Flags: func(cfg *config.Config) []cli.Flag {
			return []cli.Flag{
				&cli.StringFlag{
					Name:        "vector-infinispan-url",
					Category:    "Vector Store:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_INFINISPAN_URL", "MEMORY_SERVICE_INFINISPAN_URL"),
					Destination: &cfg.InfinispanVectorURL,
					Value:       "http://localhost:11222",
					Usage:       "Infinispan server URL",
				},
				&cli.StringFlag{
					Name:        "vector-infinispan-cache-name",
					Category:    "Vector Store:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_INFINISPAN_CACHE_NAME"),
					Destination: &cfg.InfinispanVectorCacheName,
					Usage:       "Infinispan cache name (auto-generated if empty)",
				},
				&cli.StringFlag{
					Name:        "vector-infinispan-username",
					Category:    "Vector Store:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_INFINISPAN_USERNAME", "MEMORY_SERVICE_INFINISPAN_USERNAME"),
					Destination: &cfg.InfinispanVectorUsername,
					Usage:       "Infinispan authentication username",
				},
				&cli.StringFlag{
					Name:        "vector-infinispan-password",
					Category:    "Vector Store:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_INFINISPAN_PASSWORD", "MEMORY_SERVICE_INFINISPAN_PASSWORD"),
					Destination: &cfg.InfinispanVectorPassword,
					Usage:       "Infinispan authentication password",
				},
				&cli.StringFlag{
					Name:        "vector-infinispan-auth-type",
					Category:    "Vector Store:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_INFINISPAN_AUTH_TYPE"),
					Destination: &cfg.InfinispanVectorAuthType,
					Value:       "digest",
					Usage:       "Infinispan auth mechanism (basic|digest)",
				},
				&cli.BoolFlag{
					Name:        "vector-infinispan-use-tls",
					Category:    "Vector Store:",
					Sources:     cli.EnvVars("MEMORY_SERVICE_VECTOR_INFINISPAN_USE_TLS"),
					Destination: &cfg.InfinispanVectorUseTLS,
					Usage:       "Enable TLS for Infinispan connection",
				},
			}
		},
	})
	registrymigrate.Register(registrymigrate.Plugin{Order: 200, Migrator: &infinispanMigrator{}})
}

func load(ctx context.Context) (registryvector.VectorStore, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("infinispan: missing config in context")
	}
	client, err := newInfinispanClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("infinispan: connect: %w", err)
	}
	return &InfinispanStore{
		client:    client,
		cacheName: effectiveCacheName(cfg),
		dimension: effectiveEmbeddingDimension(cfg),
	}, nil
}

// InfinispanStore implements VectorStore using Infinispan REST API.
type InfinispanStore struct {
	client    *InfinispanClient
	cacheName string
	dimension int
}

func (s *InfinispanStore) IsEnabled() bool { return true }
func (s *InfinispanStore) Name() string    { return "infinispan" }

func (s *InfinispanStore) Search(ctx context.Context, embedding []float32, conversationGroupIDs []uuid.UUID, limit int) ([]registryvector.VectorSearchResult, error) {
	if len(conversationGroupIDs) == 0 {
		return nil, nil
	}

	// Build Ickle query for vector search with conversation group filtering
	query := buildVectorSearchQuery(embedding, conversationGroupIDs, limit, s.dimension)

	// Execute search
	response, err := s.client.Search(ctx, s.cacheName, query, limit)
	if err != nil {
		return nil, err
	}

	// Parse results
	var results []registryvector.VectorSearchResult
	for _, hit := range response.Hits {
		hitData := hit.Hit
		if hitData == nil {
			continue
		}

		// Extract fields from hit
		entryIDStr, _ := hitData["entry_id"].(string)
		conversationIDStr, _ := hitData["conversation_id"].(string)
		score, _ := hit.Score.(float64)

		if entryIDStr == "" || conversationIDStr == "" {
			log.Warn("Skipping incomplete hit", "data", hitData)
			continue
		}

		entryID, err := uuid.Parse(entryIDStr)
		if err != nil {
			log.Warn("Invalid entry_id", "value", entryIDStr, "err", err)
			continue
		}

		conversationID, err := uuid.Parse(conversationIDStr)
		if err != nil {
			log.Warn("Invalid conversation_id", "value", conversationIDStr, "err", err)
			continue
		}

		results = append(results, registryvector.VectorSearchResult{
			EntryID:        entryID,
			ConversationID: conversationID,
			Score:          score,
		})
	}

	return results, nil
}

func (s *InfinispanStore) Upsert(ctx context.Context, entries []registryvector.UpsertRequest) error {
	if len(entries) == 0 {
		return nil
	}

	// Convert to Infinispan format and upsert
	for _, entry := range entries {
		vectorItem := map[string]interface{}{
			"_type":                 fmt.Sprintf("VectorItem%d", s.dimension),
			"entry_id":              entry.EntryID.String(),
			"embedding":             entry.Embedding,
			"conversation_id":       entry.ConversationID.String(),
			"conversation_group_id": entry.ConversationGroupID.String(),
			"model":                 entry.ModelName,
		}

		if err := s.client.PutEntry(ctx, s.cacheName, entry.EntryID.String(), vectorItem); err != nil {
			return fmt.Errorf("failed to upsert entry %s: %w", entry.EntryID, err)
		}
	}

	return nil
}

func (s *InfinispanStore) DeleteByConversationGroupID(ctx context.Context, conversationGroupID uuid.UUID) error {
	// Build Ickle delete query
	query := buildDeleteByGroupQuery(conversationGroupID, s.dimension)

	// Execute delete
	return s.client.DeleteByQuery(ctx, s.cacheName, query)
}

func effectiveEmbeddingDimension(cfg *config.Config) int {
	if cfg == nil {
		return 1536
	}
	if cfg.OpenAIDimensions > 0 {
		return cfg.OpenAIDimensions
	}
	switch strings.ToLower(strings.TrimSpace(cfg.EmbedType)) {
	case "local":
		return 384
	case "openai", "":
		return 1536
	default:
		return 1536
	}
}

func effectiveCacheName(cfg *config.Config) string {
	if cfg == nil {
		return "memory-service_openai-text-embedding-3-small-1536"
	}
	if name := strings.TrimSpace(cfg.InfinispanVectorCacheName); name != "" {
		return name
	}
	prefix := strings.TrimSpace(cfg.InfinispanVectorCachePrefix)
	if prefix == "" {
		prefix = "memory-service"
	}
	model := "openai-text-embedding-3-small"
	switch strings.ToLower(strings.TrimSpace(cfg.EmbedType)) {
	case "local":
		model = "all-minilm-l6-v2"
	case "openai":
		if custom := strings.TrimSpace(cfg.OpenAIModelName); custom != "" {
			model = custom
		}
	}
	model = strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(strings.ToLower(model))
	dim := effectiveEmbeddingDimension(cfg)
	return fmt.Sprintf("%s_%s-%d", prefix, model, dim)
}

func newInfinispanClient(cfg *config.Config) (*InfinispanClient, error) {
	baseURL := cfg.InfinispanVectorURL
	if baseURL == "" {
		baseURL = "http://localhost:11222"
	}

	var transport http.RoundTripper
	if cfg.InfinispanVectorUsername != "" && cfg.InfinispanVectorPassword != "" {
		// Create auth transport
		authType := cfg.InfinispanVectorAuthType
		if authType == "" {
			authType = "digest"
		}
		transport = &authTransport{
			username: cfg.InfinispanVectorUsername,
			password: cfg.InfinispanVectorPassword,
			authType: authType,
			base:     http.DefaultTransport,
		}
	}

	httpClient := &http.Client{
		Transport: transport,
	}

	return &InfinispanClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: httpClient,
	}, nil
}
