package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func currentSink(t *testing.T, configure func(*Config)) (context.Context, *ClickHouseSink, Config, driver.Conn) {
	t.Helper()
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	database, admin := createIntegrationDatabase(t, ctx)
	cfg := testConfig()
	cfg.Database = database
	cfg.Addresses = []string{integrationNativeAddress()}
	cfg.Username = "clickhouse"
	cfg.Password = "clickhouse"
	cfg.AllowInsecureClickHouse = true
	if configure != nil {
		configure(&cfg)
	}
	sink, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	return ctx, sink, cfg, admin
}
func countRows(t *testing.T, ctx context.Context, conn driver.Conn, database, table, where string, args ...any) uint64 {
	t.Helper()
	query := "SELECT count() FROM " + quoteIdentifier(database) + "." + quoteIdentifier(table)
	if where != "" {
		query += " WHERE " + where
	}
	var count uint64
	require.NoError(t, conn.QueryRow(ctx, query, args...).Scan(&count))
	return count
}
func TestClickHouseCurrentBaselineAndObservationOrdering(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	p := testProcessor(t, cfg, sink, 1)
	for _, cursor := range []string{"1", "2", "3"} {
		event := conversationEvent(cursor)
		event.Data = json.RawMessage(strings.Replace(string(event.Data), `{`, `{"archived":`+fmt.Sprint(cursor == "3")+`,`, 1))
		require.NoError(t, p.Handle(ctx, event))
	}
	require.NoError(t, p.Flush(ctx))
	var archived bool
	for range 2 {
		require.NoError(t, admin.QueryRow(ctx, "SELECT archived FROM "+quoteIdentifier(cfg.Database)+".conversations_current WHERE resource_id='conversation-1'").Scan(&archived))
		require.True(t, archived)
		require.NoError(t, admin.Exec(ctx, "OPTIMIZE TABLE "+quoteIdentifier(cfg.Database)+".conversations FINAL"))
	}
	ddl := readIntegrationSchemaDDL(t, ctx, admin, cfg.Database)
	for _, name := range []string{"resources", "purge_subjects", "deletion_fence_writer", "conversation_lineage"} {
		require.NotContains(t, ddl, name)
	}
	for _, statement := range ddl {
		require.NotContains(t, statement, "schema_version UInt16")
		require.NotContains(t, statement, "payload_json")
	}
	require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "conversation_memberships", ""))
	validate := cfg
	validate.SchemaMode = "validate"
	opened, err := OpenSink(ctx, validate)
	require.NoError(t, err)
	require.NoError(t, opened.Close())
}
func TestClickHouseMembershipRevokeRegrantAndPersistedCutoff(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, func(c *Config) { c.ExportMemberships = true; c.PurgeMode = PurgeRecordOnly })
	p := testProcessor(t, cfg, sink, 1)
	require.NoError(t, p.Handle(ctx, conversationEvent("conversation")))
	id := model.MembershipResourceID(uuid.MustParse(testGroup), "user/é")
	grant := processruntime.EventEnvelope{Kind: "membership", Event: "created", Cursor: "1", Data: json.RawMessage(`{"id":"` + id + `","conversationGroupId":"` + testGroup + `","userId":"user/é","accessLevel":"reader","createdAt":"2026-01-01T00:00:00Z"}`)}
	revoke := processruntime.EventEnvelope{Kind: "membership", Event: "deleted", Cursor: "2", Data: json.RawMessage(`{"conversation_group":"` + testGroup + `","user":"user/é"}`)}
	require.NoError(t, p.Handle(ctx, grant))
	require.NoError(t, p.Handle(ctx, revoke))
	grant.Cursor = "3"
	require.NoError(t, p.Handle(ctx, grant))
	frozen := *p.pending
	require.NoError(t, p.Flush(ctx))
	require.Equal(t, uint64(1), countRows(t, ctx, admin, cfg.Database, "conversation_memberships_current", "resource_id=?", id))
	var accessible uint64
	query := "SELECT count() FROM " + quoteIdentifier(cfg.Database) + ".conversations_current c LEFT SEMI JOIN (SELECT exporter_id,conversation_group_id FROM " + quoteIdentifier(cfg.Database) + ".conversation_memberships_current WHERE user_id=?) m ON c.exporter_id=m.exporter_id AND c.conversation_group_id=m.conversation_group_id WHERE c.exporter_id=?"
	require.NoError(t, admin.QueryRow(ctx, query, "user/é", cfg.ExporterID).Scan(&accessible))
	require.Equal(t, uint64(1), accessible)
	prior, err := sink.ResolvePurge(ctx, PurgeRow{ExporterID: cfg.ExporterID, PurgeID: frozen.Purges[0].PurgeID, ResourceKind: "membership", AnalyticsResourceID: id, ConversationGroupID: testGroup, Scope: purgeResource, CutoffIngestVersion: uint64(2) << 32})
	require.NoError(t, err)
	require.Equal(t, frozen.Purges[0].CutoffIngestVersion, prior.CutoffIngestVersion)
	require.NoError(t, sink.processPurges(ctx, Batch{Purges: []PurgeRow{prior}}))
	require.Equal(t, uint64(1), countRows(t, ctx, admin, cfg.Database, "conversation_memberships", "resource_id=?", id))
	// Replaying the old revoke in another lease must not fence the newer grant.
	restarted := testProcessor(t, cfg, sink, 2)
	require.NoError(t, restarted.Handle(ctx, revoke))
	require.NoError(t, restarted.Flush(ctx))
	require.Equal(t, uint64(1), countRows(t, ctx, admin, cfg.Database, "conversation_memberships_current", "resource_id=?", id))
	var status string
	require.NoError(t, admin.QueryRow(ctx, "SELECT status FROM "+quoteIdentifier(cfg.Database)+".purge_queue FINAL WHERE purge_id=?", prior.PurgeID).Scan(&status))
	require.Equal(t, "complete", status, "replayed pending writes cannot replace complete status")
	prior.AnalyticsResourceID = "different"
	_, err = sink.ResolvePurge(ctx, prior)
	require.ErrorContains(t, err, "conflicting immutable scope")
}

