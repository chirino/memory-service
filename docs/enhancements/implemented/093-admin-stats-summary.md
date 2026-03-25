---
status: implemented
---

# Enhancement 093: Admin Stats Summary Endpoint

> **Status**: Implemented.

## Summary

Add a new admin stats summary endpoint at `GET /v1/admin/stats/summary` that returns current datastore-backed totals for core resources plus outbox depth metadata. This complements the existing Prometheus time-series endpoints with point-in-time operational counts that Prometheus does not own.

## Motivation

The current `/v1/admin/stats/*` surface is entirely Prometheus-backed and chart-oriented:

- request rate
- error rate
- latency
- cache hit rate
- DB pool utilization
- store latency/throughput

That leaves a gap for operational questions that are about current persisted state rather than sampled metrics, especially:

- how many outbox rows are retained right now?
- how old is the oldest retained outbox row?
- how many conversation groups, conversations, entries, and memories exist right now?
- how many soft-deleted conversation groups and memories are still awaiting retention cleanup?

Those values should come from the configured datastores directly, not from Prometheus queries, because:

1. Prometheus is optional, while the admin summary should still work without it.
2. These are current inventory counts, not time-series metrics.
3. The outbox oldest timestamp is retention/debugging information, not a chart series.

## Design

### Endpoint

Add:

```http
GET /v1/admin/stats/summary
```

Behavior:

- requires `admin` or `auditor` role
- does not require Prometheus
- reads directly from the configured memory store and episodic store
- returns `200` with a single summary object
- returns `501` if the configured store does not implement the required summary interfaces

### Response Shape

Return:

```json
{
  "conversationGroups": {
    "total": 120,
    "softDeleted": 7,
    "oldestSoftDeletedAt": "2026-03-25T09:15:00Z"
  },
  "conversations": {
    "total": 185
  },
  "entries": {
    "total": 1840
  },
  "memories": {
    "total": 93,
    "softDeleted": 11,
    "oldestSoftDeletedAt": "2026-03-25T09:45:00Z"
  },
  "outboxEvents": {
    "total": 422,
    "oldestAt": "2026-03-25T10:15:00Z"
  }
}
```

Field semantics:

| Field | Meaning |
| --- | --- |
| `conversationGroups.total` | Active conversation groups where `deleted_at` is not set |
| `conversationGroups.softDeleted` | Conversation groups where `deleted_at` is set |
| `conversationGroups.oldestSoftDeletedAt` | Oldest `updatedAt` timestamp among soft-deleted conversations, or `null` when none are soft-deleted |
| `conversations.total` | Active conversations where `deleted_at` is not set |
| `entries.total` | Total stored entry rows |
| `memories.total` | Active episodic memory rows where `deleted_at` is not set |
| `memories.softDeleted` | Soft-deleted episodic memory rows where `deleted_at` is set |
| `memories.oldestSoftDeletedAt` | Oldest memory soft-delete timestamp, or `null` when no memories are soft-deleted |
| `outboxEvents.total` | Total retained outbox rows |
| `outboxEvents.oldestAt` | RFC 3339 timestamp of the oldest retained outbox row, or `null` when the outbox is empty |

`outboxEvents` itself is nullable:

- `null` when the configured datastore does not support outbox persistence
- `null` when the outbox feature is disabled
- an object when the outbox is enabled and supported

### Store Interfaces

Add optional summary interfaces instead of baking datastore-specific queries into the admin route layer.

Memory-store summary interface:

```go
type AdminStatsSummary struct {
    ConversationGroups struct {
        Total               int64
        SoftDeleted         int64
        OldestSoftDeletedAt *time.Time
    }
    Conversations struct {
        Total int64
    }
    Entries struct {
        Total int64
    }
    OutboxEvents *struct {
        Total    int64
        OldestAt *time.Time
    }
}

type AdminStatsSummaryProvider interface {
    AdminStatsSummary(ctx context.Context) (*AdminStatsSummary, error)
}
```

Episodic-store summary interface:

```go
type AdminMemoryStatsSummary struct {
    MemoriesTotal       int64
    MemoriesSoftDeleted int64
}

type AdminMemoryStatsSummaryProvider interface {
    AdminMemoryStatsSummary(ctx context.Context) (*AdminMemoryStatsSummary, error)
}
```

The admin route should:

1. require auditor role
2. read the memory-store summary
3. read the episodic-store summary
4. emit `outboxEvents: null` when outbox support is unavailable or disabled
5. merge them into one response payload

### Datastore Implementations

Implement summaries for:

- PostgreSQL memory store
- SQLite memory store
- Mongo memory store
- PostgreSQL episodic store
- SQLite episodic store
- Mongo episodic store

Wrapper stores such as `internal/plugin/store/metrics` should pass the optional interfaces through when the wrapped store supports them.

### API Contracts and Docs

Update:

- `contracts/openapi/openapi-admin.yml`
- generated admin Go bindings
- admin API docs page

