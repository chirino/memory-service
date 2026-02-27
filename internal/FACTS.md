# Internal Module Facts

**GORM `record not found` log-noise rule**: If a `record not found` log line is found, treat expected-miss lookups as noise and refactor those call sites from `First(...).Error` to `Limit(1).Find(...)` with `RowsAffected` checks. Keep `First` for true not-found error paths; don't use global logger suppression.

**Error observability**: All 500-level errors MUST produce a full stack trace in the server logs. Never swallow exceptions silently — always log the stack trace for server errors.

**Embedded OPA Rego syntax**: Rego policies using `allow if { ... }` must import `future.keywords.if`; missing this import can fail policy loading and accidentally disable route-level authorization if startup is not fail-closed.

**Default episodic OPA policy**: Built-in authz is deny-by-default and only allows `["user", <subject>, ...]` namespaces. Built-in attribute extraction persists plaintext `{"namespace":"user","sub":<subject>}` in `policy_attributes`, and built-in search filter injection enforces both keys for non-admin callers.

**Postgres append auto-create race**: Concurrent `AppendEntries` calls for the same missing conversation ID can race on root conversation/group creation and hit `23505` on `conversation_groups_pkey`. The append path must treat that unique-violation as a concurrent-create win, reload the conversation, and continue instead of returning 500.
