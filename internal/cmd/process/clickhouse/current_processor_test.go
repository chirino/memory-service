package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func conversationEvent(cursor string) processruntime.EventEnvelope {
	return processruntime.EventEnvelope{Event: "updated", Kind: "conversation", Cursor: cursor, Time: time.Now().UTC(), Data: json.RawMessage(`{"id":"conversation-1","conversationGroupId":"` + testGroup + `","ownerUserId":"owner","clientId":"client","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:01Z","title":"private-title","metadata":{"selected":"yes","secret":"private-metadata"}}`)}
}
func testProcessor(t *testing.T, cfg Config, sink Sink, generation uint64) *Processor {
	t.Helper()
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(generation))
	return p
}
func completeCheckpoint(t *testing.T, p *Processor, cursor string) json.RawMessage {
	t.Helper()
	p.state.SafeCursor = cursor
	p.state.Bootstrap.State = "complete"
	p.state.Bootstrap.BackfillStartCursor = cursor
	p.state.Bootstrap.Target = nil
	raw, err := p.Snapshot()
	require.NoError(t, err)
	return raw
}

func TestObservationVersionsOrderRepeatedResourcesInOneBatch(t *testing.T) {
	sink := &fakeSink{}
	p := testProcessor(t, testConfig(), sink, 7)
	ctx := context.Background()
	for i := range 3 {
		require.NoError(t, p.Handle(ctx, conversationEvent(fmt.Sprint(i+1))))
	}
	require.Empty(t, sink.batches)
	require.Len(t, p.pending.Projections, 3)
	for i, row := range p.pending.Projections {
		require.Equal(t, uint64(7)<<32|uint64(i+1), row.IngestVersion)
	}
	require.NoError(t, p.Flush(ctx))
	require.Len(t, sink.batches, 1)
	require.Equal(t, "3", p.state.SafeCursor)
	require.NotContains(t, fmt.Sprint(sink.batches), "private-title")
	require.NotContains(t, fmt.Sprint(sink.batches), "private-metadata")
	p.observationSequence = ^uint32(0)
	require.ErrorContains(t, p.Handle(ctx, conversationEvent("4")), "sequence exhausted")
	require.Equal(t, "3", p.state.SafeCursor)
	require.Error(t, p.SetLeaseGeneration(uint64(1)<<32))
}

func TestBufferedObservationsDoNotRewriteCheckpoint(t *testing.T) {
	p := testProcessor(t, testConfig(), &fakeSink{}, 1)
	before := completeCheckpoint(t, p, "saved")
	for _, cursor := range []string{"1", "2", "3"} {
		require.NoError(t, p.Handle(context.Background(), conversationEvent(cursor)))
		after, err := p.Snapshot()
		require.NoError(t, err)
		require.JSONEq(t, string(before), string(after), "buffered events must not cause runtime checkpoint writes")
	}
	require.NoError(t, p.Flush(context.Background()))
	after, err := p.Snapshot()
	require.NoError(t, err)
	require.NotEqual(t, string(before), string(after))
	require.NotContains(t, string(after), "observationSequence")
}

type frozenRetrySink struct {
	fakeSink
	attempts []Batch
}

type purgeLookupRetrySink struct {
	fakeSink
	attempts    int
	lookupError error
}

func (s *purgeLookupRetrySink) ResolvePurge(_ context.Context, p PurgeRow) (PurgeRow, error) {
	s.attempts++
	if s.lookupError != nil {
		return p, s.lookupError
	}
	if s.attempts == 1 {
		return p, errors.New("temporary lookup failure")
	}
	return p, nil
}

