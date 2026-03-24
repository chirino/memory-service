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

**History-side `agentId` is transitional**: Quarkus history/runtime code may still pass `agentId` on history appends for child-conversation attribution, but context reads/writes should no longer depend on request-level `agentId`.

**Async sub-agent auth propagation**: The Quarkus extension carries `userId` and bearer token for background child-task execution via its own thread-local execution context plus a per-child-conversation fallback map. That lets Memory Service-backed chat memory and history APIs keep user auth even when child execution hops threads.

**Unified sub-agent tool shape**: Quarkus default tool names are `agentSend`, `agentPoll`, `waitTask`, and `agentStop`. `agentSend` creates a child conversation when `taskId` is omitted. When `taskId` is present, `mode` is required and must be `queue` or `interrupt`. The intended model behavior is to reuse an existing child conversation when it already has the right context instead of starting a new one. `agentSend` also accepts an optional `agentId` override. Tool-facing ids are exposed as `taskId`/`taskIds` even though the runtime still stores them as child conversation ids internally.
**Aggregate child waits**: `waitTask` accepts `taskIds`; omit it or pass `[]` to wait across all current child tasks for the parent conversation, or pass one or more ids to wait on that subset. The timeout parameter is `secs`, but `secs <= 0` is normalized to a default 5-second wait. Use `agentPoll` for immediate polling.
**Sub-agent concurrency limit**: `SubAgentToolProviderFactory` exposes a per-parent max concurrency through `factory.builder().maxConcurrency(n)`. Only tasks in `RUNNING` state count against that limit; queued, stopped, and completed tasks do not.
**Low-level tool registration**: Quarkus AI Services expose sub-agent tools through `toolProviderSupplier` and a runtime `ToolProvider`. The old subclassed `SubAgentTaskTool` / `StreamingSubAgentTaskTool` example style is gone.
**Sub-agent provider builder**: `SubAgentToolProviderFactory` now uses `factory.builder()` as the supported API. Configure child agent id, max concurrency, or tool names/descriptions on the builder, then finish with `.createStreamingProvider(...)` or `.createProvider(...)`.
**Child agent safety**: If the app uses a dedicated child AI service, keep `SubAgentTool` off that child service's tool list; a different conversation ID avoids memory collision, but it does not prevent unbounded delegation loops.

**Streaming child tools**: Quarkus supports streaming child handlers via the sub-agent runtime. The runtime consumes the child `Multi<ChatEvent>` in the background, exposes accumulated partial response text through `agentPoll`, supports bounded waits through `waitTask`, and allows best-effort cancellation through `agentStop`.
**Sub-agent CDI registration**: `SubAgentTaskManager` lives in the extension runtime and needs explicit bean registration from the deployment module, otherwise Quarkus dev mode reports unsatisfied injections even though plain `compile` succeeds.
**Tavily bean source**: In Quarkus apps using `quarkus-langchain4j-tavily`, do not also produce your own default `WebSearchTool` bean unless you qualify it; the built-in bean plus an app producer causes ambiguous AI-service tool injection.
**Async child auth lookup**: Background child-task execution should use only `SubAgentExecutionContext` for user/token propagation. Do not rely on CDI `SecurityIdentity` there.
**Parent-to-child auth capture**: The parent-facing sub-agent tool entrypoint runs before child work is offloaded. It must capture bearer token and user ID from the active request identity there and pass them into `SubAgentExecutionContext`; otherwise child memory/history calls run unauthenticated and get `401 missing Authorization header`.
**Async child history writes**: `ConversationStore` also runs on background child-task threads during success/failure recording. Guard all `SecurityIdentityAssociation` / `SecurityIdentity` access behind an active request context and use `SubAgentExecutionContext` for bearer-token and user propagation off-request.
**Async child security helper**: `SecurityHelper` methods that accept `Instance<SecurityIdentity>` must return `null` when no request context is active. Calling `Instance.get()` from background child-task threads triggers `SecurityIdentityProxy`/`RequestScoped` failures inside chat-memory and proxy helpers.
**Async child chat memory**: `MemoryServiceChatMemoryStore` also injects `Instance<SecurityIdentity>` directly; its local resolver must guard on `Arc.container().requestContext().isActive()` before calling `Instance.get()`, otherwise sub-agent memory loads fail before the shared `SecurityHelper` fallback logic runs.

**Agent/sub-agent docs example**: Both `chat-quarkus` and the `03c-agent-subagent-workflows` checkpoint now use `toolProviderSupplier` plus the runtime factory instead of subclassed tool beans.
**Standalone dev-mode extension changes**: If a Quarkus example is run standalone rather than from the reactor, changes under `memory-service-extension/deployment` or `memory-service-extension/runtime` must be installed to `~/.m2` before dev mode sees new beans/build steps.

**Spotless after Java file renames**: Renaming example Java source files can leave stale paths in Spotless' cache/index, producing `File stored in the index does not exist` log lines during `mvn compile`. The build can still succeed; rerunning compile after the rename is enough unless Spotless starts failing hard.
**Sub-agent wait tool shape**: `waitTask` in `memory-service-extension/runtime` should serialize a simplified JSON array of task summaries at the tool boundary, even when waiting on a single child. Keep the task manager result types richer internally; normalize only in `SubAgentToolProviderFactory`.
**`waitForTasks` timeout rounding gotcha**: `SubAgentTaskManager.waitForTasks(...)` converts the shared remaining deadline to whole seconds before each per-task wait. With multiple child tasks, that floors away fractional time on every iteration, so tests like `SubAgentTaskManagerTest.waitForTasksReturnsAggregateResults` can fail on CI under load even when the total wall-clock delay is still within the nominal timeout.
**Sub-agent run-start race**: `SubAgentTaskManager.submit(...)` schedules async work that reads mutable `TaskState` fields like `taskInvoker` and `childAgentId` on the worker thread instead of capturing them at submit time. A concurrent `agentSend` on the same child conversation can change those fields before the run starts, so the in-flight run may execute with the wrong invoker/agent config.
