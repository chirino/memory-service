package clickhouse

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventstream"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeSink struct {
	batches  []Batch
	err      error
	failures int
}

func (s *fakeSink) EnsureSchema(context.Context, string) error { return nil }
func (s *fakeSink) WriteBatch(_ context.Context, batch Batch) error {
	if s.err != nil && s.failures != 0 {
		if s.failures > 0 {
			s.failures--
		}
		return s.err
	}
	s.batches = append(s.batches, batch)
	return nil
}
func (s *fakeSink) Close() error { return nil }

func testConfig() Config {
	return Config{ExporterID: "test", Addresses: []string{"localhost:9000"}, BatchEvents: 10, BatchRows: 20, BatchBytes: 1 << 20, RetryMin: 100 * time.Millisecond, RetryMax: 100 * time.Millisecond}
}

func TestProcessorAdvancesCheckpointOnlyAfterSuccessfulBatchWrite(t *testing.T) {
	sink := &fakeSink{err: errors.New("unavailable"), failures: 1}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(7))
	event := processruntime.EventEnvelope{Event: "created", Kind: "conversation", Cursor: "pg:7", Time: time.Unix(10, 0), Data: json.RawMessage(`{"conversation":"c1","conversation_group":"g1","title":"secret"}`)}
	require.NoError(t, p.Handle(context.Background(), event))
	state, err := p.Snapshot()
	require.NoError(t, err)
	require.NotContains(t, string(state), "pg:7")
	require.NoError(t, p.Flush(context.Background()))
	require.Equal(t, uint64(7)<<32|1, sink.batches[0].Version)
	state, err = p.Snapshot()
	require.NoError(t, err)
	require.Contains(t, string(state), "pg:7")
	require.Len(t, sink.batches, 1)
	require.NotContains(t, sink.batches[0].Resources[0].PayloadJSON, "secret")
}

func TestProcessorInvalidationFailsClosedAndPreservesCheckpoint(t *testing.T) {
	sink := &fakeSink{}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(1))
	require.NoError(t, p.Load(json.RawMessage(`{"version":1,"exporterId":"test","lastEventCursor":"mongo:stale","bootstrap":{"state":"complete","backfillStartCursor":"mongo:start"}}`)))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{Event: "deleted", Kind: "memory", Cursor: "mongo:before-gap", Time: time.Unix(100, 0), Data: json.RawMessage(`{"memory":"m1","change":"evicted"}`)}))
	err = p.Handle(context.Background(), processruntime.EventEnvelope{Event: "invalidate", Kind: "stream", Data: json.RawMessage(`{"reason":"history gap"}`)})
	require.ErrorContains(t, err, "backfill_required")
	require.Empty(t, sink.batches, "invalidation must not advance ClickHouse state or its checkpoint")
	snapshot, snapshotErr := p.Snapshot()
	require.NoError(t, snapshotErr)
	require.Contains(t, string(snapshot), "mongo:stale")
	require.Contains(t, string(snapshot), `"state":"complete"`)
}

func TestStableEventIDs(t *testing.T) {
	require.Equal(t, eventID("e", "c", "entry", "created", "created"), eventID("e", "c", "entry", "created", "created"))
}

