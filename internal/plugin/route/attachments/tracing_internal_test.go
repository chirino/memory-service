package attachments

// This file is package attachments (internal test) so that it can call
// completeSourceURLAttachment directly and verify that the production HTTP
// client construction — otelhttp.NewTransport wrapping newSourceURLTransport —
// propagates traceparent to the outbound download request.
//
// Removing otelhttp.NewTransport from completeSourceURLAttachment causes
// TestAttachmentCompleteSourceURLTracedRequestInjectsTraceparent to fail.
// Removing otelhttp.NewTransport from the parallel NewInfinispanClientForTest
// helper does not affect this test.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	b3prop "go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

// minimalAttachStore is the smallest possible AttachmentStore that allows
// completeSourceURLAttachment to run through its full path without panic.
type minimalAttachStore struct{}

func (m *minimalAttachStore) Store(_ context.Context, data io.Reader, _ int64, _ string) (*registryattach.FileStoreResult, error) {
	_, _ = io.Copy(io.Discard, data)
	return &registryattach.FileStoreResult{StorageKey: "test-key", Size: 0, SHA256: "abc"}, nil
}
func (m *minimalAttachStore) Retrieve(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (m *minimalAttachStore) Delete(_ context.Context, _ string) error { return nil }
func (m *minimalAttachStore) GetSignedURL(_ context.Context, _ string, _ time.Duration, _ *registryattach.SignedURLOptions) (*url.URL, error) {
	return nil, nil
}

// minimalMemoryStore embeds the MemoryStore interface so all unimplemented
// methods panic; only InWriteTx and FinalizeAttachment are needed by
// completeSourceURLAttachment.
type minimalMemoryStore struct {
	registrystore.MemoryStore
}

func (m *minimalMemoryStore) InWriteTx(_ context.Context, fn func(context.Context) error) error {
	return fn(context.Background())
}

func (m *minimalMemoryStore) FinalizeAttachment(_ context.Context, _ string, _ uuid.UUID, _ uuid.UUID, _ registrystore.AttachmentFinalize) (*model.Attachment, error) {
	return nil, nil
}

func (m *minimalMemoryStore) MarkAttachmentFailed(_ context.Context, _ string, _ uuid.UUID, _ uuid.UUID) (bool, error) {
	return true, nil
}

// TestAttachmentCompleteSourceURLUntracedRequestNoTraceparent verifies that a
// completeSourceURLAttachment call on an untraced context does not inject
// traceparent into the outbound HTTP download request.
func TestAttachmentCompleteSourceURLUntracedRequestNoTraceparent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	cfg := &config.Config{
		AllowPrivateSourceURLs: true,
		AttachmentMaxSize:      10 * 1024 * 1024,
		TempDir:                t.TempDir(),
	}

	attachID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	err := completeSourceURLAttachment(
		context.Background(),
		&minimalMemoryStore{},
		&minimalAttachStore{},
		cfg,
		attachID,
		uuid.New(),
		"user1",
		downstream.Server.URL,
		"application/octet-stream",
		nooptrace.NewTracerProvider(),
	)
	require.NoError(t, err)

	require.Equal(t, 1, downstream.CallCount(), "downstream must be called exactly once")
	require.Empty(t, downstream.LastHeader().Get("Traceparent"),
		"untraced request must not inject traceparent")
}

// TestAttachmentCompleteSourceURLTracedRequestInjectsTraceparent verifies that
// completeSourceURLAttachment injects a traceparent header into the outbound
// HTTP download request when the context is sampled and participating.
//
// Because the client is built inside completeSourceURLAttachment using
// otelhttp.NewTransport(newSourceURLTransport(...), ...), removing that wrapper
// from the production function causes this test to fail.
func TestAttachmentCompleteSourceURLTracedRequestInjectsTraceparent(t *testing.T) {
	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	h := testutil.NewTestHarness()
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })

	cfg := &config.Config{
		AllowPrivateSourceURLs: true,
		AttachmentMaxSize:      10 * 1024 * 1024,
		TempDir:                t.TempDir(),
	}

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inbound := testutil.NewSampledTraceparent(traceID, parentSpanID)

	extractedCtx := h.Propagator.Extract(context.Background(),
		staticInternalCarrier{"traceparent": inbound})
	ctx := tracing.MarkParticipating(extractedCtx)

	attachID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	err := completeSourceURLAttachment(
		ctx,
		&minimalMemoryStore{},
		&minimalAttachStore{},
		cfg,
		attachID,
		uuid.New(),
		"user1",
		downstream.Server.URL,
		"application/octet-stream",
		h.Provider,
	)
	require.NoError(t, err)

	require.Equal(t, 1, downstream.CallCount(), "downstream must be called exactly once")
	outbound := downstream.LastHeader().Get("Traceparent")
	require.NotEmpty(t, outbound, "traced request must inject outbound traceparent")
	require.Contains(t, outbound, traceID, "outbound traceparent must carry the caller's trace ID")
}

