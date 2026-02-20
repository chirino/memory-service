# Memory Service

A memory service for AI agents that stores messages exchanged with LLMs and users, supporting conversation replay and forking.

**Self-Updating Knowledge:**
When you discover something meaningful about this project during your work—architecture patterns, naming conventions, gotchas, dependency quirks, correct/incorrect assumptions in existing docs—update `CLAUDE.md` (or the relevant skill file) immediately so future sessions benefit without re-discovering it. Specifically:

- **Add** newly learned project-specific facts that would pertient in most assitant interactions.
- **Correct** any skill or doc content you find to be outdated or wrong.
- **Refine trigger criteria** in skill descriptions if a skill was loaded but wasn't relevant to the task—tighten its description so it activates more precisely.
- Keep updates concise and factual. Don't bloat files with obvious or generic information.

## Key Concepts
- **Agent apps mediate all operations**: Agent apps are the primary consumers. They sit between end users and the memory service, mediating all interactions.
- **Agent API**: For agent apps - manage conversations, append entries, retrieve context for LLMs, summarization. Some agent APIs are designed to be safely exposed to frontend apps (e.g., SPAs) for features like listing conversations, semantic search, viewing messages, and forking.
- **Admin API**: For administrative operations and system management.
- **User access control**: Conversations are owned by users with read/write/manager/owner access levels.
- **Data stores**: PostgreSQL, MongoDB; Redis, Infinispan (caching); PGVector, MongoDB (vector search).

## Quick Reference

**Build**: `./mvnw` (Maven Wrapper)

**Essential commands**:
- `./mvnw quarkus:dev -pl memory-service` - backend dev mode (:8080)
- `./mvnw test` - run tests
- `./mvnw compile` - compile (always run after Java changes)

**Key paths**:
- `memory-service-contracts/` - OpenAPI + proto sources of truth
- `memory-service/` - core implementation
- `quarkus/examples/chat-quarkus/` - Demo chat app (Quarkus)
- `frontends/chat-frontend/` - Demo chat app frontend (React)

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

**Pre-release**: Changes do not need backward compatibility.  Don't deprecate, just delete.  The datastores are reset frequently.

**Enhancement docs**: When implementing work from `docs/enhancements/`, update the corresponding enhancement doc as you complete each phase. If the implementation diverges from the original design, update the doc to reflect what was actually implemented.

## Learned Facts
- `langchain4j-qdrant:1.0.0-beta3` is ABI-coupled to `io.qdrant:client:1.13.0`; forcing newer `io.qdrant:client` versions (e.g. `1.16.x`) causes runtime `NoSuchMethodError` in `PointIdFactory.id(UUID)`.
