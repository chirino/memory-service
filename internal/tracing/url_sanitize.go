package tracing

import (
	"context"
	"net/url"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// urlFullKey is the OTel semconv attribute key recorded by otelhttp on client spans.
const urlFullKey = attribute.Key("url.full")

// QueryRedactingExporter wraps a SpanExporter and strips RawQuery from the
// url.full attribute of every span before forwarding to the delegate.
//
// Motivation: otelhttp records url.full as req.URL.String(), which includes any
// query string. Memory-service source URLs may carry presigned S3 signatures or
// OAuth tokens as query parameters. Stripping RawQuery before export prevents
// those credentials from reaching the trace backend.
//
// Intentional tradeoff: all query strings are stripped from every url.full in
// every exported span, including benign parameters such as pagination cursors
// and filter keys. We cannot distinguish a presigned signature from a page
// number at export time, so blanket stripping is the correct call. Traces will
// show bare paths (e.g. "https://host/path") without query detail — this is
// expected and deliberate.
//
// Placement: register this wrapper around the real exporter in
// buildTracerProvider (internal/cmd/serve/server.go). Issue 5 rewrites that
// function to honour standard OTEL_ env vars; it must preserve this wrapper
// around whatever exporter it constructs. When no exporter is configured (the
// noop path) this wrapper is never constructed, so there is nothing to sanitise
// and nothing leaks.
//
// Scope: applied at the provider level, this covers all five outbound HTTP
// clients uniformly — not just the attachment downloader.
type QueryRedactingExporter struct {
	delegate sdktrace.SpanExporter
}

// NewQueryRedactingExporter returns a QueryRedactingExporter that sanitises
// url.full on every span before forwarding to delegate.
func NewQueryRedactingExporter(delegate sdktrace.SpanExporter) *QueryRedactingExporter {
	return &QueryRedactingExporter{delegate: delegate}
}

// ExportSpans sanitises url.full on each span then delegates to the wrapped exporter.
func (e *QueryRedactingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	sanitised := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		sanitised[i] = sanitiseSpan(s)
	}
	return e.delegate.ExportSpans(ctx, sanitised)
}

// Shutdown forwards to the delegate.
func (e *QueryRedactingExporter) Shutdown(ctx context.Context) error {
	return e.delegate.Shutdown(ctx)
}

// sanitiseSpan returns a ReadOnlySpan whose url.full attribute has RawQuery stripped.
// All other attributes and span data are forwarded unchanged.
// If url.full is absent or has no query string, the original span is returned as-is.
func sanitiseSpan(s sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	attrs := s.Attributes()
	for i, kv := range attrs {
		if kv.Key != urlFullKey {
			continue
		}
		raw := kv.Value.AsString()
		u, err := url.Parse(raw)
		if err != nil || u.RawQuery == "" {
			return s // nothing to strip
		}
		// Build a new attribute slice with url.full replaced.
		sanitisedAttrs := make([]attribute.KeyValue, len(attrs))
		copy(sanitisedAttrs, attrs)
		u.RawQuery = ""
		sanitisedAttrs[i] = urlFullKey.String(u.String())
		return &sanitisedSpan{ReadOnlySpan: s, attrs: sanitisedAttrs}
	}
	return s
}

// sanitisedSpan wraps a ReadOnlySpan and replaces its Attributes() return value.
type sanitisedSpan struct {
	sdktrace.ReadOnlySpan
	attrs []attribute.KeyValue
}

func (s *sanitisedSpan) Attributes() []attribute.KeyValue {
	return s.attrs
}
