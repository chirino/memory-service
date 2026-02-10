# Enhancement 042: Indexing and Search Documentation

**Status**: ✅ Implemented

## Overview

Add documentation pages for both Quarkus and Spring that guide users through adding search indexing and search capabilities to their AI apps. This builds on the conversation history guide (checkpoint 03) by showing how to:

1. Provide an `IndexedContentProvider` to control what text gets indexed for search
2. Proxy the search endpoint so the frontend can query conversations
3. Test search with curl commands

A new checkpoint (`07-with-search`) will be created for each framework, based on `03-with-history`.

## Motivation

The conversation history guide already mentions that `listConversations` supports a `query` parameter, but doesn't explain how indexing works or how to enable semantic/fulltext search. Users need to understand:

- **Why indexing matters**: History entries are stored encrypted. The `indexedContent` field provides a searchable (unencrypted) version of the message text.
- **Redaction opportunity**: Since `indexedContent` is stored in cleartext for search, `IndexedContentProvider` gives apps a chance to redact sensitive information before it's indexed.
- **Search endpoint proxying**: The frontend needs access to `POST /v1/conversations/search` to perform searches.

## Checkpoint Strategy

Create a new checkpoint `07-with-search` based on `03-with-history`. The documentation pages will reference `03-with-history` (starting point) and `07-with-search` (completed code).

```
01-basic-agent → 02-with-memory → 03-with-history → 04-conversation-forking → 05-response-resumption → 06-sharing
                                        ↓
                                  07-with-search (new)
```

## New Files

### Checkpoint Files

#### Quarkus: `quarkus/examples/doc-checkpoints/07-with-search/`

```
07-with-search/
├── README.md
├── pom.xml                          # Same as 03-with-history
└── src/main/
    ├── java/org/acme/
    │   ├── Agent.java                   # Same as 03-with-history
    │   ├── HistoryRecordingAgent.java   # Same as 03-with-history
    │   ├── ChatResource.java            # Same as 03-with-history
    │   ├── ConversationsResource.java   # Updated: add search endpoint
    │   └── PassThroughIndexedContentProvider.java  # NEW
    └── resources/
        └── application.properties       # Same as 03-with-history
```

**New file: `PassThroughIndexedContentProvider.java`**
```java
package org.acme;

import io.github.chirino.memory.history.runtime.IndexedContentProvider;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class PassThroughIndexedContentProvider implements IndexedContentProvider {

    @Override
    public String getIndexedContent(String text, String role) {
        return text;
    }
}
```

**Updated file: `ConversationsResource.java`** — Add search endpoint:
```java
@POST
@Path("/search")
@Consumes(MediaType.APPLICATION_JSON)
@Produces(MediaType.APPLICATION_JSON)
public Response searchConversations(String body) {
    return proxy.searchConversations(body);
}
```

#### Spring: `spring/examples/doc-checkpoints/07-with-search/`

```
07-with-search/
├── README.md
├── pom.xml                          # Same as 03-with-history
└── src/main/
    ├── java/com/example/demo/
    │   ├── ChatController.java          # Same as 03-with-history
    │   ├── DemoApplication.java         # Same as 03-with-history
    │   ├── MemoryServiceProxyController.java  # Updated: add search endpoint
    │   ├── SecurityConfig.java          # Same as 03-with-history
    │   └── PassThroughIndexedContentProvider.java  # NEW
    └── resources/
        └── application.properties       # Same as 03-with-history
```

**New file: `PassThroughIndexedContentProvider.java`**
```java
package com.example.demo;

import io.github.chirino.memoryservice.history.IndexedContentProvider;
import org.springframework.stereotype.Component;

@Component
public class PassThroughIndexedContentProvider implements IndexedContentProvider {

    @Override
    public String getIndexedContent(String text, String role) {
        return text;
    }
}
```

**Updated file: `MemoryServiceProxyController.java`** — Add search endpoint:
```java
@PostMapping("/search")
public ResponseEntity<?> searchConversations(@RequestBody String body) {
    return proxy.searchConversations(body);
}
```

### Documentation Pages