func TestProcessorExportsSourceIdentifiers(t *testing.T) {
	cfg := testConfig()
	require.NoError(t, cfg.Validate())

	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(1))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "created", Kind: "conversation", Cursor: "pg:raw", Time: time.Unix(10, 0),
		Data: json.RawMessage(`{"conversation":"conversation-123","conversation_group":"group-456"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Equal(t, "conversation-123", sink.batches[0].Resources[0].ResourceID)
	require.Equal(t, "conversation-123", sink.batches[0].Resources[0].ConversationID)
	require.Equal(t, "group-456", sink.batches[0].Resources[0].ConversationGroupID)
}

func TestEntryLifecycleUsesDurableEventContentType(t *testing.T) {
	sink := &fakeSink{}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(1))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "created", Kind: "entry", Cursor: "pg:entry", Time: time.Unix(10, 0),
		Data: json.RawMessage(`{"entry":"entry-123","conversation":"conversation-123","entry_channel":"history","entry_content_type":"history/lc4j","entry_role":"USER"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Equal(t, "history/lc4j", sink.batches[0].Lifecycle[0].ContentType)
	require.JSONEq(t, `{"resourceKind":"entry","entry_channel":"history","entry_content_type":"history/lc4j","entry_role":"USER"}`, sink.batches[0].Lifecycle[0].SummaryJSON)
}

func TestHardDeleteAddsPayloadFreePurgeRequest(t *testing.T) {
	sink := &fakeSink{}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(2))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "deleted", Kind: "memory", Cursor: "pg:9", Time: time.Unix(20, 0),
		Data: json.RawMessage(`{"memory":"memory-secret-id","change":"hard_deleted"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Len(t, sink.batches[0].Purges, 1)
	require.Equal(t, "memory", sink.batches[0].Purges[0].ResourceKind)
	require.Equal(t, "memory-secret-id", sink.batches[0].Purges[0].AnalyticsResourceID)
}

func TestConversationHardDeleteCarriesDependentPurgeIdentifiers(t *testing.T) {
	sink := &fakeSink{}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(2))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "deleted", Kind: "conversation", Cursor: "pg:10", Time: time.Unix(20, 0),
		Data: json.RawMessage(`{"conversation":"conversation-secret-id","conversation_group":"group-secret-id","change":"hard_deleted"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Len(t, sink.batches[0].Purges, 1)
	purge := sink.batches[0].Purges[0]
	require.Equal(t, sink.batches[0].Resources[0].ConversationID, purge.ConversationID)
	require.Equal(t, sink.batches[0].Resources[0].ConversationGroupID, purge.ConversationGroupID)
	require.Equal(t, "conversation-secret-id", purge.ConversationID)
	require.Equal(t, "group-secret-id", purge.ConversationGroupID)
}

func TestConversationDeletedProductionEventQueuesHardDeletePurge(t *testing.T) {
	groupID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	events := eventstream.ConversationDeletedEvents([]registrystore.DeletedConversationGroup{{
		ConversationGroupID: groupID,
		ConversationIDs:     []string{"conversation-secret-id"},
		MemberUserIDs:       []string{"user-1"},
	}})
	require.Len(t, events, 1)
	raw, err := json.Marshal(events[0].Data)
	require.NoError(t, err)

	sink := &fakeSink{}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(2))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: events[0].Event, Kind: events[0].Kind, Cursor: "pg:production-delete", Time: time.Unix(20, 0), Data: raw,
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Len(t, sink.batches[0].Purges, 1)
	purge := sink.batches[0].Purges[0]
	require.Equal(t, "conversation", purge.ResourceKind)
	require.Equal(t, "conversation-secret-id", purge.AnalyticsResourceID)
	require.Equal(t, "conversation-secret-id", purge.ConversationID)
	require.Equal(t, groupID.String(), purge.ConversationGroupID)
}

func TestFullConversationCreatedEventsExportDirectLineage(t *testing.T) {
	sink := &fakeSink{}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(3))
	groupID := "00000000-0000-4000-8000-000000000002"
	root, child := "root", "child"
	rootEntry, childEntry := "00000000-0000-4000-8000-000000000003", "00000000-0000-4000-8000-000000000004"
	records := map[string]*pb.AnalyticsRecord{
		"child": {
			Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_CONVERSATION, Id: "child"}, SnapshotAvailable: true,
			Record: &pb.AnalyticsRecord_Conversation{Conversation: &pb.AnalyticsConversationRecord{Id: "child", ConversationGroupId: groupID, ForkedAtConversationId: &root, ForkedAtEntryId: &rootEntry, CreatedAt: timestamppb.New(time.Unix(20, 0)), UpdatedAt: timestamppb.New(time.Unix(20, 0))}},
		},
		"grandchild": {
			Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_CONVERSATION, Id: "grandchild"}, SnapshotAvailable: true,
			Record: &pb.AnalyticsRecord_Conversation{Conversation: &pb.AnalyticsConversationRecord{Id: "grandchild", ConversationGroupId: groupID, ForkedAtConversationId: &child, ForkedAtEntryId: &childEntry, CreatedAt: timestamppb.New(time.Unix(21, 0)), UpdatedAt: timestamppb.New(time.Unix(21, 0))}},
		},
	}
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "created", Kind: "conversation", Cursor: "pg:child", Time: time.Unix(20, 0),
		Data: testFullResourceData(t, records["child"]),
	}))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "created", Kind: "conversation", Cursor: "pg:grandchild", Time: time.Unix(21, 0),
		Data: testFullResourceData(t, records["grandchild"]),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	lineage := map[string]ResourceRow{}
	for _, row := range sink.batches[0].Resources {
		if row.ResourceType == "lineage" {
			lineage[row.ResourceID] = row
		}
	}
	require.Len(t, lineage, 2)
	for resourceID, descendant := range map[string]string{"root\nchild": "child", "child\ngrandchild": "grandchild"} {
		row, ok := lineage[resourceID]
		require.True(t, ok, resourceID)
		require.Equal(t, descendant, row.ConversationID)
		require.Equal(t, "00000000-0000-4000-8000-000000000002", row.ConversationGroupID)
	}
	require.JSONEq(t, `{"ancestorConversationId":"child","depth":1,"descendantConversationId":"grandchild","forkedAtEntryId":"00000000-0000-4000-8000-000000000004"}`, lineage["child\ngrandchild"].PayloadJSON)
}

func TestClickHouseRuntimeSubscribesWithFullDetail(t *testing.T) {
	for _, mode := range []PayloadMode{PayloadMetadata, PayloadFull, PayloadProjected} {
		cfg := testConfig()
		cfg.PayloadMode = mode
		runtimeConfig := clickHouseRuntimeConfig(StartOptions{ClientID: "exporter", Config: cfg}, func(bool) {})
		require.Equal(t, "full", runtimeConfig.Detail, mode)
		require.Equal(t, clickHouseEntryChannels, runtimeConfig.EntryChannels, mode)
	}
}

func TestConfigRequiresFullPayloadAcknowledgementAndRemoteTLS(t *testing.T) {
	cfg := testConfig()
	cfg.PayloadMode = PayloadFull
	require.ErrorContains(t, cfg.Validate(), "allow-decrypted-content")
	cfg.AllowDecryptedContent = true
	cfg.Addresses = []string{"clickhouse.example:9000"}
	require.ErrorContains(t, cfg.Validate(), "TLS is required")
	cfg.TLS = true
	require.NoError(t, cfg.Validate())
}

func TestConfigSupportsDirectClickHousePassword(t *testing.T) {
	cfg := testConfig()
	cfg.Password = "clickhouse-secret"
	require.NoError(t, cfg.Validate())

	password, err := cfg.readPassword()
	require.NoError(t, err)
	require.Equal(t, "clickhouse-secret", password)
}

func TestConfigRejectsClickHousePasswordAndPasswordFile(t *testing.T) {
	cfg := testConfig()
	cfg.Password = "clickhouse-secret"
	cfg.PasswordFile = "/run/secrets/clickhouse-password"
	require.ErrorContains(t, cfg.Validate(), "mutually exclusive")
}