func TestPurgeLookupRetriesBeforeAdvancingCursor(t *testing.T) {
	sink := &purgeLookupRetrySink{}
	p := testProcessor(t, testConfig(), sink, 1)
	event := processruntime.EventEnvelope{Kind: "conversation", Event: "deleted", Cursor: "delete", Data: json.RawMessage(`{"conversation":"deleted","conversation_group":"` + testGroup + `"}`)}
	require.NoError(t, p.Handle(context.Background(), event))
	require.Equal(t, 2, sink.attempts)
	require.Empty(t, p.state.SafeCursor)
	require.Len(t, p.pending.Purges, 1)
	require.NoError(t, p.Flush(context.Background()))
	require.Equal(t, "delete", p.state.SafeCursor)
}

func TestPurgeLookupCancellationAndConflictsKeepCheckpoint(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			sink := &purgeLookupRetrySink{lookupError: errors.New("temporary outage")}
			if conflict {
				sink.lookupError = errPurgeScopeConflict
			}
			p := testProcessor(t, testConfig(), sink, 1)
			before := completeCheckpoint(t, p, "saved")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			event := processruntime.EventEnvelope{Kind: "conversation", Event: "deleted", Cursor: "delete", Data: json.RawMessage(`{"conversation":"deleted","conversation_group":"` + testGroup + `"}`)}
			err := p.Handle(ctx, event)
			if conflict {
				require.ErrorIs(t, err, errPurgeScopeConflict)
				require.Equal(t, 1, sink.attempts)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			after, err := p.Snapshot()
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
			require.Empty(t, sink.batches)
		})
	}
}

func (s *frozenRetrySink) WriteBatch(ctx context.Context, b Batch) error {
	s.attempts = append(s.attempts, b)
	return s.fakeSink.WriteBatch(ctx, b)
}
func TestCheckpointAndFrozenRetry(t *testing.T) {
	sink := &frozenRetrySink{fakeSink: fakeSink{err: errors.New("unavailable"), failures: 1}}
	p := testProcessor(t, testConfig(), sink, 1)
	ctx := context.Background()
	require.NoError(t, p.Handle(ctx, conversationEvent("1")))
	require.Empty(t, p.state.SafeCursor)
	require.NoError(t, p.Flush(ctx))
	require.Len(t, sink.attempts, 2)
	require.Equal(t, sink.attempts[0], sink.attempts[1])
	require.Equal(t, "1", p.state.SafeCursor)
	checkpoint := completeCheckpoint(t, p, "1")
	restored := testProcessor(t, testConfig(), &fakeSink{}, 2)
	require.NoError(t, restored.Load(checkpoint))
	require.NoError(t, restored.SetLeaseGeneration(2))
	require.NoError(t, restored.Handle(ctx, conversationEvent("2")))
	require.Greater(t, restored.pending.Projections[0].IngestVersion, sink.batches[0].Projections[0].IngestVersion)
	require.ErrorIs(t, p.Handle(ctx, processruntime.EventEnvelope{Kind: "stream", Event: "invalidate"}), errBackfillRequired)
	require.Equal(t, "1", p.state.SafeCursor)
	for _, raw := range []string{`{"version":1,"exporterId":"test"}`, string(checkpoint[:len(checkpoint)-1]) + `,"version":2}`, string(checkpoint[:len(checkpoint)-1]) + `,"unknown":true}`} {
		require.Error(t, restored.Load(json.RawMessage(raw)))
	}
	require.Equal(t, "application/vnd.memory-service.clickhouse-checkpoint+json", p.ContentType())
}

