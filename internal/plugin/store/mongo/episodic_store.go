//go:build !nomongo

package mongo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/episodic"
	episodicqdrant "github.com/chirino/memory-service/internal/plugin/store/episodicqdrant"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/chirino/memory-service/internal/txscope"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func init() {
	registryepisodic.Register(registryepisodic.Plugin{
		Name: "mongo",
		Loader: func(ctx context.Context) (registryepisodic.EpisodicStore, error) {
			cfg := config.FromContext(ctx)
			opts := options.Client().ApplyURI(cfg.DBURL)
			if cfg.DBMaxOpenConns > 0 {
				opts.SetMaxPoolSize(uint64(cfg.DBMaxOpenConns))
			}
			if cfg.DBMaxIdleConns > 0 {
				opts.SetMinPoolSize(uint64(cfg.DBMaxIdleConns))
			}
			client, err := mongo.Connect(opts)
			if err != nil {
				return nil, fmt.Errorf("episodic mongo: connect: %w", err)
			}
			if err := client.Ping(ctx, nil); err != nil {
				return nil, fmt.Errorf("episodic mongo: ping: %w", err)
			}
			s := &mongoEpisodicStore{
				col:     client.Database("memory_service").Collection("memories"),
				usage:   client.Database("memory_service").Collection("memory_usage_stats"),
				vectors: client.Database("memory_service").Collection("memory_vectors"),
			}
			if strings.EqualFold(strings.TrimSpace(cfg.VectorType), "qdrant") {
				qdrantClient, qErr := episodicqdrant.New(cfg)
				if qErr != nil {
					log.Warn("Episodic qdrant unavailable; falling back to mongo in-memory vector search", "err", qErr)
				} else {
					s.qdrant = qdrantClient
				}
			}
			if !cfg.EncryptionDBDisabled {
				s.enc = dataencryption.FromContext(ctx)
			}
			return s, nil
		},
	})
}

// mongoEpisodicStore implements registryepisodic.EpisodicStore using MongoDB.
type mongoEpisodicStore struct {
	col     *mongo.Collection
	usage   *mongo.Collection
	vectors *mongo.Collection
	enc     *dataencryption.Service
	qdrant  *episodicqdrant.Client
}

func (s *mongoEpisodicStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(txscope.WithIntent(ctx, txscope.IntentRead))
}

func (s *mongoEpisodicStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(txscope.WithIntent(ctx, txscope.IntentWrite))
}

// memoryDoc is the BSON document representation of a memory row.
type memoryDoc struct {
	ID               string                 `bson:"_id"`
	Namespace        string                 `bson:"namespace"`
	Key              string                 `bson:"key"`
	ValueEncrypted   []byte                 `bson:"value_encrypted,omitempty"`
	PolicyAttributes map[string]interface{} `bson:"policy_attributes,omitempty"`
	IndexedContent   map[string]string      `bson:"indexed_content,omitempty"`
	Kind             int32                  `bson:"kind"` // 0=add, 1=update
	Revision         int64                  `bson:"revision"`
	CreatedAt        time.Time              `bson:"created_at"`
	ExpiresAt        *time.Time             `bson:"expires_at,omitempty"`
	ArchivedAt       *time.Time             `bson:"archived_at,omitempty"`
	DeletedReason    *int32                 `bson:"deleted_reason,omitempty"` // nil=active, 0=updated, 1=deleted, 2=expired
	IndexedAt        *time.Time             `bson:"indexed_at,omitempty"`
}

type memoryVectorDoc struct {
	ID               string                 `bson:"_id"`
	MemoryID         string                 `bson:"memory_id"`
	FieldName        string                 `bson:"field_name"`
	Namespace        string                 `bson:"namespace"`
	Archived         bool                   `bson:"archived"`
	PolicyAttributes map[string]interface{} `bson:"policy_attributes,omitempty"`
	Embedding        []float32              `bson:"embedding"`
}

type memoryUsageDoc struct {
	Namespace     string    `bson:"namespace"`
	Key           string    `bson:"key"`
	FetchCount    int64     `bson:"fetch_count"`
	LastFetchedAt time.Time `bson:"last_fetched_at"`
}

type adminMemoryCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

type adminOffsetCursor struct {
	Offset int `json:"offset"`
}

const memoryValueFieldDomain = "memory.value"

func (s *mongoEpisodicStore) encryptMemoryValue(id uuid.UUID, plaintext []byte) ([]byte, error) {
	if s.enc == nil || plaintext == nil {
		return plaintext, nil
	}
	return s.enc.EncryptField(plaintext, memoryValueFieldDomain, strings.ToLower(id.String()))
}

func (s *mongoEpisodicStore) decryptMemoryValue(id uuid.UUID, ciphertext []byte) ([]byte, error) {
	if s.enc == nil || ciphertext == nil {
		return ciphertext, nil
	}
	return s.enc.DecryptField(ciphertext, memoryValueFieldDomain, strings.ToLower(id.String()))
}

func encodeNS(ns []string) (string, error) {
	return episodic.EncodeNamespace(ns, 0)
}

// nsPrefixFilter returns a MongoDB filter that matches the exact namespace OR
// any namespace that starts with the prefix followed by the RS separator.
func nsPrefixFilter(nsEncoded string) bson.M {
	escaped := regexp.QuoteMeta(nsEncoded)
	return bson.M{"$or": bson.A{
		bson.M{"namespace": nsEncoded},
		bson.M{"namespace": bson.M{"$regex": "^" + escaped + "\x1e"}},
	}}
}

func matchesMemoryArchiveFilter(archivedAt *time.Time, deletedReason *int32, archived registryepisodic.ArchiveFilter) bool {
	if archivedAt == nil {
		return archived != registryepisodic.ArchiveFilterOnly
	}
	if deletedReason == nil {
		return false
	}
	switch *deletedReason {
	case 1:
		return archived != registryepisodic.ArchiveFilterExclude
	case 2:
		return false
	default:
		return false
	}
}