func TestClickHousePurgeReplayRemovesNewLeaseObservations(t *testing.T) {
	for _, kind := range []string{"conversation", "memory"} {
		t.Run(kind, func(t *testing.T) {
			ctx, sink, cfg, admin := currentSink(t, nil)
			deleted := processruntime.EventEnvelope{Kind: kind, Event: "deleted", Cursor: "delete", Change: "hard_deleted"}
			missing := processruntime.EventEnvelope{Kind: "entry", Event: "created", Cursor: "create", Data: json.RawMessage(`{"entry":"deleted-entry","conversation":"conversation-1","conversation_group":"` + testGroup + `"}`)}
			if kind == "conversation" {
				deleted.Data = json.RawMessage(`{"conversation":"conversation-1","conversation_group":"` + testGroup + `"}`)
			} else {
				missing.Kind = "memory"
				missing.Data = json.RawMessage(`{"memory":"deleted-memory","memoryKind":"default/v1"}`)
				deleted.Data = missing.Data
			}
			old := testProcessor(t, cfg, sink, 1)
			require.NoError(t, old.Handle(ctx, missing))
			require.NoError(t, old.Handle(ctx, deleted))
			// The process died after persisting a pending purge but before saving its checkpoint.
			require.NoError(t, sink.writePurge(ctx, old.pending.Purges[0], false))
			replayed := testProcessor(t, cfg, sink, 2)
			require.NoError(t, replayed.Handle(ctx, missing))
			require.NoError(t, replayed.Handle(ctx, deleted))
			require.Greater(t, replayed.pending.ProjectionFailures[0].IngestVersion, replayed.pending.Purges[0].CutoffIngestVersion)
			require.NoError(t, replayed.Flush(ctx))
			for _, table := range []string{"lifecycle_events", "projection_failures"} {
				require.Zero(t, countRows(t, ctx, admin, cfg.Database, table, "exporter_id=?", cfg.ExporterID), table)
			}
		})
	}
}
func TestClickHouseDirectGroupPurgeAndLateWriteFence(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	p := testProcessor(t, cfg, sink, 1)
	require.NoError(t, p.Handle(ctx, conversationEvent("1")))
	old := *p.pending
	require.NoError(t, p.Flush(ctx))
	// A second exporter in the same physical tables must survive the first exporter's purge.
	other := old
	other.ID = strings.Repeat("o", 64)
	other.ExporterID = "other"
	other.Lifecycle = nil
	other.Projections = append([]ProjectionRow{}, old.Projections...)
	for i := range other.Projections {
		other.Projections[i].ExporterID = "other"
		other.Projections[i].BatchID = other.ID
	}
	require.NoError(t, sink.WriteBatch(ctx, other))
	require.NoError(t, p.Handle(ctx, processruntime.EventEnvelope{Kind: "conversation", Event: "deleted", Cursor: "2", Data: json.RawMessage(`{"conversation":"conversation-1","conversation_group":"` + testGroup + `"}`)}))
	require.NoError(t, p.Flush(ctx))
	require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "conversations", "exporter_id=?", cfg.ExporterID))
	require.Equal(t, uint64(1), countRows(t, ctx, admin, cfg.Database, "conversations_current", "exporter_id='other'"))
	// Inject an old INSERT after mutation completion. Fences exclude it, but do not erase its bytes.
	old.ID = strings.Repeat("l", 64)
	old.Lifecycle = nil
	require.NoError(t, sink.WriteBatch(ctx, old))
	require.Equal(t, uint64(1), countRows(t, ctx, admin, cfg.Database, "conversations", "exporter_id=?", cfg.ExporterID))
	require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "conversations_current", "exporter_id=?", cfg.ExporterID))
	require.Equal(t, uint64(2), countRows(t, ctx, admin, cfg.Database, "deletion_fences", "exporter_id=?", cfg.ExporterID))
	// Cleanup remains available after the projection configuration is removed.
	sink.projectionTables = nil
	sink.projections = nil
	var purge PurgeRow
	require.NoError(t, admin.QueryRow(ctx, "SELECT exporter_id,purge_id,event_id,resource_kind,analytics_resource_id,conversation_id,conversation_group_id,purge_scope,cutoff_ingest_version,requested_at FROM "+quoteIdentifier(cfg.Database)+".purge_queue FINAL WHERE exporter_id=?", cfg.ExporterID).Scan(&purge.ExporterID, &purge.PurgeID, &purge.EventID, &purge.ResourceKind, &purge.AnalyticsResourceID, &purge.ConversationID, &purge.ConversationGroupID, &purge.Scope, &purge.CutoffIngestVersion, &purge.RequestedAt))
	require.NoError(t, sink.processPurges(ctx, Batch{Purges: []PurgeRow{purge}}))
	require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "conversations", "exporter_id=?", cfg.ExporterID))
}
func TestClickHousePurgeResourceKindsAndFrozenInsertDedup(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	batch := Batch{ID: strings.Repeat("b", 64), ExporterID: cfg.ExporterID}
	now := time.Now().UTC()
	common := Common{ExporterID: cfg.ExporterID, BatchID: batch.ID, EventID: batch.ID, IngestVersion: 1, ObservedAt: now}
	for _, kind := range []string{"entry", "memory"} {
		batch.ProjectionFailures = append(batch.ProjectionFailures, ProjectionFailureRow{Common: common, ResourceKind: kind, AnalyticsResourceID: "same", ProjectionName: kind, FirstSeenAt: now, LastSeenAt: now, AttemptCount: 1})
	}
	require.NoError(t, admin.Exec(ctx, "SYSTEM STOP MERGES "+quoteIdentifier(cfg.Database)+".projection_failures"))
	defer admin.Exec(context.Background(), "SYSTEM START MERGES "+quoteIdentifier(cfg.Database)+".projection_failures")
	for range 2 {
		require.NoError(t, sink.WriteBatch(ctx, batch))
	}
	require.Equal(t, uint64(2), countRows(t, ctx, admin, cfg.Database, "projection_failures", ""))
	require.NoError(t, admin.Exec(ctx, "SYSTEM START MERGES "+quoteIdentifier(cfg.Database)+".projection_failures"))
	purge := PurgeRow{ExporterID: cfg.ExporterID, PurgeID: strings.Repeat("p", 64), EventID: strings.Repeat("p", 64), ResourceKind: "entry", AnalyticsResourceID: "same", Scope: purgeResource, CutoffIngestVersion: 1, RequestedAt: now}
	require.NoError(t, sink.WriteBatch(ctx, Batch{ID: strings.Repeat("d", 64), ExporterID: cfg.ExporterID, Purges: []PurgeRow{purge}}))
	require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "projection_failures", "resource_kind='entry'"))
	require.Equal(t, uint64(1), countRows(t, ctx, admin, cfg.Database, "projection_failures", "resource_kind='memory'"))
}
func TestClickHouseRejectsSchemaDriftBeforeDDL(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	require.NoError(t, sink.Close())
	require.NoError(t, admin.Exec(ctx, "ALTER TABLE "+quoteIdentifier(cfg.Database)+".purge_queue MODIFY COLUMN cutoff_ingest_version Nullable(UInt64)"))
	before := readIntegrationSchemaDDL(t, ctx, admin, cfg.Database)
	for _, mode := range []string{"manage", "validate"} {
		cfg.SchemaMode = mode
		_, err := OpenSink(ctx, cfg)
		require.ErrorContains(t, err, "incompatible type")
		require.Equal(t, before, readIntegrationSchemaDDL(t, ctx, admin, cfg.Database))
	}
}
func TestPreviousExporterRejectsCurrentBaseline(t *testing.T) {
	binary := os.Getenv("MEMORY_SERVICE_TEST_PREVIOUS_BINARY")
	if binary == "" {
		t.Skip("set MEMORY_SERVICE_TEST_PREVIOUS_BINARY to the pre-enhancement binary")
	}
	ctx, sink, cfg, admin := currentSink(t, nil)
	require.NoError(t, sink.Close())
	before := readIntegrationSchemaDDL(t, ctx, admin, cfg.Database)
	for _, mode := range []string{"manage", "validate"} {
		cmd := exec.CommandContext(ctx, binary, "process", "clickhouse", "--endpoint", "127.0.0.1:1", "--client-id", "previous", "--clickhouse-address", integrationNativeAddress(), "--clickhouse-database", cfg.Database, "--clickhouse-username", "clickhouse", "--allow-insecure-clickhouse", "--schema-mode", mode)
		cmd.Env = append(os.Environ(), "MEMORY_SERVICE_CLICKHOUSE_PASSWORD=clickhouse")
		output, err := cmd.CombinedOutput()
		require.Error(t, err, string(output))
		require.NotContains(t, string(output), "connection refused")
		require.Equal(t, before, readIntegrationSchemaDDL(t, ctx, admin, cfg.Database))
	}
}

