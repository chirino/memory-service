---
status: proposed
---

# Enhancement 102: Checkpointed Event Processors and Turn Traces

> **Status**: Proposed.

## Summary

Add a reusable `memory-service process` command family for checkpointed gRPC event processors. The first processor, `memory-service process turn-traces`, consumes `EventStreamService.SubscribeEvents`, detects conversation turns, emits one OpenTelemetry root span per turn, and records durable progress through `AdminCheckpointService` so it can recover after failure.

## Motivation

Several future background jobs need the same operational shape: subscribe to durable Memory Service events, do bounded processing, persist progress, resume after crashes, and avoid coupling processor-specific state to the core event stream. Examples include cognition processors that create new memories from events and observability processors that derive traces from conversation activity.

Turn-level tracing is a concrete first use case. Today conversation observability is mostly request-oriented: individual API calls and storage operations can be traced, but a user-facing "turn" crosses multiple entries, context writes, response recording lifecycle events, and possibly multiple service calls. Operators need a trace root that represents the logical unit users care about:

- a user appends a new history input entry
- the service and agent append context entries for that same conversation
- the agent eventually appends a final history output entry

Real conversations are messier than the ideal flow. A user can submit a new history input before the previous agent output lands, an agent can fail, and outbox delivery can reconnect or replay. The processor must model those cases explicitly while keeping checkpoint advancement safe.

## Design

### Command Shape

Add a new top-level command family:

```bash
memory-service process turn-traces
```

`process` owns common event-processor flags and configuration. Each child command supplies processor-specific logic.

Common flags:

| Flag | Purpose |
| --- | --- |
| `--endpoint` | gRPC Memory Service endpoint. |
| `--client-id` | Stable admin checkpoint key and processor identity. |
| `--api-key` / token configuration | Admin/service-principal credentials for event and checkpoint APIs. |
| `--after-cursor` | Optional bootstrap cursor when no checkpoint exists; supports `start` for oldest retained outbox event. |
| `--checkpoint-interval` | Maximum time between checkpoint flushes while work is advancing. |
| `--scope` | Event stream scope; allowed values are `user` and `admin`, default `admin`. |

`turn-traces` flags:

| Flag | Purpose |
| --- | --- |
| `--idle-timeout` | Close an open turn when no relevant event arrives before the timeout. |
| `--max-turn-age` | Force close long-running turns even if no final agent history entry arrives. |
| `--overlap-policy` | How to handle a new user input while a prior turn is open; initial value is `cut-short`. |
| `--langfuse-session-id` | Which Memory Service identifier becomes `langfuse.session.id`; allowed values are `conversation` and `conversation-group`, default `conversation`. |
| `--otel-service-name` | OpenTelemetry service name for emitted spans. |
| `--otel-exporter-*` | Standard OTLP exporter configuration, using existing OpenTelemetry environment variables where possible. |
| `--dry-run` | Process and checkpoint normally, but log derived turn boundaries instead of exporting spans. |

Event kinds, checkpoint content type, and admin-stream justification are static processor constants, not user flags. For `turn-traces`:

| Setting | Value |
| --- | --- |
| Event kinds | `entry` and `conversation`; add `response` only in a later enrichment phase. |
| Checkpoint content type | `application/vnd.memory-service.turn-trace-checkpoint+json;v=1`. |
| Admin justification | `turn-traces processor deriving conversation turn telemetry from event stream`. |

### Reusable Processor Runtime

Create an internal package for checkpointed event processors, for example `internal/cmd/process/runtime`.

The runtime owns:

1. load checkpoint through `AdminCheckpointService.GetCheckpoint`
2. open `EventStreamService.SubscribeEvents` with the configured scope, `detail=full`, the processor's static event kinds, and `after_cursor` from checkpoint or bootstrap config
3. dispatch each event to the processor implementation
4. persist checkpoint state with `AdminCheckpointService.PutCheckpoint`
5. reconnect with exponential backoff and resume from the last safely checkpointed cursor
6. flush checkpoint state on idle, shutdown, and before process exit

The runtime must not advance `lastEventCursor` past work that is not durably represented in the checkpoint. For processors with open windows, "processed" means the event has been reduced into checkpoint state, not necessarily that the final derived output has already been emitted.

```go
type EventProcessor interface {
    ContentType() string
    Load(state json.RawMessage) error
    Handle(ctx context.Context, event EventEnvelope) error
    Snapshot() (json.RawMessage, error)
    Flush(ctx context.Context) error
}
```