func TestAttachmentJobRecordsClientSpanWithRequestProvider(t *testing.T) {
	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)
	h := testutil.NewTestHarness()
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })
	cfg := &config.Config{
		AllowPrivateSourceURLs: true,
		AttachmentMaxSize:      10 * 1024 * 1024,
		TempDir:                t.TempDir(),
	}
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	router := gin.New()
	router.Use(tracing.HTTPMiddleware(h.Provider, h.Propagator, h.Propagator))
	router.GET("/attachments", func(c *gin.Context) {
		StartFencedSourceURLAttachmentDownload(c.Request.Context(), &minimalMemoryStore{}, &minimalAttachStore{}, cfg,
			uuid.MustParse("00000000-0000-0000-0000-000000000003"), uuid.New(), "user1", downstream.Server.URL, "application/octet-stream")
		c.Status(http.StatusAccepted)
	})
	req := httptest.NewRequest(http.MethodGet, "/attachments", nil)
	req.Header.Set("traceparent", traceparent)
	router.ServeHTTP(httptest.NewRecorder(), req)

	deadline := time.Now().Add(2 * time.Second)
	var attachmentSpan bool
	for time.Now().Before(deadline) {
		for _, span := range h.Exporter.GetSpans() {
			if span.SpanKind == trace.SpanKindClient && strings.Contains(span.Name, "HTTP GET") {
				attachmentSpan = true
				break
			}
		}
		if attachmentSpan {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.True(t, attachmentSpan, "attachment client span must be exported by the server provider")
	for _, span := range h.DecoyExporter.GetSpans() {
		require.False(t, span.SpanKind == trace.SpanKindClient && strings.Contains(span.Name, "HTTP GET"),
			"attachment client span must not be exported by the decoy provider")
	}
}

// staticInternalCarrier is a read-only TextMapCarrier for synthetic header injection.
type staticInternalCarrier map[string]string

func (c staticInternalCarrier) Get(key string) string { return c[key] }
func (c staticInternalCarrier) Set(_, _ string)       {}
func (c staticInternalCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// TestAttachmentCompleteSourceURLConfiguredPropagatorUsedOutbound verifies that
// completeSourceURLAttachment uses the outbound propagator stored on the context
// via WithOutboundPropagatorContext.  When the propagator is B3 multi-header, the
// outbound download request must carry X-B3-TraceId and must not carry a W3C
// traceparent.
//
// Mutation proof target: if OutboundPropagatorFromContext is removed from
// completeSourceURLAttachment (reverting to a hardcoded propagation.TraceContext{}),
// this test fails because X-B3-TraceId is absent from the outbound request.
func TestAttachmentCompleteSourceURLConfiguredPropagatorUsedOutbound(t *testing.T) {
	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	h := testutil.NewTestHarness()
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })

	cfg := &config.Config{
		AllowPrivateSourceURLs: true,
		AttachmentMaxSize:      10 * 1024 * 1024,
		TempDir:                t.TempDir(),
	}

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"

	// Build a B3 multi-header outbound propagator and store it on the context
	// the same way HTTPMiddleware does when OTEL_PROPAGATORS=b3multi is set.
	b3Propagator := tracing.NewParticipatingPropagator(
		tracing.WithNoBaggage(b3prop.New(b3prop.WithInjectEncoding(b3prop.B3MultipleHeader))),
	)

	// Extract using B3 multi-header (inbound side also uses B3).
	b3InboundProp := b3prop.New(b3prop.WithInjectEncoding(b3prop.B3MultipleHeader))
	extractedCtx := b3InboundProp.Extract(context.Background(),
		staticInternalCarrier{"x-b3-traceid": traceID, "x-b3-spanid": parentSpanID, "x-b3-sampled": "1"})

	// Store the outbound propagator on the context, matching what HTTPMiddleware does.
	ctx := tracing.WithOutboundPropagatorContext(
		tracing.MarkParticipating(extractedCtx),
		b3Propagator,
	)

	attachID := uuid.MustParse("00000000-0000-0000-0000-000000000004")
	err := completeSourceURLAttachment(
		ctx,
		&minimalMemoryStore{},
		&minimalAttachStore{},
		cfg,
		attachID,
		uuid.New(),
		"user1",
		downstream.Server.URL,
		"application/octet-stream",
		h.Provider,
	)
	require.NoError(t, err)

	require.Equal(t, 1, downstream.CallCount(), "downstream must be called exactly once")

	lastHeader := downstream.LastHeader()
	require.NotNil(t, lastHeader)
	require.NotEmpty(t, lastHeader.Get("X-B3-TraceId"),
		"B3 propagator must inject X-B3-TraceId on attachment download request")
	require.Empty(t, lastHeader.Get("Traceparent"),
		"W3C traceparent must not be injected when B3 propagator is configured")
}