func mongoMemoryArchiveMatch(archived registryepisodic.ArchiveFilter) bson.M {
	switch archived {
	case registryepisodic.ArchiveFilterInclude:
		return bson.M{"$or": bson.A{
			bson.M{"archived_at": bson.M{"$exists": false}},
			bson.M{"deleted_reason": int32(1)},
		}}
	case registryepisodic.ArchiveFilterOnly:
		return bson.M{"deleted_reason": int32(1)}
	default:
		return bson.M{"archived_at": bson.M{"$exists": false}}
	}
}

// PutMemory upserts a memory. The previous active row is archived first.
func (s *mongoEpisodicStore) PutMemory(ctx context.Context, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
	nsEncoded, err := encodeNS(req.Namespace)
	if err != nil {
		return nil, err
	}

	newID := uuid.New()

	valueJSON, err := json.Marshal(req.Value)
	if err != nil {
		return nil, fmt.Errorf("marshal value: %w", err)
	}
	valueEnc, err := s.encryptMemoryValue(newID, valueJSON)
	if err != nil {
		return nil, fmt.Errorf("encrypt value: %w", err)
	}

	var expiresAt *time.Time
	if req.TTLSeconds > 0 {
		t := time.Now().Add(time.Duration(req.TTLSeconds) * time.Second)
		expiresAt = &t
	}

	now := time.Now()
	indexedContent := req.Index
	if indexedContent == nil {
		indexedContent = map[string]string{}
	}

	var active memoryDoc
	hasActive := false
	if err := s.col.FindOne(
		ctx,
		bson.M{"namespace": nsEncoded, "key": req.Key, "archived_at": bson.M{"$exists": false}},
		options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}),
	).Decode(&active); err != nil {
		if err != mongo.ErrNoDocuments {
			return nil, fmt.Errorf("load active memory: %w", err)
		}
	} else {
		hasActive = true
	}
	if req.ExpectedRevision != nil {
		if !hasActive || active.Revision != *req.ExpectedRevision {
			return nil, registryepisodic.ErrMemoryRevisionConflict
		}
	}
	revision := int64(1)
	if hasActive {
		revision = active.Revision + 1
	}

	// Soft-delete the current active row for (namespace, key).
	// Set deleted_reason=0 (superseded by update) and reset indexed_at.
	deletedReason0 := int32(0)
	activeFilter := bson.M{
		"namespace":   nsEncoded,
		"key":         req.Key,
		"archived_at": bson.M{"$exists": false},
	}
	updateResult, err := s.col.UpdateMany(ctx, activeFilter, bson.M{
		"$set":   bson.M{"archived_at": now, "deleted_reason": deletedReason0},
		"$unset": bson.M{"indexed_at": ""},
	})
	if err != nil {
		return nil, fmt.Errorf("archive previous memory: %w", err)
	}

	// kind=0 (add) if no previous row existed; kind=1 (update) if one was archived.
	var kind int32
	if updateResult.ModifiedCount > 0 {
		kind = 1
	}

	// Insert the new document.
	doc := memoryDoc{
		ID:               newID.String(),
		Namespace:        nsEncoded,
		Key:              req.Key,
		ValueEncrypted:   valueEnc,
		PolicyAttributes: req.PolicyAttributes,
		IndexedContent:   indexedContent,
		Kind:             kind,
		Revision:         revision,
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
		// IndexedAt omitted = pending vector sync
	}
	if _, err := s.col.InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("insert memory: %w", err)
	}

	return &registryepisodic.MemoryWriteResult{
		ID:         newID,
		Namespace:  req.Namespace,
		Key:        req.Key,
		Attributes: req.PolicyAttributes,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		Revision:   revision,
	}, nil
}

// GetMemory retrieves the current memory for the given (namespace, key).
func (s *mongoEpisodicStore) GetMemory(ctx context.Context, namespace []string, key string, archived registryepisodic.ArchiveFilter) (*registryepisodic.MemoryItem, error) {
	nsEncoded, err := encodeNS(namespace)
	if err != nil {
		return nil, err
	}

	var doc memoryDoc
	if err := s.col.FindOne(
		ctx,
		bson.M{"namespace": nsEncoded, "key": key},
		options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}),
	).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, fmt.Errorf("get memory: %w", err)
	}
	if !matchesMemoryArchiveFilter(doc.ArchivedAt, doc.DeletedReason, archived) {
		return nil, nil
	}
	return s.docToItem(doc, namespace)
}

func (s *mongoEpisodicStore) IncrementMemoryLoads(ctx context.Context, keys []registryepisodic.MemoryKey, fetchedAt time.Time) error {
	if len(keys) == 0 {
		return nil
	}

	type usageKey struct {
		namespace string
		key       string
	}

	unique := make(map[string]usageKey, len(keys))
	for _, item := range keys {
		if item.Key == "" {
			continue
		}
		nsEncoded, err := encodeNS(item.Namespace)
		if err != nil {
			return err
		}
		dedupeKey := nsEncoded + "\x00" + item.Key
		if _, ok := unique[dedupeKey]; ok {
			continue
		}
		unique[dedupeKey] = usageKey{namespace: nsEncoded, key: item.Key}
	}
	if len(unique) == 0 {
		return nil
	}

	models := make([]mongo.WriteModel, 0, len(unique))
	for _, item := range unique {
		model := mongo.NewUpdateOneModel().
			SetFilter(bson.M{"namespace": item.namespace, "key": item.key}).
			SetUpdate(bson.M{
				"$inc": bson.M{"fetch_count": int64(1)},
				"$max": bson.M{"last_fetched_at": fetchedAt},
				"$setOnInsert": bson.M{
					"namespace": item.namespace,
					"key":       item.key,
				},
			}).
			SetUpsert(true)
		models = append(models, model)
	}

	_, err := s.usage.BulkWrite(ctx, models)
	if err != nil {
		return fmt.Errorf("increment memory loads: %w", err)
	}
	return nil
}

func (s *mongoEpisodicStore) GetMemoryUsage(ctx context.Context, namespace []string, key string) (*registryepisodic.MemoryUsage, error) {
	nsEncoded, err := encodeNS(namespace)
	if err != nil {
		return nil, err
	}

	var doc memoryUsageDoc
	if err := s.usage.FindOne(ctx, bson.M{"namespace": nsEncoded, "key": key}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, fmt.Errorf("get memory usage: %w", err)
	}
	return &registryepisodic.MemoryUsage{
		FetchCount:    doc.FetchCount,
		LastFetchedAt: doc.LastFetchedAt,
	}, nil
}