func TestConfigResourceKindOptOuts(t *testing.T) {
	cfg := testConfig()
	require.Equal(t, []string{"conversation", "entry", "memory"}, cfg.resourceKinds())
	require.Equal(t, []string{"conversation", "entry", "memory"}, cfg.subscriptionKinds())

	cfg.Disable = []string{"conversations"}
	require.Equal(t, []string{"entry", "memory"}, cfg.resourceKinds())
	require.Equal(t, []string{"conversation", "entry", "memory"}, cfg.subscriptionKinds(), "conversation deletes remain subscribed for dependent entry purges")

	cfg.Disable = []string{"conversations", "entries"}
	require.Equal(t, []string{"memory"}, cfg.resourceKinds())
	require.Equal(t, []string{"conversation", "entry", "memory"}, cfg.subscriptionKinds(), "hard deletes remain subscribed for rows written before an output was disabled")

	cfg.PurgeMode = PurgeExternal
	require.Equal(t, []string{"memory"}, cfg.subscriptionKinds())

	cfg.Disable = []string{"conversations", "entries", "memories"}
	require.ErrorContains(t, cfg.Validate(), "at least one ClickHouse resource kind")
}

func TestConfigDisableSelectors(t *testing.T) {
	cfg := testConfig()
	cfg.Disable = []string{"*:lifecycle_events, conversations:lineage", "entries:projections"}
	require.NoError(t, cfg.Validate())
	require.Equal(t, []string{"*:lifecycle_events", "conversations:lineage", "entries:projections"}, cfg.Disable)
	require.True(t, cfg.featureDisabled("conversation", disableLifecycleEvents))
	require.True(t, cfg.featureDisabled("entry", disableLifecycleEvents))
	require.True(t, cfg.featureDisabled("memory", disableLifecycleEvents))
	require.True(t, cfg.featureDisabled("conversation", disableLineage))
	require.True(t, cfg.featureDisabled("entry", disableProjections))
	require.False(t, cfg.featureDisabled("memory", disableProjections))
}

func TestConfigRejectsInvalidDisableSelectors(t *testing.T) {
	for _, selector := range []string{"entry", "entries:lineage", "memories:usage_snapshots", "memories:unknown", "*:unknown"} {
		cfg := testConfig()
		cfg.Disable = []string{selector}
		require.Error(t, cfg.Validate(), selector)
	}
}

func TestConfigExternalRetentionRejectsManagedDurations(t *testing.T) {
	cfg := testConfig()
	cfg.RetentionMode = RetentionExternal
	require.NoError(t, cfg.Validate())
	cfg.LifecycleRetention = time.Hour
	require.ErrorContains(t, cfg.Validate(), "require managed retention mode")
}

func TestConfigValidatesPurgeMode(t *testing.T) {
	for _, mode := range []string{PurgeManaged, PurgeRecordOnly, PurgeExternal} {
		cfg := testConfig()
		cfg.PurgeMode = mode
		require.NoError(t, cfg.Validate())
	}
	cfg := testConfig()
	cfg.PurgeMode = "disabled"
	require.ErrorContains(t, cfg.Validate(), "purge mode")
}

func TestProjectionReplayRequiresAnEnabledProjectedResourceKind(t *testing.T) {
	cfg := testConfig()
	cfg.ProjectionReplayID = "replay-1"
	cfg.ProjectionPaths = []string{"projection.yaml"}
	cfg.PayloadMode = PayloadProjected
	cfg.Disable = []string{"entries", "memories"}
	require.ErrorContains(t, cfg.Validate(), "projection replay requires entry or memory projection output")
}

func TestConfigRejectsTombstoneRetentionThatCanResurrectCurrentRows(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		wantErr   string
	}{
		{
			name: "generic rows retained indefinitely",
			configure: func(cfg *Config) {
				cfg.TombstoneRetention = time.Hour
			},
			wantErr: "generic payload retention",
		},
		{
			name: "generic rows retained longer than tombstones",
			configure: func(cfg *Config) {
				cfg.TombstoneRetention = time.Hour
				cfg.GenericPayloadRetention = 2 * time.Hour
			},
			wantErr: "generic payload retention",
		},
		{
			name: "projection rows retained indefinitely",
			configure: func(cfg *Config) {
				cfg.PayloadMode = PayloadProjected
				cfg.ProjectionPaths = []string{"projection.yaml"}
				cfg.TombstoneRetention = time.Hour
				cfg.GenericPayloadRetention = time.Hour
			},
			wantErr: "projection retention",
		},
		{
			name: "projection rows retained longer than tombstones",
			configure: func(cfg *Config) {
				cfg.PayloadMode = PayloadProjected
				cfg.ProjectionPaths = []string{"projection.yaml"}
				cfg.TombstoneRetention = time.Hour
				cfg.GenericPayloadRetention = time.Hour
				cfg.ProjectionRetention = 2 * time.Hour
			},
			wantErr: "projection retention",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := testConfig()
			test.configure(&cfg)
			require.ErrorContains(t, cfg.Validate(), test.wantErr)
		})
	}

	cfg := testConfig()
	cfg.PayloadMode = PayloadProjected
	cfg.ProjectionPaths = []string{"projection.yaml"}
	cfg.TombstoneRetention = 2 * time.Hour
	cfg.GenericPayloadRetention = time.Hour
	cfg.ProjectionRetention = 2 * time.Hour
	require.NoError(t, cfg.Validate())
}

func TestTLSConfigurationLoadsCustomCA(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "clickhouse-test-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0o600))
	config, err := (Config{TLS: true, CAFile: path}).tlsConfig()
	require.NoError(t, err)
	require.Equal(t, uint16(0x0303), config.MinVersion)
	require.NotNil(t, config.RootCAs)
}

func TestProcessorHealthEndpoints(t *testing.T) {
	var ready atomic.Bool
	handler := healthHandler(&ready)
	for _, test := range []struct {
		path string
		want int
	}{
		{"/healthz", http.StatusOK},
		{"/readyz", http.StatusServiceUnavailable},
		{"/metrics", http.StatusOK},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		require.Equal(t, test.want, response.Code)
	}
	ready.Store(true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, response.Code)
}

