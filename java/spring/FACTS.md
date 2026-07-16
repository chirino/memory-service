# Spring Module Facts

**Chat example encryption**: `java/spring/examples/chat-spring/compose.yaml` selects `MEMORY_SERVICE_ENCRYPTION_KIND=dek` with a well-known development-only DEK so encrypted local storage and public signed attachment URLs work without setup.
**Chat example OIDC audience**: The local Compose example sets `MEMORY_SERVICE_OIDC_ALLOW_MISSING_AUDIENCE=true` for zero-configuration development. Production deployments should configure `MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES` instead.
**Chat example management routes**: The local Compose example sets `MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER=true`; hardened server startup otherwise requires a dedicated management listener.
**Chat example listener**: The local Compose service explicitly binds `0.0.0.0`, selects plaintext HTTP, disables TLS, and acknowledges non-loopback plaintext because Docker port publishing cannot reach a loopback-only container listener.
**Chat example developer console auth**: The integrated `/developer` console uses a separate browser-visible `developer_frontend` API-key client (matching the preserved `DEVELOPER_FRONTEND` env suffix) with an admin role, while the Spring chat frontend keeps its Keycloak login flow. Do not add it to trusted user-ID clients or send `X-User-ID` from the admin console.

**Conversation channel naming**: Spring integrations should use `Channel.CONTEXT` for agent-managed conversation state and reserve `Channel.HISTORY` for user-visible turns.

**Frontend event proxy boundary**: Keep `MemoryServiceProxy.streamEvents(...)` generic. Frontend-facing example handlers such as `EventsController` should enforce history-only entry visibility themselves by forwarding only `entry_channel=history` entry notifications.

**Memory repository limit gotcha**: `listConversationEntries` limit must be `<=200` (contract max). Using `1000` causes upstream `400` errors during chat memory reads and surfaces as app `500`s.

**Ownership transfer pagination parity**: `chat-spring` now forwards optional `afterCursor` and `limit` query params on `GET /v1/ownership-transfers` to `MemoryServiceProxy.listPendingTransfers(...)`.

**ResponseRecordingManager scope**: Spring `ResponseRecordingManager` is the full response-stream lifecycle client (`recorder(...)`, `replay(...)`, `check(...)`, `requestCancel(...)`, `enabled()`), not just resume/replay.

**UDS client knob**: Spring now uses `memory-service.client.url=unix:///absolute/path.sock` for both REST and gRPC. gRPC uses a Netty/JDK Unix-domain-socket channel, and REST uses Reactor Netty with a custom `UnixDomainSocketClientHttpConnector` that keeps `http://localhost` as the logical base URL and forces outbound UDS REST requests to HTTP/1.1.

**Spring REST UDS address type**: Reactor Netty's Linux epoll transport rejects JDK
`java.net.UnixDomainSocketAddress` for outbound REST UDS connections with
`Unexpected SocketAddress implementation ...`. The Spring REST client must use
Netty's `io.netty.channel.unix.DomainSocketAddress` for `remoteAddress(...)`.

**Checkpoint `05` parity**: `java/spring/examples/doc-checkpoints/05-response-resumption` now restores the proxied conversation REST routes (`GET /v1/conversations`, `GET /v1/conversations/{id}`, `GET /v1/conversations/{id}/entries`) so the UDS docs can verify one explicit REST proxy call in addition to response recording and resumption.

**Checkpoint `05` SSE hardening**: `java/spring/examples/doc-checkpoints/05-response-resumption` now serves `/chat/{conversationId}` and `/v1/conversations/{conversationId}/resume` as explicit `text/event-stream` endpoints with framed `{"text":...}` SSE events. The chat stream subscribes on a worker scheduler so an immediate upstream failure is less likely to surface as an initial HTTP 500 before the SSE response is committed.

**Spring gRPC recorder startup**: `GrpcResponseRecordingManager` now logs the
configured `memory-service.client.url` and retry attempt when opening the
`record(...)` stream, and it retries startup on the first actual `record()` /
`complete()` call if the eager constructor-time open failed. This is aimed at
CI-only UDS flakes where stream creation appears to fail before the first SSE
chunk is emitted.

**Spring docs Protobuf override**: Standalone Spring docs checkpoints under
`java/spring/examples/doc-checkpoints/` use `spring-boot-starter-parent` directly,
so they do not inherit `java/spring/pom.xml` dependency management. If
`memory-service-proto-spring` is generated with a newer `protobuf.version` than
Spring Boot manages, those checkpoint POMs need their own `protobuf-java`
dependency-management override or runtime will fail with Protobuf
gencode/runtime version mismatches.

**Entry-list proxy signature drift**: `memory-service-rest-spring` manually maps `MemoryServiceProxy.EntryListOptions` to the generated REST client. Keep every `listConversationEntries` query parameter represented and forwarded when the OpenAPI operation changes.
