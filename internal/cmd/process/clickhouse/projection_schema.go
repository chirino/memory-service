package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/charmbracelet/log"
)

type registeredProjection struct {
	Name      string
	TableName string
	Resource  string
	RowMode   string
	Ordering  string
}

func projectionSortingKey(rows, ordering string) string {
	key := "exporter_id, resource_id"
	if ordering == "conversation" {
		key = "exporter_id, conversation_id, resource_id"
	}
	if rows == ProjectionRowsMany {
		key += ", row_index"
	}
	return key
}

func (s *ClickHouseSink) ensureProjectionSchema(ctx context.Context, mode string) error {
	for _, table := range s.projectionTables {
		if s.config.featureDisabled(table.Resource, disableProjections) {
			continue
		}
		if err := s.ensureProjectionTable(ctx, mode, table); err != nil {
			return err
		}
	}
	return s.ensureRegisteredProjectionSchemas(ctx, mode)
}

func (s *ClickHouseSink) ensureProjectionTable(ctx context.Context, mode string, table *ProjectionTable) error {
	state, err := s.readProjectionChainRegistry(ctx, table)
	if err != nil {
		return err
	}
	if !state.tableRegistered {
		for _, relation := range []string{table.Name, table.Name + "_current", table.Name + "_all"} {
			var count uint64
			if err := s.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name=?", s.database, relation).Scan(&count); err != nil {
				return fmt.Errorf("inspect projection relation %s.%s: %w", s.database, relation, err)
			}
			if count != 0 {
				return fmt.Errorf("projection relation %s.%s already exists without an identical registry entry", s.database, relation)
			}
		}
	}
	if state.tableRegistered {
		if err := s.validateRegisteredTable(ctx, registeredProjection{TableName: table.Name, Resource: table.Resource, RowMode: table.RowMode, Ordering: table.Ordering}); err != nil {
			return err
		}
	}
	if mode != "validate" {
		statement := projectionCreateStatement(s.database, table, true)
		if err := s.conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("create projection table %s: %w", table.Name, err)
		}
	}
	if err := s.ensureProjectionTableColumns(ctx, mode, table); err != nil {
		return err
	}
	if mode == "validate" {
		for _, manifest := range table.Manifests {
			if !state.registeredHere[manifest.Name] {
				return fmt.Errorf("projection %q is missing from the registry", manifest.Name)
			}
		}
		return nil
	}
	var registered []string
	for _, manifest := range table.Manifests {
		if state.registeredHere[manifest.Name] {
			continue
		}
		selector := manifest.Resource + ":" + manifest.Selector
		if err := s.conn.Exec(ctx, fmt.Sprintf("INSERT INTO %s.projection_registry (exporter_id, projection_name, projection_digest, selector, table_name, state, updated_at, version, previous, row_mode, ordering) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", quoteIdentifier(s.database)), s.config.ExporterID, manifest.Name, manifest.Digest, selector, table.Name, "active", time.Now().UTC(), uint64(1), manifest.Previous, table.RowMode, table.Ordering); err != nil {
			return fmt.Errorf("register projection %s: %w", manifest.Name, err)
		}
		registered = append(registered, manifest.Name)
	}
	if len(registered) > 0 {
		if afterProjectionRegistration != nil {
			afterProjectionRegistration(ctx, table.Name)
		}
		// Re-read the registry so a concurrent exporter registering a divergent chain fails.
		// Withdraw this attempt's rows so the losing configuration does not block the table.
		if _, err := s.readProjectionChainRegistry(ctx, table); err != nil {
			return errors.Join(err, s.withdrawProjectionRegistrations(ctx, registered))
		}
	}
	return nil
}

// afterProjectionRegistration lets tests register a concurrent exporter's manifests between
// this exporter's registration and its re-check. It is nil outside tests.
var afterProjectionRegistration func(ctx context.Context, table string)

