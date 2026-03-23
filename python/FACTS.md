# Python Module Facts

**Conversation channel naming**: `MemoryServiceCheckpointSaver` stores checkpoint state in the conversation `context` channel; frontend-safe reads should continue forcing `channel="history"` unless internal agent state is explicitly desired.

**Context agent ID requirement**: `MemoryServiceCheckpointSaver` must send a stable `agentId` (currently `python-checkpointer`) on both `context` reads and writes; otherwise Memory Service rejects context-channel access with `agentId is required for context channel`.

**Checkpoint lineage**: Python docs checkpoints `04-conversation-forking`, `05-response-resumption`, `06-sharing`, and `07-with-search` are intentionally rebased from `03-with-history` (not chained from each other), mirroring the Quarkus tutorial branching model.

**Proxy pattern**: Use `memory_service_langchain.MemoryServiceProxy` plus `to_fastapi_response(...)` in checkpoint apps for API-like Memory Service passthrough methods (`list_conversation_entries`, `list_memberships`, etc.) instead of ad-hoc `memory_service_request(...)` calls.

**Chat simplification**: For checkpoint-style FastAPI chat routes, keep a single stateful agent and use `extract_assistant_text(...)`; fork metadata is passed via `memory_service_scope(...)` and consumed by the LangChain integration (middleware + checkpointer).

**Request scope helper**: Prefer `memory_service_langchain.memory_service_scope(conversation_id, forked_at_conversation_id, forked_at_entry_id)` for route context binding.

**Forking parity**: Python checkpoint `04-conversation-forking` keeps `/chat/{id}` as `text/plain` and accepts fork metadata via query params (`forkedAtConversationId`, `forkedAtEntryId`) which are bound through `memory_service_scope(...)`.

**Fork metadata ordering gotcha**: In LangChain integrations, checkpoint writes can happen before history middleware writes. `MemoryServiceCheckpointSaver.put(...)` must include fork metadata on the initial append payload (not only 404 retry paths), otherwise a new fork conversation can be created as a root before USER history is written.

**Response recording and resumption pattern**: Keep checkpoint `05` route handlers thin by delegating resume-check/replay/cancel state handling to `memory_service_langchain.MemoryServiceResponseRecordingManager`, mirroring the Quarkus `ResumeResource` style.

**Checkpoint `05` tutorial scope**: `python/examples/langchain/doc-checkpoints/05-response-resumption` is intentionally minimal: `/chat` accepts plain text body, emits SSE `PartialResponse` events only, uses `MemoryServiceResponseRecordingManager.from_env()`, replays in events mode, and proxies `/cancel` to Memory Service.

**LangGraph checkpoint `05` parity**: `python/examples/langgraph/doc-checkpoints/05-response-resumption` follows the same gRPC-backed pattern as the LangChain checkpoint (`stream_from_source`, `replay_sse(..., stream_mode="events")`, proxied `/cancel`), but uses `graph.astream(..., stream_mode="messages")` as the token source.

**True-streaming checkpoint**: `05-response-resumption` should stream live model tokens via `agent.astream(..., stream_mode="messages")`; avoid `agent.invoke(...)` + post-hoc word-splitting.

**SSE delimiter gotcha**: SSE chunks must end with real newlines (`\n\n`), not escaped backslash sequences (`\\n\\n`), or frontend incremental parsing buffers until stream end.

**gRPC-backed recording-manager support**: `MemoryServiceResponseRecordingManager` now supports gRPC `CheckRecordings`/`Replay`/`Cancel` (including `redirect_address` follow-up) and provides `replay_sse(...)` to emit SSE-compatible chunks from recorded gRPC content.

**Devcontainer Python tooling**: `uv` is installed in `.devcontainer/Dockerfile` (not via `devcontainer.json` features), so Python checkpoint workflows can run immediately after `wt up`.

**Module build**: `Taskfile.yml` owns the Dockerized Python packaging tasks: `task generate:python` regenerates gRPC stubs, `task build:python:langchain` builds the wheel, and `task verify:python` runs the full stub/build/install verification flow with `astral/uv:python3.11-bookworm`.

