package sqlite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/dataencryption"
	"github.com/chirino/memory-service/internal/episodic"
	episodicqdrant "github.com/chirino/memory-service/internal/plugin/store/episodicqdrant"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func init() {
	registryepisodic.Register(registryepisodic.Plugin{
		Name: "sqlite",
		Loader: func(ctx context.Context) (registryepisodic.EpisodicStore, error) {
			cfg := config.FromContext(ctx)
			handle, err := getSharedHandle(ctx)
			if err != nil {
				return nil, fmt.Errorf("episodic store: failed to connect to sqlite: %w", err)
			}

			ps := &SQLiteStore{handle: handle, db: handle.db, cfg: cfg}
			if !cfg.EncryptionDBDisabled {
				ps.enc = dataencryption.FromContext(ctx)
			}
			store := &sqliteEpisodicStore{handle: handle, db: handle.db, s: ps}
			if strings.EqualFold(strings.TrimSpace(cfg.VectorType), "qdrant") {
				client, qErr := episodicqdrant.New(cfg)
				if qErr != nil {
					log.Warn("Episodic qdrant unavailable; falling back to local vector backend", "err", qErr)
				} else {
					store.qdrant = client
				}
			}
			return store, nil
		},
	})
}

// sqliteEpisodicStore implements registryepisodic.EpisodicStore using GORM + SQLite.
type sqliteEpisodicStore struct {
	handle *sharedHandle
	db     *gorm.DB
	s      *SQLiteStore // for encrypt/decrypt helpers
	qdrant *episodicqdrant.Client
}

func (e *sqliteEpisodicStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return e.handle.InReadTx(ctx, fn)
}

func (e *sqliteEpisodicStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	return e.handle.InWriteTx(ctx, fn)
}

func (e *sqliteEpisodicStore) dbFor(ctx context.Context) *gorm.DB {
	db, err := requireScope(ctx, "sqlite episodic store")
	if err != nil {
		panic(err)
	}
	return db
}

func (e *sqliteEpisodicStore) writeDBFor(ctx context.Context, op string) *gorm.DB {
	db, err := requireWriteScope(ctx, op)
	if err != nil {
		panic(err)
	}
	return db
}

// memoryRow is the GORM-level row for the memories table.
type memoryRow struct {
	ID               uuid.UUID              `gorm:"primaryKey;type:uuid;column:id"`
	Namespace        string                 `gorm:"not null;column:namespace"`
	Key              string                 `gorm:"not null;column:key"`
	ValueEncrypted   []byte                 `gorm:"column:value_encrypted"` // nullable for tombstones
	PolicyAttributes map[string]interface{} `gorm:"type:jsonb;serializer:json;column:policy_attributes"`
	IndexedContent   map[string]string      `gorm:"type:jsonb;serializer:json;column:indexed_content"`
	Kind             int16                  `gorm:"not null;default:0;column:kind"`
	DeletedReason    *int16                 `gorm:"column:deleted_reason"`
	CreatedAt        time.Time              `gorm:"not null;column:created_at"`
	ExpiresAt        *time.Time             `gorm:"column:expires_at"`
	DeletedAt        *time.Time             `gorm:"column:deleted_at"`
	IndexedAt        *time.Time             `gorm:"column:indexed_at"`
}

func (memoryRow) TableName() string { return "memories" }

type memoryUsageRow struct {
	Namespace     string    `gorm:"primaryKey;column:namespace"`
	Key           string    `gorm:"primaryKey;column:key"`
	FetchCount    int64     `gorm:"column:fetch_count"`
	LastFetchedAt time.Time `gorm:"column:last_fetched_at"`
}

func (memoryUsageRow) TableName() string { return "memory_usage_stats" }

func (e *sqliteEpisodicStore) encodeNS(ns []string) (string, error) {
	// Pass 0 as maxDepth to skip depth check (checked in handler).
	return episodic.EncodeNamespace(ns, 0)
}

func (e *sqliteEpisodicStore) decodeNS(encoded string) ([]string, error) {
	return episodic.DecodeNamespace(encoded)
}

