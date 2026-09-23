//go:build !nopostgresql

package postgres_test

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/knowledge"
	_ "github.com/chirino/memory-service/internal/plugin/attach/pgstore"
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	_ "github.com/chirino/memory-service/internal/plugin/vector/pgvector"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// G1 — TestGORMQueryProducesChildSpan
// Assert that a GORM query executed under a sampled parent context records a
// db.system = postgresql span as a child of the parent span (same trace ID), with SpanKindClient.
func TestGORMQueryProducesChildSpan(t *testing.T) {
	dbURL := testpg.StartPostgres(t)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL

	// Provide harness.Provider on loader context
	loaderCtx := tracing.WithProviderContext(context.Background(), harness.Provider)
	loaderCtx = config.WithContext(loaderCtx, &cfg)

	_ = postgres.ForceImport
	require.NoError(t, registrymigrate.RunAll(loaderCtx))

	loader, err := registrystore.Select("postgres")
	require.NoError(t, err)

	store, err := loader(loaderCtx)
	require.NoError(t, err)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	extractedCtx := harness.Propagator.Extract(context.Background(), staticHTTPCarrier{"traceparent": inboundTraceparent})
	reqCtx := tracing.MarkParticipating(extractedCtx)

	_, err = store.CreateConversation(reqCtx, "user1", "client1", "Test Conversation", nil, nil, nil, nil)
	require.NoError(t, err)

	_ = harness.Provider.ForceFlush(context.Background())
	spans := harness.Exporter.GetSpans()

	require.NotEmpty(t, spans, "spans must not be empty")
	var gormSpan *tracetest.SpanStub
	for i := range spans {
		for _, attr := range spans[i].Attributes {
			if string(attr.Key) == "db.system" || string(attr.Key) == "db.system.name" {
				gormSpan = &spans[i]
				break
			}
		}
		if gormSpan != nil {
			break
		}
	}

	require.NotNil(t, gormSpan, "expected a db span from GORM")
	require.Equal(t, traceID, gormSpan.SpanContext.TraceID().String())
}

// G2 — TestGORMNoSpanWithoutParent
// Assert that with context.Background() (no parent) zero spans are exported.
func TestGORMNoSpanWithoutParent(t *testing.T) {
	dbURL := testpg.StartPostgres(t)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL

	loaderCtx := tracing.WithProviderContext(context.Background(), harness.Provider)
	loaderCtx = config.WithContext(loaderCtx, &cfg)

	_ = postgres.ForceImport
	require.NoError(t, registrymigrate.RunAll(loaderCtx))

	loader, err := registrystore.Select("postgres")
	require.NoError(t, err)

	store, err := loader(loaderCtx)
	require.NoError(t, err)

	_, err = store.CreateConversation(context.Background(), "user1", "client1", "Untraced Conversation", nil, nil, nil, nil)
	require.NoError(t, err)

	_ = harness.Provider.ForceFlush(context.Background())
	spans := harness.Exporter.GetSpans()
	require.Empty(t, spans, "no spans should be exported when parent context is untraced")
}

// G3 — TestGORMUnsampledParent
// Assert that with an unsampled parent context, zero spans are exported.
func TestGORMUnsampledParent(t *testing.T) {
	dbURL := testpg.StartPostgres(t)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL

	loaderCtx := tracing.WithProviderContext(context.Background(), harness.Provider)
	loaderCtx = config.WithContext(loaderCtx, &cfg)

	_ = postgres.ForceImport
	require.NoError(t, registrymigrate.RunAll(loaderCtx))

	loader, err := registrystore.Select("postgres")
	require.NoError(t, err)

	store, err := loader(loaderCtx)
	require.NoError(t, err)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewUnsampledTraceparent(traceID, parentSpanID)

	extractedCtx := harness.Propagator.Extract(context.Background(), staticHTTPCarrier{"traceparent": inboundTraceparent})
	reqCtx := tracing.MarkParticipating(extractedCtx)

	_, err = store.CreateConversation(reqCtx, "user1", "client1", "Unsampled Conversation", nil, nil, nil, nil)
	require.NoError(t, err)

	_ = harness.Provider.ForceFlush(context.Background())
	spans := harness.Exporter.GetSpans()
	require.Empty(t, spans, "no spans should be exported for unsampled parent")
}

