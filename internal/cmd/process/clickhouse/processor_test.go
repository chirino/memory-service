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
	"fmt"
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
func (s *fakeSink) ResolvePurge(_ context.Context, p PurgeRow) (PurgeRow, error) { return p, nil }
func (s *fakeSink) Close() error                                                 { return nil }

func testConfig() Config {
	return Config{ExporterID: "test", Addresses: []string{"localhost:9000"}, BatchEvents: 10, BatchRows: 20, BatchBytes: 1 << 20, RetryMin: 100 * time.Millisecond, RetryMax: 100 * time.Millisecond}
}

func TestStableEventIDs(t *testing.T) {
	require.Equal(t, eventID("e", "c", "entry", "created", "created"), eventID("e", "c", "entry", "created", "created"))
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
	require.Equal(t, []string{"conversation", "entry", "memory", "membership"}, cfg.subscriptionKinds())

	cfg.Disable = []string{"conversations"}
	require.Equal(t, []string{"entry", "memory"}, cfg.resourceKinds())
	require.Equal(t, []string{"conversation", "entry", "memory", "membership"}, cfg.subscriptionKinds(), "conversation deletes remain subscribed for dependent entry purges")

	cfg.Disable = []string{"conversations", "entries"}
	require.Equal(t, []string{"memory"}, cfg.resourceKinds())
	require.Equal(t, []string{"conversation", "entry", "memory", "membership"}, cfg.subscriptionKinds(), "hard deletes remain subscribed for rows written before an output was disabled")

	cfg.PurgeMode = PurgeExternal
	require.Equal(t, []string{"memory"}, cfg.subscriptionKinds())

	cfg.Disable = []string{"conversations", "entries", "memories"}
	require.ErrorContains(t, cfg.Validate(), "at least one ClickHouse resource kind")
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
    output := {"required": object.get(input.record.content, "missing", null)}
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
	record := &pb.AnalyticsRecord{Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: "failure-entry"}, SnapshotAvailable: true, Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{Id: "failure-entry", ConversationId: "conversation-1", ConversationGroupId: testGroup, ContentType: "failure/v1", CreatedAt: timestamppb.Now(), Content: content}}}
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
			"channel": "history", "contentType": item.GetContentType(),
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
		if item.GetValue() != nil {
			resource["value"] = item.GetValue().AsMap()
		}
		if item.GetAttributes() != nil {
			resource["attributes"] = item.GetAttributes().AsMap()
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

func TestProcessorWritesEveryChainManifestToTheSharedTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projections.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{testChainRoot, testChainReuse, testChainReplace}, "---\n")), 0o600))
	cfg := testConfig()
	cfg.PayloadMode = PayloadProjected
	cfg.ProjectionPaths = []string{path}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(1))
	ctx := context.Background()
	for i, contentType := range []string{"support-ticket/v1", "support-ticket/v2", "support-ticket/v3"} {
		content, err := structpb.NewValue([]any{"plan"})
		require.NoError(t, err)
		id := fmt.Sprintf("entry-%d", i)
		record := &pb.AnalyticsRecord{
			Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: id}, SnapshotAvailable: true,
			Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{Id: id, ConversationId: "conversation-1", ConversationGroupId: testGroup, ContentType: contentType, CreatedAt: timestamppb.New(time.Unix(10, 0)), Content: content}},
		}
		require.NoError(t, p.Handle(ctx, processruntime.EventEnvelope{Event: "created", Kind: "entry", Cursor: fmt.Sprintf("pg:%d", i), Time: time.Unix(10, 0), Data: testFullResourceData(t, record)}))
	}
	require.NoError(t, p.Handle(ctx, processruntime.EventEnvelope{Event: "deleted", Kind: "entry", Cursor: "pg:9", Data: []byte(`{"entry":"entry-1","conversation":"conversation-1","conversation_group":"00000000-0000-0000-0000-000000000001","entry_content_type":"support-ticket/v2"}`)}))
	require.NoError(t, p.Flush(ctx))
	var rows []ProjectionRow
	for _, batch := range sink.batches {
		rows = append(rows, batch.Projections...)
		require.Empty(t, batch.ProjectionFailures)
	}
	require.Len(t, rows, 3, "support-ticket/v3 is not selected by the chain")
	for i, row := range rows {
		require.Equal(t, "support_ticket_steps", row.TableName)
		require.Equal(t, "support_ticket_steps_duration", row.ProjectionName)
		require.Equal(t, []string{"duration_ms", "step"}, row.ColumnNames)
		require.True(t, row.Multi)
		if i < 2 {
			require.Equal(t, []any{uint64(1), "plan"}, row.Values)
		}
	}
	require.Equal(t, "entry-1", rows[2].ResourceID)
	require.True(t, rows[2].IsDeleted, "deleting an entry writes a tombstone to the shared table")
}

func TestProcessorRecordsChainFailuresUnderTheLogicManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projections.yaml")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join([]string{testChainRoot, testChainReuse, testChainReplace}, "---\n")), 0o600))
	cfg := testConfig()
	cfg.PayloadMode = PayloadProjected
	cfg.ProjectionPaths = []string{path}
	sink := &fakeSink{}
	p, err := NewProcessor(cfg, sink)
	require.NoError(t, err)
	require.NoError(t, p.SetLeaseGeneration(1))
	ctx := context.Background()
	for i, contentType := range []string{"support-ticket/v1", "support-ticket/v2"} {
		// A step must be a string, so the replacing logic rejects this content.
		content, err := structpb.NewValue([]any{42})
		require.NoError(t, err)
		id := fmt.Sprintf("entry-%d", i)
		record := &pb.AnalyticsRecord{
			Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: id}, SnapshotAvailable: true,
			Record: &pb.AnalyticsRecord_Entry{Entry: &pb.AnalyticsEntryRecord{Id: id, ConversationId: "conversation-1", ConversationGroupId: testGroup, ContentType: contentType, CreatedAt: timestamppb.New(time.Unix(10, 0)), Content: content}},
		}
		require.NoError(t, p.Handle(ctx, processruntime.EventEnvelope{Event: "created", Kind: "entry", Cursor: fmt.Sprintf("pg:%d", i), Time: time.Unix(10, 0), Data: testFullResourceData(t, record)}))
	}
	require.NoError(t, p.Flush(ctx))
	var failures []ProjectionFailureRow
	for _, batch := range sink.batches {
		require.Empty(t, batch.Projections)
		failures = append(failures, batch.ProjectionFailures...)
	}
	require.Len(t, failures, 2)
	for _, failure := range failures {
		require.Equal(t, "support_ticket_steps_duration", failure.ProjectionName)
		require.Equal(t, "type_mismatch", failure.ErrorCode)
	}
}

const testGroup = "00000000-0000-0000-0000-000000000001"

func bootstrapTestRecords(ctx context.Context, p *Processor, kind string, records []*pb.AnalyticsRecord) error {
	events := []processruntime.EventEnvelope{{Kind: "stream", Event: "phase", Cursor: "test:boundary", Data: json.RawMessage(`{"phase":"snapshot"}`)}}
	for _, record := range records {
		events = append(events, processruntime.EventEnvelope{Kind: kind, Event: "snapshot", Data: testFullResourceDataNoTest(record)})
	}
	events = append(events, processruntime.EventEnvelope{Kind: "stream", Event: "phase", Cursor: "test:boundary", Data: json.RawMessage(`{"phase":"replay"}`)})
	_, err := p.Bootstrap(ctx, &fixedEventClient{events: events}, func(context.Context) error { return nil })
	return err
}
