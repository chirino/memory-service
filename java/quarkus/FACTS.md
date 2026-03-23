# Quarkus Module Facts

**Conversation channel naming**: Quarkus integrations should use `Channel.CONTEXT` for agent-managed conversation state and reserve `Channel.HISTORY` for user-visible turns.

**Forking curl gotcha**: Checkpoint `04-conversation-forking` chat routes are `text/plain`; to demo fork creation with curl, create root turns via `/chat/{id}` then append forked entries via Memory Service `/v1/conversations/{forkId}/entries` with `forkedAtConversationId`/`forkedAtEntryId`.

**Chat attachment request parity**: `chat-quarkus` now accepts optional `href` in `RequestAttachmentRef`; the extension `AttachmentRef` supports `href` while keeping a 3-arg constructor for compatibility.

**Ownership transfer pagination parity**: `chat-quarkus` forwards optional `afterCursor` and `limit` query params on `GET /v1/ownership-transfers` to `MemoryServiceProxy.listPendingTransfers(...)`.

**ResponseRecordingManager scope**: Quarkus `ResponseRecordingManager` is the full response-stream lifecycle contract (`recorder(...)`, `replay(...)`, `check(...)`, `requestCancel(...)`, `enabled()`), not only replay/resume.

**UDS client knob**: Quarkus now uses `memory-service.client.url=unix:///absolute/path.sock` for both REST and gRPC. REST uses the extension's custom UDS HTTP client/proxy path, and gRPC uses `grpc-netty-shaded` with `NioDomainSocketChannel` + `UnixDomainSocketAddress`.

**UDS REST shim signature drift**: `runtime/UnixSocketRestClientFactory` manually maps generated REST client methods. When contract params change (for example `listConversations(..., ancestry, ...)` or `listConversationEntries(..., agentId, ...)`), update that shim or UDS-only requests will silently drop/mis-order query params.

**Checkpoint `05` parity**: `java/quarkus/examples/doc-checkpoints/05-response-resumption` now restores proxied conversation REST routes via `ConversationsResource`, so the UDS docs can verify one explicit REST proxy call in addition to response recording and resumption.

**Agent/sub-agent checkpoint branching**: `java/quarkus/examples/doc-checkpoints/03c-agent-subagent-workflows` branches from `03-with-history`, not from `04-conversation-forking` or `05-response-resumption`.

**`@AgentId` scope gotcha**: In the Quarkus history interceptor, one `@AgentId` value applies to both the intercepted user entry and the recorded AI entry for that single invocation. If the delegating message and the sub-agent response need different agent IDs, use `ConversationStore` manual appends around the delegated call instead of a single `@RecordConversation` wrapper.

**Async sub-agent auth propagation**: The Quarkus extension carries `userId` and bearer token for background child-task execution via its own thread-local execution context. That lets app-specific `SubAgentTaskTool` subclasses call Memory Service-backed chat memory and history APIs even though they run off the original request thread.

**Unified sub-agent tool shape**: Quarkus exposes one write tool, `messageSubAgent`, which creates a child conversation when `childConversationId` is omitted and appends follow-up child messages when it is present. Applications typically expose concrete subclasses such as `FactFindingSubAgentTool`; joined waiting happens in `SubAgentTurnRunner`, not inside the tool method.

**Streaming child tools**: Quarkus supports streaming child handlers via `StreamingSubAgentTaskTool`. The runtime consumes the child `Multi<ChatEvent>` in the background, exposes accumulated partial response text through `getSubAgentStatus`, and still joins the parent turn only after the child stream completes.

**Agent/sub-agent docs example**: The Quarkus agent-subagent workflow example now registers two concrete child tools on the parent AI service: `FactFindingSubAgentTool` for simple fact-gathering results and `FeedbackSubAgentTool` for streaming cross-result evaluation and follow-up questions.

**Spotless after Java file renames**: Renaming example Java source files can leave stale paths in Spotless' cache/index, producing `File stored in the index does not exist` log lines during `mvn compile`. The build can still succeed; rerunning compile after the rename is enough unless Spotless starts failing hard.
