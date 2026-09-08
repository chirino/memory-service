package infinispan

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/stretchr/testify/require"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

// TestInfinispanUntracedRequestNoTraceparent verifies that an untraced context
// (no MarkParticipating) does not inject a traceparent header into outbound
// Infinispan HTTP requests.
//
// The client is obtained from newInfinispanClient — the production constructor —
// so removing otelhttp.NewTransport from that constructor causes this test to fail.
func TestInfinispanUntracedRequestNoTraceparent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	cfg := &config.Config{InfinispanVectorURL: downstream.Server.URL}
	client, err := newInfinispanClient(cfg, nooptrace.NewTracerProvider())
	require.NoError(t, err)

	_, _ = client.Search(context.Background(), "cache", "from VectorItem1536 o where o.dummy = 1", 1)

	require.Equal(t, 1, downstream.CallCount(), "downstream must be called exactly once")
	require.Empty(t, downstream.LastHeader().Get("Traceparent"),
		"untraced request must not inject traceparent")
}

// TestInfinispanTracedRequestInjectsTraceparent verifies that a participating
// context causes the outbound Infinispan HTTP request to carry a traceparent
// header containing the caller's trace ID.
//
// The client is obtained from newInfinispanClient — the production constructor —
// so removing otelhttp.NewTransport from that constructor causes this test to fail.
func TestInfinispanTracedRequestInjectsTraceparent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	h := testutil.NewTestHarness()
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })

	cfg := &config.Config{InfinispanVectorURL: downstream.Server.URL}
	client, err := newInfinispanClient(cfg, h.Provider)
	require.NoError(t, err)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inbound := testutil.NewSampledTraceparent(traceID, parentSpanID)

	extractedCtx := h.Propagator.Extract(context.Background(),
		staticStringCarrier{"traceparent": inbound})
	ctx := tracing.MarkParticipating(extractedCtx)

	_, _ = client.Search(ctx, "cache", "from VectorItem1536 o where o.dummy = 1", 1)

	require.Equal(t, 1, downstream.CallCount(), "downstream must be called exactly once")

	outbound := downstream.LastHeader().Get("Traceparent")
	require.NotEmpty(t, outbound, "traced request must inject outbound traceparent")
	require.Contains(t, outbound, traceID, "outbound traceparent must carry the caller's trace ID")
}

// staticStringCarrier is a read-only TextMapCarrier for synthetic header injection in tests.
type staticStringCarrier map[string]string

func (c staticStringCarrier) Get(key string) string { return c[key] }
func (c staticStringCarrier) Set(_, _ string)        {}
func (c staticStringCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
