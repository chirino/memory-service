---
status: proposed
---

# Enhancement 105: Chat Quarkus Cognition Memory RAG

> **Status**: Proposed.

## Summary

Enhance the `chat-quarkus` example so new conversations start with a compact cognition-produced profile context snapshot and every turn can retrieve additional episodic memories when there is a very close semantic match. The app should use Memory Service `/v1/memories` and `/v1/memories/search` as governed retrieval surfaces, while keeping current conversation replay in `ChatMemoryStore`.

## Motivation

The cognition processor described in [099](099-quarkus-cognition-processor.md) writes durable user memories under namespaces such as `["user", "bob", "cognition.v1", "preference"]`. Enhancement [100](100-enhanced-memory-search.md) defines `/v1/memories/search` as the generic retrieval surface for those memories. The current `chat-quarkus` example records conversation history and persists LangChain4j chat memory, but a new conversation does not benefit from useful memories extracted from previous conversations.

Observed against the local dev stack, Bob's prior conversation produced a preference memory: "User prefers algorithms not to be implemented recursively when possible." A new conversation asking for algorithm help should retrieve that preference before inference instead of requiring Bob to repeat it.

The profile context snapshot work planned in `/Users/chirino/sandbox/cognitive-memory/cognition-processor-quarkus/TODO/profile-context-snapshots.md` will add a single `profile_context/latest` memory under `["user", <userId>, "cognition.v1", "profile_context"]`. That snapshot is the right default context at the start of every conversation. Atomic memories remain useful on any turn, including the first turn, but they should be injected only when the current turn has a strong semantic match; otherwise they risk distracting the model with stale or merely adjacent facts.

The goal is to demonstrate the intended application-side loop:

1. User sends a new message.
2. Chat memory supplies the current conversation context.
3. On the first turn, cognition retrieval loads the user's compact `profile_context/latest` snapshot.
4. On every turn, cognition retrieval performs ad hoc memory search and adds atomic memories only when semantic search returns very close matches.
5. LangChain4j sends a single enriched prompt to the model.
6. The normal history/response-recording flow stores only the conversation turn.

## Design

### Recommendation

Add an app-local LangChain4j RAG layer to `java/quarkus/examples/chat-quarkus`. Use `@RegisterAiService(retrievalAugmentor = CognitionMemoryRetrievalAugmentor.class)` on `Agent` so retrieval is part of the AI service invocation, not a separate tool call the model may forget to use.

The retrieval implementation should be deliberately narrow:

- only query the authenticated user's cognition namespace
- load `profile_context/latest` at the start of a new conversation
- retrieve ad hoc memory context on every turn, including the first turn, but only for very close semantic matches
- retrieve a small top-k set when additional search is warranted
- inject compact memory snippets, not raw memory JSON
- fail open by logging and returning no retrieved content when Memory Service search is unavailable
- keep current conversation replay and response recording unchanged

### Retrieval Scope

The retriever builds namespace paths from the authenticated user:

```java
List<String> cognitionPrefix = List.of("user", userId, "cognition.v1");
List<String> profileContextNamespace = List.of("user", userId, "cognition.v1", "profile_context");
List<String> profileInputNamespace = List.of("user", userId, "cognition.v1", "profile_input");
```

The app must not trust a request parameter for `userId`. It should use `SecurityIdentity` or the extension's existing security helper path so Memory Service policy and bearer-token propagation remain authoritative.

Attachments are not included in the retrieval query in phase 1; the model still receives attachments through the existing `@UserMessage List<Content>` parameter.

### Retrieval Policy

`CognitionMemoryRetrievalAugmentor` runs on every `Agent.chat(...)` call, but it does not retrieve the same context on every turn. It should inspect `AugmentationRequest.metadata().chatMemory()` before deciding whether to add profile context, while ad hoc memory search remains available on every turn.

First-turn behavior:

- Detect the start of a conversation using the loaded chat memory user-turn count.
- Fetch `profile_context/latest` directly from `/v1/memories`.
- Inject the snapshot's `value.content` as the baseline durable profile context.
- Do not require semantic similarity for the profile context snapshot.
- Also run the normal ad hoc semantic memory search for the current user message.

