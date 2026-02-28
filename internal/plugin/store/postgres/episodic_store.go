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

// memoryRow is the GORM-level row for the memories table.
type memoryRow struct {
	ID               uuid.UUID              `gorm:"primaryKey;type:uuid;column:id"`
	Namespace        string                 `gorm:"not null;column:namespace"`
	Key              string                 `gorm:"not null;column:key"`
	ValueEncrypted   []byte                 `gorm:"column:value_encrypted"` // nullable for tombstones
	Attributes       []byte                 `gorm:"column:attributes"`
	PolicyAttributes map[string]interface{} `gorm:"type:jsonb;serializer:json;column:policy_attributes"`
	IndexFields      []string               `gorm:"type:jsonb;serializer:json;column:index_fields"`
	IndexDisabled    bool                   `gorm:"column:index_disabled"`
	Kind             int16                  `gorm:"not null;default:0;column:kind"`
	DeletedReason    *int16                 `gorm:"column:deleted_reason"`
	CreatedAt        time.Time              `gorm:"not null;column:created_at"`
	ExpiresAt        *time.Time             `gorm:"column:expires_at"`
	DeletedAt        *time.Time             `gorm:"column:deleted_at"`
	IndexedAt        *time.Time             `gorm:"column:indexed_at"`
}

func (memoryRow) TableName() string { return "memories" }

func (e *postgresEpisodicStore) encodeNS(ns []string) (string, error) {
	// Pass 0 as maxDepth to skip depth check (checked in handler).
	return episodic.EncodeNamespace(ns, 0)
}

func (e *postgresEpisodicStore) decodeNS(encoded string) ([]string, error) {
	return episodic.DecodeNamespace(encoded)
}

// PutMemory upserts a memory. On update, the previous active row is soft-deleted.
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

	// Encrypt the user-supplied attributes (if any).
	var attrsEnc []byte
	if len(req.Attributes) > 0 {
		attrsJSON, err := json.Marshal(req.Attributes)
		if err != nil {
			return nil, fmt.Errorf("marshal attributes: %w", err)
		}
		attrsEnc, err = e.s.encrypt(attrsJSON)
		if err != nil {
			return nil, fmt.Errorf("encrypt attributes: %w", err)
		}
	}

	var expiresAt *time.Time
	if req.TTLSeconds > 0 {
		t := time.Now().Add(time.Duration(req.TTLSeconds) * time.Second)
		expiresAt = &t
	}

	newID := uuid.New()
	now := time.Now()

	var kind int16
	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			Attributes:       attrsEnc,
			PolicyAttributes: req.PolicyAttributes,
			IndexFields:      req.IndexFields,
			IndexDisabled:    req.IndexDisabled,
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

