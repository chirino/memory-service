package mongo

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
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

	registrymigrate.Register(registrymigrate.Plugin{Order: 110, Migrator: &mongoEpisodicMigrator{}})
}

// mongoEpisodicMigrator creates the memories collection and its indexes.
type mongoEpisodicMigrator struct{}

func (m *mongoEpisodicMigrator) Name() string { return "mongo-episodic-schema" }

func (m *mongoEpisodicMigrator) Migrate(ctx context.Context) error {
	cfg := config.FromContext(ctx)
	if cfg == nil || !cfg.DatastoreMigrateAtStart {
		return nil
	}
	if cfg.DatastoreType != "mongo" {
		return nil
	}

	log.Info("Running migration", "name", m.Name())
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.DBURL))
	if err != nil {
		return fmt.Errorf("mongo episodic migration: connect: %w", err)
	}
	defer client.Disconnect(ctx)

	db := client.Database("memory_service")
	db.CreateCollection(ctx, "memories") // idempotent: fails silently if exists
	db.CreateCollection(ctx, "memory_vectors")

	indexes := []mongo.IndexModel{
		{Keys: bson.D{
			{Key: "namespace", Value: 1},
			{Key: "key", Value: 1},
			{Key: "deleted_at", Value: 1},
		}},
		{
			Keys:    bson.D{{Key: "expires_at", Value: 1}},
			Options: options.Index().SetSparse(true),
		},
		{
			Keys:    bson.D{{Key: "indexed_at", Value: 1}},
			Options: options.Index().SetSparse(true),
		},
		// Event timeline — write events (kind IN (0,1))
		{
			Keys: bson.D{
				{Key: "namespace", Value: 1},
				{Key: "created_at", Value: 1},
				{Key: "_id", Value: 1},
			},
			Options: options.Index().SetPartialFilterExpression(bson.M{"kind": bson.M{"$in": bson.A{0, 1}}}),
		},
		// Event timeline — delete/expire events (deleted_reason IN (1,2))
		{
			Keys: bson.D{
				{Key: "namespace", Value: 1},
				{Key: "deleted_at", Value: 1},
				{Key: "_id", Value: 1},
			},
			Options: options.Index().SetPartialFilterExpression(bson.M{"deleted_reason": bson.M{"$in": bson.A{1, 2}}}),
		},
	}
	if _, err := db.Collection("memories").Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("mongo episodic migration: create indexes: %w", err)
	}
	vectorIndexes := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "memory_id", Value: 1},
				{Key: "field_name", Value: 1},
			},
			Options: options.Index().SetUnique(true),
		},
		{Keys: bson.D{{Key: "namespace", Value: 1}}},
		{
			Keys:    bson.D{{Key: "policy_attributes", Value: 1}},
			Options: options.Index().SetSparse(true),
		},
	}
	if _, err := db.Collection("memory_vectors").Indexes().CreateMany(ctx, vectorIndexes); err != nil {
		return fmt.Errorf("mongo episodic migration: create vector indexes: %w", err)
	}

	log.Info("MongoDB episodic schema migration complete")
	return nil
}

// mongoEpisodicStore implements registryepisodic.EpisodicStore using MongoDB.
type mongoEpisodicStore struct {
	col     *mongo.Collection
	vectors *mongo.Collection
	enc     *dataencryption.Service
	qdrant  *episodicqdrant.Client
}

// memoryDoc is the BSON document representation of a memory row.
type memoryDoc struct {
	ID               string                 `bson:"_id"`
	Namespace        string                 `bson:"namespace"`
	Key              string                 `bson:"key"`
	ValueEncrypted   []byte                 `bson:"value_encrypted,omitempty"`
	Attributes       []byte                 `bson:"attributes,omitempty"`
	PolicyAttributes map[string]interface{} `bson:"policy_attributes,omitempty"`
	IndexFields      []string               `bson:"index_fields,omitempty"`
	IndexDisabled    bool                   `bson:"index_disabled,omitempty"`
	Kind             int32                  `bson:"kind"` // 0=add, 1=update
	CreatedAt        time.Time              `bson:"created_at"`
	ExpiresAt        *time.Time             `bson:"expires_at,omitempty"`
	DeletedAt        *time.Time             `bson:"deleted_at,omitempty"`
	DeletedReason    *int32                 `bson:"deleted_reason,omitempty"` // nil=active, 0=updated, 1=deleted, 2=expired
	IndexedAt        *time.Time             `bson:"indexed_at,omitempty"`
}

type memoryVectorDoc struct {
	ID               string                 `bson:"_id"`
	MemoryID         string                 `bson:"memory_id"`
	FieldName        string                 `bson:"field_name"`
	Namespace        string                 `bson:"namespace"`
	PolicyAttributes map[string]interface{} `bson:"policy_attributes,omitempty"`
	Embedding        []float32              `bson:"embedding"`
}