func (s *mongoEpisodicStore) ListTopMemoryUsage(ctx context.Context, req registryepisodic.ListTopMemoryUsageRequest) ([]registryepisodic.TopMemoryUsageItem, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = config.ClampPageSize(ctx, limit)

	filter := bson.M{}
	if len(req.Prefix) > 0 {
		nsEncoded, err := encodeNS(req.Prefix)
		if err != nil {
			return nil, err
		}
		filter = nsPrefixFilter(nsEncoded)
	}

	sort := bson.D{{Key: "fetch_count", Value: -1}, {Key: "last_fetched_at", Value: -1}}
	if req.Sort == registryepisodic.MemoryUsageSortLastFetchedAt {
		sort = bson.D{{Key: "last_fetched_at", Value: -1}, {Key: "fetch_count", Value: -1}}
	}
	opts := options.Find().SetSort(sort).SetLimit(int64(limit))

	cursor, err := s.usage.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("list top memory usage: %w", err)
	}
	defer cursor.Close(ctx)

	out := make([]registryepisodic.TopMemoryUsageItem, 0, limit)
	for cursor.Next(ctx) {
		var doc memoryUsageDoc
		if err := cursor.Decode(&doc); err != nil {
			log.Warn("decode memory usage", "err", err)
			continue
		}
		ns, err := episodic.DecodeNamespace(doc.Namespace)
		if err != nil {
			continue
		}
		out = append(out, registryepisodic.TopMemoryUsageItem{
			Namespace: ns,
			Key:       doc.Key,
			Usage: registryepisodic.MemoryUsage{
				FetchCount:    doc.FetchCount,
				LastFetchedAt: doc.LastFetchedAt,
			},
		})
	}
	return out, cursor.Err()
}

// ArchiveMemory archives the active memory for the given (namespace, key).
func (s *mongoEpisodicStore) ArchiveMemory(ctx context.Context, namespace []string, key string, expectedRevision *int64) error {
	nsEncoded, err := encodeNS(namespace)
	if err != nil {
		return err
	}

	deletedReason1 := int32(1)
	filter := bson.M{
		"namespace":   nsEncoded,
		"key":         key,
		"archived_at": bson.M{"$exists": false},
	}
	if expectedRevision != nil {
		filter["revision"] = *expectedRevision
	}
	result, err := s.col.UpdateMany(ctx, filter, bson.M{
		"$set":   bson.M{"archived_at": time.Now(), "deleted_reason": deletedReason1},
		"$inc":   bson.M{"revision": int64(1)},
		"$unset": bson.M{"indexed_at": ""},
	})
	if err == nil && expectedRevision != nil && result.ModifiedCount == 0 {
		return registryepisodic.ErrMemoryRevisionConflict
	}
	return err
}

// SearchMemories performs attribute-filter-only search within the namespace prefix.
func (s *mongoEpisodicStore) SearchMemories(ctx context.Context, namespacePrefix []string, filter registryepisodic.AttributeFilter, limit int, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryItem, error) {
	nsEncoded, err := encodeNS(namespacePrefix)
	if err != nil {
		return nil, err
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: nsPrefixFilter(nsEncoded)}},
		{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "namespace", Value: "$namespace"}, {Key: "key", Value: "$key"}}},
			{Key: "doc", Value: bson.D{{Key: "$first", Value: "$$ROOT"}}},
		}}},
		{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$doc"}}}},
		{{Key: "$match", Value: mongoMemoryArchiveMatch(archived)}},
	}
	if !filter.Empty() {
		match := bson.M{}
		applyMongoFilter(match, filter)
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}
	pipeline = append(pipeline,
		bson.D{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}}},
		bson.D{{Key: "$limit", Value: int64(limit)}},
	)

	cursor, err := s.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	defer cursor.Close(ctx)

	var items []registryepisodic.MemoryItem
	for cursor.Next(ctx) {
		var doc memoryDoc
		if err := cursor.Decode(&doc); err != nil {
			log.Warn("Failed to decode memory doc", "err", err)
			continue
		}
		ns, _ := episodic.DecodeNamespace(doc.Namespace)
		item, err := s.docToItem(doc, ns)
		if err != nil {
			log.Warn("Failed to decrypt memory", "id", doc.ID, "err", err)
			continue
		}
		items = append(items, *item)
	}
	return items, cursor.Err()
}

// ListNamespaces returns distinct current namespaces matching the prefix/suffix constraints.
func (s *mongoEpisodicStore) ListNamespaces(ctx context.Context, req registryepisodic.ListNamespacesRequest) ([][]string, error) {
	pipeline := mongo.Pipeline{}
	if len(req.Prefix) > 0 {
		nsEncoded, err := encodeNS(req.Prefix)
		if err != nil {
			return nil, err
		}
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: nsPrefixFilter(nsEncoded)}})
	}
	pipeline = append(pipeline,
		bson.D{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "namespace", Value: "$namespace"}, {Key: "key", Value: "$key"}}},
			{Key: "doc", Value: bson.D{{Key: "$first", Value: "$$ROOT"}}},
		}}},
		bson.D{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$doc"}}}},
		bson.D{{Key: "$match", Value: mongoMemoryArchiveMatch(req.Archived)}},
		bson.D{{Key: "$project", Value: bson.M{"namespace": 1}}},
	)

	cursor, err := s.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	defer cursor.Close(ctx)

	rawNS := []string{}
	for cursor.Next(ctx) {
		var row struct {
			Namespace string `bson:"namespace"`
		}
		if err := cursor.Decode(&row); err != nil {
			continue
		}
		rawNS = append(rawNS, row.Namespace)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var out [][]string
	for _, encoded := range rawNS {
		if len(req.Suffix) > 0 && !episodic.MatchesSuffix(encoded, req.Suffix) {
			continue
		}
		var truncated string
		if req.MaxDepth > 0 {
			truncated = episodic.NamespaceTruncate(encoded, req.MaxDepth)
		} else {
			truncated = encoded
		}
		if seen[truncated] {
			continue
		}
		seen[truncated] = true
		decoded, err := episodic.DecodeNamespace(truncated)
		if err != nil {
			continue
		}
		out = append(out, decoded)
	}
	return out, nil
}

