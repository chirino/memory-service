---
status: implemented
---

# Enhancement 403: OTel Trace Participation

> **Status**: Implemented.

## Summary

Instrument the Go memory service as a passive OpenTelemetry trace participant. When an
inbound HTTP or gRPC request carries a valid W3C `traceparent` header, the service joins
that trace by emitting child spans and propagating the trace context to covered downstream
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
enforced by a custom `ParticipatingPropagator` that wraps the configured inbound propagator
(see `OTEL_PROPAGATORS` below): its `Inject` method is a no-op unless `IsParticipating(ctx)`
is true, so trace headers are never injected into outbound requests when no upstream trace
context was received.

Three branches apply to every inbound request:

- **Absent or invalid traceparent**: request passes through unchanged; no span, no
  outbound injection.
- **Valid, unsampled traceparent** (flags byte `00`): context is preserved and propagated
  outbound so the trace chain is not broken, but no span is recorded locally.
- **Valid, sampled traceparent** (flags byte `01`): a server span is created as a child of
  the inbound parent, and the child span context is propagated to covered outbound calls.

### Scope

The change covers:

- Main HTTP listener (Gin router) via `tracing.HTTPMiddleware` at position 1 in the chain.
- Management HTTP listener (`/health`, `/ready`, `/metrics`) via the same shared
  `newConfiguredRouter` factory.
- gRPC unary server path via `tracing.GRPCUnaryServerInterceptor` at position 1.
- gRPC streaming server path via `tracing.GRPCStreamServerInterceptor` at position 1.
- Outbound clients and datastores: OpenAI embedder, Infinispan vector HTTP client, Qdrant
  vector gRPC client, episodic Qdrant gRPC client, source-URL attachment download HTTP client,
  Prometheus stats HTTP client, and all PostgreSQL GORM datastores.

Other outbound callers (Vault, AWS KMS, Redis, S3, Infinispan/Redis cache, and encryption
plugins) are out of scope.

### TracerProvider

A single server-scoped `TracerProvider` is built in `BuildServer` and threaded through to
every plugin loader via `tracing.WithProviderContext`. It is noop by default and activates
only when `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set.
This keeps the service zero-overhead for deployments that do not export traces, and avoids
calling `otel.SetTracerProvider` globally, which would race under the BDD test suite's
concurrent `BuildServer` calls.

### Span attributes

HTTP spans carry `http.request.method`, `http.route` (the parameterised route template,
e.g. `/v1/entries/{id}`), and `http.response.status_code`. The concrete request path is
deliberately excluded to prevent signed tokens and user-supplied IDs from reaching
exporters. Span status is set to Error on 5xx responses; 4xx responses leave span status
Unset per OTel HTTP server semconv (4xx indicates a client error, not a server fault).
When a 5xx response also carries a handler error in `c.Errors`, the error is recorded on
the span via `RecordError`. `c.Errors` alone — without a 5xx status — does not set span
status to Error.

gRPC spans carry `rpc.system`, `rpc.service`, `rpc.method`, and `rpc.grpc.status_code`.
Span status is set to Error only for server-side failures; caller errors
(`InvalidArgument`, `NotFound`, `AlreadyExists`, `PermissionDenied`, `FailedPrecondition`,
`Unauthenticated`) leave span status Unset per OTel gRPC server semconv.

Both HTTP and gRPC spans are enriched on close with `memoryservice.*` attributes from the
operation event snapshot (identity, resource IDs, provider metadata, error classification).
Fields that OTel already owns (duration, result, status) are omitted. `errorDetails`
entries are recorded as span events rather than flat attributes. No authorization headers,
message content, or sensitive payloads are recorded.

`url.full` query strings are stripped by `QueryRedactingExporter` before any span reaches
the OTLP exporter, protecting presigned S3 URLs and OAuth tokens.

### Log correlation

`operationevent.Snapshot` gains `TraceID` and `SpanID` fields populated via
`SetTraceContext`. Both fields are `omitempty` -- untraced requests log neither, matching
the participation-only contract. This allows structured log entries to be correlated
directly with their trace in any OTLP-compatible backend.

### Graceful shutdown

`Server.Shutdown` drains both the management listener and the main listener **before**
calling `tracerProvider.Shutdown` with a 5-second bounded timeout. This ordering ensures
that spans ending during the listener drain are exported rather than silently dropped by
the SDK batch processor.

## Configuration

| Variable | Meaning |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP/HTTP base endpoint to export spans to (e.g. `http://localhost:4318`). Unset disables export entirely. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | OTLP/HTTP endpoint for traces specifically. Checked when `OTEL_EXPORTER_OTLP_ENDPOINT` is absent. |
| `OTEL_SERVICE_NAME` | Service name reported on spans. Defaults to the SDK default (binary name) when unset. |
| `OTEL_PROPAGATORS` | Comma-separated list of propagator formats (e.g. `tracecontext`, `b3`, `b3multi`, `jaeger`). Applied to both inbound extraction and outbound injection. Defaults to `tracecontext,baggage`. Internal baggage is always suppressed on outbound calls to third-party endpoints. |
| `OTEL_RESOURCE_ATTRIBUTES` | Additional resource attributes (e.g. `deployment.environment=production`). |

No memory-service-specific configuration is added. Standard OTel environment variables
(`OTEL_EXPORTER_OTLP_HEADERS`, etc.) are honoured automatically by the OTel SDK.

## Non-Goals

Auth logic, business logic, database schema, the S3/Vault/AWS KMS/Redis/Infinispan cache
and encryption plugins, the Prometheus stats HTTP client, `internal/cmd/process/turntraces`,
and `internal/cmd/mcp` are out of scope and unchanged. The `turntraces` processor is a
separate analytical trace feature and remains independent.
