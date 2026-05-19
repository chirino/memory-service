---
status: implemented
---

# Enhancement 103: Entry Filters for Event Streams

> **Status**: Implemented.

## Summary

Add entry-specific filters to REST SSE and gRPC event subscriptions so consumers can subscribe to only the entry events they can process. The initial filters are `entry_channels`, `entry_content_types`, and `entry_roles`, evaluated after events are published and before they are delivered to each subscriber.

## Implementation Notes

Implemented in the Go service with shared entry filter and metadata helpers under `internal/service/eventstream`. REST SSE, admin SSE, gRPC live streams, and replay paths all apply the same subscriber-side filters before `detail=full` enrichment. REST append, REST sync, gRPC append, and gRPC sync now publish filterable `entry.created` events with `entry_channel`, `entry_content_type`, and optional `entry_role` metadata. Sync no-op results do not emit events.

The reusable Java proxy classes intentionally remain generic. The frontend-facing Quarkus and Spring example handlers filter proxied `entry` notifications to `entry_channel=history` before forwarding them to browser clients, so other applications can still use the proxy classes for broader event access.

## Motivation

The event stream currently supports broad `kinds` filtering, but `kind=entry` is too coarse for processors that only need a subset of entries. The turn-traces processor needs history entries to detect user/agent turn boundaries and context entries to build a child LLM observation, but most other entry traffic is irrelevant.

Entries listing already has entry-level selection behavior. Event subscriptions should offer a similar contract without pushing processor-specific logic into the event bus.

## Design

### REST Query Parameters

Add these query parameters to both `/v1/events` and `/v1/admin/events`:

| Parameter | Default | Meaning |
| --- | --- | --- |
| `entry_channels` | `history` | Comma-separated entry channels to deliver for `entry` events. |
| `entry_content_types` | Any | Comma-separated entry content types to deliver for `entry` events. |
| `entry_roles` | Any | Comma-separated entry roles to deliver for `entry` events. |

Example:

```http
GET /v1/events?kinds=entry&entry_channels=history,context&entry_content_types=history,history/lc4j,LC4J
```

Parameter names use snake_case to match the gRPC field names and the existing protobuf style. Query parsing may also accept repeated values for generated client compatibility, but comma-separated strings are the documented REST form.

### gRPC Request Fields

Extend `SubscribeEventsRequest`:

```protobuf
message SubscribeEventsRequest {
  repeated bytes conversation_ids = 1;
  repeated string kinds = 2;
  optional string after_cursor = 3;
  optional string detail = 4;
  optional EventScope scope = 5;
  optional string justification = 6;

  // Optional: entry channel filter for entry events. Defaults to ["history"].
  repeated string entry_channels = 7;
  // Optional: entry content type filter for entry events. Empty means any.
  repeated string entry_content_types = 8;
  // Optional: entry role filter for entry events. Empty means any.
  repeated string entry_roles = 9;
}
```

### Filter Semantics

Filters are applied after the event has been published and received by the subscriber path. They must not change event routing, bus fan-out, outbox persistence, or relay behavior.

For non-entry events, entry filters do not apply.

For entry events:

```text
entry.channel in entry_channels
AND entry.contentType in entry_content_types, when provided
AND entry.role in entry_roles, when provided
```

If `entry_channels` is omitted, it defaults to `history`. If the caller wants context entries, it must opt in explicitly:

```http
GET /v1/events?kinds=entry&entry_channels=history,context
```

If `entry_content_types` is omitted, all content types match. If `entry_roles` is omitted, all roles match.

The default applies only to entry events. A subscription with no `kinds` filter still receives non-entry events normally, but entry events are limited to `history` unless `entry_channels` is set.

`entry_role` is derived from the stored entry content, not from a top-level persisted `model.Entry` field. For content that is a JSON array, use the first object element's string `role` field when present. Normalize role comparisons case-insensitively, but preserve the original role string in event metadata. If no role can be derived, omit `entry_role`; role-filtered subscriptions do not match that event.

### Event Metadata

Entry-created event summary payloads need stable metadata so subscriber-side filters can run without loading every entry:

```json
{
  "conversation": "0c2a...",
  "conversation_group": "7d9f...",
  "entry": "9a31...",
  "entry_channel": "history",
  "entry_content_type": "history/lc4j",
  "entry_role": "AI"
}
```

The filter implementation should prefer these summary fields. If an older or non-conforming event lacks the fields, the subscription path may load the entry detail to evaluate filters. New event producers must include the metadata.

This is still subscriber-side filtering: metadata is added to the event payload before publish, but filtering decisions happen only after publish in REST SSE, admin SSE, gRPC live delivery, and outbox replay. Filtering must happen before `detail=full` enrichment can replace the compact event payload with the full entry object.