// GetMemory retrieves the active memory for (namespace, key).
func (e *postgresEpisodicStore) GetMemory(ctx context.Context, namespace []string, key string) (*registryepisodic.MemoryItem, error) {
	nsEncoded, err := e.encodeNS(namespace)
	if err != nil {
		return nil, err
	}

	var row memoryRow
	result := e.db.WithContext(ctx).
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

// DeleteMemory soft-deletes the active memory for (namespace, key).
func (e *postgresEpisodicStore) DeleteMemory(ctx context.Context, namespace []string, key string) error {
	nsEncoded, err := e.encodeNS(namespace)
	if err != nil {
		return err
	}
	deletedReason := int16(1)
	return e.db.WithContext(ctx).Exec(`
		UPDATE memories
		SET deleted_at = NOW(), indexed_at = NULL, deleted_reason = ?
		WHERE namespace = ? AND key = ? AND deleted_at IS NULL`,
		deletedReason, nsEncoded, key,
	).Error
}

// SearchMemories performs attribute-filter-only search within the namespace prefix.
func (e *postgresEpisodicStore) SearchMemories(ctx context.Context, namespacePrefix []string, filter map[string]interface{}, limit, offset int) ([]registryepisodic.MemoryItem, error) {
	nsEncoded, err := e.encodeNS(namespacePrefix)
	if err != nil {
		return nil, err
	}

	q := e.db.WithContext(ctx).
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
func (e *postgresEpisodicStore) ListNamespaces(ctx context.Context, req registryepisodic.ListNamespacesRequest) ([][]string, error) {
	var rawNS []string
	q := e.db.WithContext(ctx).
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
			IndexFields:      row.IndexFields,
			IndexDisabled:    row.IndexDisabled,
			DeletedAt:        row.DeletedAt,
		}
		// Only decrypt for active rows; soft-deleted rows only need vector removal.
		if row.DeletedAt == nil {
			plain, err := e.s.decrypt(row.ValueEncrypted)
			if err != nil {
				log.Warn("Episodic: failed to decrypt value for indexing", "id", row.ID, "err", err)
			} else {
				pm.Value = plain
			}
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
func (e *postgresEpisodicStore) SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter map[string]interface{}, limit int) ([]registryepisodic.MemoryVectorSearch, error) {
	if e.qdrant != nil {
		return e.qdrant.SearchMemoryVectors(ctx, namespacePrefix, embedding, filter, limit)
	}
	if limit <= 0 {
		return nil, nil
	}
	vec := pgvec.NewVector(embedding)

	var whereFilter string
	var args []interface{}
	args = append(args, vec, namespacePrefix, episodic.NamespacePrefixPattern(namespacePrefix))
	if len(filter) > 0 {
		clause, filterArgs := buildSQLFilter(filter)
		if clause != "" {
			whereFilter = " AND " + clause
			args = append(args, filterArgs...)
		}
	}
	args = append(args, limit)

	query := `
		SELECT memory_id, MAX(1 - (embedding <=> ?::vector)) AS score
		FROM memory_vectors
		WHERE (namespace = ? OR namespace LIKE ?)` + whereFilter + `
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
func (e *postgresEpisodicStore) GetMemoriesByIDs(ctx context.Context, ids []uuid.UUID) ([]registryepisodic.MemoryItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []memoryRow
	if err := e.db.WithContext(ctx).
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
func (e *postgresEpisodicStore) ExpireMemories(ctx context.Context) (int64, error) {
	deletedReason := int16(2)
	result := e.db.WithContext(ctx).Exec(`
		UPDATE memories
		SET deleted_at = NOW(), indexed_at = NULL, deleted_reason = ?
		WHERE expires_at <= NOW() AND deleted_at IS NULL`,
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
			ORDER BY deleted_at ASC
			LIMIT ?
		)`, limit)
	return result.RowsAffected, result.Error
}

// TombstoneDeletedMemories clears encrypted data from rows with deleted_reason IN (1,2)
// that have been re-indexed (indexed_at IS NOT NULL). Returns the number tombstoned.
func (e *postgresEpisodicStore) TombstoneDeletedMemories(ctx context.Context, limit int) (int64, error) {
	result := e.db.WithContext(ctx).Exec(`
		UPDATE memories
		SET value_encrypted = NULL, attributes = NULL
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
func (e *postgresEpisodicStore) HardDeleteExpiredTombstones(ctx context.Context, olderThan time.Time, limit int) (int64, error) {
	result := e.db.WithContext(ctx).Exec(`
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
				value_encrypted, attributes, expires_at
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
				NULL::bytea AS value_encrypted, NULL::bytea AS attributes, expires_at
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
		SELECT e.id, e.namespace, e.key, e.event_kind, e.occurred_at, e.value_encrypted, e.attributes, e.expires_at
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
		Attributes     []byte     `gorm:"column:attributes"`
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
			if len(row.Attributes) > 0 {
				plain, err := e.s.decrypt(row.Attributes)
				if err == nil {
					_ = json.Unmarshal(plain, &attrs)
				}
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

	// Decrypt user-supplied attributes (may be nil).
	var attrs map[string]interface{}
	if len(row.Attributes) > 0 {
		attrsPlain, err := e.s.decrypt(row.Attributes)
		if err != nil {
			return nil, fmt.Errorf("decrypt attributes: %w", err)
		}
		if err := json.Unmarshal(attrsPlain, &attrs); err != nil {
			return nil, fmt.Errorf("unmarshal attributes: %w", err)
		}
	}

	return &registryepisodic.MemoryItem{
		ID:         row.ID,
		Namespace:  namespace,
		Key:        row.Key,
		Value:      value,
		Attributes: attrs,
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
	// Use the episodic package helper but we need positional args compatible with GORM.
	// GORM uses ? placeholders, but episodic.BuildSQLFilter uses $N.
	// Build our own here.
	var clauses []string
	var args []interface{}

	for key, val := range filter {
		safeKey := strings.ReplaceAll(key, "'", "''")
		switch v := val.(type) {
		case map[string]interface{}:
			if members, ok := v["in"]; ok {
				list := toIfaceSlice(members)
				if len(list) > 0 {
					ph := make([]string, len(list))
					for i, m := range list {
						ph[i] = "?"
						args = append(args, jsonScalarStr(m))
					}
					clauses = append(clauses,
						fmt.Sprintf("policy_attributes->>'%s' = ANY(ARRAY[%s]::text[])", safeKey, strings.Join(ph, ",")))
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
				args = append(args, rhs)
				clauses = append(clauses, fmt.Sprintf("(policy_attributes->>'%s')::numeric %s ?", safeKey, sqlOp))
			}
		default:
			args = append(args, jsonScalarStr(v))
			clauses = append(clauses, fmt.Sprintf("policy_attributes->>'%s' = ?", safeKey))
		}
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return strings.Join(clauses, " AND "), args
}

func jsonScalarStr(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(t)
		return strings.Trim(string(b), `"`)
	}
}

func toIfaceSlice(v interface{}) []interface{} {
	if s, ok := v.([]interface{}); ok {
		return s
	}
	return nil
}