// FindMemoriesPendingIndexing returns memories where indexed_at is absent.
// Archived rows remain eligible so the indexer can either preserve archived-search vectors
// or remove vectors for expired/superseded rows based on deleted_reason.
func (s *mongoEpisodicStore) FindMemoriesPendingIndexing(ctx context.Context, limit int) ([]registryepisodic.PendingMemory, error) {
	filter := bson.M{"indexed_at": bson.M{"$exists": false}}
	opts := options.Find().SetLimit(int64(limit))

	cursor, err := s.col.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find pending indexing: %w", err)
	}
	defer cursor.Close(ctx)

	var out []registryepisodic.PendingMemory
	for cursor.Next(ctx) {
		var doc memoryDoc
		if err := cursor.Decode(&doc); err != nil {
			log.Warn("Decode pending memory", "err", err)
			continue
		}
		pm := registryepisodic.PendingMemory{
			Namespace:        doc.Namespace,
			PolicyAttributes: doc.PolicyAttributes,
			IndexedContent:   doc.IndexedContent,
			ArchivedAt:       doc.ArchivedAt,
			DeletedReason:    doc.DeletedReason,
		}
		id, err := uuid.Parse(doc.ID)
		if err != nil {
			log.Warn("Parse memory ID", "id", doc.ID, "err", err)
			continue
		}
		pm.ID = id

		out = append(out, pm)
	}
	return out, cursor.Err()
}

// SetMemoryIndexedAt marks a memory as indexed.
func (s *mongoEpisodicStore) SetMemoryIndexedAt(ctx context.Context, memoryID uuid.UUID, indexedAt time.Time) error {
	_, err := s.col.UpdateOne(ctx,
		bson.M{"_id": memoryID.String()},
		bson.M{"$set": bson.M{"indexed_at": indexedAt}},
	)
	return err
}

// UpsertMemoryVectors upserts vector embeddings in the memory_vectors collection.
func (s *mongoEpisodicStore) UpsertMemoryVectors(ctx context.Context, items []registryepisodic.MemoryVectorUpsert) error {
	if s.qdrant != nil {
		return s.qdrant.UpsertMemoryVectors(ctx, items)
	}
	for _, item := range items {
		docID := item.MemoryID.String() + ":" + item.FieldName
		filter := bson.M{"memory_id": item.MemoryID.String(), "field_name": item.FieldName}
		update := bson.M{
			"$set": memoryVectorDoc{
				ID:               docID,
				MemoryID:         item.MemoryID.String(),
				FieldName:        item.FieldName,
				Namespace:        item.Namespace,
				Archived:         item.Archived,
				PolicyAttributes: item.PolicyAttributes,
				Embedding:        item.Embedding,
			},
		}
		if _, err := s.vectors.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil {
			return fmt.Errorf("upsert memory vector %s/%s: %w", item.MemoryID, item.FieldName, err)
		}
	}
	return nil
}

// DeleteMemoryVectors removes all vector rows for the given memory_id.
func (s *mongoEpisodicStore) DeleteMemoryVectors(ctx context.Context, memoryID uuid.UUID) error {
	if s.qdrant != nil {
		return s.qdrant.DeleteMemoryVectors(ctx, memoryID)
	}
	_, err := s.vectors.DeleteMany(ctx, bson.M{"memory_id": memoryID.String()})
	return err
}

// SearchMemoryVectors searches memory_vectors using in-memory cosine scoring.
func (s *mongoEpisodicStore) SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter registryepisodic.AttributeFilter, limit int, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryVectorSearch, error) {
	if s.qdrant != nil {
		return s.qdrant.SearchMemoryVectors(ctx, namespacePrefix, embedding, filter, limit, archived)
	}
	if limit <= 0 || len(embedding) == 0 {
		return nil, nil
	}
	f := bson.M{}
	if namespacePrefix != "" {
		f = nsPrefixFilter(namespacePrefix)
	}
	applyMongoFilter(f, filter)
	switch archived {
	case registryepisodic.ArchiveFilterOnly:
		f["archived"] = true
	case registryepisodic.ArchiveFilterExclude:
		f["archived"] = false
	}

	cursor, err := s.vectors.Find(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("search memory vectors: %w", err)
	}
	defer cursor.Close(ctx)

	bestByID := make(map[uuid.UUID]float64)
	for cursor.Next(ctx) {
		var doc memoryVectorDoc
		if err := cursor.Decode(&doc); err != nil {
			log.Warn("Decode memory vector", "err", err)
			continue
		}
		memID, err := uuid.Parse(doc.MemoryID)
		if err != nil {
			continue
		}
		score := cosineSimilarity(embedding, doc.Embedding)
		if prev, exists := bestByID[memID]; !exists || score > prev {
			bestByID[memID] = score
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
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

// GetMemoriesByIDs retrieves current memories by UUID.
func (s *mongoEpisodicStore) GetMemoriesByIDs(ctx context.Context, ids []uuid.UUID, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = id.String()
	}
	filter := bson.M{
		"_id": bson.M{"$in": strIDs},
	}
	cursor, err := s.col.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("get memories by ids: %w", err)
	}
	defer cursor.Close(ctx)

	var items []registryepisodic.MemoryItem
	for cursor.Next(ctx) {
		var doc memoryDoc
		if err := cursor.Decode(&doc); err != nil {
			log.Warn("Decode memory by id", "err", err)
			continue
		}
		if !matchesMemoryArchiveFilter(doc.ArchivedAt, doc.DeletedReason, archived) {
			continue
		}
		ns, _ := episodic.DecodeNamespace(doc.Namespace)
		item, err := s.docToItem(doc, ns)
		if err != nil {
			log.Warn("Decrypt memory", "id", doc.ID, "err", err)
			continue
		}
		items = append(items, *item)
	}
	return items, cursor.Err()
}

// ExpireMemories archives memories whose TTL has elapsed.
func (s *mongoEpisodicStore) ExpireMemories(ctx context.Context) (int64, error) {
	now := time.Now()
	deletedReason2 := int32(2)
	filter := bson.M{
		"expires_at":  bson.M{"$lte": now},
		"archived_at": bson.M{"$exists": false},
	}
	result, err := s.col.UpdateMany(ctx, filter, bson.M{
		"$set":   bson.M{"archived_at": now, "deleted_reason": deletedReason2},
		"$unset": bson.M{"indexed_at": ""},
	})
	if err != nil {
		return 0, err
	}
	return result.ModifiedCount, nil
}

// HardDeleteEvictableUpdates hard-deletes rows with deleted_reason=0 (superseded by update)
// that have been re-indexed (indexed_at exists). Returns the number deleted.
func (s *mongoEpisodicStore) HardDeleteEvictableUpdates(ctx context.Context, limit int) (int64, error) {
	filter := bson.M{
		"deleted_reason": int32(0),
		"indexed_at":     bson.M{"$exists": true},
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "archived_at", Value: 1}}).
		SetLimit(int64(limit)).
		SetProjection(bson.M{"_id": 1})

	cursor, err := s.col.Find(ctx, filter, opts)
	if err != nil {
		return 0, fmt.Errorf("find evictable updates: %w", err)
	}
	defer cursor.Close(ctx)

	var ids []string
	for cursor.Next(ctx) {
		var doc struct {
			ID string `bson:"_id"`
		}
		if err := cursor.Decode(&doc); err == nil {
			ids = append(ids, doc.ID)
		}
	}
	if err := cursor.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result, err := s.col.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, fmt.Errorf("hard-delete evictable updates: %w", err)
	}
	return result.DeletedCount, nil
}