### Event Producer Coverage

All paths that create entries must publish equivalent filterable `entry.created` events:

| Path | Requirement |
| --- | --- |
| REST `POST /v1/conversations/{id}/entries` | Include entry filter metadata in each event. |
| REST `POST /v1/conversations/{id}/entries/sync` | Emit an `entry.created` event when sync creates a new entry, including context entries. |
| gRPC append/sync entry APIs | Match REST event behavior. |
| Postgres/Mongo/SQLite outbox writes | Persist the same metadata in outbox event `data`. |

Sync no-op results must not emit an event.

The REST and gRPC append paths currently diverge: REST append publishes entry events, while gRPC append/sync and REST sync must be brought onto the same publish/outbox path as part of this enhancement.

### Processor Usage

Trusted processors that need internal context entries should subscribe with explicit entry filters:

```text
kinds = entry,conversation
entry_channels = history,context
entry_content_types = history,history/lc4j,LC4J
```

This lets processors receive history turn boundaries and LC4J context entries without receiving unrelated entry channels. No current Go processor package required a code update in this implementation.

### Chat Example Proxy Usage

The Quarkus and Spring chat example apps expose frontend-facing routes around Memory Service APIs. Their handlers must not forward non-history entry events to browser clients.

This prevents browser clients and end users from receiving context-channel entries through chat app event subscriptions. Context entries may contain LLM prompt context, summaries, retrieval results, tool payloads, or other agent-internal material that should remain available to agent apps and observability processors but not to general end-user UI event streams.

The frontend should not rely on the server default for this security boundary. In this implementation, the reusable proxy classes stay unrestricted and the frontend-facing handlers apply the history-only restriction before forwarding events.

## Testing

Add Cucumber coverage for live delivery and replay.

BDD coverage must explicitly prove that event subscribers can receive `context` channel entries when they opt in with `entry_channels=context` or `entry_channels=history,context`. This is a required acceptance case because the turn-traces processor depends on context entry delivery for LLM child observations.

```gherkin
Scenario: Entry event stream defaults to history entries
  Given I have a conversation with title "Entry Filter Defaults"
  And "alice" is connected to the SSE event stream filtered to kinds "entry"
  When I append a "context" entry to the conversation
  Then "alice" should not receive an SSE event with kind "entry"
  When I append a "history" entry to the conversation
  Then "alice" should receive an SSE event with kind "entry" and event "created"
```

```gherkin
Scenario: Entry event stream can opt into context entries
  Given I have a conversation with title "Entry Context Filter"
  And "alice" is connected to the SSE event stream filtered to kinds "entry" and entry channels "context"
  When I append a "context" entry to the conversation
  Then "alice" should receive an SSE event with kind "entry" and event "created"
  And the last SSE event data field "entry_channel" should equal "context"
```

```gherkin
Scenario: Entry event stream can receive history and context entries together
  Given I have a conversation with title "Entry Multi-Channel Filter"
  And "alice" is connected to the SSE event stream filtered to kinds "entry" and entry channels "history,context"
  When I append a "context" entry to the conversation
  Then "alice" should receive an SSE event with kind "entry" and event "created"
  And the last SSE event data field "entry_channel" should equal "context"
  When I append a "history" entry to the conversation
  Then "alice" should receive an SSE event with kind "entry" and event "created"
  And the last SSE event data field "entry_channel" should equal "history"
```

```gherkin
Scenario: Sync-created context entries are emitted and filterable
  Given I have a conversation with title "Sync Context Events"
  And "alice" is connected to the SSE event stream filtered to kinds "entry" and entry channels "context"
  When I sync an LC4J context entry to the conversation
  Then the response status should be 200
  And "alice" should receive an SSE event with kind "entry" and event "created"
  And the last SSE event data field "entry_channel" should equal "context"
```

```gherkin
Scenario: Postgres outbox replay preserves entry filters
  Given I have a conversation with title "Entry Filter Replay"
  And I append a "context" entry to the conversation
  And I append a "history" entry to the conversation
  When "alice" connects to the SSE event stream after cursor "start" filtered to kinds "entry" and entry channels "context"
  Then "alice" should receive an SSE event with kind "entry" and event "created"
  And the last SSE event data field "entry_channel" should equal "context"
```

Add gRPC BDD parity for `SubscribeEventsRequest.entry_channels`, `entry_content_types`, and `entry_roles`.

At minimum, gRPC BDD must include a scenario where `SubscribeEventsRequest.entry_channels = ["context"]` receives a context entry event and does not require `detail=full` to identify the channel, because summary event metadata must be sufficient for filtering and assertions.