func (s *mongoEpisodicStore) encrypt(plaintext []byte) ([]byte, error) {
	if s.enc == nil || plaintext == nil {
		return plaintext, nil
	}
	return s.enc.Encrypt(plaintext)
}

func (s *mongoEpisodicStore) decrypt(ciphertext []byte) ([]byte, error) {
	if s.enc == nil || ciphertext == nil {
		return ciphertext, nil
	}
	return s.enc.Decrypt(ciphertext)
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

// PutMemory upserts a memory. The previous active row is soft-deleted first.
func (s *mongoEpisodicStore) PutMemory(ctx context.Context, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
	nsEncoded, err := encodeNS(req.Namespace)
	if err != nil {
		return nil, err
	}

	valueJSON, err := json.Marshal(req.Value)
	if err != nil {
		return nil, fmt.Errorf("marshal value: %w", err)
	}
	valueEnc, err := s.encrypt(valueJSON)
	if err != nil {
		return nil, fmt.Errorf("encrypt value: %w", err)
	}

	var attrsEnc []byte
	if len(req.Attributes) > 0 {
		attrsJSON, err := json.Marshal(req.Attributes)
		if err != nil {
			return nil, fmt.Errorf("marshal attributes: %w", err)
		}
		attrsEnc, err = s.encrypt(attrsJSON)
		if err != nil {
			return nil, fmt.Errorf("encrypt attributes: %w", err)
		}
	}

	var expiresAt *time.Time
	if req.TTLSeconds > 0 {
		t := time.Now().Add(time.Duration(req.TTLSeconds) * time.Second)
		expiresAt = &t
	}

	now := time.Now()
	newID := uuid.New()

	// Soft-delete the current active row for (namespace, key).
	// Set deleted_reason=0 (superseded by update) and reset indexed_at.
	deletedReason0 := int32(0)
	activeFilter := bson.M{
		"namespace":  nsEncoded,
		"key":        req.Key,
		"deleted_at": bson.M{"$exists": false},
	}
	updateResult, err := s.col.UpdateMany(ctx, activeFilter, bson.M{
		"$set":   bson.M{"deleted_at": now, "deleted_reason": deletedReason0},
		"$unset": bson.M{"indexed_at": ""},
	})
	if err != nil {
		return nil, fmt.Errorf("soft-delete previous memory: %w", err)
	}

	// kind=0 (add) if no previous row existed; kind=1 (update) if one was soft-deleted.
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
		Attributes:       attrsEnc,
		PolicyAttributes: req.PolicyAttributes,
		IndexFields:      req.IndexFields,
		IndexDisabled:    req.IndexDisabled,
		Kind:             kind,
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
		// IndexedAt omitted = pending vector sync
	}
	if _, err := s.col.InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("insert memory: %w", err)
	}

	var decryptedAttrs map[string]interface{}
	if len(attrsEnc) > 0 {
		decryptedAttrs = req.Attributes
	}

	return &registryepisodic.MemoryWriteResult{
		ID:         newID,
		Namespace:  req.Namespace,
		Key:        req.Key,
		Attributes: decryptedAttrs,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
	}, nil
}

// GetMemory retrieves the active (non-deleted) memory for the given (namespace, key).
func (s *mongoEpisodicStore) GetMemory(ctx context.Context, namespace []string, key string) (*registryepisodic.MemoryItem, error) {
	nsEncoded, err := encodeNS(namespace)
	if err != nil {
		return nil, err
	}

	filter := bson.M{
		"namespace":  nsEncoded,
		"key":        key,
		"deleted_at": bson.M{"$exists": false},
	}
	var doc memoryDoc
	if err := s.col.FindOne(ctx, filter).Decode(&doc); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, fmt.Errorf("get memory: %w", err)
	}
	return s.docToItem(doc, namespace)
}

// DeleteMemory soft-deletes the active memory for the given (namespace, key).
func (s *mongoEpisodicStore) DeleteMemory(ctx context.Context, namespace []string, key string) error {
	nsEncoded, err := encodeNS(namespace)
	if err != nil {
		return err
	}

	deletedReason1 := int32(1)
	filter := bson.M{
		"namespace":  nsEncoded,
		"key":        key,
		"deleted_at": bson.M{"$exists": false},
	}
	_, err = s.col.UpdateMany(ctx, filter, bson.M{
		"$set":   bson.M{"deleted_at": time.Now(), "deleted_reason": deletedReason1},
		"$unset": bson.M{"indexed_at": ""},
	})
	return err
}

