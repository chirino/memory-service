package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/plugin/route/admin"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const samplePrometheusSuccessJSON = `{
	"status": "success",
	"data": {
		"resultType": "matrix",
		"result": [
			{
				"metric": {},
				"values": [
					[1700000000, "12.5"]
				]
			}
		]
	}
}`

func setupAdminStatsRouter(harness *testutil.Harness, cfg *config.Config) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(tracing.HTTPMiddleware(harness.Provider, harness.Propagator, harness.Propagator))
	router.GET("/v1/admin/stats/request-rate", func(c *gin.Context) {
		c.Set(security.ContextKeyRoles, map[string]bool{
			security.RoleAuditor: true,
		})
		admin.HandleAdminStatsRequestRate(c, cfg)
	})
	return router
}

// P1 — TestPrometheusStatsPropagatesTraceparent
// Assert that when an inbound request carries a sampled traceparent, the outbound
// Prometheus HTTP request carries a traceparent header containing the same trace ID.
func TestPrometheusStatsPropagatesTraceparent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorderWithBody(samplePrometheusSuccessJSON)
	t.Cleanup(downstream.Close)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := &config.Config{
		PrometheusURL: downstream.Server.URL,
	}

	router := setupAdminStatsRouter(harness, cfg)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/stats/request-rate", nil)
	req.Header.Set("Traceparent", inboundTraceparent)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, downstream.CallCount(), "Prometheus server must be called once")

	lastHeader := downstream.LastHeader()
	require.NotNil(t, lastHeader)
	outbound := lastHeader.Get("Traceparent")
	require.NotEmpty(t, outbound, "outbound request to Prometheus must carry traceparent")
	require.Contains(t, outbound, traceID, "outbound traceparent must contain parent trace ID")
}

// P2 — TestPrometheusStatsChildSpanRecorded
// Assert that a SpanKindClient span is recorded in the in-memory exporter and shares the parent's trace ID.
func TestPrometheusStatsChildSpanRecorded(t *testing.T) {
	downstream := testutil.NewDownstreamRecorderWithBody(samplePrometheusSuccessJSON)
	t.Cleanup(downstream.Close)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := &config.Config{
		PrometheusURL: downstream.Server.URL,
	}

	router := setupAdminStatsRouter(harness, cfg)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/stats/request-rate", nil)
	req.Header.Set("Traceparent", inboundTraceparent)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	_ = harness.Provider.ForceFlush(context.Background())

	spans := harness.Exporter.GetSpans()
	var clientSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].SpanKind == trace.SpanKindClient {
			clientSpan = &spans[i]
			break
		}
	}

	require.NotNil(t, clientSpan, "expected a SpanKindClient span for the outbound Prometheus call")
	require.Equal(t, traceID, clientSpan.SpanContext.TraceID().String())
	require.Equal(t, trace.SpanKindClient, clientSpan.SpanKind, "span kind must be client")

	// Find the server span to verify the client span is a child of the server span
	var serverSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].SpanKind == trace.SpanKindServer {
			serverSpan = &spans[i]
			break
		}
	}
	if serverSpan != nil {
		require.Equal(t, serverSpan.SpanContext.SpanID().String(), clientSpan.Parent.SpanID().String(),
			"outbound HTTP client span must be a child of the server span")
		require.Equal(t, parentSpanID, serverSpan.Parent.SpanID().String(),
			"server span parent must match inbound span ID")
	} else {
		require.Equal(t, parentSpanID, clientSpan.Parent.SpanID().String(),
			"client span parent must match inbound span ID")
	}
}

// P3 — TestPrometheusStatsNoSpanWithoutParent
// Assert that with no inbound traceparent, zero spans are exported and no Traceparent header appears outbound.
func TestPrometheusStatsNoSpanWithoutParent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorderWithBody(samplePrometheusSuccessJSON)
	t.Cleanup(downstream.Close)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := &config.Config{
		PrometheusURL: downstream.Server.URL,
	}

	router := setupAdminStatsRouter(harness, cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/stats/request-rate", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, downstream.CallCount())

	_ = harness.Provider.ForceFlush(context.Background())
	spans := harness.Exporter.GetSpans()
	require.Empty(t, spans, "no spans should be exported without parent traceparent")

	lastHeader := downstream.LastHeader()
	require.NotNil(t, lastHeader)
	require.Empty(t, lastHeader.Get("Traceparent"), "no outbound traceparent should be present")
}

// P4 — TestPrometheusStatsUnsampledParent
// Assert that an unsampled inbound traceparent (flags 00) causes no recorded span,
// but the Traceparent header IS forwarded on the outbound request.
func TestPrometheusStatsUnsampledParent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorderWithBody(samplePrometheusSuccessJSON)
	t.Cleanup(downstream.Close)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := &config.Config{
		PrometheusURL: downstream.Server.URL,
	}

	router := setupAdminStatsRouter(harness, cfg)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewUnsampledTraceparent(traceID, parentSpanID)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/stats/request-rate", nil)
	req.Header.Set("Traceparent", inboundTraceparent)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, downstream.CallCount())

	_ = harness.Provider.ForceFlush(context.Background())
	spans := harness.Exporter.GetSpans()
	require.Empty(t, spans, "no spans should be recorded for unsampled parent")

	lastHeader := downstream.LastHeader()
	require.NotNil(t, lastHeader)
	outbound := lastHeader.Get("Traceparent")
	require.NotEmpty(t, outbound, "traceparent should be forwarded even if unsampled")
	require.Contains(t, outbound, traceID)
}

// P5 — TestPrometheusStatsErrorSpanStatus
// Assert that when the fake Prometheus server returns HTTP 500, the recorded SpanKindClient span has StatusCode == codes.Error.
func TestPrometheusStatsErrorSpanStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","error":"internal server error"}`))
	}))
	t.Cleanup(server.Close)

	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	cfg := &config.Config{
		PrometheusURL: server.URL,
	}

	router := setupAdminStatsRouter(harness, cfg)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/stats/request-rate", nil)
	req.Header.Set("Traceparent", inboundTraceparent)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	_ = harness.Provider.ForceFlush(context.Background())
	spans := harness.Exporter.GetSpans()

	var clientSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].SpanKind == trace.SpanKindClient {
			clientSpan = &spans[i]
			break
		}
	}

	require.NotNil(t, clientSpan, "expected a SpanKindClient span for outbound Prometheus call")
	require.Equal(t, trace.SpanKindClient, clientSpan.SpanKind, "span kind must be client")
	require.Equal(t, codes.Error, clientSpan.Status.Code, "client span should have error status on HTTP 500")

	// Find the server span to verify parent-child hierarchy
	var serverSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].SpanKind == trace.SpanKindServer {
			serverSpan = &spans[i]
			break
		}
	}
	if serverSpan != nil {
		require.Equal(t, serverSpan.SpanContext.SpanID().String(), clientSpan.Parent.SpanID().String(),
			"outbound HTTP client span must be a child of the server span")
		require.Equal(t, parentSpanID, serverSpan.Parent.SpanID().String(),
			"server span parent must match inbound span ID")
	} else {
		require.Equal(t, parentSpanID, clientSpan.Parent.SpanID().String(),
			"client span parent must match inbound span ID")
	}
}
