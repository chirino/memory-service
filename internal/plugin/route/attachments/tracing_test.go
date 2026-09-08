package attachments_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/chirino/memory-service/internal/plugin/route/attachments"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

// These tests verify that the transport stack used by the attachment
// source-URL download client propagates traceparent correctly.
// They build the same otelhttp+newSourceURLTransport stack that
// completeSourceURLAttachment builds, then send a request to a
// DownstreamRecorder and assert on the outgoing headers.

func TestAttachmentSourceURLUntracedRequestNoTraceparent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	participatingProp := tracing.NewParticipatingPropagator(propagation.TraceContext{})
	client := &http.Client{
		Transport: otelhttp.NewTransport(
			attachments.NewSourceURLTransportForTest(true /*allowPrivate*/),
			otelhttp.WithTracerProvider(nooptrace.NewTracerProvider()),
			otelhttp.WithPropagators(participatingProp),
		),
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, downstream.Server.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Equal(t, 1, downstream.CallCount(), "server must be reached exactly once")
	require.Empty(t, downstream.LastHeader().Get("Traceparent"),
		"untraced request must not inject traceparent")
}

func TestAttachmentSourceURLTracedRequestInjectsTraceparent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	h := testutil.NewTestHarness()
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })

	participatingProp := tracing.NewParticipatingPropagator(propagation.TraceContext{})
	client := &http.Client{
		Transport: otelhttp.NewTransport(
			attachments.NewSourceURLTransportForTest(true),
			otelhttp.WithTracerProvider(h.Provider),
			otelhttp.WithPropagators(participatingProp),
		),
	}

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inbound := testutil.NewSampledTraceparent(traceID, parentSpanID)

	extractedCtx := h.Propagator.Extract(context.Background(),
		staticHTTPCarrier{"traceparent": inbound})
	ctx := tracing.MarkParticipating(extractedCtx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downstream.Server.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Equal(t, 1, downstream.CallCount(), "server must be reached exactly once")
	outbound := downstream.LastHeader().Get("Traceparent")
	require.NotEmpty(t, outbound, "traced request must inject outbound traceparent")
	require.Contains(t, outbound, traceID, "outbound traceparent must carry the caller's trace ID")
}

type staticHTTPCarrier map[string]string

func (c staticHTTPCarrier) Get(key string) string { return c[key] }
func (c staticHTTPCarrier) Set(_, _ string)        {}
func (c staticHTTPCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