// TombstoneDeletedMemories clears encrypted data from rows with deleted_reason IN (1,2)
// that have been re-indexed (indexed_at exists). Returns the number tombstoned.
func (s *mongoEpisodicStore) TombstoneDeletedMemories(ctx context.Context, limit int) (int64, error) {
	filter := bson.M{
		"deleted_reason":  bson.M{"$in": bson.A{int32(1), int32(2)}},
		"indexed_at":      bson.M{"$exists": true},
		"value_encrypted": bson.M{"$exists": true},
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "archived_at", Value: 1}}).
		SetLimit(int64(limit)).
		SetProjection(bson.M{"_id": 1})

	cursor, err := s.col.Find(ctx, filter, opts)
	if err != nil {
		return 0, fmt.Errorf("find tombstone candidates: %w", err)
	}
	defer cursor.Close(ctx)

	var ids []string
	for cursor.Next(ctx) {
		var doc struct {
			ID string `bson:"_id"`
		}
		if err := cursor.Decode(&doc); err == nil {
			ids = append(ids, doc.ID)
		}
	}
	if err := cursor.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result, err := s.col.UpdateMany(ctx,
		bson.M{"_id": bson.M{"$in": ids}},
		bson.M{"$unset": bson.M{"value_encrypted": ""}},
	)
	if err != nil {
		return 0, fmt.Errorf("tombstone deleted memories: %w", err)
	}
	return result.ModifiedCount, nil
}

// HardDeleteExpiredTombstones hard-deletes tombstone rows older than olderThan.
// Returns the number deleted.
func (s *mongoEpisodicStore) HardDeleteExpiredTombstones(ctx context.Context, olderThan time.Time, limit int) (int64, error) {
	filter := bson.M{
		"deleted_reason":  bson.M{"$in": bson.A{int32(1), int32(2)}},
		"value_encrypted": bson.M{"$exists": false},
		"archived_at":     bson.M{"$lte": olderThan},
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "archived_at", Value: 1}}).
		SetLimit(int64(limit)).
		SetProjection(bson.M{"_id": 1})

	cursor, err := s.col.Find(ctx, filter, opts)
	if err != nil {
		return 0, fmt.Errorf("find expired tombstones: %w", err)
	}
	defer cursor.Close(ctx)

	var ids []string
	for cursor.Next(ctx) {
		var doc struct {
			ID string `bson:"_id"`
		}
		if err := cursor.Decode(&doc); err == nil {
			ids = append(ids, doc.ID)
		}
	}
	if err := cursor.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result, err := s.col.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	if err != nil {
		return 0, fmt.Errorf("hard-delete expired tombstones: %w", err)
	}
	return result.DeletedCount, nil
}

