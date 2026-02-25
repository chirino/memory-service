package qdrant

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
	pb "github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// qdrantMigrator implements migrate.Migrator for Qdrant collection setup.
type qdrantMigrator struct{}

func (m *qdrantMigrator) Name() string { return "qdrant" }
func (m *qdrantMigrator) Migrate(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	if cfg == nil || cfg.VectorType != "qdrant" || !cfg.VectorMigrateAtStart {
		return nil
	}

	log.Info("Running migration", "name", m.Name())
	migrateCtx, cancel := context.WithTimeout(ctx, cfg.QdrantStartupTimeout)
	defer cancel()

	conn, err := grpc.NewClient(cfg.QdrantAddress(), dialOptions(cfg)...)
	if err != nil {
		return fmt.Errorf("qdrant migrate: connect: %w", err)
	}
	defer conn.Close()

	client := pb.NewCollectionsClient(conn)
	collectionName := effectiveCollectionName(cfg)

	// Check if collection exists
	_, err = client.Get(migrateCtx, &pb.GetCollectionInfoRequest{CollectionName: collectionName})
	if err == nil {
		return nil // collection exists
	}

	// Create collection with cosine distance
	vectorSize := effectiveEmbeddingDimension(cfg)
	_, err = client.Create(migrateCtx, &pb.CreateCollection{
		CollectionName: collectionName,
		VectorsConfig: &pb.VectorsConfig{
			Config: &pb.VectorsConfig_Params{
				Params: &pb.VectorParams{
					Size:     vectorSize,
					Distance: pb.Distance_Cosine,
				},
			},
		},
		HnswConfig: &pb.HnswConfigDiff{
			M:                 newUint64(16),
			EfConstruct:       newUint64(64),
			FullScanThreshold: newUint64(10000),
		},
	})
	if err != nil {
		return fmt.Errorf("qdrant migrate: create collection: %w", err)
	}
	log.Info("Created Qdrant collection", "name", collectionName)
	return nil
}

func init() {
	registryvector.Register(registryvector.Plugin{
		Name:   "qdrant",
		Loader: load,
	})
	registrymigrate.Register(registrymigrate.Plugin{Order: 200, Migrator: &qdrantMigrator{}})
}

func load(ctx context.Context) (registryvector.VectorStore, error) {
	cfg := config.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("qdrant: missing config in context")
	}
	conn, err := grpc.NewClient(cfg.QdrantAddress(), dialOptions(cfg)...)
	if err != nil {
		return nil, fmt.Errorf("qdrant: connect: %w", err)
	}
	return &QdrantStore{
		points:         pb.NewPointsClient(conn),
		conn:           conn,
		collectionName: effectiveCollectionName(cfg),
	}, nil
}

type QdrantStore struct {
	points         pb.PointsClient
	conn           *grpc.ClientConn
	collectionName string
}

func (s *QdrantStore) IsEnabled() bool { return true }
func (s *QdrantStore) Name() string    { return "qdrant" }

