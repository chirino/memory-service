package clickhouse

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

type ClickHouseSink struct {
	conn                driver.Conn
	database            string
	projections         []*Projection
	projectionTables    []*ProjectionTable
	config              Config
	retentionRegistered bool
	retentionOwner      string
	retentionCancel     context.CancelFunc
	retentionWG         sync.WaitGroup
	retentionFailureMu  sync.RWMutex
	retentionFailure    error
	retentionErrors     chan error
}

func OpenSink(ctx context.Context, cfg Config) (*ClickHouseSink, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	password, err := cfg.readPassword()
	if err != nil {
		return nil, err
	}
	tlsConfig, err := cfg.tlsConfig()
	if err != nil {
		return nil, err
	}
	protocol := ch.Native
	if cfg.Protocol == "http" {
		protocol = ch.HTTP
	}
	options := &ch.Options{
		Protocol: protocol,
		Addr:     append([]string(nil), cfg.Addresses...),
		// Owned SQL is qualified, so startup can claim a database that does not exist yet.
		Auth:        ch.Auth{Username: cfg.Username, Password: password},
		TLS:         cloneTLS(tlsConfig),
		DialTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second,
		Settings:    ch.Settings{"async_insert": 0},
		Compression: &ch.Compression{Method: ch.CompressionLZ4},
	}
	projections, err := LoadProjectionSet(ctx, cfg.ProjectionPaths)
	if err != nil {
		return nil, err
	}
	conn, err := ch.Open(options)
	if err != nil {
		return nil, fmt.Errorf("open ClickHouse: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping ClickHouse: %w", err)
	}
	sink := &ClickHouseSink{conn: conn, database: cfg.Database, projections: projections.Items, projectionTables: projections.Tables, config: cfg, retentionOwner: uuid.NewString(), retentionErrors: make(chan error, 1)}
	if err := sink.EnsureSchema(ctx, cfg.SchemaMode); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := sink.ensureProjectionSchema(ctx, cfg.SchemaMode); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := sink.configureInsertDeduplication(ctx, cfg.SchemaMode); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := sink.configureRetention(ctx, cfg.SchemaMode); err != nil {
		_ = sink.Close()
		return nil, err
	}
	return sink, nil
}

func cloneTLS(value *tls.Config) *tls.Config {
	if value == nil {
		return nil
	}
	return value.Clone()
}

// EnsureSchema claims only an empty database or validates this exact current baseline.
func (s *ClickHouseSink) EnsureSchema(ctx context.Context, mode string) error {
	statements := schemaStatements(s.database)
	sum := sha256.Sum256([]byte(strings.Join(statements[1:], "\n")))
	expected := hex.EncodeToString(sum[:])
	var tables, registry uint64
	if err := s.conn.QueryRow(ctx, "SELECT count(),countIf(name='schema_versions') FROM system.tables WHERE database=?", s.database).Scan(&tables, &registry); err != nil {
		return err
	}
	if registry == 0 {
		if mode == "validate" || tables != 0 {
			return errors.New("incompatible or unowned analytics database; use an empty dedicated database and fresh checkpoint")
		}
		for _, statement := range statements[:2] {
			if err := s.conn.Exec(ctx, statement); err != nil {
				return fmt.Errorf("initialize analytics schema: %w", err)
			}
		}
		if err := s.conn.Exec(ctx, fmt.Sprintf("INSERT INTO %s.schema_versions (component,checksum,applied_at) VALUES (?,?,?)", quoteIdentifier(s.database)), "clickhouse-export", expected, time.Now().UTC()); err != nil {
			return err
		}
	}
	var fields string
	if err := s.conn.QueryRow(ctx, "SELECT arrayStringConcat(arraySort(groupArray(name)),',') FROM system.columns WHERE database=? AND table='schema_versions'", s.database).Scan(&fields); err != nil {
		return err
	}
	if fields != "applied_at,checksum,component" {
		return errors.New("incompatible analytics schema registry; use a fresh database and checkpoint")
	}
	var count, distinct, other uint64
	var checksum string
	if err := s.conn.QueryRow(ctx, fmt.Sprintf("SELECT count(),uniqExact(checksum),any(checksum),countIf(component!='clickhouse-export') FROM %s.schema_versions", quoteIdentifier(s.database))).Scan(&count, &distinct, &checksum, &other); err != nil {
		return err
	}
	if count == 0 || distinct != 1 || other != 0 || checksum != expected {
		return errors.New("incompatible analytics schema checksum; use a fresh database and checkpoint")
	}
	if err := s.validateInfrastructure(ctx, mode != "validate"); err != nil {
		return err
	}
	if mode != "validate" {
		for _, statement := range statements[2:] {
			if err := s.conn.Exec(ctx, statement); err != nil {
				return fmt.Errorf("initialize analytics object: %w", err)
			}
		}
	}
	for _, name := range []string{"lifecycle_events", "deletion_fences", "projection_registry", "retention_policy", "projection_failures", "purge_queue", "conversation_memberships", "lifecycle_events_current", "conversation_memberships_current"} {
		var count uint64
		if err := s.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name=?", s.database, name).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("required analytics relation %s is missing; initialize in manage mode", name)
		}
	}
	return nil
}

