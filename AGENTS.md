# Memory Service

A memory service for AI agents that stores messages exchanged with LLMs and users, supporting conversation replay and forking.

**Self-Updating Knowledge:**
When you discover something meaningful about this project during your work—architecture patterns, naming conventions, gotchas, dependency quirks, correct/incorrect assumptions in existing docs — update `AGENTS.md` (or the relevant skill file) immediately so future sessions benefit without re-discovering it. Specifically:

- **Correct** any skill or doc content you find to be outdated or wrong.
- **Refine trigger criteria** in skill descriptions if a skill was loaded but wasn't relevant to the task—tighten its description so it activates more precisely.
- Keep updates concise and factual. Don't bloat files with obvious or generic information.
- Module specific knowlege should be placed into a FACTS.md in the top level directory of that module to avoid poluting AGENTS.md

## Key Concepts
- **Agent apps mediate all operations**: Agent apps are the primary consumers. They sit between end users and the memory service, mediating all interactions.
- **Agent API**: For agent apps - manage conversations, append entries, retrieve context for LLMs. Some agent APIs are designed to be safely exposed to frontend apps (e.g., SPAs) for features like listing conversations, semantic search, viewing messages, and forking.
- **Admin API**: For administrative operations and system management.
- **User access control**: Conversations are owned by users with read/write/manager/owner access levels.
- **Data stores**: PostgreSQL, MongoDB; Redis, Infinispan (caching); PGVector, Qdrant (vector search).
- **Porting Server To Go**: we are porting the ./memory-service java module to ./main.go
- **Docker Compose dev environment**: `docker compose up -d` runs the Go-based memory service using [air](https://github.com/air-verse/air) for hot reloading on port 8083. It runs in parallel with the Java version on port 8082.  I will have it running on the host, so to connect to it don't use `wt`.  Tail the docker logs for the container to check to see if code changes
have completed deploying.

## Quick Reference

**Build**: `./mvnw` (Maven Wrapper)

**Essential commands**:
- `./mvnw quarkus:dev -pl memory-service` - backend dev mode (:8080)
- `./mvnw test` - run tests
- `./mvnw compile` - compile (always run after Java changes)
- `./mvnw -pl python verify` - regenerate Python gRPC stubs and validate Python package build/install (runs in Docker)
- `go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1` - Go BDD runner for Postgres + Keycloak OIDC integration

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
- List endpoints may include `"afterCursor": null`; docs-test JSON assertions should tolerate additive pagination fields.
- Attachment download tokens (`/v1/attachments/download/:token/:filename`) are HMAC-signed with `AttachmentSigningSecret`; keep `MEMORY_SERVICE_ATTACHMENT_SIGNING_SECRET` non-empty, especially with DB attachment stores where storage keys are guessable. The unauthenticated download route is not registered when this secret is unset.
- Go cache serialization gotcha: `model.Entry` has custom JSON marshaling for `content`; keep marshal/unmarshal behavior symmetric or cached memory entries lose content and break sync/list semantics.

## Development Guidelines

**Coding style**: Java 4-space indent, UTF-8, constructor injection. Packages `io.github.chirino`, classes `PascalCase`, methods/fields `camelCase`.

**Error observability**: All 500-level errors MUST produce a full stack trace in the server logs. Never swallow exceptions silently — always log the stack trace for server errors. See `memory-service/FACTS.md` for details.

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

**GORM `record not found` log-noise rule**: If a `record not found` log line is found, treat expected-miss lookups as noise and refactor those call sites from `First(...).Error` to `Limit(1).Find(...)` with `RowsAffected` checks. Keep `First` for true not-found error paths; don't use global logger suppression.

**Module-specific knowledge** lives in `FACTS.md` files within each module directory:
- `python/FACTS.md` — Python/LangChain checkpoint patterns, proxy pattern, streaming, build, gotchas
- `site-tests/FACTS.md` — Docs test filtering, MDX gotchas, user isolation, env var interpolation
- `quarkus/FACTS.md` — Forking curl gotcha
- `spring/FACTS.md` — Memory repository limit gotcha
- `memory-service/FACTS.md` — GlobalExceptionMapper details

**Pre-release**: Changes do not need backward compatibility.  Don't deprecate, just delete.  The datastores are reset frequently.

**Enhancement docs**: When implementing work from `docs/enhancements/`, update the corresponding enhancement doc as you complete each phase. If the implementation diverges from the original design, update the doc to reflect what was actually implemented.