**Package layout**: Python integrations currently include both `python/langchain` (`memory-service-langchain`) and `python/langgraph` (`memory-service-langgraph`) for episodic memory `BaseStore` support.

**Chat example paths**: Full FastAPI chat apps are at `python/examples/langchain/chat-langchain` (LangChain agent path) and `python/examples/langgraph/chat-langgraph` (LangGraph path).

**Chat frontend path resolution**: `python/examples/langchain/chat-langchain/app.py` should locate repo root by searching for `Taskfile.yml`, not fixed `Path(...).parents[N]`, otherwise the frontend dist path can incorrectly resolve under `python/frontends/...`.

**LangChain middleware scope**: `MemoryServiceHistoryMiddleware` only persists USER/AI history entries and does not attach streaming callbacks or gRPC recorders; response recording/replay is owned by `MemoryServiceResponseRecordingManager.stream_from_source(...)`.

**Stream token extraction gotcha**: `extract_stream_text(...)` must tolerate provider-specific chunk shapes (`content_blocks`, nested `text/value/delta` objects), or `/chat` SSE can appear non-streaming because emitted token chunks are empty.

**Uvicorn logging gotcha**: In `chat-langchain`, diagnostics should log to `uvicorn.error` (not module-local `__name__` logger) if you want `INFO` stream-progress lines to appear alongside access logs by default.

**LangChain gRPC target default**: `MemoryServiceHistoryMiddleware` and `MemoryServiceResponseRecordingManager` derive default gRPC target from `MEMORY_SERVICE_URL` (`host:port`, with `80/443` fallback), matching the Go single-port server topology; `MEMORY_SERVICE_GRPC_PORT`/`MEMORY_SERVICE_GRPC_TARGET` still override.

**Python client transport split**: `python/langchain` does not ship a generated REST client; REST calls are hand-written `httpx.Client`/`AsyncClient` wrappers keyed off `MEMORY_SERVICE_URL`, while gRPC uses generated stubs plus explicit `grpc.insecure_channel(target)` construction.

**UDS config knob**: `MEMORY_SERVICE_UNIX_SOCKET` now drives both Python REST and gRPC transport setup. LangChain/LangGraph wrappers build `httpx` UDS transports for REST and derive `unix:///absolute/path.sock` for gRPC unless an explicit gRPC target override is set.

**Recorder stream deadline gotcha**: Do not set per-call gRPC deadlines on `ResponseRecorderService.Record(stream)`; long generations can outlive the deadline and terminate the stream without a final `complete`, leaving recordings marked in-progress.

**Recorder registration timing**: Python `GrpcResponseRecorder` starts the gRPC `Record` stream eagerly so `resume-check` can see an in-progress response before the first text token is emitted, matching the Java and TypeScript clients.

**gRPC replay timeout behavior**: `MemoryServiceResponseRecordingManager` uses no replay deadline by default (`MEMORY_SERVICE_GRPC_REPLAY_TIMEOUT_SECONDS` unset), and treats gRPC `DEADLINE_EXCEEDED`/`CANCELLED` as normal stream termination for `/resume` SSE to avoid ASGI crash logs on disconnect/long-poll edges.

**Disconnect-resume behavior**: `MemoryServiceResponseRecordingManager.stream_from_source(...)` now runs source production in a background task; if the active SSE client disconnects, generation/recording continues while gRPC replay/check remain the source of truth.

**Resume-check access race**: Python chat/checkpoint apps that use `stream_from_source(...)` should create the conversation (or append USER history) before returning `StreamingResponse`; otherwise `CheckRecordings` can return `[]` even when replay works, because access control checks the conversation before the background producer has created it.

**Local cancel behavior**: `MemoryServiceResponseRecordingManager.cancel(...)` must cancel the local producer task (not just flip a state flag) so `finally` runs promptly and closes the recorder stream when users click Stop.

**Remote cancel propagation**: The Python recording manager must watch for gRPC recorder `RECORD_STATUS_CANCELLED` and cancel the local producer task, so cancel requests from another client/backend instance stop the original producer.

