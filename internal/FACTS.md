# Internal Module Facts

**GORM `record not found` log-noise rule**: If a `record not found` log line is found, treat expected-miss lookups as noise and refactor those call sites from `First(...).Error` to `Limit(1).Find(...)` with `RowsAffected` checks. Keep `First` for true not-found error paths; don't use global logger suppression.

**Error observability**: All 500-level errors MUST produce a full stack trace in the server logs. Never swallow exceptions silently — always log the stack trace for server errors.

**Embedded OPA Rego syntax**: Rego policies using `allow if { ... }` must import `future.keywords.if`; missing this import can fail policy loading and accidentally disable route-level authorization if startup is not fail-closed.

**Default episodic OPA policy**: Built-in authz is deny-by-default and only allows `["user", <subject>, ...]` namespaces. Built-in attribute extraction persists plaintext `{"namespace":"user","sub":<subject>}` in `policy_attributes`, and built-in search filter injection enforces both keys for non-admin callers.

**Episodic indexer pending rows**: `registry/episodic.PendingMemory.Namespace` is a single RS-delimited string (`\x1f`), not `[]string`; tests/mocks must provide encoded namespace values.

**Postgres append auto-create race**: Concurrent `AppendEntries` calls for the same missing conversation ID can race on root conversation/group creation and hit `23505` on `conversation_groups_pkey`. The append path must treat that unique-violation as a concurrent-create win, reload the conversation, and continue instead of returning 500.
**Postgres duplicate-key diagnostics**: When conversation/group auto-create paths hit `23505`, the store now emits structured warning logs with `constraint`, `table`, and Postgres `detail` plus `userID`, `conversationID`, and `conversationGroupID` so CI flakes can be mapped back to scenario-specific IDs.

**gRPC recorder disconnect cleanup**: In `ResponseRecorderServer.Record`, if the stream fails with gRPC/ctx `CANCELED` or `DEADLINE_EXCEEDED` after a recorder has been created, call `recorder.Complete()` before returning so the locator/cache registry entry for that conversation is removed.

**gRPC cancel semantics**: `ResponseRecorderServer.Record` subscribes to `resumer.CancelStream(conversationID)` once the first chunk sets `conversation_id`; when cancel is requested it completes the recorder (removing locator/cache registry) and returns unary `RecordResponse{status=RECORD_STATUS_CANCELLED}`.
**Fork ancestry nil stop-point semantics**: In entry ancestry filtering (Postgres and Mongo), a nil `forkedAtEntryId` means "exclude all inherited entries from that ancestor and jump directly to the child conversation". This makes the first newly appended message the first visible entry in the fork.
**BDD Mongo SQL-skip semantics**: `internal/bdd/steps_sql.go` treats `nil` rows as "skip SQL assertions"; `MongoTestDB.ExecSQL` intentionally returns `nil, nil`, so SQL verification steps in shared feature files do not validate datastore state on Mongo runs unless parallel Mongo-specific assertions are added.
**BDD Mongo query variable expansion**: Mongo query docstrings can contain Mongo `$` operators/field paths (for example `$ne`, `$match`, `$task_body.path`). Use brace-only expansion (`${var}`) for scenario variables; do not run generic `os.Expand` over whole Mongo query strings or `$ne`/`$foo` will be treated as missing variables.
