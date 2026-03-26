---
status: implemented
---

# Enhancement 094: Archive Operations Replace Soft Delete APIs

> **Status**: Implemented.

## Summary

Conversation and episodic-memory soft-delete APIs now use archive semantics.
Storage timestamps moved from `deletedAt` / `deleted_at` to `archivedAt` / `archived_at`, while REST and gRPC expose a synthetic `archived` boolean and use update-style operations instead of delete endpoints for archival.
Conversation delete operations were removed rather than retained as aliases, the admin/archive vocabulary was propagated into filters, stats, and frontend UI labels, and archived conversations remain readable until eviction hard-deletes them.
Archived memories now follow the same model: archive is an update-state change rather than a delete action, archived memories remain directly readable, and memory list/search/namespace APIs expose the same tri-state archive filter used by conversation lists.
Because test/dev datastores are routinely reset in this project, the canonical PostgreSQL and SQLite schema files were updated in place to the archive contract instead of adding compatibility migrations.
The Java REST/grpc client checkpoints and site-doc build path were also aligned so generated client code, example proxies, and site tests match the archive-only contract.

## Motivation

The previous API mixed two different ideas:

- user-facing operations said "delete" even when they only hid data behind a timestamp
- admin APIs had separate delete and restore actions even though both changed the same archival state

That made the contract misleading and forced clients to understand storage-oriented soft-delete details.

## Design

- Rename datastore timestamps from `deleted_at` to `archived_at` for conversations, conversation groups, and episodic memories.
- Remove conversation delete operations rather than keeping them as compatibility aliases.
- Replace archive-triggering REST deletes with PATCH/update operations:
  - `PATCH /v1/conversations/{conversationId}` with `{ "archived": true }`
  - `PATCH /v1/memories?...` with `{ "archived": true }`
  - `PATCH /v1/admin/conversations/{id}` with `{ "archived": true|false }`
- Remove the admin restore endpoint and treat unarchive as `archived=false` on the admin conversation PATCH.
- Replace the admin conversation-list booleans with one archive filter:
  - `archived=exclude` (default, active only)
  - `archived=include` (active and archived)
  - `archived=only` (archived only)
- Add the same `archived=exclude|include|only` filter to user conversation listing.
- Add the same `archived=exclude|include|only` filter to memory GET/search/namespace-list APIs, and allow archived memories to be fetched directly through normal user reads.
- Apply the same `archived=exclude|include|only` semantics to semantic/vector memory search, not just attribute-only search.
- Rename admin summary stats fields from delete terminology to archive terminology:
  - `conversationGroups.archived`
  - `conversationGroups.oldestArchivedAt`
  - `memories.archived`
  - `memories.oldestArchivedAt`
- Expose synthetic `archived` booleans in REST/gRPC resource payloads instead of timestamp fields.
- Update the chat frontend to call conversation PATCH with `{ archived: true }` and relabel destructive UI affordances from `Delete conversation` to `Archive conversation`.
- Align current design/site/example docs to the archive contract and add current-contract notes to older enhancement docs that still describe pre-094 delete/restore behavior.
- Keep archived conversations readable through normal user GET/list/entry-read flows until eviction hard-deletes them.
- Keep archived memories readable through normal user GET/search/namespace flows until memory eviction hard-deletes tombstones.
- Keep archived-memory semantic search vectors available for `archived=include|only`, while filtering them out for `archived=exclude`.
- Treat archive as an update event and emit delete events only when eviction hard-deletes archived conversations.
- Treat memory archive as an update-state change rather than a delete event in the memory lifecycle timeline.
- Use archive metadata in vector stores to pre-filter semantic search results, then post-filter hydrated memory rows again for correctness.
- Keep eviction as a hard delete, but base retention on `archivedAt`.
- Update resettable PostgreSQL and SQLite schema sources directly to `archived_at` so fresh databases match the current store/query contract.
- Align Spring and Quarkus checkpoint/client proxy layers with the removed delete endpoints by routing conversation archival through update/PATCH requests.
- Make the site-doc Java bootstrap use `clean install` so removed OpenAPI/proto models do not leave stale generated Java sources in `target/` and break later docs builds.

## Testing

- Update REST and gRPC BDD scenarios to use archive/update requests and archived assertions.
- Update admin REST/site docs and frontend interactions to remove restore/delete wording for conversation archival.
- Rebuild generated OpenAPI and protobuf code, then compile all Go packages.
- Run the full Go and site-doc test suites after the archive cleanup lands.