// G4 — TestGORMNoQueryVariables
// Assert that db.statement span attributes do not contain bind parameter values.
func TestGORMNoQueryVariables(t *testing.T) {
	dbURL := testpg.StartPostgres(t)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL

	loaderCtx := tracing.WithProviderContext(context.Background(), harness.Provider)
	loaderCtx = config.WithContext(loaderCtx, &cfg)

	_ = postgres.ForceImport
	require.NoError(t, registrymigrate.RunAll(loaderCtx))

	loader, err := registrystore.Select("postgres")
	require.NoError(t, err)

	store, err := loader(loaderCtx)
	require.NoError(t, err)

	secretParam := "super-secret-unique-title-12345"
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	extractedCtx := harness.Propagator.Extract(context.Background(), staticHTTPCarrier{"traceparent": inboundTraceparent})
	reqCtx := tracing.MarkParticipating(extractedCtx)

	conv, err := store.CreateConversation(reqCtx, "user1", "client1", secretParam, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, conv)

	_ = harness.Provider.ForceFlush(context.Background())
	spans := harness.Exporter.GetSpans()
	require.NotEmpty(t, spans, "expected spans from GORM operations")

	for _, span := range spans {
		for _, attr := range span.Attributes {
			require.NotContains(t, attr.Value.AsString(), secretParam,
				"span attribute %s must not contain query bind parameter value", attr.Key)
		}
	}
}

// G5 — TestGORMTracingAllPostgresHandles
// Exercises the remaining 4 PostgreSQL GORM handles: episodic store, pgvector, pgstore (attachments), and knowledge store.
func TestGORMTracingAllPostgresHandles(t *testing.T) {
	dbURL := testpg.StartPostgres(t)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.DatastoreType = "postgres"
	cfg.VectorType = "pgvector"
	cfg.VectorMigrateAtStart = true

	loaderCtx := tracing.WithProviderContext(context.Background(), harness.Provider)
	loaderCtx = config.WithContext(loaderCtx, &cfg)

	_ = postgres.ForceImport
	require.NoError(t, registrymigrate.RunAll(loaderCtx))

	// 1. Episodic store
	epLoader, err := registryepisodic.Select("postgres")
	require.NoError(t, err)
	epStore, err := epLoader(loaderCtx)
	require.NoError(t, err)

	// 2. pgvector
	vecLoader, err := registryvector.Select("pgvector")
	require.NoError(t, err)
	vecStore, err := vecLoader(loaderCtx)
	require.NoError(t, err)

	// 3. pgstore attachment
	attLoader, err := registryattach.Select("postgres")
	require.NoError(t, err)
	attStore, err := attLoader(loaderCtx)
	require.NoError(t, err)

	// 4. Knowledge store
	knowStore, err := knowledge.OpenPostgresKnowledgeStore(loaderCtx, dbURL)
	require.NoError(t, err)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	extractedCtx := harness.Propagator.Extract(context.Background(), staticHTTPCarrier{"traceparent": inboundTraceparent})
	reqCtx := tracing.MarkParticipating(extractedCtx)

	// Exercise episodic store
	_, _, err = epStore.GetMemoryRowKind(reqCtx, []string{"ns1"}, "key1", registryepisodic.ArchiveFilterExclude)
	require.NoError(t, err)

	// Exercise pgvector
	err = vecStore.DeleteByConversationGroupID(reqCtx, uuid.New())
	require.NoError(t, err)

	// Exercise attachment store
	_, err = attStore.Retrieve(reqCtx, "nonexistent-attachment")
	require.Error(t, err) // Expected not found error, but GORM executed

	// Exercise knowledge store
	_, err = knowStore.ListUsersWithEmbeddings(reqCtx)
	require.NoError(t, err)

	_ = harness.Provider.ForceFlush(context.Background())
	spans := harness.Exporter.GetSpans()
	// At least 4 spans, one from each handle operation
	require.GreaterOrEqual(t, len(spans), 4, "expected at least 4 spans, one from each postgres handle")

	for _, span := range spans {
		require.Equal(t, traceID, span.SpanContext.TraceID().String())
	}
}

type staticHTTPCarrier map[string]string

func (c staticHTTPCarrier) Get(key string) string {
	return c[key]
}

func (c staticHTTPCarrier) Set(key, val string) {
	c[key] = val
}

func (c staticHTTPCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