func TestBootstrapCapturesHighWaterAndBackfillsEveryResourcePhase(t *testing.T) {
	sink := &fakeSink{}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(3))
	events := &markerEventClient{}
	saves := 0
	cursor, err := p.Bootstrap(context.Background(), events, func(context.Context) error {
		saves++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "pg:100", cursor)
	require.Equal(t, "current", events.request.InitialState)
	require.Equal(t, "full", events.request.Detail)
	require.ElementsMatch(t, []string{"conversation", "entry", "memory"}, events.request.Kinds)
	require.Equal(t, clickHouseEntryChannels, events.request.EntryChannels)
	require.GreaterOrEqual(t, saves, 3)
	require.Len(t, sink.batches, 1)
	require.Len(t, sink.batches[0].Resources, 1)
	require.Equal(t, "conversation", sink.batches[0].Resources[0].ResourceType)
	require.Contains(t, sink.batches[0].Resources[0].PayloadJSON, `"sourceId":"conversation-1"`)
	require.Contains(t, sink.batches[0].Resources[0].PayloadJSON, `"ownerUserId":"owner"`)
	require.Contains(t, sink.batches[0].Resources[0].PayloadJSON, `"clientId":"client"`)
	state, err := p.Snapshot()
	require.NoError(t, err)
	require.Contains(t, string(state), `"state":"catchup"`)
	require.Contains(t, string(state), `"lastEventCursor":"pg:100"`)
}

func TestBootstrapRestartPreservesOriginalCatchupBoundary(t *testing.T) {
	sink := &fakeSink{}
	p, err := NewProcessor(testConfig(), sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(3))
	require.NoError(t, p.Load(json.RawMessage(`{
		"version":1,
		"exporterId":"test",
		"bootstrap":{"state":"scanning","backfillStartCursor":"pg:100"}
	}`)))

	events := &fixedEventClient{events: []processruntime.EventEnvelope{
		{Event: "phase", Kind: "stream", Cursor: "pg:200", Data: json.RawMessage(`{"phase":"snapshot"}`)},
		{Event: "phase", Kind: "stream", Cursor: "pg:200", Data: json.RawMessage(`{"phase":"replay"}`)},
	}}
	cursor, err := p.Bootstrap(context.Background(), events, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.Equal(t, "pg:100", cursor, "restart must replay deletes since the original snapshot boundary")

	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "deleted", Kind: "conversation", Cursor: "pg:150",
		Data: json.RawMessage(`{"id":"deleted-during-scan","conversationGroupId":"00000000-0000-0000-0000-000000000001"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Len(t, sink.batches[0].Purges, 1)
	require.Equal(t, "deleted-during-scan", sink.batches[0].Purges[0].AnalyticsResourceID)
}

func TestBootstrapSkipsDisabledResourceKinds(t *testing.T) {
	cfg := testConfig()
	cfg.Disable = []string{"entries", "memories"}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(3))
	events := &markerEventClient{}

	_, err = p.Bootstrap(context.Background(), events, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.Equal(t, []string{"conversation"}, events.request.Kinds)
}

func TestBootstrapSkipsDisabledLineage(t *testing.T) {
	cfg := testConfig()
	cfg.Disable = []string{"conversations:lineage", "entries", "memories"}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(3))
	events := &markerEventClient{}

	_, err = p.Bootstrap(context.Background(), events, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.Equal(t, []string{"conversation"}, events.request.Kinds)
}

func TestDisablingResourceKindPreservesCheckpoint(t *testing.T) {
	cfg := testConfig()
	cfg.Disable = []string{"memories"}
	p, err := NewProcessor(cfg, &fakeSink{})
	require.NoError(t, err)
	require.NoError(t, p.Load(json.RawMessage(`{"version":1,"exporterId":"test","lastEventCursor":"pg:55","enabledOutputs":["conversations","conversations:lifecycle_events","conversations:lineage","entries","entries:lifecycle_events","entries:projections","memories","memories:lifecycle_events","memories:projections"],"purgeMode":"managed","bootstrap":{"state":"complete","backfillStartCursor":"pg:50"}}`)))

	snapshot, err := p.Snapshot()
	require.NoError(t, err)
	require.Contains(t, string(snapshot), `"lastEventCursor":"pg:55"`)
	require.NotContains(t, string(snapshot), `"memories"`)
	require.Contains(t, string(snapshot), `"state":"complete"`)
}

func TestReenablingResourceKindRequiresResetOrNewExporter(t *testing.T) {
	p, err := NewProcessor(testConfig(), &fakeSink{})
	require.NoError(t, err)
	err = p.Load(json.RawMessage(`{"version":1,"exporterId":"test","lastEventCursor":"pg:55","enabledOutputs":["conversations","conversations:lifecycle_events","conversations:lineage","entries","entries:lifecycle_events","entries:projections"],"purgeMode":"managed","bootstrap":{"state":"complete","backfillStartCursor":"pg:50"}}`))
	require.ErrorContains(t, err, "was disabled in the checkpoint")
	require.ErrorContains(t, err, "reset the exporter checkpoint")
}

func TestReenablingProjectionsRequiresNewReplayID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projection.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: replay_v1}
spec:
  resource: entry
  selector: {contentType: replay/v1}
  columns:
    value: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"value": input.content.value}
`), 0o600))
	checkpoint := json.RawMessage(`{"version":1,"exporterId":"test","lastEventCursor":"pg:55","enabledOutputs":["conversations","conversations:lifecycle_events","conversations:lineage","entries","entries:lifecycle_events","memories","memories:lifecycle_events"],"purgeMode":"managed","bootstrap":{"state":"complete","backfillStartCursor":"pg:50"}}`)

	cfg := testConfig()
	cfg.PayloadMode = PayloadProjected
	cfg.ProjectionPaths = []string{path}
	p, err := NewProcessor(cfg, &fakeSink{})
	require.NoError(t, err)
	require.ErrorContains(t, p.Load(checkpoint), "entries:projections")

	cfg.ProjectionReplayID = "enable-projections-1"
	p, err = NewProcessor(cfg, &fakeSink{})
	require.NoError(t, err)
	require.NoError(t, p.Load(checkpoint))
}