// A matching partial infrastructure install may resume, but an unregistered user table may not.
func TestClickHousePartialInitialization(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	require.NoError(t, sink.Close())
	for _, name := range []string{"conversations_current", "conversations_all", "conversations", "entries_current", "entries_all", "entries", "memories_current", "memories_all", "memories", "conversation_memberships_current", "lifecycle_events_current", "conversation_memberships", "lifecycle_events", "projection_registry", "retention_policy", "projection_failures", "purge_queue", "deletion_fences"} {
		require.NoError(t, admin.Exec(ctx, "DROP TABLE "+quoteIdentifier(cfg.Database)+"."+quoteIdentifier(name)))
	}
	rebuilt, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, rebuilt.Close())
}

func TestClickHouseRejectsPreviousDatasetWithoutMutation(t *testing.T) {
	requireClickHouseIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database, admin := createIntegrationDatabase(t, ctx)
	prefix := quoteIdentifier(database) + "."
	// Preserve the previous exporter's registry and resource-table definitions as
	// an upgrade fixture, including a stored payload that must survive rejection.
	require.NoError(t, admin.Exec(ctx, `CREATE TABLE `+prefix+`schema_versions (
 component LowCardinality(String), schema_version UInt16, checksum FixedString(64), applied_at DateTime64(9, 'UTC')
 ) ENGINE=MergeTree ORDER BY (component, schema_version)`))
	require.NoError(t, admin.Exec(ctx, `CREATE TABLE `+prefix+`resources (
 exporter_id String, batch_id FixedString(64), event_id FixedString(64), source_cursor String,
 ingest_version UInt64, observed_at DateTime64(9, 'UTC'), schema_version UInt16,
 resource_id String, conversation_id String, conversation_group_id String,
 resource_type LowCardinality(String), created_at DateTime64(9, 'UTC'), updated_at DateTime64(9, 'UTC'),
 is_archived UInt8, is_deleted UInt8, payload_json String
 ) ENGINE=ReplacingMergeTree(ingest_version) ORDER BY (exporter_id, resource_type, resource_id)`))
	require.NoError(t, admin.Exec(ctx, "INSERT INTO "+prefix+"schema_versions VALUES (?,?,?,?)", "clickhouse-export", uint16(1), strings.Repeat("0", 64), time.Now().UTC()))
	require.NoError(t, admin.Exec(ctx, "INSERT INTO "+prefix+"resources (exporter_id,resource_id,schema_version,payload_json) VALUES (?,?,?,?)", "previous-exporter", "retained-resource", uint16(1), `{"retained":"history"}`))
	before := readIntegrationSchemaDDL(t, ctx, admin, database)
	cfg := testConfig()
	cfg.Database = database
	cfg.Addresses = []string{integrationNativeAddress()}
	cfg.Username, cfg.Password = "clickhouse", "clickhouse"
	cfg.AllowInsecureClickHouse = true
	for _, mode := range []string{"manage", "validate"} {
		t.Run(mode, func(t *testing.T) {
			cfg.SchemaMode = mode
			_, err := OpenSink(ctx, cfg)
			require.ErrorContains(t, err, "incompatible analytics schema registry")
			require.Equal(t, before, readIntegrationSchemaDDL(t, ctx, admin, database))
			require.Equal(t, uint64(1), countRows(t, ctx, admin, database, "schema_versions", "schema_version=1"))
			require.Equal(t, uint64(1), countRows(t, ctx, admin, database, "resources", "exporter_id=? AND resource_id=? AND payload_json=?", "previous-exporter", "retained-resource", `{"retained":"history"}`))
		})
	}
}