func TestBootstrapAndReplayCommitOnlyAtLiveBoundary(t *testing.T) {
	ctx := context.Background()
	sink := &fakeSink{}
	p := testProcessor(t, testConfig(), sink, 1)
	client := &markerEventClient{}
	cursor, err := p.Bootstrap(ctx, client, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.Equal(t, "pg:100", cursor)
	require.Equal(t, "catchup", p.state.Bootstrap.State)
	require.NotNil(t, p.state.Bootstrap.Target)
	require.Empty(t, sink.batches[0].Lifecycle)
	require.Len(t, sink.batches[0].Projections, 1)
	checkpoint, err := p.Snapshot()
	require.NoError(t, err)
	restarted := testProcessor(t, testConfig(), &fakeSink{}, 2)
	require.NoError(t, restarted.Load(checkpoint))
	require.NoError(t, restarted.SetLeaseGeneration(2))
	cursor, err = restarted.Bootstrap(ctx, nil, func(context.Context) error { t.Fatal("catchup must not scan again"); return nil })
	require.NoError(t, err)
	require.Equal(t, "pg:100", cursor)
	require.NoError(t, restarted.Handle(ctx, processruntime.EventEnvelope{Kind: "stream", Event: "phase", Cursor: "pg:110", Data: json.RawMessage(`{"phase":"live"}`)}))
	require.Equal(t, "complete", restarted.state.Bootstrap.State)
	require.Nil(t, restarted.state.Bootstrap.Target)
	cfg := testConfig()
	cfg.ExportMemberships = true
	newOutput := testProcessor(t, cfg, &fakeSink{}, 3)
	require.ErrorContains(t, newOutput.Load(completeCheckpoint(t, restarted, "pg:110")), "replay-id")
	cfg.ProjectionReplayID = "add-memberships"
	newOutput = testProcessor(t, cfg, &fakeSink{}, 3)
	require.NoError(t, newOutput.Load(completeCheckpoint(t, restarted, "pg:110")))
	require.Equal(t, "scanning", newOutput.state.Bootstrap.State)
	require.Equal(t, "pg:110", newOutput.state.Bootstrap.BackfillStartCursor)
	pending, err := newOutput.Snapshot()
	require.NoError(t, err)
	cfg.ProjectionReplayID = "changed"
	changed := testProcessor(t, cfg, &fakeSink{}, 4)
	require.ErrorContains(t, changed.Load(pending), "pending replay")
}

func TestNormalizationPoliciesAndExactNumbers(t *testing.T) {
	cfg := testConfig()
	require.NoError(t, cfg.Validate())
	cfg.MetadataKeys = []string{"selected"}
	observation, err := decodeObservation(cfg, conversationEvent("1"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"selected": "yes"}, observation.Record["metadata"])
	require.NotContains(t, observation.Record, "title")
	raw := json.RawMessage(`{"id":"entry-1","conversationId":"conversation-1","conversationGroupId":"` + testGroup + `","channel":"history","contentType":"custom","createdAt":"2026-01-01T01:00:00+01:00","epoch":9007199254740993,"seq":4294967295,"content":["private-content"],"indexedContent":"private-index","unknown":"private-unknown"}`)
	event := processruntime.EventEnvelope{Kind: "entry", Event: "updated", Data: raw}
	for _, mode := range []PayloadMode{PayloadMetadata, PayloadProjected, PayloadFull} {
		cfg.PayloadMode = mode
		o, err := decodeObservation(cfg, event)
		require.NoError(t, err)
		require.Equal(t, int64(9007199254740993), o.Record["epoch"])
		require.Equal(t, uint64(4294967295), o.Record["seq"])
		require.Equal(t, "2026-01-01T00:00:00Z", o.Record["createdAt"])
		require.Equal(t, "HISTORY", o.Record["channel"])
		require.NotContains(t, o.Record, "unknown")
		_, content := o.Record["content"]
		require.Equal(t, mode != PayloadMetadata, content)
		_, indexed := o.Record["indexedContent"]
		require.Equal(t, mode == PayloadFull, indexed)
	}
	for _, data := range []string{`{"id":"entry"}`, `{"id":"entry","conversationId":"c","conversationGroupId":"` + testGroup + `","createdAt":"bad"}`, string(raw[:len(raw)-1]) + `,"seq":4294967296}`, string(raw[:len(raw)-1]) + `,"epoch":9223372036854775808}`} {
		event.Data = json.RawMessage(data)
		_, err := decodeObservation(cfg, event)
		require.Error(t, err)
	}
	cfg.MaxRecordBytes = 10
	_, err = decodeObservation(cfg, conversationEvent("1"))
	require.ErrorContains(t, err, "record_too_large")
}

func TestDisabledOutputsKeepDeletionCoverage(t *testing.T) {
	for _, mode := range []string{PurgeManaged, PurgeRecordOnly, PurgeExternal} {
		cfg := testConfig()
		cfg.PurgeMode = mode
		cfg.Disable = []string{"conversations"}
		sink := &fakeSink{}
		p := testProcessor(t, cfg, sink, 1)
		event := processruntime.EventEnvelope{Kind: "conversation", Event: "deleted", Cursor: "1", Data: json.RawMessage(`{"conversation":"conversation-1","conversation_group":"` + testGroup + `"}`)}
		require.NoError(t, p.Handle(context.Background(), event))
		require.NoError(t, p.Flush(context.Background()))
		b := sink.batches[0]
		require.Empty(t, b.Projections)
		require.Empty(t, b.Lifecycle)
		require.Len(t, b.Fences, 2)
		if mode == PurgeExternal {
			require.Empty(t, b.Purges)
		} else {
			require.Len(t, b.Purges, 1)
			require.Equal(t, purgeGroup, b.Purges[0].Scope)
			require.Equal(t, uint64(1)<<32|1, b.Purges[0].CutoffIngestVersion)
		}
	}
}

func TestMembershipIdentityAndRegrantInOneBatch(t *testing.T) {
	cfg := testConfig()
	cfg.ExportMemberships = true
	sink := &fakeSink{}
	p := testProcessor(t, cfg, sink, 1)
	ctx := context.Background()
	user := "用户/é"
	id := model.MembershipResourceID(uuid.MustParse(testGroup), user)
	grant := processruntime.EventEnvelope{Kind: "membership", Event: "created", Cursor: "1", Data: json.RawMessage(`{"id":"` + id + `","conversationGroupId":"` + testGroup + `","userId":"` + user + `","accessLevel":"reader","createdAt":"2026-01-01T00:00:00Z"}`)}
	require.NoError(t, p.Handle(ctx, grant))
	require.NoError(t, p.Handle(ctx, processruntime.EventEnvelope{Kind: "membership", Event: "deleted", Cursor: "2", Data: json.RawMessage(`{"conversation_group":"` + testGroup + `","user":"` + user + `"}`)}))
	grant.Cursor = "3"
	require.NoError(t, p.Handle(ctx, grant))
	require.NoError(t, p.Flush(ctx))
	require.Len(t, sink.batches, 1)
	b := sink.batches[0]
	require.Len(t, b.Memberships, 3)
	require.Equal(t, id, b.Memberships[1].ResourceID)
	require.True(t, b.Memberships[1].IsDeleted)
	require.Equal(t, purgeResource, b.Purges[0].Scope)
	require.Greater(t, b.Memberships[2].IngestVersion, b.Fences[0].IngestVersion)
}

func TestRetentionAndFailureConfiguration(t *testing.T) {
	cfg := testConfig()
	cfg.TombstoneRetention = time.Hour
	require.ErrorContains(t, cfg.Validate(), "projection retention")
	cfg.ProjectionRetention = 2 * time.Hour
	require.Error(t, cfg.Validate())
	cfg.ProjectionRetention = time.Hour
	require.NoError(t, cfg.Validate())
	cfg.ProjectionFailurePolicy = "skip-projection"
	require.Error(t, cfg.Validate())
	cfg.ProjectionFailurePolicy = "record"
	cfg.ProjectionReplayID = "replay"
	cfg.Disable = []string{"entries", "memories"}
	require.NoError(t, cfg.Validate())
}

func TestMissingIdentityIsFatalAndSnapshotAbsenceIsRecorded(t *testing.T) {
	for _, policy := range []string{"record", "stop"} {
		cfg := testConfig()
		cfg.ProjectionFailurePolicy = policy
		sink := &fakeSink{}
		p := testProcessor(t, cfg, sink, 1)
		err := p.Handle(context.Background(), processruntime.EventEnvelope{Kind: "conversation", Event: "deleted", Cursor: "bad", Data: json.RawMessage(`{"conversation":"c"}`)})
		require.ErrorContains(t, err, "group_identity")
		require.Empty(t, p.state.SafeCursor)
		require.Empty(t, sink.batches)
		event := processruntime.EventEnvelope{Kind: "conversation", Event: "updated", Cursor: "summary", Data: json.RawMessage(`{"conversation":"c","conversation_group":"` + testGroup + `"}`)}
		err = p.Handle(context.Background(), event)
		require.NoError(t, err)
		require.NoError(t, p.Flush(context.Background()))
		require.Equal(t, "summary", p.state.SafeCursor)
		require.Len(t, sink.batches[0].ProjectionFailures, 1)
		require.Empty(t, sink.batches[0].Projections)
	}
}

func TestStopPolicyStillRejectsInvalidProjectionOutput(t *testing.T) {
	cfg := testConfig()
	cfg.ProjectionFailurePolicy = "stop"
	p := testProcessor(t, cfg, &fakeSink{}, 1)
	event := processruntime.EventEnvelope{Kind: "memory", Event: "created", Cursor: "bad", Data: json.RawMessage(`{"id":"memory","namespace":"wrong-type","key":"key","kind":"kind","revision":1,"createdAt":"2026-01-01T00:00:00Z"}`)}
	require.ErrorContains(t, p.Handle(context.Background(), event), "invalid_string_array")
	require.Empty(t, p.state.SafeCursor)
}

func TestDefaultMemoryProjectionPreservesIntegerPrecision(t *testing.T) {
	sink := &fakeSink{}
	p := testProcessor(t, testConfig(), sink, 1)
	event := processruntime.EventEnvelope{Kind: "memory", Event: "created", Cursor: "1", Data: json.RawMessage(`{"id":"memory","namespace":[],"key":"key","kind":"kind","revision":9007199254740993,"createdAt":"2026-01-01T00:00:00Z"}`)}
	require.NoError(t, p.Handle(context.Background(), event))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches[0].Projections, 1)
	row := sink.batches[0].Projections[0]
	found := false
	for i, name := range row.ColumnNames {
		if name == "revision" {
			found = true
			require.Equal(t, int64(9007199254740993), row.Values[i])
		}
	}
	require.True(t, found)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, p.Handle(ctx, event), context.Canceled)
	require.Nil(t, p.pending)
}