func TestTailOnlyDevelopmentCapturesTailWithoutBackfill(t *testing.T) {
	cfg := testConfig()
	cfg.TailOnlyDevelopment = true
	p, err := NewProcessor(cfg, &fakeSink{})
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(1))
	saves := 0
	cursor, err := p.Bootstrap(context.Background(), &markerEventClient{}, func(context.Context) error { saves++; return nil })
	require.NoError(t, err)
	require.Equal(t, "pg:100", cursor)
	require.Equal(t, 1, saves)
	state, err := p.Snapshot()
	require.NoError(t, err)
	require.Contains(t, string(state), `"state":"complete"`)
}

func TestDisabledConversationExportStillPurgesDependentEntries(t *testing.T) {
	cfg := testConfig()
	cfg.Disable = []string{"conversations"}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(2))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "deleted", Kind: "conversation", Cursor: "pg:10", Time: time.Unix(20, 0),
		Data: json.RawMessage(`{"conversation":"conversation-secret-id","conversation_group":"group-secret-id","change":"hard_deleted"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Empty(t, sink.batches[0].Lifecycle)
	require.Empty(t, sink.batches[0].Resources)
	require.Len(t, sink.batches[0].Purges, 1)
	require.Equal(t, "conversation-secret-id", sink.batches[0].Purges[0].AnalyticsResourceID)
}

func TestDisabledResourceStillPurgesPreviouslyExportedRows(t *testing.T) {
	cfg := testConfig()
	cfg.Disable = []string{"memories"}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(2))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "deleted", Kind: "memory", Cursor: "pg:10", Time: time.Unix(20, 0),
		Data: json.RawMessage(`{"memory":"memory-secret-id","change":"hard_deleted"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Empty(t, sink.batches[0].Lifecycle)
	require.Empty(t, sink.batches[0].Resources)
	require.Len(t, sink.batches[0].Purges, 1)
	require.Equal(t, "memory-secret-id", sink.batches[0].Purges[0].AnalyticsResourceID)
}

func TestDisabledLifecycleEventsStillWritesCurrentState(t *testing.T) {
	cfg := testConfig()
	cfg.Disable = []string{"*:lifecycle_events"}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(2))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "created", Kind: "entry", Cursor: "pg:11", Time: time.Unix(20, 0),
		Data: json.RawMessage(`{"entry":"entry-1","conversation":"conversation-1","conversation_group":"group-1","entry_content_type":"history/v1"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Empty(t, sink.batches[0].Lifecycle)
	require.Len(t, sink.batches[0].Resources, 1)
}

func TestExternalPurgeModeDoesNotQueueHardDeletes(t *testing.T) {
	cfg := testConfig()
	cfg.PurgeMode = PurgeExternal
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(2))
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{
		Event: "deleted", Kind: "memory", Cursor: "pg:12", Time: time.Unix(20, 0),
		Data: json.RawMessage(`{"memory":"memory-1","change":"hard_deleted"}`),
	}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Empty(t, sink.batches[0].Purges)
	require.Len(t, sink.batches[0].Resources, 1)
	require.True(t, sink.batches[0].Resources[0].IsDeleted)
}

func TestManagedPurgeCannotResumeAfterExternalCheckpoint(t *testing.T) {
	p, err := NewProcessor(testConfig(), &fakeSink{})
	require.NoError(t, err)
	err = p.Load(json.RawMessage(`{"version":1,"exporterId":"test","purgeMode":"external","enabledOutputs":["conversations","conversations:lifecycle_events","conversations:lineage","entries","entries:lifecycle_events","entries:projections","memories","memories:lifecycle_events","memories:projections"],"bootstrap":{"state":"complete","backfillStartCursor":"pg:50"}}`))
	require.ErrorContains(t, err, "hard-delete events may have been skipped")
}

func TestProjectedModeWritesTypedRowWithoutGenericPlaintext(t *testing.T) {
	dir := t.TempDir()
	manifest := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata:
  name: support_ticket_v1
spec:
  resource: entry
  selector:
    contentType: support-ticket/v1
  columns:
    outcome: {type: string, nullable: false}
    latency_ms: {type: uint64, nullable: true}
    tags: {type: string_array, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {
      "outcome": input.content.outcome,
      "latency_ms": object.get(input.content, "latencyMs", null),
      "tags": object.get(input.content, "tags", []),
    }
`
	path := filepath.Join(dir, "projection.yaml")
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
	cfg := testConfig()
	cfg.PayloadMode = PayloadProjected
	cfg.ProjectionPaths = []string{path}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(5))
	content, err := structpb.NewValue(map[string]any{"outcome": "resolved", "latencyMs": 12, "tags": []any{"urgent"}, "secret": "must-not-persist"})
	require.NoError(t, err)
	record := &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: "entry-1"}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{Id: "entry-1", ConversationId: "conversation-1", ConversationGroupId: "group-1", ContentType: "support-ticket/v1", CreatedAt: timestamppb.New(time.Unix(10, 0)), Content: content}},
	}
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{Event: "created", Kind: "entry", Cursor: "pg:20", Time: time.Unix(10, 0), Data: testFullResourceData(t, record)}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches, 1)
	require.Equal(t, "support-ticket/v1", sink.batches[0].Lifecycle[0].ContentType)
	require.Len(t, sink.batches[0].Projections, 1)
	require.Equal(t, []string{"latency_ms", "outcome", "tags"}, sink.batches[0].Projections[0].ColumnNames)
	require.Equal(t, []any{uint64(12), "resolved", []string{"urgent"}}, sink.batches[0].Projections[0].Values)
	require.NotContains(t, sink.batches[0].Resources[0].PayloadJSON, "must-not-persist")
	require.NotContains(t, sink.batches[0].Resources[0].PayloadJSON, "resolved")

	disabledCfg := cfg
	disabledCfg.Disable = []string{"entries:projections"}
	disabledSink := &fakeSink{}
	disabledProcessor, err := NewProcessor(disabledCfg, disabledSink)
	require.NoError(t, err)
	require.NoError(t, disabledProcessor.SetLeaseGeneration(6))
	disabledRecord := &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: "entry-2"}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{Id: "entry-2", ConversationId: "conversation-1", ConversationGroupId: "group-1", ContentType: "support-ticket/v1", CreatedAt: timestamppb.New(time.Unix(11, 0)), Content: content}},
	}
	require.NoError(t, disabledProcessor.Handle(context.Background(), processruntime.EventEnvelope{Event: "created", Kind: "entry", Cursor: "pg:21", Time: time.Unix(11, 0), Data: testFullResourceData(t, disabledRecord)}))
	require.NoError(t, disabledProcessor.Flush(context.Background()))
	require.Len(t, disabledSink.batches, 1)
	require.Empty(t, disabledSink.batches[0].Projections)
	require.Empty(t, disabledSink.batches[0].ProjectionFailures)
	require.Len(t, disabledSink.batches[0].Resources, 1)
}