The runtime should keep processor state small. If a future processor needs large durable work queues, it should add a processor-specific store instead of growing the admin checkpoint payload without bound.

### Event Scope

`--scope` controls only `EventStreamService.SubscribeEvents` filtering:

| CLI value | gRPC value | Behavior |
| --- | --- | --- |
| `user` | `EVENT_SCOPE_AUTHORIZED` | Subscribe using the authenticated user's normal conversation membership filtering. |
| `admin` | `EVENT_SCOPE_ADMIN` | Subscribe using admin/auditor event stream behavior across conversations. |

Production observability processors usually run with `--scope=admin` so they can derive traces for all conversations. Tests should run with `--scope=user` and a dedicated test user that only owns or can read the conversations created by that test. This keeps event-stream assertions isolated even when the backing test environment contains unrelated conversations.

Checkpointing still uses `AdminCheckpointService` in both modes. Current gRPC checkpoint APIs require the admin role, and if the authenticated client ID is non-empty it must match the checkpoint `client_id`. Deployments that run `--scope=user` must therefore authenticate as the desired user for `EVENT_SCOPE_AUTHORIZED`, carry the admin role for checkpoint calls, and use a checkpoint `--client-id` that matches the authenticated client ID when one is present. A future non-admin checkpoint mechanism would be needed to relax that split.

### Checkpoint Schema

The turn-trace processor stores JSON under a versioned content type:

```text
application/vnd.memory-service.turn-trace-checkpoint+json;v=1
```

Example checkpoint:

```json
{
  "version": 1,
  "runtimeId": "turn-traces",
  "runtimeVersion": "dev",
  "lastEventCursor": "pg:00000000000000000042",
  "updatedAt": "2026-05-19T12:00:00Z",
  "openTurns": {
    "7b1d8b91-2b7c-42b3-85fb-7d0630babc2d": {
      "turnId": "7b1d8b91-2b7c-42b3-85fb-7d0630babc2d:01J...",
      "conversationId": "7b1d8b91-2b7c-42b3-85fb-7d0630babc2d",
      "startCursor": "pg:00000000000000000040",
      "latestCursor": "pg:00000000000000000042",
      "userEntryId": "6a9091ad-d06a-4c4a-bb4c-f28bdfcf75fb",
      "contextEntryIds": ["2a965d25-eab7-4af4-b74d-2a4df2cda9a3"],
      "startedAt": "2026-05-19T11:59:58Z",
      "latestEventAt": "2026-05-19T12:00:00Z"
    }
  }
}
```

`openTurns` is bounded by configuration. If the checkpoint would exceed the configured maximum, the processor should close the oldest open turn with `memory_service.turn.end_reason="checkpoint_window_limit"` before accepting more cursor progress.

### Turn Detection

The processor subscribes to `entry` and `conversation` events with full detail. Entry events drive normal turn boundaries; conversation events are used for owner cache population and archive-triggered turn closure. `response` events remain out of scope for Phase 1 boundaries and can be added later for enrichment.

A turn starts when the processor observes a history entry whose content has role `USER` in a conversation. A turn ends when one of these conditions is met:

| End reason | Rule |
| --- | --- |
| `agent_history_entry` | A history entry with role `AI` is appended to the same conversation after the turn start. |
| `new_user_input` | A new history `USER` entry arrives for the same conversation before an `AI` history entry; the previous turn is cut short and a new turn starts. |
| `idle_timeout` | No relevant events arrive for the open turn before `--idle-timeout`. |
| `max_turn_age` | The turn remains open longer than `--max-turn-age`. |
| `conversation_archived` | A full-detail `conversation` update event shows `archivedAt` while a turn is open. |
| `shutdown` | The processor is shutting down and has been configured to close open spans instead of preserving them. |

Conversation archive detection requires `conversation` events in the subscription. Current full-detail conversation events expose `archivedAt` on the internal conversation detail payload, so the processor can close open turns when an updated conversation event reports an archive timestamp.

Context entries appended between the user history entry and the closing event are attached to the open turn. Multiple context entries are expected. The processor records entry IDs and event cursors in span attributes rather than copying full content into telemetry.

If event replay delivers duplicate events, the open-turn state must deduplicate by event cursor and entry ID. If replay starts from an older cursor than the checkpoint's `lastEventCursor`, events at or before the checkpoint cursor are ignored unless they are needed to restore open-turn state already present in the checkpoint.

