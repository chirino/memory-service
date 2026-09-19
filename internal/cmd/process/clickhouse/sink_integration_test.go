package clickhouse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func integrationNativeAddress() string {
	if address := os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE_ADDRESS"); address != "" {
		return address
	}
	return "localhost:9002"
}

func integrationHTTPAddress() string {
	if address := os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE_HTTP_ADDRESS"); address != "" {
		return address
	}
	return "localhost:8123"
}

func TestClickHouseSinkIntegration(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("memory-service-analytics"), 0o600))
	cfg := Config{ExporterID: "integration", Addresses: []string{integrationNativeAddress()}, Username: "memory_service_analytics", PasswordFile: filepath.Join(dir, "password"), AllowInsecureClickHouse: true}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	processor, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(1))
	created := processruntime.EventEnvelope{Event: "created", Kind: "conversation", Cursor: "integration:1", Time: time.Now().UTC(), Data: []byte(`{"conversation":"test-conversation","conversation_group":"test-group"}`)}
	require.NoError(t, processor.Handle(ctx, created))
	require.NoError(t, processor.Flush(ctx))
	var count uint64
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM memory_service.resources_current WHERE exporter_id=? AND source_cursor=?", "integration", "integration:1").Scan(&count))
	require.Equal(t, uint64(1), count)
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM memory_service.conversations_current WHERE exporter_id=? AND source_cursor=?", "integration", "integration:1").Scan(&count))
	require.Equal(t, uint64(1), count)
	require.NoError(t, processor.Handle(ctx, created))
	require.NoError(t, processor.Flush(ctx))
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM memory_service.lifecycle_events_current WHERE exporter_id=? AND source_cursor=?", "integration", "integration:1").Scan(&count))
	require.Equal(t, uint64(1), count, "replay must expose one logical event")
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM memory_service.conversations_current WHERE exporter_id=? AND source_cursor=?", "integration", "integration:1").Scan(&count))
	require.Equal(t, uint64(1), count, "replay must expose one logical resource")
	require.NoError(t, processor.Handle(ctx, processruntime.EventEnvelope{Event: "deleted", Kind: "conversation", Cursor: "integration:2", Time: time.Now().UTC(), Data: []byte(`{"conversation":"test-conversation","conversation_group":"test-group","change":"hard_deleted"}`)}))
	require.NoError(t, processor.Flush(ctx))
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM memory_service.resources_current WHERE exporter_id=? AND resource_type=?", "integration", "conversation").Scan(&count))
	require.Equal(t, uint64(0), count)
	var status string
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT argMax(status, status_version) FROM memory_service.purge_queue WHERE exporter_id=?", "integration").Scan(&status))
	require.Equal(t, "complete", status)
}

func TestClickHouseDirectLineageSupportsRecursiveTraversal(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, _ := createIntegrationDatabase(t, ctx)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	cfg := Config{ExporterID: "direct-lineage", Database: database, Addresses: []string{integrationNativeAddress()}, Username: "clickhouse", PasswordFile: filepath.Join(dir, "password"), AllowInsecureClickHouse: true}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	processor, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(1))
	root, child, rootEntry, childEntry := "root", "child", "root-entry", "child-entry"
	records := map[string]*pb.AnalyticsRecord{
		"child":      {Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_CONVERSATION, Id: "child"}, SnapshotAvailable: true, Record: &pb.AnalyticsRecord_Conversation{Conversation: &pb.AnalyticsConversationRecord{Id: "child", ConversationGroupId: "group", ForkedAtConversationId: &root, ForkedAtEntryId: &rootEntry}}},
		"grandchild": {Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_CONVERSATION, Id: "grandchild"}, SnapshotAvailable: true, Record: &pb.AnalyticsRecord_Conversation{Conversation: &pb.AnalyticsConversationRecord{Id: "grandchild", ConversationGroupId: "group", ForkedAtConversationId: &child, ForkedAtEntryId: &childEntry}}},
	}
	for _, event := range []processruntime.EventEnvelope{
		{Event: "created", Kind: "conversation", Cursor: "lineage:child", Time: time.Now().UTC(), Data: testFullResourceData(t, records["child"])},
		{Event: "created", Kind: "conversation", Cursor: "lineage:grandchild", Time: time.Now().UTC(), Data: testFullResourceData(t, records["grandchild"])},
	} {
		require.NoError(t, processor.Handle(ctx, event))
	}
	require.NoError(t, processor.Flush(ctx))

	query := `WITH RECURSIVE ancestry AS (
		SELECT JSONExtractString(payload_json, 'ancestorConversationId') AS ancestor_id,
		       JSONExtractString(payload_json, 'descendantConversationId') AS descendant_id,
		       toUInt32(1) AS depth
		FROM ` + quoteIdentifier(database) + `.conversation_lineage_current
		WHERE exporter_id = ? AND descendant_id = 'grandchild'
		UNION ALL
		SELECT JSONExtractString(parent.payload_json, 'ancestorConversationId') AS ancestor_id,
		       ancestry.descendant_id AS descendant_id,
		       ancestry.depth + 1 AS depth
		FROM ` + quoteIdentifier(database) + `.conversation_lineage_current AS parent
		INNER JOIN ancestry ON JSONExtractString(parent.payload_json, 'descendantConversationId') = ancestry.ancestor_id
		WHERE parent.exporter_id = ?
	)
	SELECT countIf(ancestor_id = 'child' AND depth = 1), countIf(ancestor_id = 'root' AND depth = 2)
	FROM ancestry`
	var direct, transitive uint64
	require.NoError(t, sink.conn.QueryRow(ctx, query, cfg.ExporterID, cfg.ExporterID).Scan(&direct, &transitive))
	require.Equal(t, uint64(1), direct)
	require.Equal(t, uint64(1), transitive)
}