func (s *ClickHouseSink) ResolvePurge(ctx context.Context, purge PurgeRow) (PurgeRow, error) {
	if err := validatePurge(purge); err != nil {
		return purge, err
	}
	prior := purge
	err := s.conn.QueryRow(ctx, fmt.Sprintf(`SELECT resource_kind,analytics_resource_id,conversation_id,conversation_group_id,purge_scope,cutoff_ingest_version,requested_at FROM %s.purge_queue FINAL WHERE exporter_id=? AND purge_id=?`, quoteIdentifier(s.database)), purge.ExporterID, purge.PurgeID).Scan(&prior.ResourceKind, &prior.AnalyticsResourceID, &prior.ConversationID, &prior.ConversationGroupID, &prior.Scope, &prior.CutoffIngestVersion, &prior.RequestedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return purge, nil
	}
	if err != nil {
		return purge, err
	}
	if prior.ResourceKind != purge.ResourceKind || prior.AnalyticsResourceID != purge.AnalyticsResourceID || prior.ConversationID != purge.ConversationID || prior.ConversationGroupID != purge.ConversationGroupID || prior.Scope != purge.Scope {
		return purge, errors.New("purge identity has conflicting immutable scope")
	}
	return prior, validatePurge(prior)
}
func validatePurge(p PurgeRow) error {
	if p.ExporterID == "" || p.PurgeID == "" || p.AnalyticsResourceID == "" || p.CutoffIngestVersion == 0 {
		return errors.New("invalid purge identity or cutoff")
	}
	if p.ResourceKind != "conversation" && p.ResourceKind != "entry" && p.ResourceKind != "memory" && p.ResourceKind != "membership" {
		return errors.New("invalid purge resource kind")
	}
	switch p.Scope {
	case purgeResource:
	case purgeGroup:
		group, err := uuid.Parse(p.ConversationGroupID)
		if p.ResourceKind != "conversation" || err != nil || group == uuid.Nil {
			return errors.New("invalid group purge scope")
		}
	default:
		return errors.New("invalid purge scope")
	}
	return nil
}
func commonValues(c Common) []any {
	return []any{c.ExporterID, c.BatchID, c.EventID, c.SourceCursor, c.IngestVersion, c.ObservedAt}
}

const commonInsertColumns = "exporter_id,batch_id,event_id,source_cursor,ingest_version,observed_at"