### Langfuse OpenTelemetry Output

The OpenTelemetry collector target for this processor is Langfuse. Langfuse receives OTLP over HTTP on `/api/public/otel`; its docs currently state that OTLP gRPC is not supported for direct ingestion. Deployments may still send spans to a local OpenTelemetry collector over gRPC if that collector exports to Langfuse with `otlphttp`.

Direct-to-Langfuse deployments should use normal OTLP HTTP environment configuration:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=https://cloud.langfuse.com/api/public/otel
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic ${AUTH_STRING},x-langfuse-ingestion-version=4"
```

Langfuse maps `langfuse.*` OpenTelemetry attributes directly into its trace and observation data model. For manual instrumentation, use these explicit attributes instead of relying on generic catch-all metadata. Trace-level attributes that should support filtering or aggregation must be present on every emitted span in the trace. The first implementation emits one root span per turn, so setting these attributes on the root span is sufficient until child observations are added.

Each closed turn emits one root span. When context entries were observed before the closing AI history entry, the processor also emits a child `memory-service.llm` generation span whose input is the aggregated context entry text and whose output is the closing AI history text. The root span is not a child of the original request trace because the processor may run asynchronously after the requests that created the entries. Instead, it uses links to source entry/request trace IDs when those identifiers become available in event payloads or entry metadata.

Span name:

```text
memory-service.turn
```

The default span name can be overridden with `--langfuse-name` / `MEMORY_SERVICE_TURN_TRACES_LANGFUSE_NAME`. The complete attribute mapping below is the source of truth for Langfuse fields. Phase 1 emits turn input/output text from history entries plus IDs, cursors, counts, timestamps, status, and processor identity. It does not attach attachment bytes or provider payloads that are not already represented in the Memory Service history entries.

### Complete Langfuse OTEL Attribute Mapping

Langfuse's OpenTelemetry integration maps specific trace-level and observation-level attributes into its data model. The turn-trace processor should document every mapped attribute and either populate it from Memory Service state or intentionally leave it unset.

Trace-level attributes:

| Langfuse field | OTEL attribute | Memory Service mapping | Phase 1 behavior |
| --- | --- | --- | --- |
| `name` | `langfuse.trace.name` | Configured Langfuse name, default `memory-service.turn`. | Set. |
| `name` | Root span name | Configured Langfuse name, default `memory-service.turn`. | Set by span creation. |
| `userId` | `langfuse.user.id` | `ConversationDetail.ownerUserId`; fallback to starting user history entry `userId` only if conversation lookup fails. | Set when available. |
| `userId` | `user.id` | Same value as `langfuse.user.id` for ecosystem compatibility. | Optional duplicate. |
| `sessionId` | `langfuse.session.id` | `conversationId` by default; `conversationGroupId` when `--langfuse-session-id=conversation-group`. | Set. |
| `sessionId` | `session.id` | Same value as `langfuse.session.id` for ecosystem compatibility. | Optional duplicate. |
| `release` | `langfuse.release` | Processor build version or git SHA. | Set when available. |
| `public` | `langfuse.trace.public` | No Memory Service equivalent. | Leave unset. |
| `tags` | `langfuse.trace.tags` | Static tags plus derived turn state: `memory-service`, `turn-trace`, `end:<reason>`, optionally `agent:<agentId>` if short and non-sensitive. | Set. |
| `metadata` | `langfuse.trace.metadata.conversation_id` | Conversation ID from entry or conversation event. | Set. |
| `metadata` | `langfuse.trace.metadata.conversation_group_id` | Conversation group ID from preserved summary event or enriched event payload. | Set when available; may require enrichment change. |
| `metadata` | `langfuse.trace.metadata.agent_id` | Conversation-level or entry-level `agentId`. | Set when available. |
| `metadata` | `langfuse.trace.metadata.client_id` | Conversation-level or entry-level `clientId`, admin telemetry only. | Set when available. |
| `metadata` | `langfuse.trace.metadata.turn_id` | Processor-generated turn ID. | Set. |
| `metadata` | `langfuse.trace.metadata.turn_end_reason` | Derived turn end reason. | Set. |
| `metadata` | `langfuse.trace.metadata.start_cursor` | User-input event cursor. | Set. |
| `metadata` | `langfuse.trace.metadata.end_cursor` | Closing event cursor. | Set. |
| `metadata` | `langfuse.trace.metadata.user_entry_id` | Starting history `USER` entry ID. | Set. |
| `metadata` | `langfuse.trace.metadata.agent_entry_id` | Closing history `AI` entry ID. | Set when available. |
| `metadata` | `langfuse.trace.metadata.context_entry_count` | Count of context entries observed between start and end. | Set. |
| `input` | `langfuse.trace.input` | Starting user entry content. | Set from history entry text so getting-started traces are inspectable in Langfuse. |
| `input` | Root span observation input | Same as above. | Set through `langfuse.observation.input` and `input.value`. |
| `output` | `langfuse.trace.output` | Closing agent entry content. | Set from history entry text so getting-started traces are inspectable in Langfuse. |
| `output` | Root span observation output | Same as above. | Set through `langfuse.observation.output` and `output.value`. |
| `version` | `langfuse.version` | Turn-trace processor schema version, for example `turn-traces-v1`. | Set. |
| `environment` | `langfuse.environment` | Processor config, `LANGFUSE_TRACING_ENVIRONMENT`, or `OTEL_RESOURCE_ATTRIBUTES`. | Set when configured. |
| `environment` | `deployment.environment`, `deployment.environment.name` | Same value as `langfuse.environment` when using generic OTEL resource attributes. | Optional duplicate/resource attribute. |

Observation-level attributes for the root turn span:

| Langfuse field | OTEL attribute | Memory Service mapping | Phase 1 behavior |
| --- | --- | --- | --- |
| `type` | `langfuse.observation.type` | Constant `span`. | Set. |
| `type` | Presence of `model` attribute | No model is invoked by the turn-trace processor. | Do not set model attributes on the root turn span. |
| `level` | `langfuse.observation.level` | `DEFAULT` for normal turns; `WARNING` for cut-short or timeout turns; `ERROR` for processor/export errors if a span is emitted. | Set from end reason/status. |
| `level` | Span status code | Same as above through OTEL span status. | Set on errors. |
| `statusMessage` | `langfuse.observation.status_message` | Human-readable processor status such as export retry exhaustion or malformed event payload. | Set only for warnings/errors. |
| `statusMessage` | Span status message | Same value as `langfuse.observation.status_message`. | Set only for warnings/errors. |
| `metadata` | `langfuse.observation.metadata.conversation_id` | Conversation ID. | Set for filterability at observation level. |
| `metadata` | `langfuse.observation.metadata.turn_id` | Processor-generated turn ID. | Set. |
| `metadata` | `langfuse.observation.metadata.turn_end_reason` | Derived turn end reason. | Set. |
| `metadata` | `langfuse.observation.metadata.context_entry_count` | Count of context entries. | Set. |
| `input` | `langfuse.observation.input`, `gen_ai.prompt`, `input.value`, `mlflow.spanInputs` | Starting user entry content. | Set `langfuse.observation.input` and `input.value`. |
| `output` | `langfuse.observation.output`, `gen_ai.completion`, `output.value`, `mlflow.spanOutputs` | Closing agent entry content. | Set `langfuse.observation.output` and `output.value`. |
| `model` | `langfuse.observation.model.name`, `gen_ai.request.model`, `gen_ai.response.model`, `llm.model_name`, `model` | Not present in conversation/entry events today. Future response/model events could supply provider model name. | Leave unset in Phase 1. |
| `modelParameters` | `langfuse.observation.model.parameters`, `gen_ai.request.*`, `llm.invocation_parameters.*` | Not present in conversation/entry events today. | Leave unset. |
| `usage` | `langfuse.observation.usage_details`, `gen_ai.usage.*`, `llm.token_count.*` | Not present in conversation/entry events today. Future response recorder metadata could supply token counts. | Leave unset. |
| `cost` | `langfuse.observation.cost_details`, `gen_ai.usage.cost` | Not present in conversation/entry events today. | Leave unset. |
| `prompt` | `langfuse.observation.prompt.name`, `langfuse.observation.prompt.version` | Not present in conversation/entry events today. | Leave unset. |
| `completionStartTime` | `langfuse.observation.completion_start_time` | Not present in conversation/entry events today. Response recording start events could provide an approximation later. | Leave unset in Phase 1. |
| `version` | `langfuse.version` | Turn-trace processor schema version. | Set. |
| `environment` | `langfuse.environment`, `deployment.environment`, `deployment.environment.name` | Processor environment config/resource attribute. | Set when configured. |

Child generation span attributes:

| Langfuse field | OTEL attribute | Memory Service mapping | Phase 1 behavior |
| --- | --- | --- | --- |
| `name` | Child span name | Constant `memory-service.llm`. | Set when context entries are observed. |
| `type` | `langfuse.observation.type` | Constant `generation`. | Set. |
| `input` | `langfuse.observation.input`, `input.value`, `gen_ai.prompt` | Aggregated context entry text, prefixed by context entry ID when available. | Set when context text is available. |
| `output` | `langfuse.observation.output`, `output.value`, `gen_ai.completion` | Closing AI history entry text. | Set when available. |
| `metadata` | `langfuse.observation.metadata.context_entry_ids` | Context entry IDs observed during the turn. | Set. |
| `metadata` | `langfuse.observation.metadata.context_entry_count` | Count of context entries observed during the turn. | Set. |

### Future Content-Type Enrichment

Memory context entries can contain framework-specific payloads from different agent runtimes. Examples already present in the project include history subtypes such as `history/lc4j` and context content types such as `LC4J`; future integrations may add Vercel AI or other provider-specific envelopes.

Phase 1 should not introspect arbitrary context payloads. Later phases can add a registry of typed content adapters:

```go
type EntryContentAdapter interface {
    ContentTypes() []string
    ExtractObservationAttributes(entry EntryEnvelope) (ObservationEnrichment, error)
}
```

Adapters should be opt-in per known `contentType` and schema version. They must fail closed: unknown content types, unknown schema versions, malformed payloads, or ambiguous fields should produce no enrichment rather than guessed Langfuse attributes.

Potential future mappings:

| Source content | Extractable Langfuse fields | Notes |
| --- | --- | --- |
| LangChain4j history/context payloads (`history/lc4j`, `LC4J`) | `langfuse.observation.model.name`, `langfuse.observation.model.parameters`, `langfuse.observation.usage_details`, prompt metadata when explicitly present. | Only map fields that are structurally represented by the LC4J payload, not parsed from free-form text. |
| Vercel AI SDK payloads | Model name, provider metadata, token usage, finish reason, tool-call metadata. | Requires confirming the exact stored content schema used by the TypeScript integration. |
| Response recording lifecycle events | `langfuse.observation.completion_start_time`, response recording ID, failure status. | Useful for timing and status without exporting content. |
| Provider-specific usage records | `langfuse.observation.usage_details`, `langfuse.observation.cost_details`. | Cost should be mapped only when provided by the source payload or a deterministic pricing table is configured. |

Adapter outputs must preserve the Phase 1 privacy rule by default: do not export raw prompts, completions, tool outputs, or attachment content. If a deployment later wants content export, it should require an explicit policy flag plus masking/redaction tests.

### Langfuse Attribute Extraction

Current gRPC `detail=full` event enrichment returns JSON bytes containing full internal `model.Entry` JSON for `entry` events and full internal `ConversationDetail` JSON for `conversation` events. It does not return the generated proto `Entry` message inside `EventNotification.data`. For `entry` events, the processor can extract:

| Field | Extraction |
| --- | --- |
| Conversation ID | `entry.conversationId`. |
| Conversation group ID | Preserved as `conversationGroupId` from the original event summary when full-detail event enrichment swaps in the full entry JSON. |
| Entry ID | `entry.id`. |
| Entry channel | `entry.channel`. |
| Entry content type | `entry.contentType`. |
| Entry role | Parse `entry.content` when `channel=history` and `contentType=history` or `history/<subtype>`; use role `USER` to start turns and role `AI` to close turns. |
| Entry user ID | `entry.userId`; use as a fallback only when conversation owner is unavailable. |
| Entry client ID / agent ID | `entry.clientId` and `entry.agentId` when present. |
| Entry timestamp | `entry.createdAt`, used for span start/end timing when available. |

For `conversation` events, the processor can extract `id`, `ownerUserId`, `agentId`, `metadata`, `createdAt`, `updatedAt`, `archivedAt`, and the enriched `conversationGroupId`. `clientId` is present on the Go store structs but intentionally serialized as `json:"-"`, so it is not available from current full-detail conversation event JSON.

For Langfuse `userId`, prefer a small conversation cache keyed by conversation ID. Populate it from `conversation` events or an admin conversation lookup, using `ConversationDetail.ownerUserId`. If that lookup fails, fall back to the starting user entry's `userId` and record `langfuse.trace.metadata.user_source="entry_user_id"`. This avoids assigning agent/context entries as end users.

For Langfuse `sessionId`, follow `--langfuse-session-id`. `conversation` maps one Memory Service conversation to one Langfuse session. `conversation-group` groups forks into the same Langfuse session by using the `conversationGroupId` preserved in full-detail event payloads.

### Langfuse References

This design follows the Langfuse OpenTelemetry documentation:

- [Langfuse data model](https://langfuse.com/docs/observability/data-model): traces represent a single interaction; sessions group multi-turn conversations.
- [Langfuse OpenTelemetry integration](https://langfuse.com/integrations/native/opentelemetry): direct ingestion uses OTLP HTTP, `langfuse.*` attributes map to Langfuse fields, and trace-level attributes should be propagated to all spans.
- [Langfuse sessions](https://langfuse.com/docs/observability/features/sessions): `sessionId` groups traces and must be a US-ASCII string under 200 characters.
- [Langfuse metadata](https://langfuse.com/docs/observability/features/metadata): propagated metadata values are short strings and top-level metadata keys are filterable.
- [Langfuse Public API](https://langfuse.com/docs/api-and-data-platform/features/public-api): tests can retrieve ingested traces and observations through Langfuse APIs.
- [Langfuse query via SDKs](https://langfuse.com/docs/api-and-data-platform/features/query-via-sdk): SDKs expose trace list/get and observations queries with filters and pagination.
- [Langfuse headless initialization](https://langfuse.com/self-hosting/administration/headless-initialization): self-hosted test stacks can create deterministic organizations, projects, users, and API keys through `LANGFUSE_INIT_*` environment variables.

### Entry Payload Parsing

The processor should parse `EventNotification.data` as the full-detail entry JSON currently emitted by gRPC event enrichment. History entries are identified by `channel="history"` and `contentType="history"` or `history/<subtype>`. The role is read from the history content block, matching the contract that history content contains a single object with `role` plus text or subtype-specific event payloads.

If a full event does not include enough entry detail, the processor may fetch the entry through the admin gRPC entry API before advancing the checkpoint. That fetch result must be reduced into checkpoint state or span output before `lastEventCursor` advances.

### Failure Behavior

- Export failures do not advance the checkpoint past the closing event unless the configured policy is `drop-on-export-failure`.
- The default policy retries span export with backoff and keeps the turn closed-but-uncheckpointed until export succeeds or the processor exits.
- Checkpoint write failures stop cursor advancement and force reconnect/retry.
- `SubscribeEvents` errors reconnect from the last checkpointed cursor.
- `UNIMPLEMENTED` for replay after cursor is a startup error unless the processor is configured for tail-only mode.

### Package Layout

```text
internal/cmd/commands/process.go
internal/cmd/process/
  command.go
  runtime/
    checkpoint.go
    event_client.go
    processor.go
  turntraces/
    command.go
    detector.go
    detector_test.go
    otel.go
    checkpoint.go