This endpoint belongs in the Admin Stats section, but it should be documented as a datastore-backed summary, not a Prometheus metric.

## Testing

### BDD Scenarios

```gherkin
@admin-stats
Feature: Admin stats summary REST API
  Scenario: Admin can fetch summary stats
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/stats/summary"
    Then the response status should be 200
    And the response body field "conversationGroups.total" should not be null
    And the response body field "conversationGroups.softDeleted" should not be null
    And the response body field "conversations.total" should not be null
    And the response body field "entries.total" should not be null
    And the response body field "memories.total" should not be null
    And the response body field "memories.softDeleted" should not be null

  Scenario: Auditor can fetch summary stats
    Given I am authenticated as auditor user "charlie"
    When I call GET "/v1/admin/stats/summary"
    Then the response status should be 200

  Scenario: Non-admin user cannot fetch summary stats
    Given I am authenticated as user "bob"
    When I call GET "/v1/admin/stats/summary"
    Then the response status should be 403

  Scenario: Outbox summary is null when outbox is unavailable
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/stats/summary"
    Then the response status should be 200
    And the response body field "outboxEvents" should be null
```

### Unit Tests

- PostgreSQL memory-store summary query returns expected counts
- SQLite memory-store summary query returns expected counts
- Mongo memory-store summary query returns expected counts
- episodic summary queries distinguish active vs soft-deleted memories
- outbox summary returns `oldestAt = null` when the outbox is empty
- summary response returns `outboxEvents = null` when outbox support is disabled or unavailable
- metrics wrapper forwards the optional summary interfaces

## Tasks

- [x] Add admin stats summary schemas and path to `contracts/openapi/openapi-admin.yml`
- [x] Add optional admin summary interfaces to the memory and episodic registries
- [x] Implement memory-store summary queries for PostgreSQL, SQLite, and Mongo
- [x] Implement episodic-store summary queries for PostgreSQL, SQLite, and Mongo
- [x] Pass the optional summary interfaces through the metrics store wrapper
- [x] Add `GET /v1/admin/stats/summary` to the wrapper adapter
- [x] Regenerate admin OpenAPI bindings
- [x] Add BDD coverage for admin, auditor, and forbidden user access
- [x] Update admin API docs to describe the summary endpoint and its fields

## Files to Modify

| File | Change |
| --- | --- |
| `contracts/openapi/openapi-admin.yml` | Add the new admin stats summary path and schemas |
| `internal/registry/store/event_outbox.go` or `internal/registry/store/plugin.go` | Add the memory-store admin summary interface and types |
| `internal/registry/episodic/plugin.go` | Add the episodic summary interface and types |
| `internal/plugin/store/postgres/*.go` | Implement PostgreSQL conversation/entry/outbox summary queries |
| `internal/plugin/store/sqlite/*.go` | Implement SQLite conversation/entry/outbox summary queries |
| `internal/plugin/store/mongo/*.go` | Implement Mongo conversation/entry/outbox summary queries |
| `internal/plugin/store/postgres/episodic_store.go` | Implement PostgreSQL memory summary queries |
| `internal/plugin/store/sqlite/episodic_store.go` | Implement SQLite memory summary queries |
| `internal/plugin/store/mongo/episodic_store.go` | Implement Mongo memory summary queries |
| `internal/plugin/store/metrics/metrics.go` | Pass through the optional summary interfaces |
| `internal/plugin/route/admin/admin.go` | Mount the new summary route |
| `internal/cmd/serve/wrapper_routes.go` | Register the new generated admin route |
| `internal/bdd/testdata/features/admin-stats-rest.feature` | Add summary endpoint scenarios |
| `site/src/pages/docs/concepts/admin-apis.mdx` | Document the new summary endpoint |

## Verification

```bash
go generate ./...
go build ./...
go test ./internal/bdd -run '^TestFeatures/admin-stats-rest$' -count=1
go test -tags 'sqlite_fts5' ./internal/bdd -run '^TestFeaturesSQLite/admin-stats-rest$' -count=1
go test ./internal/bdd -run '^TestFeaturesMongo/admin-stats-rest$' -count=1
cd site && npm run build
cd frontends/chat-frontend && npm run generate && npm run build
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus,spring/memory-service-rest-spring compile -am
```

## Design Decisions

### Why add a separate summary endpoint instead of extending the Prometheus stats endpoints?

Because these values are not time-series metrics. They are current persisted inventory counts and retention signals. A summary endpoint keeps that contract explicit and avoids pretending these are chart series.

### Why split memory-store and episodic-store summary interfaces?

Because conversation data and episodic memory data already live behind separate store abstractions. The route should merge summaries, not force one subsystem to know how to count the other subsystem’s tables or collections.

## Non-Goals

- replacing the existing Prometheus-backed admin stats endpoints
- adding a gRPC admin stats summary endpoint
- exposing per-kind outbox breakdowns
- exposing attachment, transfer, or membership totals in this first pass