func TestClickHouseProjectionIntegration(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("memory-service-analytics"), 0o600))
	manifest := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_v2}
spec:
  resource: entry
  selector: {contentType: support-ticket/v1}
  columns:
    details:
      type: json
      nullable: false
      clickhouse: {maxDynamicPaths: 128, maxDynamicTypes: 8}
    outcome: {type: string, nullable: false}
    result: {type: variant, variants: [string, int64, bool], nullable: false}
    latency_ms: {type: uint64, nullable: false}
    tags: {type: string_array, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {
      "details": input.content.details,
      "outcome": input.content.outcome,
      "result": {"type": "string", "value": input.content.outcome},
      "latency_ms": input.content.latencyMs,
      "tags": input.content.tags,
    }
`
	manifestPath := filepath.Join(dir, "projection.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))
	cfg := Config{ExporterID: "integration-projection", Addresses: []string{integrationNativeAddress()}, Username: "memory_service_analytics", PasswordFile: filepath.Join(dir, "password"), AllowInsecureClickHouse: true, PayloadMode: PayloadProjected, ProjectionPaths: []string{manifestPath}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	processor, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(2))
	content, err := structpb.NewValue(map[string]any{"outcome": "resolved", "latencyMs": 17, "tags": []any{"urgent"}, "details": map[string]any{"context": map[string]any{"region": "us"}}, "secret": "not-generic"})
	require.NoError(t, err)
	record := &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: "projection-entry"}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{Id: "projection-entry", ConversationId: "projection-conversation", ConversationGroupId: "projection-group", ContentType: "support-ticket/v1", CreatedAt: timestamppb.Now(), Content: content}},
	}
	require.NoError(t, processor.Handle(ctx, processruntime.EventEnvelope{Event: "created", Kind: "entry", Cursor: "integration-projection:1", Time: time.Now().UTC(), Data: testFullResourceData(t, record)}))
	require.NoError(t, processor.Flush(ctx))
	table := processor.projections.Items[0].TableName
	require.Equal(t, "support_ticket_v2", table)
	var relationCount uint64
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name IN (?, ?, ?)", sink.database, table, table+"_current", table+"_all").Scan(&relationCount))
	require.Equal(t, uint64(3), relationCount)
	var outcome, variantType, variantValue, region, genericPayload string
	var latency uint64
	var tags []string
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT outcome, latency_ms, tags, variantType(result), variantElement(result, 'String'), toString(details.context.region) FROM memory_service."+quoteIdentifier(table+"_current")+" WHERE exporter_id=?", cfg.ExporterID).Scan(&outcome, &latency, &tags, &variantType, &variantValue, &region))
	require.Equal(t, "resolved", outcome)
	require.Equal(t, uint64(17), latency)
	require.Equal(t, []string{"urgent"}, tags)
	require.Equal(t, "String", variantType)
	require.Equal(t, "resolved", variantValue)
	require.Equal(t, "us", region)
	var resultDDL, detailsDDL string
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT type FROM system.columns WHERE database=? AND table=? AND name='result'", sink.database, table).Scan(&resultDDL))
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT type FROM system.columns WHERE database=? AND table=? AND name='details'", sink.database, table).Scan(&detailsDDL))
	require.Equal(t, "Variant(Bool, Int64, String)", resultDDL)
	require.Contains(t, detailsDDL, "JSON(")
	require.Contains(t, detailsDDL, "max_dynamic_paths=128")
	require.Contains(t, detailsDDL, "max_dynamic_types=8")
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT payload_json FROM memory_service.entries_current WHERE exporter_id=?", cfg.ExporterID).Scan(&genericPayload))
	require.NotContains(t, genericPayload, "not-generic")
	var lifecycleContentType string
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT content_type FROM memory_service.lifecycle_events_current WHERE exporter_id=? AND source_cursor=?", cfg.ExporterID, "integration-projection:1").Scan(&lifecycleContentType))
	require.Equal(t, "support-ticket/v1", lifecycleContentType)
	require.NoError(t, processor.Handle(ctx, processruntime.EventEnvelope{Event: "deleted", Kind: "entry", Cursor: "integration-projection:2", Time: time.Now().UTC(), Data: []byte(`{"entry":"projection-entry","entry_content_type":"support-ticket/v1","change":"hard_deleted"}`)}))
	require.NoError(t, processor.Flush(ctx))
	var count uint64
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM memory_service."+quoteIdentifier(table+"_current")+" WHERE exporter_id=?", cfg.ExporterID).Scan(&count))
	require.Zero(t, count)
}

func TestClickHouseRejectsUnregisteredProjectionRelation(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, projection := writeIntegrationProjection(t, ctx, "existing_relation_v1", "existing/relation/v1")
	database, admin := createIntegrationDatabase(t, ctx)
	require.NoError(t, admin.Exec(ctx, "CREATE TABLE "+quoteIdentifier(database)+"."+quoteIdentifier(projection.TableName)+" (value String) ENGINE=MergeTree ORDER BY tuple()"))

	_, err := OpenSink(ctx, integrationDatabaseConfig(database, "existing-relation", dir, manifest))
	require.ErrorContains(t, err, "already exists without an identical registry entry")
}

func TestClickHouseCurrentViewsDoNotResurrectAfterTombstonePartRemoval(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("memory-service-analytics"), 0o600))
	manifest := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: tombstone_fence_v1}
spec:
  resource: entry
  selector: {contentType: tombstone-fence/v1}
  columns:
    outcome: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"outcome": input.content.outcome}
`
	manifestPath := filepath.Join(dir, "projection.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0o600))
	cfg := Config{ExporterID: "integration-tombstone-fence-" + strconv.FormatInt(time.Now().UnixNano(), 10), Addresses: []string{integrationNativeAddress()}, Username: "memory_service_analytics", PasswordFile: filepath.Join(dir, "password"), AllowInsecureClickHouse: true, PayloadMode: PayloadProjected, ProjectionPaths: []string{manifestPath}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	table := sink.projections[0].TableName
	resourceID := strings.Repeat("r", 64)
	now := time.Now().UTC()
	batch := func(suffix string, version uint64, deleted bool) Batch {
		batchID := strings.Repeat(suffix, 64)
		common := Common{ExporterID: cfg.ExporterID, BatchID: batchID, EventID: strings.Repeat(suffix, 64), SourceCursor: suffix, IngestVersion: version, ObservedAt: now.Add(time.Duration(version) * time.Second), SchemaVersion: schemaVersion}
		return Batch{ID: batchID, ExporterID: cfg.ExporterID, FirstCursor: suffix, LastCursor: suffix, Version: version, ObservedAt: common.ObservedAt,
			Resources:   []ResourceRow{{Common: common, ResourceID: resourceID, ResourceType: "entry", CreatedAt: now, UpdatedAt: common.ObservedAt, IsDeleted: deleted, PayloadJSON: `{}`}},
			Projections: []ProjectionRow{{Common: common, ProjectionName: "tombstone_fence_v1", TableName: table, ResourceID: resourceID, IsDeleted: deleted, ColumnNames: []string{"outcome"}, Values: []any{"resolved"}}},
		}
	}
	require.NoError(t, sink.WriteBatch(ctx, batch("a", 1, false)))
	require.NoError(t, sink.WriteBatch(ctx, batch("b", 2, true)))
	assertCurrentViewCounts(t, ctx, sink, table, cfg.ExporterID, 0)

	mutationCtx := ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	for _, target := range []string{"resources", "entries", table} {
		require.NoError(t, sink.conn.Exec(mutationCtx, "ALTER TABLE "+quoteIdentifier(sink.database)+"."+quoteIdentifier(target)+" DELETE WHERE exporter_id=? AND resource_id=? AND is_deleted=1", cfg.ExporterID, resourceID))
	}
	assertCurrentViewCounts(t, ctx, sink, table, cfg.ExporterID, 0)

	require.NoError(t, sink.WriteBatch(ctx, batch("c", 3, false)))
	assertCurrentViewCounts(t, ctx, sink, table, cfg.ExporterID, 1)
}

func TestClickHousePurgeRetryUsesPersistedSubjects(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, projection := writeIntegrationProjection(t, ctx, "purge_retry_v1", "purge-retry/v1")
	database, _ := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "purge-retry", dir, manifest)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	purgeID, entryID := strings.Repeat("q", 64), strings.Repeat("r", 64)
	batchID := strings.Repeat("a", 64)
	now := time.Now().UTC()
	require.NoError(t, sink.conn.Exec(ctx, "INSERT INTO "+quoteIdentifier(database)+"."+quoteIdentifier(projection.TableName)+" (exporter_id,batch_id,event_id,resource_id,conversation_id,conversation_group_id,ingest_version,observed_at,is_deleted,schema_version,outcome) VALUES (?,?,?,?,?,?,?,?,?,?,?)", cfg.ExporterID, batchID, batchID, entryID, "", "", uint64(1), now, uint8(0), uint16(schemaVersion), "resolved"))
	require.NoError(t, sink.conn.Exec(ctx, "INSERT INTO "+quoteIdentifier(database)+".purge_subjects VALUES (?,?,?,?,?)", cfg.ExporterID, purgeID, "entry", entryID, uint64(1)))

	purgeBatch := strings.Repeat("p", 64)
	require.NoError(t, sink.WriteBatch(ctx, Batch{ID: purgeBatch, ExporterID: cfg.ExporterID, Version: 2, Purges: []PurgeRow{{ExporterID: cfg.ExporterID, BatchID: purgeBatch, PurgeID: purgeID, EventID: purgeID, ResourceKind: "conversation", AnalyticsResourceID: strings.Repeat("c", 64), ConversationID: strings.Repeat("c", 64), ConversationGroupID: strings.Repeat("g", 64), RequestedAt: now}}}))
	var count uint64
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+"."+quoteIdentifier(projection.TableName)+" WHERE exporter_id=?", cfg.ExporterID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".purge_subjects WHERE exporter_id=? AND purge_id=?", cfg.ExporterID, purgeID).Scan(&count))
	require.Zero(t, count)
}