func TestEntryPayloadModesProtectContentAndIndexedContentForLiveAndBootstrap(t *testing.T) {
	dir := t.TempDir()
	manifest := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: payload_boundary_v1}
spec:
  resource: entry
  selector: {contentType: payload-boundary/v1}
  columns:
    approved: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"approved": input.content.approved}
`
	projectionPath := filepath.Join(dir, "projection.yaml")
	require.NoError(t, os.WriteFile(projectionPath, []byte(manifest), 0o600))

	contentSecret := "content-secret-unique"
	indexedSecret := "indexed-secret-unique"
	approved := "projection-approved-value"
	indexedAt := timestamppb.New(time.Unix(12, 0))
	content, err := structpb.NewValue(map[string]any{"approved": approved, "secret": contentSecret})
	require.NoError(t, err)
	record := &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: "payload-entry"}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{
			Id: "payload-entry", ConversationId: "conversation-1", ConversationGroupId: "group-1",
			Channel: pb.Channel_HISTORY, ContentType: "payload-boundary/v1", Content: content,
			IndexedContent: &indexedSecret, IndexedAt: indexedAt, CreatedAt: timestamppb.New(time.Unix(10, 0)),
		}},
	}
	resourceData := testFullResourceData(t, record)
	bootstrapRecord, err := analyticsRecordFromResource("entry", resourceData)
	require.NoError(t, err)

	for _, mode := range []PayloadMode{PayloadMetadata, PayloadProjected, PayloadFull} {
		for _, path := range []string{"live", "bootstrap"} {
			t.Run(string(mode)+"/"+path, func(t *testing.T) {
				cfg := testConfig()
				cfg.PayloadMode = mode
				cfg.AllowDecryptedContent = mode == PayloadFull
				if mode == PayloadProjected {
					cfg.ProjectionPaths = []string{projectionPath}
				}
				sink := &fakeSink{}
				processor, err := NewProcessor(cfg, sink)
				require.NoError(t, err)
				require.NoError(t, processor.SetLeaseGeneration(9))
				if path == "live" {
					require.NoError(t, processor.Handle(context.Background(), processruntime.EventEnvelope{
						Event: "created", Kind: "entry", Cursor: "pg:payload", Time: time.Unix(10, 0), Data: resourceData,
					}))
					require.NoError(t, processor.Flush(context.Background()))
				} else {
					require.NoError(t, processor.writeBackfillPage(context.Background(), "entry", []*pb.AnalyticsRecord{bootstrapRecord}))
				}
				require.Len(t, sink.batches, 1)
				require.Len(t, sink.batches[0].Resources, 1)
				payload := sink.batches[0].Resources[0].PayloadJSON
				if mode == PayloadFull {
					require.Contains(t, payload, contentSecret)
					require.Contains(t, payload, indexedSecret)
					require.Contains(t, payload, approved)
				} else {
					require.NotContains(t, payload, contentSecret)
					require.NotContains(t, payload, indexedSecret)
					require.NotContains(t, payload, approved)
				}
				if mode == PayloadProjected {
					require.Len(t, sink.batches[0].Projections, 1)
					require.Equal(t, []any{approved}, sink.batches[0].Projections[0].Values)
				} else {
					require.Empty(t, sink.batches[0].Projections)
				}
			})
		}
	}
}

func TestSingleRecordMayExceedAggregateBatchTargetForLiveAndBootstrap(t *testing.T) {
	contentText := strings.Repeat("x", 2<<20)
	content, err := structpb.NewValue(map[string]any{"text": contentText})
	require.NoError(t, err)
	record := &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: "large-entry"}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{
			Id: "large-entry", ConversationId: "conversation-1", ConversationGroupId: "group-1",
			Channel: pb.Channel_HISTORY, ContentType: "history", Content: content, CreatedAt: timestamppb.New(time.Unix(10, 0)),
		}},
	}
	resourceData := testFullResourceData(t, record)
	bootstrapRecord, err := analyticsRecordFromResource("entry", resourceData)
	require.NoError(t, err)

	for _, path := range []string{"live", "bootstrap"} {
		t.Run(path, func(t *testing.T) {
			cfg := testConfig()
			cfg.PayloadMode = PayloadFull
			cfg.AllowDecryptedContent = true
			cfg.BatchBytes = 1 << 20
			sink := &fakeSink{}
			processor, err := NewProcessor(cfg, sink)
			require.NoError(t, err)
			require.NoError(t, processor.SetLeaseGeneration(10))
			if path == "live" {
				require.NoError(t, processor.Handle(context.Background(), processruntime.EventEnvelope{
					Event: "created", Kind: "entry", Cursor: "pg:large", Time: time.Unix(10, 0), Data: resourceData,
				}))
				state, err := processor.Snapshot()
				require.NoError(t, err)
				require.Contains(t, string(state), "pg:large")
			} else {
				require.NoError(t, processor.writeBackfillPage(context.Background(), "entry", []*pb.AnalyticsRecord{bootstrapRecord}))
			}
			require.Len(t, sink.batches, 1)
			require.Len(t, sink.batches[0].Resources, 1)
			require.Greater(t, len(sink.batches[0].Resources[0].PayloadJSON), cfg.BatchBytes)
			require.Contains(t, sink.batches[0].Resources[0].PayloadJSON, contentText)
		})
	}
}

func TestRecordAboveMaximumFailsForLiveAndBootstrap(t *testing.T) {
	content, err := structpb.NewValue(map[string]any{"text": strings.Repeat("x", 2<<20)})
	require.NoError(t, err)
	record := &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: "too-large-entry"}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{
			Id: "too-large-entry", ConversationId: "conversation-1", ConversationGroupId: "group-1",
			Channel: pb.Channel_HISTORY, ContentType: "history", Content: content, CreatedAt: timestamppb.New(time.Unix(10, 0)),
		}},
	}
	resourceData := testFullResourceData(t, record)
	bootstrapRecord, err := analyticsRecordFromResource("entry", resourceData)
	require.NoError(t, err)

	for _, path := range []string{"live", "bootstrap"} {
		t.Run(path, func(t *testing.T) {
			cfg := testConfig()
			cfg.PayloadMode = PayloadFull
			cfg.AllowDecryptedContent = true
			cfg.MaxRecordBytes = 1 << 20
			sink := &fakeSink{}
			processor, err := NewProcessor(cfg, sink)
			require.NoError(t, err)
			require.NoError(t, processor.SetLeaseGeneration(11))
			if path == "live" {
				err = processor.Handle(context.Background(), processruntime.EventEnvelope{
					Event: "created", Kind: "entry", Cursor: "pg:too-large", Time: time.Unix(10, 0), Data: resourceData,
				})
			} else {
				err = processor.writeBackfillPage(context.Background(), "entry", []*pb.AnalyticsRecord{bootstrapRecord})
			}
			require.ErrorContains(t, err, "record_too_large")
			require.ErrorContains(t, err, "record limit is 1048576")
			require.Empty(t, sink.batches)
		})
	}
}

func TestProjectionManifestRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("apiVersion: memory-service/v1alpha1\nkind: AnalyticsProjection\nunknown: true\n"), 0o600))
	_, err := LoadProjectionSet(context.Background(), []string{path})
	require.ErrorContains(t, err, "field unknown not found")
}

func TestProjectionManifestRejectsNondeterministicRego(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-rego.yaml")
	manifest := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: unsafe_v1}
spec:
  resource: entry
  selector: {contentType: unsafe/v1}
  columns:
    observed: {type: int64, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"observed": time.now_ns()}
`
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
	_, err := LoadProjectionSet(context.Background(), []string{path})
	require.ErrorContains(t, err, "nondeterministic builtins")
}