#### `site/src/pages/docs/quarkus/indexing-and-search.mdx`

Structure:
```
---
layout: ../../../layouts/DocsLayout.astro
title: Indexing and Search
description: Add search indexing and semantic search to your conversations.
---
import components...

[Intro paragraph linking to conversation-history guide]

## Prerequisites
- Starting checkpoint: 03-with-history
- Completed conversation history guide

## How Search Indexing Works
- History entries are stored encrypted — they can't be searched directly
- The `indexedContent` field provides a searchable version of the message text
- `IndexedContentProvider` transforms message text before indexing
- This is your opportunity to redact sensitive content before it's stored in cleartext

## Add an IndexedContentProvider
- Create `PassThroughIndexedContentProvider.java`
- Explain the interface: `getIndexedContent(String text, String role)`
- Show how the framework auto-discovers the bean via CDI
- Explain that returning `null` skips indexing for that message
- <CodeFromFile> referencing checkpoint 07-with-search

## Custom Redaction Example
- Show a code example (not in checkpoint) of a redacting provider:
  ```java
  @ApplicationScoped
  public class RedactingIndexedContentProvider implements IndexedContentProvider {
      @Override
      public String getIndexedContent(String text, String role) {
          // Redact credit card numbers, SSNs, etc.
          return text.replaceAll("\\b\\d{4}[- ]?\\d{4}[- ]?\\d{4}[- ]?\\d{4}\\b", "[REDACTED]");
      }
  }
  ```

## Expose the Search API
- Add search endpoint to ConversationsResource
- <CodeFromFile> referencing the search method in checkpoint 07-with-search
- Explain the SearchConversationsRequest fields: query, searchType, limit, groupByConversation

## Test Search
- <TestScenario> with checkpoint 07-with-search
- First send a chat message (reuse from conversation-history)
- Then search for it:
  <CurlTest> POST /v1/conversations/search with {"query": "random number"}
- Show example response with highlights

## Completed Checkpoint
- Link to 07-with-search

## Next Steps
- Links to conversation-forking and response-resumption
```

#### `site/src/pages/docs/spring/indexing-and-search.mdx`

Same structure as Quarkus version but with Spring-specific code:
- `@Component` instead of `@ApplicationScoped`
- `@PostMapping` instead of `@POST @Path`
- `ResponseEntity<?>` instead of `Response`
- Spring constructor injection pattern

## Implementation Plan

### Phase 1: Create Checkpoint Examples

1. **Copy checkpoint 03 as base for 07**
   ```bash
   cp -r quarkus/examples/doc-checkpoints/03-with-history quarkus/examples/doc-checkpoints/07-with-search
   cp -r spring/examples/doc-checkpoints/03-with-history spring/examples/doc-checkpoints/07-with-search
   ```

2. **Add `PassThroughIndexedContentProvider.java` to each checkpoint**
   - Quarkus: `src/main/java/org/acme/PassThroughIndexedContentProvider.java`
   - Spring: `src/main/java/com/example/demo/PassThroughIndexedContentProvider.java`

3. **Update proxy resources to add search endpoint**
   - Quarkus: Add `searchConversations` method + `@Consumes`/`@Produces` imports to `ConversationsResource.java`
   - Spring: Add `searchConversations` method + `@PostMapping`/`@RequestBody` imports to `MemoryServiceProxyController.java`

4. **Update README.md for each checkpoint**
   - Describe what the checkpoint adds (indexed content provider + search endpoint)
   - Add curl test commands for search

### Phase 2: Create Documentation Pages

5. **Create `site/src/pages/docs/quarkus/indexing-and-search.mdx`**
   - Follow the structure outlined above
   - Use `<CodeFromFile>` to reference checkpoint 07-with-search files
   - Use `<TestScenario>` and `<CurlTest>` for testable curl examples
   - Include example search response JSON

6. **Create `site/src/pages/docs/spring/indexing-and-search.mdx`**
   - Parallel Spring version with framework-specific code and annotations

### Phase 3: Update Navigation and Cross-References

