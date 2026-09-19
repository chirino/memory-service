package clickhouse

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
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
		Protocol:    protocol,
		Addr:        append([]string(nil), cfg.Addresses...),
		Auth:        ch.Auth{Database: cfg.Database, Username: cfg.Username, Password: password},
		TLS:         cloneTLS(tlsConfig),
		DialTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second,
		Settings:    ch.Settings{"async_insert": 0},
		Compression: &ch.Compression{Method: ch.CompressionLZ4},
	}
	conn, err := ch.Open(options)
	if err != nil {
		return nil, fmt.Errorf("open ClickHouse: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping ClickHouse: %w", err)
	}
	projections, err := LoadProjectionSet(ctx, cfg.ProjectionPaths)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	sink := &ClickHouseSink{conn: conn, database: cfg.Database, projections: projections.Items, config: cfg, retentionOwner: uuid.NewString(), retentionErrors: make(chan error, 1)}
	if err := sink.EnsureSchema(ctx, cfg.SchemaMode); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := sink.ensureProjectionSchema(ctx, cfg.SchemaMode); err != nil {
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

func (s *ClickHouseSink) EnsureSchema(ctx context.Context, mode string) error {
	if s == nil || s.conn == nil {
		return errors.New("ClickHouse connection is required")
	}
	statements := schemaStatements(s.database)
	checksum := sha256.Sum256([]byte(fmt.Sprint(statements[2:], deletionFenceMaterializedViewStatement(s.database))))
	expectedChecksum := hex.EncodeToString(checksum[:])
	if mode == "validate" {
		for _, table := range []string{"schema_versions", "lifecycle_events", "resources", "deletion_fences", "deletion_fence_writer", "conversations", "conversation_lineage", "entries", "memories", "projection_registry", "retention_policy", "projection_failures", "purge_subjects", "purge_queue"} {
			var count uint64
			if err := s.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name=?", s.database, table).Scan(&count); err != nil {
				return fmt.Errorf("validate ClickHouse table %s: %w", table, err)
			}
			if count != 1 {
				return fmt.Errorf("required ClickHouse table %s.%s is missing", s.database, table)
			}
		}
		return s.validateSchemaVersion(ctx, expectedChecksum)
	}
	for _, statement := range statements[:2] {
		if err := s.conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply ClickHouse schema: %w", err)
		}
	}
	var existing, other uint64
	query := fmt.Sprintf("SELECT countIf(schema_version=?), countIf(schema_version!=?) FROM `%s`.schema_versions WHERE component=?", s.database)
	if err := s.conn.QueryRow(ctx, query, uint16(schemaVersion), uint16(schemaVersion), "clickhouse-export").Scan(&existing, &other); err != nil {
		return fmt.Errorf("read ClickHouse schema state: %w", err)
	}
	if other > 0 {
		return fmt.Errorf("unsupported prerelease ClickHouse schema detected; reset database %s before starting the exporter", s.database)
	}
	for _, statement := range statements[2:] {
		if err := s.conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("apply ClickHouse schema: %w", err)
		}
	}
	if err := s.conn.Exec(ctx, deletionFenceMaterializedViewStatement(s.database)); err != nil {
		return fmt.Errorf("create ClickHouse deletion fence writer: %w", err)
	}
	if existing == 0 {
		if err := s.conn.Exec(ctx, fmt.Sprintf("INSERT INTO `%s`.schema_versions VALUES (?, ?, ?, ?)", s.database), "clickhouse-export", uint16(schemaVersion), expectedChecksum, time.Now().UTC()); err != nil {
			return fmt.Errorf("record ClickHouse schema version: %w", err)
		}
	}
	return s.validateSchemaVersion(ctx, expectedChecksum)
}

