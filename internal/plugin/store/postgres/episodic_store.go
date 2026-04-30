//go:build !nopostgresql

package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	pgvec "github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func init() {
	registryepisodic.Register(registryepisodic.Plugin{
		Name: "postgres",
		Loader: func(ctx context.Context) (registryepisodic.EpisodicStore, error) {
			cfg := config.FromContext(ctx)
			db, err := gorm.Open(postgres.Open(cfg.DBURL), &gorm.Config{})
			if err != nil {
				return nil, fmt.Errorf("episodic store: failed to connect to postgres: %w", err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				return nil, err
			}
			sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)
			sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)

			ps := &PostgresStore{db: db, cfg: cfg}
			if !cfg.EncryptionDBDisabled {
				ps.enc = dataencryption.FromContext(ctx)
			}
			store := &postgresEpisodicStore{db: db, s: ps}
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

// postgresEpisodicStore implements registryepisodic.EpisodicStore using GORM + PostgreSQL.
type postgresEpisodicStore struct {
	db     *gorm.DB
	s      *PostgresStore // for encrypt/decrypt helpers
	qdrant *episodicqdrant.Client
}

func (e *postgresEpisodicStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(txscope.WithIntent(ctx, txscope.IntentRead))
}

func (e *postgresEpisodicStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(txscope.WithIntent(ctx, txscope.IntentWrite))
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
	Revision         int64                  `gorm:"not null;default:1;column:revision"`
	DeletedReason    *int16                 `gorm:"column:deleted_reason"`
	CreatedAt        time.Time              `gorm:"not null;column:created_at"`
	ExpiresAt        *time.Time             `gorm:"column:expires_at"`
	ArchivedAt       *time.Time             `gorm:"column:archived_at"`
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

func matchesMemoryArchiveFilter(archivedAt *time.Time, deletedReason *int16, archived registryepisodic.ArchiveFilter) bool {
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

func postgresMemoryArchiveWhere(alias string, archived registryepisodic.ArchiveFilter) string {
	switch archived {
	case registryepisodic.ArchiveFilterInclude:
		return fmt.Sprintf("(%s.archived_at IS NULL OR %s.deleted_reason = 1)", alias, alias)
	case registryepisodic.ArchiveFilterOnly:
		return fmt.Sprintf("%s.deleted_reason = 1", alias)
	default:
		return fmt.Sprintf("%s.archived_at IS NULL", alias)
	}
}

func (e *postgresEpisodicStore) encodeNS(ns []string) (string, error) {
	// Pass 0 as maxDepth to skip depth check (checked in handler).
	return episodic.EncodeNamespace(ns, 0)
}

func (e *postgresEpisodicStore) decodeNS(encoded string) ([]string, error) {
	return episodic.DecodeNamespace(encoded)
}

// PutMemory upserts a memory. On update, the previous active row is archived.
func (e *postgresEpisodicStore) PutMemory(ctx context.Context, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
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
	revision := int64(1)
	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active []memoryRow
		if err := tx.Raw(`
			SELECT *
			FROM memories
			WHERE namespace = ? AND key = ? AND archived_at IS NULL
			ORDER BY created_at DESC, id DESC
			LIMIT 1`,
			nsEncoded, req.Key,
		).Scan(&active).Error; err != nil {
			return fmt.Errorf("load active row: %w", err)
		}
		if req.ExpectedRevision != nil {
			if len(active) == 0 || active[0].Revision != *req.ExpectedRevision {
				return registryepisodic.ErrMemoryRevisionConflict
			}
		}
		if len(active) > 0 {
			revision = active[0].Revision + 1
		}

		// Soft-delete the current active row for this (namespace, key), if any.
		// Set deleted_reason=0 (superseded by update) and reset indexed_at so the indexer
		// removes the old vector entry.
		deletedReason := int16(0)
		result := tx.Exec(`
			UPDATE memories
			SET archived_at = ?, indexed_at = NULL, deleted_reason = ?
			WHERE namespace = ? AND key = ? AND archived_at IS NULL`,
			now, deletedReason, nsEncoded, req.Key,
		)
		if result.Error != nil {
			return fmt.Errorf("archive previous row: %w", result.Error)
		}
		// kind=0 (add) if no previous row existed, kind=1 (update) if one was archived.
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
			Revision:         revision,
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
		Revision:   revision,
	}, nil
}

// GetMemory retrieves the current memory for (namespace, key).
func (e *postgresEpisodicStore) GetMemory(ctx context.Context, namespace []string, key string, archived registryepisodic.ArchiveFilter) (*registryepisodic.MemoryItem, error) {
	nsEncoded, err := e.encodeNS(namespace)
	if err != nil {
		return nil, err
	}

	var rows []memoryRow
	result := e.db.WithContext(ctx).Raw(`
		SELECT *
		FROM memories
		WHERE namespace = ? AND key = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1`,
		nsEncoded, key,
	).Scan(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("get memory: %w", result.Error)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	row := rows[0]
	if !matchesMemoryArchiveFilter(row.ArchivedAt, row.DeletedReason, archived) {
		return nil, nil
	}
	return e.rowToItem(row, namespace)
}

func (e *postgresEpisodicStore) IncrementMemoryLoads(ctx context.Context, keys []registryepisodic.MemoryKey, fetchedAt time.Time) error {
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
			last_fetched_at = GREATEST(memory_usage_stats.last_fetched_at, EXCLUDED.last_fetched_at)`

	return e.db.WithContext(ctx).Exec(query, args...).Error
}

func (e *postgresEpisodicStore) GetMemoryUsage(ctx context.Context, namespace []string, key string) (*registryepisodic.MemoryUsage, error) {
	nsEncoded, err := e.encodeNS(namespace)
	if err != nil {
		return nil, err
	}

	var row memoryUsageRow
	result := e.db.WithContext(ctx).
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

func (e *postgresEpisodicStore) ListTopMemoryUsage(ctx context.Context, req registryepisodic.ListTopMemoryUsageRequest) ([]registryepisodic.TopMemoryUsageItem, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	q := e.db.WithContext(ctx).Table("memory_usage_stats")
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

// ArchiveMemory archives the active memory for (namespace, key).
func (e *postgresEpisodicStore) ArchiveMemory(ctx context.Context, namespace []string, key string, expectedRevision *int64) error {
	nsEncoded, err := e.encodeNS(namespace)
	if err != nil {
		return err
	}
	deletedReason := int16(1)
	args := []interface{}{deletedReason, nsEncoded, key}
	where := "namespace = ? AND key = ? AND archived_at IS NULL"
	if expectedRevision != nil {
		where += " AND revision = ?"
		args = append(args, *expectedRevision)
	}
	result := e.db.WithContext(ctx).Exec(`
		UPDATE memories
		SET archived_at = NOW(), indexed_at = NULL, deleted_reason = ?, revision = revision + 1
		WHERE `+where,
		args...,
	)
	if result.Error != nil {
		return result.Error
	}
	if expectedRevision != nil && result.RowsAffected == 0 {
		return registryepisodic.ErrMemoryRevisionConflict
	}
	return nil
}

// SearchMemories performs attribute-filter-only search within the namespace prefix.
func (e *postgresEpisodicStore) SearchMemories(ctx context.Context, namespacePrefix []string, filter registryepisodic.AttributeFilter, limit int, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryItem, error) {
	nsEncoded, err := e.encodeNS(namespacePrefix)
	if err != nil {
		return nil, err
	}

	q := e.db.WithContext(ctx).
		Table("memories AS m").
		Select("m.*").
		Joins(`
			JOIN (
				SELECT namespace, key, MAX(created_at) AS max_created_at
				FROM memories
				WHERE namespace = ? OR namespace LIKE ?
				GROUP BY namespace, key
			) latest
			ON m.namespace = latest.namespace
			AND m.key = latest.key
			AND m.created_at = latest.max_created_at`,
			nsEncoded, episodic.NamespacePrefixPattern(nsEncoded),
		).
		Where(postgresMemoryArchiveWhere("m", archived)).
		Order("m.created_at DESC, m.id DESC").
		Limit(limit)

	if !filter.Empty() {
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

// ListNamespaces returns distinct current namespaces under the given prefix.
func (e *postgresEpisodicStore) ListNamespaces(ctx context.Context, req registryepisodic.ListNamespacesRequest) ([][]string, error) {
	var rawNS []string
	q := e.db.WithContext(ctx).Table("memories AS m").Select("DISTINCT m.namespace")
	if len(req.Prefix) > 0 {
		nsEncoded, err := e.encodeNS(req.Prefix)
		if err != nil {
			return nil, err
		}
		q = q.Joins(`
			JOIN (
				SELECT namespace, key, MAX(created_at) AS max_created_at
				FROM memories
				WHERE namespace = ? OR namespace LIKE ?
				GROUP BY namespace, key
			) latest
			ON m.namespace = latest.namespace
			AND m.key = latest.key
			AND m.created_at = latest.max_created_at`,
			nsEncoded, episodic.NamespacePrefixPattern(nsEncoded),
		)
	} else {
		q = q.Joins(`
			JOIN (
				SELECT namespace, key, MAX(created_at) AS max_created_at
				FROM memories
				GROUP BY namespace, key
			) latest
			ON m.namespace = latest.namespace
			AND m.key = latest.key
			AND m.created_at = latest.max_created_at`)
	}
	q = q.Where(postgresMemoryArchiveWhere("m", req.Archived))
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
// Archived rows remain eligible so the indexer can either preserve archived-search vectors
// or remove vectors for expired/superseded rows based on deleted_reason.
func (e *postgresEpisodicStore) FindMemoriesPendingIndexing(ctx context.Context, limit int) ([]registryepisodic.PendingMemory, error) {
	var rows []memoryRow
	if err := e.db.WithContext(ctx).
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
			ArchivedAt:       row.ArchivedAt,
		}
		if row.DeletedReason != nil {
			value := int32(*row.DeletedReason)
			pm.DeletedReason = &value
		}
		out = append(out, pm)
	}
	return out, nil
}

// SetMemoryIndexedAt marks a memory as indexed.
func (e *postgresEpisodicStore) SetMemoryIndexedAt(ctx context.Context, memoryID uuid.UUID, indexedAt time.Time) error {
	return e.db.WithContext(ctx).Exec(
		"UPDATE memories SET indexed_at = ? WHERE id = ?", indexedAt, memoryID,
	).Error
}

// UpsertMemoryVectors upserts vector embeddings (stored in memory_vectors via raw SQL — no pgvector driver required here).
func (e *postgresEpisodicStore) UpsertMemoryVectors(ctx context.Context, items []registryepisodic.MemoryVectorUpsert) error {
	if len(items) == 0 {
		return nil
	}
	if e.qdrant != nil {
		return e.qdrant.UpsertMemoryVectors(ctx, items)
	}
	tx := e.db.WithContext(ctx)
	for _, item := range items {
		vec := pgvec.NewVector(item.Embedding)
		if err := tx.Exec(`
			INSERT INTO memory_vectors (memory_id, field_name, namespace, policy_attributes, embedding)
			VALUES (?, ?, ?, ?, ?::vector)
			ON CONFLICT (memory_id, field_name)
			DO UPDATE SET
			  namespace = EXCLUDED.namespace,
			  policy_attributes = EXCLUDED.policy_attributes,
			  embedding = EXCLUDED.embedding`,
			item.MemoryID, item.FieldName, item.Namespace, item.PolicyAttributes, vec,
		).Error; err != nil {
			return fmt.Errorf("upsert memory vector %s/%s: %w", item.MemoryID, item.FieldName, err)
		}
	}
	return nil
}

// DeleteMemoryVectors removes all vector rows for the given memory_id.
func (e *postgresEpisodicStore) DeleteMemoryVectors(ctx context.Context, memoryID uuid.UUID) error {
	if e.qdrant != nil {
		return e.qdrant.DeleteMemoryVectors(ctx, memoryID)
	}
	return e.db.WithContext(ctx).Exec(
		"DELETE FROM memory_vectors WHERE memory_id = ?", memoryID,
	).Error
}

// SearchMemoryVectors performs ANN search via pgvector (raw SQL).
// This is a fallback; the indexer service calls the vector store directly for ANN.
func (e *postgresEpisodicStore) SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter registryepisodic.AttributeFilter, limit int, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryVectorSearch, error) {
	if e.qdrant != nil {
		return e.qdrant.SearchMemoryVectors(ctx, namespacePrefix, embedding, filter, limit, archived)
	}
	if limit <= 0 {
		return nil, nil
	}
	vec := pgvec.NewVector(embedding)

	var whereFilter string
	var args []interface{}
	args = append(args, vec, namespacePrefix, episodic.NamespacePrefixPattern(namespacePrefix))
	if !filter.Empty() {
		clause, filterArgs := buildSQLFilter(filter)
		if clause != "" {
			clause = strings.ReplaceAll(clause, "policy_attributes", "mv.policy_attributes")
			whereFilter = " AND " + clause
			args = append(args, filterArgs...)
		}
	}
	args = append(args, limit)

	query := `
		SELECT memory_id, MAX(1 - (embedding <=> ?::vector)) AS score
		FROM memory_vectors mv
		JOIN memories m ON m.id = mv.memory_id
		WHERE (mv.namespace = ? OR mv.namespace LIKE ?)
		  AND ` + postgresMemoryArchiveWhere("m", archived) + whereFilter + `
		GROUP BY memory_id
		ORDER BY score DESC
		LIMIT ?`

	rows, err := e.db.WithContext(ctx).Raw(query, args...).Rows()
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
func (e *postgresEpisodicStore) GetMemoriesByIDs(ctx context.Context, ids []uuid.UUID, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []memoryRow
	if err := e.db.WithContext(ctx).
		Where("id IN ?", ids).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get memories by ids: %w", err)
	}
	items := make([]registryepisodic.MemoryItem, 0, len(rows))
	for _, row := range rows {
		if !matchesMemoryArchiveFilter(row.ArchivedAt, row.DeletedReason, archived) {
			continue
		}
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

// ExpireMemories archives memories whose TTL has elapsed.
func (e *postgresEpisodicStore) ExpireMemories(ctx context.Context) (int64, error) {
	deletedReason := int16(2)
	result := e.db.WithContext(ctx).Exec(`
		UPDATE memories
		SET archived_at = NOW(), indexed_at = NULL, deleted_reason = ?
		WHERE expires_at <= NOW() AND archived_at IS NULL`,
		deletedReason,
	)
	return result.RowsAffected, result.Error
}

// HardDeleteEvictableUpdates hard-deletes rows with deleted_reason=0 (superseded by update)
// that have been re-indexed (indexed_at IS NOT NULL). Returns the number deleted.
func (e *postgresEpisodicStore) HardDeleteEvictableUpdates(ctx context.Context, limit int) (int64, error) {
	result := e.db.WithContext(ctx).Exec(`
		DELETE FROM memories
		WHERE id IN (
			SELECT id FROM memories
			WHERE deleted_reason = 0 AND indexed_at IS NOT NULL
			ORDER BY archived_at ASC
			LIMIT ?
		)`, limit)
	return result.RowsAffected, result.Error
}

// TombstoneDeletedMemories clears encrypted data from rows with deleted_reason IN (1,2)
// that have been re-indexed (indexed_at IS NOT NULL). Returns the number tombstoned.
func (e *postgresEpisodicStore) TombstoneDeletedMemories(ctx context.Context, limit int) (int64, error) {
	result := e.db.WithContext(ctx).Exec(`
		UPDATE memories
		SET value_encrypted = NULL
		WHERE id IN (
			SELECT id FROM memories
			WHERE deleted_reason IN (1, 2) AND indexed_at IS NOT NULL AND value_encrypted IS NOT NULL
			ORDER BY archived_at ASC
			LIMIT ?
		)`, limit)
	return result.RowsAffected, result.Error
}

// HardDeleteExpiredTombstones hard-deletes tombstone rows older than olderThan.
// Returns the number deleted.
func (e *postgresEpisodicStore) HardDeleteExpiredTombstones(ctx context.Context, olderThan time.Time, limit int) (int64, error) {
	result := e.db.WithContext(ctx).Exec(`
		DELETE FROM memories
		WHERE id IN (
			SELECT id FROM memories
			WHERE deleted_reason IN (1, 2) AND value_encrypted IS NULL AND archived_at <= ?
			ORDER BY archived_at ASC
			LIMIT ?
		)`, olderThan, limit)
	return result.RowsAffected, result.Error
}

// ListMemoryEvents returns a paginated, time-ordered stream of memory lifecycle events.
// Write events come from rows with kind IN (0,1); delete/expired events come from
// rows with deleted_reason IN (1,2).
func (e *postgresEpisodicStore) ListMemoryEvents(ctx context.Context, req registryepisodic.ListEventsRequest) (*registryepisodic.MemoryEventPage, error) {
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

	if includeUpdate {
		parts = append(parts, `
			SELECT id, namespace, key,
				'update' AS event_kind,
				archived_at AS occurred_at,
				value_encrypted, policy_attributes, expires_at
			FROM memories WHERE deleted_reason = 1`)
	}
	if includeExpired {
		parts = append(parts, `
			SELECT id, namespace, key,
				'expired' AS event_kind,
				archived_at AS occurred_at,
				NULL::bytea AS value_encrypted, NULL::jsonb AS policy_attributes, expires_at
			FROM memories WHERE deleted_reason = 2`)
	}

	if len(parts) == 0 {
		return &registryepisodic.MemoryEventPage{}, nil
	}

	union := strings.Join(parts, " UNION ALL ")
	outerWhere := "1=1"
	var outerArgs []interface{}

	if !cursorOccurredAt.IsZero() {
		outerWhere += " AND (e.occurred_at > ? OR (e.occurred_at = ? AND e.id::text > ?))"
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
	if err := e.db.WithContext(ctx).Raw(finalSQL, allArgs...).Scan(&rows).Error; err != nil {
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
func (e *postgresEpisodicStore) AdminGetMemoryByID(ctx context.Context, memoryID uuid.UUID) (*registryepisodic.MemoryItem, error) {
	var row memoryRow
	result := e.db.WithContext(ctx).Where("id = ?", memoryID).Limit(1).Find(&row)
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
func (e *postgresEpisodicStore) AdminForceDeleteMemory(ctx context.Context, memoryID uuid.UUID) error {
	return e.db.WithContext(ctx).Exec("DELETE FROM memories WHERE id = ?", memoryID).Error
}

// AdminCountPendingIndexing returns the number of memories pending vector sync.
func (e *postgresEpisodicStore) AdminCountPendingIndexing(ctx context.Context) (int64, error) {
	var count int64
	err := e.db.WithContext(ctx).
		Table("memories").
		Where("indexed_at IS NULL").
		Count(&count).Error
	return count, err
}

// rowToItem decrypts a memoryRow into a MemoryItem.
func (e *postgresEpisodicStore) rowToItem(row memoryRow, namespace []string) (*registryepisodic.MemoryItem, error) {
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
		ArchivedAt: row.ArchivedAt,
		Revision:   row.Revision,
	}, nil
}

// buildSQLFilter builds a WHERE clause fragment using the shared helper.
// Returns the clause and args, ready for gorm.DB.Where(clause, args...).
func buildSQLFilter(filter registryepisodic.AttributeFilter) (string, []interface{}) {
	if filter.Empty() {
		return "", nil
	}
	var clauses []string
	var args []interface{}
	for _, cond := range filter.Conditions {
		switch cond.Op {
		case registryepisodic.AttributeFilterOpEq:
			value := cond.Values[0]
			arrayJSON, _ := json.Marshal([]interface{}{value.Raw})
			clauses = append(clauses, "((jsonb_typeof(policy_attributes -> ?) <> 'array' AND policy_attributes->>? = ?) OR (jsonb_typeof(policy_attributes -> ?) = 'array' AND (policy_attributes -> ?) @> ?::jsonb))")
			args = append(args, cond.Field, cond.Field, value.Text, cond.Field, cond.Field, string(arrayJSON))
		case registryepisodic.AttributeFilterOpIn:
			var parts []string
			for _, value := range cond.Values {
				arrayJSON, _ := json.Marshal([]interface{}{value.Raw})
				parts = append(parts, "((jsonb_typeof(policy_attributes -> ?) <> 'array' AND policy_attributes->>? = ?) OR (jsonb_typeof(policy_attributes -> ?) = 'array' AND (policy_attributes -> ?) @> ?::jsonb))")
				args = append(args, cond.Field, cond.Field, value.Text, cond.Field, cond.Field, string(arrayJSON))
			}
			clauses = append(clauses, "("+strings.Join(parts, " OR ")+")")
		case registryepisodic.AttributeFilterOpExists:
			clauses = append(clauses, "((policy_attributes -> ?) IS NOT NULL AND (policy_attributes -> ?) <> 'null'::jsonb AND (jsonb_typeof(policy_attributes -> ?) <> 'array' OR jsonb_array_length(policy_attributes -> ?) > 0))")
			args = append(args, cond.Field, cond.Field, cond.Field, cond.Field)
		case registryepisodic.AttributeFilterOpGte, registryepisodic.AttributeFilterOpLte:
			sqlOp := ">="
			if cond.Op == registryepisodic.AttributeFilterOpLte {
				sqlOp = "<="
			}
			value := cond.Values[0]
			if cond.RangeKind == registryepisodic.AttributeFilterRangeTime {
				clauses = append(clauses, fmt.Sprintf("(jsonb_typeof(policy_attributes -> ?) = 'string' AND (policy_attributes->>?)::timestamptz %s ?::timestamptz)", sqlOp))
				args = append(args, cond.Field, cond.Field, value.Text)
			} else {
				clauses = append(clauses, fmt.Sprintf("(jsonb_typeof(policy_attributes -> ?) = 'number' AND (policy_attributes->>?)::double precision %s ?)", sqlOp))
				args = append(args, cond.Field, cond.Field, value.Raw)
			}
		}
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return strings.Join(clauses, " AND "), args
}