**chat-langchain response integration**: `python/examples/langchain/chat-langchain/app.py` wires `/chat` through `memory_service_scope(...)` + `MemoryServiceHistoryMiddleware.from_env(...)` + `MemoryServiceResponseRecordingManager.from_env()`; gRPC recording is only done in `recording_manager.stream_from_source(...)` and records Quarkus-style payloads (event JSON lines or raw token text), not SSE `data:` framing bytes.

**Cancel/failure partial history parity**: `chat-langchain` now explicitly persists buffered assistant text to `history` on stream cancel/failure (mirroring Quarkus `finishCancel`/`finishFailure` behavior where partial responses are stored).

**chat example stream mode parity**: `chat-langchain` and `chat-langgraph` use fixed events-mode streaming (no `streamMode` query/body override) to match the Quarkus chat example; resume endpoints also replay in events mode.

**LangChain event naming**: Use canonical event names (`PartialResponse`, `PartialThinking`, `ChatCompleted`) for SSE and recorded `history/lc` events; avoid Java-style `...Event` suffixes so frontend rich-event renderers work without translation.

**Replay mode selection**: `MemoryServiceResponseRecordingManager.replay_sse(...)` supports `stream_mode` (`tokens|events|auto`). `auto` detects recorded JSON event chunks (`eventType`/`event`) and otherwise emits token payloads.

**Replay framing gotcha**: gRPC `Replay` delivers a byte stream and may split chunks mid-SSE frame; `replay_sse(...)` must reframe on `\n\n` boundaries before forwarding, not assume each gRPC chunk is one SSE message.

**Replay format gotcha**: Legacy/alternate recordings may be JSON-lines (`{"eventType":...}\n`) instead of SSE-framed `data:` records; `replay_sse(...)` must auto-detect and reframe `jsonl` vs `sse` before sending to the frontend.

**chat-langchain stream API**: `python/examples/langchain/chat-langchain/app.py` now uses `agent.astream(..., stream_mode="messages")` for the normal agent path (recommended streaming API) rather than `astream_events(...)`.

**chat-langgraph stream API**: `python/examples/langgraph/chat-langgraph/app.py` uses a compiled `StateGraph(MessagesState)` and `graph.astream(..., stream_mode="messages")`, while explicitly appending USER/AI history entries around the stream.

**Shared stream adapter**: `memory_service_langchain.chat_helpers.stream_chunks_as_sse(...)` now owns token extraction, SSE chunk shaping, partial-history persistence on cancel/failure, and stream diagnostics; both `chat-langchain` and `chat-langgraph` call it to keep endpoint code minimal and behavior aligned.

**FastAPI auth middleware default**: `install_fastapi_authorization_middleware(...)` binds the bearer token into request context for downstream Memory Service calls; JWT claim validation is optional and disabled by default (`MEMORY_SERVICE_JWT_VALIDATION_ENABLED=false`).

**Chat app auth config**: `chat-langchain` and `chat-langgraph` explicitly call `install_fastapi_authorization_middleware(app, validate_jwt=False)` so they always forward bearer tokens to Memory Service without local JWT validation.

**FastAPI helper reuse**: Prefer `memory_service_langchain.install_fastapi_authorization_middleware`, `memory_service_scope`, and `memory_service_request` over redefining ContextVars/middleware in checkpoint apps.

**LangGraph threadpool auth gotcha**: `ContextVar` request auth does not reliably flow into LangGraph checkpointer worker threads. `memory_service_scope(...)` now also tracks `conversation_id -> Authorization` for the active scope, and `MemoryServiceCheckpointSaver` falls back to that mapping when the direct getter is empty; this avoids intermittent `{"code":"forbidden","error":"forbidden"}` on checkpoint writes under parallel site tests.

**LangGraph episodic write contract**: `memory_service_langgraph` now sends episodic write indexing via `index` (map of field-path to redacted text) instead of legacy `index_fields` / `index_disabled`.

**LangGraph index controls**: `MemoryServiceStore` and `AsyncMemoryServiceStore` accept user hooks `index_builder` (full payload override) or `index_redactor` (per-field mutate/drop in the default builder).
