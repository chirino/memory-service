package clickhouse

import (
	"context"
	"os"
	"path/filepath"
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

const integrationChainRoot = `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_chain_v1}
spec:
  resource: entry
  selector: {contentType: support-ticket/v1}
  columns:
    step: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"step": input.content.step}
`

const integrationChainLater = `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_chain_v2}
spec:
  resource: entry
  previous: support_ticket_chain_v1
  projectionRegoFrom: support_ticket_chain_v1
  selector: {contentType: support-ticket/v2}
---
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_chain_duration}
spec:
  resource: entry
  previous: support_ticket_chain_v2
  projectionRegoReplaces: support_ticket_chain_v1
  addColumns:
    duration_ms: {type: uint64, nullable: true}
  projectionRego: |
    package memoryservice.analytics
    output := {"step": input.content.step, "duration_ms": object.get(input.content, "durationMs", null)}
`

func writeChainManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func chainEntryRecord(t *testing.T, id, contentType string, content map[string]any) *pb.AnalyticsRecord {
	t.Helper()
	value, err := structpb.NewValue(content)
	require.NoError(t, err)
	return &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: id}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{Id: id, ConversationId: "chain-conversation", ConversationGroupId: "chain-group", ContentType: contentType, CreatedAt: timestamppb.Now(), Content: value}},
	}
}

func handleChainEntry(t *testing.T, ctx context.Context, processor *Processor, cursor string, record *pb.AnalyticsRecord) {
	t.Helper()
	require.NoError(t, processor.Handle(ctx, processruntime.EventEnvelope{Event: "created", Kind: "entry", Cursor: cursor, Time: time.Now().UTC(), Data: testFullResourceData(t, record)}))
}

type chainRow struct {
	ResourceID string
	Step       string
	DurationMS *uint64
}

func readChainRows(t *testing.T, ctx context.Context, conn driver.Conn, database, view, exporterID string) []chainRow {
	t.Helper()
	rows, err := conn.Query(ctx, "SELECT resource_id, step, duration_ms FROM "+quoteIdentifier(database)+"."+quoteIdentifier(view)+" WHERE exporter_id=? ORDER BY resource_id, step", exporterID)
	require.NoError(t, err)
	defer rows.Close()
	var result []chainRow
	for rows.Next() {
		var row chainRow
		require.NoError(t, rows.Scan(&row.ResourceID, &row.Step, &row.DurationMS))
		result = append(result, row)
	}
	require.NoError(t, rows.Err())
	return result
}

func readTableColumns(t *testing.T, ctx context.Context, conn driver.Conn, database, table string) []string {
	t.Helper()
	rows, err := conn.Query(ctx, "SELECT name || ' ' || type FROM system.columns WHERE database=? AND table=? ORDER BY position", database, table)
	require.NoError(t, err)
	defer rows.Close()
	var result []string
	for rows.Next() {
		var column string
		require.NoError(t, rows.Scan(&column))
		result = append(result, column)
	}
	require.NoError(t, rows.Err())
	return result
}

func readRegistryChain(t *testing.T, ctx context.Context, conn driver.Conn, database, exporterID string) [][3]string {
	t.Helper()
	rows, err := conn.Query(ctx, "SELECT projection_name, previous, table_name FROM "+quoteIdentifier(database)+".projection_registry FINAL WHERE exporter_id=? ORDER BY projection_name", exporterID)
	require.NoError(t, err)
	defer rows.Close()
	var result [][3]string
	for rows.Next() {
		var row [3]string
		require.NoError(t, rows.Scan(&row[0], &row[1], &row[2]))
		result = append(result, row)
	}
	require.NoError(t, rows.Err())
	return result
}