// withdrawProjectionRegistrations removes this exporter's registry rows for the named manifests.
func (s *ClickHouseSink) withdrawProjectionRegistrations(ctx context.Context, names []string) error {
	predicate, args := fixedStringPredicate("projection_name", names)
	withdrawCtx := ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	if err := s.deletePurgeRows(withdrawCtx, "projection_registry", s.config.ExporterID, predicate, args); err != nil {
		return fmt.Errorf("withdraw projection registrations %s: %w", strings.Join(names, ", "), err)
	}
	return nil
}

// projectionChainRegistry describes which manifests of a configured chain are registered.
type projectionChainRegistry struct {
	tableRegistered bool
	registeredHere  map[string]bool
}

// readProjectionChainRegistry checks that the registered manifests of a table, across every
// exporter, form the beginning of the configured chain with identical digests. Registered
// manifests can never be edited, reordered, or removed from the configuration.
func (s *ClickHouseSink) readProjectionChainRegistry(ctx context.Context, table *ProjectionTable) (projectionChainRegistry, error) {
	state := projectionChainRegistry{registeredHere: map[string]bool{}}
	registered := map[string]bool{}
	for _, manifest := range table.Manifests {
		var count, here, digests, tables, rowModes, orderings uint64
		var digest, tableName, rowMode, ordering string
		if err := s.conn.QueryRow(ctx, fmt.Sprintf("SELECT count(), countIf(exporter_id=?), uniqExact(projection_digest), any(projection_digest), uniqExact(table_name), any(table_name), uniqExact(row_mode), any(row_mode), uniqExact(ordering), any(ordering) FROM %s.projection_registry WHERE projection_name=?", quoteIdentifier(s.database)), s.config.ExporterID, manifest.Name).Scan(&count, &here, &digests, &digest, &tables, &tableName, &rowModes, &rowMode, &orderings, &ordering); err != nil {
			return state, fmt.Errorf("read projection registry for %s: %w", manifest.Name, err)
		}
		if count == 0 {
			continue
		}
		if rowModes != 1 || orderings != 1 || rowMode != table.RowMode || ordering != table.Ordering {
			return state, fmt.Errorf("projection %q has conflicting layout metadata", manifest.Name)
		}
		if digests != 1 || digest != manifest.Digest {
			return state, fmt.Errorf("projection %q already exists with a different immutable digest", manifest.Name)
		}
		if tables != 1 || tableName != table.Name {
			return state, fmt.Errorf("projection %q is registered for table %q, not %q", manifest.Name, tableName, table.Name)
		}
		registered[manifest.Name] = true
		state.registeredHere[manifest.Name] = here > 0
	}
	rows, err := s.conn.Query(ctx, fmt.Sprintf("SELECT DISTINCT projection_name FROM %s.projection_registry WHERE table_name=? ORDER BY projection_name", quoteIdentifier(s.database)), table.Name)
	if err != nil {
		return state, fmt.Errorf("read projection registry ownership for %s: %w", table.Name, err)
	}
	configured := map[string]bool{}
	for _, manifest := range table.Manifests {
		configured[manifest.Name] = true
	}
	var unknown []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return state, err
		}
		state.tableRegistered = true
		if !configured[name] {
			unknown = append(unknown, name)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return state, err
	}
	if err := rows.Close(); err != nil {
		return state, err
	}
	if len(unknown) > 0 {
		return state, fmt.Errorf("projection table %q is registered with manifests %s that are not in its configured chain; registered manifests cannot be edited, renamed, or removed", table.Name, strings.Join(unknown, ", "))
	}
	for i := len(table.Manifests) - 1; i > 0; i-- {
		if registered[table.Manifests[i].Name] && !registered[table.Manifests[i-1].Name] {
			return state, fmt.Errorf("projection %q is registered for table %q but its previous manifest %q is not; registered manifests must form the beginning of the configured chain", table.Manifests[i].Name, table.Name, table.Manifests[i-1].Name)
		}
	}
	return state, nil
}

