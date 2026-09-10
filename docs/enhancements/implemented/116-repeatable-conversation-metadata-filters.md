---
status: implemented
---

# Enhancement 116: Repeatable conversation metadata filters

> **Status**: Implemented.

## Summary

Extend conversation metadata filtering with repeatable equality and not-equal predicates across the agent and admin REST and gRPC APIs. Each request accepts at most five predicates, combines them with `AND`, and evaluates them in the datastore before fork selection and pagination. Retain the current single-equality forms as deprecated compatibility paths during rollout.

This proposal implements [GitHub issue 561](https://github.com/chirino/memory-service/issues/561).

## Motivation

The current conversation list APIs accept one exact string metadata filter. REST represents it as `metadata[key]=value`, while gRPC uses the `metadata_filter_key` and `metadata_filter_value` pair. Callers cannot express either of these common queries:

```text
status is not "waiting"
status is "waiting" and agent-id is "worker-1"
```

Filtering a returned page in the client does not preserve pagination or `latest-fork` behavior. A page can become short even when more matching conversations exist, and the selected representative for a fork tree can be wrong. Filtering in the service process has the same problem unless the store over-fetches without a fixed bound.

Agent and admin APIs already share metadata filter semantics. REST handlers use `ParseMetadataFilterQuery`, and both gRPC handlers use `MetadataFilterFromOptionalPair`. The stores also use one metadata filter model for public and admin list queries. Adding the behavior to only one transport or authorization scope would break that parity.

## Design

### Filter semantics

A metadata predicate has a key, an operator, and a string value. Requests may contain zero to five predicates. The service combines repeated predicates with `AND`, including predicates that use the same key.

The first version supports two operators:

| Operator | Meaning |
| --- | --- |
| `=` | The key exists and its value is a scalar string equal to the requested value. |
| `!=` | The key exists and its value is a scalar string different from the requested value. |

Both operators use the same string-only type rule:

| Stored value for `status` | `status=waiting` | `status!=waiting` |
| --- | --- | --- |
| `"waiting"` | Match | No match |
| `"running"` | No match | Match |
| `""` | No match | Match |
| Missing | No match | No match |
| JSON `null` | No match | No match |
| Number, boolean, array, or object | No match | No match |

An empty requested value is valid. A value may contain `!` and `=`. String comparison is binary and case-sensitive, with no trimming, case folding, or Unicode normalization. Keys retain the existing validation rule and may contain only ASCII letters, digits, underscores, and hyphens.

The service rejects a sixth predicate. Agent and admin REST endpoints return `400 Bad Request`; agent and admin gRPC endpoints return `INVALID_ARGUMENT`.

### REST contract

Replace the `deepObject` parameter in both OpenAPI contracts with a repeated string parameter:

```yaml
- name: metadata
  in: query
  required: false
  description: |
    Metadata filter expressions. The service combines multiple expressions with AND.
    A request may contain at most five expressions.

    Operators:
      =   equal
      !=  not equal
  style: form
  explode: true
  schema:
    type: array
    maxItems: 5
    items:
      type: string
      pattern: '^[A-Za-z0-9_-]+(?:!=|=)[\s\S]*$'
  examples:
    multipleFilters:
      value:
        - status=waiting
        - agent-id=worker-1
```

[OpenAPI `form` style with `explode: true`](https://spec.openapis.org/oas/v3.1.0.html#style-examples) serializes the array as repeated query parameters:

```http
GET /v1/conversations?metadata=status=waiting
GET /v1/conversations?metadata=status!=waiting
GET /v1/conversations?metadata=status=waiting&metadata=agent-id=worker-1
GET /v1/admin/conversations?metadata=status!=waiting&metadata=region=us-east
```

The parser first consumes the longest valid metadata-key prefix. The remaining text must start with `!=` or `=`; the parser checks for `!=` first. Everything after the operator is the value. These examples show the boundary:

| Expression | Parsed result |
| --- | --- |
| `status=` | Key `status`, operator `=`, empty value |
| `status==waiting` | Key `status`, operator `=`, value `=waiting` |
| `status!==waiting` | Key `status`, operator `!=`, value `=waiting` |
| `status!=a!b=c` | Key `status`, operator `!=`, value `a!b=c` |
| `status!waiting` | Invalid operator |
| `status` | Missing operator |

`url.ParseQuery` performs percent decoding before expression parsing. The parser checks the repeated-value count before parsing individual expressions, then reports the zero-based predicate index for a malformed item. Callers must percent-encode a literal `+` as `%2B`, because form query decoding otherwise converts it to a space. A malformed percent escape, an invalid key, a missing operator, an unknown operator, or more than five expressions returns `400 Bad Request`.

The OpenAPI contract replaces the old deep-object form, so generated clients change the parameter type from a string map to a list of expression strings. The server temporarily retains one old `metadata[status]=waiting` parameter as a deprecated equality-only compatibility path. More than one legacy metadata key retains the current `400 Bad Request` behavior. A request that mixes a legacy parameter with the new exact `metadata` parameter also returns `400 Bad Request`; it never ignores either form. Other malformed parameters whose decoded names begin with `metadata` retain the current rejection behavior.

### gRPC contract

gRPC uses typed predicates rather than copying the REST expression grammar:

```protobuf
enum ConversationMetadataComparison {
  CONVERSATION_METADATA_COMPARISON_UNSPECIFIED = 0;
  CONVERSATION_METADATA_COMPARISON_EQUAL = 1;
  CONVERSATION_METADATA_COMPARISON_NOT_EQUAL = 2;
}

message ConversationMetadataFilter {
  string key = 1;
  ConversationMetadataComparison comparison = 2;
  string value = 3;
}
```

Add the repeated field to both request messages. The field number is 8 for `ListConversationsRequest` and 13 for `AdminListConversationsRequest`:

```protobuf
message ListConversationsRequest {
  // Existing fields 1 through 5 omitted.
  optional string metadata_filter_key = 6 [deprecated = true];
  optional string metadata_filter_value = 7 [deprecated = true];
  // At most five predicates. The service combines them with AND.
  repeated ConversationMetadataFilter metadata_filters = 8;
}

message AdminListConversationsRequest {
  // Existing fields 1 through 10 omitted.
  optional string metadata_filter_key = 11 [deprecated = true];
  optional string metadata_filter_value = 12 [deprecated = true];
  // At most five predicates. The service combines them with AND.
  repeated ConversationMetadataFilter metadata_filters = 13;
}
```

An unspecified operator is invalid. An empty key is invalid, while an empty value is valid. Unknown enum values are invalid.

The existing key/value fields remain as an equality-only compatibility path:

- Neither legacy field and no new predicates means no metadata filter.
- Both legacy fields means one equality predicate.
- Exactly one legacy field returns `INVALID_ARGUMENT`.
- A request that uses a legacy field and `metadata_filters` returns `INVALID_ARGUMENT`.

The service must not reuse field numbers 6, 7, 11, or 12 for the new message fields. If a later release removes the legacy pair, the protobuf contract must reserve those numbers and names.

[Adding a protobuf field is wire-compatible](https://protobuf.dev/programming-guides/proto3/#updating), but it is not automatically behavior-compatible. An old server ignores the unknown `metadata_filters` field and can return an unfiltered successful response. To make feature detection fail closed, add these optional REST capability properties and corresponding protobuf fields 8 and 9 to `CapabilitiesFeatures`:

```yaml
conversation_metadata_filter_version:
  type: integer
  minimum: 1
  description: Version of the repeatable conversation metadata filter contract.
max_conversation_metadata_filters:
  type: integer
  minimum: 1
  description: Maximum predicates accepted by one conversation list request.
```

```protobuf
uint32 conversation_metadata_filter_version = 8;
uint32 max_conversation_metadata_filters = 9;
```

The new server reports version `1` and maximum `5`. Filter-contract versions are monotonic: a later version must retain version 1 operators and semantics or advertise a separate capability. The OpenAPI properties remain optional so a new capabilities client can decode an older server response. In protobuf, a missing field decodes to zero. A client must not send `metadata_filters` unless it has observed version 1 or later and a maximum at least as large as its predicate count from the same server deployment. Without that capability, it may use the legacy pair for one equality predicate or fail locally. Generated low-level stubs expose the capability and filter fields but cannot enforce this call sequence for callers.

### Internal representation and validation

Replace `MetadataKeyFilter` with a predicate slice:

```go
const MaxConversationMetadataPredicates = 5

type ConversationMetadataOperator string

const (
	ConversationMetadataEqual    ConversationMetadataOperator = "="
	ConversationMetadataNotEqual ConversationMetadataOperator = "!="
)

type ConversationMetadataPredicate struct {
	Key      string
	Operator ConversationMetadataOperator
	Value    string
}
```

The store interface accepts `[]ConversationMetadataPredicate`. `AdminConversationQuery` stores the same slice. A slice is required because a map would discard duplicate keys and could change valid `AND` expressions.

One validation helper enforces the key rules, supported operators, and five-predicate limit. The REST parser and both gRPC handlers convert their transport representations to this internal type before calling a store. Stores validate incoming predicates at every entry point and reject invalid keys, operators, or counts with `BadRequestError`. Datastore query builders select operators from the typed enum and bind all keys and values as query parameters.

Cursor-anchor resolution is also shared store behavior. An invalid anchor returns `BadRequestError`; the REST and gRPC boundaries map it to `400 Bad Request` and `INVALID_ARGUMENT` without revealing whether an inaccessible conversation exists.

### Datastore query placement

Each datastore adds every metadata predicate to the base conversation query before these operations:

1. `latest-fork` or roots selection.
2. Result ordering.
3. Cursor application.
4. `limit + 1` page limiting.

Admin-only filters such as `owner_user_id`, `archived_after`, and `archived_before` are combined with metadata predicates by `AND`. Public authorization and membership conditions remain part of the same base query.

This order means that `latest-fork` selects the most recently updated conversation that satisfies every metadata predicate. It does not select the fork-tree representative first and test its metadata afterward.

### PostgreSQL

Equality keeps the current JSONB containment predicate and string type guard so the existing `jsonb_path_ops` GIN index remains useful:

```sql
jsonb_typeof(c.metadata -> :key) = 'string'
AND c.metadata @> :one_key_string_object::jsonb
```

Not-equal uses the same JSONB equality semantics under a negation so it remains the exact complement of equality for string-typed values and does not inherit a database collation:

```sql
jsonb_typeof(c.metadata -> :key) = 'string'
AND NOT (c.metadata @> :one_key_string_object::jsonb)
```

GORM adds one grouped `WHERE` clause per predicate. Repeated clauses preserve duplicate-key `AND` behavior. The existing ranked subquery for `latest-fork` receives all clauses before `ROW_NUMBER()` selects one conversation per group.

The current GIN index can support equality containment. It does not make the negative predicate index-only. PostgreSQL may scan every authorized row that survives the other selective conditions before applying `<>`.

### SQLite

SQLite uses `json_type` to reject missing and non-string values, then compares the extracted scalar:

```sql
json_type(c.metadata, :path) = 'text'
AND json_extract(c.metadata, :path) COLLATE BINARY = :value
```

The not-equal form changes the final comparison to `<>` and also uses `COLLATE BINARY`. The existing key validator makes the constructed JSON path safe and consistent across datastores. SQLite has no dedicated metadata index, so both operators evaluate JSON expressions for candidate rows.

As in PostgreSQL, the base query receives every predicate before the ranked `latest-fork` subquery.

### MongoDB

Build a `$and` array with one document per predicate. Do not assign predicates directly into one `bson.M`, because repeated keys and repeated `$expr` fields would overwrite each other.

Each equality predicate combines the current direct field equality with an `$expr` string type guard. Each not-equal predicate combines a direct `$ne` condition with the same type guard. The type guard is required because MongoDB `$ne` also matches missing fields and values of other BSON types. Metadata-filtered `find` and aggregation operations use MongoDB's `simple` collation so comparison remains binary even if a deployment configures another collection collation.

The metadata wildcard index can support direct equality conditions. Negative conditions are usually less selective, and the `$expr` type check prevents an index-only result. Documentation must not claim that not-equal filters have the same index behavior as equality filters.

No MongoDB index migration is required. Membership lookup uses the existing unique `(conversation_group_id, user_id)` index, roots lookup uses the ancestry collection's `_id` index, and metadata equality can use the existing wildcard index. Operators must still expect negative predicates and representative sorting to examine more records.

The current public list preloads every membership for the user, and the public and admin `latest-fork` helpers load every filtered conversation candidate into Go. That violates the page-memory requirement even if only conversation decoding is fixed. Replace the MongoDB agent and admin conversation-list query paths with aggregation pipelines. The public pipeline uses the existing unique `(conversation_group_id, user_id)` membership index in a `$lookup`, unwinds the one matching membership, and carries its access level into the result. The admin pipeline omits this authorization stage.

The pipelines perform these stages as applicable:

1. `$match` archive, async-parent ancestry, admin, and metadata conditions.
2. For agent lists, `$lookup` and `$unwind` the authenticated user's membership.
3. For `mode=roots`, `$lookup` the conversation ancestry document by conversation ID and retain documents without a fork parent.
4. For `mode=latest-fork`, `$sort` by `(updated_at DESC, created_at DESC, _id DESC)`, then `$group` by `conversation_group_id` and retain the first document.
5. Restore the conversation document as the aggregation root while retaining the agent access level when applicable.
6. Apply the standardized result order and cursor predicate described below.
7. `$limit` to `limit + 1`.

Run the aggregation with `allowDiskUse` enabled. The pre-group sort and `$group` can otherwise exceed [MongoDB's aggregation memory limit](https://www.mongodb.com/docs/manual/reference/limits/#mongodb-limit-Aggregation-Pipeline-Stages) for a large authorized result set, particularly for a [non-selective `$ne` predicate](https://www.mongodb.com/docs/manual/core/query-optimization/#create-selective-queries). Disk spill prevents an avoidable request failure but can increase latency and temporary disk I/O.

Only the returned page is decoded into Go conversation-list result structs. Application memory for both conversation candidates and membership authorization is therefore `O(limit)` rather than `O(total authorized groups or conversations)`. MongoDB can still scan, sort, group, or spill more records internally; the page-size guarantee is about service-process memory, not database work.

### Pagination and fork behavior

The cursor remains the ID of the last returned conversation, and results remain newest first. The current implementations are not deterministic when two conversations have the same `created_at`: PostgreSQL and SQLite sort only by `created_at`, while one MongoDB path already uses the conversation ID as a tie-breaker. This enhancement standardizes agent and admin conversation lists on `(created_at DESC, id DESC)` and applies an `afterCursor` anchor as:

```text
created_at < anchor.created_at
OR (created_at = anchor.created_at AND id < anchor.id)
```

This does not change the cursor's wire format. It closes same-timestamp gaps and gives the new MongoDB aggregation one behavior to match. For public APIs, the cursor ID must resolve to a conversation in a group visible to the authenticated user. For admin APIs, it must resolve to an existing conversation. It need not still match the current metadata, archive, ancestry, or fork-mode filter because those mutable fields can change between page requests. An unknown or unauthorized cursor returns REST `400 Bad Request` or gRPC `INVALID_ARGUMENT`.

Pagination is keyset-based but not snapshot-isolated across requests. Concurrent creation, archival, or metadata updates can move a conversation into or out of the filtered set between pages. This is the existing list consistency model; callers that need a point-in-time snapshot must copy results or use a future snapshot API.

For example, assume a root has `status=waiting` and its more recently updated fork has `status=running`. With `mode=latest-fork&metadata=status=waiting`, the root represents the fork tree because it is the most recently updated conversation in that tree that matches the predicate. The same rule applies to not-equal and repeated predicates.

No datastore may post-filter metadata predicates in Go. PostgreSQL and SQLite keep `LIMIT` in SQL. MongoDB keeps `$limit` in the aggregation pipeline and no longer preloads memberships for conversation listing. The service therefore retains conversation result data proportional to the requested page size instead of the number of candidate conversations. Existing bounded title-query post-filtering is separate and is not expanded by this enhancement.

### Compatibility

This enhancement does not change persisted data, datastore schemas, or metadata values. It needs no migration.

The OpenAPI REST representation changes, and generated Go and TypeScript clients expose a list of expression strings instead of a string map. The server-side legacy REST path gives existing clients a transition period even though it is no longer advertised in OpenAPI. The gRPC change is additive on the wire, and the old field numbers remain available and deprecated.

Roll out the feature in this order:

1. Deploy the new server to every instance behind a load balancer. It accepts the old REST and gRPC equality forms and advertises filter contract version 1 with maximum 5.
2. Confirm that every instance reports the new capability before enabling clients that send repeated or not-equal filters.
3. Deploy clients that use the new REST representation or that capability-gate the new gRPC field.
4. Before a server rollback, disable new client filter use or roll clients back first. Rolling back servers while new gRPC clients remain enabled can silently remove filtering.

A capability response from one instance is not proof that every instance in a mixed-version load-balanced pool supports the feature. Fleet convergence is therefore a rollout requirement, not merely a client-side check. Removing either legacy compatibility path requires a separate enhancement and usage evidence.

### Security considerations

The five-predicate limit bounds query construction and reduces the cost a caller can add to one list request. Existing page-size and request-size limits remain unchanged.

The service validates keys before building JSON paths or BSON field names. SQL implementations bind keys and values. Query builders choose `=` or `!=` from the internal enum and never copy an operator string from the request into SQL.

The change does not alter authorization. Public authorization and metadata predicates are conjoined in the datastore query, so filtering cannot make an inaccessible conversation visible. Admin filters retain existing role, scope, and justification checks.

Validation errors identify the predicate index and problem but do not echo the predicate value. The service does not add metadata keys or values as metric labels or structured log fields. Callers should not place secrets in query parameters because infrastructure access logs commonly record request URLs.

### Reliability and observability

Conversation listing is read-only and safe to retry under the existing request deadline. Client cancellation propagates through SQL queries and MongoDB aggregation. Datastore errors, including disk-spill or temporary-storage failures, return the existing internal/unavailable transport error rather than a partial page.

No new metric label includes a filter key, operator, or value. Existing HTTP and gRPC request duration/status metrics and store-operation latency measure the feature without creating unbounded cardinality. During rollout, operators should compare list latency and error rates by datastore and inspect PostgreSQL plans, SQLite query timing, and MongoDB aggregation spill/slow-query data for representative equality and not-equal workloads. The five-predicate cap bounds query shape, not the number of rows examined, so it is not a latency guarantee.

## Design decisions

| Decision | Reason |
| --- | --- |
| Limit requests to five predicates | Negative predicates can be expensive, and more than five conversation-state conditions is not a demonstrated use case. |
| Combine predicates only with `AND` | Repeated query parameters have one clear meaning and do not require a nested expression language. |
| Use typed gRPC predicates | Protobuf can represent the operator directly and should not expose REST parsing rules. |
| Preserve duplicate keys | Expressions such as `status=waiting` and `status!=running` are valid conjunctions. |
| Keep not-equal existence-sensitive | Treating a missing key as not equal makes schema omissions look like explicit state and differs across datastore-native operators. |
| Keep the legacy REST and gRPC equality forms temporarily | A server-first rollout can support existing clients while new clients adopt the repeated contract. Mixed forms are rejected instead of guessed. |
| Advertise a version and maximum in capabilities | Old protobuf servers ignore unknown request fields, so a wire-compatible addition needs explicit semantic feature detection. |
| Extend the existing gRPC list RPCs | Capability gating avoids parallel versioned RPCs while preserving the established request and response types. |
| Standardize the conversation-list tie-breaker | `(created_at DESC, id DESC)` prevents skipped rows with equal timestamps and makes pagination identical across stores. |
| Add no general-purpose filter language | Conversation metadata filtering needs two string operators. Episodic memory search has different types, indexing, and policy constraints. |

## Testing

### API behavior matrix

Run the same behavior through agent REST, admin REST, agent gRPC, and admin gRPC:

| Case | Expected result |
| --- | --- |
| One equality predicate | Matching scalar string values only. |
| One not-equal predicate | Different scalar string values only. |
| Two predicates on different keys | Both must match. |
| Two predicates on the same key | Both must match without key deduplication. |
| Five predicates | Request accepted. |
| Six predicates | REST `400`; gRPC `INVALID_ARGUMENT`. |
| Empty requested value | Request accepted and compared as a string. |
| Value contains `!` or `=` | Value parsed without truncation. |
| Percent-encoded literal `+` | Value remains `+`, while an unescaped `+` follows form decoding and becomes a space. |
| Case or Unicode normalization differs | No match; comparison is binary and exact. |
| Missing, null, numeric, boolean, array, or object value | No match for either operator. |
| Invalid key, missing operator, or unknown operator | Transport validation error. |
| Unspecified or unknown gRPC enum value | `INVALID_ARGUMENT`. |
| Legacy REST deep-object parameter | One equality predicate. |
| Mixed legacy and new REST parameters | `400 Bad Request`. |
| Legacy gRPC key/value pair | One equality predicate. |
| Mixed legacy and new gRPC fields | `INVALID_ARGUMENT`. |
| Paginated result | Full pages and stable cursors over the filtered set. |
| Equal `created_at` values across a page boundary | No skipped or repeated conversation. |
| Unknown or unauthorized cursor | REST `400`; gRPC `INVALID_ARGUMENT`. |
| `latest-fork` result | Most recently updated matching conversation per fork tree. |
| Capability discovery | Version `1` and maximum `5` on REST and gRPC. |

The REST feature can adapt this scenario:

```gherkin
Scenario: Repeated metadata predicates use AND semantics
  Given I create a conversation with request:
  """
  {
    "title": "Waiting worker",
    "metadata": {
      "status": "waiting",
      "agent-id": "worker-1"
    }
  }
  """
  And the response status should be 201
  When I call GET "/v1/conversations?mode=all&metadata=status!=running&metadata=agent-id=worker-1"
  Then the response status should be 200
  And the response should contain 1 conversation
```

The gRPC feature can adapt this scenario:

```gherkin
Scenario: Admin gRPC rejects more than five metadata predicates
  Given I am authenticated as admin user "alice"
  When I send gRPC request "AdminConversationsService/ListConversations" with body:
  """
  metadata_filters { key: "a" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "1" }
  metadata_filters { key: "b" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "2" }
  metadata_filters { key: "c" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "3" }
  metadata_filters { key: "d" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "4" }
  metadata_filters { key: "e" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "5" }
  metadata_filters { key: "f" comparison: CONVERSATION_METADATA_COMPARISON_EQUAL value: "6" }
  """
  Then the gRPC response should have status "INVALID_ARGUMENT"
```

Unit tests for `internal/registry/store` cover expression parsing, transport-independent validation, the five-predicate boundary, duplicate-key preservation, legacy REST and gRPC conversion, mixed-form rejection, and cursor-anchor validation where applicable.

Store tests run the same equality, not-equal, type, duplicate-key, same-timestamp pagination, invalid-cursor, and `latest-fork` cases against PostgreSQL, SQLite, and MongoDB. MongoDB tests also create more candidates and memberships than the page limit and confirm that the aggregation returns at most `limit + 1` result documents to Go. A pipeline-construction test verifies that metadata `$match` and membership authorization precede representative selection, the final `$limit` is `limit + 1`, and aggregation enables disk use.

Capability tests verify the REST and gRPC values and document that missing or zero values mean unsupported. Regeneration checks the Go OpenAPI server and client types, Go protobuf types, both TypeScript clients, and Python protobuf stubs. Java compile checks the Quarkus and Spring REST and protobuf client modules against the updated contracts.

## Tasks

- [x] Replace the agent and admin OpenAPI metadata parameter with a repeated expression list capped at five.
- [x] Add typed repeated metadata predicates to the agent and admin protobuf requests.
- [x] Preserve and deprecate the legacy REST deep-object parameter and gRPC key/value fields; reject mixed forms.
- [x] Advertise filter contract version 1 and maximum 5 through REST and gRPC capabilities.
- [x] Replace `MetadataKeyFilter` with the shared predicate slice and central validation.
- [x] Update agent and admin REST parsing and error handling.
- [x] Update agent and admin gRPC conversion and error handling.
- [x] Apply all predicates in PostgreSQL before fork selection and pagination.
- [x] Apply all predicates in SQLite before fork selection and pagination.
- [x] Apply all predicates in MongoDB with `$and` and string type guards.
- [x] Move MongoDB agent/admin conversation listing, membership authorization, roots selection, latest-fork collapse, sorting, and limiting into aggregation pipelines with disk use enabled.
- [x] Standardize agent and admin conversation-list ordering and cursor predicates on `(created_at DESC, id DESC)` in all stores.
- [x] Regenerate Go, TypeScript, and Python contract artifacts.
- [x] Add agent and admin REST and gRPC BDD coverage.
- [x] Add PostgreSQL, SQLite, MongoDB, and parser unit coverage.
- [x] Update agent and admin conversation documentation with equality, not-equal, repeated-filter, limit, compatibility, and rollout examples.
- [x] Update `internal/FACTS.md` and this enhancement as implementation details become final.
- [x] Run the verification commands below.

## Files to Modify

| Area | Files |
| --- | --- |
| Agent REST contract | `contracts/openapi/openapi.yml` |
| Admin REST contract | `contracts/openapi/openapi-admin.yml` |
| gRPC contract | `contracts/protobuf/memory/v1/memory_service.proto` |
| Capability summary | `internal/service/capabilities/summary.go`, `internal/service/capabilities/summary_test.go` |
| Filter model and validation | `internal/registry/store/metadata.go`, `internal/registry/store/metadata_test.go`, `internal/registry/store/plugin.go` |
| Agent and admin REST handlers | `internal/plugin/route/conversations/conversations.go`, `internal/plugin/route/admin/admin.go` |
| Agent and admin gRPC handlers | `internal/grpc/server.go` and focused gRPC unit tests |
| Store wrapper | `internal/plugin/store/metrics/metrics.go` |
| PostgreSQL | `internal/plugin/store/postgres/postgres.go`, `internal/plugin/store/postgres/metadata_filter_test.go` |
| SQLite | `internal/plugin/store/sqlite/sqlite.go`, `internal/plugin/store/sqlite/metadata_filter_test.go` |
| MongoDB | `internal/plugin/store/mongo/mongo.go`, `internal/plugin/store/mongo/metadata_filter_test.go` |
| REST BDD | `internal/bdd/testdata/features/conversation-metadata-rest.feature`, `internal/bdd/testdata/features/admin-conversation-metadata-rest.feature` |
| Capability BDD | `internal/bdd/testdata/features/capabilities-rest.feature`, `internal/bdd/testdata/features-grpc/capabilities-grpc.feature` |
| gRPC BDD | `internal/bdd/testdata/features-grpc/metadata-and-conversation-patch-grpc.feature` |
| Generated Go contracts | `internal/generated/api/api.gen.go`, `internal/generated/apiclient/apiclient.gen.go`, `internal/generated/admin/admin.gen.go`, `internal/generated/pb/memory/v1/memory_service.pb.go` |
| Generated TypeScript clients | `frontends/chat-frontend/src/client/`, `frontends/developer/src/api/generated/` |
| Generated Python protobuf | `python/langchain/memory_service_langchain/grpc/memory/v1/memory_service_pb2.py` |
| User documentation | `site/src/pages/docs/concepts/conversations.md`, `site/src/pages/docs/concepts/admin-apis.mdx` |
| Repository knowledge | `internal/FACTS.md` |
| Enhancement status | `docs/enhancements/116-repeatable-conversation-metadata-filters.md` |

Java REST and protobuf sources are generated under Maven `target` directories and are not committed. The Quarkus and Spring REST and protobuf modules compile the updated contracts during verification.

## Verification

```bash
# Regenerate and format contract-derived sources.
task generate > generate.log 2>&1
rg -n "ERROR|FAIL|panic" generate.log

# Run filter parser and datastore tests.
CGO_ENABLED=1 go test -race -tags='sqlite_fts5' \
  ./internal/registry/store \
  ./internal/service/capabilities \
  ./internal/plugin/store/postgres \
  ./internal/plugin/store/sqlite \
  ./internal/plugin/store/mongo > metadata-filter-tests.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" metadata-filter-tests.log

# Run agent/admin REST and gRPC scenarios with test-fixture authentication.
task test:bdd:sqlite > metadata-filter-bdd.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" metadata-filter-bdd.log

# Compile all Go packages affected by the store interface and generated contracts.
go build ./... > go-build.log 2>&1
rg -n "ERROR|FAIL|panic|undefined:" go-build.log

# Verify generated Python protobuf stubs and package installation.
task verify:python > python-verify.log 2>&1
rg -n "ERROR|FAIL|panic" python-verify.log

# Compile the Quarkus and Spring REST and protobuf integrations.
./java/mvnw -f java/pom.xml \
  -pl :memory-service-rest-quarkus,:memory-service-rest-spring,:memory-service-proto-quarkus,:memory-service-proto-spring \
  -am compile > java-contract-compile.log 2>&1
rg -n "ERROR|FAILURE|Compilation failure" java-contract-compile.log

# Verify generated TypeScript clients and site documentation.
npm --prefix frontends/chat-frontend run lint
npm --prefix frontends/chat-frontend run build
npm --prefix frontends/developer run lint
npm --prefix frontends/developer run build
npm --prefix site run build
```

An empty `rg` result after each successful command is expected.

## Non-goals

- Add `OR`, grouping, nesting, range comparison, regular expressions, existence tests, or case-insensitive comparison.
- Compare numeric, boolean, array, object, or JSON null metadata values.
- Add metadata indexes for selected application keys.
- Change conversation authorization, archive behavior, ancestry meaning, or cursor wire format beyond standardizing the existing newest-first tie-breaker.
- Remove the legacy REST or gRPC equality compatibility paths.
- Reuse this filter contract for episodic memory search.