// PutMemory upserts a memory. On update, the previous active row is soft-deleted.
func (e *sqliteEpisodicStore) PutMemory(ctx context.Context, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
	nsEncoded, err := e.encodeNS(req.Namespace)
	if err != nil {
		return nil, err
	}

	// Encrypt the value.
	valueJSON, err := json.Marshal(req.Value)
	if err != nil {
		return nil, fmt.Errorf("marshal value: %w", err)
	}
	valueEnc, err := e.s.encrypt(valueJSON)
	if err != nil {
		return nil, fmt.Errorf("encrypt value: %w", err)
	}

	var expiresAt *time.Time
	if req.TTLSeconds > 0 {
		t := time.Now().Add(time.Duration(req.TTLSeconds) * time.Second)
		expiresAt = &t
	}

	newID := uuid.New()
	now := time.Now()
	indexedContent := req.Index
	if indexedContent == nil {
		indexedContent = map[string]string{}
	}

	var kind int16
	err = e.writeDBFor(ctx, "sqlite episodic store put memory").Transaction(func(tx *gorm.DB) error {
		// Soft-delete the current active row for this (namespace, key), if any.
		// Set deleted_reason=0 (superseded by update) and reset indexed_at so the indexer
		// removes the old vector entry.
		deletedReason := int16(0)
		result := tx.Exec(`
			UPDATE memories
			SET deleted_at = ?, indexed_at = NULL, deleted_reason = ?
			WHERE namespace = ? AND key = ? AND deleted_at IS NULL`,
			now, deletedReason, nsEncoded, req.Key,
		)
		if result.Error != nil {
			return fmt.Errorf("soft-delete previous row: %w", result.Error)
		}
		// kind=0 (add) if no previous row existed, kind=1 (update) if one was soft-deleted.
		if result.RowsAffected > 0 {
			kind = 1
		}

		// Insert the new row.
		row := memoryRow{
			ID:               newID,
			Namespace:        nsEncoded,
			Key:              req.Key,
			ValueEncrypted:   valueEnc,
			PolicyAttributes: req.PolicyAttributes,
			IndexedContent:   indexedContent,
			Kind:             kind,
			CreatedAt:        now,
			ExpiresAt:        expiresAt,
			// IndexedAt NULL = pending vector sync
		}
		return tx.Create(&row).Error
	})
	if err != nil {
		return nil, err
	}

	return &registryepisodic.MemoryWriteResult{
		ID:         newID,
		Namespace:  req.Namespace,
		Key:        req.Key,
		Attributes: req.PolicyAttributes,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
	}, nil
}

