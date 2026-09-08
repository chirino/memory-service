package tracing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/tracing"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// sampledTraceparent is a fixed W3C traceparent with sampled=01.
const sampledTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// sampledCtx returns a context carrying a sampled remote parent via the given
// propagator. ParentBased(NeverSample()) creates child spans only when the
// remote parent is sampled; a locally started root span is rejected.
func sampledCtx(prop propagation.TextMapPropagator) context.Context {
	carrier := propagation.MapCarrier{}
	carrier.Set("traceparent", sampledTraceparent)
	ctx := prop.Extract(context.Background(), carrier)
	return tracing.MarkParticipating(ctx)
}

// newTestPropagator returns a ParticipatingPropagator backed by W3C TraceContext,
// matching the production configuration.
func newTestPropagator() propagation.TextMapPropagator {
	base := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	return tracing.NewParticipatingPropagator(base)
}

// TestQueryRedactingExporterStripsURLFullQuery verifies that the
// QueryRedactingExporter removes RawQuery from the url.full span attribute
// while leaving the outbound HTTP request URL unchanged.
//
// Mutation proof target: removing tracing.NewQueryRedactingExporter wrapping
// in provider construction causes url.full to carry the raw query, failing
// the "must not contain secret" assertion.
func TestQueryRedactingExporterStripsURLFullQuery(t *testing.T) {
	// downstream records the exact URL the HTTP client used
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(downstream.Close)

	rawExporter := tracetest.NewInMemoryExporter()
	// Wrap the raw exporter with the sanitising wrapper — this is the production
	// composition point. Issue 5 rewrites buildTracerProvider; it must preserve
	// this wrapper around whatever exporter it constructs.
	sanitisingExporter := tracing.NewQueryRedactingExporter(rawExporter)

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(sanitisingExporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	prop := newTestPropagator()

	// Build an otelhttp client backed by the server provider — the same stack
	// as completeSourceURLAttachment.
	client := &http.Client{
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithTracerProvider(provider),
			otelhttp.WithPropagators(prop),
		),
	}

	// Construct a URL with a secret query parameter (simulates a presigned URL).
	secretURL := downstream.URL + "/download?sig=supersecret&expires=9999"

	ctx := sampledCtx(prop)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	// Force-flush so the syncer has received all spans.
	require.NoError(t, provider.ForceFlush(context.Background()))

	spans := rawExporter.GetSpans()
	require.NotEmpty(t, spans, "at least one span must have been exported")

	// Find the client span.
	var clientSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].SpanKind == trace.SpanKindClient {
			clientSpan = &spans[i]
			break
		}
	}
	require.NotNil(t, clientSpan, "otelhttp must have created a client span")

	// Find url.full in the client span attributes.
	urlFull := ""
	for _, kv := range clientSpan.Attributes {
		if string(kv.Key) == "url.full" {
			urlFull = kv.Value.AsString()
			break
		}
	}
	require.NotEmpty(t, urlFull, "url.full attribute must be present on the client span")

	// The exported span must not contain the secret query parameter.
	require.NotContains(t, urlFull, "supersecret",
		"url.full must not carry query parameters (presigned URL secret would be exposed)")
	require.NotContains(t, urlFull, "?",
		"url.full must have RawQuery stripped entirely")

	// The exported URL must still carry the correct path and host.
	require.Contains(t, urlFull, "/download",
		"url.full must retain the path after query stripping")

	// Verify the real request reached the downstream server with the full URL.
	// The otelhttp transport must not modify the actual outbound request.
	// We confirm this by checking that the downstream server received a request
	// to the path including the query string (via the request URI).
	// We check this indirectly: if the downstream server was called (resp above is
	// 200), the real URL was used. But we want an explicit assertion on the
	// query presence — use a recording server instead.
}

// TestQueryRedactingExporterRealURLReachesDownstream verifies explicitly that
// the outbound HTTP request carries the original URL with query intact, while
// the exported span has it stripped. This is the complement to the assertion
// above: the wrapper must not alter the transport layer, only the export layer.
func TestQueryRedactingExporterRealURLReachesDownstream(t *testing.T) {
	var receivedQuery string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(downstream.Close)

	rawExporter := tracetest.NewInMemoryExporter()
	sanitisingExporter := tracing.NewQueryRedactingExporter(rawExporter)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(sanitisingExporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	prop := newTestPropagator()
	client := &http.Client{
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithTracerProvider(provider),
			otelhttp.WithPropagators(prop),
		),
	}

	ctx := sampledCtx(prop)
	secretURL := downstream.URL + "/download?sig=supersecret&expires=9999"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, provider.ForceFlush(context.Background()))

	// The real HTTP request must carry the full query string unchanged.
	require.True(t, strings.Contains(receivedQuery, "sig=supersecret"),
		"outbound HTTP request must carry the original query string (got %q)", receivedQuery)

	// The exported span must have url.full stripped of query.
	spans := rawExporter.GetSpans()
	var urlFull string
	for _, s := range spans {
		if s.SpanKind == trace.SpanKindClient {
			for _, kv := range s.Attributes {
				if string(kv.Key) == "url.full" {
					urlFull = kv.Value.AsString()
				}
			}
		}
	}
	require.NotEmpty(t, urlFull)
	require.NotContains(t, urlFull, "supersecret",
		"url.full in exported span must not contain the secret query parameter")
}

// TestQueryRedactingExporterNoopWhenNoQuery verifies that url.full is left
// unchanged when there is no query string — no spurious truncation.
func TestQueryRedactingExporterNoopWhenNoQuery(t *testing.T) {
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(downstream.Close)

	rawExporter := tracetest.NewInMemoryExporter()
	sanitisingExporter := tracing.NewQueryRedactingExporter(rawExporter)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(sanitisingExporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	prop := newTestPropagator()
	client := &http.Client{
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithTracerProvider(provider),
			otelhttp.WithPropagators(prop),
		),
	}

	ctx := sampledCtx(prop)
	noQueryURL := downstream.URL + "/download"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, noQueryURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, provider.ForceFlush(context.Background()))

	spans := rawExporter.GetSpans()
	var urlFull string
	for _, s := range spans {
		if s.SpanKind == trace.SpanKindClient {
			for _, kv := range s.Attributes {
				if string(kv.Key) == "url.full" {
					urlFull = kv.Value.AsString()
				}
			}
		}
	}
	require.NotEmpty(t, urlFull)
	require.Contains(t, urlFull, "/download",
		"url.full must be unchanged when there is no query string")
	require.NotContains(t, urlFull, "?",
		"url.full must not gain a trailing '?' when query was already absent")
}
