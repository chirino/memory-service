---
status: implemented
---

# Enhancement 109: Tail Pagination for Conversation Entries

> **Status**: Implemented.

## Summary

Add tail-first and backward pagination to conversation entry listing so chat clients can open at the newest entries and load older pages without scanning from the beginning. REST gains `tail` and `beforeCursor`; gRPC gains equivalent entry-list fields. The common history-channel path uses bounded, fork-aware datastore queries instead of loading the entire conversation group.

This enhancement implements [issue #370](https://github.com/chirino/memory-service/issues/370).

## Motivation

Chat UIs normally open at the bottom of a conversation and load older messages as the user scrolls upward. The current API is forward-only:

- `afterCursor` / `page.page_token` advances from an entry toward newer entries.
- `fromSeq` supports forward replay from a sequence number.
- `upToEntryId` bounds reconstruction at an entry but still paginates from the oldest result.

Clients therefore have to walk every forward page to reach the newest entries. The current Postgres, SQLite, and Mongo implementations also load and filter an entire conversation group before applying ordinary history pagination, which makes recent-history reads increasingly expensive as a fork tree grows.

## Design

### API contract

#### REST request

Add the following query parameters to both user and admin entry-list endpoints:

| Parameter | Type | Description |
|---|---|---|
| `beforeCursor` | UUID string, optional | Return up to `limit` entries strictly before this entry in the caller-visible, filtered order. |
| `tail` | boolean, default `false` | Return the last `limit` entries in the caller-visible, filtered order. |

The modes are mutually exclusive:

| Request | Result |
|---|---|
| no cursor and `tail=false` | Oldest page, preserving current behavior. |
| `afterCursor={id}` | First `limit` entries strictly after the anchor, preserving current behavior. |
| `beforeCursor={id}` | Last `limit` entries strictly before the anchor. Results remain chronological (ascending). |
| `tail=true` | Last `limit` entries. Results remain chronological (ascending). |

Supplying more than one of `afterCursor`, `beforeCursor`, and `tail=true` returns REST `400 Bad Request`. A malformed `beforeCursor`, or one that is not in the caller-visible result after channel, ancestry, epoch, `upToEntryId`, and `fromSeq` filtering, also returns `400`. Existing `afterCursor` error behavior is unchanged by this enhancement.

`beforeCursor` and `tail` work with all existing filters. Filtering and fork visibility are applied first; the selected pagination mode is applied to that ordered result. The bounded datastore path described below is an optimization and must not change fallback-path results.

#### REST response

Add `beforeCursor` alongside the existing `afterCursor`:

```yaml
afterCursor:
  type: string
  format: uuid
  nullable: true
  description: Pass as afterCursor to fetch the adjacent newer page.
beforeCursor:
  type: string
  format: uuid
  nullable: true
  description: Pass as beforeCursor to fetch the adjacent older page.
```

Both cursors describe navigation relative to the returned page:

- `beforeCursor` is the ID of the first returned entry when an older entry exists; otherwise it is null.
- `afterCursor` is the ID of the last returned entry when a newer entry exists; otherwise it is null.
- An empty page returns both cursors as null.

All listing APIs share a configurable page-size ceiling. The server defaults to
1000 items and exposes `--max-page-size` / `MEMORY_SERVICE_MAX_PAGE_SIZE` to
change it. REST and gRPC reject larger requests before dispatch, and the bounded
entry-store paths repeat the check before allocating their `limit + 1` probe.
When the configured ceiling is below an endpoint's normal default, the implicit
default is clamped to the ceiling rather than returning an oversized page.

Consequently, an initial tail page has `afterCursor: null`. A middle page can contain both cursors. Cursor anchors are excluded from the adjacent page, so paging does not duplicate the boundary entry.

Example for 120 entries and `limit=50`:

| Request | Returned entries | `beforeCursor` | `afterCursor` |
|---|---:|---|---|
| `tail=true` | 71-120 | entry 71 | null |
| `beforeCursor={entry71}` | 21-70 | entry 21 | entry 70 |
| `beforeCursor={entry21}` | 1-20 | null | entry 20 |

#### OpenAPI changes

Update both:

- `GET /v1/conversations/{conversationId}/entries` in `contracts/openapi/openapi.yml`
- `GET /v1/admin/conversations/{id}/entries` in `contracts/openapi/openapi-admin.yml`

Document `400` for invalid or conflicting pagination controls. The response shape remains an object containing `data` and nullable navigation cursors.

#### gRPC changes

The existing forward cursor is a UUID string in `PageRequest.page_token`, despite entry IDs being represented as bytes elsewhere in the proto. Keep the new cursor in the same token representation rather than introducing an incompatible bytes cursor.

```protobuf
message PageInfo {
  string next_page_token = 1;
  string previous_page_token = 2; // New; empty when no older page exists.
}

message ListEntriesRequest {
  // Existing fields 1-7 unchanged.
  optional string before_page_token = 8;
  optional bool tail = 9;
}

message AdminListEntriesRequest {
  // Existing fields 1-7 unchanged.
  optional string before_page_token = 8;
  optional bool tail = 9;
}
```

`page.page_token`, `before_page_token`, and `tail=true` are mutually exclusive. Invalid combinations or invalid backward tokens return `INVALID_ARGUMENT`. `ListEntriesResponse` already uses `PageInfo` and is shared by user and admin services, so it needs no entry-specific top-level cursor field.

`next_page_token` maps to REST `afterCursor`; `previous_page_token` maps to REST `beforeCursor`.

### Store contract

Avoid adding more positional arguments to the already long `MemoryStore.GetEntries` signature. Introduce a request object used by both user and admin paths:

```go
type EntryListQuery struct {
    AfterCursor  *string
    BeforeCursor *string
    Tail         bool
    UpToEntryID  *string
    Limit        int
    Channel      *model.Channel
    EpochFilter  *MemoryEpochFilter
    ClientID     *string
    AgentID      *string
    AllForks     bool
    FromSeq      *uint32
}

type PagedEntries struct {
    Data         []model.Entry `json:"data"`
    AfterCursor  *string       `json:"afterCursor,omitempty"`
    BeforeCursor *string       `json:"beforeCursor,omitempty"`
}
```

Change `GetEntries` to accept `EntryListQuery`. Extend or replace `AdminMessageQuery` with the same pagination fields so REST and gRPC admin listing have identical semantics.

The in-memory fallback paginator operates on the fully filtered ascending slice and returns both cursors:

```go
func paginateEntries(
    entries []model.Entry,
    afterEntryID *string,
    beforeEntryID *string,
    tail bool,
    limit int,
) (page []model.Entry, afterCursor, beforeCursor *string, err error)
```

All three stores currently duplicate this logic. Move the direction-independent paginator into the store registry package (or another shared internal package) so its boundary semantics are tested once and cannot drift between datastores.

### Bounded history queries

#### Eligibility

Use the bounded path when the effective query is:

- `channel=history`
- `forks=none`
- no `upToEntryId`
- no `fromSeq`
- pagination mode is `tail` or `beforeCursor`

Other combinations continue through the existing materialize/filter/paginate path. In particular, `forks=all`, all-channel agent reads, context epoch reads, and sequence replay remain fallback cases.

#### Fork-aware algorithm

Do not query only by `conversation_group_id`: that can mix sibling branches and violates existing ancestry semantics. Build the existing root-to-target ancestry stack from conversation metadata, then read eligible history entries from its segments newest-to-oldest:

1. A tail read starts at the newest entry in the target conversation segment.
2. A backward read resolves the anchor entry, verifies that it is a visible history entry on the target ancestry path, and starts strictly before it.
3. Query the current segment in reverse default order, bounded by the segment's fork stop when it is an ancestor.
4. If the segment does not fill `limit + 1`, continue into the preceding ancestor segment.
5. Stop after collecting `limit + 1` visible entries across the path. The extra entry determines whether `beforeCursor` is non-null.
6. Drop the extra entry and reverse the page before returning it.

The fork anchor is the first excluded parent entry under current semantics. The optimized path must therefore exclude the anchor and all later entries in that parent, exactly like `filterEntriesByAncestry`.

This bounds entry materialization by the requested page size rather than the size of the conversation group. Query count may grow with ancestry depth, but entry rows/documents read are bounded by `limit + 1` plus anchor lookups; sibling branches are never scanned.

#### Stable ordering

The current default order is:

```text
created_at ASC, seq ASC NULLS FIRST, id ASC
```

Reverse scans must use the exact inverse:

```text
created_at DESC, seq DESC NULLS LAST, id DESC
```

Mongo uses `created_at`, `seq`, and `_id` with the same directions. Omitting `seq` would make page boundaries inconsistent with the existing API when timestamps collide.

#### Indexes and migration

Add datastore indexes that support the bounded branch/channel scan:

- Postgres: `(conversation_id, channel, created_at ASC, seq ASC NULLS FIRST, id ASC)`
- SQLite: `(conversation_id, channel, created_at, seq, id)`
- Mongo: `{conversation_id: 1, channel: 1, created_at: 1, seq: 1, _id: 1}`

Create these indexes through the existing non-destructive startup migration/index setup. Do not replace or drop the current group-order indexes because fallback and `forks=all` reads still use them.

### Route and server mapping

Both REST handlers parse and validate the new parameters before invoking the store. Both gRPC services map `before_page_token`, `tail`, and both returned cursors. The metrics store wrapper passes the query object through unchanged.

Generated REST proxy interfaces gain optional parameters when OpenAPI is regenerated. Stable Java proxy APIs covered by Enhancement 103 must add fields to their request/options objects without exposing new generated positional signatures to callers.

The Quarkus and Spring proxies expose `EntryListOptions` records containing
`afterCursor`, `beforeCursor`, `tail`, `limit`, `channel`, `epoch`, and `forks`.
The original positional overloads remain for source compatibility and delegate
to the options-based overload. Quarkus's Unix-domain-socket adapter maps every
generated parameter explicitly so UDS requests have the same behavior as TCP.

## Testing

### Behavioral coverage

Add BDD coverage for both user REST and gRPC semantics; cover admin mapping with focused handler/server tests:

```gherkin
Scenario: Tail page returns newest entries in chronological order
  Given a conversation with 120 history entries
  When I list entries with tail enabled and a limit of 50
  Then entries 71 through 120 are returned in chronological order
  And the previous page cursor is entry 71
  And the next page cursor is empty

Scenario: A client pages backward and forward without overlap
  Given I fetched the tail page of a 120-entry conversation with limit 50
  When I fetch the previous page using its previous page cursor
  Then entries 21 through 70 are returned in chronological order
  And both navigation cursors are present
  When I fetch the next page using that next page cursor
  Then entries 71 through 120 are returned without a duplicate boundary entry

Scenario: Backward pagination preserves fork ancestry
  Given conversation B forks from conversation A at entry A-51
  And A has entries A-1 through A-100
  And B has entries B-1 through B-30
  When I list conversation B entries with tail enabled and limit 40
  Then A-41 through A-50 and B-1 through B-30 are returned in order
  And no entry at or after A-51 is returned

Scenario: Conflicting pagination controls are rejected
  When I combine any two of afterCursor, beforeCursor, and tail=true
  Then the request is rejected as invalid
```

Also cover an empty conversation, fewer entries than the limit, exact-limit results, malformed and non-visible backward anchors, channel filtering, and `limit=1`.

Implemented BDD coverage includes user REST tail/backward navigation, fork-point
exclusion, paging across child and parent segments, rejection of context
cursors, `seq=null` versus `seq=0`, admin REST pagination, and user/admin gRPC
cursor mapping. The common REST feature runs in the SQLite, PostgreSQL, and
Mongo datastore matrices.

### Store and query coverage

For Postgres, SQLite, and Mongo:

- verify identical tail/backward results for a root conversation and a multi-level fork
- verify timestamp collisions and null/non-null `seq` values do not create gaps or duplicates
- capture executed queries or use a test seam to prove eligible reads do not call the full-group loader
- verify the bounded path reads no sibling-fork entries and fetches at most `limit + 1` result entries across ancestry segments
- verify ineligible filter combinations use the fallback path and return the same pagination semantics
- verify the new indexes exist after migration; use datastore query plans where practical

## Tasks

- [x] Update user and admin OpenAPI entry-list parameters, response cursors, and `400` responses.
- [x] Update protobuf entry-list request fields and `PageInfo.previous_page_token`.
- [x] Regenerate Go, Java, Python, and TypeScript artifacts affected by the contract workflow.
- [x] Introduce `EntryListQuery`; update `MemoryStore`, `AdminMessageQuery`, wrappers, mocks, and callers.
- [x] Share and test the in-memory bidirectional paginator.
- [x] Implement fork-aware bounded history reads in Postgres, SQLite, and Mongo.
- [x] Add the branch/channel/order indexes without removing existing indexes.
- [x] Map request and response cursors in user/admin REST and gRPC handlers.
- [x] Use `strconv.ParseBool` for `tail` parameter validation; reject malformed values with HTTP 400.
- [x] Add stable Quarkus/Spring `EntryListOptions` overloads and preserve the original positional overloads.
- [x] Update the Quarkus Unix-domain-socket generated-client parameter mapping.
- [x] Add user/admin REST and gRPC BDD coverage, including fork boundaries, cursor visibility, and null/zero sequence ordering.
- [x] Add focused store tests for null/zero ordering and bounded `limit + 1` materialization.
- [x] Update the site pagination, entries, and Quarkus gRPC client documentation.

## Files to Modify

| Area | Files | Change |
|---|---|---|
| REST contracts | `contracts/openapi/openapi.yml`, `contracts/openapi/openapi-admin.yml` | Add request controls, response cursor, and invalid-request responses. |
| gRPC contract | `contracts/protobuf/memory/v1/memory_service.proto` | Add entry-list request controls and `PageInfo.previous_page_token`. |
| Store contract | `internal/registry/store/plugin.go` | Add `EntryListQuery`, backward cursor result, and admin query fields. |
| Store implementations | `internal/plugin/store/postgres/`, `internal/plugin/store/sqlite/`, `internal/plugin/store/mongo/` | Add shared pagination semantics, bounded ancestry reads, migrations/indexes, and tests. |
| Store wrappers | `internal/plugin/store/metrics/` and store mocks/fixtures | Pass the query object and both cursors through. |
| REST handlers | `internal/plugin/route/entries/entries.go`, `internal/plugin/route/admin/admin.go` | Validate controls and serialize both cursors. |
| gRPC servers | `internal/grpc/server.go` | Map user/admin request modes and `PageInfo` cursors. |
| Generated bindings | `internal/generated/`, generated Java REST/proto sources, `python/`, `frontends/chat-frontend/` | Regenerate artifacts affected by OpenAPI and protobuf changes. |
| Stable Java APIs | Quarkus/Spring REST wrappers and their tests | Extend entry-list option objects without leaking generated positional APIs. |
| Quarkus UDS adapter | `java/quarkus/memory-service-extension/runtime/.../UnixSocketRestClientFactory.java` | Map the generated entry-list parameters to the correct query names and positions. |
| BDD | `internal/bdd/` | Add REST/gRPC tail, backward, conflict, and fork scenarios. |
| Site documentation | `site/src/pages/docs/concepts/pagination.md`, `site/src/pages/docs/concepts/entries.md`, `site/src/pages/docs/quarkus/grpc-client.mdx` | Document bidirectional REST and gRPC entry pagination. |

## Non-goals

- Descending response bodies; all returned entry pages remain chronological.
- Arbitrary client-selected sort keys.
- Optimizing `forks=all`, context epochs, journal/all-channel reads, `upToEntryId`, or `fromSeq` in this phase.
- Changing persisted entry data or current fork-anchor semantics.

## Issue #370 coverage

| Acceptance criterion | Resolution |
|---|---|
| Open at newest history page | `tail=true` / gRPC `tail`. |
| Page backward without a forward scan | `beforeCursor` / `before_page_token` and response previous cursor. |
| Equivalent REST and gRPC contracts | Explicit field mapping and shared cursor rules. |
| Bounded Postgres, SQLite, and Mongo reads | Fork-aware segment queries plus supporting indexes, datastore-matrix BDD coverage, and a focused SQLite `limit + 1` materialization assertion. |
| Preserve fork ancestry | Optimized algorithm uses the existing ancestry stack and fork stop semantics. |
| Tail, backward, and fork tests | BDD plus datastore result and bounded-query assertions. |

## Verification

Follow the OpenAPI workflow for generation and validation, then run the affected Go build and BDD/store suites. At minimum:

```bash
task generate
go build ./... > build.log 2>&1
rg -n "ERROR|FAIL|panic|undefined:" build.log
go test ./internal/plugin/store/postgres ./internal/plugin/store/sqlite ./internal/plugin/store/mongo -count=1 > store-test.log 2>&1
rg -n "ERROR|FAIL|panic|--- FAIL:" store-test.log
go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1 > test.log 2>&1
go test -tags auth_testfixtures ./internal/bdd -run TestFeaturesSQLite -count=1 >> test.log 2>&1
rg -n "ERROR|FAIL|panic|--- FAIL:" test.log
./java/mvnw -f java/pom.xml compile > java-build.log 2>&1
rg -n "ERROR|FAILURE|BUILD FAILURE" java-build.log
(cd frontends/chat-frontend && npm run lint && npm run build)
task verify:python
```

Run generated-client compile/build checks for every language changed by `task generate`.
