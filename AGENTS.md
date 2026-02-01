# Memory Service

A memory service for AI agents that stores messages exchanged with LLMs and users, supporting conversation replay and forking.

## Key Concepts
- **User access control**: Conversations are owned by users with read/write/manager/owner access levels.
- **User-facing API**: For chat frontends - list conversations, semantic search, get messages, fork conversations.
- **Agent-facing API**: For retrieving context for LLMs, including summarization support.
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
- `common/chat-frontend/` - Demo chat app frontend (React)

## Development Guidelines

**Coding style**: Java 4-space indent, UTF-8, constructor injection. Packages `io.github.chirino`, classes `PascalCase`, methods/fields `camelCase`.

**Security**: Don't commit secrets; use env vars or Quarkus config (`QUARKUS_*`).

**Commits**: Conventional Commits (`feat:`, `fix:`, `docs:`). Include test commands and config changes.

## Notes for AI Assistants

**ALWAYS compile after changes**:
- Java: `./mvnw compile`
- TypeScript: `npm run lint && npm run build` from `common/chat-frontend/`

**Test output strategy**: When running tests, redirect output to a file and search for errors instead of using `| tail`. This ensures you see all relevant error context:
```bash
./mvnw test > test.log 2>&1
# Then search for errors using Grep tool on test.log
```

**Pre-release**: Changes do not need backward compatibility.  Don't deprecate, just delete.  The datastores are reset frequently.

## Skills

- `/build-test` - Dev mode commands and debugging tips
- `/testing` - Cucumber BDD patterns and failure reporting
- `/openapi-workflow` - OpenAPI change workflow and client regeneration
