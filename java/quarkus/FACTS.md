# Quarkus Module Facts

**Conversation channel naming**: Quarkus integrations should use `Channel.CONTEXT` for agent-managed conversation state and reserve `Channel.HISTORY` for user-visible turns.

**Forking curl gotcha**: Checkpoint `04-conversation-forking` chat routes are `text/plain`; to demo fork creation with curl, create root turns via `/chat/{id}` then append forked entries via Memory Service `/v1/conversations/{forkId}/entries` with `forkedAtConversationId`/`forkedAtEntryId`.

**Chat attachment request parity**: `chat-quarkus` now accepts optional `href` in `RequestAttachmentRef`; the extension `AttachmentRef` supports `href` while keeping a 3-arg constructor for compatibility.

**Ownership transfer pagination parity**: `chat-quarkus` forwards optional `afterCursor` and `limit` query params on `GET /v1/ownership-transfers` to `MemoryServiceProxy.listPendingTransfers(...)`.

**ResponseRecordingManager scope**: Quarkus `ResponseRecordingManager` is the full response-stream lifecycle contract (`recorder(...)`, `replay(...)`, `check(...)`, `requestCancel(...)`, `enabled()`), not only replay/resume.

**UDS client knob**: Quarkus now uses `memory-service.client.url=unix:///absolute/path.sock` for both REST and gRPC. REST uses the extension's custom UDS HTTP client/proxy path, and gRPC uses `grpc-netty-shaded` with `NioDomainSocketChannel` + `UnixDomainSocketAddress`.

**Checkpoint `05` parity**: `java/quarkus/examples/doc-checkpoints/05-response-resumption` now restores proxied conversation REST routes via `ConversationsResource`, so the UDS docs can verify one explicit REST proxy call in addition to response resumption.