func TestClickHouseConversationLayoutKeepsLatestMultiRowSnapshot(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, func(c *Config) {
		dir := t.TempDir()
		c.ProjectionPaths = []string{writeChainManifest(t, dir, "layout.yaml", `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: layout_steps}
spec:
  resource: entry
  rows: many
  clickhouse: {ordering: conversation}
  columns:
    label: {type: string, nullable: false, clickhouse: {lowCardinality: true}}
    deletion_version: {type: int64, nullable: false}
    rn: {type: int64, nullable: false}
    version_rank: {type: int64, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := [{"label": label,"deletion_version":7,"rn":8,"version_rank":9} | some label in input.record.content]
`)}
		c.PayloadMode = PayloadProjected
	})
	p := testProcessor(t, cfg, sink, 1)
	contents := []string{`["old","other","surplus"]`, `["new"]`, `[]`, `["latest","last"]`}
	for i, content := range contents {
		event := processruntime.EventEnvelope{Event: "updated", Kind: "entry", Cursor: fmt.Sprint(i), Data: []byte(`{"id":"entry","conversationId":"conversation","conversationGroupId":"` + testGroup + `","channel":"history","contentType":"custom","createdAt":"2026-01-01T00:00:00Z","content":` + content + `}`)}
		require.NoError(t, p.Handle(ctx, event))
		require.NoError(t, p.Flush(ctx))
		if i == 2 {
			require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "layout_steps_current", ""))
		}
	}
	for range 2 {
		require.Equal(t, uint64(2), countRows(t, ctx, admin, cfg.Database, "layout_steps_current", "conversation_id='conversation' AND deletion_version=7 AND rn=8 AND version_rank=9"))
		require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "layout_steps_current", "label='old'"))
		require.NoError(t, admin.Exec(ctx, "OPTIMIZE TABLE "+quoteIdentifier(cfg.Database)+".layout_steps FINAL"))
	}
}