func TestClickHouseHistoricalProjectionRetention(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, historicalManifest, historical := writeIntegrationProjection(t, ctx, "maintenance_a_v1", "maintenance-a/v1")
	database, _ := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "maintenance", dir, historicalManifest)
	cfg.ProjectionRetention = time.Hour
	cfg.GenericPayloadRetention = time.Hour
	cfg.TombstoneRetention = 2 * time.Hour
	first, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	_, current, _ := writeIntegrationProjectionInDir(t, ctx, dir, "maintenance_b_v1", "maintenance-b/v1")
	cfg.ProjectionPaths = []string{current}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	var ddl string
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT create_table_query FROM system.tables WHERE database=? AND name=?", database, historical.TableName).Scan(&ddl))
	require.Contains(t, ddl, "TTL")
}

func TestClickHouseRejectsConflictingSharedRetentionPolicy(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, _ := writeIntegrationProjection(t, ctx, "retention_policy_v1", "retention-policy/v1")
	database, admin := createIntegrationDatabase(t, ctx)
	firstConfig := integrationDatabaseConfig(database, "retention-a", dir, manifest)
	firstConfig.LifecycleRetention = time.Hour
	first, err := OpenSink(ctx, firstConfig)
	require.NoError(t, err)

	conflicting := integrationDatabaseConfig(database, "retention-b", dir, manifest)
	conflicting.LifecycleRetention = 2 * time.Hour
	_, err = OpenSink(ctx, conflicting)
	require.ErrorContains(t, err, "retention policy conflicts")

	// Simulate a process crash: its owner row remains active after its last
	// liveness deadline, and a new owner must be able to reclaim the policy.
	first.stopRetentionHeartbeat()
	require.NoError(t, admin.Exec(ctx, "INSERT INTO "+quoteIdentifier(database)+".retention_policy VALUES (?,?,?,?,?,?,?,?,?,?)", firstConfig.ExporterID, first.retentionOwner, int64(3600), int64(0), int64(0), int64(0), int64(0), uint8(1), uint64(time.Now().UTC().UnixNano()), time.Now().UTC().Add(-retentionOwnerTTL-time.Second)))
	require.NoError(t, first.conn.Close())
	first.conn = nil

	updated := integrationDatabaseConfig(database, "retention-a", dir, manifest)
	updated.LifecycleRetention = 2 * time.Hour
	second, err := OpenSink(ctx, updated)
	require.NoError(t, err)
	_, err = OpenSink(ctx, integrationDatabaseConfig(database, "retention-c", dir, manifest))
	require.ErrorContains(t, err, "retention policy conflicts")
	require.NoError(t, second.Close())

	matching := integrationDatabaseConfig(database, "retention-c", dir, manifest)
	matching.LifecycleRetention = 2 * time.Hour
	third, err := OpenSink(ctx, matching)
	require.NoError(t, err)
	require.NoError(t, third.Close())
	var ddl string
	require.NoError(t, admin.QueryRow(ctx, "SELECT create_table_query FROM system.tables WHERE database=? AND name='lifecycle_events'", database).Scan(&ddl))
	require.Contains(t, ddl, "toIntervalSecond(7200)")
}

