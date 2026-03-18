---
status: implemented
---

# Enhancement 080: Simplify Fork Entry Semantics

> **Status**: Implemented.

## Summary

Store `forkedAtEntryId` as the original value sent by the client (the entry to **exclude** / diverge before) instead of transforming it to the previous entry (the last entry to **include**). This eliminates a server-side database query during fork creation, removes an edge-case bug when forking at the first entry, and aligns the stored value with the client-facing API contract while keeping `forkedAtEntryId = null` reserved for explicit blank-slate forks that inherit no parent entries.

## Motivation

The current implementation transforms `forkedAtEntryId` on write: when a client says "fork at entry X", the server looks up the entry immediately before X and stores *that* entry's ID. This was done for Java parity but introduces several problems:

1. **Edge-case bug**: When forking at the very first entry (no previous entry exists), the server kept the original ID — which incorrectly means "include this entry" under the current inclusive-stop semantics. The fork then shares the first message when it shouldn't.
2. **Extra database query**: Every fork creation requires a `SELECT ... WHERE created_at < ? ORDER BY created_at DESC LIMIT 1` query just to find the previous entry.
3. **Semantic mismatch**: The client sends "the entry I'm forking at" but the server stores "the entry before the one the client sent." This makes debugging confusing — the stored `forkedAtEntryId` doesn't match what the user clicked on.
4. **Code in 3 stores**: The transformation logic is duplicated across Postgres, SQLite, and MongoDB stores.

## Design

### Semantic change

| Aspect | Before (inclusive stop) | After (exclusive stop) |
|--------|------------------------|------------------------|
| **Stored value** | Entry *before* the fork point (last to include) | The fork point entry itself (first to exclude) |
| **Meaning** | "Include entries up to and including this ID" | "Exclude this entry and everything after it" |
| **Nil meaning** | Fork includes zero parent entries | Fork includes zero parent entries |
| **Client → DB** | Transformed (shifted back by one entry) | Stored as-is |

`nil` remains a special case meaning "blank-slate fork" and is only stored when the caller omits `forkedAtEntryId`. Forking at the first entry should now store that first entry's ID and rely on exclusive-stop filtering to inherit zero parent entries without overloading `nil`.

### Server changes

#### 1. Remove transformation in `createConversationWithID` (all 3 stores)

**Before** (`postgres.go`, `sqlite.go`, `mongo.go`):
```go
// Java parity: forkedAtEntryId stored is the entry BEFORE the fork point.
var prevEntry model.Entry
result := s.db.WithContext(ctx).
    Where("conversation_group_id = ? AND created_at < ?", sourceConv.ConversationGroupID, entry.CreatedAt).
    Order("created_at DESC").
    Limit(1).
    Find(&prevEntry)
if result.RowsAffected > 0 {
    prevID := prevEntry.ID
    forkedAtEntryID = &prevID
} else {
    forkedAtEntryID = nil
}
```

**After**: Delete the transformation block entirely. Keep only the validation that the entry exists. If the client forks at the first entry, persist that first entry's ID; do not rewrite it to `nil`. `nil` remains reserved for requests where `forkedAtEntryId` was omitted entirely.

#### 2. Update `filterEntriesByAncestry` (all 3 stores)

Change from inclusive stop (append then check) to exclusive stop (check then skip):

**Before**:
```go
result = append(result, entry)
if !isTarget && current.StopAtEntryID != nil && entry.ID == *current.StopAtEntryID {
    // advance to next ancestor
}
```

**After**:
```go
// Exclusive stop: forkedAtEntryId is the first entry to exclude from the parent.
if !isTarget && current.StopAtEntryID != nil && entry.ID == *current.StopAtEntryID {
    // advance to next ancestor
    continue
}
result = append(result, entry)
```

Keep `advanceForkAncestorForNilStop` unchanged. A nil stop point still means "skip inherited parent entries entirely and jump straight to the child conversation."

#### 3. Update `filterMemoryEntriesWithEpoch` (all 3 stores)

Same pattern — move the stop check before the epoch filtering logic and `continue` on match.

### Frontend changes

#### Update `createForkView` in `conversation.ts`

The `getEntries` helper currently uses inclusive stop:
```typescript
result.push(entry);
if (entry.id === untilEntryId) break; // inclusive
```

Change to exclusive stop:
```typescript
if (entry.id === untilEntryId) break; // exclusive — don't include
result.push(entry);
```

That is not the only frontend change. `createForkView` currently assumes `forkedAtEntryId` means "last included parent entry" in three places:

1. The comments and type docs describe `forkedAtEntryId` as the last inherited entry.
2. Fork markers are rendered on the entry *after* the stored ID by looking up `forksByEntryId.get(prevId)` while iterating the combined entry list.
3. Child fork summaries are associated with the previous rendered entry boundary.

Update the frontend to treat `forkedAtEntryId` as the first excluded parent entry. Normal forks should be looked up on the current entry ID, while blank-slate forks should continue to use the empty-string sentinel on the first rendered entry:

```typescript
return combinedEntries.map((entry, index) => ({
  entry,
  forks:
    index === 0
      ? [...(forksByEntryId.get("") ?? []), ...(forksByEntryId.get(entry.id) ?? [])]
      : forksByEntryId.get(entry.id),
}));
```

Blank-slate forks (`forkedAtEntryId = null`) should continue using the empty-string sentinel so they render before the first visible entry in the parent conversation.

## Testing

Existing BDD scenarios in `forking-rest.feature` and `forking-grpc.feature` need assertion updates:

**Before** (line ~25):
```gherkin
Then the response body "forkedAtEntryId" should be "${firstEntryId}"
```

**After**:
```gherkin
Then the response body "forkedAtEntryId" should be "${secondEntryId}"
```

The fork at `secondEntryId` should now store `secondEntryId` (the entry itself), not `firstEntryId` (the previous one).

Also keep or add coverage for:

- Forking at the very first entry: stored `forkedAtEntryId` should be that first entry's ID, and the fork should inherit zero parent entries.
- Fork-on-append without `forkedAtEntryId`: stored `forkedAtEntryId` should remain `null`, and the fork should still be blank-slate.
- Frontend fork-view rendering: branch annotations should appear on the first excluded entry, while blank-slate forks still render before the first visible entry.

Entry retrieval tests (verifying ancestry-based filtering) should continue to pass since the net visible result is the same for non-blank-slate forks: entries before the fork point are included, the fork point entry and after are excluded.

## Tasks

- [x] Remove fork-point transformation in Postgres store
- [x] Remove fork-point transformation in SQLite store
- [x] Remove fork-point transformation in MongoDB store
- [x] Update `filterEntriesByAncestry` to exclusive stop (Postgres)
- [x] Update `filterEntriesByAncestry` to exclusive stop (SQLite)
- [x] Update `filterEntriesByAncestry` to exclusive stop (MongoDB)
- [x] Update `filterMemoryEntriesWithEpoch` to exclusive stop (Postgres)
- [x] Update `filterMemoryEntriesWithEpoch` to exclusive stop (SQLite)
- [x] Update `filterMemoryEntriesWithEpoch` to exclusive stop (MongoDB)
- [x] Update frontend `createForkView` comments, fork-boundary mapping, and exclusive-stop semantics
- [x] Update REST BDD assertions in `forking-rest.feature`
- [x] Update gRPC BDD assertions in `features-grpc/forking-grpc.feature`
- [x] Update OpenAPI and protobuf descriptions/comments for `forkedAtEntryId`
- [x] Update design and entry-model docs to reflect the new stored semantics
- [x] Update enhancement docs 034 and 046 to reflect the new semantics
- [x] Update site docs that describe conversation forking semantics
- [x] Build and run tests

## Files to Modify

| File | Change |
|------|--------|
| `internal/plugin/store/postgres/postgres.go` | Remove transformation; update filter functions |
| `internal/plugin/store/sqlite/sqlite.go` | Remove transformation; update filter functions |
| `internal/plugin/store/mongo/mongo.go` | Remove transformation; update filter functions |
| `frontends/chat-frontend/src/lib/conversation.ts` | Update exclusive-stop handling, fork-boundary mapping, and blank-slate sentinel rendering |
| `internal/bdd/testdata/features/forking-rest.feature` | Update `forkedAtEntryId` assertions |
| `internal/bdd/testdata/features-grpc/forking-grpc.feature` | Update gRPC `forkedAtEntryId` assertions |
| `contracts/openapi/openapi.yml` | Update agent API descriptions/examples for stored fork semantics |
| `contracts/openapi/openapi-admin.yml` | Update admin API descriptions for stored fork semantics |
| `contracts/protobuf/memory/v1/memory_service.proto` | Update protobuf comments for stored fork semantics |
| `docs/design.md` | Update high-level forking semantics description |
| `docs/entry-data-model.md` | Update detailed fork storage/retrieval semantics |
| `docs/enhancements/implemented/034-forked-entry-retrieval.md` | Update semantics documentation |
| `docs/enhancements/implemented/046-simpler-forking.md` | Update implicit-fork design notes that mention previous-entry storage |
| `site/src/pages/docs/concepts/forking.md` | Update public concept docs for stored semantics and blank-slate forks |

## Verification

```bash
# Go build
go build ./...

# Go BDD tests (forking scenarios)
go test ./internal/bdd -run TestFeatures -count=1 > test.log 2>&1
# Search for failures using Grep tool on test.log

# Frontend build
cd frontends/chat-frontend && npm run lint && npm run build
```

## Design Decisions

- **Keep field names**: `forkedAtEntryId` and `forkedAtConversationId` remain unchanged. The name "forked at entry X" accurately describes "the entry where the fork happens" regardless of inclusive/exclusive semantics.
- **Keep blank-slate semantics**: `forkedAtEntryId = null` still means "inherit no parent entries". The change only affects non-nil values.
- **No migration needed**: Pre-release stance — datastores are reset frequently, so existing fork data with the old semantics doesn't need migration.
- **Drop Java parity**: The Java implementation is being replaced by the Go port. No need to maintain the transformation for compatibility.