Every-turn ad hoc search behavior:

- Run semantic memory search only when the latest user message is substantial enough to search.
- Inject atomic memories only when returned `score` values meet a high configured threshold.
- If semantic search is unavailable or results do not include scores, skip atomic memory injection.
- Skip `profile_context` and `profile_input` namespaces in ad hoc search results; the profile snapshot has its own first-turn direct load, and user-authored profile inputs are consumed by the cognition processor, not injected directly.
- Do not reload `profile_context/latest` on later turns by default.

The first-turn check must be verified with an integration test because LangChain4j may include the current user message in `metadata.chatMemory()`. Treat `userTurns <= 1` as first-turn candidate behavior unless tests prove a stricter predicate is correct. `CognitionMemoryRetrievalAugmentor` should own this decision because it receives the raw `AugmentationRequest`; the content retriever can receive derived retrieval flags or query metadata but should not independently guess conversation-start state.

Example Memory Service request:

```json
{
  "namespace_prefix": ["user", "bob", "cognition.v1"],
  "query": "write an efficient sorting algorithm in Java",
  "filter": {
    "memoryKind": {
      "$in": [
        "preference",
        "procedure",
        "decision",
        "fact",
        "summary",
        "bridge",
        "topic"
      ]
    },
    "confidence": { "$in": ["medium", "high"] }
  },
  "limit": 8,
  "include_usage": true
}
```

Until the deployed memory policy extracts `memoryKind` and `confidence` as safe attributes, the retriever should omit those filters and search only by prefix plus query. Adding the cognition attribute policy is part of this enhancement because otherwise `chat-quarkus` cannot demonstrate governed, type-aware retrieval.

For ad hoc atomic memory retrieval, client-side score gating is mandatory:

```java
boolean closeEnough(MemoryItem item) {
    return item.getScore() != null && item.getScore() >= config.minSemanticScore();
}
```

Client-side namespace gating is also mandatory because prefix search can return non-atomic cognition memories when filters are unavailable:

```java
boolean adHocCandidate(MemoryItem item) {
    List<String> namespace = item.getNamespace();
    String kind = stringValue(item.getValue(), "kind");
    return closeEnough(item)
            && !namespaceEndsWith(namespace, "profile_context")
            && !namespaceEndsWith(namespace, "profile_input")
            && !"profile_context".equals(kind)
            && !"profile_context_inputs".equals(kind);
}
```

`namespaceEndsWith(...)` should be a null-safe final-segment check, not a string-prefix comparison on a joined namespace.

The profile context snapshot is loaded by direct key:

```http
GET /v1/memories?ns=user&ns=bob&ns=cognition.v1&ns=profile_context&key=latest
```

### LangChain4j Wiring

`Agent` currently uses `@RegisterAiService` with tools and a sub-agent tool provider:

```java
@RegisterAiService(
        tools = {
            ImageGenerationTool.class,
            WebSearchTool.class,
        },
        toolProviderSupplier = SubAgentToolProviderSupplier.class)
public interface Agent {
    // ...
}
```

Add a retrieval augmentor supplier:

```java
@RegisterAiService(
        tools = {
            ImageGenerationTool.class,
            WebSearchTool.class,
        },
        toolProviderSupplier = SubAgentToolProviderSupplier.class,
        retrievalAugmentor = CognitionMemoryRetrievalAugmentor.class)
public interface Agent {
    // ...
}
```

The supplier creates a custom `RetrievalAugmentor` that performs first-turn detection, runs ad hoc search, and injects the combined content once:

```java
@ApplicationScoped
public class CognitionMemoryRetrievalAugmentor implements Supplier<RetrievalAugmentor> {

    @Inject CognitionMemoryProfileContext profileContext;
    @Inject CognitionMemoryContentRetriever retriever;
    @Inject CognitionMemoryContentInjector injector;

    @Override
    public RetrievalAugmentor get() {
        return request -> {
            List<dev.langchain4j.rag.content.Content> contents = new ArrayList<>();
            if (isFirstTurn(request)) {
                contents.addAll(profileContext.retrieve(request));
            }
            contents.addAll(retriever.retrieve(Query.from(userText(request.chatMessage()), request.metadata())));
            return new AugmentationResult(
                    injector.inject(contents, request.chatMessage()),
                    contents);
        };
    }
}
```