// GetMemory retrieves the active memory for (namespace, key).
func (e *sqliteEpisodicStore) GetMemory(ctx context.Context, namespace []string, key string) (*registryepisodic.MemoryItem, error) {
	nsEncoded, err := e.encodeNS(namespace)
	if err != nil {
		return nil, err
	}

	var row memoryRow
	result := e.dbFor(ctx).
		Where("namespace = ? AND key = ? AND deleted_at IS NULL", nsEncoded, key).
		Limit(1).Find(&row)
	if result.Error != nil {
		return nil, fmt.Errorf("get memory: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return e.rowToItem(row, namespace)
}

func (e *sqliteEpisodicStore) IncrementMemoryLoads(ctx context.Context, keys []registryepisodic.MemoryKey, fetchedAt time.Time) error {
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
		nsEncoded, err := e.encodeNS(item.Namespace)
		if err != nil {
			return err
		}
		k := nsEncoded + "\x00" + item.Key
		if _, ok := unique[k]; ok {
			continue
		}
		unique[k] = usageKey{namespace: nsEncoded, key: item.Key}
	}
	if len(unique) == 0 {
		return nil
	}

	values := make([]string, 0, len(unique))
	args := make([]interface{}, 0, len(unique)*3)
	for _, item := range unique {
		values = append(values, "(?, ?, 1, ?)")
		args = append(args, item.namespace, item.key, fetchedAt)
	}

	query := `
		INSERT INTO memory_usage_stats (namespace, key, fetch_count, last_fetched_at)
		VALUES ` + strings.Join(values, ",") + `
		ON CONFLICT (namespace, key) DO UPDATE
		SET fetch_count = memory_usage_stats.fetch_count + EXCLUDED.fetch_count,
			last_fetched_at = CASE
				WHEN memory_usage_stats.last_fetched_at >= EXCLUDED.last_fetched_at THEN memory_usage_stats.last_fetched_at
				ELSE EXCLUDED.last_fetched_at
			END`

	return e.writeDBFor(ctx, "sqlite episodic store increment memory loads").Exec(query, args...).Error
}

func (e *sqliteEpisodicStore) GetMemoryUsage(ctx context.Context, namespace []string, key string) (*registryepisodic.MemoryUsage, error) {
	nsEncoded, err := e.encodeNS(namespace)
	if err != nil {
		return nil, err
	}

	var row memoryUsageRow
	result := e.dbFor(ctx).
		Where("namespace = ? AND key = ?", nsEncoded, key).
		Limit(1).
		Find(&row)
	if result.Error != nil {
		return nil, fmt.Errorf("get memory usage: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &registryepisodic.MemoryUsage{
		FetchCount:    row.FetchCount,
		LastFetchedAt: row.LastFetchedAt,
	}, nil
}

func (e *sqliteEpisodicStore) ListTopMemoryUsage(ctx context.Context, req registryepisodic.ListTopMemoryUsageRequest) ([]registryepisodic.TopMemoryUsageItem, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	q := e.dbFor(ctx).Table("memory_usage_stats")
	if len(req.Prefix) > 0 {
		nsEncoded, err := e.encodeNS(req.Prefix)
		if err != nil {
			return nil, err
		}
		q = q.Where("namespace = ? OR namespace LIKE ?", nsEncoded, episodic.NamespacePrefixPattern(nsEncoded))
	}

	switch req.Sort {
	case registryepisodic.MemoryUsageSortLastFetchedAt:
		q = q.Order("last_fetched_at DESC, fetch_count DESC")
	default:
		q = q.Order("fetch_count DESC, last_fetched_at DESC")
	}
	q = q.Limit(limit)

	var rows []memoryUsageRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list top memory usage: %w", err)
	}

	out := make([]registryepisodic.TopMemoryUsageItem, 0, len(rows))
	for _, row := range rows {
		ns, err := e.decodeNS(row.Namespace)
		if err != nil {
			continue
		}
		out = append(out, registryepisodic.TopMemoryUsageItem{
			Namespace: ns,
			Key:       row.Key,
			Usage: registryepisodic.MemoryUsage{
				FetchCount:    row.FetchCount,
				LastFetchedAt: row.LastFetchedAt,
			},
		})
	}
	return out, nil
}

// DeleteMemory soft-deletes the active memory for (namespace, key).
func (e *sqliteEpisodicStore) DeleteMemory(ctx context.Context, namespace []string, key string) error {
	nsEncoded, err := e.encodeNS(namespace)
	if err != nil {
		return err
	}
	deletedReason := int16(1)
	return e.writeDBFor(ctx, "sqlite episodic store delete memory").Exec(`
		UPDATE memories
		SET deleted_at = ?, indexed_at = NULL, deleted_reason = ?
		WHERE namespace = ? AND key = ? AND deleted_at IS NULL`,
		time.Now().UTC(), deletedReason, nsEncoded, key,
	).Error
}

// SearchMemories performs attribute-filter-only search within the namespace prefix.
func (e *sqliteEpisodicStore) SearchMemories(ctx context.Context, namespacePrefix []string, filter map[string]interface{}, limit, offset int) ([]registryepisodic.MemoryItem, error) {
	nsEncoded, err := e.encodeNS(namespacePrefix)
	if err != nil {
		return nil, err
	}

	q := e.dbFor(ctx).
		Table("memories").
		Where("deleted_at IS NULL").
		Where("namespace = ? OR namespace LIKE ?", nsEncoded, episodic.NamespacePrefixPattern(nsEncoded)).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset)

	if len(filter) > 0 {
		clause, args := buildSQLFilter(filter)
		if clause != "" {
			q = q.Where(clause, args...)
		}
	}

	var rows []memoryRow
	if err := q.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	items := make([]registryepisodic.MemoryItem, 0, len(rows))
	for _, row := range rows {
		ns, _ := e.decodeNS(row.Namespace)
		item, err := e.rowToItem(row, ns)
		if err != nil {
			log.Warn("Failed to decrypt memory row", "id", row.ID, "err", err)
			continue
		}
		items = append(items, *item)
	}
	return items, nil
}

