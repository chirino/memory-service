//go:build !nosqlite

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chirino/memory-service/internal/episodic"
	"github.com/chirino/memory-service/internal/model"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	"github.com/google/uuid"
)

// CreateMemoryKindVersion persists an immutable schema version.
// Item 8 fix: uses INSERT OR IGNORE so concurrent races cannot produce a
// duplicate-primary-key error visible to the caller.  After a no-op insert
// (RowsAffected==0) the existing row is reloaded and compared; identical →
// return existing; differing → ErrMemoryKindVersionConflict.
func (e *sqliteEpisodicStore) CreateMemoryKindVersion(ctx context.Context, version model.MemoryKindVersion) (*model.MemoryKindVersion, error) {
	db := e.writeDBFor(ctx, "CreateMemoryKindVersion")
	attrTypesJSON, err := json.Marshal(version.AttributeTypes)
	if err != nil {
		return nil, err
	}
	regoVal := interface{}(nil)
	if version.AttributesRego != nil {
		regoVal = *version.AttributesRego
	}
	res := db.Exec(
		`INSERT OR IGNORE INTO memory_kind_versions (name, attribute_types, attributes_rego, writable, created_at, created_at_ns)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		version.Name, string(attrTypesJSON), regoVal, version.Writable, version.CreatedAt,
	)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected > 0 {
		// New row inserted — return as-is.
		return &version, nil
	}
	// Row already existed; reload to check idempotency.
	var existing model.MemoryKindVersion
	reload := db.Table("memory_kind_versions").Where("name = ?", version.Name).Limit(1).Find(&existing)
	if reload.Error != nil {
		return nil, reload.Error
	}
	if reload.RowsAffected == 0 {
		// Extremely unlikely race (another tx deleted it); treat as conflict.
		return nil, registryepisodic.ErrMemoryKindVersionConflict
	}
	if !schemaVersionsEqual(existing, version) {
		return nil, registryepisodic.ErrMemoryKindVersionConflict
	}
	return &existing, nil
}

// GetMemoryKindVersion retrieves a schema version by canonical name.
func (e *sqliteEpisodicStore) GetMemoryKindVersion(ctx context.Context, name string) (*model.MemoryKindVersion, error) {
	db := e.dbFor(ctx)
	var v model.MemoryKindVersion
	res := db.Table("memory_kind_versions").Where("name = ?", name).Limit(1).Find(&v)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &v, nil
}

// ListMemoryKindVersions lists schema versions, optionally filtered by family.
func (e *sqliteEpisodicStore) ListMemoryKindVersions(ctx context.Context, family string) ([]model.MemoryKindVersion, error) {
	db := e.dbFor(ctx)
	q := db.Table("memory_kind_versions").Order("name ASC")
	if family != "" {
		q = q.Where("name LIKE ?", family+"/%")
	}
	var versions []model.MemoryKindVersion
	if err := q.Find(&versions).Error; err != nil {
		return nil, err
	}
	return versions, nil
}

// CreateMemoryKindMigration persists a new migration.
// Defect 11 fix: translate unique-violation from concurrent races to
// ErrMemoryKindMigrationActiveForSource instead of returning a raw DB error.
func (e *sqliteEpisodicStore) CreateMemoryKindMigration(ctx context.Context, m model.MemoryKindMigration) (*model.MemoryKindMigration, error) {
	db := e.writeDBFor(ctx, "CreateMemoryKindMigration")
	var existing model.MemoryKindMigration
	res := db.Table("memory_kind_migrations").
		Where("source = ? AND state IN ('queued','running','canceling')", m.Source).
		Limit(1).Find(&existing)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected > 0 {
		return nil, registryepisodic.ErrMemoryKindMigrationActiveForSource
	}
	if err := db.Table("memory_kind_migrations").Create(&m).Error; err != nil {
		if _, ok := sqliteUniqueViolation(err); ok {
			// Concurrent race: another goroutine created an active migration just now.
			return nil, registryepisodic.ErrMemoryKindMigrationActiveForSource
		}
		return nil, err
	}
	return &m, nil
}

func (e *sqliteEpisodicStore) CreateMemoryKindMigrationAndTask(ctx context.Context, m model.MemoryKindMigration) (*model.MemoryKindMigration, error) {
	created, err := e.CreateMemoryKindMigration(ctx, m)
	if err != nil {
		return nil, err
	}
	taskName := "migration:" + created.ID.String()
	task := model.Task{
		ID: uuid.New(), TaskName: &taskName, TaskType: "memory_kind_migration",
		TaskBody:  map[string]interface{}{"migration_id": created.ID.String(), "taskName": taskName},
		CreatedAt: time.Now().UTC(), RetryAt: time.Now().UTC(),
	}
	if err := e.dbFor(ctx).Create(&task).Error; err != nil {
		return nil, fmt.Errorf("create initial migration task: %w", err)
	}
	return created, nil
}

func (e *sqliteEpisodicStore) CreateMemoryKindMigrationTask(ctx context.Context, body map[string]interface{}) error {
	var taskName *string
	if raw, ok := body["taskName"].(string); ok && strings.TrimSpace(raw) != "" {
		name := strings.TrimSpace(raw)
		taskName = &name
	}
	task := model.Task{ID: uuid.New(), TaskName: taskName, TaskType: "memory_kind_migration", TaskBody: body, CreatedAt: time.Now().UTC(), RetryAt: time.Now().UTC()}
	err := e.dbFor(ctx).Create(&task).Error
	if taskName != nil {
		if _, duplicate := sqliteUniqueViolation(err); duplicate {
			return nil
		}
	}
	return err
}

// DeleteMemoryKindMigration permanently removes a migration record by UUID.
func (e *sqliteEpisodicStore) DeleteMemoryKindMigration(ctx context.Context, id uuid.UUID) error {
	db := e.writeDBFor(ctx, "DeleteMemoryKindMigration")
	return db.Table("memory_kind_migrations").Where("id = ?", id.String()).Delete(nil).Error
}

// GetMemoryKindMigration retrieves a migration by UUID.
func (e *sqliteEpisodicStore) GetMemoryKindMigration(ctx context.Context, id uuid.UUID) (*model.MemoryKindMigration, error) {
	db := e.dbFor(ctx)
	var m model.MemoryKindMigration
	res := db.Table("memory_kind_migrations").Where("id = ?", id).Limit(1).Find(&m)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &m, nil
}

// ListMemoryKindMigrations lists migrations, optionally filtered by state.
func (e *sqliteEpisodicStore) ListMemoryKindMigrations(ctx context.Context, state string) ([]model.MemoryKindMigration, error) {
	db := e.dbFor(ctx)
	q := db.Table("memory_kind_migrations").Order("created_at DESC, created_at_ns DESC")
	if state != "" {
		q = q.Where("state = ?", state)
	}
	var migrations []model.MemoryKindMigration
	if err := q.Find(&migrations).Error; err != nil {
		return nil, err
	}
	return migrations, nil
}

// UpdateMemoryKindMigrationCancelRequested marks a queued or running migration as canceling.
// Returns ErrMemoryKindMigrationNotFound if no migration with the given ID exists.
func (e *sqliteEpisodicStore) UpdateMemoryKindMigrationCancelRequested(ctx context.Context, id uuid.UUID) error {
	db := e.writeDBFor(ctx, "UpdateMemoryKindMigrationCancelRequested")
	res := db.Table("memory_kind_migrations").
		Where("id = ? AND state IN ('queued','running')", id).
		Updates(map[string]interface{}{"cancel_requested": 1, "state": "canceling"})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	// No rows updated: either ID doesn't exist or migration is already in a terminal/canceling state.
	m, err := e.GetMemoryKindMigration(ctx, id)
	if err != nil {
		return err
	}
	if m == nil {
		return registryepisodic.ErrMemoryKindMigrationNotFound
	}
	// Already canceling/canceled/succeeded/failed — idempotent no-op.
	return nil
}

// UpdateMemoryKindMigrationState updates state and timestamps.
// Uses writeDBFor so it participates in the enclosing InWriteTx transaction.
func (e *sqliteEpisodicStore) UpdateMemoryKindMigrationState(ctx context.Context, id uuid.UUID, state string, startedAt, completedAt *time.Time) error {
	db := e.writeDBFor(ctx, "UpdateMemoryKindMigrationState")
	updates := map[string]interface{}{"state": state}
	if startedAt != nil {
		updates["started_at"] = *startedAt
	}
	if completedAt != nil {
		updates["completed_at"] = *completedAt
	}
	q := db.Table("memory_kind_migrations").Where("id = ?", id)
	switch state {
	case model.MigrationStateRunning:
		q = q.Where("state = ? AND cancel_requested = 0", model.MigrationStateQueued)
	case model.MigrationStateCanceled:
		q = q.Where("state IN ? AND cancel_requested = 1", []string{model.MigrationStateQueued, model.MigrationStateRunning, model.MigrationStateCanceling})
	}
	res := q.Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return registryepisodic.ErrMemoryKindMigrationStateConflict
	}
	return nil
}

// UpdateMemoryKindMigrationIncrementMigrated increments migrated_count by 1.
// Must be called inside the same InWriteTx as MigrateOneMemoryKindCAS so both roll back
// together if the transaction is aborted.
func (e *sqliteEpisodicStore) UpdateMemoryKindMigrationIncrementMigrated(ctx context.Context, id uuid.UUID) error {
	db := e.writeDBFor(ctx, "UpdateMemoryKindMigrationIncrementMigrated")
	return db.Exec(
		`UPDATE memory_kind_migrations SET migrated_count = migrated_count + 1 WHERE id = ?`,
		id,
	).Error
}

// UpdateMemoryKindMigrationSucceeded atomically sets state=succeeded, completed_at, and
// the ABSOLUTE skipped_tombstone_count observed during the final verification sweep.
// Idempotent: re-running overwrites with the same absolute count rather than accumulating.
// Uses writeDBFor so it participates in the enclosing InWriteTx transaction.
func (e *sqliteEpisodicStore) UpdateMemoryKindMigrationSucceeded(ctx context.Context, id uuid.UUID, completedAt time.Time, absoluteSkippedTombstoneCount int64) error {
	db := e.writeDBFor(ctx, "UpdateMemoryKindMigrationSucceeded")
	res := db.Exec(
		`UPDATE memory_kind_migrations SET
		   state = 'succeeded',
		   completed_at = ?, completed_at_ns = ?,
		   skipped_tombstone_count = ?
		 WHERE id = ? AND state = 'running' AND cancel_requested = 0`,
		completedAt, absoluteSkippedTombstoneCount, id,
	)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return registryepisodic.ErrMemoryKindMigrationStateConflict
	}
	return nil
}

// UpdateMemoryKindMigrationCounters increments progress counters (used by indexer path).
// Uses writeDBFor so it participates in the enclosing InWriteTx transaction.
func (e *sqliteEpisodicStore) UpdateMemoryKindMigrationCounters(ctx context.Context, id uuid.UUID, migratedDelta, tombstoneDelta, vectorPendingDelta int64) error {
	db := e.writeDBFor(ctx, "UpdateMemoryKindMigrationCounters")
	return db.Exec(
		`UPDATE memory_kind_migrations SET
		   migrated_count = migrated_count + ?,
		   skipped_tombstone_count = skipped_tombstone_count + ?,
		   vector_pending_count = vector_pending_count + ?
		 WHERE id = ?`,
		migratedDelta, tombstoneDelta, vectorPendingDelta, id,
	).Error
}

// UpdateMemoryKindMigrationStateFailed atomically sets state=failed, last_error_code, and completed_at.
// Uses writeDBFor so it participates in the enclosing InWriteTx transaction.
func (e *sqliteEpisodicStore) UpdateMemoryKindMigrationStateFailed(ctx context.Context, id uuid.UUID, errorCode string, completedAt time.Time) error {
	db := e.writeDBFor(ctx, "UpdateMemoryKindMigrationStateFailed")
	return db.Exec(
		`UPDATE memory_kind_migrations SET state = 'failed', last_error_code = ?, completed_at = ?, completed_at_ns = ? WHERE id = ?`,
		errorCode, completedAt, id,
	).Error
}

// UpdateMemoryKindMigrationRetry increments retry_count and sets last_error_code.
// Uses writeDBFor so it participates in the enclosing InWriteTx transaction.
func (e *sqliteEpisodicStore) UpdateMemoryKindMigrationRetry(ctx context.Context, id uuid.UUID, errorCode string) error {
	db := e.writeDBFor(ctx, "UpdateMemoryKindMigrationRetry")
	return db.Exec(
		`UPDATE memory_kind_migrations SET retry_count = retry_count + 1, last_error_code = ? WHERE id = ?`,
		errorCode, id,
	).Error
}

// ResolveKindForWrite resolves the canonical schema name for a write.
func (e *sqliteEpisodicStore) ResolveKindForWrite(ctx context.Context, sel string) (string, error) {
	canonicalName := strings.TrimSpace(sel)
	if canonicalName == "" {
		canonicalName = episodic.DefaultKindName
	}
	if _, _, err := episodic.ParseCanonicalKindName(canonicalName); err != nil {
		return "", fmt.Errorf("%w: %v", registryepisodic.ErrMemoryKindInvalid, err)
	}
	v, err := e.GetMemoryKindVersion(ctx, canonicalName)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("%w: %s", registryepisodic.ErrMemoryKindNotFound, canonicalName)
	}
	if !v.Writable {
		return "", fmt.Errorf("%w: %s", registryepisodic.ErrMemoryKindNotWritable, canonicalName)
	}
	return canonicalName, nil
}

// SetMemoryKindIndexedAtCAS sets indexed_at on a memory row only if schema and revision still match.
// archived_at is intentionally not guarded — see postgres implementation for rationale.
func (e *sqliteEpisodicStore) SetMemoryKindIndexedAtCAS(ctx context.Context, memoryID uuid.UUID, expectedKind string, expectedRevision int64, indexedAt time.Time) error {
	db := e.writeDBFor(ctx, "SetMemoryKindIndexedAtCAS")
	res := db.Exec(
		`UPDATE memories SET indexed_at = ?, indexed_at_ns = ?
		 WHERE id = ? AND revision = ? AND memory_kind = ?`,
		indexedAt, memoryID, expectedRevision, expectedKind,
	)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return registryepisodic.ErrMemoryRevisionConflict
	}
	return nil
}

// schemaVersionsEqual compares two MemoryKindVersion records for idempotency.
func schemaVersionsEqual(a, b model.MemoryKindVersion) bool {
	if a.Writable != b.Writable {
		return false
	}
	if len(a.AttributeTypes) != len(b.AttributeTypes) {
		return false
	}
	for k, v := range a.AttributeTypes {
		if b.AttributeTypes[k] != v {
			return false
		}
	}
	aRego := ""
	bRego := ""
	if a.AttributesRego != nil {
		aRego = *a.AttributesRego
	}
	if b.AttributesRego != nil {
		bRego = *b.AttributesRego
	}
	return aRego == bRego
}

// FindMemoriesToMigrateByKind returns up to limit memory rows whose effective schema is sourceKind.
func (e *sqliteEpisodicStore) FindMemoriesToMigrateByKind(ctx context.Context, sourceKind string, namespacePrefix []string, afterCreatedAt time.Time, afterID uuid.UUID, limit int) ([]registryepisodic.MigrationCandidate, error) {
	db := e.dbFor(ctx)
	schemaWhere := "memory_kind = ?"
	args := []interface{}{sourceKind}
	whereClause := schemaWhere
	if !afterCreatedAt.IsZero() {
		whereClause += " AND ((created_at, created_at_ns) > (?, ?) OR ((created_at, created_at_ns) = (?, ?) AND id > ?))"
		args = append(args, afterCreatedAt, afterCreatedAt, afterID.String())
	}
	if len(namespacePrefix) > 0 {
		encoded, err := episodic.EncodeNamespace(namespacePrefix, 0)
		if err != nil {
			return nil, err
		}
		whereClause += " AND (namespace = ? OR namespace LIKE ?)"
		args = append(args, encoded, episodic.NamespacePrefixPattern(encoded))
	}
	type scanRow struct {
		ID               string    `gorm:"column:id"`
		Namespace        string    `gorm:"column:namespace"`
		Key              string    `gorm:"column:key"`
		ValueEncrypted   []byte    `gorm:"column:value_encrypted"`
		PolicyAttributes string    `gorm:"column:policy_attributes"`
		IndexedContent   string    `gorm:"column:indexed_content"`
		MemoryKind       string    `gorm:"column:memory_kind"`
		Revision         int64     `gorm:"column:revision"`
		CreatedAt        time.Time `gorm:"column:created_at"`
	}
	var rows []scanRow
	if err := db.Raw("SELECT id, namespace, key, value_encrypted, policy_attributes, indexed_content, memory_kind, revision, created_at, created_at_ns FROM memories WHERE "+whereClause+" ORDER BY created_at ASC, created_at_ns ASC, id ASC LIMIT ?", append(args, limit)...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]registryepisodic.MigrationCandidate, 0, len(rows))
	for _, r := range rows {
		id, err := uuid.Parse(r.ID)
		if err != nil {
			continue
		}
		var attrs map[string]interface{}
		if r.PolicyAttributes != "" && r.PolicyAttributes != "null" {
			_ = json.Unmarshal([]byte(r.PolicyAttributes), &attrs)
		}
		var indexed map[string]string
		if r.IndexedContent != "" && r.IndexedContent != "null" {
			_ = json.Unmarshal([]byte(r.IndexedContent), &indexed)
		}
		out = append(out, registryepisodic.MigrationCandidate{
			ID:               id,
			CreatedAt:        r.CreatedAt,
			Namespace:        r.Namespace,
			Key:              r.Key,
			ValueEncrypted:   r.ValueEncrypted,
			PolicyAttributes: attrs,
			IndexedContent:   indexed,
			MemoryKind:       r.MemoryKind,
			Revision:         r.Revision,
		})
	}
	return out, nil
}

// MigrateOneMemoryKindCAS atomically updates attributes and schema on a memory row only if
// the expected schema and revision still match. Clears indexed_at.
func (e *sqliteEpisodicStore) MigrateOneMemoryKindCAS(ctx context.Context, id uuid.UUID, expectedKind string, expectedRevision int64, newAttributes map[string]interface{}, newSchema string) error {
	db := e.writeDBFor(ctx, "MigrateOneMemoryKindCAS")
	attrJSON, err := json.Marshal(newAttributes)
	if err != nil {
		return err
	}
	schemaCondition := "memory_kind = ?"
	args := []interface{}{string(attrJSON), newSchema, id.String(), expectedRevision, expectedKind}
	res := db.Exec(
		"UPDATE memories SET policy_attributes = ?, memory_kind = ?, indexed_at = NULL, indexed_at_ns = NULL, revision = revision + 1 WHERE id = ? AND revision = ? AND "+schemaCondition,
		args...,
	)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return registryepisodic.ErrMemoryRevisionConflict
	}
	return nil
}

// CountMemoriesPendingIndexByKind returns the number of memory rows with the given
// targetKind and indexed_at IS NULL, optionally scoped to a namespace prefix.
// Defect 10 fix: used for accurate race-safe vector_pending_count at read time.
func (e *sqliteEpisodicStore) CountMemoriesPendingIndexByKind(ctx context.Context, targetKind string, namespacePrefix []string) (int64, error) {
	db := e.dbFor(ctx)
	schemaWhere := "memory_kind = ?"
	args := []interface{}{targetKind}
	whereClause := schemaWhere + " AND indexed_at IS NULL"
	if len(namespacePrefix) > 0 {
		encoded, err := episodic.EncodeNamespace(namespacePrefix, 0)
		if err != nil {
			return 0, err
		}
		whereClause += " AND (namespace = ? OR namespace LIKE ?)"
		args = append(args, encoded, episodic.NamespacePrefixPattern(encoded))
	}
	var count int64
	err := db.Raw("SELECT COUNT(*) FROM memories WHERE "+whereClause, args...).Scan(&count).Error
	return count, err
}
