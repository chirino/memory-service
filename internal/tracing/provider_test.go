package tracing_test

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/tracing"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestProviderFromContextOrNoop(t *testing.T) {
	t.Run("nil context returns noop provider", func(t *testing.T) {
		tp := tracing.ProviderFromContextOrNoop(nil) //nolint:staticcheck
		require.NotNil(t, tp)
		tracer := tp.Tracer("test")
		_, span := tracer.Start(context.Background(), "noop-span")
		require.False(t, span.SpanContext().IsValid())
	})

	t.Run("empty context returns noop provider", func(t *testing.T) {
		tp := tracing.ProviderFromContextOrNoop(context.Background())
		require.NotNil(t, tp)
		tracer := tp.Tracer("test")
		_, span := tracer.Start(context.Background(), "noop-span")
		require.False(t, span.SpanContext().IsValid())
	})

	t.Run("context with provider returns stored provider", func(t *testing.T) {
		customTP := sdktrace.NewTracerProvider()
		t.Cleanup(func() { _ = customTP.Shutdown(context.Background()) })

		ctx := tracing.WithProviderContext(context.Background(), customTP)
		tp := tracing.ProviderFromContextOrNoop(ctx)
		require.Same(t, customTP, tp)
	})
}