// ListNamespaces returns distinct active namespaces under the given prefix.
func (e *sqliteEpisodicStore) ListNamespaces(ctx context.Context, req registryepisodic.ListNamespacesRequest) ([][]string, error) {
	var rawNS []string
	q := e.dbFor(ctx).
		Table("memories").
		Select("DISTINCT namespace").
		Where("deleted_at IS NULL")
	if len(req.Prefix) > 0 {
		nsEncoded, err := e.encodeNS(req.Prefix)
		if err != nil {
			return nil, err
		}
		q = q.Where("namespace = ? OR namespace LIKE ?", nsEncoded, episodic.NamespacePrefixPattern(nsEncoded))
	}
	result := q.Pluck("namespace", &rawNS)
	if result.Error != nil {
		return nil, fmt.Errorf("list namespaces: %w", result.Error)
	}

	// Decode, filter by suffix, truncate to maxDepth, and deduplicate.
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
		decoded, err := e.decodeNS(truncated)
		if err != nil {
			continue
		}
		out = append(out, decoded)
	}
	return out, nil
}

// FindMemoriesPendingIndexing returns memories where indexed_at IS NULL.
// For active rows (deleted_at IS NULL) the Value field is decrypted JSON.
// For soft-deleted rows the Value field is nil (only vector removal is needed).
func (e *sqliteEpisodicStore) FindMemoriesPendingIndexing(ctx context.Context, limit int) ([]registryepisodic.PendingMemory, error) {
	var rows []memoryRow
	if err := e.dbFor(ctx).
		Where("indexed_at IS NULL").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("find pending indexing: %w", err)
	}
	out := make([]registryepisodic.PendingMemory, 0, len(rows))
	for _, row := range rows {
		pm := registryepisodic.PendingMemory{
			ID:               row.ID,
			Namespace:        row.Namespace,
			PolicyAttributes: row.PolicyAttributes,
			IndexedContent:   row.IndexedContent,
			DeletedAt:        row.DeletedAt,
		}
		out = append(out, pm)
	}
	return out, nil
}

// SetMemoryIndexedAt marks a memory as indexed.
func (e *sqliteEpisodicStore) SetMemoryIndexedAt(ctx context.Context, memoryID uuid.UUID, indexedAt time.Time) error {
	return e.writeDBFor(ctx, "sqlite episodic store set indexed at").Exec(
		"UPDATE memories SET indexed_at = ? WHERE id = ?", indexedAt, memoryID,
	).Error
}

// UpsertMemoryVectors upserts vector embeddings (stored in memory_vectors via raw SQL — no pgvector driver required here).
func (e *sqliteEpisodicStore) UpsertMemoryVectors(ctx context.Context, items []registryepisodic.MemoryVectorUpsert) error {
	if len(items) == 0 {
		return nil
	}
	if e.qdrant != nil {
		return e.qdrant.UpsertMemoryVectors(ctx, items)
	}
	tx := e.writeDBFor(ctx, "sqlite episodic store upsert memory vectors")
	for _, item := range items {
		vectorBlob, err := vec.SerializeFloat32(item.Embedding)
		if err != nil {
			return fmt.Errorf("serialize memory vector %s/%s: %w", item.MemoryID, item.FieldName, err)
		}
		policyAttributes, err := marshalJSONText(item.PolicyAttributes)
		if err != nil {
			return fmt.Errorf("marshal memory vector attributes %s/%s: %w", item.MemoryID, item.FieldName, err)
		}
		if err := tx.Exec(`
			INSERT INTO memory_vectors (memory_id, field_name, namespace, policy_attributes, embedding)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (memory_id, field_name)
			DO UPDATE SET
			  namespace = excluded.namespace,
			  policy_attributes = excluded.policy_attributes,
			  embedding = excluded.embedding`,
			item.MemoryID, item.FieldName, item.Namespace, policyAttributes, vectorBlob,
		).Error; err != nil {
			return fmt.Errorf("upsert memory vector %s/%s: %w", item.MemoryID, item.FieldName, err)
		}
	}
	return nil
}

