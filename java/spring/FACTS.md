# Spring Module Facts

**Memory repository limit gotcha**: `listConversationEntries` limit must be `<=200` (contract max). Using `1000` causes upstream `400` errors during chat memory reads and surfaces as app `500`s.

**Ownership transfer pagination parity**: `chat-spring` now forwards optional `afterCursor` and `limit` query params on `GET /v1/ownership-transfers` to `MemoryServiceProxy.listPendingTransfers(...)`.

**ResponseRecordingManager scope**: Spring `ResponseRecordingManager` is the full response-stream lifecycle client (`recorder(...)`, `replay(...)`, `check(...)`, `requestCancel(...)`, `enabled()`), not just resume/replay.

**UDS client knob**: Spring now uses `memory-service.client.url=unix:///absolute/path.sock` for both REST and gRPC. gRPC uses a Netty/JDK Unix-domain-socket channel, and REST uses Reactor Netty with a custom `UnixDomainSocketClientHttpConnector` that keeps `http://localhost` as the logical base URL and forces outbound UDS REST requests to HTTP/1.1.

**Checkpoint `05` parity**: `java/spring/examples/doc-checkpoints/05-response-resumption` now restores the proxied conversation REST routes (`GET /v1/conversations`, `GET /v1/conversations/{id}`, `GET /v1/conversations/{id}/entries`) so the UDS docs can verify one explicit REST proxy call in addition to response resumption.