func TestClickHouseExternalRetentionAndRecordOnlyPurge(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := Config{
		ExporterID: "external-lifecycle", Database: database, Addresses: []string{integrationNativeAddress()},
		Username: "clickhouse", Password: "clickhouse", AllowInsecureClickHouse: true,
		RetentionMode: RetentionExternal, PurgeMode: PurgeRecordOnly,
	}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	var count uint64
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".retention_policy WHERE exporter_id=?", cfg.ExporterID).Scan(&count))
	require.Zero(t, count)

	batchID := strings.Repeat("b", 64)
	purgeID := strings.Repeat("p", 64)
	require.NoError(t, sink.WriteBatch(ctx, Batch{
		ID: batchID, ExporterID: cfg.ExporterID, Version: 1, ObservedAt: time.Now().UTC(),
		Purges: []PurgeRow{{ExporterID: cfg.ExporterID, BatchID: batchID, PurgeID: purgeID, EventID: purgeID, ResourceKind: "memory", AnalyticsResourceID: "memory-1", RequestedAt: time.Now().UTC()}},
	}))
	var purgeStatus string
	require.NoError(t, admin.QueryRow(ctx, "SELECT argMax(status, status_version) FROM "+quoteIdentifier(database)+".purge_queue WHERE exporter_id=? AND purge_id=?", cfg.ExporterID, purgeID).Scan(&purgeStatus))
	require.Equal(t, "pending", purgeStatus)
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".purge_subjects WHERE exporter_id=?", cfg.ExporterID).Scan(&count))
	require.Zero(t, count)
}