// DeleteMemoryVectors removes all vector rows for the given memory_id.
func (e *sqliteEpisodicStore) DeleteMemoryVectors(ctx context.Context, memoryID uuid.UUID) error {
	if e.qdrant != nil {
		return e.qdrant.DeleteMemoryVectors(ctx, memoryID)
	}
	return e.writeDBFor(ctx, "sqlite episodic store delete memory vectors").Exec(
		"DELETE FROM memory_vectors WHERE memory_id = ?", memoryID,
	).Error
}

// SearchMemoryVectors performs ANN search via pgvector (raw SQL).
// This is a fallback; the indexer service calls the vector store directly for ANN.
func (e *sqliteEpisodicStore) SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter map[string]interface{}, limit int) ([]registryepisodic.MemoryVectorSearch, error) {
	if e.qdrant != nil {
		return e.qdrant.SearchMemoryVectors(ctx, namespacePrefix, embedding, filter, limit)
	}
	if limit <= 0 {
		return nil, nil
	}
	queryVector, err := vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("serialize query vector: %w", err)
	}

	var whereFilter string
	var args []interface{}
	args = append(args, queryVector, namespacePrefix, episodic.NamespacePrefixPattern(namespacePrefix))
	if len(filter) > 0 {
		clause, filterArgs := buildSQLFilter(filter)
		if clause != "" {
			whereFilter = " AND " + clause
			args = append(args, filterArgs...)
		}
	}
	args = append(args, limit)

	query := `
		SELECT memory_id, MAX(1.0 - vec_distance_cosine(embedding, ?)) AS score
		FROM memory_vectors
		WHERE (namespace = ? OR namespace LIKE ?)` + whereFilter + `
		GROUP BY memory_id
		ORDER BY score DESC, memory_id ASC
		LIMIT ?`

	rows, err := e.dbFor(ctx).Raw(query, args...).Rows()
	if err != nil {
		return nil, fmt.Errorf("search memory vectors: %w", err)
	}
	defer rows.Close()

	var out []registryepisodic.MemoryVectorSearch
	for rows.Next() {
		var item registryepisodic.MemoryVectorSearch
		if err := rows.Scan(&item.MemoryID, &item.Score); err != nil {
			return nil, fmt.Errorf("scan memory vectors: %w", err)
		}
		out = append(out, item)
	}
	return out, nil
}

// GetMemoriesByIDs retrieves active memories by UUID.
func (e *sqliteEpisodicStore) GetMemoriesByIDs(ctx context.Context, ids []uuid.UUID) ([]registryepisodic.MemoryItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []memoryRow
	if err := e.dbFor(ctx).
		Where("id IN ? AND deleted_at IS NULL", ids).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get memories by ids: %w", err)
	}
	items := make([]registryepisodic.MemoryItem, 0, len(rows))
	for _, row := range rows {
		ns, _ := e.decodeNS(row.Namespace)
		item, err := e.rowToItem(row, ns)
		if err != nil {
			log.Warn("Failed to decrypt memory", "id", row.ID, "err", err)
			continue
		}
		items = append(items, *item)
	}
	return items, nil
}

// ExpireMemories soft-deletes memories whose TTL has elapsed.
func (e *sqliteEpisodicStore) ExpireMemories(ctx context.Context) (int64, error) {
	deletedReason := int16(2)
	now := time.Now().UTC()
	result := e.writeDBFor(ctx, "sqlite episodic store expire memories").Exec(`
		UPDATE memories
		SET deleted_at = ?, indexed_at = NULL, deleted_reason = ?
		WHERE expires_at <= ? AND deleted_at IS NULL`,
		now, deletedReason, now,
	)
	return result.RowsAffected, result.Error
}