```

## Testing

### Unit Tests

- Turn detector starts on a user history entry and closes on the next agent history entry.
- A new user history entry cuts short the previous open turn and starts a new one.
- Context entries are attributed to the currently open turn.
- Duplicate events do not duplicate context entry IDs or emit duplicate spans.
- Checkpoint load restores open turns and resumes from `lastEventCursor`.
- Checkpoint snapshots remain bounded when many conversations are active.

### Integration Scenarios

```gherkin
Feature: Checkpointed turn trace processor

  Scenario: Processor resumes from stored checkpoint
    Given an admin checkpoint for client "turn-traces" contains lastEventCursor "cursor-10"
    When the turn trace processor starts with scope "user"
    Then it subscribes to gRPC events after cursor "cursor-10" with scope AUTHORIZED

  Scenario: Processor emits one span for a completed turn
    Given Langfuse is running in a Testcontainers stack
    And the turn trace processor is configured to export OTLP HTTP traces to Langfuse
    And the turn trace processor starts with scope "user"
    And a conversation owned by the test user receives a history USER entry
    And the conversation receives two context entries
    And the conversation receives a history AI entry
    When the turn trace processor consumes the entry events
    Then Langfuse eventually contains one trace named "memory-service.turn" for the conversation session
    And the Langfuse trace has userId equal to the conversation owner user ID
    And the Langfuse trace metadata contains the user entry ID and agent entry ID
    And the Langfuse observation metadata has context entry count 2
    And the checkpoint advances to the AI entry event cursor

  Scenario: New user input cuts short an open turn
    Given a conversation receives a history USER entry "u1"
    When the same conversation receives a history USER entry "u2" before an AI entry
    Then the processor exports a turn span for "u1" with end reason "new_user_input"
    And a new open turn starts at "u2"

  Scenario: Export failure prevents unsafe checkpoint advancement
    Given a completed turn is detected
    And the OpenTelemetry exporter is unavailable
    When the processor handles the closing event
    Then the checkpoint does not advance past the closing event
    And the processor retries the export
