---
status: implemented
---

# Enhancement 403: OTel Trace Participation

> **Status**: Implemented.

## Summary

Instrument the Go memory service as a passive OpenTelemetry trace participant. When an
inbound HTTP or gRPC request carries a valid W3C `traceparent` header, the service joins
that trace by emitting child spans and propagating the trace context to all downstream
calls. When no trace context is present, the service behaves identically to before -- no
spans are created, no headers are injected, and no OTel overhead is incurred.

This implements [GitHub issue #403](https://github.com/chirino/memory-service/issues/403).

## Motivation

Agent frameworks and upstream services that call memory-service already produce
distributed traces. Without participation, memory-service latency and its downstream
operations (vector search, embedding calls, attachment downloads) are invisible within
those traces. Operators cannot determine whether a slow agent turn is caused by
memory-service itself or by one of its backends.

The service must never originate traces for untraced traffic -- doing so would pollute
observability backends with low-value root spans for every ordinary request.

## Design

### Participation model

The sampler is `ParentBased(NeverSample())`. The service never starts a root span. A span
is created only when an inbound request carries a valid, sampled `traceparent`. This is
enforced by a custom `ParticipatingPropagator` that wraps the standard W3C propagator:
its `Inject` method is a no-op unless `IsParticipating(ctx)` is true, so trace headers
are never injected into outbound requests when no upstream trace context was received.

Three branches apply to every inbound request:

- **Absent or invalid traceparent**: request passes through unchanged; no span, no
  outbound injection.
- **Valid, unsampled traceparent** (flags byte `00`): context is preserved and propagated
  outbound so the trace chain is not broken, but no span is recorded locally.
- **Valid, sampled traceparent** (flags byte `01`): a server span is created as a child of
  the inbound parent, and the child span context is propagated to all outbound calls.

### Scope

The change covers:

- Main HTTP listener (Gin router) via `tracing.HTTPMiddleware` at position 1 in the chain.
- Management HTTP listener (`/health`, `/ready`, `/metrics`) via the same shared
  `newConfiguredRouter` factory.
- gRPC unary server path via `tracing.GRPCUnaryServerInterceptor` at position 1.
- gRPC streaming server path via `tracing.GRPCStreamServerInterceptor` at position 1.
- All five outbound clients: OpenAI embedder, Infinispan vector HTTP client, Qdrant vector
  gRPC client, episodic Qdrant gRPC client, and source-URL attachment download HTTP client.

### TracerProvider

A single server-scoped `TracerProvider` is built in `BuildServer` and threaded through to
every plugin loader via `tracing.WithProviderContext`. It is noop by default and activates
only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. This keeps the service zero-overhead for
deployments that do not export traces, and avoids calling `otel.SetTracerProvider`
globally, which would race under the BDD test suite's concurrent `BuildServer` calls.

### Span attributes

HTTP spans carry `http.request.method`, `url.path`, `http.response.status_code`, and OTel
error status on 4xx/5xx. gRPC spans carry `rpc.system`, `rpc.service`, `rpc.method`, and
`rpc.grpc.status_code`. Both are enriched on close with `memoryservice.*` attributes from
the operation event snapshot (identity, resource IDs, provider metadata, error
classification). Fields that OTel already owns (duration, result, status) are omitted.
`errorDetails` entries are recorded as span events rather than flat attributes. No
authorization headers, message content, or sensitive payloads are recorded.

### Log correlation

`operationevent.Snapshot` gains `TraceID` and `SpanID` fields populated via
`SetTraceContext`. Both fields are `omitempty` -- untraced requests log neither, matching
the participation-only contract. This allows structured log entries to be correlated
directly with their trace in any OTLP-compatible backend.

### Graceful shutdown

`Server.Shutdown` calls `tracerProvider.Shutdown` with a 5-second bounded timeout before
closing the network listener, ensuring in-flight spans are flushed.

## Configuration

| Variable | Meaning |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/HTTP endpoint to export spans to (e.g. `http://localhost:4318`). Unset disables export entirely. |
| `OTEL_SERVICE_NAME` | Service name reported on spans. Defaults to `memory-service` via `resource.Default()`. |

No memory-service-specific configuration is added. Standard OTel environment variables
(`OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_RESOURCE_ATTRIBUTES`, etc.) are honoured
automatically by the OTel SDK.

## Non-Goals

Auth logic, business logic, database schema, the S3/Vault/AWS KMS/Redis/Infinispan cache and encryption plugins, the Prometheus stats HTTP client,
`internal/cmd/process/turntraces`, and `internal/cmd/mcp` are out of scope and unchanged.
The `turntraces` processor is a separate analytical trace feature and remains independent.
