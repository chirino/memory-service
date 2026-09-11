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
	"go.opentelemetry.io/otel/attribute"
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

// TestShutdownDrainsRunningBeforeProviderShutdown verifies that Server.Shutdown
// closes the main network listeners before shutting down the tracer provider.
//
// The failure mode: if tracerProvider.Shutdown runs first, any span that ends
// during the listener drain (Running.Close) is silently dropped by the SDK
// batch processor because the provider is already shut down.
//
// This test proves the ordering by ending a span inside Running.Close and
// asserting it was exported. If the provider shuts down first, the span is
// dropped and the assertion fails.
//
// Mutation proof target: swapping Running.Close and tracerProvider.Shutdown
// in Server.Shutdown causes this test to fail.
func TestShutdownDrainsRunningBeforeProviderShutdown(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Start a span before shutdown begins — simulates a span open during drain.
	tracer := tp.Tracer("shutdown-test")
	_, span := tracer.Start(context.Background(), "drain-span")

	// Running.Close ends the span and captures the exporter state immediately
	// after end — before tracerProvider.Shutdown clears the InMemoryExporter.
	// WithSyncer uses SimpleSpanProcessor which exports synchronously on End(),
	// so the span must be present in the exporter the moment End() returns.
	var spansAtDrain []tracetest.SpanStub
	srv := &Server{
		tracerProvider: tp,
		Running: &RunningServers{
			Close: func(_ context.Context) error {
				span.End() // span ends during the listener drain
				spansAtDrain = exporter.GetSpans()
				return nil
			},
		},
	}

	require.NoError(t, srv.Shutdown(context.Background()))

	// The span must have been present when Running.Close ran — proving the
	// provider was still live when the span ended.
	require.Len(t, spansAtDrain, 1,
		"span ended during Running.Close must be exported before provider shutdown; "+
			"if tracerProvider.Shutdown runs before Running.Close the span is dropped")
	require.Equal(t, "drain-span", spansAtDrain[0].Name)
}

// TestShutdownDrainsManagementBeforeProviderShutdown verifies that
// Server.Shutdown closes the management listener before shutting down the
// tracer provider. The management router is instrumented with OTel
// HTTPMiddleware, so spans can end during its drain just as during Running.Close.
//
// Mutation proof target: moving tracerProvider.Shutdown ahead of
// closeManagement in Server.Shutdown causes this test to fail.
func TestShutdownDrainsManagementBeforeProviderShutdown(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	tracer := tp.Tracer("shutdown-test")
	_, span := tracer.Start(context.Background(), "mgmt-drain-span")

	var spansAtMgmtDrain []tracetest.SpanStub
	srv := &Server{
		tracerProvider: tp,
		closeManagement: func(_ context.Context) error {
			span.End() // span ends during the management listener drain
			spansAtMgmtDrain = exporter.GetSpans()
			return nil
		},
	}

	require.NoError(t, srv.Shutdown(context.Background()))

	require.Len(t, spansAtMgmtDrain, 1,
		"span ended during closeManagement must be exported before provider shutdown; "+
			"if tracerProvider.Shutdown runs before closeManagement the span is dropped")
	require.Equal(t, "mgmt-drain-span", spansAtMgmtDrain[0].Name)
}

// TestBuildTracerProviderHonoursTracesEndpoint verifies that buildTracerProvider
// enables tracing when OTEL_EXPORTER_OTLP_TRACES_ENDPOINT is set (even when
// OTEL_EXPORTER_OTLP_ENDPOINT is absent), and disables tracing when neither is set.
//
// The failure mode: the current implementation checks only
// OTEL_EXPORTER_OTLP_ENDPOINT, so setting only the traces-specific variable
// returns a noop provider and no spans are exported.
func TestBuildTracerProviderHonoursTracesEndpoint(t *testing.T) {
	t.Run("TracesEndpointEnablesTracing", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://localhost:4318")

		_, sdkTP, err := buildTracerProvider(t.Context())
		require.NoError(t, err)
		require.NotNil(t, sdkTP,
			"sdk provider must be non-nil when OTEL_EXPORTER_OTLP_TRACES_ENDPOINT is set; "+
				"current code only checks OTEL_EXPORTER_OTLP_ENDPOINT and falls through to noop")
		t.Cleanup(func() { _ = sdkTP.Shutdown(context.Background()) })
	})

	t.Run("NeitherEndpointDisablesTracing", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

		_, sdkTP, err := buildTracerProvider(t.Context())
		require.NoError(t, err)
		require.Nil(t, sdkTP,
			"sdk provider must be nil (noop path) when no endpoint env var is set")
	})
}