// HardDeleteEvictableUpdates hard-deletes rows with deleted_reason=0 (superseded by update)
// that have been re-indexed (indexed_at IS NOT NULL). Returns the number deleted.
func (e *sqliteEpisodicStore) HardDeleteEvictableUpdates(ctx context.Context, limit int) (int64, error) {
	result := e.writeDBFor(ctx, "sqlite episodic store hard delete evictable updates").Exec(`
		DELETE FROM memories
		WHERE id IN (
			SELECT id FROM memories
			WHERE deleted_reason = 0 AND indexed_at IS NOT NULL
			ORDER BY deleted_at ASC
			LIMIT ?
		)`, limit)
	return result.RowsAffected, result.Error
}

// TombstoneDeletedMemories clears encrypted data from rows with deleted_reason IN (1,2)
// that have been re-indexed (indexed_at IS NOT NULL). Returns the number tombstoned.
func (e *sqliteEpisodicStore) TombstoneDeletedMemories(ctx context.Context, limit int) (int64, error) {
	result := e.writeDBFor(ctx, "sqlite episodic store tombstone deleted memories").Exec(`
		UPDATE memories
		SET value_encrypted = NULL
		WHERE id IN (
			SELECT id FROM memories
			WHERE deleted_reason IN (1, 2) AND indexed_at IS NOT NULL AND value_encrypted IS NOT NULL
			ORDER BY deleted_at ASC
			LIMIT ?
		)`, limit)
	return result.RowsAffected, result.Error
}

// HardDeleteExpiredTombstones hard-deletes tombstone rows older than olderThan.
// Returns the number deleted.
func (e *sqliteEpisodicStore) HardDeleteExpiredTombstones(ctx context.Context, olderThan time.Time, limit int) (int64, error) {
	result := e.writeDBFor(ctx, "sqlite episodic store hard delete expired tombstones").Exec(`
		DELETE FROM memories
		WHERE id IN (
			SELECT id FROM memories
			WHERE deleted_reason IN (1, 2) AND value_encrypted IS NULL AND deleted_at <= ?
			ORDER BY deleted_at ASC
			LIMIT ?
		)`, olderThan, limit)
	return result.RowsAffected, result.Error
}

