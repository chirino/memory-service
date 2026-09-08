//go:build !noopenai

package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/stretchr/testify/require"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

const embeddingBody = `{"data":[{"index":0,"embedding":[0.1,0.2,0.3]}]}`

type embedderTestSetup struct {
	Harness    *testutil.Harness
	Downstream *testutil.DownstreamRecorder
	Embedder   *OpenAIEmbedder
}

func setupEmbedderTest(t *testing.T) *embedderTestSetup {
	t.Helper()
	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	downstream := testutil.NewDownstreamRecorderWithBody(embeddingBody)
	t.Cleanup(downstream.Close)

	embedder := NewOpenAIEmbedder("test-key", "text-embedding-3-small", downstream.Server.URL, 0,
		harness.Provider)

	return &embedderTestSetup{
		Harness:    harness,
		Downstream: downstream,
		Embedder:   embedder,
	}
}

func TestOpenAIEmbedderUntracedRequestNoTraceparent(t *testing.T) {
	setup := setupEmbedderTest(t)

	// Untraced context — no MarkParticipating.
	_, err := setup.Embedder.EmbedTexts(context.Background(), []string{"hello"})
	require.NoError(t, err)

	// Positive assertion: downstream was reached exactly once.
	require.Equal(t, 1, setup.Downstream.CallCount(), "downstream must be called exactly once")

	// Negative assertion on executed path: no traceparent header injected.
	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)
	require.Empty(t, lastHeader.Get("Traceparent"), "untraced request must not inject traceparent")
}

func TestOpenAIEmbedderTracedRequestInjectsTraceparent(t *testing.T) {
	setup := setupEmbedderTest(t)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	// Build a participating context the same way the HTTP/gRPC inbound middleware does:
	// extract the remote span context into ctx, then mark it as participating.
	extractedCtx := setup.Harness.Propagator.Extract(context.Background(),
		staticCarrier{"traceparent": inboundTraceparent})
	ctx := tracing.MarkParticipating(extractedCtx)

	_, err := setup.Embedder.EmbedTexts(ctx, []string{"hello"})
	require.NoError(t, err)

	// Positive assertion: downstream was reached exactly once.
	require.Equal(t, 1, setup.Downstream.CallCount(), "downstream must be called exactly once")

	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)

	// traceparent must be present and carry the caller's trace ID.
	outbound := lastHeader.Get("Traceparent")
	require.NotEmpty(t, outbound, "traced request must inject outbound traceparent")
	require.Contains(t, outbound, traceID, "outbound traceparent must carry the caller's trace ID")

	// Baggage must never be forwarded to a third-party endpoint.
	require.Empty(t, lastHeader.Get("Baggage"), "baggage must not be forwarded to OpenAI")
}

// staticCarrier is a minimal read-only TextMapCarrier for injecting synthetic
// headers into a context via a propagator during testing.
type staticCarrier map[string]string

func (c staticCarrier) Get(key string) string {
	return c[strings.ToLower(key)]
}
func (c staticCarrier) Set(key, val string) {}
func (c staticCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

func TestRedactAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
		msg    string
	}{
		{
			name:   "classic openai key",
			apiKey: "sk-1234567890abcdefghijklmnop",
			msg:    "Incorrect API key provided: sk-1234567890abcdefghijklmnop.",
		},
		{
			name:   "project openai key",
			apiKey: "sk-proj-1234567890_abcdefghijklmnop",
			msg:    "Incorrect API key provided: sk-proj-1234567890_abcdefghijklmnop.",
		},
		{
			name:   "compatible provider key with punctuation",
			apiKey: "provider/key:with+symbols=and.dots",
			msg:    "Incorrect API key provided: provider/key:with+symbols=and.dots.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactAPIKey(tt.msg, tt.apiKey)
			if strings.Contains(got, tt.apiKey) {
				t.Fatalf("redacted message still contains API key: %q", got)
			}
			if !strings.Contains(got, "[REDACTED_OPENAI_API_KEY]") {
				t.Fatalf("redacted message missing marker: %q", got)
			}
		})
	}
}

func TestEmbedTextsUsesGenericErrorForProviderAuthFailure(t *testing.T) {
	apiKey := "provider/key:with+symbols=and.dots"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer "+apiKey; got != want {
			t.Fatalf("Authorization header = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "provider-request-1")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s."}}`, apiKey)
	}))
	defer server.Close()

	embedder := NewOpenAIEmbedder(apiKey, "text-embedding-3-small", server.URL, 0,
		nooptrace.NewTracerProvider())

	_, err := embedder.EmbedTexts(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if strings.Contains(got, apiKey) {
		t.Fatalf("error still contains API key: %q", got)
	}
	if strings.Contains(got, "Incorrect API key provided") {
		t.Fatalf("error still contains upstream auth message: %q", got)
	}
	if !strings.Contains(got, "authentication failed with status 401") {
		t.Fatalf("error missing generic auth failure: %q", got)
	}
	var detailer operationevent.ErrorDetailer
	if !errors.As(err, &detailer) {
		t.Fatalf("error does not expose typed provider details: %T", err)
	}
	details := detailer.OperationErrorDetails()
	if details.Provider == nil || details.Provider.Name != "openai" || details.Provider.StatusCode != http.StatusUnauthorized || details.Provider.TransactionID != "provider-request-1" || details.Reason != "authentication_failed" {
		t.Fatalf("unexpected provider details: %#v", details)
	}
}

func TestEmbedTextsOmitsProviderBodyFromNonAuthProviderError(t *testing.T) {
	apiKey := "provider/key:with+symbols=and.dots"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"Bad request included key %s."}}`, apiKey)
	}))
	defer server.Close()

	embedder := NewOpenAIEmbedder(apiKey, "text-embedding-3-small", server.URL, 0,
		nooptrace.NewTracerProvider())

	_, err := embedder.EmbedTexts(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error")
	}
	got := err.Error()
	if strings.Contains(got, apiKey) {
		t.Fatalf("error still contains API key: %q", got)
	}
	if strings.Contains(got, "Bad request included key") || strings.Contains(got, "[REDACTED_OPENAI_API_KEY]") {
		t.Fatalf("error included provider response content: %q", got)
	}
}