func TestClickHouseDisabledProjectionIsValidatedButNotCreated(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, projection := writeIntegrationProjection(t, ctx, "disabled_projection_v1", "disabled-projection/v1")
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "disabled-projection", dir, manifest)
	cfg.Disable = []string{"*:projections"}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	var count uint64
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name=?", database, projection.TableName).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".projection_registry WHERE exporter_id=?", cfg.ExporterID).Scan(&count))
	require.Zero(t, count)
}

func TestClickHouseValidateModeRegistersRetentionOwnerAndCommits(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, _ := writeIntegrationProjection(t, ctx, "validate_write_v1", "validate-write/v1")
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "validate-write", dir, manifest)
	cfg.LifecycleRetention = time.Hour

	provisioner, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, provisioner.Close())
	ddlBefore := readIntegrationSchemaDDL(t, ctx, admin, database)

	validateConfig := cfg
	validateConfig.SchemaMode = "validate"
	sink, err := OpenSink(ctx, validateConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	require.Equal(t, ddlBefore, readIntegrationSchemaDDL(t, ctx, admin, database), "validate mode must not execute schema or TTL DDL")

	batchID := strings.Repeat("v", 64)
	resourceID := strings.Repeat("r", 64)
	now := time.Now().UTC()
	common := Common{ExporterID: cfg.ExporterID, BatchID: batchID, EventID: batchID, SourceCursor: "validate:1", IngestVersion: 1, ObservedAt: now, SchemaVersion: schemaVersion}
	require.NoError(t, sink.WriteBatch(ctx, Batch{ID: batchID, ExporterID: cfg.ExporterID, FirstCursor: common.SourceCursor, LastCursor: common.SourceCursor, Version: 1, ObservedAt: now, Resources: []ResourceRow{{Common: common, ResourceID: resourceID, ResourceType: "entry", CreatedAt: now, UpdatedAt: now, PayloadJSON: `{}`}}}))
	var visible uint64
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".resources_current WHERE exporter_id=? AND resource_id=?", cfg.ExporterID, resourceID).Scan(&visible))
	require.Equal(t, uint64(1), visible)
}

func TestClickHouseValidateModeRejectsRetentionTTLMismatchWithoutDDL(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	tests := []struct {
		name        string
		provision   func(*Config)
		configure   func(*Config)
		targetTable func(*Projection) string
	}{
		{
			name:      "missing lifecycle",
			provision: func(cfg *Config) { cfg.LifecycleRetention = 0 },
			configure: func(cfg *Config) { cfg.LifecycleRetention = time.Hour },
			targetTable: func(*Projection) string {
				return "lifecycle_events"
			},
		},
		{
			name:      "resource two clause",
			configure: func(cfg *Config) { cfg.GenericPayloadRetention = time.Hour },
			targetTable: func(*Projection) string {
				return "resources"
			},
		},
		{
			name:      "projection failure",
			configure: func(cfg *Config) { cfg.ProjectionFailureRetention = time.Hour },
			targetTable: func(*Projection) string {
				return "projection_failures"
			},
		},
		{
			name:      "historical projection",
			configure: func(cfg *Config) { cfg.ProjectionRetention = time.Hour },
			targetTable: func(historical *Projection) string {
				return historical.TableName
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			dir, historicalManifest, historical := writeIntegrationProjection(t, ctx, "validate_retention_a_v1", "validate-retention-a/v1")
			database, admin := createIntegrationDatabase(t, ctx)
			cfg := integrationDatabaseConfig(database, "validate-retention", dir, historicalManifest)
			cfg.LifecycleRetention = 2 * time.Hour
			cfg.TombstoneRetention = 2 * time.Hour
			cfg.GenericPayloadRetention = 2 * time.Hour
			cfg.ProjectionRetention = 2 * time.Hour
			cfg.ProjectionFailureRetention = 2 * time.Hour
			if tt.provision != nil {
				tt.provision(&cfg)
			}
			first, err := OpenSink(ctx, cfg)
			require.NoError(t, err)
			require.NoError(t, first.Close())

			_, currentManifest, _ := writeIntegrationProjectionInDir(t, ctx, dir, "validate_retention_b_v1", "validate-retention-b/v1")
			cfg.ProjectionPaths = []string{currentManifest}
			second, err := OpenSink(ctx, cfg)
			require.NoError(t, err)
			require.NoError(t, second.Close())
			ddlBefore := readIntegrationSchemaDDL(t, ctx, admin, database)

			validateConfig := cfg
			validateConfig.SchemaMode = "validate"
			tt.configure(&validateConfig)
			_, err = OpenSink(ctx, validateConfig)
			require.ErrorContains(t, err, "TTL mismatch")
			require.ErrorContains(t, err, tt.targetTable(historical))
			require.Equal(t, ddlBefore, readIntegrationSchemaDDL(t, ctx, admin, database), "validate mode must not modify DDL after a retention mismatch")
		})
	}
}