func (s *ClickHouseSink) sendRows(ctx context.Context, token, table, columns string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	insert, err := s.conn.PrepareBatch(insertContext(ctx, token), fmt.Sprintf("INSERT INTO %s.%s (%s)", quoteIdentifier(s.database), quoteIdentifier(table), columns))
	if err != nil {
		return fmt.Errorf("prepare analytics batch: %w", err)
	}
	defer insert.Close()
	for _, row := range rows {
		if err := insert.Append(row...); err != nil {
			return fmt.Errorf("append analytics row: %w", err)
		}
	}
	return insert.Send()
}
func (s *ClickHouseSink) WriteBatch(ctx context.Context, batch Batch) error {
	if err := s.retentionOwnershipError(); err != nil {
		return err
	}
	if batchRowCount(&batch) == 0 {
		return nil
	}
	if err := s.renewRetentionOwnership(ctx); err != nil {
		return err
	}
	for _, purge := range batch.Purges {
		if err := s.writePurge(ctx, purge, false); err != nil {
			return err
		}
	}
	rows := make([][]any, 0, len(batch.Fences))
	for _, row := range batch.Fences {
		rows = append(rows, append(commonValues(row.Common), row.ResourceKind, row.ResourceID, row.ConversationID, row.ConversationGroupID))
	}
	if err := s.sendRows(ctx, batch.ID+":fences", "deletion_fences", commonInsertColumns+",resource_kind,resource_id,conversation_id,conversation_group_id", rows); err != nil {
		return err
	}
	rows = nil
	for _, row := range batch.Lifecycle {
		rows = append(rows, append(commonValues(row.Common), row.OccurredAt, row.ResourceKind, row.AnalyticsResourceID, row.Action, row.Change, row.ConversationID, row.ConversationGroupID, row.ContentType, row.MemoryKind, boolByte(row.SnapshotAvailable)))
	}
	if err := s.sendRows(ctx, batch.ID+":lifecycle", "lifecycle_events", commonInsertColumns+",occurred_at,resource_kind,analytics_resource_id,action,change,conversation_id,conversation_group_id,content_type,memory_kind,snapshot_available", rows); err != nil {
		return err
	}
	rows = nil
	for _, row := range batch.Memberships {
		rows = append(rows, append(commonValues(row.Common), row.ResourceID, row.ConversationGroupID, row.UserID, row.AccessLevel, row.CreatedAt, boolByte(row.IsDeleted)))
	}
	if err := s.sendRows(ctx, batch.ID+":memberships", "conversation_memberships", commonInsertColumns+",resource_id,conversation_group_id,user_id,access_level,created_at,is_deleted", rows); err != nil {
		return err
	}
	if err := s.writeProjectionRows(ctx, batch); err != nil {
		return err
	}
	rows = nil
	for _, row := range batch.ProjectionFailures {
		rows = append(rows, append(commonValues(row.Common), row.ResourceKind, row.AnalyticsResourceID, row.ConversationID, row.ConversationGroupID, row.ProjectionName, row.ErrorCode, row.AttemptCount, row.FirstSeenAt, row.LastSeenAt))
	}
	if err := s.sendRows(ctx, batch.ID+":failures", "projection_failures", commonInsertColumns+",resource_kind,analytics_resource_id,conversation_id,conversation_group_id,projection_name,error_code,attempt_count,first_seen_at,last_seen_at", rows); err != nil {
		return err
	}
	if s.config.PurgeMode == PurgeManaged {
		return s.processPurges(ctx, batch)
	}
	return nil
}

const purgeInsertColumns = "exporter_id,batch_id,purge_id,event_id,resource_kind,analytics_resource_id,conversation_id,conversation_group_id,purge_scope,cutoff_ingest_version,requested_at,completed_at,status,status_code,status_version"

func (s *ClickHouseSink) writePurge(ctx context.Context, purge PurgeRow, complete bool) error {
	if err := validatePurge(purge); err != nil {
		return err
	}
	status, code, version := "pending", "", uint8(0)
	var completed *time.Time
	if complete {
		status = "complete"
		code = "mutations_complete"
		version = 1
		now := time.Now().UTC()
		completed = &now
	}
	row := []any{purge.ExporterID, purge.BatchID, purge.PurgeID, purge.EventID, purge.ResourceKind, purge.AnalyticsResourceID, purge.ConversationID, purge.ConversationGroupID, purge.Scope, purge.CutoffIngestVersion, purge.RequestedAt, completed, status, code, version}
	return s.sendRows(ctx, purge.PurgeID+":"+status, "purge_queue", purgeInsertColumns, [][]any{row})
}
func (s *ClickHouseSink) processPurges(ctx context.Context, batch Batch) error {
	projections, err := s.registeredProjections(ctx)
	if err != nil {
		return err
	}
	mutationCtx := ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	for _, purge := range batch.Purges {
		if err := validatePurge(purge); err != nil {
			return err
		}
		for _, table := range []string{"lifecycle_events", "projection_failures", "conversation_memberships"} {
			kindColumn, idColumn := "resource_kind", "analytics_resource_id"
			fixedKind := ""
			if table == "conversation_memberships" {
				kindColumn = ""
				idColumn = "resource_id"
				fixedKind = "membership"
			}
			if err := s.purgeTable(mutationCtx, table, kindColumn, idColumn, fixedKind, purge); err != nil {
				return s.failPurge(purge, err)
			}
		}
		for _, projection := range projections {
			if err := s.purgeTable(mutationCtx, projection.TableName, "", "resource_id", projection.Resource, purge); err != nil {
				return s.failPurge(purge, err)
			}
		}
		if err := s.writePurge(ctx, purge, true); err != nil {
			return s.failPurge(purge, err)
		}
		processorPurges.WithLabelValues(purge.ExporterID, purge.ResourceKind, "success").Inc()
		processorPurgeDelay.WithLabelValues(purge.ExporterID, purge.ResourceKind).Set(max(0, time.Since(purge.RequestedAt).Seconds()))
	}
	return nil
}
func (s *ClickHouseSink) purgeTable(ctx context.Context, table, kindColumn, idColumn, fixedKind string, purge PurgeRow) error {
	predicate := "ingest_version <= ? AND "
	args := []any{purge.CutoffIngestVersion}
	if purge.Scope == purgeGroup {
		predicate += "conversation_group_id=?"
		args = append(args, purge.ConversationGroupID)
	} else {
		if fixedKind != "" && fixedKind != purge.ResourceKind {
			return nil
		}
		if kindColumn != "" {
			predicate += kindColumn + "=? AND "
			args = append(args, purge.ResourceKind)
		}
		predicate += idColumn + "=?"
		args = append(args, purge.AnalyticsResourceID)
	}
	return s.deletePurgeRows(ctx, table, purge.ExporterID, predicate, args)
}
func (s *ClickHouseSink) deletePurgeRows(ctx context.Context, table, exporter, predicate string, args []any) error {
	if predicate == "" {
		return errors.New("empty purge predicate")
	}
	return s.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE %s.%s DELETE WHERE exporter_id=? AND (%s)", quoteIdentifier(s.database), quoteIdentifier(table), predicate), append([]any{exporter}, args...)...)
}
func (s *ClickHouseSink) failPurge(purge PurgeRow, err error) error {
	processorPurges.WithLabelValues(purge.ExporterID, purge.ResourceKind, "failed").Inc()
	return err
}