func (s *QdrantStore) Search(ctx context.Context, embedding []float32, conversationGroupIDs []uuid.UUID, limit int) ([]registryvector.VectorSearchResult, error) {
	if len(conversationGroupIDs) == 0 {
		return nil, nil
	}

	groupStrings := make([]string, len(conversationGroupIDs))
	for i, id := range conversationGroupIDs {
		groupStrings[i] = id.String()
	}

	resp, err := s.points.Search(ctx, &pb.SearchPoints{
		CollectionName: s.collectionName,
		Vector:         embedding,
		Limit:          uint64(limit),
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
		Filter: &pb.Filter{
			Must: []*pb.Condition{
				{
					ConditionOneOf: &pb.Condition_Field{
						Field: &pb.FieldCondition{
							Key: "conversation_group_id",
							Match: &pb.Match{
								MatchValue: &pb.Match_Keywords{
									Keywords: &pb.RepeatedStrings{Strings: groupStrings},
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	var results []registryvector.VectorSearchResult
	for _, pt := range resp.GetResult() {
		r := registryvector.VectorSearchResult{
			Score: float64(pt.GetScore()),
		}
		payload := pt.GetPayload()
		if v, ok := payload["entry_id"]; ok {
			if id, err := uuid.Parse(v.GetStringValue()); err == nil {
				r.EntryID = id
			}
		}
		if v, ok := payload["conversation_id"]; ok {
			if id, err := uuid.Parse(v.GetStringValue()); err == nil {
				r.ConversationID = id
			}
		}
		results = append(results, r)
	}
	return results, nil
}

func (s *QdrantStore) Upsert(ctx context.Context, entries []registryvector.UpsertRequest) error {
	points := make([]*pb.PointStruct, len(entries))
	for i, e := range entries {
		points[i] = &pb.PointStruct{
			Id: &pb.PointId{PointIdOptions: &pb.PointId_Uuid{Uuid: e.EntryID.String()}},
			Vectors: &pb.Vectors{
				VectorsOptions: &pb.Vectors_Vector{
					Vector: &pb.Vector{Data: e.Embedding},
				},
			},
			Payload: map[string]*pb.Value{
				"entry_id":              {Kind: &pb.Value_StringValue{StringValue: e.EntryID.String()}},
				"conversation_id":       {Kind: &pb.Value_StringValue{StringValue: e.ConversationID.String()}},
				"conversation_group_id": {Kind: &pb.Value_StringValue{StringValue: e.ConversationGroupID.String()}},
				"model":                 {Kind: &pb.Value_StringValue{StringValue: e.ModelName}},
			},
		}
	}
	_, err := s.points.Upsert(ctx, &pb.UpsertPoints{
		CollectionName: s.collectionName,
		Points:         points,
	})
	return err
}

func (s *QdrantStore) DeleteByConversationGroupID(ctx context.Context, conversationGroupID uuid.UUID) error {
	_, err := s.points.Delete(ctx, &pb.DeletePoints{
		CollectionName: s.collectionName,
		Points: &pb.PointsSelector{
			PointsSelectorOneOf: &pb.PointsSelector_Filter{
				Filter: &pb.Filter{
					Must: []*pb.Condition{
						{
							ConditionOneOf: &pb.Condition_Field{
								Field: &pb.FieldCondition{
									Key: "conversation_group_id",
									Match: &pb.Match{
										MatchValue: &pb.Match_Keyword{
											Keyword: conversationGroupID.String(),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	return err
}

func newUint64(v uint64) *uint64 {
	return &v
}

func dialOptions(cfg *config.Config) []grpc.DialOption {
	opts := make([]grpc.DialOption, 0, 2)
	if cfg.QdrantUseTLS {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(nil)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if strings.TrimSpace(cfg.QdrantAPIKey) != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(apiKeyCredentials{
			apiKey:     cfg.QdrantAPIKey,
			requireTLS: cfg.QdrantUseTLS,
		}))
	}
	return opts
}

type apiKeyCredentials struct {
	apiKey     string
	requireTLS bool
}

func (a apiKeyCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"api-key": a.apiKey}, nil
}

func (a apiKeyCredentials) RequireTransportSecurity() bool {
	return a.requireTLS
}

func effectiveEmbeddingDimension(cfg *config.Config) uint64 {
	if cfg == nil {
		return 1536
	}
	if cfg.OpenAIDimensions > 0 {
		return uint64(cfg.OpenAIDimensions)
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

func effectiveCollectionName(cfg *config.Config) string {
	if cfg == nil {
		return "memory-service_openai-text-embedding-3-small-1536"
	}
	if name := strings.TrimSpace(cfg.QdrantCollectionName); name != "" {
		return name
	}
	prefix := strings.TrimSpace(cfg.QdrantCollectionPrefix)
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