func TestClickHouseRetentionConflictFencesRecoveredWriter(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, _ := writeIntegrationProjection(t, ctx, "retention_fence_v1", "retention-fence/v1")
	database, admin := createIntegrationDatabase(t, ctx)
	firstConfig := integrationDatabaseConfig(database, "retention-fence-a", dir, manifest)
	firstConfig.LifecycleRetention = time.Hour
	first, err := OpenSink(ctx, firstConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })

	first.stopRetentionHeartbeat()
	require.NoError(t, admin.Exec(ctx, "INSERT INTO "+quoteIdentifier(database)+".retention_policy VALUES (?,?,?,?,?,?,?,?,?,?)", firstConfig.ExporterID, first.retentionOwner, int64(3600), int64(0), int64(0), int64(0), int64(0), uint8(1), uint64(time.Now().UTC().UnixNano()), time.Now().UTC().Add(-retentionOwnerTTL-time.Second)))
	secondConfig := integrationDatabaseConfig(database, "retention-fence-b", dir, manifest)
	secondConfig.LifecycleRetention = 2 * time.Hour
	second, err := OpenSink(ctx, secondConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	batchID := strings.Repeat("f", 64)
	now := time.Now().UTC()
	common := Common{ExporterID: firstConfig.ExporterID, BatchID: batchID, EventID: batchID, SourceCursor: "retention:fenced", IngestVersion: 1, ObservedAt: now, SchemaVersion: schemaVersion}
	err = first.WriteBatch(ctx, Batch{ID: batchID, ExporterID: firstConfig.ExporterID, FirstCursor: common.SourceCursor, LastCursor: common.SourceCursor, Version: 1, ObservedAt: now, Resources: []ResourceRow{{Common: common, ResourceID: strings.Repeat("r", 64), ResourceType: "entry", CreatedAt: now, UpdatedAt: now, PayloadJSON: `{}`}}})
	require.ErrorContains(t, err, "retention policy ownership lost")
	select {
	case backgroundErr := <-first.BackgroundErrors():
		require.ErrorContains(t, backgroundErr, "retention policy ownership lost")
	case <-time.After(time.Second):
		t.Fatal("retention ownership loss was not reported to the processor runtime")
	}
	var physical, visible uint64
	require.NoError(t, first.conn.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".resources WHERE exporter_id=? AND batch_id=?", firstConfig.ExporterID, batchID).Scan(&physical))
	require.Zero(t, physical, "retention ownership must be renewed before any row is written")
	require.NoError(t, first.conn.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".resources_current WHERE exporter_id=? AND batch_id=?", firstConfig.ExporterID, batchID).Scan(&visible))
	require.Zero(t, visible)
}

func TestClickHouseRetentionRenewalFailureIsTerminal(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, manifest, _ := writeIntegrationProjection(t, ctx, "retention_renewal_failure_v1", "retention-renewal-failure/v1")
	database, _ := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "retention-renewal-failure", dir, manifest)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	sink.stopRetentionHeartbeat()
	require.NoError(t, sink.conn.Close())

	err = sink.renewRetentionOwnership(ctx)
	require.ErrorContains(t, err, "retention policy ownership lost")
	select {
	case backgroundErr := <-sink.BackgroundErrors():
		require.ErrorContains(t, backgroundErr, "verify live retention owner")
	case <-time.After(time.Second):
		t.Fatal("retention renewal failure was not reported to the processor runtime")
	}
}

func TestClickHouseHardDeletePurgesHistoricalProjectionTables(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, historicalManifest, historical := writeIntegrationProjection(t, ctx, "purge_historical_a_v1", "purge-historical-a/v1")
	database, _ := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "purge-historical", dir, historicalManifest)
	first, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	now := time.Now().UTC()
	conversationID, groupID, entryID := strings.Repeat("c", 64), strings.Repeat("g", 64), strings.Repeat("e", 64)
	batchID := strings.Repeat("a", 64)
	common := Common{ExporterID: cfg.ExporterID, BatchID: batchID, EventID: batchID, SourceCursor: "a", IngestVersion: 1, ObservedAt: now, SchemaVersion: schemaVersion}
	require.NoError(t, first.WriteBatch(ctx, Batch{ID: batchID, ExporterID: cfg.ExporterID, Version: 1, Resources: []ResourceRow{{Common: common, ResourceID: entryID, ConversationID: conversationID, ConversationGroupID: groupID, ResourceType: "entry", CreatedAt: now, UpdatedAt: now}}, Projections: []ProjectionRow{{Common: common, ProjectionName: historical.Name, TableName: historical.TableName, ResourceID: entryID, ConversationID: conversationID, ConversationGroupID: groupID, ColumnNames: []string{"outcome"}, Values: []any{"resolved"}}}}))
	require.NoError(t, first.Close())

	_, currentManifest, _ := writeIntegrationProjectionInDir(t, ctx, dir, "purge_historical_b_v1", "purge-historical-b/v1")
	cfg.ProjectionPaths = []string{currentManifest}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	purgeBatch, purgeID := strings.Repeat("p", 64), strings.Repeat("q", 64)
	require.NoError(t, sink.WriteBatch(ctx, Batch{ID: purgeBatch, ExporterID: cfg.ExporterID, Version: 2, Purges: []PurgeRow{{ExporterID: cfg.ExporterID, BatchID: purgeBatch, PurgeID: purgeID, EventID: purgeID, ResourceKind: "conversation", AnalyticsResourceID: conversationID, ConversationID: conversationID, ConversationGroupID: groupID, RequestedAt: now}}}))
	var count uint64
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+"."+quoteIdentifier(historical.TableName)+" WHERE exporter_id=?", cfg.ExporterID).Scan(&count))
	require.Zero(t, count)
	var status string
	require.NoError(t, sink.conn.QueryRow(ctx, "SELECT argMax(status, status_version) FROM "+quoteIdentifier(database)+".purge_queue WHERE exporter_id=? AND purge_id=?", cfg.ExporterID, purgeID).Scan(&status))
	require.Equal(t, "complete", status)
}

