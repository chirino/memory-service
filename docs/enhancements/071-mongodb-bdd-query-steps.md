---
status: implemented
---

# Enhancement 071: MongoDB Query Assertions for Go BDD

> **Status**: Implemented.

## Summary

Add `When I execute MongoDB query:` and MongoDB-specific assertion steps to the Go BDD suite so Mongo-backed runs verify datastore side effects with parity to existing SQL verification steps.

## Motivation

Mongo BDD runs currently skip SQL verification checks by design (`ExecSQL` on `MongoTestDB` returns `nil, nil`). This keeps shared feature files runnable, but it also means key persistence assertions in scenarios such as conversation deletion and eviction are not actually validated on Mongo.

We need Mongo-native query/assertion steps that preserve the current SQL assertion ergonomics and eliminate this coverage gap.

## Design

### 1. Add MongoDB Query Step

Register a new step in `internal/bdd/steps_mongo.go`:

- `When I execute MongoDB query:`

The docstring payload is JSON with variable expansion (`${...}`) before execution.

```json
{
  "collection": "conversations",
  "operation": "find",
  "filter": { "_id": "${conversationId}" },
  "projection": { "_id": 1, "deleted_at": 1 },
  "sort": { "created_at": 1 },
  "limit": 10
}
```

Supported operations:

| Operation | Required fields | Result shape |
|---|---|---|
| `find` | `collection` | Array of documents (`[]map[string]any`) |
| `count` | `collection` | Single row: `[{"count": <n>}]` |
| `aggregate` | `collection`, `pipeline` | Array of pipeline result documents |

### 2. Add MongoDB Assertion Steps (SQL-Equivalent Semantics)

Add assertion steps mirroring SQL behavior:

- `Then the MongoDB result should have (\d+) rows?`
- `Then the MongoDB result should match:`
- `Then the MongoDB result column "..." should be non-null`
- `Then the MongoDB result at row (\d+) column "..." should be "..."`

Behavior should match SQL steps:

- table-based matching compares stringified values
- expected values support `${...}` expansion
- first-row non-null checks and row/column equality checks keep identical error style

### 3. Store Mongo Query Results in Response Bytes

After `I execute MongoDB query:`, marshal the result rows to JSON and write them into the scenario response bytes (same pattern used by SQL steps). This allows existing generic response assertions (`response body should be json`, etc.) to work with Mongo query output.

### 4. Backing DB Interface Changes

Extend `cucumber.TestDB` with a Mongo execution method:

```go
ExecMongoQuery(ctx context.Context, query string) ([]map[string]interface{}, error)
```

Backend behavior:

- `MongoTestDB.ExecMongoQuery`: parse JSON payload, execute query via mongo-driver, normalize values (timestamps to RFC3339Nano) for stable assertions.
- `PostgresTestDB.ExecMongoQuery`: return `nil, nil` (skip), matching current cross-backend compatibility pattern.

### 5. Feature Rollout Strategy

For scenarios that currently verify persistence via SQL (notably `conversations-rest.feature` and `eviction-rest.feature`), add adjacent Mongo verification blocks using the new steps.

Pattern:

```gherkin
When I execute SQL query:
"""
SELECT id, deleted_at FROM conversation_groups WHERE id = '${groupId}'
"""
Then the SQL result should have 1 row
And the SQL result column "deleted_at" should be non-null

When I execute MongoDB query:
"""
{
  "collection": "conversation_groups",
  "operation": "find",
  "filter": { "_id": "${groupId}" },
  "projection": { "_id": 1, "deleted_at": 1 }
}
"""
Then the MongoDB result should have 1 row
And the MongoDB result column "deleted_at" should be non-null
```

This keeps one shared feature file while enabling each backend to run its native assertions.

## Testing

### Unit Tests

- query parser validation (missing `collection`, unsupported `operation`, malformed JSON)
- `count` query output normalization to `[{"count": ...}]`
- assertion parity tests for table matching, row count, non-null, and row/column comparisons

### BDD Coverage

- add Mongo query assertions alongside existing SQL checks in:
  - `internal/bdd/testdata/features/conversations-rest.feature`
  - `internal/bdd/testdata/features/eviction-rest.feature`
- run both Postgres and Mongo suites to ensure no regressions in shared scenarios

## Tasks

- [x] Add `steps_mongo.go` with Mongo query and assertion step registrations.
- [x] Add shared row-assertion helper(s) used by SQL and Mongo steps to avoid drift.
- [x] Extend `cucumber.TestDB` with `ExecMongoQuery(...)`.
- [x] Implement `ExecMongoQuery` in `MongoTestDB`.
- [x] Implement no-op `ExecMongoQuery` in `PostgresTestDB`.
- [x] Update BDD feature files to include Mongo verification blocks where SQL checks currently cover persistence semantics.
- [x] Run Postgres and Mongo BDD suites and confirm equivalent assertions pass.

## Files to Modify

| File | Change |
|---|---|
| `internal/bdd/steps_mongo.go` | **New** Mongo query execution + assertion steps |
| `internal/bdd/steps_sql.go` | Reuse shared assertion helper (optional refactor for parity) |
| `internal/bdd/steps_query_assertions.go` | **New** common row assertion logic for SQL/Mongo |
| `internal/testutil/cucumber/cucumber.go` | Add `ExecMongoQuery` to `TestDB` interface |
| `internal/bdd/testdb_mongo.go` | Implement Mongo query execution |
| `internal/bdd/testdb_postgres.go` | Add no-op Mongo query executor |
| `internal/bdd/testdata/features/conversations-rest.feature` | Add Mongo query assertions for persistence checks |
| `internal/bdd/testdata/features/eviction-rest.feature` | Add Mongo query assertions for persistence checks |

## Verification

```bash
# Ensure devcontainer is running for test/build commands.
wt up

# Compile affected Go packages.
wt exec -- go build ./internal/bdd ./internal/testutil/cucumber

# Postgres BDD suite.
wt exec -- sh -lc 'go test ./internal/bdd -run TestFeatures -count=1 > bdd-pg.log 2>&1'
rg -n "FAIL|panic:|--- FAIL|Error:" bdd-pg.log

# MongoDB BDD suite.
wt exec -- sh -lc 'go test ./internal/bdd -run TestFeaturesMongo -count=1 > bdd-mongo.log 2>&1'
rg -n "FAIL|panic:|--- FAIL|Error:" bdd-mongo.log
```

## Non-Goals

- translating existing SQL strings into Mongo queries automatically
- replacing SQL verification steps in Postgres runs
- changing production datastore code paths (scope is test harness + feature assertions only)

## Design Decisions

- Use JSON docstrings for Mongo queries, not JavaScript shell syntax, to keep payloads deterministic and easy to parse in Go tests.
- Keep assertion semantics parallel to SQL steps so feature authors can reuse the same verification style across backends.