// ListMemoryEvents returns a paginated, time-ordered stream of memory lifecycle events.
// Write events come from rows with kind IN (0,1); archive updates/expired events come from deleted_reason IN (1,2).
// Uses two separate queries merged and sorted in memory.
func (s *mongoEpisodicStore) ListMemoryEvents(ctx context.Context, req registryepisodic.ListEventsRequest) (*registryepisodic.MemoryEventPage, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = config.ClampPageSize(ctx, limit)

	// Decode cursor.
	var cursorOccurredAt time.Time
	var cursorID string
	if req.AfterCursor != "" {
		raw, err := base64.StdEncoding.DecodeString(req.AfterCursor)
		if err == nil {
			var cur registryepisodic.EventCursor
			if err = json.Unmarshal(raw, &cur); err == nil {
				cursorOccurredAt = cur.OccurredAt
				cursorID = cur.ID
			}
		}
	}

	// Determine which kinds to include.
	includeAdd, includeUpdate, includeExpired := true, true, true
	if len(req.Kinds) > 0 {
		includeAdd, includeUpdate, includeExpired = false, false, false
		for _, k := range req.Kinds {
			switch k {
			case registryepisodic.EventKindAdd:
				includeAdd = true
			case registryepisodic.EventKindUpdate:
				includeUpdate = true
			case registryepisodic.EventKindExpired:
				includeExpired = true
			}
		}
	}

	// Namespace prefix filter.
	var nsPrefixF bson.M
	if len(req.NamespacePrefix) > 0 {
		enc, err := encodeNS(req.NamespacePrefix)
		if err != nil {
			return nil, err
		}
		nsPrefixF = nsPrefixFilter(enc)
	}

	type eventItem struct {
		id         uuid.UUID
		namespace  []string
		key        string
		kind       string
		occurredAt time.Time
		valueEnc   []byte
		attrs      map[string]interface{}
		expiresAt  *time.Time
	}

	fetchN := limit + 1

	buildCursorCond := func(occurredAtField string) bson.M {
		if cursorOccurredAt.IsZero() {
			return bson.M{}
		}
		return bson.M{"$or": bson.A{
			bson.M{occurredAtField: bson.M{"$gt": cursorOccurredAt}},
			bson.M{occurredAtField: cursorOccurredAt, "_id": bson.M{"$gt": cursorID}},
		}}
	}

	buildTimeCond := func(occurredAtField string) bson.M {
		cond := bson.M{}
		if req.After != nil {
			cond["$gt"] = req.After
		}
		if req.Before != nil {
			cond["$lt"] = req.Before
		}
		if len(cond) > 0 {
			return bson.M{occurredAtField: cond}
		}
		return bson.M{}
	}

	mergeBsonM := func(maps ...bson.M) bson.M {
		out := bson.M{}
		for _, m := range maps {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}

	var allItems []eventItem

	// Query write events.
	writeKinds := bson.A{}
	if includeAdd {
		writeKinds = append(writeKinds, int32(0))
	}
	if includeUpdate {
		writeKinds = append(writeKinds, int32(1))
	}
	if len(writeKinds) > 0 {
		writeFilter := mergeBsonM(
			bson.M{"kind": bson.M{"$in": writeKinds}},
			buildTimeCond("created_at"),
			buildCursorCond("created_at"),
		)
		if nsPrefixF != nil {
			writeFilter = mergeBsonM(writeFilter, nsPrefixF)
		}
		writeCursor, err := s.col.Find(ctx, writeFilter,
			options.Find().
				SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).
				SetLimit(int64(fetchN)),
		)
		if err != nil {
			return nil, fmt.Errorf("list write events: %w", err)
		}
		defer writeCursor.Close(ctx)
		for writeCursor.Next(ctx) {
			var doc memoryDoc
			if err := writeCursor.Decode(&doc); err != nil {
				continue
			}
			id, err := uuid.Parse(doc.ID)
			if err != nil {
				continue
			}
			ns, _ := episodic.DecodeNamespace(doc.Namespace)
			kindStr := registryepisodic.EventKindAdd
			if doc.Kind == 1 {
				kindStr = registryepisodic.EventKindUpdate
			}
			allItems = append(allItems, eventItem{
				id: id, namespace: ns, key: doc.Key, kind: kindStr,
				occurredAt: doc.CreatedAt, valueEnc: doc.ValueEncrypted,
				attrs: doc.PolicyAttributes, expiresAt: doc.ExpiresAt,
			})
		}
		if err := writeCursor.Err(); err != nil {
			return nil, err
		}
	}

	if includeUpdate {
		updateFilter := mergeBsonM(
			bson.M{"deleted_reason": int32(1)},
			buildTimeCond("archived_at"),
			buildCursorCond("archived_at"),
		)
		if nsPrefixF != nil {
			updateFilter = mergeBsonM(updateFilter, nsPrefixF)
		}
		updateCursor, err := s.col.Find(ctx, updateFilter,
			options.Find().
				SetSort(bson.D{{Key: "archived_at", Value: 1}, {Key: "_id", Value: 1}}).
				SetLimit(int64(fetchN)),
		)
		if err != nil {
			return nil, fmt.Errorf("list archive-update events: %w", err)
		}
		defer updateCursor.Close(ctx)
		for updateCursor.Next(ctx) {
			var doc memoryDoc
			if err := updateCursor.Decode(&doc); err != nil {
				continue
			}
			id, err := uuid.Parse(doc.ID)
			if err != nil {
				continue
			}
			ns, _ := episodic.DecodeNamespace(doc.Namespace)
			allItems = append(allItems, eventItem{
				id: id, namespace: ns, key: doc.Key, kind: registryepisodic.EventKindUpdate,
				occurredAt: *doc.ArchivedAt, valueEnc: doc.ValueEncrypted,
				attrs: doc.PolicyAttributes, expiresAt: doc.ExpiresAt,
			})
		}
		if err := updateCursor.Err(); err != nil {
			return nil, err
		}
	}
	if includeExpired {
		expiredFilter := mergeBsonM(
			bson.M{"deleted_reason": int32(2)},
			buildTimeCond("archived_at"),
			buildCursorCond("archived_at"),
		)
		if nsPrefixF != nil {
			expiredFilter = mergeBsonM(expiredFilter, nsPrefixF)
		}
		expiredCursor, err := s.col.Find(ctx, expiredFilter,
			options.Find().
				SetSort(bson.D{{Key: "archived_at", Value: 1}, {Key: "_id", Value: 1}}).
				SetLimit(int64(fetchN)),
		)
		if err != nil {
			return nil, fmt.Errorf("list expired events: %w", err)
		}
		defer expiredCursor.Close(ctx)
		for expiredCursor.Next(ctx) {
			var doc memoryDoc
			if err := expiredCursor.Decode(&doc); err != nil {
				continue
			}
			id, err := uuid.Parse(doc.ID)
			if err != nil {
				continue
			}
			ns, _ := episodic.DecodeNamespace(doc.Namespace)
			allItems = append(allItems, eventItem{
				id: id, namespace: ns, key: doc.Key, kind: registryepisodic.EventKindExpired,
				occurredAt: *doc.ArchivedAt, expiresAt: doc.ExpiresAt,
			})
		}
		if err := expiredCursor.Err(); err != nil {
			return nil, err
		}
	}

	// Sort merged results by (occurred_at ASC, id ASC).
	sort.Slice(allItems, func(i, j int) bool {
		if allItems[i].occurredAt.Equal(allItems[j].occurredAt) {
			return allItems[i].id.String() < allItems[j].id.String()
		}
		return allItems[i].occurredAt.Before(allItems[j].occurredAt)
	})

	// Truncate to limit+1.
	if len(allItems) > fetchN {
		allItems = allItems[:fetchN]
	}

	hasMore := len(allItems) > limit
	if hasMore {
		allItems = allItems[:limit]
	}

	events := make([]registryepisodic.MemoryEvent, 0, len(allItems))
	for _, item := range allItems {
		var value map[string]interface{}
		var attrs map[string]interface{}
		if item.kind == registryepisodic.EventKindAdd || item.kind == registryepisodic.EventKindUpdate {
			if len(item.valueEnc) > 0 {
				plain, err := s.decryptMemoryValue(item.id, item.valueEnc)
				if err != nil {
					return nil, fmt.Errorf("decrypt memory event value: %w", err)
				}
				if err := json.Unmarshal(plain, &value); err != nil {
					return nil, fmt.Errorf("unmarshal memory event value: %w", err)
				}
			}
			attrs = item.attrs
		}
		events = append(events, registryepisodic.MemoryEvent{
			ID:         item.id,
			Namespace:  item.namespace,
			Key:        item.key,
			Kind:       item.kind,
			OccurredAt: item.occurredAt,
			Value:      value,
			Attributes: attrs,
			ExpiresAt:  item.expiresAt,
		})
	}

	var afterCursor string
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		cur := registryepisodic.EventCursor{OccurredAt: last.OccurredAt, ID: last.ID.String()}
		raw, _ := json.Marshal(cur)
		afterCursor = base64.StdEncoding.EncodeToString(raw)
	}

	return &registryepisodic.MemoryEventPage{
		Events:      events,
		AfterCursor: afterCursor,
	}, nil
}