func TestClickHouseProjectionChainExtendsExistingTable(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	rootPath := writeChainManifest(t, dir, "a-root.yaml", integrationChainRoot)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "projection-chain", dir, rootPath)
	cfg.ProjectionRetention = time.Hour
	cfg.GenericPayloadRetention = time.Hour
	cfg.TombstoneRetention = 2 * time.Hour

	// A projection created before its chain existed.
	first, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	processor, err := NewProcessor(cfg, first)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(1))
	handleChainEntry(t, ctx, processor, "chain:1", chainEntryRecord(t, "e1", "support-ticket/v1", map[string]any{"step": "plan", "durationMs": 3}))
	require.NoError(t, processor.Flush(ctx))
	require.NoError(t, first.Close())

	// Later manifests update it: the table keeps its name and rows and gains a column.
	chainPath := writeChainManifest(t, dir, "b-chain.yaml", integrationChainLater)
	cfg.ProjectionPaths = []string{rootPath, chainPath}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	columns := readTableColumns(t, ctx, admin, database, "support_ticket_chain_v1")
	require.Equal(t, "step Nullable(String)", columns[len(columns)-2])
	require.Equal(t, "duration_ms Nullable(UInt64)", columns[len(columns)-1])
	require.Equal(t, [][3]string{
		{"support_ticket_chain_duration", "support_ticket_chain_v2", "support_ticket_chain_v1"},
		{"support_ticket_chain_v1", "", "support_ticket_chain_v1"},
		{"support_ticket_chain_v2", "support_ticket_chain_v1", "support_ticket_chain_v1"},
	}, readRegistryChain(t, ctx, admin, database, cfg.ExporterID))
	require.Equal(t, []chainRow{{"e1", "plan", nil}}, readChainRows(t, ctx, admin, database, "support_ticket_chain_v1_current", cfg.ExporterID), "existing rows stay NULL until a replay")

	processor, err = NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(2))
	handleChainEntry(t, ctx, processor, "chain:2", chainEntryRecord(t, "e2", "support-ticket/v2", map[string]any{"step": "act", "durationMs": 5}))
	handleChainEntry(t, ctx, processor, "chain:3", chainEntryRecord(t, "e3", "support-ticket/v1", map[string]any{"step": "check"}))
	require.NoError(t, processor.Flush(ctx))
	require.Equal(t, []chainRow{{"e1", "plan", nil}, {"e2", "act", new(uint64(5))}, {"e3", "check", nil}}, readChainRows(t, ctx, admin, database, "support_ticket_chain_v1_current", cfg.ExporterID))

	// A replay rewrites existing rows with the replacing logic through the table key.
	processor, err = NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(3))
	require.NoError(t, processor.writeBackfillPage(ctx, "entry", []*pb.AnalyticsRecord{chainEntryRecord(t, "e1", "support-ticket/v1", map[string]any{"step": "plan", "durationMs": 3})}))
	require.Equal(t, []chainRow{{"e1", "plan", new(uint64(3))}, {"e2", "act", new(uint64(5))}, {"e3", "check", nil}}, readChainRows(t, ctx, admin, database, "support_ticket_chain_v1_current", cfg.ExporterID))
	var failures uint64
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".projection_failures WHERE exporter_id=?", cfg.ExporterID).Scan(&failures))
	require.Zero(t, failures)

	// Validate mode accepts the registered chain, including retention on the shared table.
	validateCfg := cfg
	validateCfg.SchemaMode = "validate"
	validated, err := OpenSink(ctx, validateCfg)
	require.NoError(t, err)
	require.NoError(t, validated.Close())

	// Registered manifests can be neither removed nor edited.
	cfg.ProjectionPaths = []string{rootPath}
	_, err = OpenSink(ctx, cfg)
	require.ErrorContains(t, err, "not in its configured chain")
	editedDir := t.TempDir()
	editedPath := writeChainManifest(t, editedDir, "b-chain.yaml", integrationChainLater+"    # edited\n")
	cfg.ProjectionPaths = []string{rootPath, editedPath}
	_, err = OpenSink(ctx, cfg)
	require.ErrorContains(t, err, `projection "support_ticket_chain_duration" already exists with a different immutable digest`)

	// A hard delete purges the rows that any manifest of the chain wrote to the shared table.
	require.NoError(t, processor.Handle(ctx, processruntime.EventEnvelope{Event: "deleted", Kind: "entry", Cursor: "chain:4", Time: time.Now().UTC(), Data: []byte(`{"entry":"e2","entry_content_type":"support-ticket/v2","change":"hard_deleted"}`)}))
	require.NoError(t, processor.Flush(ctx))
	require.Equal(t, []chainRow{{"e1", "plan", new(uint64(3))}, {"e3", "check", nil}}, readChainRows(t, ctx, admin, database, "support_ticket_chain_v1_current", cfg.ExporterID))
	var stored uint64
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM "+quoteIdentifier(database)+".support_ticket_chain_v1 WHERE resource_id='e2'").Scan(&stored))
	require.Zero(t, stored)
}

