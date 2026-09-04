---
status: implemented
---

# Enhancement 116: CreatedAt Date Filtering for Entries API

> **Status**: Implemented.

## Summary

Adds `createdAt` timestamp filtering to the Memory Service Entries API across all supported query surfaces (agent REST, admin REST, agent gRPC, and admin gRPC), allowing clients to filter conversation entries by exact timestamp (`createdAt` / `created_at_eq`), range (`createdAtAfter` / `created_at_after` and `createdAtBefore` / `created_at_before`), or point-in-time lower/upper bounds.

## Motivation

AI agent applications frequently need to retrieve conversation history within specific time windows—such as entries created during a single user turn, entries recorded after a specific event, or entries within a particular session timeframe. Previously, entries could only be filtered by channel, pagination cursors (`beforeCursor`, `afterCursor`), sequence boundaries (`upToEntryId`, `upToSeq`), or pagination limits. Pushing timestamp filters down to the datastore (PostgreSQL, SQLite, and MongoDB) avoids over-fetching and in-memory filtering.

## Design

### API Contracts

#### 1. Agent REST API (`GET /v1/conversations/{conversationId}/entries`)
Query parameters added to `contracts/openapi/openapi.yml`:
- `createdAtAfter` (`date-time`): Return only entries with `createdAt >= createdAtAfter`.
- `createdAtBefore` (`date-time`): Return only entries with `createdAt <= createdAtBefore`.
- `createdAt` (`date-time`): Return only entries with `createdAt == createdAt`. Mutually exclusive with `createdAtAfter` and `createdAtBefore`.

#### 2. Admin REST API (`GET /v1/admin/conversations/{conversationId}/entries`)
Same query parameters added to `contracts/openapi/openapi-admin.yml`: `createdAtAfter`, `createdAtBefore`, `createdAt`.

#### 3. Agent & Admin gRPC API (`ListEntriesRequest` & `AdminListEntriesRequest`)
Fields added to `contracts/protobuf/memory/v1/memory_service.proto`:
- `optional google.protobuf.Timestamp created_at_after = 11;` (agent) / `= 10;` (admin)
- `optional google.protobuf.Timestamp created_at_before = 12;` (agent) / `= 11;` (admin)
- `optional google.protobuf.Timestamp created_at_eq = 13;` (agent) / `= 12;` (admin)

### Validation & Mutual Exclusivity
- If `createdAt` (or `created_at_eq`) is supplied alongside `createdAtAfter` (or `created_at_after`) or `createdAtBefore` (or `created_at_before`), the handler rejects the request immediately with HTTP 400 (`invalid_request`: `"createdAt is mutually exclusive with createdAtAfter and createdAtBefore"`) or gRPC `INVALID_ARGUMENT`.
- Date parsing errors return HTTP 400 (`invalid_request`: `"invalid createdAtAfter/createdAtBefore/createdAt parameter format: ..."`).

### Datastore Pushdown

#### Internal Query Types (`internal/registry/store/plugin.go`)
- Added `CreatedAtFilter` struct:
  ```go
  type CreatedAtFilter struct {
      After  *time.Time
      Before *time.Time
      Exact  *time.Time
  }
  ```
- Included `CreatedAt CreatedAtFilter` on `EntryListQuery` and `AdminMessageQuery`.

#### SQL Datastores (`sqlentry/pager.go`, PostgreSQL, SQLite)
- `sqlentry.ApplyCreatedAtFilter(db *gorm.DB, filter CreatedAtFilter) *gorm.DB` applies:
  - `Exact`: `db.Where("created_at = ?", *filter.Exact)`
  - `After`: `db.Where("created_at >= ?", *filter.After)`
  - `Before`: `db.Where("created_at <= ?", *filter.Before)`
- Applied to both bounded scanning queries (`BoundedQuery.CreatedAtFilter`) and fallback/unbounded queries in `internal/plugin/store/postgres` and `internal/plugin/store/sqlite`.
- In-memory `FilterEntriesByCreatedAt` applied when scanning ancestor branches across fork trees to ensure bounded scans discard non-matching rows.

#### MongoDB (`internal/plugin/store/mongo/mongo.go`)
- `buildCreatedAtFilter(f CreatedAtFilter) bson.M` constructs MongoDB timestamp filters on `created_at`:
  - `Exact`: `bson.M{"created_at": *f.Exact}`
  - `After` and/or `Before`: `bson.M{"created_at": bson.M{"$gte": ..., "$lte": ...}}`
- Pushed into `Find` filter expressions across all entry queries.

## Testing