func (s *ClickHouseSink) validateSchemaVersion(ctx context.Context, expectedChecksum string) error {
	var count, distinct uint64
	var checksum string
	err := s.conn.QueryRow(ctx, fmt.Sprintf(`SELECT count(), uniqExact(checksum), any(checksum)
		FROM %s.schema_versions WHERE component=? AND schema_version=?`, "`"+s.database+"`"), "clickhouse-export", uint16(schemaVersion)).Scan(&count, &distinct, &checksum)
	if err != nil {
		return fmt.Errorf("validate ClickHouse schema version: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("required ClickHouse schema version %d is missing", schemaVersion)
	}
	if distinct != 1 || checksum != expectedChecksum {
		return fmt.Errorf("ClickHouse schema version %d checksum mismatch", schemaVersion)
	}
	return nil
}

func (s *ClickHouseSink) WriteBatch(ctx context.Context, batch Batch) error {
	if err := s.retentionOwnershipError(); err != nil {
		return err
	}
	if len(batch.Lifecycle) == 0 && len(batch.Resources) == 0 && len(batch.Projections) == 0 && len(batch.ProjectionFailures) == 0 && len(batch.Purges) == 0 {
		return nil
	}
	if err := s.renewRetentionOwnership(ctx); err != nil {
		return err
	}
	if len(batch.Lifecycle) > 0 {
		settingsCtx := insertContext(ctx, batch.ID+":lifecycle:v1")
		insert, err := s.conn.PrepareBatch(settingsCtx, fmt.Sprintf("INSERT INTO `%s`.lifecycle_events", s.database))
		if err != nil {
			return fmt.Errorf("prepare lifecycle batch: %w", err)
		}
		for _, row := range batch.Lifecycle {
			c := row.Common
			if err := insert.Append(c.ExporterID, c.BatchID, c.EventID, c.SourceCursor, c.IngestVersion, c.ObservedAt, c.SchemaVersion, row.OccurredAt, row.ResourceKind, row.AnalyticsResourceID, row.Action, row.Change, row.ConversationID, row.ConversationGroupID, row.ContentType, row.MemoryKind, boolByte(row.SnapshotAvailable), row.SummaryJSON); err != nil {
				return err
			}
		}
		if err := insert.Send(); err != nil {
			return fmt.Errorf("send lifecycle batch: %w", err)
		}
	}
	if len(batch.Resources) > 0 {
		if err := s.writeResourceTable(ctx, batch, "resources", batch.Resources); err != nil {
			return err
		}
		if err := s.writeDeletionFences(ctx, batch); err != nil {
			return err
		}
		for _, target := range []struct{ resourceType, table string }{
			{"conversation", "conversations"},
			{"lineage", "conversation_lineage"},
			{"entry", "entries"},
			{"memory", "memories"},
		} {
			rows := make([]ResourceRow, 0)
			for _, row := range batch.Resources {
				if row.ResourceType == target.resourceType {
					rows = append(rows, row)
				}
			}
			if len(rows) > 0 {
				if err := s.writeResourceTable(ctx, batch, target.table, rows); err != nil {
					return err
				}
			}
		}
	}
	if len(batch.Purges) > 0 && s.config.PurgeMode != PurgeExternal {
		settingsCtx := insertContext(ctx, batch.ID+":purges:v1")
		insert, err := s.conn.PrepareBatch(settingsCtx, fmt.Sprintf("INSERT INTO `%s`.purge_queue", s.database))
		if err != nil {
			return fmt.Errorf("prepare purge batch: %w", err)
		}
		for _, row := range batch.Purges {
			if err := insert.Append(row.ExporterID, row.BatchID, row.PurgeID, row.EventID, row.ResourceKind, row.AnalyticsResourceID, row.RequestedAt, nil, "pending", "", uint64(1)); err != nil {
				return err
			}
		}
		if err := insert.Send(); err != nil {
			return fmt.Errorf("send purge batch: %w", err)
		}
	}
	if err := s.writeProjectionRows(ctx, batch); err != nil {
		return err
	}
	if len(batch.ProjectionFailures) > 0 {
		insert, err := s.conn.PrepareBatch(insertContext(ctx, batch.ID+":projection-failures:v1"), fmt.Sprintf("INSERT INTO `%s`.projection_failures (exporter_id, batch_id, event_id, analytics_resource_id, conversation_id, conversation_group_id, projection_name, error_code, attempt_count, first_seen_at, last_seen_at, version)", s.database))
		if err != nil {
			return fmt.Errorf("prepare projection failure batch: %w", err)
		}
		for _, row := range batch.ProjectionFailures {
			if err := insert.Append(row.ExporterID, row.BatchID, row.EventID, row.AnalyticsResourceID, row.ConversationID, row.ConversationGroupID, row.ProjectionName, row.ErrorCode, row.AttemptCount, row.FirstSeenAt, row.LastSeenAt, row.Version); err != nil {
				return err
			}
		}
		if err := insert.Send(); err != nil {
			return fmt.Errorf("send projection failure batch: %w", err)
		}
	}
	if len(batch.Purges) > 0 && s.config.PurgeMode == PurgeManaged {
		if err := s.processPurges(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

func (s *ClickHouseSink) writeResourceTable(ctx context.Context, batch Batch, table string, rows []ResourceRow) error {
	settingsCtx := insertContext(ctx, batch.ID+":"+table+":v1")
	insert, err := s.conn.PrepareBatch(settingsCtx, fmt.Sprintf("INSERT INTO `%s`.%s", s.database, table))
	if err != nil {
		return fmt.Errorf("prepare %s batch: %w", table, err)
	}
	for _, row := range rows {
		c := row.Common
		if err := insert.Append(c.ExporterID, c.BatchID, c.EventID, c.SourceCursor, c.IngestVersion, c.ObservedAt, c.SchemaVersion, row.ResourceID, row.ConversationID, row.ConversationGroupID, row.ResourceType, row.CreatedAt, row.UpdatedAt, boolByte(row.IsArchived), boolByte(row.IsDeleted), row.PayloadJSON); err != nil {
			return err
		}
	}
	if err := insert.Send(); err != nil {
		return fmt.Errorf("send %s batch: %w", table, err)
	}
	return nil
}

func (s *ClickHouseSink) writeDeletionFences(ctx context.Context, batch Batch) error {
	if deletionFenceCount(batch.Resources) == 0 {
		return nil
	}
	insert, err := s.conn.PrepareBatch(insertContext(ctx, batch.ID+":deletion-fences:v1"), fmt.Sprintf("INSERT INTO `%s`.deletion_fences (exporter_id, batch_id, event_id, resource_type, resource_id, conversation_id, conversation_group_id, ingest_version, observed_at)", s.database))
	if err != nil {
		return fmt.Errorf("prepare deletion fence batch: %w", err)
	}
	for _, row := range batch.Resources {
		if !row.IsDeleted {
			continue
		}
		if err := insert.Append(row.ExporterID, row.BatchID, row.EventID, row.ResourceType, row.ResourceID, row.ConversationID, row.ConversationGroupID, row.IngestVersion, row.ObservedAt); err != nil {
			return err
		}
	}
	if err := insert.Send(); err != nil {
		return fmt.Errorf("send deletion fence batch: %w", err)
	}
	return nil
}

func deletionFenceCount(rows []ResourceRow) int {
	count := 0
	for _, row := range rows {
		if row.IsDeleted {
			count++
		}
	}
	return count
}

func (s *ClickHouseSink) processPurges(ctx context.Context, batch Batch) error {
	mutationCtx := ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	registeredProjections, err := s.registeredProjections(ctx)
	if err != nil {
		return fmt.Errorf("load registered projections for purge: %w", err)
	}
	for _, purge := range batch.Purges {
		var priorStatus string
		_ = s.conn.QueryRow(ctx, fmt.Sprintf("SELECT argMax(status,status_version) FROM %s.purge_queue WHERE exporter_id=? AND purge_id=?", quoteIdentifier(s.database)), purge.ExporterID, purge.PurgeID).Scan(&priorStatus)
		if priorStatus == "ready" {
			if err := s.completePurge(ctx, purge); err != nil {
				return err
			}
			continue
		}
		subjects, err := s.loadPurgeSubjects(ctx, purge)
		if err != nil {
			return s.failPurge(purge, err)
		}
		if err := s.persistPurgeSubjects(ctx, purge, subjects); err != nil {
			return s.failPurge(purge, fmt.Errorf("persist purge subjects: %w", err))
		}
		if err := s.mergePersistedPurgeSubjects(ctx, purge, subjects); err != nil {
			return s.failPurge(purge, fmt.Errorf("load persisted purge subjects: %w", err))
		}
		allResourceIDs := flattenPurgeSubjects(subjects)
		predicate, args := purgePredicate("analytics_resource_id", allResourceIDs, purge, "conversation_id", "conversation_group_id")
		if err := s.deletePurgeRows(mutationCtx, "lifecycle_events", purge.ExporterID, predicate, args); err != nil {
			return s.failPurge(purge, fmt.Errorf("purge lifecycle rows: %w", err))
		}
		for _, projection := range registeredProjections {
			predicate, args = purgePredicate("resource_id", subjects[projection.Resource], purge, "conversation_id", "conversation_group_id")
			if predicate == "" {
				continue
			}
			if err := s.deletePurgeRows(mutationCtx, projection.TableName, purge.ExporterID, predicate, args); err != nil {
				return s.failPurge(purge, fmt.Errorf("purge projection %s rows: %w", projection.Name, err))
			}
		}
		predicate, args = purgePredicate("analytics_resource_id", allResourceIDs, purge, "conversation_id", "conversation_group_id")
		if err := s.deletePurgeRows(mutationCtx, "projection_failures", purge.ExporterID, predicate, args); err != nil {
			return s.failPurge(purge, fmt.Errorf("purge projection failure rows: %w", err))
		}
		// Delete typed and generic discovery sources only after all dependent rows.
		for _, target := range []struct{ kind, table string }{{"conversation", "conversations"}, {"lineage", "conversation_lineage"}, {"entry", "entries"}, {"memory", "memories"}} {
			predicate, args = purgePredicate("resource_id", subjects[target.kind], purge, "conversation_id", "conversation_group_id")
			if predicate != "" {
				if err := s.deletePurgeRows(mutationCtx, target.table, purge.ExporterID, predicate, args); err != nil {
					return s.failPurge(purge, fmt.Errorf("purge %s rows: %w", target.kind, err))
				}
			}
		}
		predicate, args = purgePredicate("resource_id", allResourceIDs, purge, "conversation_id", "conversation_group_id")
		if err := s.deletePurgeRows(mutationCtx, "resources", purge.ExporterID, predicate, args); err != nil {
			return s.failPurge(purge, fmt.Errorf("purge resource rows: %w", err))
		}
		predicate, args = purgePredicate("resource_id", allResourceIDs, purge, "conversation_id", "conversation_group_id")
		if err := s.deletePurgeRows(mutationCtx, "deletion_fences", purge.ExporterID, predicate, args); err != nil {
			return s.failPurge(purge, fmt.Errorf("purge deletion fence: %w", err))
		}
		if err := s.conn.Exec(ctx, fmt.Sprintf("INSERT INTO `%s`.purge_queue VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", s.database), purge.ExporterID, purge.BatchID, purge.PurgeID, purge.EventID, purge.ResourceKind, purge.AnalyticsResourceID, purge.RequestedAt, time.Now().UTC(), "ready", "mutations_complete", uint64(2)); err != nil {
			return s.failPurge(purge, err)
		}
		if err := s.deletePurgeRows(mutationCtx, "purge_subjects", purge.ExporterID, "purge_id=?", []any{purge.PurgeID}); err != nil {
			return s.failPurge(purge, err)
		}
		if err := s.completePurge(ctx, purge); err != nil {
			return err
		}
	}
	return nil
}

func (s *ClickHouseSink) completePurge(ctx context.Context, purge PurgeRow) error {
	if err := s.conn.Exec(insertContext(ctx, purge.PurgeID+":complete:v1"), fmt.Sprintf("INSERT INTO `%s`.purge_queue VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", s.database), purge.ExporterID, purge.BatchID, purge.PurgeID, purge.EventID, purge.ResourceKind, purge.AnalyticsResourceID, purge.RequestedAt, time.Now().UTC(), "complete", "ok", uint64(3)); err != nil {
		processorPurges.WithLabelValues(purge.ExporterID, purge.ResourceKind, "failed").Inc()
		return fmt.Errorf("complete purge request: %w", err)
	}
	processorPurges.WithLabelValues(purge.ExporterID, purge.ResourceKind, "success").Inc()
	processorPurgeDelay.WithLabelValues(purge.ExporterID, purge.ResourceKind).Set(max(0, time.Since(purge.RequestedAt).Seconds()))
	return nil
}

func (s *ClickHouseSink) loadPurgeSubjects(ctx context.Context, purge PurgeRow) (map[string][]string, error) {
	subjects := map[string][]string{purge.ResourceKind: {purge.AnalyticsResourceID}}
	if purge.ResourceKind == "conversation" {
		predicate, args := purgePredicate("resource_id", []string{purge.AnalyticsResourceID}, purge, "conversation_id", "conversation_group_id")
		queryArgs := append([]any{purge.ExporterID}, args...)
		rows, err := s.conn.Query(ctx, fmt.Sprintf("SELECT DISTINCT resource_type, toString(resource_id) FROM %s.resources WHERE exporter_id=? AND (%s)", quoteIdentifier(s.database), predicate), queryArgs...)
		if err != nil {
			return nil, fmt.Errorf("load dependent purge subjects: %w", err)
		}
		for rows.Next() {
			var kind, id string
			if err := rows.Scan(&kind, &id); err != nil {
				_ = rows.Close()
				return nil, err
			}
			subjects[kind] = append(subjects[kind], id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	for kind, ids := range subjects {
		subjects[kind] = uniqueStrings(ids)
	}
	return subjects, nil
}

func (s *ClickHouseSink) persistPurgeSubjects(ctx context.Context, purge PurgeRow, subjects map[string][]string) error {
	for kind, ids := range subjects {
		for _, id := range ids {
			if err := s.conn.Exec(ctx, fmt.Sprintf("INSERT INTO %s.purge_subjects (exporter_id,purge_id,subject_kind,subject_id,version) VALUES (?,?,?,?,?)", quoteIdentifier(s.database)), purge.ExporterID, purge.PurgeID, kind, id, uint64(1)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ClickHouseSink) mergePersistedPurgeSubjects(ctx context.Context, purge PurgeRow, subjects map[string][]string) error {
	rows, err := s.conn.Query(ctx, fmt.Sprintf("SELECT subject_kind,toString(subject_id) FROM %s.purge_subjects WHERE exporter_id=? AND purge_id=?", quoteIdentifier(s.database)), purge.ExporterID, purge.PurgeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			return err
		}
		subjects[kind] = append(subjects[kind], id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for kind, ids := range subjects {
		subjects[kind] = uniqueStrings(ids)
	}
	return nil
}

func purgePredicate(idColumn string, ids []string, purge PurgeRow, conversationColumn, groupColumn string) (string, []any) {
	predicate, args := fixedStringPredicate(idColumn, ids)
	clauses := []string{}
	if predicate != "" {
		clauses = append(clauses, predicate)
	}
	if purge.ResourceKind == "conversation" && purge.ConversationID != "" && conversationColumn != "" {
		clauses = append(clauses, conversationColumn+"=?")
		args = append(args, purge.ConversationID)
	}
	if purge.ResourceKind == "conversation" && purge.ConversationGroupID != "" && groupColumn != "" {
		clauses = append(clauses, groupColumn+"=?")
		args = append(args, purge.ConversationGroupID)
	}
	return strings.Join(clauses, " OR "), args
}

func fixedStringPredicate(column string, ids []string) (string, []any) {
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return "", nil
	}
	args := make([]any, len(ids))
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		placeholders[i] = "?"
	}
	return column + " IN (" + strings.Join(placeholders, ",") + ")", args
}

func flattenPurgeSubjects(subjects map[string][]string) []string {
	ids := []string{}
	for _, values := range subjects {
		ids = append(ids, values...)
	}
	return uniqueStrings(ids)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (s *ClickHouseSink) deletePurgeRows(ctx context.Context, table, exporterID, predicate string, args []any) error {
	if predicate == "" {
		return nil
	}
	queryArgs := append([]any{exporterID}, args...)
	return s.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE %s.%s DELETE WHERE exporter_id=? AND (%s)", quoteIdentifier(s.database), quoteIdentifier(table), predicate), queryArgs...)
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
		columns := []string{"exporter_id", "batch_id", "event_id", "resource_id", "conversation_id", "conversation_group_id", "ingest_version", "observed_at", "is_deleted", "schema_version"}
		columns = append(columns, rows[0].ColumnNames...)
		quoted := make([]string, len(columns))
		for i, column := range columns {
			quoted[i] = "`" + column + "`"
		}
		insert, err := s.conn.PrepareBatch(insertContext(ctx, batch.ID+":"+table+":v1"), fmt.Sprintf("INSERT INTO `%s`.`%s` (%s)", s.database, table, strings.Join(quoted, ",")))
		if err != nil {
			return fmt.Errorf("prepare projection %s batch: %w", table, err)
		}
		for _, row := range rows {
			values := []any{row.ExporterID, row.BatchID, row.EventID, row.ResourceID, row.ConversationID, row.ConversationGroupID, row.IngestVersion, row.ObservedAt, boolByte(row.IsDeleted), row.SchemaVersion}
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

func resourceTableForKind(kind string) string {
	switch kind {
	case "conversation":
		return "conversations"
	case "entry":
		return "entries"
	case "memory":
		return "memories"
	case "lineage":
		return "conversation_lineage"
	default:
		return ""
	}
}

func insertContext(ctx context.Context, token string) context.Context {
	return ch.Context(ctx, ch.WithSettings(ch.Settings{"async_insert": 0, "insert_deduplication_token": token}))
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
