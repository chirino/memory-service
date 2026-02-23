# Memory Service

A memory service for AI agents that stores messages exchanged with LLMs and users, supporting conversation replay and forking.

**Self-Updating Knowledge:**
When you discover something meaningful about this project during your work—architecture patterns, naming conventions, gotchas, dependency quirks, correct/incorrect assumptions in existing docs—update `AGENTS.md` (or the relevant skill file) immediately so future sessions benefit without re-discovering it. Specifically:

- **Correct** any skill or doc content you find to be outdated or wrong.
- **Refine trigger criteria** in skill descriptions if a skill was loaded but wasn't relevant to the task—tighten its description so it activates more precisely.
- Keep updates concise and factual. Don't bloat files with obvious or generic information.

## Key Concepts
- **Agent apps mediate all operations**: Agent apps are the primary consumers. They sit between end users and the memory service, mediating all interactions.
- **Agent API**: For agent apps - manage conversations, append entries, retrieve context for LLMs. Some agent APIs are designed to be safely exposed to frontend apps (e.g., SPAs) for features like listing conversations, semantic search, viewing messages, and forking.
- **Admin API**: For administrative operations and system management.
- **User access control**: Conversations are owned by users with read/write/manager/owner access levels.
- **Data stores**: PostgreSQL, MongoDB; Redis, Infinispan (caching); PGVector, Qdrant (vector search).

## Quick Reference

**Build**: `./mvnw` (Maven Wrapper)

**Essential commands**:
- `./mvnw quarkus:dev -pl memory-service` - backend dev mode (:8080)
- `./mvnw test` - run tests
- `./mvnw compile` - compile (always run after Java changes)
- `./mvnw -pl python verify` - regenerate Python gRPC stubs and validate Python package build/install (runs in Docker)

**Key paths**:
- `memory-service-contracts/` - OpenAPI + proto sources of truth
- `memory-service/` - core implementation
- `quarkus/examples/chat-quarkus/` - Demo chat app (Quarkus)
- `spring/examples/chat-spring/` - Demo chat app (Spring)
- `frontends/chat-frontend/` - Demo chat app frontend (React)
- `site-tests/` - Documentation test module (MDX `<TestScenario>/<CurlTest>` to Cucumber pipeline)

**API gotchas**:
- Conversation search endpoint is `/v1/conversations/search` (not `/v1/search`).
- In gRPC `memory/v1/memory_service.proto`, response recorder fields use snake_case (`conversation_id`).

## Development Guidelines

**Coding style**: Java 4-space indent, UTF-8, constructor injection. Packages `io.github.chirino`, classes `PascalCase`, methods/fields `camelCase`.

**Error observability**: All 500-level errors MUST produce a full stack trace in the server logs. The `GlobalExceptionMapper` in `memory-service/src/main/java/.../api/GlobalExceptionMapper.java` catches unhandled exceptions and logs them with `LOG.errorf(e, ...)`. When adding new endpoints or error paths, never swallow exceptions silently — always log the stack trace for server errors.

**Security**: Don't commit secrets; use env vars or Quarkus config (`QUARKUS_*`).

**Commits**: Conventional Commits (`feat:`, `fix:`, `docs:`). Include test commands and config changes.


## Worktree-Isolated Execution

This project has a `.devcontainer/devcontainer.json` and uses `wt` (git worktree manager). If the `wt` command is available, commands that open ports, start services, use shared resources, run builds with artifacts at fixed paths, or run integration/end-to-end tests **MUST** be run inside the devcontainer using `wt exec -- <command>`. Run `wt up` first to ensure the devcontainer is running. Do NOT use `wt exec` for read-only operations (file reads, git commands, editing, linting). See [.skills/wt/SKILL.md](.skills/wt/SKILL.md) for full details including proxy access to container services.

## Notes for AI Assistants

**ALWAYS compile after changes**:
- Java: `./mvnw compile`
- TypeScript: `npm run lint && npm run build` from `frontends/chat-frontend/`

**Test output strategy**: When running tests, redirect output to a file and search for errors instead of using `| tail`. This ensures you see all relevant error context:
```bash
./mvnw test > test.log 2>&1
# Then search for errors using Grep tool on test.log
```

**Docs test filtering**: `site-tests` scenarios are taggable by framework and checkpoint. For Python-only loops use:
```bash
./mvnw -Psite-tests -pl site-tests -Dcucumber.filter.tags=@python surefire:test
```

