# Internal Module Facts

**GORM `record not found` log-noise rule**: If a `record not found` log line is found, treat expected-miss lookups as noise and refactor those call sites from `First(...).Error` to `Limit(1).Find(...)` with `RowsAffected` checks. Keep `First` for true not-found error paths; don't use global logger suppression.

**Error observability**: All 500-level errors MUST produce a full stack trace in the server logs. Never swallow exceptions silently — always log the stack trace for server errors.

**Embedded OPA Rego syntax**: Rego policies using `allow if { ... }` must import `future.keywords.if`; missing this import can fail policy loading and accidentally disable route-level authorization if startup is not fail-closed.

**Default episodic OPA policy**: Built-in authz is deny-by-default and only allows `["user", <subject>, ...]` namespaces. Built-in attribute extraction persists plaintext `{"namespace":"user","sub":<subject>}` in `policy_attributes`, and built-in search filter injection enforces both keys for non-admin callers.

**Episodic indexer pending rows**: `registry/episodic.PendingMemory.Namespace` is a single RS-delimited string (`\x1e`), not `[]string`; tests/mocks must provide encoded namespace values.

**Postgres append auto-create race**: Concurrent `AppendEntries` calls for the same missing conversation ID can race on root conversation/group creation and hit `23505` on `conversation_groups_pkey`. The append path must treat that unique-violation as a concurrent-create win, reload the conversation, and continue instead of returning 500.
**Postgres duplicate-key diagnostics**: When conversation/group auto-create paths hit `23505`, the store now emits structured warning logs with `constraint`, `table`, and Postgres `detail` plus `userID`, `conversationID`, and `conversationGroupID` so CI flakes can be mapped back to scenario-specific IDs.
**oapi route registration gotcha**: Do not call generated `RegisterHandlers(...)` while other manual registrations for the same endpoints are mounted; Gin panics on duplicate `METHOD + PATH` registrations.
**Serve wrapper routing architecture**: `internal/cmd/serve/server.go` now exposes Agent/Admin HTTP endpoints through generated wrappers wired in `internal/cmd/serve/wrapper_routes.go`; runtime legacy route mounting in `serve` has been removed.
**Memories wrapper-native migration**: `/v1/memories*` endpoints are bound through generated wrappers backed by `internal/plugin/route/memories/wrapper_adapter.go`, which directly delegates to memories route logic instead of proxying through the legacy router.
**Transfers wrapper-native migration**: `/v1/ownership-transfers*` endpoints are bound through generated wrappers and handled directly via exported transfer route helpers (`internal/plugin/route/transfers/transfers.go`) instead of proxying through the legacy router.
**Memberships wrapper-native migration**: memberships endpoints under `/v1/conversations/:conversationId/memberships` are bound through generated wrappers and handled directly via exported membership route helpers (`internal/plugin/route/memberships/memberships.go`) instead of proxying through the legacy router.
**Attachments wrapper-native migration**: `/v1/attachments*` endpoints are bound through generated wrappers and handled directly via exported attachment route helpers (`internal/plugin/route/attachments/attachments.go`) instead of proxying through the legacy router.
**Signed attachment download auth parity**: `/v1/attachments/download/:token/:filename` must remain unauthenticated (token-auth only). In wrapper mode, register this endpoint with a wrapper that has no auth middleware; otherwise BDD signed-download scenarios fail with 401.
**Search wrapper-native migration**: `/v1/conversations/search`, `/v1/conversations/index`, and `/v1/conversations/unindexed` are bound through generated wrappers and handled directly via exported search route helpers (`internal/plugin/route/search/search.go`).
**Wrapper auth middleware gotcha**: Wrapper-native endpoints must run auth (and for memories, client-id) as wrapper middlewares in `internal/cmd/serve/wrapper_routes.go`; otherwise role checks can fail even when tokens are valid.
**Entries invalid-id parity**: Legacy `GET /v1/conversations/:conversationId/entries` returns `404 {"code":"not_found","error":"conversation not found"}` when `conversationId` is not a UUID. Wrapper binding emits 400 by default, so wrapper error handling must map this specific case back to legacy 404.

**gRPC recorder disconnect cleanup**: In `ResponseRecorderServer.Record`, if the stream fails with gRPC/ctx `CANCELED` or `DEADLINE_EXCEEDED` after a recorder has been created, call `recorder.Complete()` before returning so the locator/cache registry entry for that conversation is removed.

**gRPC cancel semantics**: `ResponseRecorderServer.Record` subscribes to `resumer.CancelStream(conversationID)` once the first chunk sets `conversation_id`; when cancel is requested it completes the recorder (removing locator/cache registry) and returns unary `RecordResponse{status=RECORD_STATUS_CANCELLED}`.
**Fork ancestry nil stop-point semantics**: In entry ancestry filtering (Postgres and Mongo), a nil `forkedAtEntryId` means "exclude all inherited entries from that ancestor and jump directly to the child conversation". This makes the first newly appended message the first visible entry in the fork.
**BDD Mongo SQL-skip semantics**: `internal/bdd/steps_sql.go` treats `nil` rows as "skip SQL assertions"; `MongoTestDB.ExecSQL` intentionally returns `nil, nil`, so SQL verification steps in shared feature files do not validate datastore state on Mongo runs unless parallel Mongo-specific assertions are added.
**BDD Mongo query variable expansion**: Mongo query docstrings can contain Mongo `$` operators/field paths (for example `$ne`, `$match`, `$task_body.path`). Use brace-only expansion (`${var}`) for scenario variables; do not run generic `os.Expand` over whole Mongo query strings or `$ne`/`$foo` will be treated as missing variables.
**Godog report format switch**: BDD report writers honor `GODOG_REPORT_FORMAT`; default is `junit` (`.xml`), and setting `GODOG_REPORT_FORMAT=cucumber` writes Cucumber JSON (`.json`) under `GODOG_REPORT_DIR`.
**Cucumber report chunking tool**: `go run ./internal/cmd/create-cucumber-reports` scans `*/reports/*.json`, normalizes array/object report roots, and writes chunked combined files to `.artifacts/cucumber-report-###.json` (default max 50 features per file).

**Search route parity behavior**: Go `POST /v1/conversations/search` now supports `searchType`, `afterCursor`, and `groupByConversation`; `searchType` accepts a string or array (e.g. `["semantic","fulltext"]`) and applies `limit` per requested type. Multi-type pagination uses an opaque base64url JSON cursor carrying per-type sub-cursors.
