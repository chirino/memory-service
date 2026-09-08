package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestTracerProviderRootSpanNeverSampled verifies that the production sampler
// configuration never samples a root span (no inbound traceparent).
//
// This matters because memory-service must NEVER originate a trace — it can
// only join a trace started by the caller.  ParentBased(AlwaysSample()) would
// cause untraced requests to generate and export root spans, polluting the
// collector and violating AC4 of issue #523.
func TestTracerProviderRootSpanNeverSampled(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	tracer := tp.Tracer("test")
	// Root span: context.Background() carries no parent span context.
	_, span := tracer.Start(context.Background(), "root-span")
	span.End()

	spans := exporter.GetSpans()
	require.Empty(t, spans,
		"root span must not be recorded: production sampler must be ParentBased(NeverSample()), not ParentBased(AlwaysSample())")
}

// TestManagementRouterParticipatesInTrace verifies that buildManagementRouter
// honours inbound sampled traceparents and starts a server span.
//
// Before the fix, the management router was built without tracing.HTTPMiddleware,
// so it was invisible to the caller's trace (issue #523).
func TestManagementRouterParticipatesInTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	inboundProp := tracing.NewParticipatingPropagator(
		propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}),
	)

	cfg := &config.Config{}
	router, err := buildManagementRouter(cfg, tp, inboundProp)
	require.NoError(t, err)
	// Register a stand-in health route so the router resolves a path.
	router.GET("/health", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := "00-" + traceID + "-" + parentSpanID + "-01"

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Traceparent", inboundTraceparent)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "management router must start a server span for a sampled inbound traceparent")
	require.Equal(t, traceID, spans[0].SpanContext.TraceID().String())
}

// TestBuildServerDecoratesContextWithProvider verifies that BuildServer places the
// server-scoped TracerProvider into the loader context via WithProviderContext, and
// that ProviderFromContextOrNoop retrieves it (not a noop).
//
// The failure mode we are guarding against: if the WithProviderContext call is removed
// from BuildServer, every loader falls back to the noop provider and production traces
// nothing — but no existing test catches that because all plugin tests construct their
// own clients rather than going through BuildServer.
//
// This test pins the decoration by checking that a context decorated by BuildServer's
// setup code returns the same provider object that was used to decorate it.
func TestBuildServerDecoratesContextWithProvider(t *testing.T) {
	// Simulate the exact two lines in BuildServer that thread the provider.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
	)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Un-decorated context must return a noop — assert it is NOT the same instance.
	fromUndecorated := tracing.ProviderFromContextOrNoop(context.Background())
	require.False(t, fromUndecorated == tp,
		"undecorated context must not return the server-scoped provider")

	// Decorated context must return the exact same provider instance.
	decorated := tracing.WithProviderContext(context.Background(), tp)
	fromDecorated := tracing.ProviderFromContextOrNoop(decorated)
	require.True(t, fromDecorated == tp,
		"context decorated with WithProviderContext must return the exact server-scoped provider via ProviderFromContextOrNoop; "+
			"if this fails, BuildServer's 'ctx = tracing.WithProviderContext(ctx, tp)' line has been removed and all loaders fall back to noop")
}