func TestClickHouseCreatesFreshDatabase(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	require.NoError(t, sink.Close())
	require.NoError(t, admin.Exec(ctx, "DROP DATABASE "+quoteIdentifier(cfg.Database)))
	fresh, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, fresh.Close())
}

func TestClickHouseResumesEmptyCurrentRegistry(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	require.NoError(t, sink.Close())
	require.NoError(t, admin.Exec(ctx, "DROP DATABASE "+quoteIdentifier(cfg.Database)))
	for _, statement := range schemaStatements(cfg.Database)[:2] {
		require.NoError(t, admin.Exec(ctx, statement))
	}
	before := readIntegrationSchemaDDL(t, ctx, admin, cfg.Database)
	cfg.SchemaMode = "validate"
	_, err := OpenSink(ctx, cfg)
	require.Error(t, err)
	require.Equal(t, before, readIntegrationSchemaDDL(t, ctx, admin, cfg.Database))
	require.Zero(t, countRows(t, ctx, admin, cfg.Database, "schema_versions", ""))
	cfg.SchemaMode = "manage"
	restarted, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, restarted.Close())
	require.Equal(t, uint64(1), countRows(t, ctx, admin, cfg.Database, "schema_versions", ""))
}

func TestClickHouseDisabledMembershipAvoidsEmptyMutations(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	id := model.MembershipResourceID(uuid.MustParse(testGroup), "previous-member")
	revoke := processruntime.EventEnvelope{Kind: "membership", Event: "deleted", Cursor: "revoke", Data: json.RawMessage(`{"conversation_group":"` + testGroup + `","user":"previous-member"}`)}
	mutations := func() uint64 {
		var count uint64
		require.NoError(t, admin.QueryRow(ctx, "SELECT count() FROM system.mutations WHERE database=?", cfg.Database).Scan(&count))
		return count
	}
	before := mutations()
	p := testProcessor(t, cfg, sink, 1)
	require.NoError(t, p.Handle(ctx, revoke))
	require.NoError(t, p.Flush(ctx))
	require.Equal(t, before, mutations(), "an exporter that has never exported memberships needs no ALTER DELETE")
	// Disabling the output must still clean up rows from earlier enabled runs.
	now := time.Now().UTC()
	batchID := strings.Repeat("h", 64)
	require.NoError(t, sink.WriteBatch(ctx, Batch{ID: batchID, ExporterID: cfg.ExporterID, Lifecycle: []LifecycleRow{{Common: Common{ExporterID: cfg.ExporterID, BatchID: batchID, EventID: batchID, IngestVersion: 1, ObservedAt: now}, OccurredAt: now, ResourceKind: "membership", AnalyticsResourceID: id, ConversationGroupID: testGroup}}}))
	p = testProcessor(t, cfg, sink, 2)
	revoke.Cursor = "revoke-again"
	require.NoError(t, p.Handle(ctx, revoke))
	require.NoError(t, p.Flush(ctx))
	require.Zero(t, countRows(t, ctx, admin, cfg.Database, "lifecycle_events", "resource_kind='membership'"))
	require.Greater(t, mutations(), before)
	// Without membership export, no revoke stores the removed user's identity in
	// a fence. Only the purge that removed earlier rows is queued.
	require.Zero(t, countRows(t, ctx, admin, cfg.Database, "deletion_fences", "resource_id=?", id))
	require.Equal(t, uint64(2), countRows(t, ctx, admin, cfg.Database, "purge_queue", "analytics_resource_id=?", id), "one pending and one complete row for the second revoke only")
	require.Equal(t, uint64(1), countRows(t, ctx, admin, cfg.Database, "purge_queue", "analytics_resource_id=? AND status='complete'", id))
}