func TestClickHouseProjectionChainValidateModeListsMissingColumns(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	rootPath := writeChainManifest(t, dir, "a-root.yaml", integrationChainRoot)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "projection-chain-validate", dir, rootPath)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, sink.Close())
	before := readTableColumns(t, ctx, admin, database, "support_ticket_chain_v1")

	cfg.ProjectionPaths = []string{rootPath, writeChainManifest(t, dir, "b-chain.yaml", integrationChainLater)}
	cfg.SchemaMode = "validate"
	_, err = OpenSink(ctx, cfg)
	require.ErrorContains(t, err, "is missing columns duration_ms")
	require.Equal(t, before, readTableColumns(t, ctx, admin, database, "support_ticket_chain_v1"), "validate mode must not change the table")
	require.Len(t, readRegistryChain(t, ctx, admin, database, cfg.ExporterID), 1, "validate mode must not register manifests")
}

func TestClickHouseProjectionChainFreshInstallWithVersionNeutralTable(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	path := writeChainManifest(t, dir, "chain.yaml", `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: steps_v1}
spec:
  resource: entry
  table: steps
  selector: {contentType: steps/v1}
  rows: many
  columns:
    step: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := [{"step": s} | some s in input.content.steps]
---
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: steps_v2}
spec:
  resource: entry
  previous: steps_v1
  projectionRegoFrom: steps_v1
  selector: {contentType: steps/v2}
---
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: steps_duration}
spec:
  resource: entry
  previous: steps_v2
  projectionRegoReplaces: steps_v1
  addColumns:
    duration_ms: {type: uint64, nullable: true}
  projectionRego: |
    package memoryservice.analytics
    output := [{"step": s, "duration_ms": input.content.durationMs} | some s in input.content.steps]
`)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "projection-chain-fresh", dir, path)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	// A fresh install applies the whole chain in order: the same column order as an upgrade.
	columns := readTableColumns(t, ctx, admin, database, "steps")
	require.Equal(t, []string{"step Nullable(String)", "duration_ms Nullable(UInt64)"}, columns[len(columns)-2:])
	var sortingKey string
	require.NoError(t, admin.QueryRow(ctx, "SELECT sorting_key FROM system.tables WHERE database=? AND name='steps'", database).Scan(&sortingKey))
	require.Equal(t, multiRowSortingKey, sortingKey)
	var relations uint64
	require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database=? AND name IN ('steps_all', 'steps_current', 'steps_v1')", database).Scan(&relations))
	require.Equal(t, uint64(2), relations, "manifest names do not create tables")
	require.Equal(t, [][3]string{{"steps_duration", "steps_v2", "steps"}, {"steps_v1", "", "steps"}, {"steps_v2", "steps_v1", "steps"}}, readRegistryChain(t, ctx, admin, database, cfg.ExporterID))

	// A projection replay fills the new table from existing entries of every content type.
	processor, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(1))
	require.NoError(t, processor.writeBackfillPage(ctx, "entry", []*pb.AnalyticsRecord{
		chainEntryRecord(t, "e1", "steps/v1", map[string]any{"steps": []any{"plan", "act"}, "durationMs": 2}),
		chainEntryRecord(t, "e2", "steps/v2", map[string]any{"steps": []any{"check"}, "durationMs": 4}),
		chainEntryRecord(t, "e3", "steps/v3", map[string]any{"steps": []any{"ignored"}, "durationMs": 6}),
	}))
	require.Equal(t, []chainRow{{"e1", "act", new(uint64(2))}, {"e1", "plan", new(uint64(2))}, {"e2", "check", new(uint64(4))}}, readChainRows(t, ctx, admin, database, "steps_current", cfg.ExporterID))
}

