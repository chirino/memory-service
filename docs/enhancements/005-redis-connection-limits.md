# Redis Connection Limits for Response Replay (Draft)

## Problem Summary
When many clients reload/resume a long response at the same time, the memory-service
can exhaust the Redis connection pool:

- `ConnectionPoolTooBusyException: Connection pool reached max wait queue size`

The current replay path keeps a Redis connection open for each active replay. This
scales linearly with concurrent resumes.

## Goals
- Allow the system to scale to many concurrent resume/replay requests.
- Avoid Redis connection pool exhaustion under normal load.
- Keep replay latency low and failure rates near zero.
- Keep the solution portable (Linux/macOS/Windows).

## Non-Goals
- Redesigning the entire response-resumer API.
- Guaranteeing zero loss when Redis is unavailable.

## Proposed Strategy (Temp File Recorder + In-Memory Registry)
Replace Redis streaming reads during replay with a local temp-file recorder per
in-flight response stream.

### High-Level Flow
1) When a response stream starts recording:
   - Create a temp file with a random name (one file per response stream).
   - Register the stream in an in-memory registry:
     - `conversationId -> { tempFilePath, lastWrittenOffset, state }`.
   - Store in Redis:
     - `response:{conversationId} = { host, port, tempFileName }`.
   - Use an advertised per-pod address (not a service DNS) so redirects target
     the specific recorder instance.
2) As tokens arrive:
   - Append to the temp file.
   - Update `lastWrittenOffset` in the registry.
   - Refresh the Redis entry every 5 seconds with a 10 second TTL while recording.
3) When stream completes or is canceled:
   - Mark the registry entry as closed (with final offset).
   - Remove Redis key `response:{conversationId}`.
   - Delete the temp file once there are no readers or writers (best effort).

### Resume Flow
1) On resume request:
   - Read `response:{conversationId}` from Redis.
   - If the host is not the current server, issue a redirect to the correct host.
2) On the correct host:
   - Read from the temp file up to `lastWrittenOffset`.
   - Wait for offset updates from the registry and continue streaming.
   - Detect end-of-stream via registry state (closed) plus offset checks.

### Cancel Flow
1) On cancel request:
   - Read `response:{conversationId}` from Redis.
   - If the host is not the current server, issue a redirect to the correct host.
2) On the correct host:
   - Use the registry to signal the recorder to cancel the in-flight stream.

### Stream End Detection (Windows-Friendly)
We should not assume the temp file can be deleted while open for reads (Windows
can block deletes when a file handle is open). To keep this portable:

- Registry entry includes `state: open | closing | closed` and a `finalOffset`.
- When closing:
  - Set `state=closed` and `finalOffset=lastWrittenOffset`.
  - Then delete the file after all readers detach, or on a background reaper.
- Readers treat `state=closed` and `readOffset >= finalOffset` as end-of-stream.
- A background cleanup job can retry deletion of closed files until success.

### Advertised Host/Port Heuristics
When choosing the `host:port` stored in Redis, use this priority order:
1) Application config: `memory-service.grpc-advertised-address=host:port`.
2) Metadata headers from the `StreamResponseTokens` gRPC request
   (e.g., `:authority`, `x-forwarded-host`, `x-forwarded-port`, if present).
3) Local hostname + destination port that handled the `StreamResponseTokens`
   request.

If none of these yield a usable external address, redirects may not work
outside the pod; log a warning and continue recording.

## Implementation Plan
1) Define data structures:
   - `InFlightRegistry` keyed by `conversationId` with:
     - `tempFilePath`, `lastWrittenOffset`, `state`, `finalOffset`,
       `readerCount`, `writerCount`.
   - `RegistryEntry` exposes:
     - atomic offset updates, state transitions, and wait/notify for readers.
2) Recorder lifecycle:
   - On start, create temp file and register entry.
   - Start a 5s ticker to refresh Redis key with 10s TTL while recording.
   - Append tokens to file, update `lastWrittenOffset`.
3) Resume path:
   - Read Redis key; if host mismatch, redirect to owner.
   - On owner, open temp file, stream up to `lastWrittenOffset`.
   - Wait on registry notifications for new offsets; stop when `state=closed`
     and `readOffset >= finalOffset`.
4) Cancel path:
   - Read Redis key; if host mismatch, redirect to owner.
   - On owner, signal cancel via registry (e.g., set `cancelRequested` or
     emit on cancel stream).
5) Completion and cleanup:
   - On complete/cancel, set `state=closed`, `finalOffset=lastWrittenOffset`.
   - Remove Redis key.
   - Delete temp file when `readerCount==0 && writerCount==0`, otherwise mark
     for background cleanup.
6) Failure handling:
   - On server crash, Redis TTL expires and new resumes fail fast.
   - Add a startup reaper to delete stale temp files older than a threshold.
