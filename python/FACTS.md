# Python Module Facts

**Checkpoint lineage**: Python docs checkpoints `04-conversation-forking`, `05-response-resumption`, `06-sharing`, and `07-with-search` are intentionally rebased from `03-with-history` (not chained from each other), mirroring the Quarkus tutorial branching model.

**Proxy pattern**: Use `memory_service_langchain.MemoryServiceProxy` plus `to_fastapi_response(...)` in checkpoint apps for API-like Memory Service passthrough methods (`list_conversation_entries`, `list_memberships`, etc.) instead of ad-hoc `memory_service_request(...)` calls.

**Chat simplification**: For checkpoint-style FastAPI chat routes, keep a single stateful agent and use `extract_assistant_text(...)`; fork metadata is passed via `memory_service_scope(...)` and consumed by the LangChain integration (middleware + checkpointer).

**Request scope helper**: Prefer `memory_service_langchain.memory_service_scope(conversation_id, forked_at_conversation_id, forked_at_entry_id)` for route context binding.

**Forking parity**: Python checkpoint `04-conversation-forking` keeps `/chat/{id}` as `text/plain` and accepts fork metadata via query params (`forkedAtConversationId`, `forkedAtEntryId`) which are bound through `memory_service_scope(...)`.

**Response resumption pattern**: Keep checkpoint `05` route handlers thin by delegating resume-check/replay/cancel state handling to `memory_service_langchain.MemoryServiceResponseResumer`, mirroring the Quarkus `ResumeResource` style.

**True-streaming checkpoint**: `05-response-resumption` should stream live model tokens via `agent.astream(..., stream_mode="messages")`; avoid `agent.invoke(...)` + post-hoc word-splitting.

**Devcontainer Python tooling**: `uv` is installed in `.devcontainer/Dockerfile` (not via `devcontainer.json` features), so Python checkpoint workflows can run immediately after `wt up`.

**Module build**: `python/pom.xml` runs Python packaging tasks in Docker (`ghcr.io/astral-sh/uv:python3.11-bookworm`) so host setup only requires Docker.

**Package layout**: Python integrations currently include both `python/langchain` (`memory-service-langchain`) and `python/langgraph` (`memory-service-langgraph`) for episodic memory `BaseStore` support.

**LangChain middleware callback gotcha**: In `wrap_model_call`, attach callbacks using `request.model.with_config({"callbacks": [...]})`, not `request.model_settings["callbacks"]`, to avoid `generate_prompt() got multiple values for keyword argument 'callbacks'`.

**LangChain gRPC recorder default**: `MemoryServiceHistoryMiddleware` enables gRPC response recording only when `MEMORY_SERVICE_GRPC_TARGET` is set (or `MEMORY_SERVICE_GRPC_RECORDING_ENABLED=true`).

**FastAPI helper reuse**: Prefer `memory_service_langchain.install_fastapi_authorization_middleware`, `memory_service_scope`, and `memory_service_request` over redefining ContextVars/middleware in checkpoint apps.

**LangGraph threadpool auth gotcha**: `ContextVar` request auth does not reliably flow into LangGraph checkpointer worker threads. `memory_service_scope(...)` now also tracks `conversation_id -> Authorization` for the active scope, and `MemoryServiceCheckpointSaver` falls back to that mapping when the direct getter is empty; this avoids intermittent `{"code":"forbidden","error":"forbidden"}` on checkpoint writes under parallel site tests.