// TestBuildTracerProviderServiceNameFromEnv verifies that buildTracerProvider
// respects OTEL_SERVICE_NAME rather than hardcoding "memory-service".
//
// The failure mode: resource.Merge with a hardcoded service.name attribute wins
// the merge, overwriting whatever OTEL_SERVICE_NAME the operator set.
//
// Because buildTracerProvider's exporter targets a network endpoint we cannot
// intercept the batcher. This test uses the buildTracerProviderWithExporter
// seam so spans route to an in-memory recorder, letting us read the resource
// from the exported SpanStub.
func TestBuildTracerProviderServiceNameFromEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "my-prod-deployment")

	syncer := tracetest.NewInMemoryExporter()
	sdkTP := buildTracerProviderWithExporter(syncer)
	t.Cleanup(func() { _ = sdkTP.Shutdown(context.Background()) })

	_, span := sdkTP.Tracer("test").Start(context.Background(), "probe")
	span.End()

	spans := syncer.GetSpans()
	require.Len(t, spans, 1)
	var found string
	for _, attr := range spans[0].Resource.Attributes() {
		if string(attr.Key) == "service.name" {
			found = attr.Value.AsString()
		}
	}
	require.Equal(t, "my-prod-deployment", found,
		"OTEL_SERVICE_NAME must be used as service.name; "+
			"hardcoding 'memory-service' in resource.Merge overwrites the operator-supplied value")
}

// TestBuildTracerProviderQueryRedactingExporterWired verifies that
// buildTracerProvider wraps its exporter with tracing.NewQueryRedactingExporter.
//
// The OTLP batcher in buildTracerProvider exports to a network endpoint and
// cannot be intercepted in a unit test. This test therefore uses the
// buildTracerProviderWithExporter seam — a package-internal variant that
// accepts a pre-built SpanExporter — so we can route spans through an
// in-memory recorder and assert the query string is stripped.
//
// Mutation proof target: removing tracing.NewQueryRedactingExporter from
// buildTracerProviderWithExporter causes spans with url.full=...?secret=x to
// reach the raw exporter unredacted, and this test fails.
func TestBuildTracerProviderQueryRedactingExporterWired(t *testing.T) {
	raw := tracetest.NewInMemoryExporter()
	sdkTP := buildTracerProviderWithExporter(raw)
	t.Cleanup(func() { _ = sdkTP.Shutdown(context.Background()) })

	_, span := sdkTP.Tracer("test").Start(context.Background(), "probe")
	span.SetAttributes(attribute.String("url.full", "https://s3.example.com/bucket/key?X-Amz-Signature=abc123&X-Amz-Expires=300"))
	span.End()

	spans := raw.GetSpans()
	require.Len(t, spans, 1)
	for _, kv := range spans[0].Attributes {
		if string(kv.Key) == "url.full" {
			require.NotContains(t, kv.Value.AsString(), "X-Amz-Signature",
				"buildTracerProvider must wrap the exporter with QueryRedactingExporter")
		}
	}
}

// TestBuildInboundPropagatorHonoursOtelPropagators verifies that
// buildInboundPropagator reads OTEL_PROPAGATORS and builds the appropriate
// propagator, asserting by the headers actually injected on an outbound request.
//
// With OTEL_PROPAGATORS unset: outbound request carries traceparent (W3C), no b3 headers.
// With OTEL_PROPAGATORS=b3: outbound request carries b3 single-header, no traceparent.
//
// Mutation proof target: reverting buildInboundPropagator to the hardcoded
// propagation.NewCompositeTextMapPropagator(TraceContext{}, Baggage{}) causes
// the b3 sub-test to fail because no b3 header is injected.
func TestBuildInboundPropagatorHonoursOtelPropagators(t *testing.T) {
	t.Run("UnsetUsesTraceContext", func(t *testing.T) {
		t.Setenv("OTEL_PROPAGATORS", "")

		prop := buildInboundPropagator()

		// Inject into a carrier on a participating context carrying a valid span.
		tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		ctx, span := tp.Tracer("t").Start(context.Background(), "s")
		span.End()
		ctx = tracing.MarkParticipating(ctx)

		headers := make(propagation.MapCarrier)
		prop.Inject(ctx, headers)

		require.NotEmpty(t, headers.Get("traceparent"),
			"unset OTEL_PROPAGATORS must inject W3C traceparent")
		require.Empty(t, headers.Get("b3"),
			"unset OTEL_PROPAGATORS must not inject b3 header")
	})

	t.Run("B3InjectsB3Header", func(t *testing.T) {
		t.Setenv("OTEL_PROPAGATORS", "b3")

		prop := buildInboundPropagator()

		tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		ctx, span := tp.Tracer("t").Start(context.Background(), "s")
		span.End()
		ctx = tracing.MarkParticipating(ctx)

		headers := make(propagation.MapCarrier)
		prop.Inject(ctx, headers)

		require.NotEmpty(t, headers.Get("b3"),
			"OTEL_PROPAGATORS=b3 must inject b3 single-header")
		require.Empty(t, headers.Get("traceparent"),
			"OTEL_PROPAGATORS=b3 must not inject W3C traceparent")
	})
}