Quarkus LangChain4j 1.7.0 exposes `retrievalAugmentor` on `@RegisterAiService`, but does not expose LangChain4j's lower-level `storeRetrievedContentInChatMemory(false)` builder option through the annotation path. The implementation must verify whether augmented prompt content is persisted through `MemoryServiceChatMemoryStore`. If retrieved snippets are persisted as system messages, add a small guard in the chat-memory store or use a custom augmentor path that keeps retrieved content request-scoped only.

### Content Retriever

`CognitionMemoryContentRetriever` should:

- resolve the bearer token using the same `SecurityHelper` pattern used by the existing Memory Service clients
- build `MemoriesApi` through `MemoryServiceApiBuilder.withBearerAuth(token)`
- call `searchMemories(...)` for ad hoc atomic memories on every turn when close-match retrieval is warranted
- convert each returned `MemoryItem` to LangChain4j `Content`
- bound total injected text by item count and character/token budget
- remove duplicate `value.content` strings
- require scores above `chat.cognition-rag.min-semantic-score` for ad hoc atomic memories
- exclude `profile_context` and `profile_input` results from ad hoc injection

`CognitionMemoryProfileContext` should:

- resolve the same bearer token as the ad hoc retriever
- call `getMemory(...)` for `profile_context/latest` only when the augmentor identifies a first turn
- convert the profile snapshot's `value.content` into one LangChain4j `Content` block
- treat a missing profile snapshot (`404`) as no profile context, not as a chat failure when fail-open is enabled

The profile context text should be injected as a single compact block:

```text
Durable profile context
source: profile_context/latest

Active Goals
...

Preferences And Working Style
...
```

Ad hoc atomic memory text should be compact and explicit:

```text
Durable user memory
kind: preference
confidence: 0.90
memory: User prefers algorithms not to be implemented recursively when possible.
source: conversation c2dcd0d9-b29b-450c-ad9a-1c72d35d43e6
```

The retriever should ignore memories that do not have a string `value.content` or equivalent supported atomic-memory text field. It should not expose raw citations, raw evidence packs, provider prompts, API keys, `clientId`, or arbitrary unreviewed metadata.

### Content Injection

Use a custom content injector so retrieved profile context and atomic memories are framed as advisory context, not instructions from the user:

```text
Durable context from previous conversations is provided below.
Use the profile context as baseline background for this conversation.
Use ad hoc memories only when they are clearly relevant to the current request.
If durable context conflicts with the user's current message, follow the current message and mention the conflict when useful.

{{contents}}
```

The existing `Agent` system prompt should add a short policy:

```text
Use durable user memories when relevant, but do not reveal memory internals unless the user asks.
```

### Configuration

Add app-level config:

| Property                                       | Default        | Purpose                                                                                                               |
| ---------------------------------------------- | -------------- | --------------------------------------------------------------------------------------------------------------------- |
| `chat.cognition-rag.enabled`                   | `false`        | Enables cognition memory retrieval. Set to `true` in the `alt` profile used with compose and the cognition processor. |
| `chat.cognition-rag.namespace-root`            | `cognition.v1` | Namespace segment after `["user", userId]`.                                                                           |
| `chat.cognition-rag.profile-context.enabled`   | `true`         | Fetch `profile_context/latest` at conversation start.                                                                 |
| `chat.cognition-rag.profile-context.key`       | `latest`       | Key for the current profile context snapshot.                                                                         |
| `chat.cognition-rag.additional-search.enabled` | `true`         | Enables close-match atomic memory search on every turn.                                                               |
| `chat.cognition-rag.limit`                     | `8`            | Maximum memory items to retrieve.                                                                                     |
| `chat.cognition-rag.min-query-chars`           | `24`           | Minimum current-message length before ad hoc search.                                                                  |
| `chat.cognition-rag.min-semantic-score`        | `0.82`         | Minimum score for injecting ad hoc atomic memories.                                                                   |
| `chat.cognition-rag.max-chars`                 | `4000`         | Total injected character budget.                                                                                      |
| `chat.cognition-rag.include-usage`             | `true`         | Include usage metadata in ad hoc search responses without incrementing counters.                                      |
| `chat.cognition-rag.fail-open`                 | `true`         | Continue the chat turn if memory retrieval fails.                                                                     |
| `chat.profile-context.inputs.max-items`        | `50`           | Maximum accepted user-authored profile input strings.                                                                 |
| `chat.profile-context.inputs.max-item-chars`   | `1000`         | Maximum length of one submitted profile input string after normalization.                                             |