func (s *ClickHouseSink) writeProjectionRows(ctx context.Context, batch Batch) error {
	byTable := map[string][]ProjectionRow{}
	for _, row := range batch.Projections {
		byTable[row.TableName] = append(byTable[row.TableName], row)
	}
	tables := make([]string, 0, len(byTable))
	for table := range byTable {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		rows := byTable[table]
		if len(rows) == 0 {
			continue
		}
		columns := []string{"exporter_id", "batch_id", "event_id", "resource_id", "conversation_id", "conversation_group_id", "ingest_version", "observed_at", "is_deleted"}
		multi := rows[0].Multi
		if multi {
			columns = append(columns, "row_index", "row_count")
		}
		columns = append(columns, rows[0].ColumnNames...)
		quoted := make([]string, len(columns))
		for i, column := range columns {
			quoted[i] = "`" + column + "`"
		}
		insert, err := s.conn.PrepareBatch(insertContext(ctx, batch.ID+":"+table), fmt.Sprintf("INSERT INTO `%s`.`%s` (%s)", s.database, table, strings.Join(quoted, ",")))
		if err != nil {
			return fmt.Errorf("prepare projection %s batch: %w", table, err)
		}
		defer insert.Close()
		for _, row := range rows {
			if row.Multi != multi {
				return fmt.Errorf("projection %s mixes single-row and multi-row writes", table)
			}
			values := []any{row.ExporterID, row.BatchID, row.EventID, row.ResourceID, row.ConversationID, row.ConversationGroupID, row.IngestVersion, row.ObservedAt, boolByte(row.IsDeleted)}
			if multi {
				values = append(values, row.RowIndex, row.RowCount)
			}
			values = append(values, row.Values...)
			if err := insert.Append(values...); err != nil {
				return fmt.Errorf("append projection %s row: %w", row.ProjectionName, err)
			}
		}
		if err := insert.Send(); err != nil {
			return fmt.Errorf("send projection %s batch: %w", table, err)
		}
	}
	return nil
}

func insertContext(ctx context.Context, token string) context.Context {
	return ch.Context(ctx, ch.WithSettings(ch.Settings{"async_insert": 0, "insert_deduplicate": 1, "insert_deduplication_token": token}))
}

func boolByte(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}

func (s *ClickHouseSink) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	var unregisterErr error
	s.stopRetentionHeartbeat()
	if s.retentionRegistered {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		unregisterErr = s.writeRetentionOwner(ctx, 0)
		cancel()
		s.retentionRegistered = false
	}
	return errors.Join(unregisterErr, s.conn.Close())
}