// Databases created before projection chains lack the registry previous column.
func TestClickHouseProjectionRegistryGainsPreviousColumn(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	rootPath := writeChainManifest(t, dir, "a-root.yaml", integrationChainRoot)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "projection-registry-upgrade", dir, rootPath)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, sink.Close())
	require.NoError(t, admin.Exec(ctx, "ALTER TABLE "+quoteIdentifier(database)+".projection_registry DROP COLUMN previous"))

	validateCfg := cfg
	validateCfg.SchemaMode = "validate"
	_, err = OpenSink(ctx, validateCfg)
	require.ErrorContains(t, err, "projection_registry is missing column previous")

	cfg.ProjectionPaths = []string{rootPath, writeChainManifest(t, dir, "b-chain.yaml", integrationChainLater)}
	sink, err = OpenSink(ctx, cfg)
	require.NoError(t, err, "manage mode adds the column without changing the schema checksum")
	require.NoError(t, sink.Close())
	require.Equal(t, [][3]string{
		{"support_ticket_chain_duration", "support_ticket_chain_v2", "support_ticket_chain_v1"},
		{"support_ticket_chain_v1", "", "support_ticket_chain_v1"},
		{"support_ticket_chain_v2", "support_ticket_chain_v1", "support_ticket_chain_v1"},
	}, readRegistryChain(t, ctx, admin, database, cfg.ExporterID))
}

func TestClickHouseProjectionChainChecksAddedColumnTypes(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	rootPath := writeChainManifest(t, dir, "a-root.yaml", integrationChainRoot)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "projection-chain-types", dir, rootPath)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, sink.Close())

	// An earlier interrupted start added the column for a manifest that was changed before
	// it was registered.
	require.NoError(t, admin.Exec(ctx, "ALTER TABLE "+quoteIdentifier(database)+".support_ticket_chain_v1 ADD COLUMN duration_ms Nullable(String)"))
	cfg.ProjectionPaths = []string{rootPath, writeChainManifest(t, dir, "b-chain.yaml", integrationChainLater)}
	_, err = OpenSink(ctx, cfg)
	require.ErrorContains(t, err, "column duration_ms has type Nullable(String), but support_ticket_chain_duration declares Nullable(UInt64)")
	require.Len(t, readRegistryChain(t, ctx, admin, database, cfg.ExporterID), 1)

	// Provider-specific added column types round-trip through system.columns.
	require.NoError(t, admin.Exec(ctx, "ALTER TABLE "+quoteIdentifier(database)+".support_ticket_chain_v1 DROP COLUMN duration_ms"))
	cfg.ProjectionPaths = []string{rootPath, writeChainManifest(t, dir, "b-chain.yaml", `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_chain_details}
spec:
  resource: entry
  previous: support_ticket_chain_v1
  addColumns:
    details: {type: json, nullable: true, clickhouse: {maxDynamicPaths: 64, maxDynamicTypes: 4}}
    implicit_details: {type: json, nullable: true}
    server_default_details: {type: json, nullable: true, clickhouse: {maxDynamicPaths: 1024, maxDynamicTypes: 32}}
    default_paths_details: {type: json, nullable: true, clickhouse: {maxDynamicPaths: 1024, maxDynamicTypes: 8}}
    default_types_details: {type: json, nullable: true, clickhouse: {maxDynamicPaths: 64, maxDynamicTypes: 32}}
    result: {type: variant, variants: [string, int64, bool], nullable: true}
    measure: {type: variant, variants: [float64, string], nullable: true}
    tags: {type: string_array, nullable: true}
    seen_at: {type: timestamp, nullable: true}
    flag: {type: bool, nullable: true}
    count: {type: int64, nullable: true}
    ratio: {type: float64, nullable: true}
    raw: {type: json_string, nullable: true}
`)}
	// ClickHouse omits JSON parameters equal to its own defaults from system.columns types.
	for range 2 {
		sink, err = OpenSink(ctx, cfg)
		require.NoError(t, err, "reopening must accept the columns it added")
		require.NoError(t, sink.Close())
	}
	validateCfg := cfg
	validateCfg.SchemaMode = "validate"
	sink, err = OpenSink(ctx, validateCfg)
	require.NoError(t, err, "validate mode must accept the columns manage mode added")
	require.NoError(t, sink.Close())
}