Profile context retrieval uses direct `GET /v1/memories`, which increments Memory Service usage counters for that profile key. This is intentional so profile-context consumption is visible operationally; ad hoc search still uses `include_usage` only for response enrichment and does not increment usage counters.

The config should live in `chat-quarkus` first. The base profile should leave `chat.cognition-rag.enabled=false` so ordinary Quarkus dev services do not issue failing semantic searches before a cognition processor and profile snapshots exist. The `%alt` profile should set it to `true`, because that profile already targets the external compose stack. The frontend profile-context endpoints are a separate UI facade and should remain available when RAG injection is disabled; missing backing memories should return empty frontend responses rather than disable the UI surface.

### Safe Attribute Policy

The current built-in episodic policy extracts only namespace guard attributes such as `namespace` and `sub`. To support type-aware cognition retrieval, add a deployable cognition `attributes.rego` example, for example under `deploy/episodic-policies/cognition/attributes.rego`, that extracts only safe plaintext attributes. Do not add these cognition-specific fields to the built-in default policy; deployments that run a cognition processor should opt into the richer attribute extraction by setting `MEMORY_SERVICE_EPISODIC_POLICY_DIR`. The policy loader falls back to built-in `authz.rego` and `filter.rego` when a policy directory only overrides `attributes.rego`.

| Attribute         | Source                                                        |
| ----------------- | ------------------------------------------------------------- |
| `memoryKind`      | `value.kind` or final namespace segment                       |
| `runtimeId`       | `value.provenance.runtime_id` or `value.runtime.id`           |
| `runtimeVersion`  | `value.provenance.runtime_version` or `value.runtime.version` |
| `confidence`      | normalized bucket derived from numeric `value.confidence`     |
| `conversationIds` | `value.provenance.conversation_id` / `conversation_ids`       |
| `entryIds`        | `value.provenance.entry_ids`                                  |

Do not extract raw memory content, citations, evidence text, prompts, provider IDs, or internal client metadata as searchable attributes.

### Admin Inspection

The enhancement does not require a new UI, but the implementation should remain easy to inspect through the admin APIs from [104](implemented/104-admin-episodic-memory-exploration.md):

```bash
curl -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "namespace_prefix": ["user", "bob", "cognition.v1"],
    "query": "recursive algorithm preference",
    "justification": "debug chat cognition RAG"
  }' \
  http://localhost:8082/admin/v1/memories/search
```

### Frontend Profile Context API

The chat app should expose a narrow profile-context API to the frontend instead of proxying the full generic `/v1/memories` surface. The generic memory APIs remain backend-only implementation details for this example.

`GET /v1/profile-context` returns the latest derived profile context snapshot for the authenticated user. It is implemented with the same direct `GET /v1/memories` key lookup as first-turn RAG profile retrieval, so viewing this endpoint also increments profile-context usage counters. The frontend response should map the snapshot memory value's `generated_at` field to `generatedAt`.

```json
{
  "exists": true,
  "generatedAt": "2026-06-03T03:00:00Z",
  "content": "Active Goals\n...\n\nPreferences And Working Style\n...",
  "sections": {},
  "conflicts": [],
  "omitted": []
}
```

Missing `profile_context/latest` returns:

```json
{
  "exists": false,
  "content": ""
}
```

The frontend should still show the Manage memory item in the user popup menu for this empty response and render an empty profile-context screen. It should hide the item only when `GET /v1/profile-context` itself returns `404`, which indicates that the agent app does not support this optional API surface.

`GET /v1/profile-context/inputs` returns the user-authored profile input list as strings:

```json
[
  "Prefer non-recursive algorithms when possible.",
  "Use concise implementation plans before code changes."
]
```