## Tasks

- [x] Rename storage-layer archive timestamps and related query filters.
- [x] Replace archive-triggering REST delete endpoints with PATCH/update endpoints.
- [x] Replace gRPC delete RPCs for conversations and memories with update RPCs carrying `archived`.
- [x] Update admin archive/restore flow to a single PATCH endpoint.
- [x] Drop conversation delete operations instead of preserving delete aliases.
- [x] Rename admin list filters and summary stats from delete terminology to archive terminology.
- [x] Update chat-frontend conversation actions to PATCH archive and relabel the UI to Archive.
- [x] Add tri-state archive filters to admin and user conversation lists.
- [x] Preserve archived conversations for user reads until eviction.
- [x] Emit `updated` for archive state changes and reserve `deleted` for hard deletes.
- [x] Add tri-state archive filters and archived-read behavior to memory APIs.
- [x] Rename internal memory archive semantics away from delete-oriented authz/store methods.
- [x] Make memory lifecycle events treat archive as update rather than delete.
- [x] Update generated clients, handlers, and BDD helpers.
- [x] Update docs and AGENTS guidance for the archive terminology.
- [x] Align current docs/examples and mark older enhancement docs as historical where needed.
- [x] Update resettable PostgreSQL and SQLite schema sources to `archived_at`.
- [x] Align Spring and Quarkus proxy/example layers with archive-via-update semantics.
- [x] Make site-doc Java artifact bootstrap resilient to removed generated models by using `clean install`.
- [x] Re-run and pass the full Go and site test suites.

## Files to Modify

| File | Change |
| --- | --- |
| `contracts/openapi/openapi.yml` | Replace delete-based archive operations with PATCH/update requests and `archived` fields |
| `contracts/openapi/openapi-admin.yml` | Merge admin delete/restore into archive PATCH and rename admin filters/stats |
| `contracts/protobuf/memory/v1/memory_service.proto` | Replace delete RPCs for conversations/memories with update requests carrying `archived` |
| `internal/plugin/route/conversations/conversations.go` | Handle conversation archive via PATCH |
| `internal/plugin/route/memories/memories.go` | Handle memory archive via PATCH and expose archive filters on memory reads/search |
| `internal/plugin/route/admin/admin.go` | Handle admin archive/restore via one PATCH endpoint |
| `internal/grpc/server.go` | Map archive updates, memory archive filters, and synthetic `archived` response fields |
| `internal/plugin/store/**` | Rename conversation/memory storage timestamps, memory archive semantics, and retention queries to `archived_at` |
| `internal/plugin/store/postgres/db/schema.sql` | Update resettable PostgreSQL schema to the archive contract |
| `internal/plugin/store/sqlite/db/schema.sql` | Update resettable SQLite schema to the archive contract |
| `internal/bdd/**` | Update archive-focused tests and helpers |
| `frontends/chat-frontend/src/App.tsx` | Switch conversation archive action from delete endpoint to PATCH with `archived: true` |
| `frontends/chat-frontend/src/components/chat-panel.tsx` | Relabel conversation menu action from Delete to Archive |
| `java/spring/memory-service-rest-spring/**` | Replace removed conversation delete-client usage with archive-via-update requests |
| `java/quarkus/memory-service-extension/runtime/**` | Replace removed conversation delete-client usage with archive-via-update requests |
| `internal/sitebdd/site_test.go` | Use `clean install` for Java checkpoint bootstrap so stale generated sources do not break site builds |
| `site/src/pages/docs/concepts/admin-apis.mdx` | Document PATCH archive/unarchive and archive-named admin filters |
| `site/src/pages/docs/concepts/memories.md` | Document memory PATCH archive semantics, tri-state archive filters, and archive-as-update events |
| `docs/design.md` | Replace conversation delete references with archive PATCH semantics |
| `python/examples/**/README.md` | Remove obsolete conversation delete endpoint references |
| `docs/enhancements/{implemented,partial}/**` | Add current-contract notes where older enhancement docs still describe pre-094 delete/archive behavior |

## Verification

```bash
go generate ./...
go build ./...
python3 -m compileall python/examples/langchain/chat-langchain python/examples/langgraph/chat-langgraph
cd frontends/chat-frontend && npm run generate && npm run lint && npm run build
task test:go
task test:site
```

## Non-Goals

- Renaming hard-delete admin operations such as force-deleting an episodic memory by ID
- Changing attachment hard-delete behavior