// ListMemoryEvents returns a paginated, time-ordered stream of memory lifecycle events.
// Write events come from rows with kind IN (0,1); delete/expired events come from
// rows with deleted_reason IN (1,2).
func (e *sqliteEpisodicStore) ListMemoryEvents(ctx context.Context, req registryepisodic.ListEventsRequest) (*registryepisodic.MemoryEventPage, error) {
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

	// Determine which kinds to include in each sub-query.
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
	var nsFilter string
	var nsArgs []interface{}
	if len(req.NamespacePrefix) > 0 {
		enc, err := e.encodeNS(req.NamespacePrefix)
		if err != nil {
			return nil, err
		}
		nsFilter = " AND (e.namespace = ? OR e.namespace LIKE ?)"
		nsArgs = []interface{}{enc, episodic.NamespacePrefixPattern(enc)}
	}

	// Build sub-queries and UNION them.
	var parts []string
	var args []interface{}

	writeKinds := []interface{}{}
	if includeAdd {
		writeKinds = append(writeKinds, int16(0))
	}
	if includeUpdate {
		writeKinds = append(writeKinds, int16(1))
	}
	if len(writeKinds) > 0 {
		ph := strings.Repeat("?,", len(writeKinds))
		ph = ph[:len(ph)-1]
		parts = append(parts, `
			SELECT id, namespace, key,
				CASE kind WHEN 0 THEN 'add' ELSE 'update' END AS event_kind,
				created_at AS occurred_at,
				value_encrypted, policy_attributes, expires_at
			FROM memories WHERE kind IN (`+ph+`)`)
		args = append(args, writeKinds...)
	}

	deleteReasons := []interface{}{}
	if includeDelete {
		deleteReasons = append(deleteReasons, int16(1))
	}
	if includeExpired {
		deleteReasons = append(deleteReasons, int16(2))
	}
	if len(deleteReasons) > 0 {
		ph := strings.Repeat("?,", len(deleteReasons))
		ph = ph[:len(ph)-1]
		parts = append(parts, `
			SELECT id, namespace, key,
				CASE deleted_reason WHEN 1 THEN 'delete' ELSE 'expired' END AS event_kind,
				deleted_at AS occurred_at,
				CAST(NULL AS BLOB) AS value_encrypted, CAST(NULL AS TEXT) AS policy_attributes, expires_at
			FROM memories WHERE deleted_reason IN (`+ph+`)`)
		args = append(args, deleteReasons...)
	}

	if len(parts) == 0 {
		return &registryepisodic.MemoryEventPage{}, nil
	}

	union := strings.Join(parts, " UNION ALL ")
	outerWhere := "1=1"
	var outerArgs []interface{}

	if !cursorOccurredAt.IsZero() {
		outerWhere += " AND (e.occurred_at > ? OR (e.occurred_at = ? AND e.id > ?))"
		outerArgs = append(outerArgs, cursorOccurredAt, cursorOccurredAt, cursorID)
	}
	if req.After != nil {
		outerWhere += " AND e.occurred_at > ?"
		outerArgs = append(outerArgs, req.After)
	}
	if req.Before != nil {
		outerWhere += " AND e.occurred_at < ?"
		outerArgs = append(outerArgs, req.Before)
	}

	finalSQL := fmt.Sprintf(`
		SELECT e.id, e.namespace, e.key, e.event_kind, e.occurred_at, e.value_encrypted, e.policy_attributes, e.expires_at
		FROM (%s) e
		WHERE %s%s
		ORDER BY e.occurred_at ASC, e.id ASC
		LIMIT ?`, union, outerWhere, nsFilter)

	// Assemble all args: inner args, outer args, ns args, limit.
	allArgs := append(args, outerArgs...)
	allArgs = append(allArgs, nsArgs...)
	allArgs = append(allArgs, limit+1) // fetch one extra to detect next page

	type scanRow struct {
		ID             uuid.UUID  `gorm:"column:id"`
		Namespace      string     `gorm:"column:namespace"`
		Key            string     `gorm:"column:key"`
		EventKind      string     `gorm:"column:event_kind"`
		OccurredAt     time.Time  `gorm:"column:occurred_at"`
		ValueEncrypted []byte     `gorm:"column:value_encrypted"`
		PolicyAttrsRaw []byte     `gorm:"column:policy_attributes"`
		ExpiresAt      *time.Time `gorm:"column:expires_at"`
	}
	var rows []scanRow
	if err := e.dbFor(ctx).Raw(finalSQL, allArgs...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list memory events: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	events := make([]registryepisodic.MemoryEvent, 0, len(rows))
	for _, row := range rows {
		ns, _ := e.decodeNS(row.Namespace)

		var value map[string]interface{}
		var attrs map[string]interface{}
		if row.EventKind == registryepisodic.EventKindAdd || row.EventKind == registryepisodic.EventKindUpdate {
			if len(row.ValueEncrypted) > 0 {
				plain, err := e.s.decrypt(row.ValueEncrypted)
				if err == nil {
					_ = json.Unmarshal(plain, &value)
				}
			}
			if len(row.PolicyAttrsRaw) > 0 {
				_ = json.Unmarshal(row.PolicyAttrsRaw, &attrs)
			}
		}

		events = append(events, registryepisodic.MemoryEvent{
			ID:         row.ID,
			Namespace:  ns,
			Key:        row.Key,
			Kind:       row.EventKind,
			OccurredAt: row.OccurredAt,
			Value:      value,
			Attributes: attrs,
			ExpiresAt:  row.ExpiresAt,
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

// AdminGetMemoryByID retrieves any memory by UUID.
func (e *sqliteEpisodicStore) AdminGetMemoryByID(ctx context.Context, memoryID uuid.UUID) (*registryepisodic.MemoryItem, error) {
	var row memoryRow
	result := e.dbFor(ctx).Where("id = ?", memoryID).Limit(1).Find(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	ns, _ := e.decodeNS(row.Namespace)
	return e.rowToItem(row, ns)
}

// AdminForceDeleteMemory hard-deletes any memory by UUID.
func (e *sqliteEpisodicStore) AdminForceDeleteMemory(ctx context.Context, memoryID uuid.UUID) error {
	return e.writeDBFor(ctx, "sqlite episodic store admin force delete memory").Exec("DELETE FROM memories WHERE id = ?", memoryID).Error
}

// AdminCountPendingIndexing returns the number of memories pending vector sync.
func (e *sqliteEpisodicStore) AdminCountPendingIndexing(ctx context.Context) (int64, error) {
	var count int64
	err := e.dbFor(ctx).
		Table("memories").
		Where("indexed_at IS NULL").
		Count(&count).Error
	return count, err
}

// rowToItem decrypts a memoryRow into a MemoryItem.
func (e *sqliteEpisodicStore) rowToItem(row memoryRow, namespace []string) (*registryepisodic.MemoryItem, error) {
	// Decrypt value. nil means the row is a tombstone (data cleared after eviction).
	var value map[string]interface{}
	if len(row.ValueEncrypted) > 0 {
		valuePlain, err := e.s.decrypt(row.ValueEncrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt value: %w", err)
		}
		if err := json.Unmarshal(valuePlain, &value); err != nil {
			return nil, fmt.Errorf("unmarshal value: %w", err)
		}
	}

	return &registryepisodic.MemoryItem{
		ID:         row.ID,
		Namespace:  namespace,
		Key:        row.Key,
		Value:      value,
		Attributes: row.PolicyAttributes,
		CreatedAt:  row.CreatedAt,
		ExpiresAt:  row.ExpiresAt,
	}, nil
}

// buildSQLFilter builds a WHERE clause fragment using the shared helper.
// Returns the clause and args, ready for gorm.DB.Where(clause, args...).
func buildSQLFilter(filter map[string]interface{}) (string, []interface{}) {
	if len(filter) == 0 {
		return "", nil
	}
	var clauses []string
	var args []interface{}

	for key, val := range filter {
		jsonPath := sqliteJSONPath(key)
		switch v := val.(type) {
		case map[string]interface{}:
			if members, ok := v["in"]; ok {
				list := toIfaceSlice(members)
				if len(list) > 0 {
					ph := make([]string, len(list))
					inArgs := make([]interface{}, 0, len(list)+1)
					inArgs = append(inArgs, jsonPath)
					for i, m := range list {
						ph[i] = "?"
						inArgs = append(inArgs, sqliteJSONScalar(m))
					}
					clauses = append(clauses, fmt.Sprintf("json_extract(policy_attributes, ?) IN (%s)", strings.Join(ph, ",")))
					args = append(args, inArgs...)
				}
			}
			for op, rhs := range v {
				var sqlOp string
				switch op {
				case "gt":
					sqlOp = ">"
				case "gte":
					sqlOp = ">="
				case "lt":
					sqlOp = "<"
				case "lte":
					sqlOp = "<="
				default:
					continue
				}
				clauses = append(clauses, fmt.Sprintf("CAST(json_extract(policy_attributes, ?) AS REAL) %s ?", sqlOp))
				args = append(args, jsonPath, sqliteNumericScalar(rhs))
			}
		default:
			args = append(args, jsonPath, sqliteJSONScalar(v))
			clauses = append(clauses, "json_extract(policy_attributes, ?) = ?")
		}
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return strings.Join(clauses, " AND "), args
}

func toIfaceSlice(v interface{}) []interface{} {
	if s, ok := v.([]interface{}); ok {
		return s
	}
	return nil
}

func marshalJSONText(v interface{}) (interface{}, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func sqliteJSONPath(key string) string {
	escaped := strings.ReplaceAll(key, `"`, `\"`)
	return `$."` + escaped + `"`
}

func sqliteJSONScalar(v interface{}) interface{} {
	switch t := v.(type) {
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		return t
	default:
		return t
	}
}

func sqliteNumericScalar(v interface{}) interface{} {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case string:
		if i, err := strconv.ParseInt(t, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f
		}
		return t
	default:
		return t
	}
}
