package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/log"
)

type registeredProjection struct {
	Name      string
	TableName string
	Resource  string
	RowMode   string
}

// multiRowSortingKey identifies tables created for `rows: many` projections. The registry
// does not store the row mode, so views for registered projections derive it from the table.
const multiRowSortingKey = "exporter_id, resource_id, row_index"

func projectionRowsForSortingKey(sortingKey string) string {
	if sortingKey == multiRowSortingKey {
		return ProjectionRowsMany
	}
	return ProjectionRowsOne
}

func (s *ClickHouseSink) ensureProjectionSchema(ctx context.Context, mode string) error {
	for _, projection := range s.projections {
		if s.config.featureDisabled(projection.Resource, disableProjections) {
			continue
		}
		selector := projection.Resource + ":" + projection.Selector
		var existingCount uint64
		var existingDigest string
		err := s.conn.QueryRow(ctx, fmt.Sprintf("SELECT count(), argMax(projection_digest, version) FROM %s.projection_registry WHERE exporter_id=? AND projection_name=?", quoteIdentifier(s.database)), s.config.ExporterID, projection.Name).Scan(&existingCount, &existingDigest)
		if err != nil {
			return fmt.Errorf("read projection registry for %s: %w", projection.Name, err)
		}
		if existingCount > 0 && existingDigest != projection.Digest {
			return fmt.Errorf("projection %q already exists with a different immutable digest", projection.Name)
		}
		registeredCount, err := s.validateProjectionRegistryOwnership(ctx, projection)
		if err != nil {
			return err
		}
		if registeredCount == 0 {
			for _, relation := range []string{projection.TableName, projection.TableName + "_current", projection.TableName + "_all"} {
				var count uint64
				if err := s.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name=?", s.database, relation).Scan(&count); err != nil {
					return fmt.Errorf("inspect projection relation %s.%s: %w", s.database, relation, err)
				}
				if count != 0 {
					return fmt.Errorf("projection relation %s.%s already exists without an identical registry entry", s.database, relation)
				}
			}
		}
		if mode == "validate" && existingCount == 0 {
			return fmt.Errorf("projection %q is missing from the registry", projection.Name)
		}
		if mode != "validate" {
			columns := make([]string, 0, len(projection.ColumnNames))
			for _, name := range projection.ColumnNames {
				column := projection.Columns[name]
				columnType := projectionColumnSQLType(column)
				if column.Type != "string_array" && column.Type != "variant" {
					columnType = "Nullable(" + columnType + ")"
				}
				columns = append(columns, fmt.Sprintf("`%s` %s", name, columnType))
			}
			rowColumns, sortingKey := "", "exporter_id, resource_id"
			if projection.RowMode == ProjectionRowsMany {
				rowColumns, sortingKey = "row_index UInt32, row_count UInt32,\n  ", multiRowSortingKey
			}
			statement := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s (
  exporter_id String, batch_id FixedString(64), event_id FixedString(64), resource_id String,
  conversation_id String, conversation_group_id String,
  ingest_version UInt64, observed_at DateTime64(9, 'UTC'), is_deleted UInt8,
  schema_version UInt16,
  %s%s
) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (%s)`, quoteIdentifier(s.database), quoteIdentifier(projection.TableName), rowColumns, strings.Join(columns, ",\n  "), sortingKey)
			if err := s.conn.Exec(ctx, statement); err != nil {
				return fmt.Errorf("create projection table %s: %w", projection.TableName, err)
			}
		}
		if mode != "validate" && existingCount == 0 {
			if err := s.conn.Exec(ctx, fmt.Sprintf("INSERT INTO %s.projection_registry (exporter_id, projection_name, projection_digest, selector, table_name, state, updated_at, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", quoteIdentifier(s.database)), s.config.ExporterID, projection.Name, projection.Digest, selector, projection.TableName, "active", time.Now().UTC(), uint64(1)); err != nil {
				return fmt.Errorf("register projection %s: %w", projection.Name, err)
			}
			if _, err := s.validateProjectionRegistryOwnership(ctx, projection); err != nil {
				return err
			}
		}
	}
	return s.ensureRegisteredProjectionSchemas(ctx, mode)
}

func (s *ClickHouseSink) validateProjectionRegistryOwnership(ctx context.Context, projection *Projection) (uint64, error) {
	var registeredCount, registeredDigests uint64
	var registeredDigest string
	if err := s.conn.QueryRow(ctx, fmt.Sprintf("SELECT count(), uniqExact(projection_digest), any(projection_digest) FROM %s.projection_registry WHERE table_name=?", quoteIdentifier(s.database)), projection.TableName).Scan(&registeredCount, &registeredDigests, &registeredDigest); err != nil {
		return 0, fmt.Errorf("read projection registry ownership for %s: %w", projection.Name, err)
	}
	if registeredCount > 0 && (registeredDigests != 1 || registeredDigest != projection.Digest) {
		return 0, fmt.Errorf("projection table %q is registered with a different immutable manifest", projection.TableName)
	}
	return registeredCount, nil
}

func (s *ClickHouseSink) registeredProjections(ctx context.Context) ([]registeredProjection, error) {
	rows, err := s.conn.Query(ctx, fmt.Sprintf(`SELECT projection_name, argMax(table_name, version), argMax(selector, version)
FROM %s.projection_registry
WHERE exporter_id=?
GROUP BY projection_name
ORDER BY projection_name`, quoteIdentifier(s.database)), s.config.ExporterID)
	if err != nil {
		return nil, err
	}
	result := make([]registeredProjection, 0)
	resourcesByTable := map[string]string{}
	for rows.Next() {
		var name, table, selector string
		if err := rows.Scan(&name, &table, &selector); err != nil {
			_ = rows.Close()
			return nil, err
		}
		projection, err := validateRegisteredProjection(name, table, selector)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if existing, ok := resourcesByTable[table]; ok && existing != projection.Resource {
			_ = rows.Close()
			return nil, fmt.Errorf("registered projection table %q has conflicting resource kinds", table)
		}
		resourcesByTable[table] = projection.Resource
		result = append(result, projection)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i, projection := range result {
		var count uint64
		var engine, sortingKey string
		if err := s.conn.QueryRow(ctx, "SELECT count(), any(engine), any(sorting_key) FROM system.tables WHERE database=? AND name=?", s.database, projection.TableName).Scan(&count, &engine, &sortingKey); err != nil {
			return nil, fmt.Errorf("validate registered projection table %s: %w", projection.TableName, err)
		}
		if count != 1 || engine == "View" || engine == "MaterializedView" {
			return nil, fmt.Errorf("registered projection table %s.%s is missing or is not a physical table", s.database, projection.TableName)
		}
		result[i].RowMode = projectionRowsForSortingKey(sortingKey)
	}
	return result, nil
}

func (s *ClickHouseSink) registeredProjectionsAllExporters(ctx context.Context) ([]registeredProjection, error) {
	rows, err := s.conn.Query(ctx, fmt.Sprintf(`SELECT any(projection_name), table_name, selector
FROM %s.projection_registry GROUP BY table_name, selector ORDER BY table_name`, quoteIdentifier(s.database)))
	if err != nil {
		return nil, err
	}
	result := []registeredProjection{}
	resources := map[string]string{}
	for rows.Next() {
		var name, table, selector string
		if err := rows.Scan(&name, &table, &selector); err != nil {
			return nil, err
		}
		projection, err := validateRegisteredProjection(name, table, selector)
		if err != nil {
			return nil, err
		}
		if resource, ok := resources[table]; ok {
			if resource != projection.Resource {
				return nil, fmt.Errorf("registered projection table %q has conflicting resource kinds", table)
			}
			continue
		}
		resources[table] = projection.Resource
		result = append(result, projection)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, projection := range result {
		var count uint64
		var engine string
		if err := s.conn.QueryRow(ctx, "SELECT count(), any(engine) FROM system.tables WHERE database=? AND name=?", s.database, projection.TableName).Scan(&count, &engine); err != nil {
			return nil, err
		}
		if count != 1 || engine == "View" || engine == "MaterializedView" {
			return nil, fmt.Errorf("registered projection table %s.%s is missing or is not a physical table", s.database, projection.TableName)
		}
	}
	return result, nil
}

func validateRegisteredProjection(name, table, selector string) (registeredProjection, error) {
	resource, expression, found := strings.Cut(selector, ":")
	if !found || expression == "" || (resource != "entry" && resource != "memory") {
		return registeredProjection{}, fmt.Errorf("projection %q has invalid registered selector %q", name, selector)
	}
	if !projectionNamePattern.MatchString(name) || table != name || isReservedProjectionTableName(table) {
		return registeredProjection{}, fmt.Errorf("projection %q has unsafe registered table name %q", name, table)
	}
	return registeredProjection{Name: name, TableName: table, Resource: resource}, nil
}

func (s *ClickHouseSink) ensureRegisteredProjectionSchemas(ctx context.Context, mode string) error {
	projections, err := s.registeredProjections(ctx)
	if err != nil {
		return fmt.Errorf("read registered projections: %w", err)
	}
	for _, projection := range projections {
		if mode == "validate" {
			for _, suffix := range []string{"_all", "_current"} {
				var count uint64
				var engine string
				if err := s.conn.QueryRow(ctx, "SELECT count(), any(engine) FROM system.tables WHERE database=? AND name=?", s.database, projection.TableName+suffix).Scan(&count, &engine); err != nil {
					return fmt.Errorf("validate projection view %s%s: %w", projection.TableName, suffix, err)
				}
				if count != 1 || engine != "View" {
					return fmt.Errorf("required projection view %s.%s%s is missing or is not a view", s.database, projection.TableName, suffix)
				}
			}
			continue
		}
		if err := s.conn.Exec(ctx, projectionAllViewStatement(s.database, projection.TableName, projection.RowMode)); err != nil {
			return fmt.Errorf("create projection all view %s: %w", projection.TableName, err)
		}
		if err := s.conn.Exec(ctx, projectionCurrentViewStatement(s.database, projection.TableName, projection.Resource)); err != nil {
			return fmt.Errorf("create projection view %s: %w", projection.TableName, err)
		}
	}
	return nil
}

func projectionAllViewStatement(database, table, rowMode string) string {
	prefix := quoteIdentifier(database) + "."
	if rowMode == ProjectionRowsMany {
		// Keep every row of the latest resource version. Older versions may have had more rows,
		// and those row indexes are never replaced, so filter them by version instead of by key.
		// Rows with row_index >= row_count are markers for versions that projected no rows.
		return fmt.Sprintf(`CREATE OR REPLACE VIEW %s%s AS
SELECT r.* EXCEPT(version_rank, rn) FROM (
 SELECT r.*,
  dense_rank() OVER (PARTITION BY r.exporter_id, r.resource_id ORDER BY r.ingest_version DESC, r.event_id DESC) version_rank,
  row_number() OVER (PARTITION BY r.exporter_id, r.resource_id, r.row_index ORDER BY r.ingest_version DESC, r.event_id DESC) rn
 FROM %s%s r
) r
WHERE version_rank=1 AND rn=1 AND (r.is_deleted=1 OR r.row_index < r.row_count)`, prefix, quoteIdentifier(table+"_all"), prefix, quoteIdentifier(table))
	}
	return fmt.Sprintf(`CREATE OR REPLACE VIEW %s%s AS
SELECT r.* EXCEPT(rn) FROM (
 SELECT r.*, row_number() OVER (PARTITION BY r.exporter_id, r.resource_id ORDER BY r.ingest_version DESC, r.event_id DESC) rn
 FROM %s%s r
) r
WHERE rn=1`, prefix, quoteIdentifier(table+"_all"), prefix, quoteIdentifier(table))
}

func projectionCurrentViewStatement(database, table, resource string) string {
	prefix := quoteIdentifier(database) + "."
	return fmt.Sprintf(`CREATE OR REPLACE VIEW %s%s AS
SELECT r.* FROM %s%s r
LEFT JOIN (%s) d ON r.exporter_id=d.exporter_id AND d.resource_type='%s' AND r.resource_id=d.resource_id
WHERE is_deleted=0 AND r.ingest_version > ifNull(d.deletion_version, 0)`, prefix, quoteIdentifier(table+"_current"), prefix, quoteIdentifier(table+"_all"), deletionFenceSubquery(prefix), resource)
}

func (s *ClickHouseSink) configureRetention(ctx context.Context, mode string) error {
	if s.config.RetentionMode == RetentionExternal {
		return nil
	}
	if mode == "validate" {
		if err := s.validateRetention(ctx); err != nil {
			return err
		}
	}
	if err := s.ensureRetentionPolicy(ctx); err != nil {
		return err
	}
	s.startRetentionHeartbeat()
	if mode == "validate" {
		return nil
	}
	if err := s.setTTL(ctx, "lifecycle_events", s.config.LifecycleRetention, "occurred_at", ""); err != nil {
		return err
	}
	for _, table := range []string{"resources", "conversations", "conversation_lineage", "entries", "memories"} {
		if err := s.setResourceTTL(ctx, table); err != nil {
			return err
		}
	}
	if err := s.setTTL(ctx, "projection_failures", s.config.ProjectionFailureRetention, "last_seen_at", ""); err != nil {
		return err
	}
	projections, err := s.registeredProjections(ctx)
	if err != nil {
		return fmt.Errorf("load registered projections for retention: %w", err)
	}
	for _, projection := range projections {
		if err := s.setProjectionTTL(ctx, projection.TableName); err != nil {
			return err
		}
	}
	return nil
}

const (
	retentionOwnerTTL          = 30 * time.Second
	retentionHeartbeatInterval = 10 * time.Second
)

var errRetentionPolicyConflict = errors.New("ClickHouse database retention policy conflicts")

func (s *ClickHouseSink) BackgroundErrors() <-chan error { return s.retentionErrors }

func (s *ClickHouseSink) retentionOwnershipError() error {
	s.retentionFailureMu.RLock()
	defer s.retentionFailureMu.RUnlock()
	return s.retentionFailure
}

func (s *ClickHouseSink) failRetentionOwnership(cause error) error {
	s.retentionFailureMu.Lock()
	if s.retentionFailure != nil {
		err := s.retentionFailure
		s.retentionFailureMu.Unlock()
		return err
	}
	err := fmt.Errorf("ClickHouse retention policy ownership lost: %w", cause)
	s.retentionFailure = err
	s.retentionFailureMu.Unlock()
	select {
	case s.retentionErrors <- err:
	default:
	}
	return err
}

func (s *ClickHouseSink) retentionConflict(detail string) error {
	err := fmt.Errorf("%w %s", errRetentionPolicyConflict, detail)
	if s.retentionRegistered {
		return s.failRetentionOwnership(err)
	}
	return err
}

func retentionPolicyValues(cfg Config) []int64 {
	return []int64{int64(cfg.LifecycleRetention / time.Second), int64(cfg.TombstoneRetention / time.Second), int64(cfg.GenericPayloadRetention / time.Second), int64(cfg.ProjectionRetention / time.Second), int64(cfg.ProjectionFailureRetention / time.Second)}
}

func retentionLivePredicate() string {
	return fmt.Sprintf("active=1 AND updated_at > now64(9) - toIntervalSecond(%d)", int64(retentionOwnerTTL/time.Second))
}

func (s *ClickHouseSink) writeRetentionOwner(ctx context.Context, active uint8) error {
	values := retentionPolicyValues(s.config)
	now := time.Now().UTC()
	return s.conn.Exec(ctx, fmt.Sprintf("INSERT INTO %s.retention_policy VALUES (?,?,?,?,?,?,?,?,?,?)", quoteIdentifier(s.database)), s.config.ExporterID, s.retentionOwner, values[0], values[1], values[2], values[3], values[4], active, uint64(now.UnixNano()), now)
}

// renewRetentionOwnership is the write fence used by both the heartbeat and
// batch write path. An expired owner must never silently re-register: another
// exporter may already have taken over and changed the database-wide TTLs.
func (s *ClickHouseSink) renewRetentionOwnership(ctx context.Context) error {
	if s.config.RetentionMode == RetentionExternal {
		return nil
	}
	if err := s.retentionOwnershipError(); err != nil {
		return err
	}
	var live uint64
	query := fmt.Sprintf("SELECT count() FROM %s.retention_policy FINAL WHERE exporter_id=? AND owner_id=? AND %s", quoteIdentifier(s.database), retentionLivePredicate())
	if err := s.conn.QueryRow(ctx, query, s.config.ExporterID, s.retentionOwner).Scan(&live); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.failRetentionOwnership(fmt.Errorf("verify live retention owner: %w", err))
	}
	if live != 1 {
		return s.failRetentionOwnership(errors.New("retention owner lease expired"))
	}
	if err := s.ensureRetentionPolicy(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.failRetentionOwnership(fmt.Errorf("renew retention owner: %w", err))
	}
	return nil
}

func (s *ClickHouseSink) startRetentionHeartbeat() {
	if !s.retentionRegistered || s.retentionCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.retentionCancel = cancel
	s.retentionWG.Add(1)
	go func() {
		defer s.retentionWG.Done()
		ticker := time.NewTicker(retentionHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.renewRetentionOwnership(ctx); err != nil && ctx.Err() == nil {
					log.Warn("ClickHouse retention policy heartbeat failed", "exporterID", s.config.ExporterID, "err", err)
					return
				}
			}
		}
	}()
}

func (s *ClickHouseSink) stopRetentionHeartbeat() {
	if s.retentionCancel == nil {
		return
	}
	s.retentionCancel()
	s.retentionWG.Wait()
	s.retentionCancel = nil
}

func (s *ClickHouseSink) ensureRetentionPolicy(ctx context.Context) error {
	values := retentionPolicyValues(s.config)
	var distinct uint64
	var lifecycle, tombstone, generic, projection, failure int64
	query := fmt.Sprintf("SELECT uniqExact(tuple(lifecycle_seconds,tombstone_seconds,generic_seconds,projection_seconds,failure_seconds)), any(lifecycle_seconds),any(tombstone_seconds),any(generic_seconds),any(projection_seconds),any(failure_seconds) FROM %s.retention_policy FINAL WHERE %s", quoteIdentifier(s.database), retentionLivePredicate())
	if err := s.conn.QueryRow(ctx, query).Scan(&distinct, &lifecycle, &tombstone, &generic, &projection, &failure); err != nil {
		return err
	}
	if distinct > 1 || (distinct == 1 && (lifecycle != values[0] || tombstone != values[1] || generic != values[2] || projection != values[3] || failure != values[4])) {
		return s.retentionConflict("with the registered shared policy")
	}
	if err := s.writeRetentionOwner(ctx, 1); err != nil {
		return err
	}
	// Mark registration immediately so OpenSink's failure cleanup deactivates
	// this owner if the concurrent-policy check or later TTL setup fails.
	s.retentionRegistered = true
	if err := s.conn.QueryRow(ctx, fmt.Sprintf("SELECT uniqExact(tuple(lifecycle_seconds,tombstone_seconds,generic_seconds,projection_seconds,failure_seconds)) FROM %s.retention_policy FINAL WHERE %s", quoteIdentifier(s.database), retentionLivePredicate())).Scan(&distinct); err != nil {
		return err
	}
	if distinct != 1 {
		_ = s.writeRetentionOwner(ctx, 0)
		err := s.retentionConflict("with a concurrent exporter")
		s.retentionRegistered = false
		return err
	}
	return nil
}

func (s *ClickHouseSink) setResourceTTL(ctx context.Context, table string) error {
	return s.modifyTTL(ctx, table, s.resourceTTLClauses())
}

func (s *ClickHouseSink) resourceTTLClauses() []string {
	clauses := make([]string, 0, 2)
	if s.config.TombstoneRetention > 0 {
		clauses = append(clauses, ttlClause("updated_at", s.config.TombstoneRetention, "is_deleted=1"))
	}
	if s.config.GenericPayloadRetention > 0 {
		clauses = append(clauses, ttlClause("updated_at", s.config.GenericPayloadRetention, "is_deleted=0"))
	}
	return clauses
}

func (s *ClickHouseSink) setProjectionTTL(ctx context.Context, table string) error {
	return s.modifyTTL(ctx, table, s.projectionTTLClauses())
}

func (s *ClickHouseSink) projectionTTLClauses() []string {
	clauses := make([]string, 0, 2)
	if s.config.TombstoneRetention > 0 {
		clauses = append(clauses, ttlClause("observed_at", s.config.TombstoneRetention, "is_deleted=1"))
	}
	if s.config.ProjectionRetention > 0 {
		clauses = append(clauses, ttlClause("observed_at", s.config.ProjectionRetention, "is_deleted=0"))
	}
	return clauses
}

func (s *ClickHouseSink) setTTL(ctx context.Context, table string, retention time.Duration, column, where string) error {
	clauses := []string{}
	if retention > 0 {
		clauses = append(clauses, ttlClause(column, retention, where))
	}
	return s.modifyTTL(ctx, table, clauses)
}

func (s *ClickHouseSink) modifyTTL(ctx context.Context, table string, clauses []string) error {
	action := "REMOVE TTL"
	if len(clauses) > 0 {
		action = "MODIFY TTL " + strings.Join(clauses, ", ")
	} else {
		var hasTTL uint64
		if err := s.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name=? AND create_table_query LIKE '% TTL %'", s.database, table).Scan(&hasTTL); err != nil {
			return fmt.Errorf("inspect retention for %s: %w", table, err)
		}
		if hasTTL == 0 {
			return nil
		}
	}
	if err := s.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE %s.%s %s", quoteIdentifier(s.database), quoteIdentifier(table), action)); err != nil {
		return fmt.Errorf("configure retention for %s: %w", table, err)
	}
	return nil
}

func ttlClause(column string, retention time.Duration, where string) string {
	clause := fmt.Sprintf("%s + toIntervalSecond(%d)", quoteIdentifier(column), int64(retention/time.Second))
	if where != "" {
		clause += " WHERE " + where
	}
	return clause
}

func (s *ClickHouseSink) validateRetention(ctx context.Context) error {
	targets := []struct {
		table   string
		clauses []string
	}{
		{table: "lifecycle_events", clauses: ttlClauses(s.config.LifecycleRetention, "occurred_at", "")},
		{table: "projection_failures", clauses: ttlClauses(s.config.ProjectionFailureRetention, "last_seen_at", "")},
	}
	for _, table := range []string{"resources", "conversations", "conversation_lineage", "entries", "memories"} {
		targets = append(targets, struct {
			table   string
			clauses []string
		}{table: table, clauses: s.resourceTTLClauses()})
	}
	projections, err := s.registeredProjections(ctx)
	if err != nil {
		return fmt.Errorf("load registered projections for retention validation: %w", err)
	}
	for _, projection := range projections {
		targets = append(targets, struct {
			table   string
			clauses []string
		}{table: projection.TableName, clauses: s.projectionTTLClauses()})
	}
	for _, target := range targets {
		if err := s.validateTableTTL(ctx, target.table, target.clauses); err != nil {
			return err
		}
	}
	return nil
}

func ttlClauses(retention time.Duration, column, where string) []string {
	if retention <= 0 {
		return nil
	}
	return []string{ttlClause(column, retention, where)}
}

func (s *ClickHouseSink) validateTableTTL(ctx context.Context, table string, clauses []string) error {
	var ddl string
	if err := s.conn.QueryRow(ctx, "SELECT create_table_query FROM system.tables WHERE database=? AND name=?", s.database, table).Scan(&ddl); err != nil {
		return fmt.Errorf("inspect retention for %s: %w", table, err)
	}
	actual := canonicalTTLExpression(extractTTLExpression(ddl))
	expected := canonicalTTLExpression(strings.Join(clauses, ", "))
	if actual != expected {
		return fmt.Errorf("ClickHouse TTL mismatch for %s.%s: found %q, expected %q", s.database, table, extractTTLExpression(ddl), strings.Join(clauses, ", "))
	}
	return nil
}

func extractTTLExpression(ddl string) string {
	upper := strings.ToUpper(ddl)
	start := strings.Index(upper, "\nTTL ")
	markerLength := len("\nTTL ")
	if start < 0 {
		start = strings.Index(upper, " TTL ")
		markerLength = len(" TTL ")
	}
	if start < 0 {
		return ""
	}
	value := ddl[start+markerLength:]
	upperValue := strings.ToUpper(value)
	end := len(value)
	for _, marker := range []string{"\nSETTINGS ", " SETTINGS ", "\nCOMMENT ", " COMMENT "} {
		if index := strings.Index(upperValue, marker); index >= 0 && index < end {
			end = index
		}
	}
	return strings.TrimSpace(value[:end])
}

func canonicalTTLExpression(value string) string {
	var result strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsSpace(r) || r == '`' {
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func quoteIdentifier(value string) string { return "`" + value + "`" }