// AdminGetMemoryByID retrieves any memory (active or archived) by UUID.
func (s *mongoEpisodicStore) AdminGetMemoryByID(ctx context.Context, memoryID uuid.UUID) (*registryepisodic.MemoryItem, error) {
	var doc memoryDoc
	if err := s.col.FindOne(ctx, bson.M{"_id": memoryID.String()}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, fmt.Errorf("admin get memory: %w", err)
	}
	ns, _ := episodic.DecodeNamespace(doc.Namespace)
	return s.docToItem(doc, ns)
}

// AdminForceDeleteMemory hard-deletes any memory by UUID regardless of state.
func (s *mongoEpisodicStore) AdminForceDeleteMemory(ctx context.Context, memoryID uuid.UUID) error {
	_, err := s.col.DeleteOne(ctx, bson.M{"_id": memoryID.String()})
	return err
}

// AdminCountPendingIndexing returns the number of memories with indexed_at absent.
func (s *mongoEpisodicStore) AdminCountPendingIndexing(ctx context.Context) (int64, error) {
	return s.col.CountDocuments(ctx, bson.M{"indexed_at": bson.M{"$exists": false}})
}

// AdminListMemories retrieves latest memory rows across users without policy injection.
func (s *mongoEpisodicStore) AdminListMemories(ctx context.Context, query registryepisodic.AdminMemoryQuery) (registryepisodic.AdminMemoryPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = config.ClampPageSize(ctx, limit)
	pipeline, err := adminLatestMemoryPipeline(query.NamespacePrefix)
	if err != nil {
		return registryepisodic.AdminMemoryPage{}, err
	}
	pipeline = append(pipeline, bson.D{{Key: "$match", Value: mongoMemoryArchiveMatch(query.Archived)}})
	match := bson.M{}
	if query.KeyPrefix != "" {
		match["key"] = bson.M{"$regex": "^" + regexp.QuoteMeta(query.KeyPrefix)}
	}
	if query.CreatedAfter != nil || query.CreatedBefore != nil {
		created := bson.M{}
		if query.CreatedAfter != nil {
			created["$gte"] = *query.CreatedAfter
		}
		if query.CreatedBefore != nil {
			created["$lte"] = *query.CreatedBefore
		}
		match["created_at"] = created
	}
	if query.ExpiresBefore != nil {
		match["expires_at"] = bson.M{"$exists": true, "$lte": *query.ExpiresBefore}
	}
	if query.AfterCursor != "" {
		if cur, ok := decodeAdminMemoryCursor(query.AfterCursor); ok {
			match["$or"] = bson.A{
				bson.M{"created_at": bson.M{"$lt": cur.CreatedAt}},
				bson.M{"created_at": cur.CreatedAt, "_id": bson.M{"$lt": cur.ID}},
			}
		}
	}
	if len(match) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}
	pipeline = append(pipeline,
		bson.D{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}}},
		bson.D{{Key: "$limit", Value: int64(limit + 1)}},
	)
	cursor, err := s.col.Aggregate(ctx, pipeline)
	if err != nil {
		return registryepisodic.AdminMemoryPage{}, fmt.Errorf("admin list memories: %w", err)
	}
	defer cursor.Close(ctx)
	var docs []memoryDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return registryepisodic.AdminMemoryPage{}, fmt.Errorf("admin list memories: %w", err)
	}
	return s.adminDocsToPage(docs, limit)
}

// AdminSearchMemories retrieves latest matching memory rows across users without policy injection.
func (s *mongoEpisodicStore) AdminSearchMemories(ctx context.Context, query registryepisodic.AdminMemorySearchQuery) ([]registryepisodic.MemoryItem, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}
	limit = config.ClampPageSize(ctx, limit)
	pipeline, err := adminLatestMemoryPipeline(query.NamespacePrefix)
	if err != nil {
		return nil, err
	}
	pipeline = append(pipeline, bson.D{{Key: "$match", Value: mongoMemoryArchiveMatch(query.Archived)}})
	match := bson.M{}
	if query.KeyPrefix != "" {
		match["key"] = bson.M{"$regex": "^" + regexp.QuoteMeta(query.KeyPrefix)}
	}
	if !query.Filter.Empty() {
		applyMongoFilter(match, query.Filter)
	}
	if len(match) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}
	pipeline = append(pipeline,
		bson.D{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}}},
		bson.D{{Key: "$limit", Value: int64(limit)}},
	)
	cursor, err := s.col.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("admin search memories: %w", err)
	}
	defer cursor.Close(ctx)
	items := []registryepisodic.MemoryItem{}
	for cursor.Next(ctx) {
		var doc memoryDoc
		if err := cursor.Decode(&doc); err != nil {
			log.Warn("Failed to decode memory doc", "err", err)
			continue
		}
		ns, _ := episodic.DecodeNamespace(doc.Namespace)
		item, err := s.docToItem(doc, ns)
		if err != nil {
			log.Warn("Failed to decrypt memory", "id", doc.ID, "err", err)
			continue
		}
		items = append(items, *item)
	}
	return items, cursor.Err()
}