These inputs are editable facts or instructions the user explicitly wants the profile context builder to consider. They are not the derived profile context itself.

The chat app must store these user-authored bits as Memory Service episodic memories, not in local application state. `ProfileContextResource` is only a narrow frontend facade over `/v1/memories` for this specific profile-input use case.

Use a single replace operation for editing and deletion:

```http
PUT /v1/profile-context/inputs
```

Request:

```json
[
  "Prefer iterative algorithms when practical.",
  "Use concise implementation plans before code changes."
]
```

The server normalizes the submitted list and writes the accepted list as one Memory Service memory. Editing, deletion, and reordering are all represented by replacing the `inputs` array.

The response returns the accepted active list:

```json
[
  "Prefer iterative algorithms when practical.",
  "Use concise implementation plans before code changes."
]
```

The backing memory value should stay simple. Because Memory Service `value` is modeled as an object/map, store the list inside an object rather than as a top-level JSON array:

```json
{
  "kind": "profile_context_inputs",
  "inputs": [
    "Prefer iterative algorithms when practical.",
    "Use concise implementation plans before code changes."
  ],
  "source": "user",
  "updated_by": "user"
}
```

Store that memory under:

```text
namespace: ["user", <userId>, "cognition.v1", "profile_input"]
key: "latest"
```

The API response intentionally hides the backing key and value object so the frontend can edit a plain string list. If duplicate strings are submitted, the server should normalize and deduplicate them while preserving first occurrence order. `GET /v1/profile-context/inputs` should fetch `profile_input/latest`; a missing memory returns `[]`. Like other direct memory fetches, this increments usage counters for the backing key.

The `PUT` endpoint should validate the submitted array before writing:

- reject non-string values
- trim and normalize whitespace before deduplication
- drop blank strings
- enforce `chat.profile-context.inputs.max-items`
- enforce `chat.profile-context.inputs.max-item-chars`
- return the normalized accepted list in persisted order

An accepted empty list should still upsert `profile_input/latest` with `"inputs": []`. The cognition processor should treat a missing `profile_input/latest` memory and an empty `inputs` array equivalently.

`PUT /v1/profile-context/inputs` updates user-authored inputs only. It must not directly mutate `profile_context/latest`, because that snapshot is derived by the cognition processor. The processor should consume the active `profile_input/latest` memory during the next consolidation run. A future `POST /v1/profile-context/refresh` can request asynchronous snapshot regeneration, but this enhancement does not require synchronous regeneration after every edit.

### Failure Behavior

Memory retrieval is context enrichment, not the primary chat path. With `chat.cognition-rag.fail-open=true`, profile-context retrieval and ad hoc search fail independently: a failed profile fetch should not prevent ad hoc search, and a failed ad hoc search should not suppress a successfully loaded profile context. These conditions return no content for the affected retrieval path and log at `WARN`:

- no active request identity
- no bearer token
- Memory Service profile fetch or search returns `401`, `403`, `503`, or a transport error
- profile context `GET` returns `404`
- semantic search is unavailable for ad hoc retrieval
- result payloads cannot be converted to text

With `fail-open=false`, retrieval failures should fail the chat request so integration tests and strict deployments can catch configuration errors. A missing `profile_context/latest` should still be configurable; local development may run before the profile snapshot job exists.

The frontend profile-context API should not use chat retrieval fail-open semantics. Missing `profile_context/latest` and missing `profile_input/latest` have explicit empty responses, but authentication, authorization, Memory Service transport errors, and malformed backing memory values should return an API error so the frontend can show the profile editor as unavailable instead of silently discarding user-authored context.

## Testing

### Cucumber Scenarios