```

### Langfuse Testcontainers Verification

Integration tests should start a real self-hosted Langfuse stack with Testcontainers, not a fake OTLP collector. The preferred setup is a `testcontainers-go` helper based on the current Langfuse self-hosting compose topology, including Langfuse Web, Langfuse Worker, PostgreSQL, ClickHouse, Redis/Valkey, and object storage services required by Langfuse v3.

Use Langfuse headless initialization environment variables to create a deterministic test organization, project, user, and API keys:

```text
LANGFUSE_INIT_ORG_ID=memory-service-tests
LANGFUSE_INIT_PROJECT_ID=turn-traces
LANGFUSE_INIT_PROJECT_PUBLIC_KEY=lf_pk_test_turn_traces
LANGFUSE_INIT_PROJECT_SECRET_KEY=lf_sk_test_turn_traces
LANGFUSE_INIT_USER_EMAIL=memory-service-tests@example.com
LANGFUSE_INIT_USER_PASSWORD=memory-service-tests
```

The test should configure the processor with:

```bash
--scope=user
--otel-service-name=memory-service-turn-traces-test
OTEL_EXPORTER_OTLP_ENDPOINT=http://<langfuse-web>/api/public/otel
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic <base64(public:secret)>,x-langfuse-ingestion-version=4"
```

Use `--scope=user` in tests so the event stream only sees conversations visible to the test user. Each test should create a unique user and conversation, or a unique conversation under a dedicated test user, then assert by session ID and metadata so unrelated traces cannot satisfy the test.

After the processor flushes spans, tests should poll Langfuse's Public API until ingestion completes. The verification path should query:

1. trace list filtered by `session_id=<conversationId>` and tag `turn-trace`
2. trace get by returned trace ID
3. observations list filtered by the trace ID

Assertions should verify the data landed in Langfuse, not merely that the processor called an exporter:

- trace name is `memory-service.turn` unless overridden with `--langfuse-name`
- `sessionId` matches the selected `--langfuse-session-id` mapping
- `userId` matches the Memory Service conversation owner
- trace metadata includes `conversation_id`, `turn_id`, `turn_end_reason`, `start_cursor`, `end_cursor`, and entry IDs
- observation type is `SPAN`
- observation metadata includes `context_entry_count`
- trace input/output and observation input/output match the detected history USER/AI entry text

The Langfuse API is eventually consistent because OTLP ingestion is queued and processed by the Langfuse worker. Tests should poll with a bounded timeout rather than assuming immediate trace visibility.

## Tasks

- [x] Add the `memory-service process` command family.
- [x] Implement shared gRPC event subscription and checkpoint runtime.
- [x] Add `--scope=user|admin` support and map it to gRPC authorized/admin event scopes.
- [x] Add versioned JSON checkpoint schemas for reusable processor state.
- [x] Implement `turn-traces` event parsing and turn detection.
- [x] Add OpenTelemetry span export with safe attributes and no raw message content.
- [x] Add `--langfuse-session-id=conversation|conversation-group` mapping support.
- [x] Implement and test the documented Langfuse OTEL attribute mapping.
- [x] Add a future-phase extension point for `contentType`-specific observation enrichment adapters.
- [x] Add bounded open-turn checkpoint state and duplicate-event handling.
- [x] Subscribe to `conversation` events and close open turns on archived conversations.
- [x] Preserve or expose conversation group ID in enriched events before enabling `--langfuse-session-id=conversation-group`.
- [x] Add unit tests for turn detection, checkpoint restore, and checkpoint bounding.
- [x] Add BDD coverage for live turn export and restart recovery through the shared `turntraces.StartProcessor` lifecycle API.
- [x] Add BDD or integration coverage for checkpoint-safe export failure behavior.
- [x] Add Langfuse Testcontainers integration coverage that verifies ingested traces through Langfuse Public APIs.
- [x] Document configuration for the processor command and OTLP exporter.

## Files to Modify

| File | Changes |
| --- | --- |
| `internal/cmd/commands/process.go` | Register the new `process` command family. |
| `internal/cmd/process/command.go` | Define common command flags and child command registration. |
| `internal/cmd/process/runtime/` | Add shared event subscription, checkpoint, reconnect, and processor interfaces. |
| `internal/cmd/process/turntraces/` | Add the turn-trace processor, checkpoint schema, detector, and OpenTelemetry exporter. |
| `internal/cmd/process/turntraces/*_integration_test.go` | Add user-scope processor tests that start Langfuse with Testcontainers and query Langfuse APIs. |
| `internal/testutil/testlangfuse/` | Shared Langfuse Testcontainers stack helper for OTLP integration tests. |
| `contracts/protobuf/memory/v1/memory_service.proto` | No planned contract changes; use existing `SubscribeEvents` and `AdminCheckpointService`. |
| `internal/generated/proto/` | Regenerate only if the proto contract changes. |
| `internal/bdd/testdata/features/` | Add integration coverage if the processor is exercised through BDD. |
| `docs/` or `site/` | Document command usage, checkpoint behavior, and OpenTelemetry configuration. |

## Verification

```bash
# Compile affected Go command packages
go test ./internal/cmd/commands ./internal/cmd/process/... -run '^$'

# Run focused processor tests
go test ./internal/cmd/process/... -count=1

# Run Langfuse integration tests; requires Docker/Testcontainers
go test -tags integration ./internal/cmd/process/turntraces -run TestLangfuse -count=1

# Run BDD processor coverage; requires Docker/Testcontainers
go test ./internal/bdd -run TestFeaturesPgOutbox -count=1

# If protobuf changes are needed, regenerate and compile broadly
go generate ./...
go test ./internal/grpc ./internal/cmd/process/... -run '^$'
```

## Non-Goals

- Do not create a generic durable job queue in this enhancement.
- Do not store raw conversation content in OpenTelemetry spans.
- Do not make turn traces the source of truth for conversation state.
- Do not implement memory-generation cognition processors here; this enhancement only creates the reusable runtime they can later share.
- Do not change event outbox retention or delivery semantics.
- Do not introspect arbitrary context entry payloads for model, usage, prompt, cost, or tool-call data in Phase 1.

## Design Decisions

### Prefer a Subcommand Over an In-Server Background Worker

The processor should run as a separate operational role even if it ships in the same binary. This keeps failure, scaling, credentials, and OpenTelemetry exporter configuration independent from the serving path.

### Use Admin Checkpoints as the Only Required Durable State

Turn tracing needs small open-window state and a replay cursor, so `AdminCheckpointService` is enough. Future processors that need large queues, long evidence windows, or exactly-once side effects should add their own state store while still using the checkpoint cursor for stream recovery.

### Emit Root Spans With Links Instead of Parent Spans

Turn spans are derived asynchronously and may close long after the originating request has ended. Making them roots avoids inventing inaccurate parentage. Links can preserve correlation when request trace IDs are available.

## Security Considerations

When the processor subscribes with `--scope=admin`, it must use dedicated credentials with the smallest practical roles. When it subscribes with `--scope=user`, event visibility is narrowed by the authenticated user's conversation memberships, but checkpoint writes still require the admin role and a matching checkpoint client ID unless a future non-admin checkpoint mechanism is added. Span attributes include history-entry input/output text for Langfuse usability plus IDs, counts, timestamps, end reasons, and processor identity; they must not include attachment bytes or provider payloads that are not already represented in Memory Service history entries.

Checkpoints are encrypted at rest by the existing admin checkpoint implementation. Open-turn checkpoints include the starting history input text so a restarted processor can export a complete Langfuse trace when the AI response arrives.

## Resolved Decisions

- Turn spans include `conversation_group_id` only when full-detail event enrichment preserves it from the event summary.
- Export failure defaults to blocking checkpoint advancement. Deployments may opt into at-most-once telemetry only with an explicit `drop-on-export-failure` policy.
- Response recording events remain enrichment-only until response lifecycle correlation is mature enough to close turns without risking false completions.
