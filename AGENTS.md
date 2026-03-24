# Memory Service

A memory service for AI agents that stores messages exchanged with LLMs and users, supporting conversation replay and forking.

**Self-Updating Knowledge:**
When you discover something meaningful about this project during your work—architecture patterns, naming conventions, gotchas, dependency quirks, correct/incorrect assumptions in existing docs — update `AGENTS.md` (or the relevant skill file) immediately so future sessions benefit without re-discovering it. Specifically:

- **Correct** any skill or doc content you find to be outdated or wrong.
- **Refine trigger criteria** in skill descriptions if a skill was loaded but wasn't relevant to the task—tighten its description so it activates more precisely.
- Keep updates concise and factual. Don't bloat files with obvious or generic information.
- Module specific knowlege should be placed into a `FACTS.md` in the top level directory of that module to avoid poluting AGENTS.md

## Key Concepts
- **Agent apps mediate all operations**: Agent apps are the primary consumers. They sit between end users and the memory service, mediating all interactions.
- **Agent API**: For agent apps - manage conversations, append entries, retrieve context for LLMs. Some agent APIs are designed to be safely exposed to frontend apps (e.g., SPAs) for features like listing conversations, semantic search, viewing messages, and forking.
- **Admin API**: For administrative operations and system management.
- **User access control**: Conversations are owned by users with read/write/manager/owner access levels.
- **Data stores**: PostgreSQL, MongoDB; Redis, Infinispan (caching); PGVector, Qdrant (vector search).
- **Porting Server To Go**: we are porting the ./memory-service java module to ./main.go
- **dev mode**: `task dev:memory-service` runs the go-based memory service using [air](https://github.com/air-verse/air) for hot reloading on port 8082 and it's dependencies get started with docker compose.

## Quick Reference

**Build**: `./java/mvnw -f java/pom.xml` (Maven Wrapper)

**Essential commands**:
- `./java/mvnw -f java/pom.xml test` - run Java/Quarkus/Spring tests
- `./java/mvnw -f java/pom.xml compile` - compile Java modules (always run after Java changes)
- `task verify:python` - regenerate Python gRPC stubs and validate the LangChain package build/install (runs in Docker)
- `task dev:memory-service` - backend dev mode (:8082)
- `go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1` - Go BDD runner for Postgres + Keycloak OIDC integration
- `cd memory-service-mcp && go build -o mcp-server .` - build the standalone MCP server binary

**Key paths**:
- `contracts/` - OpenAPI (`contracts/openapi/`) and protobuf (`contracts/protobuf/`) sources of truth
- `main.go` + `internal/` - core Go implementation
- `deploy/dev/air.toml` - local Air live-reload config for `task dev:memory-service`
- `deploy/docker/prometheus.yml` - local Docker Compose Prometheus scrape config
- `java/quarkus/examples/chat-quarkus/` - Demo chat app (Quarkus)
- `java/spring/examples/chat-spring/` - Demo chat app (Spring)
- `frontends/chat-frontend/` - Demo chat app frontend (React)
- `internal/sitebdd/` - Documentation test module (MDX `<TestScenario>/<CurlTest>` to Go/Cucumber pipeline)
- `internal/cmd/mcp/` - MCP server integrated into main binary (`./memory-service mcp`)
- `memory-service-mcp/` - Standalone MCP binary wrapper (build with `cd memory-service-mcp && go build -o mcp-server .`; `.mcp.json` uses `${PWD}` for portable paths)

**API gotchas**:
- Conversation search endpoint is `/v1/conversations/search` (not `/v1/search`).
- Fork creation is implicit on first append to a new conversation ID using `forkedAtConversationId` + `forkedAtEntryId`; `POST /v1/conversations/{conversationId}/entries/{entryId}/fork` is obsolete.
- Entry listing uses `forks=all` to return entries from all branches in a fork tree (not `allForks=true`).
- Enhancement doc `implemented/007-multi-agent-support.md` is older agent-scoped memory work; do not treat it as the design for parent/child agent conversations or conversation-lineage APIs.
- Current `clientId` semantics are app/system identity, not logical agent identity; multi-agent apps may need multiple `agentId` values under one authenticated client.
- Conversation `clientId` is internal/admin-only metadata: user-facing REST conversation payloads must not expose it, while admin conversation APIs may.
- Conversation identity migration status: public contracts and conversation APIs now use conversation-level `clientId` plus optional conversation-level `agentId`, but Go stores still persist entry-level `clientId`/`agentId` and still use agent-scoped context cache/query paths internally; Enhancement 089 is partial, not complete.
- Async sub-agent orchestration now uses explicit model-facing control tools instead of implicit end-of-turn joins. The Quarkus default tool names are `agentSend`, `agentPoll`, `waitTask`, and `agentStop`; follow-up messages to an existing running child conversation require an explicit mode such as `queue` or `interrupt`.
- In gRPC `memory/v1/memory_service.proto`, response recorder fields use snake_case (`conversation_id`).
- In gRPC `EventStreamService.SubscribeEvents`, `SubscribeEventsRequest.conversation_ids` exists in the proto but is not currently applied by the server implementation; only `kinds` filtering is enforced today.
- Response recording naming is intentionally split by scope: client-side lifecycle APIs use `ResponseRecordingManager` / `RecordingSession`, while record-only server/proto pieces use `ResponseRecorderService` and recorder handles; avoid renaming the umbrella concept back to `ResponseRecorder`.
- Event stream routing is now user-scoped: publish paths resolve recipient user IDs up front, REST/gRPC `/v1/events` subscribers subscribe by authenticated user, and Redis/PostgreSQL transports fan out on per-user channels plus shared broadcast/admin channels instead of broadcasting every business event to every subscriber.
- List endpoints may include `"afterCursor": null`; docs-test JSON assertions should tolerate additive pagination fields.
- Attachment download tokens (`/v1/attachments/download/:token/:filename`) are HMAC-signed with `AttachmentSigningSecret`; keep `MEMORY_SERVICE_ATTACHMENT_SIGNING_SECRET` non-empty, especially with DB attachment stores where storage keys are guessable. The unauthenticated download route is not registered when this secret is unset.
- Go cache serialization gotcha: `model.Entry` has custom JSON marshaling for `content`; keep marshal/unmarshal behavior symmetric or cached memory entries lose content and break sync/list semantics.
- Repo-root Docker build gotcha: the release `Dockerfile` builds without `.git` metadata in context, so Go commands in that image must use `-buildvcs=false` or `go build` fails with `error obtaining VCS status`.
- Devcontainer Go gotcha: keep `go.mod` using a language version in the `go` directive (for example `go 1.24`) and put patch-level pinning in `toolchain go1.24.6`; if `.devcontainer/Dockerfile` installs an older Go version, Go auto-downloads the newer toolchain into `GOPATH`, so keep `/go` writable by `vscode` (or set `GOPATH` to a user-owned path) to avoid `mkdir .../golang.org/toolchain: permission denied`.
- Devcontainer site-test gotcha: `.devcontainer/Dockerfile` needs `libsqlite3-dev` installed or `task test:site` / `go build -tags='site_tests sqlite_fts5' ./internal/sitebdd/` fails while compiling `github.com/asg017/sqlite-vec-go-bindings` with `fatal error: sqlite3.h: No such file or directory`.
- Memory usage counters increment only on direct fetch reads (`GET /v1/memories`, gRPC `GetMemory`); search endpoints can return usage metadata with `include_usage` but do not increment counters.
- Quarkus REST client module builds can require `-am` (`./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus -am ...`) so `memory-service-contracts` is built in the same reactor.
- Site-doc checkpoint builds run as standalone Maven apps; after changing shared Java snapshot modules they depend on, use `./java/mvnw -f java/pom.xml -pl <module> -am install -DskipTests` rather than only `compile`, or site tests may keep using stale artifacts from `~/.m2`.
- The demo Quarkus image Dockerfile lives at `./java/quarkus/examples/chat-quarkus/Dockerfile`; repo-root compose/task commands should use that path.
- Contract specs live in repo-root `contracts/`; Java modules should resolve them from `${maven.multiModuleProjectDirectory}/../contracts`, and the `java/memory-service-contracts` module publishes them via `../../contracts`.
- The Maven wrapper and reactor root live under `java/`; repo-root Maven commands must use `./java/mvnw -f java/pom.xml ...`.


## Development Guidelines

**Security**: Don't commit secrets; pass them with env vars
**Commits**: Conventional Commits (`feat:`, `fix:`, `docs:`). Include test commands and config changes.

## Notes for AI Assistants

**Verify only changed modules (required)**:
- Go changes (`main.go`, `internal/`, `go.mod`, `go.sum`, etc.): run Go build for affected packages (use `go build ./...` when scope is broad).
- Java/Quarkus/Spring changes: run Maven compile for affected modules (prefer `-pl` targeted modules; use full `./java/mvnw -f java/pom.xml compile` only when scope is broad).
- Frontend changes (`frontends/chat-frontend/`): run `npm run lint && npm run build` from `frontends/chat-frontend/`.
- Python changes (`python/`): run `python3 -m compileall` on changed files/modules; run `task verify:python` when Python packaging/stubs are impacted.
- Cross-stack or uncertain impact: run all relevant checks above; full-repo compile is optional unless needed by the change scope.

**Taskfile shell compatibility**: Task commands execute via `sh`; use POSIX redirection (`>/dev/null 2>&1`) instead of shell-specific forms like `&>` or malformed `2&>1`.
- **TypeScript example task gotcha**: Under `typescript/examples/vecelai/doc-checkpoints/`, both `05-response-resumption/` and `05b-response-resumption/` exist; keep `Taskfile.yml` entries explicit instead of assuming a sequential directory list.

**Test output strategy**: When running tests, redirect output to a file and search for errors instead of using `| tail`. This ensures you see all relevant error context:
```bash
task test:go > test.log 2>&1
# Then search for errors using Grep tool on test.log
```

**Module-specific knowledge** lives in `FACTS.md` files within each module directory:
- `./frontends/chat-frontend/FACTS.md`
- `./java/memory-service-contracts/FACTS.md`
- `./java/quarkus/FACTS.md`
- `./python/FACTS.md`
- `./internal/sitebdd/FACTS.md`
- `./internal/FACTS.md`
- `./site/FACTS.md`
- `./java/spring/FACTS.md`

**Pre-release**: Changes do not need backward compatibility.  Don't deprecate, just delete.  The datastores are reset frequently.

**Enhancement docs**: Proposed enhancements stay in `docs/enhancements/`. Non-proposed enhancements live in `docs/enhancements/<status>/` where status is `implemented`, `partial`, or `superseded`. When implementing work from `docs/enhancements/`, update the corresponding enhancement doc as you complete each phase. If the implementation diverges from the original design, update the doc to reflect what was actually implemented.

**Workarounds**: If you implement a workaround (e.g., to avoid a bug, limitation, or missing feature in a dependency), you MUST:
1. Record it in `./WORKAROUNDS.md` with: what the workaround is, why it was needed, and what a proper fix might look like.
2. Inform the user that a workaround was added so they can decide whether to accept it or pursue a better solution.