func TestClickHouseMaxIngestVersionCoversStoredRows(t *testing.T) {
	ctx, sink, cfg, _ := currentSink(t, nil)
	version, err := sink.MaxIngestVersion(ctx, cfg.ExporterID)
	require.NoError(t, err)
	require.Zero(t, version)
	p := testProcessor(t, cfg, sink, 3)
	require.NoError(t, p.Handle(ctx, conversationEvent("1")))
	require.NoError(t, p.Handle(ctx, processruntime.EventEnvelope{Kind: "conversation", Event: "deleted", Cursor: "2", Data: json.RawMessage(`{"conversation":"conversation-1","conversation_group":"` + testGroup + `"}`)}))
	require.NoError(t, p.Flush(ctx))
	version, err = sink.MaxIngestVersion(ctx, cfg.ExporterID)
	require.NoError(t, err)
	require.Equal(t, uint64(3), version>>32)
	other, err := sink.MaxIngestVersion(ctx, "other-exporter")
	require.NoError(t, err)
	require.Zero(t, other)
	restarted := testProcessor(t, cfg, sink, 2)
	completeCheckpoint(t, restarted, "2")
	_, err = restarted.Bootstrap(ctx, nil, func(context.Context) error { return nil })
	require.ErrorContains(t, err, "lost or reset")
}