type membershipRowsSink struct {
	fakeSink
	hasRows bool
	checked []string
}

func (s *membershipRowsSink) HasMembershipRows(_ context.Context, exporterID, resourceID string) (bool, error) {
	s.checked = append(s.checked, exporterID+"/"+resourceID)
	return s.hasRows, nil
}

func TestDisabledMembershipRevokeStoresNoUserIdentity(t *testing.T) {
	ctx := context.Background()
	user := "carol-secret-user"
	id := model.MembershipResourceID(uuid.MustParse(testGroup), user)
	revoke := processruntime.EventEnvelope{Kind: "membership", Event: "deleted", Cursor: "1", Data: json.RawMessage(`{"conversation_group":"` + testGroup + `","user":"` + user + `"}`)}
	for _, mode := range []string{PurgeManaged, PurgeRecordOnly} {
		sink := &membershipRowsSink{}
		cfg := testConfig()
		cfg.PurgeMode = mode
		p := testProcessor(t, cfg, sink, 1)
		require.NoError(t, p.Handle(ctx, revoke))
		require.NoError(t, p.Flush(ctx))
		require.Equal(t, []string{"test/" + id}, sink.checked)
		for _, b := range sink.batches {
			require.Empty(t, b.Fences, "a revoke without exported memberships must not store a fence")
			require.Empty(t, b.Purges, "a revoke without exported memberships must not queue a purge")
		}
		require.Equal(t, "1", p.state.SafeCursor)

		// Rows from an earlier enabled export still need a purge, but no fence.
		sink = &membershipRowsSink{hasRows: true}
		p = testProcessor(t, cfg, sink, 2)
		require.NoError(t, p.Handle(ctx, revoke))
		require.NoError(t, p.Flush(ctx))
		require.Len(t, sink.batches, 1)
		require.Empty(t, sink.batches[0].Fences)
		require.Len(t, sink.batches[0].Purges, 1)
		require.Equal(t, id, sink.batches[0].Purges[0].AnalyticsResourceID)
	}
	// External purge mode never queues purges, so it needs no lookup either.
	sink := &membershipRowsSink{hasRows: true}
	cfg := testConfig()
	cfg.PurgeMode = PurgeExternal
	p := testProcessor(t, cfg, sink, 1)
	require.NoError(t, p.Handle(ctx, revoke))
	require.NoError(t, p.Flush(ctx))
	require.Empty(t, sink.checked)
	for _, b := range sink.batches {
		require.Empty(t, b.Fences)
		require.Empty(t, b.Purges)
	}
}