// AdminListNamespaces retrieves memory namespaces across users without policy injection.
func (s *mongoEpisodicStore) AdminListNamespaces(ctx context.Context, query registryepisodic.AdminNamespaceQuery) (registryepisodic.AdminNamespacePage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 200
	}
	limit = config.ClampPageSize(ctx, limit)
	offset := decodeAdminOffsetCursor(query.AfterCursor)
	namespaces, err := s.ListNamespaces(ctx, registryepisodic.ListNamespacesRequest{
		Prefix:   query.NamespacePrefix,
		Suffix:   query.Suffix,
		MaxDepth: query.MaxDepth,
		Archived: query.Archived,
	})
	if err != nil {
		return registryepisodic.AdminNamespacePage{}, err
	}
	if offset > len(namespaces) {
		offset = len(namespaces)
	}
	end := offset + limit
	if end > len(namespaces) {
		end = len(namespaces)
	}
	page := registryepisodic.AdminNamespacePage{Namespaces: namespaces[offset:end]}
	if end < len(namespaces) {
		page.AfterCursor = encodeAdminOffsetCursor(end)
	}
	return page, nil
}

func adminLatestMemoryPipeline(namespacePrefix []string) (mongo.Pipeline, error) {
	pipeline := mongo.Pipeline{}
	if len(namespacePrefix) > 0 {
		nsEncoded, err := encodeNS(namespacePrefix)
		if err != nil {
			return nil, err
		}
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: nsPrefixFilter(nsEncoded)}})
	}
	pipeline = append(pipeline,
		bson.D{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "namespace", Value: "$namespace"}, {Key: "key", Value: "$key"}}},
			{Key: "doc", Value: bson.D{{Key: "$first", Value: "$$ROOT"}}},
		}}},
		bson.D{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$doc"}}}},
	)
	return pipeline, nil
}

func (s *mongoEpisodicStore) adminDocsToPage(docs []memoryDoc, limit int) (registryepisodic.AdminMemoryPage, error) {
	page := registryepisodic.AdminMemoryPage{}
	if len(docs) > limit {
		next := docs[limit-1]
		page.AfterCursor = encodeAdminMemoryCursor(adminMemoryCursor{CreatedAt: next.CreatedAt, ID: next.ID})
		docs = docs[:limit]
	}
	for _, doc := range docs {
		ns, _ := episodic.DecodeNamespace(doc.Namespace)
		item, err := s.docToItem(doc, ns)
		if err != nil {
			log.Warn("Failed to decrypt memory", "id", doc.ID, "err", err)
			continue
		}
		page.Items = append(page.Items, *item)
	}
	return page, nil
}

func encodeAdminMemoryCursor(cur adminMemoryCursor) string {
	raw, _ := json.Marshal(cur)
	return base64.StdEncoding.EncodeToString(raw)
}

func decodeAdminMemoryCursor(cursor string) (adminMemoryCursor, bool) {
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return adminMemoryCursor{}, false
	}
	var cur adminMemoryCursor
	if err := json.Unmarshal(raw, &cur); err != nil {
		return adminMemoryCursor{}, false
	}
	return cur, !cur.CreatedAt.IsZero() && cur.ID != ""
}

func encodeAdminOffsetCursor(offset int) string {
	raw, _ := json.Marshal(adminOffsetCursor{Offset: offset})
	return base64.StdEncoding.EncodeToString(raw)
}

func decodeAdminOffsetCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	var cur adminOffsetCursor
	if err := json.Unmarshal(raw, &cur); err != nil || cur.Offset < 0 {
		return 0
	}
	return cur.Offset
}

// docToItem converts a memoryDoc to a MemoryItem by decrypting value.
func (s *mongoEpisodicStore) docToItem(doc memoryDoc, namespace []string) (*registryepisodic.MemoryItem, error) {
	id, err := uuid.Parse(doc.ID)
	if err != nil {
		return nil, fmt.Errorf("parse memory id: %w", err)
	}

	// nil ValueEncrypted means the row is a tombstone (data cleared after eviction).
	var value map[string]interface{}
	if len(doc.ValueEncrypted) > 0 {
		valuePlain, err := s.decryptMemoryValue(id, doc.ValueEncrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt value: %w", err)
		}
		if err := json.Unmarshal(valuePlain, &value); err != nil {
			return nil, fmt.Errorf("unmarshal value: %w", err)
		}
	}

	return &registryepisodic.MemoryItem{
		ID:         id,
		Namespace:  namespace,
		Key:        doc.Key,
		Value:      value,
		Attributes: doc.PolicyAttributes,
		CreatedAt:  doc.CreatedAt,
		ExpiresAt:  doc.ExpiresAt,
		ArchivedAt: doc.ArchivedAt,
		Revision:   doc.Revision,
	}, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot float64
	var na float64
	var nb float64
	for i := 0; i < n; i++ {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// applyMongoFilter merges an attribute filter into an existing bson.M filter document.
// Keys are matched against the policy_attributes subdocument.
func applyMongoFilter(dst bson.M, filter registryepisodic.AttributeFilter) {
	if filter.Empty() {
		return
	}
	for _, cond := range filter.Conditions {
		safeKey := "policy_attributes." + strings.ReplaceAll(cond.Field, "$", "")
		switch cond.Op {
		case registryepisodic.AttributeFilterOpEq:
			appendMongoAnd(dst, bson.M{safeKey: cond.Values[0].Raw})
		case registryepisodic.AttributeFilterOpIn:
			values := make([]interface{}, 0, len(cond.Values))
			for _, value := range cond.Values {
				values = append(values, value.Raw)
			}
			appendMongoAnd(dst, bson.M{safeKey: bson.M{"$in": values}})
		case registryepisodic.AttributeFilterOpExists:
			appendMongoAnd(dst, bson.M{safeKey: bson.M{"$exists": true, "$ne": nil, "$not": bson.M{"$size": 0}}})
		case registryepisodic.AttributeFilterOpGte, registryepisodic.AttributeFilterOpLte:
			op := "$gte"
			if cond.Op == registryepisodic.AttributeFilterOpLte {
				op = "$lte"
			}
			appendMongoAnd(dst, bson.M{safeKey: bson.M{op: cond.Values[0].Raw}})
		}
	}
}

func appendMongoAnd(dst bson.M, condition bson.M) {
	if len(condition) == 0 {
		return
	}
	existing, ok := dst["$and"].([]bson.M)
	if !ok {
		existing = []bson.M{}
	}
	dst["$and"] = append(existing, condition)
}