func TestProjectionFailurePolicyRecordsPayloadFreeFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projection.yaml")
	manifest := `apiVersion: memory-service/v1alpha1
kind: AnalyticsProjection
metadata: {name: failure_v1}
spec:
  resource: entry
  selector: {contentType: failure/v1}
  columns:
    required: {type: string, nullable: false}
  projectionRego: |
    package memoryservice.analytics
    output := {"required": object.get(input.content, "missing", null)}
`
	require.NoError(t, os.WriteFile(path, []byte(manifest), 0o600))
	cfg := testConfig()
	cfg.PayloadMode = PayloadProjected
	cfg.ProjectionPaths = []string{path}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(6))
	content, err := structpb.NewValue(map[string]any{"secret": "never-in-failure"})
	require.NoError(t, err)
	record := &pb.AnalyticsRecord{Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: "failure-entry"}, SnapshotAvailable: true, Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{Id: "failure-entry", ContentType: "failure/v1", CreatedAt: timestamppb.Now(), Content: content}}}
	require.NoError(t, p.Handle(context.Background(), processruntime.EventEnvelope{Event: "created", Kind: "entry", Cursor: "pg:failure", Data: testFullResourceData(t, record)}))
	require.NoError(t, p.Flush(context.Background()))
	require.Len(t, sink.batches[0].ProjectionFailures, 1)
	failureJSON, err := json.Marshal(sink.batches[0].ProjectionFailures[0])
	require.NoError(t, err)
	require.NotContains(t, string(failureJSON), "never-in-failure")
	require.Equal(t, "null_required_column", sink.batches[0].ProjectionFailures[0].ErrorCode)
}

type markerEventClient struct {
	request processruntime.SubscribeRequest
}

type fixedEventClient struct {
	events []processruntime.EventEnvelope
}

func (c *fixedEventClient) Subscribe(context.Context, processruntime.SubscribeRequest) (processruntime.EventStream, error) {
	return &markerEventStream{events: c.events}, nil
}

func (c *markerEventClient) Subscribe(_ context.Context, request processruntime.SubscribeRequest) (processruntime.EventStream, error) {
	c.request = request
	if request.InitialState != "current" {
		return &markerEventStream{events: []processruntime.EventEnvelope{{Event: "phase", Kind: "stream", Cursor: "pg:100", Data: json.RawMessage(`{"phase":"live"}`)}}}, nil
	}
	conversation := &pb.AnalyticsRecord{
		Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_CONVERSATION, Id: "conversation-1"}, SnapshotAvailable: true,
		Record: &pb.AnalyticsRecord_Conversation{Conversation: &pb.AnalyticsConversationRecord{Id: "conversation-1", ConversationGroupId: "00000000-0000-0000-0000-000000000001", OwnerUserId: "owner", ClientId: "client", CreatedAt: timestamppb.New(time.Unix(1, 0)), UpdatedAt: timestamppb.New(time.Unix(2, 0))}},
	}
	return &markerEventStream{events: []processruntime.EventEnvelope{
		{Event: "phase", Kind: "stream", Cursor: "pg:100", Data: json.RawMessage(`{"phase":"snapshot"}`)},
		{Event: "snapshot", Kind: "conversation", Data: testFullResourceDataNoTest(conversation)},
		{Event: "phase", Kind: "stream", Cursor: "pg:100", Data: json.RawMessage(`{"phase":"replay"}`)},
	}}, nil
}

