package clickhouse

import (
	"context"
	"testing"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	"github.com/stretchr/testify/require"
)

func TestClickHouseTakeoverWithOutstandingInsert(t *testing.T) {
	for _, protocol := range []string{"native", "http"} {
		t.Run(protocol, func(t *testing.T) {
			ctx, sink, cfg, admin := currentSink(t, func(c *Config) {
				c.Protocol = protocol
				if protocol == "http" {
					c.Addresses = []string{integrationHTTPAddress()}
				}
			})
			// Slow server-side default evaluation, so this is a real unfinished INSERT,
			// not merely an acknowledgment lost after the row was already committed.
			require.NoError(t, admin.Exec(ctx, "ALTER TABLE "+quoteIdentifier(cfg.Database)+".conversations ADD COLUMN insert_delay UInt8 DEFAULT sleep(2)"))
			old := testProcessor(t, cfg, sink, 1)
			require.NoError(t, old.Handle(ctx, conversationEvent("create")))
			batch := *old.pending
			batch.Lifecycle = nil
			writeCtx, cancelWrite := context.WithCancel(ctx)
			defer cancelWrite()
			done := make(chan error, 1)
			go func() { done <- sink.WriteBatch(writeCtx, batch) }()
			// Find the slow INSERT by its statement and use the query_id the server assigned.
			// Don't put a fixed query_id on writeCtx: WriteBatch runs several statements,
			// and over HTTP ClickHouse can still have the previous one registered when the
			// next arrives, rejecting it with QUERY_WITH_SAME_ID_IS_ALREADY_RUNNING.
			insertPrefix := "INSERT INTO " + quoteIdentifier(cfg.Database) + "." + quoteIdentifier("conversations")
			var queryID string
			deadline := time.Now().Add(5 * time.Second)
			for queryID == "" {
				select {
				case err := <-done:
					t.Fatalf("WriteBatch returned before its INSERT was seen running: %v", err)
				default:
				}
				require.True(t, time.Now().Before(deadline), "outstanding INSERT never appeared in system.processes")
				var ids []string
				require.NoError(t, admin.QueryRow(ctx, "SELECT groupArray(query_id) FROM system.processes WHERE query_kind='Insert' AND startsWith(query, ?)", insertPrefix).Scan(&ids))
				if len(ids) > 0 {
					queryID = ids[0]
				} else {
					time.Sleep(10 * time.Millisecond)
				}
			}
			cancelWrite() // Represents loss of the old checkpoint lease.
			// A resumed admin stream cannot load a now-deleted resource. Replay keeps its
			// summary, then its ordered delete; it never invents an active full snapshot.
			next := testProcessor(t, cfg, sink, 2)
			require.NoError(t, next.Handle(ctx, processruntime.EventEnvelope{Kind: "conversation", Event: "created", Cursor: "create", Data: []byte(`{"conversation":"conversation-1","conversation_group":"` + testGroup + `"}`)}))
			require.NoError(t, next.Handle(ctx, processruntime.EventEnvelope{Kind: "conversation", Event: "deleted", Cursor: "delete", Data: []byte(`{"conversation":"conversation-1","conversation_group":"` + testGroup + `"}`)}))
			require.NoError(t, next.Flush(ctx))
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.Eventually(t, func() bool {
				var count uint64
				err := admin.QueryRow(ctx, "SELECT count() FROM system.processes WHERE query_id=?", queryID).Scan(&count)
				return err == nil && count == 0
			}, 5*time.Second, 20*time.Millisecond)
			require.Equal(t, uint64(0), countRows(t, ctx, admin, cfg.Database, "conversations_current", "exporter_id=?", cfg.ExporterID))
			raw := countRows(t, ctx, admin, cfg.Database, "conversations", "exporter_id=?", cfg.ExporterID)
			t.Logf("%s outstanding INSERT was unfinished at takeover: physical rows=%d, current rows=0", protocol, raw)
			require.Equal(t, uint64(2), countRows(t, ctx, admin, cfg.Database, "deletion_fences", "exporter_id=?", cfg.ExporterID))
			require.Equal(t, "delete", next.state.SafeCursor)
		})
	}
}
