---
name: operationevent-logging
description: Implement or review Memory Service backend operational logging. Use for REST or gRPC boundaries, provider errors, error wrapping, background workers and task attempts, indexers and maintenance jobs, SSE or gRPC streams, request correlation, and changes under internal/operationevent.
---

# Operation Event Logging

Use `internal/operationevent` for canonical operation records. Preserve API contracts, request-ID behavior, audit records, and independently scoped diagnostic logs.

## Workflow

1. Identify the operation boundary and its stable name:
   - REST: `http METHOD /route/{parameter}` from `gin.Context.FullPath()`; use `<unmatched>` when absent.
   - gRPC: `grpc /package.Service/Method` from the full method.
   - Jobs: an allowlisted `job.*` name.
2. Create and attach the event at the outer boundary after request-ID propagation and before rate limiting, authentication, recovery, and application work.
3. Enrich only through typed setters. Set identity and resource IDs after they are validated or resolved. Do not add an arbitrary properties map.
   - REST wrappers with generic path names such as `id` must set the resource-specific field explicitly.
   - gRPC request-derived IDs must not be applied to authentication, rate-limit, or syntactic-validation rejections; explicit handler enrichment takes precedence.
   - Map generic gRPC protobuf fields such as `id` by full RPC method. Never infer their resource type globally.
   - gRPC datastore transaction helpers mark request resources validated. A handler that starts authorized, validated work without those helpers must call `security.MarkGRPCOperationResourcesValidated` before that work.
4. Emit no more than one `start` record, and only for streams or substantial background work. Emit exactly one terminal record at every boundary.
5. Enrich errors only when they determine the terminal result. Do not enrich successful fallback, expected absence, ignored best-effort work, or an eventually successful in-process retry.
6. Run focused race tests, then the affected project verification.

## Terminal results

Use the shared mapping in `internal/operationevent`:

| Outcome | Result |
| --- | --- |
| HTTP 2xx/3xx or gRPC OK | `success` |
| HTTP 400/422 or InvalidArgument/OutOfRange | `invalid` |
| HTTP 401 or Unauthenticated | `unauthenticated` |
| HTTP 403 or PermissionDenied | `forbidden` |
| HTTP 404 or NotFound | `not_found` |
| HTTP 409 or AlreadyExists/Aborted/FailedPrecondition | `conflict` |
| HTTP 429 or ResourceExhausted | `rate_limited` |
| Deadline expiry | `timed_out` |
| Client or context cancellation | `canceled` |
| Other caller rejection | `rejected` |
| HTTP 5xx or Internal/Unavailable/DataLoss | `failed` |
| Failed task attempt successfully returned to its retry queue | `retrying` |

Log starts, successes, and cancellations at info; caller rejection, timeout, and retry at warn; terminal failure at error.

## Error enrichment

- Preserve causes with `%w` or `Unwrap`. A mapped gRPC error must implement both `GRPCStatus()` and `Unwrap()`.
- Every gRPC error mapper, including service-specific internal-error helpers, must retain the original cause while keeping the public status message sanitized.
- Use `errors.Is` and `errors.As` for operational and domain error classification. Do not compare sentinel errors with `==` in paths where an error may be wrapped for operational details, transport status, storage context, or provider diagnostics.
- A REST helper that writes a terminal error response must register the original error with Gin before writing the response so the outer operation middleware can collect typed details. Do not register errors from successful fallback or ignored best-effort work.
- Expose safe diagnostics with `operationevent.ErrorDetailer`, `ErrorDetails`, `ErrorDetailsEntry`, and `ErrorDetailsProvider`.
- Keep compatibility-selected `errorType`, `errorCode`, `reason`, and provider fields at the top level; retain all selected causes in `errorDetails`.
- Preserve dotted numeric branch paths and deterministic deepest-first ordering.
- Return at most eight details, traverse at most 64 error nodes, bound diagnostic strings to 128 characters, and bound provider transaction IDs to 256 characters.
- Keep `errorDetailsTruncated` monotonic. String sanitization alone does not set it.

## Privacy and audit boundary

Never put these values in a canonical event: concrete unmatched paths, query values, headers, cookies, request or response bodies, credentials, message or memory content, namespaces or keys, raw errors, stacks, provider messages, or raw provider responses. Bound and control-character-sanitize every dynamic field.

Provider errors may expose only allowlisted typed metadata such as provider name, status code, stable error code, stable reason, and provider transaction ID.

Keep admin justification only in a distinct `Admin audit` record. The audit and canonical operation may share `requestID`; do not merge their fields.

## Point logs

Keep startup and shutdown status, retry or reconnect-loop state changes, audit records, product events such as replay/eviction/slow-consumer events, and independently scoped failure diagnostics. Internal server failures must retain a separate stack-bearing point log. Remove success/open/close point logs that duplicate a canonical event.

For background jobs, classify context cancellation or deadline expiry before incrementing failure counts or emitting failure diagnostics. A normal shutdown remains `canceled` even when the interrupted call returned an error.

## Verification

Run the smallest focused tests first:

```bash
go test -race ./internal/operationevent ./internal/security ./internal/service ./internal/grpc ./internal/cmd/serve -count=1
go build ./...
```

For broad backend changes, also run:

```bash
task test:go
task test:site
```

Run `task test:site` only after the Go suite completes; do not run it concurrently with Java builds in the same worktree.