// ensureProjectionTableColumns adds the table's missing columns in manage mode. Columns added
// by spec.addColumns are nullable, so ADD COLUMN only changes metadata. Validate mode lists
// the missing columns instead of changing the table.
func (s *ClickHouseSink) ensureProjectionTableColumns(ctx context.Context, mode string, table *ProjectionTable) error {
	rows, err := s.conn.Query(ctx, "SELECT name, type FROM system.columns WHERE database=? AND table=?", s.database, table.Name)
	if err != nil {
		return fmt.Errorf("inspect projection table %s columns: %w", table.Name, err)
	}
	existing := map[string]string{}
	for rows.Next() {
		var name, columnType string
		if err := rows.Scan(&name, &columnType); err != nil {
			_ = rows.Close()
			return err
		}
		existing[name] = columnType
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(existing) == 0 && mode == "validate" {
		// The table itself is missing; the registry check reports the unregistered manifests.
		return nil
	}
	var missing []string
	for _, name := range table.ColumnOrder {
		actual, ok := existing[name]
		if !ok {
			if table.ColumnOwners[name] == table.Manifests[0].Name {
				return fmt.Errorf("projection table %s is missing immutable root column %s", table.Name, name)
			}
			missing = append(missing, name)
			continue
		}
		// Added columns may come from an interrupted earlier start whose manifest was then
		// changed before registration, so their physical type must match the declaration.
		declared := projectionColumnStorageType(table.Columns[name])
		canonical, err := s.canonicalClickHouseType(ctx, declared)
		if err != nil {
			return fmt.Errorf("canonicalize projection table %s column %s type %s: %w", table.Name, name, declared, err)
		}
		if actual != canonical {
			return fmt.Errorf("projection table %s.%s column %s has type %s, but %s declares %s", s.database, table.Name, name, actual, table.ColumnOwners[name], declared)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if mode == "validate" {
		statements := make([]string, len(missing))
		for i, name := range missing {
			statements[i] = fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN IF NOT EXISTS %s", quoteIdentifier(s.database), quoteIdentifier(table.Name), projectionColumnDDL(name, table.Columns[name]))
		}
		// Applying these statements alone is not enough: only manage mode registers the new
		// manifests and recreates the views, so the statements are a preview of that run.
		return fmt.Errorf("projection table %s.%s is missing columns %s; run the processor once with --schema-mode manage and a ClickHouse user that can alter tables to add them, register the new manifests, and recreate the views. That run applies: %s", s.database, table.Name, strings.Join(missing, ", "), strings.Join(statements, "; "))
	}
	for _, name := range missing {
		if err := s.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE %s.%s ADD COLUMN IF NOT EXISTS %s", quoteIdentifier(s.database), quoteIdentifier(table.Name), projectionColumnDDL(name, table.Columns[name]))); err != nil {
			return fmt.Errorf("add projection table %s column %s: %w", table.Name, name, err)
		}
	}
	return nil
}

func projectionCreateStatement(database string, table *ProjectionTable, rootOnly bool) string {
	columns := []string{}
	for _, name := range table.ColumnOrder {
		if !rootOnly || table.ColumnOwners[name] == table.Manifests[0].Name {
			columns = append(columns, projectionColumnDDL(name, table.Columns[name]))
		}
	}
	rowColumns := ""
	if table.RowMode == ProjectionRowsMany {
		rowColumns = "row_index UInt32, row_count UInt32,\n  "
	}
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s (
 exporter_id String, batch_id FixedString(64), event_id FixedString(64), resource_id String,
 conversation_id String, conversation_group_id String, ingest_version UInt64,
 observed_at DateTime64(9, 'UTC'), is_deleted UInt8,
 %s%s
) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (%s)`, quoteIdentifier(database), quoteIdentifier(table.Name), rowColumns, strings.Join(columns, ",\n "), projectionSortingKey(table.RowMode, table.Ordering))
}

func projectionColumnDDL(name string, column projectionColumn) string {
	return fmt.Sprintf("%s %s", quoteIdentifier(name), projectionColumnStorageType(column))
}

// projectionColumnStorageType is the physical ClickHouse type of a projection column.
func projectionColumnStorageType(column projectionColumn) string {
	columnType := projectionColumnSQLType(column)
	if column.Type == "string" && column.ClickHouse != nil && column.ClickHouse.LowCardinality {
		return "LowCardinality(Nullable(String))"
	}
	if column.Type != "string_array" && column.Type != "variant" {
		columnType = "Nullable(" + columnType + ")"
	}
	return columnType
}

// canonicalClickHouseType returns the type name system.columns reports for a declared type.
// ClickHouse rewrites type names (whitespace, JSON parameter order, and JSON parameters equal
// to its own defaults are dropped), so the server canonicalizes the declaration.
func (s *ClickHouseSink) canonicalClickHouseType(ctx context.Context, declared string) (string, error) {
	var canonical string
	if err := s.conn.QueryRow(ctx, "SELECT toTypeName(defaultValueOfTypeName(?))", declared).Scan(&canonical); err != nil {
		return "", err
	}
	return canonical, nil
}

func (s *ClickHouseSink) registeredProjections(ctx context.Context) ([]registeredProjection, error) {
	return s.readRegisteredProjections(ctx, true)
}
func (s *ClickHouseSink) registeredProjectionsAllExporters(ctx context.Context) ([]registeredProjection, error) {
	return s.readRegisteredProjections(ctx, false)
}
func (s *ClickHouseSink) readRegisteredProjections(ctx context.Context, own bool) ([]registeredProjection, error) {
	query := fmt.Sprintf("SELECT DISTINCT projection_name,table_name,selector,row_mode,ordering FROM %s.projection_registry FINAL", quoteIdentifier(s.database))
	var args []any
	if own {
		query += " WHERE exporter_id=?"
		args = append(args, s.config.ExporterID)
	}
	query += " ORDER BY table_name,projection_name"
	rows, err := s.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	result := []registeredProjection{}
	seen := map[string]registeredProjection{}
	for rows.Next() {
		var name, table, selector, rowMode, ordering string
		if err := rows.Scan(&name, &table, &selector, &rowMode, &ordering); err != nil {
			_ = rows.Close()
			return nil, err
		}
		projection, err := validateRegisteredProjection(name, table, selector)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if rowMode != ProjectionRowsOne && rowMode != ProjectionRowsMany || ordering != "resource" && ordering != "conversation" || projection.Resource == "memory" && ordering == "conversation" {
			_ = rows.Close()
			return nil, errors.New("invalid registered projection layout")
		}
		projection.RowMode = rowMode
		projection.Ordering = ordering
		if prior, ok := seen[table]; ok {
			if prior.Resource != projection.Resource || prior.RowMode != rowMode || prior.Ordering != ordering {
				_ = rows.Close()
				return nil, errors.New("conflicting registered table ownership or layout")
			}
			continue
		}
		seen[table] = projection
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
		if err := s.validateRegisteredTable(ctx, projection); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *ClickHouseSink) validateRegisteredTable(ctx context.Context, projection registeredProjection) error {
	var count uint64
	var engine, key, partition, engineFull string
	if err := s.conn.QueryRow(ctx, "SELECT count(),any(engine),any(sorting_key),any(partition_key),any(engine_full) FROM system.tables WHERE database=? AND name=?", s.database, projection.TableName).Scan(&count, &engine, &key, &partition, &engineFull); err != nil {
		return err
	}
	normalize := func(value string) string { return strings.ReplaceAll(strings.ReplaceAll(value, " ", ""), "`", "") }
	if count != 1 || engine != "ReplacingMergeTree" || normalize(key) != normalize(projectionSortingKey(projection.RowMode, projection.Ordering)) || partition != "" || !strings.HasPrefix(normalize(engineFull), "ReplacingMergeTree(ingest_version)") {
		return fmt.Errorf("projection table %s has incompatible physical layout", projection.TableName)
	}
	required := map[string]string{"exporter_id": "String", "batch_id": "FixedString(64)", "event_id": "FixedString(64)", "resource_id": "String", "conversation_id": "String", "conversation_group_id": "String", "ingest_version": "UInt64", "observed_at": "DateTime64(9, 'UTC')", "is_deleted": "UInt8"}
	if projection.RowMode == ProjectionRowsMany {
		required["row_index"] = "UInt32"
		required["row_count"] = "UInt32"
	}
	rows, err := s.conn.Query(ctx, "SELECT name,type FROM system.columns WHERE database=? AND table=?", s.database, projection.TableName)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return err
		}
		if expected, ok := required[name]; ok {
			canonical, err := s.canonicalClickHouseType(ctx, expected)
			if err != nil {
				return err
			}
			if kind != canonical {
				return fmt.Errorf("projection table %s has invalid lifecycle column %s", projection.TableName, name)
			}
			delete(required, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(required) != 0 {
		return fmt.Errorf("projection table %s is missing required lifecycle columns", projection.TableName)
	}
	return nil
}

// validateRegisteredProjection checks a registry row before its table name is used in DDL.
// The selector expression is empty for chain manifests that only replace logic or add
// columns; the resource prefix is always present.
func validateRegisteredProjection(name, table, selector string) (registeredProjection, error) {
	resource, _, found := strings.Cut(selector, ":")
	if !found || (resource != "entry" && resource != "memory" && resource != "conversation") {
		return registeredProjection{}, fmt.Errorf("projection %q has invalid registered selector %q", name, selector)
	}
	if !projectionNamePattern.MatchString(name) || !projectionNamePattern.MatchString(table) || isReservedProjectionTableName(table) {
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
				ddl := projectionCurrentViewStatement(s.database, projection.TableName, projection.Resource)
				if suffix == "_all" {
					ddl = projectionAllViewStatement(s.database, projection.TableName, projection.RowMode, projection.Ordering)
				}
				if err := s.validateView(ctx, projection.TableName+suffix, ddl); err != nil {
					return err
				}

			}
			continue
		}
		if err := s.conn.Exec(ctx, projectionAllViewStatement(s.database, projection.TableName, projection.RowMode, projection.Ordering)); err != nil {
			return fmt.Errorf("create projection all view %s: %w", projection.TableName, err)
		}
		if err := s.conn.Exec(ctx, projectionCurrentViewStatement(s.database, projection.TableName, projection.Resource)); err != nil {
			return fmt.Errorf("create projection view %s: %w", projection.TableName, err)
		}
	}
	return nil
}

func projectionAllViewStatement(database, table, rowMode, ordering string) string {
	prefix := quoteIdentifier(database) + "."
	partition := "r.exporter_id,r.resource_id"
	if ordering == "conversation" {
		partition = "r.exporter_id,r.conversation_id,r.resource_id"
	}
	if rowMode == ProjectionRowsMany {
		return fmt.Sprintf(`CREATE OR REPLACE VIEW %s%s AS
 SELECT r.* EXCEPT(_projection_version_rank,_projection_row_number) FROM (
 SELECT r.*, dense_rank() OVER (PARTITION BY %s ORDER BY r.ingest_version DESC,r.event_id DESC) _projection_version_rank,
 row_number() OVER (PARTITION BY %s,r.row_index ORDER BY r.ingest_version DESC,r.event_id DESC) _projection_row_number
 FROM %s%s r) r WHERE _projection_version_rank=1 AND _projection_row_number=1 AND (r.is_deleted=1 OR r.row_index<r.row_count)`, prefix, quoteIdentifier(table+"_all"), partition, partition, prefix, quoteIdentifier(table))
	}
	return fmt.Sprintf(`CREATE OR REPLACE VIEW %s%s AS SELECT * EXCEPT(_projection_row_number) FROM (
 SELECT r.*,row_number() OVER (PARTITION BY %s ORDER BY r.ingest_version DESC,r.event_id DESC) _projection_row_number
 FROM %s%s r) WHERE _projection_row_number=1`, prefix, quoteIdentifier(table+"_all"), partition, prefix, quoteIdentifier(table))
}
func projectionCurrentViewStatement(database, table, resource string) string {
	prefix := quoteIdentifier(database) + "."
	return fmt.Sprintf(`CREATE OR REPLACE VIEW %s%s AS SELECT r.* FROM %s%s r
 LEFT JOIN (%s) d ON r.exporter_id=d._fence_exporter_id AND d._fence_resource_kind='%s' AND r.resource_id=d._fence_resource_id
 LEFT JOIN (%s) g ON r.exporter_id=g._fence_exporter_id AND g._fence_resource_kind='conversation_group' AND r.conversation_group_id=g._fence_resource_id
 WHERE r.is_deleted=0 AND r.ingest_version>ifNull(d._fence_version,0) AND ifNull(g._fence_version,0)=0`, prefix, quoteIdentifier(table+"_current"), prefix, quoteIdentifier(table+"_all"), deletionFenceSubquery(prefix), resource, deletionFenceSubquery(prefix))
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
	if err := s.setProjectionTTL(ctx, "conversation_memberships"); err != nil {
		return err
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
	return []int64{int64(cfg.LifecycleRetention / time.Second), int64(cfg.TombstoneRetention / time.Second), int64(cfg.ProjectionRetention / time.Second), int64(cfg.ProjectionFailureRetention / time.Second)}
}

func retentionLivePredicate() string {
	return fmt.Sprintf("active=1 AND updated_at > now64(9) - toIntervalSecond(%d)", int64(retentionOwnerTTL/time.Second))
}

func (s *ClickHouseSink) writeRetentionOwner(ctx context.Context, active uint8) error {
	values := retentionPolicyValues(s.config)
	now := time.Now().UTC()
	return s.conn.Exec(ctx, fmt.Sprintf("INSERT INTO %s.retention_policy VALUES (?,?,?,?,?,?,?,?,?)", quoteIdentifier(s.database)), s.config.ExporterID, s.retentionOwner, values[0], values[1], values[2], values[3], active, uint64(now.UnixNano()), now)
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
	var lifecycle, tombstone, projection, failure int64
	query := fmt.Sprintf("SELECT uniqExact(tuple(lifecycle_seconds,tombstone_seconds,projection_seconds,failure_seconds)), any(lifecycle_seconds),any(tombstone_seconds),any(projection_seconds),any(failure_seconds) FROM %s.retention_policy FINAL WHERE %s", quoteIdentifier(s.database), retentionLivePredicate())
	if err := s.conn.QueryRow(ctx, query).Scan(&distinct, &lifecycle, &tombstone, &projection, &failure); err != nil {
		return err
	}
	if distinct > 1 || (distinct == 1 && (lifecycle != values[0] || tombstone != values[1] || projection != values[2] || failure != values[3])) {
		return s.retentionConflict("with the registered shared policy")
	}
	if err := s.writeRetentionOwner(ctx, 1); err != nil {
		return err
	}
	// Mark registration immediately so OpenSink's failure cleanup deactivates
	// this owner if the concurrent-policy check or later TTL setup fails.
	s.retentionRegistered = true
	if err := s.conn.QueryRow(ctx, fmt.Sprintf("SELECT uniqExact(tuple(lifecycle_seconds,tombstone_seconds,projection_seconds,failure_seconds)) FROM %s.retention_policy FINAL WHERE %s", quoteIdentifier(s.database), retentionLivePredicate())).Scan(&distinct); err != nil {
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
	targets = append(targets, struct {
		table   string
		clauses []string
	}{table: "conversation_memberships", clauses: s.projectionTTLClauses()})
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

func fixedStringPredicate(column string, values []string) (string, []any) {
	if len(values) == 0 {
		return "", nil
	}
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + ")", args
}