func TestClickHouseHardDeletePurgesDependentAnalyticsData(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("memory-service-analytics"), 0o600))
	manifestPath := filepath.Join(dir, "projection.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(`apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: purge_entry_v1}
spec:
  resource: entry
  selector: {contentType: purge-entry/v1}
  columns:
    outcome: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"outcome": input.content.outcome}
`), 0o600))
	exporterID := "integration-dependent-purge-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cfg := Config{ExporterID: exporterID, Addresses: []string{integrationNativeAddress()}, Username: "memory_service_analytics", PasswordFile: filepath.Join(dir, "password"), AllowInsecureClickHouse: true, PayloadMode: PayloadFull, AllowDecryptedContent: true, ProjectionPaths: []string{manifestPath}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	projectionTable := sink.projections[0].TableName
	now := time.Now().UTC()
	conversationID := strings.Repeat("c", 64)
	groupID := strings.Repeat("g", 64)
	entryID := strings.Repeat("e", 64)
	lineageID := strings.Repeat("l", 64)
	memoryID := strings.Repeat("m", 64)
	logicalMemoryID := strings.Repeat("u", 64)

	entryDeleteBatch := strings.Repeat("d", 64)
	entryDeleteCommon := Common{ExporterID: exporterID, BatchID: entryDeleteBatch, EventID: entryDeleteBatch, SourceCursor: "delete-old-entry", IngestVersion: 1, ObservedAt: now, SchemaVersion: schemaVersion}
	require.NoError(t, sink.WriteBatch(ctx, Batch{ID: entryDeleteBatch, ExporterID: exporterID, Version: 1, ObservedAt: now,
		Resources: []ResourceRow{{Common: entryDeleteCommon, ResourceID: entryID, ConversationID: conversationID, ConversationGroupID: groupID, ResourceType: "entry", CreatedAt: now, UpdatedAt: now, IsDeleted: true}},
	}))

	activeBatch := strings.Repeat("a", 64)
	activeCommon := Common{ExporterID: exporterID, BatchID: activeBatch, EventID: activeBatch, SourceCursor: "active", IngestVersion: 2, ObservedAt: now.Add(time.Second), SchemaVersion: schemaVersion}
	resources := []ResourceRow{
		{Common: activeCommon, ResourceID: conversationID, ConversationID: conversationID, ConversationGroupID: groupID, ResourceType: "conversation", CreatedAt: now, UpdatedAt: now, PayloadJSON: `{"title":"private"}`},
		{Common: activeCommon, ResourceID: entryID, ConversationID: conversationID, ConversationGroupID: groupID, ResourceType: "entry", CreatedAt: now, UpdatedAt: now, PayloadJSON: `{"content":"private entry"}`},
		{Common: activeCommon, ResourceID: lineageID, ConversationID: conversationID, ConversationGroupID: groupID, ResourceType: "lineage", CreatedAt: now, UpdatedAt: now, PayloadJSON: `{}`},
		{Common: activeCommon, ResourceID: memoryID, ResourceType: "memory", CreatedAt: now, UpdatedAt: now, PayloadJSON: `{"logicalMemoryId":"` + logicalMemoryID + `","value":"private memory"}`},
	}
	lifecycle := make([]LifecycleRow, 0, len(resources))
	for _, row := range resources {
		lifecycle = append(lifecycle, LifecycleRow{Common: activeCommon, OccurredAt: now, ResourceKind: row.ResourceType, AnalyticsResourceID: row.ResourceID, Action: "created", Change: "created", ConversationID: row.ConversationID, ConversationGroupID: row.ConversationGroupID})
	}
	require.NoError(t, sink.WriteBatch(ctx, Batch{ID: activeBatch, ExporterID: exporterID, Version: 2, ObservedAt: activeCommon.ObservedAt,
		Resources: resources, Lifecycle: lifecycle,
		Projections:        []ProjectionRow{{Common: activeCommon, ProjectionName: "purge_entry_v1", TableName: projectionTable, ResourceID: entryID, ConversationID: conversationID, ConversationGroupID: groupID, ColumnNames: []string{"outcome"}, Values: []any{"private outcome"}}},
		ProjectionFailures: []ProjectionFailureRow{{ExporterID: exporterID, BatchID: activeBatch, EventID: activeBatch, AnalyticsResourceID: entryID, ConversationID: conversationID, ConversationGroupID: groupID, ProjectionName: "purge_entry_v1", ErrorCode: "test", AttemptCount: 1, FirstSeenAt: now, LastSeenAt: now, Version: 2}},
	}))

	purgeBatch := strings.Repeat("p", 64)
	require.NoError(t, sink.WriteBatch(ctx, Batch{ID: purgeBatch, ExporterID: exporterID, Version: 3, ObservedAt: now.Add(2 * time.Second), Purges: []PurgeRow{
		{ExporterID: exporterID, BatchID: purgeBatch, PurgeID: strings.Repeat("q", 64), EventID: strings.Repeat("q", 64), ResourceKind: "conversation", AnalyticsResourceID: conversationID, ConversationID: conversationID, ConversationGroupID: groupID, RequestedAt: now},
		{ExporterID: exporterID, BatchID: purgeBatch, PurgeID: strings.Repeat("r", 64), EventID: strings.Repeat("r", 64), ResourceKind: "memory", AnalyticsResourceID: memoryID, RequestedAt: now},
	}}))

	for _, table := range []string{"resources", "conversations", "entries", "memories", "conversation_lineage", "lifecycle_events", "projection_failures", "deletion_fences", projectionTable} {
		var count uint64
		require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(sink.database)+"."+quoteIdentifier(table)+" WHERE exporter_id=?", exporterID).Scan(&count))
		require.Zero(t, count, table)
	}
}

func createIntegrationDatabase(t *testing.T, ctx context.Context) (string, driver.Conn) {
	t.Helper()
	database := "clickhouse_v1_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	admin, err := ch.Open(&ch.Options{Addr: []string{integrationNativeAddress()}, Auth: ch.Auth{Database: "default", Username: "clickhouse", Password: "clickhouse"}})
	require.NoError(t, err)
	require.NoError(t, admin.Ping(ctx))
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, admin.Exec(ctx, "CREATE DATABASE "+quoteIdentifier(database)))
	t.Cleanup(func() { _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quoteIdentifier(database)) })
	return database, admin
}

func readIntegrationSchemaDDL(t *testing.T, ctx context.Context, conn driver.Conn, database string) map[string]string {
	t.Helper()
	rows, err := conn.Query(ctx, "SELECT name, create_table_query FROM system.tables WHERE database=? ORDER BY name", database)
	require.NoError(t, err)
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, ddl string
		require.NoError(t, rows.Scan(&name, &ddl))
		result[name] = ddl
	}
	require.NoError(t, rows.Err())
	return result
}

func integrationDatabaseConfig(database, exporterID, dir, manifestPath string) Config {
	return Config{ExporterID: exporterID, Database: database, Addresses: []string{integrationNativeAddress()}, Username: "clickhouse", PasswordFile: filepath.Join(dir, "password"), AllowInsecureClickHouse: true, PayloadMode: PayloadProjected, ProjectionPaths: []string{manifestPath}}
}

func writeIntegrationProjection(t *testing.T, ctx context.Context, name, selector string) (string, string, *Projection) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	_, path, projection := writeIntegrationProjectionInDir(t, ctx, dir, name, selector)
	return dir, path, projection
}

func writeIntegrationProjectionInDir(t *testing.T, ctx context.Context, dir, name, selector string) (string, string, *Projection) {
	t.Helper()
	path := filepath.Join(dir, "projection-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".yaml")
	manifest := fmt.Sprintf(`apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: %s}