type storedVersionSink struct {
	fakeSink
	version  uint64
	failures int
}

func (s *storedVersionSink) MaxIngestVersion(context.Context, string) (uint64, error) {
	if s.failures > 0 {
		s.failures--
		return 0, errors.New("temporary version lookup failure")
	}
	return s.version, nil
}

func TestBootstrapRefusesGenerationNotAboveStoredRows(t *testing.T) {
	ctx := context.Background()
	for _, generation := range []uint64{1, 3} {
		p := testProcessor(t, testConfig(), &storedVersionSink{version: uint64(3)<<32 | 5}, generation)
		completeCheckpoint(t, p, "pg:10")
		_, err := p.Bootstrap(ctx, nil, func(context.Context) error { t.Fatal("refused bootstrap must not save"); return nil })
		require.ErrorContains(t, err, "lost or reset")
		require.ErrorContains(t, err, "new client ID")
	}
	sink := &storedVersionSink{version: uint64(3)<<32 | 5, failures: 1}
	p := testProcessor(t, testConfig(), sink, 4)
	completeCheckpoint(t, p, "pg:10")
	cursor, err := p.Bootstrap(ctx, nil, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.Equal(t, "pg:10", cursor)
	require.Zero(t, sink.failures, "transient lookup failures are retried")
}

func TestTailOnlyDevelopmentStartsAtLiveBoundary(t *testing.T) {
	cfg := testConfig()
	cfg.TailOnlyDevelopment = true
	sink := &fakeSink{}
	p := testProcessor(t, cfg, sink, 1)
	client := &fixedEventClient{events: []processruntime.EventEnvelope{
		{Kind: "stream", Event: "phase", Data: json.RawMessage(`{"phase":"live"}`)},
		{Kind: "stream", Event: "phase", Cursor: "pg:42", Data: json.RawMessage(`{"phase":"replay"}`)},
		{Kind: "stream", Event: "phase", Cursor: "pg:42", Data: json.RawMessage(`{"phase":"live"}`)},
	}}
	saves := 0
	cursor, err := p.Bootstrap(context.Background(), client, func(context.Context) error { saves++; return nil })
	require.NoError(t, err)
	require.Equal(t, "pg:42", cursor)
	require.Equal(t, 1, saves)
	require.Equal(t, "pg:42", p.state.SafeCursor)
	require.Equal(t, bootstrapCheckpoint{State: "complete", BackfillStartCursor: "pg:42"}, p.state.Bootstrap)
	require.Empty(t, sink.batches, "tail-only mode skips the current-state scan")
}
