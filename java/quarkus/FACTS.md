# Quarkus Module Facts

**Forking curl gotcha**: Checkpoint `04-conversation-forking` chat routes are `text/plain`; to demo fork creation with curl, create root turns via `/chat/{id}` then append forked entries via Memory Service `/v1/conversations/{forkId}/entries` with `forkedAtConversationId`/`forkedAtEntryId`.

**Chat attachment request parity**: `chat-quarkus` now accepts optional `href` in `RequestAttachmentRef`; the extension `AttachmentRef` supports `href` while keeping a 3-arg constructor for compatibility.

**Ownership transfer pagination parity**: `chat-quarkus` forwards optional `afterCursor` and `limit` query params on `GET /v1/ownership-transfers` to `MemoryServiceProxy.listPendingTransfers(...)`.

**ResponseRecordingManager scope**: Quarkus `ResponseRecordingManager` is the full response-stream lifecycle contract (`recorder(...)`, `replay(...)`, `check(...)`, `requestCancel(...)`, `enabled()`), not only replay/resume.
