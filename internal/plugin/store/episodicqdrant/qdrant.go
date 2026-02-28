package episodicqdrant

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/chirino/memory-service/internal/config"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/google/uuid"
	pb "github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Client implements episodic vector operations against Qdrant.
type Client struct {
	points         pb.PointsClient
	conn           *grpc.ClientConn
	collectionName string
}

// New creates a Qdrant-backed episodic vector client.
func New(cfg *config.Config) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("qdrant episodic: missing config")
	}
	conn, err := grpc.NewClient(cfg.QdrantAddress(), dialOptions(cfg)...)
	if err != nil {
		return nil, fmt.Errorf("qdrant episodic: connect: %w", err)
	}
	return &Client{
		points:         pb.NewPointsClient(conn),
		conn:           conn,
		collectionName: effectiveCollectionName(cfg),
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// UpsertMemoryVectors writes memory vectors to Qdrant.
func (c *Client) UpsertMemoryVectors(ctx context.Context, items []registryepisodic.MemoryVectorUpsert) error {
	if c == nil || len(items) == 0 {
		return nil
	}
	points := make([]*pb.PointStruct, 0, len(items))
	for _, item := range items {
		payload := map[string]*pb.Value{
			"kind":       stringValue("memory"),
			"memory_id":  stringValue(item.MemoryID.String()),
			"field_name": stringValue(item.FieldName),
			"namespace":  stringValue(item.Namespace),
		}
		ancestors := namespaceAncestors(item.Namespace)
		if len(ancestors) > 0 {
			payload["namespace_ancestors"] = stringListValue(ancestors)
		}
		for k, v := range item.PolicyAttributes {
			key := "policy_attributes." + sanitizePayloadKey(k)
			if pv := toQdrantValue(v); pv != nil {
				payload[key] = pv
			}
		}

		points = append(points, &pb.PointStruct{
			Id: &pb.PointId{
				PointIdOptions: &pb.PointId_Uuid{
					Uuid: memoryFieldPointID(item.MemoryID, item.FieldName),
				},
			},
			Vectors: &pb.Vectors{
				VectorsOptions: &pb.Vectors_Vector{
					Vector: &pb.Vector{Data: item.Embedding},
				},
			},
			Payload: payload,
		})
	}

	_, err := c.points.Upsert(ctx, &pb.UpsertPoints{
		CollectionName: c.collectionName,
		Points:         points,
	})
	if err != nil {
		return fmt.Errorf("qdrant episodic upsert: %w", err)
	}
	return nil
}

// DeleteMemoryVectors removes all vectors for the given memory ID.
func (c *Client) DeleteMemoryVectors(ctx context.Context, memoryID uuid.UUID) error {
	if c == nil {
		return nil
	}
	_, err := c.points.Delete(ctx, &pb.DeletePoints{
		CollectionName: c.collectionName,
		Points: &pb.PointsSelector{
			PointsSelectorOneOf: &pb.PointsSelector_Filter{
				Filter: &pb.Filter{
					Must: []*pb.Condition{
						matchKeywordCondition("kind", "memory"),
						matchKeywordCondition("memory_id", memoryID.String()),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("qdrant episodic delete: %w", err)
	}
	return nil
}

// SearchMemoryVectors searches vectors using namespace_ancestors + attribute filter.
func (c *Client) SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter map[string]interface{}, limit int) ([]registryepisodic.MemoryVectorSearch, error) {
	if c == nil || limit <= 0 || len(embedding) == 0 {
		return nil, nil
	}

	must := []*pb.Condition{
		matchKeywordCondition("kind", "memory"),
		// Qdrant matches keyword against array elements, so this enforces prefix by exact ancestor match.
		matchKeywordCondition("namespace_ancestors", namespacePrefix),
	}
	must = append(must, filterConditions(filter)...)

	searchLimit := limit * 6 // overfetch because one memory can have multiple field vectors
	if searchLimit < limit {
		searchLimit = limit
	}
	if searchLimit > 1000 {
		searchLimit = 1000
	}

	resp, err := c.points.Search(ctx, &pb.SearchPoints{
		CollectionName: c.collectionName,
		Vector:         embedding,
		Limit:          uint64(searchLimit),
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
		Filter:         &pb.Filter{Must: must},
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant episodic search: %w", err)
	}

	bestByID := make(map[uuid.UUID]float64)
	for _, pt := range resp.GetResult() {
		memoryID, ok := memoryIDFromPayload(pt.GetPayload())
		if !ok {
			continue
		}
		score := float64(pt.GetScore())
		if prev, exists := bestByID[memoryID]; !exists || score > prev {
			bestByID[memoryID] = score
		}
	}

	results := make([]registryepisodic.MemoryVectorSearch, 0, len(bestByID))
	for id, score := range bestByID {
		results = append(results, registryepisodic.MemoryVectorSearch{
			MemoryID: id,
			Score:    score,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func memoryFieldPointID(memoryID uuid.UUID, fieldName string) string {
	// Use deterministic UUIDv5 over memoryID + field so each field has a stable point ID.
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(memoryID.String()+":"+fieldName)).String()
}

func namespaceAncestors(encoded string) []string {
	if strings.TrimSpace(encoded) == "" {
		return nil
	}
	parts := strings.Split(encoded, "\x1e")
	out := make([]string, 0, len(parts))
	var prefix string
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i == 0 || prefix == "" {
			prefix = part
		} else {
			prefix = prefix + "\x1e" + part
		}
		out = append(out, prefix)
	}
	return out
}

func filterConditions(filter map[string]interface{}) []*pb.Condition {
	if len(filter) == 0 {
		return nil
	}
	keys := make([]string, 0, len(filter))
	for k := range filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]*pb.Condition, 0, len(keys))
	for _, key := range keys {
		value := filter[key]
		payloadKey := "policy_attributes." + sanitizePayloadKey(key)

		switch typed := value.(type) {
		case map[string]interface{}:
			if members, ok := typed["in"]; ok {
				if cond := inCondition(payloadKey, members); cond != nil {
					out = append(out, cond)
				}
			}
			if cond := rangeCondition(payloadKey, typed); cond != nil {
				out = append(out, cond)
			}
		default:
			if cond := scalarMatchCondition(payloadKey, typed); cond != nil {
				out = append(out, cond)
			}
		}
	}
	return out
}

func rangeCondition(key string, expr map[string]interface{}) *pb.Condition {
	var r pb.Range
	has := false
	if v, ok := toFloat(expr["gt"]); ok {
		r.Gt = &v
		has = true
	}
	if v, ok := toFloat(expr["gte"]); ok {
		r.Gte = &v
		has = true
	}
	if v, ok := toFloat(expr["lt"]); ok {
		r.Lt = &v
		has = true
	}
	if v, ok := toFloat(expr["lte"]); ok {
		r.Lte = &v
		has = true
	}
	if !has {
		return nil
	}
	return &pb.Condition{
		ConditionOneOf: &pb.Condition_Field{
			Field: &pb.FieldCondition{
				Key:   key,
				Range: &r,
			},
		},
	}
}

func inCondition(key string, members interface{}) *pb.Condition {
	list, ok := members.([]interface{})
	if !ok || len(list) == 0 {
		return nil
	}

	ints := make([]int64, 0, len(list))
	strs := make([]string, 0, len(list))
	allInts := true
	for _, item := range list {
		if i, ok := toInt64(item); ok {
			ints = append(ints, i)
			strs = append(strs, strconv.FormatInt(i, 10))
			continue
		}
		allInts = false
		strs = append(strs, fmt.Sprintf("%v", item))
	}
	if allInts {
		return &pb.Condition{
			ConditionOneOf: &pb.Condition_Field{
				Field: &pb.FieldCondition{
					Key: key,
					Match: &pb.Match{
						MatchValue: &pb.Match_Integers{
							Integers: &pb.RepeatedIntegers{Integers: ints},
						},
					},
				},
			},
		}
	}

	return &pb.Condition{
		ConditionOneOf: &pb.Condition_Field{
			Field: &pb.FieldCondition{
				Key: key,
				Match: &pb.Match{
					MatchValue: &pb.Match_Keywords{
						Keywords: &pb.RepeatedStrings{Strings: strs},
					},
				},
			},
		},
	}
}

func scalarMatchCondition(key string, value interface{}) *pb.Condition {
	var match *pb.Match
	switch typed := value.(type) {
	case string:
		match = &pb.Match{MatchValue: &pb.Match_Keyword{Keyword: typed}}
	case bool:
		match = &pb.Match{MatchValue: &pb.Match_Boolean{Boolean: typed}}
	default:
		if i, ok := toInt64(value); ok {
			match = &pb.Match{MatchValue: &pb.Match_Integer{Integer: i}}
		} else {
			match = &pb.Match{MatchValue: &pb.Match_Keyword{Keyword: fmt.Sprintf("%v", typed)}}
		}
	}
	return &pb.Condition{
		ConditionOneOf: &pb.Condition_Field{
			Field: &pb.FieldCondition{
				Key:   key,
				Match: match,
			},
		},
	}
}

func memoryIDFromPayload(payload map[string]*pb.Value) (uuid.UUID, bool) {
	if payload == nil {
		return uuid.Nil, false
	}
	raw, ok := payload["memory_id"]
	if !ok || raw == nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw.GetStringValue())
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func sanitizePayloadKey(s string) string {
	return strings.ReplaceAll(s, "$", "")
}

func toQdrantValue(v interface{}) *pb.Value {
	switch typed := v.(type) {
	case nil:
		return nil
	case string:
		return stringValue(typed)
	case bool:
		return &pb.Value{Kind: &pb.Value_BoolValue{BoolValue: typed}}
	case int:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case int8:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case int16:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case int32:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case int64:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: typed}}
	case uint:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case uint8:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case uint16:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case uint32:
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case uint64:
		if typed > math.MaxInt64 {
			return &pb.Value{Kind: &pb.Value_DoubleValue{DoubleValue: float64(typed)}}
		}
		return &pb.Value{Kind: &pb.Value_IntegerValue{IntegerValue: int64(typed)}}
	case float32:
		return &pb.Value{Kind: &pb.Value_DoubleValue{DoubleValue: float64(typed)}}
	case float64:
		return &pb.Value{Kind: &pb.Value_DoubleValue{DoubleValue: typed}}
	case []string:
		return stringListValue(typed)
	case []interface{}:
		values := make([]*pb.Value, 0, len(typed))
		for _, item := range typed {
			if pv := toQdrantValue(item); pv != nil {
				values = append(values, pv)
			}
		}
		return &pb.Value{
			Kind: &pb.Value_ListValue{
				ListValue: &pb.ListValue{Values: values},
			},
		}
	default:
		return stringValue(fmt.Sprintf("%v", typed))
	}
}

func stringValue(v string) *pb.Value {
	return &pb.Value{Kind: &pb.Value_StringValue{StringValue: v}}
}

func stringListValue(values []string) *pb.Value {
	list := make([]*pb.Value, 0, len(values))
	for _, value := range values {
		list = append(list, stringValue(value))
	}
	return &pb.Value{
		Kind: &pb.Value_ListValue{
			ListValue: &pb.ListValue{Values: list},
		},
	}
}

func matchKeywordCondition(key, value string) *pb.Condition {
	return &pb.Condition{
		ConditionOneOf: &pb.Condition_Field{
			Field: &pb.FieldCondition{
				Key: key,
				Match: &pb.Match{
					MatchValue: &pb.Match_Keyword{Keyword: value},
				},
			},
		},
	}
}

func toInt64(v interface{}) (int64, bool) {
	switch typed := v.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		return int64(typed), true
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case float32:
		f := float64(typed)
		if math.Mod(f, 1) != 0 {
			return 0, false
		}
		return int64(f), true
	case float64:
		if math.Mod(typed, 1) != 0 {
			return 0, false
		}
		return int64(typed), true
	default:
		return 0, false
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch typed := v.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case string:
		value, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return value, true
	default:
		return 0, false
	}
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