### BDD Scenarios (Cucumber)
- `internal/bdd/testdata/features/entries-created-at-filter-rest.feature`:
  - Exact `createdAt` match (HTTP 200, 1 entry returned).
  - `createdAtAfter` range filter (HTTP 200, matching entries returned).
  - `createdAtBefore` range filter (HTTP 200, matching entries returned).
  - Combined `createdAtAfter` + `createdAtBefore` range query (HTTP 200).
  - Validation rejection on conflicting `createdAt` + `createdAtAfter` (HTTP 400).
  - Validation rejection on conflicting `createdAt` + `createdAtBefore` (HTTP 400).
  - Admin REST date range query (HTTP 200).
  - Admin REST mutual exclusivity rejection (HTTP 400).
- `internal/bdd/testdata/features-grpc/entries-created-at-filter-grpc.feature`:
  - gRPC `created_at_eq` exact match.
  - gRPC `created_at_after` + `created_at_before` range match.
  - gRPC `INVALID_ARGUMENT` rejection when combining `created_at_eq` with range parameters.

## Tasks

- [x] Update OpenAPI contracts (`openapi.yml`, `openapi-admin.yml`) and Protobuf contracts (`memory_service.proto`)
- [x] Run code generation (`go generate .`)
- [x] Add `CreatedAtFilter` to internal store contracts and datastore implementations (Postgres, SQLite, MongoDB)
- [x] Implement parameter parsing and mutual exclusivity validation in Agent REST handler (`internal/plugin/route/entries`)
- [x] Implement parameter parsing and mutual exclusivity validation in Admin REST handler (`internal/plugin/route/admin`)
- [x] Implement timestamp parsing and validation in gRPC handlers (`internal/grpc`)
- [x] Add live API verification curl tests
- [x] Add BDD tests and step definitions for REST and gRPC
- [x] Write enhancement document `docs/enhancements/116-createdAt-entry-filter.md`

## Files Modified

| File | Changes |
|------|---------|
| `contracts/openapi/openapi.yml` | Added `createdAtAfter`, `createdAtBefore`, `createdAt` query params to `ListEntries` |
| `contracts/openapi/openapi-admin.yml` | Added `createdAtAfter`, `createdAtBefore`, `createdAt` query params to `AdminListEntries` |
| `contracts/protobuf/memory/v1/memory_service.proto` | Added `created_at_after`, `created_at_before`, `created_at_eq` to `ListEntriesRequest` & `AdminListEntriesRequest` |
| `internal/registry/store/plugin.go` | Added `CreatedAtFilter` to `EntryListQuery` and `AdminMessageQuery` |
| `internal/plugin/store/sqlentry/pager.go` | Added `ApplyCreatedAtFilter` and `FilterEntriesByCreatedAt` |
| `internal/plugin/store/postgres/postgres.go` | Wired `CreatedAtFilter` through Postgres queries |
| `internal/plugin/store/sqlite/sqlite.go` | Wired `CreatedAtFilter` through SQLite queries |
| `internal/plugin/store/mongo/mongo.go` | Wired `CreatedAtFilter` through Mongo queries |
| `internal/plugin/route/entries/entries.go` | Handled `createdAt` params, parsing, and validation |
| `internal/plugin/route/admin/admin.go` | Handled admin `createdAt` params, parsing, and validation |
| `internal/grpc/server.go` | Handled gRPC timestamps and validation in `ListEntries` and `AdminListEntries` |
| `internal/testutil/cucumber/cucumber.go` | Added `SetEntryCreatedAt` to `TestDB` interface |
| `internal/bdd/testdb_sqlite.go` | Implemented `SetEntryCreatedAt` for SQLite |
| `internal/bdd/testdb_postgres.go` | Implemented `SetEntryCreatedAt` for PostgreSQL |
| `internal/bdd/testdb_mongo.go` | Implemented `SetEntryCreatedAt` for MongoDB |
| `internal/bdd/steps_entries.go` | Added `entry "..." has createdAt "..."` step definition |
| `internal/bdd/testdata/features/entries-created-at-filter-rest.feature` | Added REST BDD feature |
| `internal/bdd/testdata/features-grpc/entries-created-at-filter-grpc.feature` | Added gRPC BDD feature |
| `docs/enhancements/116-createdAt-entry-filter.md` | Created enhancement document |

## Verification

```bash
# Verify codegen and compilation
go generate .
go build ./...

# Run focused BDD tests
go test -tags 'sqlite_fts5 auth_testfixtures' ./internal/bdd -run 'TestFeaturesSQLite/(entries-created-at-filter-rest|entries-created-at-filter-grpc)$' -count=1
```