**Python checkpoint lineage**: Python docs checkpoints `04-conversation-forking`, `05-response-resumption`, `06-sharing`, and `07-with-search` are intentionally rebased from `03-with-history` (not chained from each other), mirroring the Quarkus tutorial branching model.
**Python proxy pattern**: Use `memory_service_langchain.MemoryServiceProxy` plus `to_fastapi_response(...)` in checkpoint apps for API-like Memory Service passthrough methods (`list_conversation_entries`, `list_memberships`, etc.) instead of ad-hoc `memory_service_request(...)` calls.
**Python chat simplification**: For checkpoint-style FastAPI chat routes, keep a single stateful agent and use `extract_assistant_text(...)`; fork metadata is passed via `memory_service_scope(...)` and consumed by the LangChain integration (middleware + checkpointer).
**Python request scope helper**: Prefer `memory_service_langchain.memory_service_scope(conversation_id, forked_at_conversation_id, forked_at_entry_id)` for route context binding.
**Quarkus/Spring forking curl gotcha**: Checkpoint `04-conversation-forking` chat routes are `text/plain`; to demo fork creation with curl, create root turns via `/chat/{id}` then append forked entries via Memory Service `/v1/conversations/{forkId}/entries` with `forkedAtConversationId`/`forkedAtEntryId`.
**Python forking parity**: Python checkpoint `04-conversation-forking` keeps `/chat/{id}` as `text/plain` and accepts fork metadata via query params (`forkedAtConversationId`, `forkedAtEntryId`) which are bound through `memory_service_scope(...)`.
**Python response resumption pattern**: Keep checkpoint `05` route handlers thin by delegating resume-check/replay/cancel state handling to `memory_service_langchain.MemoryServiceResponseResumer`, mirroring the Quarkus `ResumeResource` style.
**Python true-streaming checkpoint**: `05-response-resumption` should stream live model tokens via `agent.astream(..., stream_mode=\"messages\")`; avoid `agent.invoke(...)` + post-hoc word-splitting.

**Devcontainer Python tooling**: `uv` is installed in `.devcontainer/Dockerfile` (not via `devcontainer.json` features), so Python checkpoint workflows can run immediately after `wt up`.

**Python module build**: `python/pom.xml` runs Python packaging tasks in Docker (`ghcr.io/astral-sh/uv:python3.11-bookworm`) so host setup only requires Docker.
**Python package layout**: Python integration is now a single package at `python/langchain` (`memory-service-langchain`); the separate `python/langgraph` package was removed.

**MDX `CodeFromFile` gotcha**: Match strings must be unique in the target file. Prefer function-signature anchors or `lines="start-end"` over route strings with `{...}` placeholders to avoid MDX parsing/smart-quote mismatches.
**Checkpoint 04 forking (Java docs)**: `quarkus` and `spring` checkpoint 04 keep chat handlers effectively unchanged; fork metadata is demonstrated by appending entries directly to the Memory Service API with `forkedAtConversationId`/`forkedAtEntryId` and exposing `listConversationForks` on the proxy resource/controller.
**`site-tests` env var gotcha**: Curl command interpolation supports `${VAR}` but not shell default syntax like `${VAR:-default}`. Use explicit values or plain `${VAR}` placeholders in testable docs.

**LangChain middleware callback gotcha**: In `wrap_model_call`, attach callbacks using `request.model.with_config({"callbacks": [...]})`, not `request.model_settings["callbacks"]`, to avoid `generate_prompt() got multiple values for keyword argument 'callbacks'`.

**LangChain gRPC recorder default**: `MemoryServiceHistoryMiddleware` enables gRPC response recording only when `MEMORY_SERVICE_GRPC_TARGET` is set (or `MEMORY_SERVICE_GRPC_RECORDING_ENABLED=true`).

**Python FastAPI helper reuse**: Prefer `memory_service_langchain.install_fastapi_authorization_middleware`, `memory_service_scope`, and `memory_service_request` over redefining ContextVars/middleware in checkpoint apps.

**Pre-release**: Changes do not need backward compatibility.  Don't deprecate, just delete.  The datastores are reset frequently.

**Enhancement docs**: When implementing work from `docs/enhancements/`, update the corresponding enhancement doc as you complete each phase. If the implementation diverges from the original design, update the doc to reflect what was actually implemented.