// SearchMemories performs attribute-filter-only search within the namespace prefix.
func (s *mongoEpisodicStore) SearchMemories(ctx context.Context, namespacePrefix []string, filter map[string]interface{}, limit, offset int) ([]registryepisodic.MemoryItem, error) {
	nsEncoded, err := encodeNS(namespacePrefix)
	if err != nil {
		return nil, err
	}

	f := nsPrefixFilter(nsEncoded)
	f["deleted_at"] = bson.M{"$exists": false}
	applyMongoFilter(f, filter)

	opts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(int64(offset)).
		SetLimit(int64(limit))

	cursor, err := s.col.Find(ctx, f, opts)
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

// ListNamespaces returns distinct active namespaces matching the prefix/suffix constraints.
func (s *mongoEpisodicStore) ListNamespaces(ctx context.Context, req registryepisodic.ListNamespacesRequest) ([][]string, error) {
	prefixFilter := bson.M{}
	if len(req.Prefix) > 0 {
		nsEncoded, err := encodeNS(req.Prefix)
		if err != nil {
			return nil, err
		}
		prefixFilter = nsPrefixFilter(nsEncoded)
	}
	prefixFilter["deleted_at"] = bson.M{"$exists": false}

	var rawNS []string
	if err := s.col.Distinct(ctx, "namespace", prefixFilter).Decode(&rawNS); err != nil && err != mongo.ErrNoDocuments {
		return nil, fmt.Errorf("list namespaces: %w", err)
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
// Value is decrypted for active rows; nil for soft-deleted rows.
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
			IndexFields:      doc.IndexFields,
			IndexDisabled:    doc.IndexDisabled,
			DeletedAt:        doc.DeletedAt,
		}
		id, err := uuid.Parse(doc.ID)
		if err != nil {
			log.Warn("Parse memory ID", "id", doc.ID, "err", err)
			continue
		}
		pm.ID = id

		if doc.DeletedAt == nil {
			plain, err := s.decrypt(doc.ValueEncrypted)
			if err != nil {
				log.Warn("Decrypt memory value for indexing", "id", doc.ID, "err", err)
			} else {
				pm.Value = plain
			}
		}
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
func (s *mongoEpisodicStore) SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter map[string]interface{}, limit int) ([]registryepisodic.MemoryVectorSearch, error) {
	if s.qdrant != nil {
		return s.qdrant.SearchMemoryVectors(ctx, namespacePrefix, embedding, filter, limit)
	}
	if limit <= 0 || len(embedding) == 0 {
		return nil, nil
	}
	f := nsPrefixFilter(namespacePrefix)
	applyMongoFilter(f, filter)

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

// GetMemoriesByIDs retrieves active memories by UUID.
func (s *mongoEpisodicStore) GetMemoriesByIDs(ctx context.Context, ids []uuid.UUID) ([]registryepisodic.MemoryItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = id.String()
	}
	filter := bson.M{
		"_id":        bson.M{"$in": strIDs},
		"deleted_at": bson.M{"$exists": false},
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

// ExpireMemories soft-deletes memories whose TTL has elapsed.
func (s *mongoEpisodicStore) ExpireMemories(ctx context.Context) (int64, error) {
	now := time.Now()
	deletedReason2 := int32(2)
	filter := bson.M{
		"expires_at": bson.M{"$lte": now},
		"deleted_at": bson.M{"$exists": false},
	}
	result, err := s.col.UpdateMany(ctx, filter, bson.M{
		"$set":   bson.M{"deleted_at": now, "deleted_reason": deletedReason2},
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
		SetSort(bson.D{{Key: "deleted_at", Value: 1}}).
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
		SetSort(bson.D{{Key: "deleted_at", Value: 1}}).
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
		bson.M{"$unset": bson.M{"value_encrypted": "", "attributes": ""}},
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
		"deleted_at":      bson.M{"$lte": olderThan},
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "deleted_at", Value: 1}}).
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
// Write events come from rows with kind IN (0,1); delete/expired events from deleted_reason IN (1,2).
// Uses two separate queries merged and sorted in memory.
func (s *mongoEpisodicStore) ListMemoryEvents(ctx context.Context, req registryepisodic.ListEventsRequest) (*registryepisodic.MemoryEventPage, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

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
	includeAdd, includeUpdate, includeDelete, includeExpired := true, true, true, true
	if len(req.Kinds) > 0 {
		includeAdd, includeUpdate, includeDelete, includeExpired = false, false, false, false
		for _, k := range req.Kinds {
			switch k {
			case registryepisodic.EventKindAdd:
				includeAdd = true
			case registryepisodic.EventKindUpdate:
				includeUpdate = true
			case registryepisodic.EventKindDelete:
				includeDelete = true
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
		attrsEnc   []byte
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
				attrsEnc: doc.Attributes, expiresAt: doc.ExpiresAt,
			})
		}
		if err := writeCursor.Err(); err != nil {
			return nil, err
		}
	}

	// Query delete/expired events.
	deleteReasons := bson.A{}
	if includeDelete {
		deleteReasons = append(deleteReasons, int32(1))
	}
	if includeExpired {
		deleteReasons = append(deleteReasons, int32(2))
	}
	if len(deleteReasons) > 0 {
		deleteFilter := mergeBsonM(
			bson.M{"deleted_reason": bson.M{"$in": deleteReasons}},
			buildTimeCond("deleted_at"),
			buildCursorCond("deleted_at"),
		)
		if nsPrefixF != nil {
			deleteFilter = mergeBsonM(deleteFilter, nsPrefixF)
		}
		deleteCursor, err := s.col.Find(ctx, deleteFilter,
			options.Find().
				SetSort(bson.D{{Key: "deleted_at", Value: 1}, {Key: "_id", Value: 1}}).
				SetLimit(int64(fetchN)),
		)
		if err != nil {
			return nil, fmt.Errorf("list delete events: %w", err)
		}
		defer deleteCursor.Close(ctx)
		for deleteCursor.Next(ctx) {
			var doc memoryDoc
			if err := deleteCursor.Decode(&doc); err != nil {
				continue
			}
			id, err := uuid.Parse(doc.ID)
			if err != nil {
				continue
			}
			ns, _ := episodic.DecodeNamespace(doc.Namespace)
			kindStr := registryepisodic.EventKindDelete
			if doc.DeletedReason != nil && *doc.DeletedReason == 2 {
				kindStr = registryepisodic.EventKindExpired
			}
			allItems = append(allItems, eventItem{
				id: id, namespace: ns, key: doc.Key, kind: kindStr,
				occurredAt: *doc.DeletedAt, expiresAt: doc.ExpiresAt,
			})
		}
		if err := deleteCursor.Err(); err != nil {
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
				plain, err := s.decrypt(item.valueEnc)
				if err == nil {
					_ = json.Unmarshal(plain, &value)
				}
			}
			if len(item.attrsEnc) > 0 {
				plain, err := s.decrypt(item.attrsEnc)
				if err == nil {
					_ = json.Unmarshal(plain, &attrs)
				}
			}
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

// AdminGetMemoryByID retrieves any memory (active or soft-deleted) by UUID.
func (s *mongoEpisodicStore) AdminGetMemoryByID(ctx context.Context, memoryID uuid.UUID) (*registryepisodic.MemoryItem, error) {
	var doc memoryDoc
	if err := s.col.FindOne(ctx, bson.M{"_id": memoryID.String()}).Decode(&doc); err != nil {
		if err == mongo.ErrNoDocuments {
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

// docToItem converts a memoryDoc to a MemoryItem by decrypting value and attributes.
func (s *mongoEpisodicStore) docToItem(doc memoryDoc, namespace []string) (*registryepisodic.MemoryItem, error) {
	// nil ValueEncrypted means the row is a tombstone (data cleared after eviction).
	var value map[string]interface{}
	if len(doc.ValueEncrypted) > 0 {
		valuePlain, err := s.decrypt(doc.ValueEncrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt value: %w", err)
		}
		if err := json.Unmarshal(valuePlain, &value); err != nil {
			return nil, fmt.Errorf("unmarshal value: %w", err)
		}
	}

	var attrs map[string]interface{}
	if len(doc.Attributes) > 0 {
		attrsPlain, err := s.decrypt(doc.Attributes)
		if err != nil {
			return nil, fmt.Errorf("decrypt attributes: %w", err)
		}
		if err := json.Unmarshal(attrsPlain, &attrs); err != nil {
			return nil, fmt.Errorf("unmarshal attributes: %w", err)
		}
	}

	id, err := uuid.Parse(doc.ID)
	if err != nil {
		return nil, fmt.Errorf("parse memory id: %w", err)
	}

	return &registryepisodic.MemoryItem{
		ID:         id,
		Namespace:  namespace,
		Key:        doc.Key,
		Value:      value,
		Attributes: attrs,
		CreatedAt:  doc.CreatedAt,
		ExpiresAt:  doc.ExpiresAt,
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
func applyMongoFilter(dst bson.M, filter map[string]interface{}) {
	if len(filter) == 0 {
		return
	}
	for key, val := range filter {
		safeKey := "policy_attributes." + strings.ReplaceAll(key, "$", "")
		switch v := val.(type) {
		case map[string]interface{}:
			cond := bson.M{}
			if members, ok := v["in"]; ok {
				if list, ok := members.([]interface{}); ok {
					cond["$in"] = list
				}
			}
			for op, rhs := range v {
				switch op {
				case "gt":
					cond["$gt"] = rhs
				case "gte":
					cond["$gte"] = rhs
				case "lt":
					cond["$lt"] = rhs
				case "lte":
					cond["$lte"] = rhs
				}
			}
			if len(cond) > 0 {
				dst[safeKey] = cond
			}
		default:
			dst[safeKey] = v
		}
	}
}