Add example-app proxy coverage that verifies frontend-facing event streams do not forward context-channel entry events. The test should append or sync a context entry through Memory Service, keep the chat app proxy subscribed, and assert that the proxied frontend stream receives no `entry` event for that context write. A companion history-entry assertion should prove the stream is still active and delivering normal user-visible history invalidations.

## Tasks

- [x] Update OpenAPI agent and admin event stream parameters.
- [x] Update protobuf `SubscribeEventsRequest` and regenerate Go bindings.
- [x] Add shared entry event filter parsing/evaluation code for REST and gRPC.
- [x] Include `entry_channel`, `entry_content_type`, and `entry_role` in entry-created event payloads.
- [x] Emit filterable events for sync-created entries.
- [x] Apply filters to REST SSE live delivery.
- [x] Apply filters to REST SSE outbox replay.
- [x] Apply filters to admin SSE live delivery and replay.
- [x] Apply filters to gRPC live delivery and replay.
- [x] Add an event metadata helper shared by REST append, REST sync, and gRPC append/sync so `entry_role` derivation stays consistent across stores.
- [x] Keep reusable Java proxy classes generic and restrict frontend-facing Quarkus handlers to history entry notifications.
- [x] Keep reusable Java proxy classes generic and restrict frontend-facing Spring handlers to history entry notifications.
- [x] Add BDD coverage for defaults, explicit context filter delivery, content-type/role filters, sync-created context entries, and outbox replay.
- [ ] Add site/checkpoint proxy tests proving context-channel entries are not forwarded to frontend event streams.

## Files to Modify

| File | Change |
| --- | --- |
| `contracts/openapi/openapi.yml` | Add agent SSE query parameters. |
| `contracts/openapi/openapi-admin.yml` | Add admin SSE query parameters. |
| `contracts/protobuf/memory/v1/memory_service.proto` | Add gRPC entry filter fields. |
| `internal/generated/**` | Regenerate OpenAPI and protobuf code. |
| `internal/plugin/route/agent/events.go` | Parse and apply REST SSE entry filters for live and replay events. |
| `internal/plugin/route/admin/events.go` | Parse and apply admin SSE entry filters for live and replay events. |
| `internal/grpc/server.go` | Parse and apply gRPC entry filters for live and replay events. |
| `internal/plugin/route/entries/entries.go` | Add entry filter metadata to created-entry events and emit events for sync-created entries. |
| `java/quarkus/examples/chat-quarkus/**` | Filter frontend-facing event notifications to history-channel entries. |
| `java/spring/examples/chat-spring/**` | Filter frontend-facing event notifications to history-channel entries. |
| `java/quarkus/examples/doc-checkpoints/**` | Update checkpoint examples with event proxies when they proxy event streams. |
| `java/spring/examples/doc-checkpoints/**` | Update checkpoint examples with event proxies when they proxy event streams. |
| `internal/bdd/testdata/features*/` | Add REST/gRPC live and replay filter scenarios. |
| `internal/sitebdd/` | Add or update docs/checkpoint tests for chat proxy event-stream filtering when applicable. |

## Security Considerations

Context entries are agent-internal by default. They may include system instructions, retrieved memory snippets, summaries, tool inputs/outputs, or provider-specific prompt state. Event filtering must therefore support two distinct use cases:

- End-user frontend event streams receive history-channel entry invalidations only.
- Trusted processors and admin tools explicitly opt into context-channel entries when needed.

The Memory Service endpoint defaults omitted `entry_channels` to `history`, but example app handlers still enforce history-only entry forwarding explicitly. Security-sensitive frontend behavior should be visible in application code and tests rather than relying only on a server-side default that future maintainers may overlook.

## Verification

```bash
# Regenerate contracts after OpenAPI/protobuf changes.
task generate

# Run focused Go tests for event stream routes, gRPC streams, and event helpers.
go test ./internal/plugin/route/agent ./internal/plugin/route/admin ./internal/grpc ./internal/service/eventstream -count=1 > event-filter-go.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" event-filter-go.log

# Run BDD suites that cover event streams, gRPC streams, and outbox replay.
go test ./internal/bdd -run 'TestFeatures$|TestFeatures(PgOutbox|SQLiteOutbox|MongoOutbox)' -count=1 > event-filter-bdd.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" event-filter-bdd.log
```

## Non-Goals

- Do not filter before publishing to the event bus.
- Do not add a generic query language for event streams.
- Do not add time-range or metadata filters in this enhancement.
- Do not change membership authorization semantics.

## Design Decisions

Subscriber-side filtering keeps the event bus and outbox as durable broadcast infrastructure. Producers only add enough metadata for efficient downstream filtering; they do not decide which subscribers should receive which entry categories.

The `entry_channels=history` default preserves the current practical behavior for common consumers that only care about conversation messages, while requiring processors that need context entries to opt in explicitly.