```gherkin
Feature: Chat Quarkus cognition memory RAG
  Scenario: New conversation uses profile context snapshot
    Given Bob has a cognition memory under ["user","bob","cognition.v1","profile_context"] with key "latest"
    And the profile context content includes "Bob prefers algorithms not to be implemented recursively when possible."
    When Bob starts a new chat conversation asking "Write a sorting algorithm in Java"
    Then the model request should include the profile context content
    And the assistant should prefer an iterative implementation

  Scenario: First turn can also inject close atomic memory matches
    Given Bob has a profile context under ["user","bob","cognition.v1","profile_context"] with key "latest"
    And Bob has a close semantic memory search result with score 0.91
    And chat cognition RAG min semantic score is 0.82
    When Bob starts a new chat conversation
    Then the model request should include the profile context content
    And the model request should include the close memory content

  Scenario: Every turn skips weak atomic memory matches
    Given Bob has a weak semantic memory search result with score 0.52
    And chat cognition RAG min semantic score is 0.82
    When Bob sends a chat message
    Then the model request should not include the weak memory content

  Scenario: Every turn injects close atomic memory matches
    Given Bob has a close semantic memory search result with score 0.91
    And chat cognition RAG min semantic score is 0.82
    When Bob sends a chat message
    Then the model request should include the close memory content

  Scenario: Ad hoc search skips profile context and profile inputs
    Given Bob has a profile_context/latest search result with score 0.96
    And Bob has a profile_input/latest search result with score 0.94
    When Bob sends a later-turn chat message
    Then the model request should not include profile_context/latest through ad hoc search
    And the model request should not include profile_input/latest

  Scenario: User-scoped retrieval does not leak another user's memories
    Given Bob has a profile context under ["user","bob","cognition.v1","profile_context"]
    And Alice has a different profile context under ["user","alice","cognition.v1","profile_context"]
    When Bob sends a chat message
    Then the memory request should use namespace ["user","bob","cognition.v1","profile_context"]
    And the model request should not include Alice's profile context

  Scenario: Frontend lists user-authored profile inputs
    Given Bob has a profile_input/latest memory
    When Bob calls GET "/v1/profile-context/inputs"
    Then the response should be a JSON array of strings
    And the response should include only Bob's profile inputs

  Scenario: Frontend replaces user-authored profile inputs
    Given Bob has profile inputs "Prefer recursive examples" and "Use Java"
    When Bob calls PUT "/v1/profile-context/inputs" with ["Prefer iterative examples", "Use Java"]
    Then the active profile inputs should be ["Prefer iterative examples", "Use Java"]
    And the profile_input/latest memory should contain that accepted list
    And profile_context/latest should not be directly rewritten by the request

  Scenario: Frontend reorders user-authored profile inputs
    Given Bob has profile inputs "Use Java" and "Prefer iterative examples"
    When Bob calls PUT "/v1/profile-context/inputs" with ["Prefer iterative examples", "Use Java"]
    Then the active profile inputs should be returned in the submitted order
    And profile_input/latest should contain the submitted order

  Scenario: Retrieval failure can fail open
    Given chat cognition RAG is enabled
    And fail-open is true
    And Memory Service memory search is unavailable
    When Bob sends a chat message
    Then the chat response should still be generated
    And the application logs the retrieval failure

  Scenario: Retrieval failure can fail closed
    Given chat cognition RAG is enabled
    And fail-open is false
    And Memory Service memory search is unavailable
    When Bob sends a chat message
    Then the chat request should fail with a server error
```

### Unit Tests

- `CognitionMemoryContentRetriever` builds the namespace prefix from the authenticated user.
- `CognitionMemoryRetrievalAugmentor` identifies first-turn state from `AugmentationRequest.metadata().chatMemory()`.
- First-turn retrieval fetches `["user", userId, "cognition.v1", "profile_context"]` key `latest`.
- Later-turn retrieval does not fetch `profile_context/latest` by default.
- First-turn retrieval still runs ad hoc semantic memory search.
- Later-turn retrieval runs ad hoc semantic memory search.
- Search requests include configured `limit`, `include_usage`, and optional safe-attribute filters.
- Ad hoc search is skipped when the latest user message is shorter than `min-query-chars`.
- Ad hoc atomic memory search results below `min-semantic-score` are skipped.
- Ad hoc atomic memory search results without scores are skipped.
- Ad hoc search results from `profile_context` and `profile_input` namespaces are skipped.
- Empty search results produce an empty content list.
- Duplicate memory content is injected once.
- Non-string or malformed memory values are skipped.
- Result formatting includes memory kind, confidence, content, and compact provenance only.
- Fail-open and fail-closed behavior is covered for Memory Service errors.
- Tests verify retrieved content is not written back as persistent conversation context.
- `GET /v1/profile-context` maps `value.generated_at` to response `generatedAt`.
- `GET /v1/profile-context/inputs` returns active profile inputs as an ordered string array.
- `GET /v1/profile-context/inputs` returns `[]` when `profile_input/latest` is missing.
- `PUT /v1/profile-context/inputs` writes one `profile_input/latest` memory with a `value.inputs` array.
- `PUT /v1/profile-context/inputs` persists submitted order in `value.inputs`.
- `PUT /v1/profile-context/inputs` with only blank or duplicate-eliminated empty content writes `value.inputs=[]`.
- Duplicate submitted profile input strings are deduplicated.
- Invalid profile input payloads are rejected or normalized according to the documented validation rules.
- Profile input updates do not directly mutate `profile_context/latest`.

