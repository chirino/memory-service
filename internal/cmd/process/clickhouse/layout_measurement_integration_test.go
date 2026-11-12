package clickhouse

import (
	"fmt"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"
)

// Log reproducible local measurements rather than imposing hardware-sensitive
// latency thresholds. Both layouts must retain the same canonical results.
func TestClickHouseLayoutMeasurements(t *testing.T) {
	ctx, sink, cfg, admin := currentSink(t, func(c *Config) {
		c.ProjectionPaths = []string{}
		dir := t.TempDir()
		for _, ordering := range []string{"resource", "conversation"} {
			c.ProjectionPaths = append(c.ProjectionPaths, writeChainManifest(t, dir, ordering+".yaml", fmt.Sprintf(`apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: layout_%s}
spec:
  resource: entry
  clickhouse: {ordering: %s}
  columns:
    status: {type: string, nullable: false, clickhouse: {lowCardinality: %t}}
  projectionRego: |
    package memoryservice.analytics
    output := {"status": input.record.status}
`, ordering, ordering, ordering == "conversation")))
		}
	})
	now := time.Now().UTC()
	started := time.Now()
	batch := Batch{ID: fmt.Sprintf("%064d", 1), ExporterID: cfg.ExporterID}
	const conversations = 64
	const entries = 512
	for i := range conversations {
		for j := range entries {
			id := fmt.Sprintf("entry-%08d", j*conversations+i)
			common := Common{ExporterID: cfg.ExporterID, BatchID: batch.ID, EventID: fmt.Sprintf("%064d", j*conversations+i), IngestVersion: 1, ObservedAt: now}
			for _, table := range sink.projectionTables {
				batch.Projections = append(batch.Projections, ProjectionRow{Common: common, TableName: table.Name, ResourceID: id, ConversationID: fmt.Sprintf("conversation-%03d", i), ConversationGroupID: testGroup, ColumnNames: []string{"status"}, Values: []any{"completed"}})
			}
		}
	}
	require.NoError(t, sink.WriteBatch(ctx, batch))
	ingest := time.Since(started)
	for _, ordering := range []string{"resource", "conversation"} {
		table := "layout_" + ordering
		require.NoError(t, admin.Exec(ctx, "OPTIMIZE TABLE "+quoteIdentifier(cfg.Database)+"."+quoteIdentifier(table)+" FINAL"))
		query := "SELECT count() FROM " + quoteIdentifier(cfg.Database) + "." + quoteIdentifier(table+"_current") + " WHERE exporter_id=? AND conversation_id=?"
		plan, err := admin.Query(ctx, "EXPLAIN indexes=1 "+query, cfg.ExporterID, "conversation-032")
		require.NoError(t, err)
		var lines []string
		for plan.Next() {
			var line string
			require.NoError(t, plan.Scan(&line))
			lines = append(lines, line)
		}
		require.NoError(t, plan.Err())
		require.NoError(t, plan.Close())
		require.NotEmpty(t, lines)
		queryID := cfg.Database + "_" + table
		queryCtx := ch.Context(ctx, ch.WithQueryID(queryID), ch.WithSettings(ch.Settings{"log_queries": 1}))
		var count uint64
		require.NoError(t, admin.QueryRow(queryCtx, query, cfg.ExporterID, "conversation-032").Scan(&count))
		require.Equal(t, uint64(entries), count)
		require.NoError(t, admin.Exec(ctx, "SYSTEM FLUSH LOGS"))
		var readRows, memory, duration, bytes uint64
		require.NoError(t, admin.QueryRow(ctx, "SELECT read_rows,memory_usage,query_duration_ms FROM system.query_log WHERE query_id=? AND type='QueryFinish' ORDER BY event_time_microseconds DESC LIMIT 1", queryID).Scan(&readRows, &memory, &duration))
		require.NoError(t, admin.QueryRow(ctx, "SELECT sum(bytes_on_disk) FROM system.parts WHERE database=? AND table=? AND active", cfg.Database, table).Scan(&bytes))
		t.Logf("%s: entries=%d read_rows=%d query_ms=%d memory_bytes=%d disk_bytes=%d shared_ingest_ms=%d", ordering, entries, readRows, duration, memory, bytes, ingest.Milliseconds())
	}
}