7. **Update `site/src/components/DocsSidebar.astro`**
   - Add `{ label: 'Indexing and Search', href: '/docs/quarkus/indexing-and-search/' }` after Conversation History
   - Add `{ label: 'Indexing and Search', href: '/docs/spring/indexing-and-search/' }` after Conversation History

8. **Update conversation-history Next Steps sections**
   - Add link to "Indexing and Search" in both `conversation-history.mdx` files

### Phase 4: Verify

9. **Compile checkpoints**
   ```bash
   ./mvnw compile -pl quarkus/examples/doc-checkpoints/07-with-search
   ./mvnw compile -pl spring/examples/doc-checkpoints/07-with-search
   ```

10. **Build site to verify MDX pages render correctly**
    ```bash
    cd site && npm run build
    ```

## Search API Reference (for documentation content)

### Request: `POST /v1/conversations/search`

```json
{
  "query": "random number",
  "searchType": "auto",
  "limit": 20,
  "groupByConversation": true,
  "includeEntry": true
}
```

Fields:
- `query` (required): Natural language search query
- `searchType`: `auto` (default), `semantic`, or `fulltext`
- `limit`: Maximum results (default 20)
- `groupByConversation`: Group results by conversation (default true)
- `includeEntry`: Include full entry content in results (default true)
- `after`: Cursor for pagination

### Response

```json
{
  "data": [
    {
      "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
      "conversationTitle": "Give me a random number between 1 and 100",
      "entryId": "d15ab065-ab94-4856-8def-a040d6d2d9db",
      "score": 0.95,
      "highlights": ["Give me a ==random number== between 1 and 100"],
      "entry": {
        "id": "d15ab065-ab94-4856-8def-a040d6d2d9db",
        "conversationId": "3579aac5-c86e-4b67-bbea-6ec1a3644942",
        "userId": "bob",
        "channel": "history",
        "contentType": "history",
        "content": [{"role": "USER", "text": "Give me a random number between 1 and 100."}],
        "createdAt": "2025-01-10T14:32:05Z"
      }
    }
  ],
  "nextCursor": null
}
```

## Files Modified

| File | Change Type | Description |
|------|-------------|-------------|
| `quarkus/examples/doc-checkpoints/07-with-search/` | New | Quarkus checkpoint with search |
| `spring/examples/doc-checkpoints/07-with-search/` | New | Spring checkpoint with search |
| `site/src/pages/docs/quarkus/indexing-and-search.mdx` | New | Quarkus search guide |
| `site/src/pages/docs/spring/indexing-and-search.mdx` | New | Spring search guide |
| `site/src/components/DocsSidebar.astro` | Modified | Add nav entries |
| `site/src/pages/docs/quarkus/conversation-history.mdx` | Modified | Add Next Steps link |
| `site/src/pages/docs/spring/conversation-history.mdx` | Modified | Add Next Steps link |
| `site-tests/openai-mock/fixtures/quarkus/07-with-search/001.json` | New | WireMock fixture (copied from 03) |
| `site-tests/openai-mock/fixtures/spring/07-with-search/001.json` | New | WireMock fixture (copied from 03) |

## Implementation Notes

### CodeFromFile match strings in MDX
- `<CodeFromFile>` children text is used as a literal match against the source file
- MDX processes `<` and `>` as JSX, so `ResponseEntity<?>` cannot be used as a match string
- Use a unique substring that doesn't contain angle brackets (e.g., `searchConversations(@RequestBody`)

### Search curl test not wrapped in CurlTest
- The search `curl` command is shown as documentation only (not wrapped in `<CurlTest>`)
- The test environment's docker compose doesn't configure a vector store, so `POST /v1/conversations/search` returns 501
- The chat `curl` command IS tested via `<CurlTest>` to verify the checkpoint builds and runs correctly
- The search curl must be placed **outside** the `<TestScenario>` block, because `TestScenario` extracts ALL bash code blocks within it as test scenarios

### Site-tests
- All 07-with-search tests pass (both Quarkus and Spring)
- WireMock fixtures reuse the same mock as checkpoint 03 (single chat completion)
- The pre-existing Test 03-with-history flaky failure (shared state, Run 1 fails / Run 2 passes) is unrelated