func TestClickHouseProjectionRegistrationsCanBeWithdrawn(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	path := writeChainManifest(t, dir, "chain.yaml", integrationChainRoot+"---\n"+integrationChainLater)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "projection-chain-withdraw", dir, path)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	other := cfg
	other.ExporterID = "projection-chain-withdraw-other"
	otherSink, err := OpenSink(ctx, other)
	require.NoError(t, err)
	require.NoError(t, otherSink.Close())

	require.NoError(t, sink.withdrawProjectionRegistrations(ctx, []string{"support_ticket_chain_v2", "support_ticket_chain_duration"}))
	require.Equal(t, [][3]string{{"support_ticket_chain_v1", "", "support_ticket_chain_v1"}}, readRegistryChain(t, ctx, admin, database, cfg.ExporterID))
	require.Len(t, readRegistryChain(t, ctx, admin, database, other.ExporterID), 3, "other exporters keep their registrations")
}

func chainMemoryRecord(t *testing.T, id, kind string, attributes map[string]any) *pb.AnalyticsRecord {
	t.Helper()
	values, err := structpb.NewStruct(attributes)
	require.NoError(t, err)
	return &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_MEMORY, Id: id}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Memory{Memory: &pb.AnalyticsMemoryRecord{Id: id, Namespace: []string{"user", "chain"}, Key: id, MemoryKind: kind, Revision: 1, CreatedAt: timestamppb.Now(), Attributes: values}},
	}
}

func readMemoryChainRows(t *testing.T, ctx context.Context, conn driver.Conn, database, exporterID string) [][3]*string {
	t.Helper()
	rows, err := conn.Query(ctx, "SELECT resource_id, subject, topic FROM "+quoteIdentifier(database)+".notes_current WHERE exporter_id=? ORDER BY resource_id", exporterID)
	require.NoError(t, err)
	defer rows.Close()
	var result [][3]*string
	for rows.Next() {
		var id string
		var subject, topic *string
		require.NoError(t, rows.Scan(&id, &subject, &topic))
		result = append(result, [3]*string{&id, subject, topic})
	}
	require.NoError(t, rows.Err())
	return result
}

func TestClickHouseMemoryProjectionChainSplitsAKindWithReplay(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	chainPath := writeChainManifest(t, dir, "a-chain.yaml", testMemoryChain)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "memory-chain", dir, chainPath)
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	processor, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(1))
	first := chainMemoryRecord(t, "m1", "notes/v1", map[string]any{"subject": "billing"})
	second := chainMemoryRecord(t, "m2", "notes/v2", map[string]any{"subject": "shipping", "topic": "delays"})
	for i, record := range []*pb.AnalyticsRecord{first, second} {
		require.NoError(t, processor.Handle(ctx, processruntime.EventEnvelope{Event: "created", Kind: "memory", Cursor: "memory-chain:" + string(rune('1'+i)), Time: time.Now().UTC(), Data: testFullResourceData(t, record)}))
	}
	require.NoError(t, processor.Flush(ctx))
	require.NoError(t, sink.Close())
	text := func(value string) *string { return &value }
	require.Equal(t, [][3]*string{{text("m1"), text("billing"), nil}, {text("m2"), text("shipping"), text("delays")}}, readMemoryChainRows(t, ctx, admin, database, cfg.ExporterID))

	// Split notes/v1 off with its own logic, then replay: only notes/v1 rows change.
	cfg.ProjectionPaths = []string{chainPath, writeChainManifest(t, dir, "b-split.yaml", testMemoryChainSplit)}
	sink, err = OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	processor, err = NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, processor.SetLeaseGeneration(2))
	require.NoError(t, processor.writeBackfillPage(ctx, "memory", []*pb.AnalyticsRecord{first, second}))
	require.Equal(t, [][3]*string{{text("m1"), text("BILLING"), nil}, {text("m2"), text("shipping"), text("delays")}}, readMemoryChainRows(t, ctx, admin, database, cfg.ExporterID))
}