### Integration Tests

Add a `@QuarkusTest` that replaces the chat model with `TestChatModel` and asserts the model request contains retrieved cognition memory text. Use a stubbed or in-memory `MemoriesApi`/retriever for the narrow AI-service wiring test, and one Memory Service-backed test when dev services are available.

## Tasks

- [x] Add `CognitionMemoryRagConfig` config mapping to `chat-quarkus`.
- [x] Add `CognitionMemoryRetrievalAugmentor` supplier.
- [x] Add `CognitionMemoryContentRetriever` backed by Memory Service `MemoriesApi`.
- [x] Fetch `profile_context/latest` at the start of each new conversation.
- [x] Run ad hoc atomic memory search on every turn, including the first turn.
- [x] Gate ad hoc atomic memory search by minimum query length and minimum semantic score.
- [x] Exclude `profile_context` and `profile_input` memories from ad hoc atomic memory injection.
- [x] Add `CognitionMemoryContentInjector` with compact advisory framing.
- [x] Wire `Agent` with `retrievalAugmentor = CognitionMemoryRetrievalAugmentor.class`.
- [x] Update the `Agent` system message with durable-memory usage guidance.
- [x] Add a deployable cognition-safe `attributes.rego` policy example and document `MEMORY_SERVICE_EPISODIC_POLICY_DIR`.
- [ ] Add focused unit tests for first-turn profile context, every-turn ad hoc search, close-match gating, request construction, formatting, and failure behavior.
- [ ] Add an AI-service integration test that verifies retrieved memory reaches the model request.
- [ ] Verify whether retrieved content is persisted by `MemoryServiceChatMemoryStore`; add a guard if needed.
- [x] Add `GET /v1/profile-context` for frontend profile context display.
- [x] Add `GET /v1/profile-context/inputs` returning active user-authored input strings.
- [x] Add `PUT /v1/profile-context/inputs` to replace the active user-authored input list.
- [x] Store profile inputs as one Memory Service memory under `["user", userId, "cognition.v1", "profile_input"]` key `latest`.
- [x] Persist and return profile input order with `value.inputs`.
- [x] Validate profile input counts, item lengths, string types, blank values, and duplicates.
- [x] Ensure profile input edits do not directly rewrite `profile_context/latest`.
- [x] Update `chat-quarkus` README with cognition RAG configuration and a short local dev walkthrough.

## Files to Modify