type markerEventStream struct {
	events []processruntime.EventEnvelope
	index  int
}

func testFullResourceData(t *testing.T, record *pb.AnalyticsRecord) json.RawMessage {
	t.Helper()
	data := testFullResourceDataNoTest(record)
	require.NotNil(t, data)
	return data
}

func testFullResourceDataNoTest(record *pb.AnalyticsRecord) json.RawMessage {
	resource := map[string]any{}
	switch value := record.GetRecord().(type) {
	case *pb.AnalyticsRecord_Conversation:
		item := value.Conversation
		createdAt, updatedAt := time.Unix(0, 0).UTC(), time.Unix(0, 0).UTC()
		if item.GetCreatedAt() != nil {
			createdAt = item.GetCreatedAt().AsTime().UTC()
		}
		if item.GetUpdatedAt() != nil {
			updatedAt = item.GetUpdatedAt().AsTime().UTC()
		}
		resource = map[string]any{
			"id": item.GetId(), "conversationGroupId": item.GetConversationGroupId(),
			"ownerUserId": item.GetOwnerUserId(), "clientId": item.GetClientId(), "metadata": map[string]any{},
			"createdAt": createdAt, "updatedAt": updatedAt,
			"archived": item.GetArchivedAt() != nil, "accessLevel": "owner",
		}
		if item.ForkedAtConversationId != nil {
			resource["forkedAtConversationId"] = item.GetForkedAtConversationId()
		}
		if item.ForkedAtEntryId != nil {
			resource["forkedAtEntryId"] = item.GetForkedAtEntryId()
		}
	case *pb.AnalyticsRecord_Entry:
		item := value.Entry
		createdAt := time.Unix(0, 0).UTC()
		if item.GetCreatedAt() != nil {
			createdAt = item.GetCreatedAt().AsTime().UTC()
		}
		resource = map[string]any{
			"id": item.GetId(), "conversationId": item.GetConversationId(), "conversationGroupId": item.GetConversationGroupId(),
			"channel": strings.ToLower(item.GetChannel().String()), "contentType": item.GetContentType(),
			"content": []any{}, "createdAt": createdAt,
		}
		if item.GetContent() != nil {
			resource["content"] = item.GetContent().AsInterface()
		}
		if item.IndexedContent != nil {
			resource["indexedContent"] = item.GetIndexedContent()
		}
		if item.IndexedAt != nil {
			resource["indexedAt"] = item.GetIndexedAt().AsTime().UTC()
		}
	case *pb.AnalyticsRecord_Memory:
		item := value.Memory
		resource = map[string]any{
			"id": item.GetId(), "namespace": item.GetNamespace(), "key": item.GetKey(), "kind": item.GetMemoryKind(),
			"revision": item.GetRevision(), "createdAt": item.GetCreatedAt().AsTime().UTC(), "archived": item.GetArchivedAt() != nil,
		}
	}
	data, _ := json.Marshal(resource)
	return data
}

func (s *markerEventStream) Recv() (processruntime.EventEnvelope, error) {
	if s.index >= len(s.events) {
		return processruntime.EventEnvelope{}, errors.New("unexpected receive after test events")
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func TestProcessorSeparatesMultiRowSnapshots(t *testing.T) {
	ctx := context.Background()
	for _, backfill := range []bool{false, true} {
		name := "live"
		if backfill {
			name = "backfill"
		}
		t.Run(name, func(t *testing.T) {
			sink := &fakeSink{err: errors.New("retry insert"), failures: 1}
			cfg := testConfig()
			p, err := NewProcessor(cfg, sink)
			require.NoError(t, err)
			p.projections.Items = []*Projection{testMultiRowProjection(t, `output := [{"step": step} | some step in input.content]`)}
			require.NoError(t, p.SetLeaseGeneration(1))
			payloads := []json.RawMessage{
				json.RawMessage(`{"id":"entry-1","conversationId":"c1","contentType":"support-steps/v1","content":["plan","act","check"]}`),
				json.RawMessage(`{"id":"entry-1","conversationId":"c1","contentType":"support-steps/v1","content":["resolve"]}`),
			}
			if backfill {
				var records []*pb.AnalyticsRecord
				for _, payload := range payloads {
					record, err := analyticsRecordFromResource("entry", payload)
					require.NoError(t, err)
					records = append(records, record)
				}
				require.NoError(t, p.writeBackfillPage(ctx, "entry", records))
			} else {
				require.NoError(t, p.Handle(ctx, processruntime.EventEnvelope{Kind: "entry", Event: "updated", Cursor: "first", Data: payloads[0]}))
				require.NoError(t, p.Handle(ctx, processruntime.EventEnvelope{Kind: "entry", Event: "updated", Cursor: "second", Data: payloads[1]}))
				require.Equal(t, "first", p.state.SafeCursor, "only the flushed snapshot may advance the checkpoint")
				require.NoError(t, p.Flush(ctx))
				require.Equal(t, "second", p.state.SafeCursor)
			}
			require.Len(t, sink.batches, 2, "distinct snapshots must not share a ReplacingMergeTree version")
			require.Len(t, sink.batches[0].Projections, 3)
			require.Len(t, sink.batches[1].Projections, 1)
			require.Greater(t, sink.batches[1].Projections[0].IngestVersion, sink.batches[0].Projections[0].IngestVersion)
		})
	}
}