func TestClickHouseProjectionChainRegistryMustBeAPrefixForEveryExporter(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	rootPath := writeChainManifest(t, dir, "a-root.yaml", integrationChainRoot)
	chainPath := writeChainManifest(t, dir, "b-chain.yaml", integrationChainLater)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "registry-prefix", dir, rootPath)
	cfg.ProjectionPaths = []string{rootPath, chainPath}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, sink.Close())

	// Another exporter configured with a shorter chain would drop registered manifests.
	other := cfg
	other.ExporterID = "registry-prefix-other"
	other.ProjectionPaths = []string{rootPath}
	_, err = OpenSink(ctx, other)
	require.ErrorContains(t, err, "support_ticket_chain_duration, support_ticket_chain_v2 that are not in its configured chain")

	// Reordering the chain changes the reordered manifests' digests.
	reorderedPath := writeChainManifest(t, t.TempDir(), "b-chain.yaml", `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_chain_duration}
spec:
  resource: entry
  previous: support_ticket_chain_v1
  projectionRegoReplaces: support_ticket_chain_v1
  addColumns:
    duration_ms: {type: uint64, nullable: true}
  projectionRego: |
    package memoryservice.analytics
    output := {"step": input.content.step, "duration_ms": object.get(input.content, "durationMs", null)}
---
apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_chain_v2}
spec:
  resource: entry
  previous: support_ticket_chain_duration
  projectionRegoFrom: support_ticket_chain_duration
  selector: {contentType: support-ticket/v2}
`)
	other.ProjectionPaths = []string{rootPath, reorderedPath}
	_, err = OpenSink(ctx, other)
	require.ErrorContains(t, err, "already exists with a different immutable digest")

	// A registry that skips a manifest is not the beginning of the chain.
	mutation := ch.Context(ctx, ch.WithSettings(ch.Settings{"mutations_sync": 2}))
	require.NoError(t, admin.Exec(mutation, "ALTER TABLE "+quoteIdentifier(database)+".projection_registry DELETE WHERE projection_name='support_ticket_chain_v2'"))
	_, err = OpenSink(ctx, cfg)
	require.ErrorContains(t, err, `projection "support_ticket_chain_duration" is registered for table "support_ticket_chain_v1" but its previous manifest "support_ticket_chain_v2" is not`)
}

func TestClickHouseProjectionChainWithdrawsLosingConcurrentRegistration(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "password"), []byte("clickhouse"), 0o600))
	rootPath := writeChainManifest(t, dir, "a-root.yaml", integrationChainRoot)
	ours := writeChainManifest(t, dir, "b-ours.yaml", `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_chain_v2}
spec:
  resource: entry
  previous: support_ticket_chain_v1
  projectionRegoFrom: support_ticket_chain_v1
  selector: {contentType: support-ticket/v2}
`)
	theirsDir := t.TempDir()
	theirs := writeChainManifest(t, theirsDir, "b-theirs.yaml", `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: support_ticket_chain_alt}
spec:
  resource: entry
  previous: support_ticket_chain_v1
  projectionRegoFrom: support_ticket_chain_v1
  selector: {contentType: support-ticket/alt}
`)
	theirSet, err := LoadProjectionSet(ctx, []string{rootPath, theirs})
	require.NoError(t, err)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := integrationDatabaseConfig(database, "registry-race", dir, rootPath)
	cfg.ProjectionPaths = []string{rootPath, ours}
	other := cfg
	other.ExporterID = "registry-race-other"
	other.ProjectionPaths = []string{rootPath, theirs}

	// Another exporter registers a divergent extension between our registration and re-check.
	afterProjectionRegistration = func(ctx context.Context, table string) {
		for _, manifest := range theirSet.Tables[0].Manifests {
			require.NoError(t, admin.Exec(ctx, "INSERT INTO "+quoteIdentifier(database)+".projection_registry (exporter_id, projection_name, projection_digest, selector, table_name, state, updated_at, version, previous) VALUES (?, ?, ?, ?, ?, 'active', now64(9), 1, ?)", other.ExporterID, manifest.Name, manifest.Digest, manifest.Resource+":"+manifest.Selector, table, manifest.Previous))
		}
	}
	t.Cleanup(func() { afterProjectionRegistration = nil })
	_, err = OpenSink(ctx, cfg)
	afterProjectionRegistration = nil
	require.ErrorContains(t, err, "support_ticket_chain_alt that are not in its configured chain")
	require.Empty(t, readRegistryChain(t, ctx, admin, database, cfg.ExporterID), "the losing exporter withdraws what it registered")

	// The winning configuration is not blocked by the losing one.
	sink, err := OpenSink(ctx, other)
	require.NoError(t, err)
	require.NoError(t, sink.Close())
	require.Equal(t, [][3]string{{"support_ticket_chain_alt", "support_ticket_chain_v1", "support_ticket_chain_v1"}, {"support_ticket_chain_v1", "", "support_ticket_chain_v1"}}, readRegistryChain(t, ctx, admin, database, other.ExporterID))
}