| File                                                                                               | Change                                                                          |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| `java/quarkus/examples/chat-quarkus/src/main/java/org/acme/Agent.java`                             | Register the retrieval augmentor and update system prompt guidance.             |
| `java/quarkus/examples/chat-quarkus/src/main/java/org/acme/CognitionMemoryRagConfig.java`          | New config mapping for feature flag, limits, namespace root, and failure mode.  |
| `java/quarkus/examples/chat-quarkus/src/main/java/org/acme/CognitionMemoryRetrievalAugmentor.java` | New LangChain4j `RetrievalAugmentor` supplier.                                  |
| `java/quarkus/examples/chat-quarkus/src/main/java/org/acme/CognitionMemoryContentRetriever.java`   | New Memory Service-backed `ContentRetriever`.                                   |
| `java/quarkus/examples/chat-quarkus/src/main/java/org/acme/CognitionMemoryProfileContext.java`     | New first-turn `profile_context/latest` loader.                                 |
| `java/quarkus/examples/chat-quarkus/src/main/java/org/acme/CognitionMemoryContentInjector.java`    | New prompt framing for retrieved memories.                                      |
| `java/quarkus/examples/chat-quarkus/src/main/java/org/acme/ProfileContextResource.java`            | New frontend-facing profile context and profile input endpoints.                |
| `java/quarkus/examples/chat-quarkus/src/main/resources/application.properties`                     | Add `chat.cognition-rag.*` defaults, `%alt` enablement, and logging categories. |
| `java/quarkus/examples/chat-quarkus/src/test/java/org/acme/`                                       | Add unit/integration tests for retrieval and AI-service wiring.                 |
| `java/quarkus/examples/chat-quarkus/README.md`                                                     | Document local cognition processor workflow and expected behavior.              |
| `deploy/episodic-policies/cognition/attributes.rego`                                               | New optional policy example for safe cognition attribute extraction.            |
| `site/src/pages/docs/`                                                                             | Optional follow-up docs if this becomes a published guide.                      |
| `java/quarkus/FACTS.md`                                                                            | Keep any discovered Quarkus LangChain4j retrieval gotchas current.              |

## Verification

```bash
# Compile the changed Quarkus example and required modules
./java/mvnw -f java/pom.xml -pl quarkus/examples/chat-quarkus -am compile

# Run focused chat-quarkus tests
./java/mvnw -f java/pom.xml -pl quarkus/examples/chat-quarkus test > chat-quarkus-test.log 2>&1
# Search for failures using Grep tool on chat-quarkus-test.log

# If policy files or Go episodic memory behavior changes, verify affected Go packages
go test ./internal/episodic ./internal/plugin/route/memories -count=1 > go-test.log 2>&1
# Search for failures using Grep tool on go-test.log

# Optional local smoke test with compose, chat-quarkus alt profile, and cognition processor running
task dev:memory-service
./java/mvnw -f java/pom.xml -pl :chat-quarkus quarkus:dev -Dquarkus.profile=alt
```

## Non-Goals

- Building or changing the cognition processor extraction pipeline.
- Adding a new cognition-specific Memory Service endpoint.
- Replacing per-conversation `ChatMemoryStore` replay.
- Injecting raw prior conversations into the prompt.
- Adding a frontend memory browser.
- Guaranteeing backward compatibility for app-local configuration while the example remains pre-release.

## Design Decisions

### Use Retrieval Augmentor Instead of Tools

The model should not have to decide to call a "memory search" tool before answering. This is deterministic pre-inference context assembly, so `RetrievalAugmentor` is the right LangChain4j hook.

### Use Memory Service Search Instead of a Local Vector Store

Cognition memories already live in Memory Service and are governed by Memory Service policy, encryption, archive, TTL, and vector-search behavior. Adding a second vector store inside `chat-quarkus` would duplicate storage and risk policy drift.

### Keep Retrieval User-Scoped

The retriever uses the authenticated principal and bearer token so normal `/v1/memories/search` policy enforces user isolation. Admin search is for inspection only and must not be used in the chat hot path.

### Keep The Retriever App-Local Initially

The retriever should stay in `chat-quarkus` for this enhancement. Moving it into `memory-service-extension/runtime` is a follow-up only after the example proves the API shape and more than one Quarkus app needs the same behavior.

### Enable In The Compose Profile

The base `chat-quarkus` profile should leave cognition RAG disabled. The `%alt` profile should enable it because that profile is the intended local path for `compose.yaml`, the external Memory Service, and the cognition processor. With fail-open behavior this feature can tolerate missing profile snapshots, but default-off avoids noisy warning logs and semantic-search calls in plain dev-service runs.

## Security Considerations

- The retriever must never accept a user ID from request input.
- Retrieved memories are same-user only through bearer-token-authenticated Memory Service calls.
- Injected text must omit raw evidence, citations beyond compact source IDs, provider prompts, API keys, and `clientId`.
- Fail-open logging must not include full memory values unless debug logging is explicitly enabled.
- If retrieved content is persisted into conversation context, add a guard before enabling this feature by default.