spec:
  resource: entry
  selector: {contentType: %s}
  columns:
    outcome: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"outcome": input.content.outcome}
`, name, selector)
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
	set, err := LoadProjectionSet(ctx, []string{path})
	require.NoError(t, err)
	require.Len(t, set.Items, 1)
	return dir, path, set.Items[0]
}

func assertCurrentViewCounts(t *testing.T, ctx context.Context, sink *ClickHouseSink, projectionTable, exporterID string, want uint64) {
	t.Helper()
	for _, table := range []string{"resources_current", "entries_current", projectionTable + "_current"} {
		var count uint64
		require.NoError(t, sink.conn.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(sink.database)+"."+quoteIdentifier(table)+" WHERE exporter_id=?", exporterID).Scan(&count))
		require.Equal(t, want, count, table)
	}
}

func TestClickHouseHTTPSinkIntegration(t *testing.T) {
	if os.Getenv("MEMORY_SERVICE_TEST_CLICKHOUSE") != "true" {
		t.Skip("set MEMORY_SERVICE_TEST_CLICKHOUSE=true")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("memory-service-analytics"), 0o600))
	cfg := Config{ExporterID: "integration-http", Addresses: []string{integrationHTTPAddress()}, Protocol: "http", Username: "memory_service_analytics", PasswordFile: filepath.Join(dir, "password"), AllowInsecureClickHouse: true, SchemaMode: "validate"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, sink.Close())
}