func TestClickHouseInsertDeduplicationSettings(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, nil)
	require.NoError(t, sink.Close())
	window := func(table string) string {
		return readIntegrationSchemaDDL(t, ctx, admin, cfg.Database)[table]
	}
	// An existing table with deduplication disabled is refused in validate mode
	// without changes, and repaired in manage mode.
	for _, table := range []string{"lifecycle_events", "conversations"} {
		t.Run(table, func(t *testing.T) {
			qualified := quoteIdentifier(cfg.Database) + "." + quoteIdentifier(table)
			require.NoError(t, admin.Exec(ctx, "ALTER TABLE "+qualified+" MODIFY SETTING non_replicated_deduplication_window=0"))
			before := readIntegrationSchemaDDL(t, ctx, admin, cfg.Database)
			validate := cfg
			validate.SchemaMode = "validate"
			_, err := OpenSink(ctx, validate)
			require.ErrorContains(t, err, "disabled insert deduplication")
			require.Equal(t, before, readIntegrationSchemaDDL(t, ctx, admin, cfg.Database))
			opened, err := OpenSink(ctx, cfg)
			require.NoError(t, err)
			require.NoError(t, opened.Close())
			require.Contains(t, window(table), "non_replicated_deduplication_window = 1000")
		})
	}
	// Manage mode keeps a window the operator tuned.
	require.NoError(t, admin.Exec(ctx, "ALTER TABLE "+quoteIdentifier(cfg.Database)+".lifecycle_events MODIFY SETTING non_replicated_deduplication_window=23"))
	opened, err := OpenSink(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, opened.Close())
	require.Contains(t, window("lifecycle_events"), "non_replicated_deduplication_window = 23")
}

func TestClickHouseExpiredRetentionOwnerCannotResumeWrites(t *testing.T) {
	ctx, first, cfg, admin := currentSink(t, func(c *Config) { c.LifecycleRetention = time.Hour })
	first.stopRetentionHeartbeat()
	require.NoError(t, admin.Exec(ctx, "INSERT INTO "+quoteIdentifier(cfg.Database)+".retention_policy VALUES (?,?,?,?,?,?,?,?,?)", cfg.ExporterID, first.retentionOwner, int64(3600), int64(0), int64(0), int64(0), uint8(1), uint64(time.Now().UnixNano()), time.Now().UTC().Add(-retentionOwnerTTL-time.Second)))
	nextConfig := cfg
	nextConfig.ExporterID = "next-owner"
	nextConfig.LifecycleRetention = 2 * time.Hour
	next, err := OpenSink(ctx, nextConfig)
	require.NoError(t, err)
	defer next.Close()
	now := time.Now().UTC()
	id := strings.Repeat("f", 64)
	batch := Batch{ID: id, ExporterID: cfg.ExporterID, Lifecycle: []LifecycleRow{{Common: Common{ExporterID: cfg.ExporterID, BatchID: id, EventID: id, IngestVersion: 1, ObservedAt: now}, ResourceKind: "memory", AnalyticsResourceID: "memory", OccurredAt: now, Action: "updated"}}}
	require.ErrorContains(t, first.WriteBatch(ctx, batch), "retention policy ownership lost")
	select {
	case err := <-first.BackgroundErrors():
		require.ErrorContains(t, err, "retention policy ownership lost")
	default:
		t.Fatal("retention ownership failure was not reported")
	}
	require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "lifecycle_events", "exporter_id=?", cfg.ExporterID))
}
