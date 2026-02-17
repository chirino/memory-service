---
status: implemented
supersedes:
  - 037-attachments.md
---

# Enhancement 043: Improved Attachments (Full Multimodal Support)

> **Status**: Implemented. Supersedes the phased attachment approach in
> [037](037-attachments.md).

## Summary

The current Quarkus extension and demo app only support sending a single image to the LLM via `@ImageUrl`. LangChain4j supports rich multimodal messages with multiple content types — `ImageContent`, `AudioContent`, `VideoContent`, `PdfFileContent`, and `TextContent` — and Quarkus LangChain4j exposes these through AI Service method parameters. Similarly, the Spring demo app duplicates attachment resolution logic in the controller. This enhancement updates both the Quarkus extension and the Spring Boot autoconfigure module to handle any number and type of attachments generically, pushing resolution logic into the library modules and keeping the demo apps simple.

## Motivation

1. **Single-image limitation**: `Agent.chatWithImage()` accepts one `@ImageUrl String`. If the user uploads two images, only the first is sent to the LLM; the rest are silently dropped.

2. **Image-only**: Audio, video, and PDF attachments are uploaded and stored in conversation history as metadata, but they are never resolved and passed to the LLM. The user sees them in the chat UI but the agent is blind to them.

3. **Boilerplate in the demo app**: `AgentSseResource.resolveFirstImageUrl()` manually downloads one attachment, base64-encodes it, and constructs a data URI. Each new content type would require another resolve method and another agent method overload. This doesn't scale.

4. **LangChain4j already supports this**: AI Service methods can accept `Content`, `List<Content>`, `ImageContent`, `AudioContent`, `VideoContent`, `PdfFileContent`, or any mix — all annotated with `@UserMessage`. Quarkus LangChain4j passes them through to the model provider. We just need to build the `Content` objects from our uploaded attachments.

## Current Architecture

### Extension (`ConversationInterceptor`)

The interceptor scans method parameters for annotations:

| Annotation | What it does |
|---|---|
| `@ConversationId` | Extracts conversation ID (String) |
| `@UserMessage` | Extracts user text (String) |
| `@Attachments` | Extracts explicit attachment metadata (`List<Map<String, Object>>`) |
| `@ImageUrl` | Converts a String arg to a `{href, contentType: "image/*"}` attachment map |

The `@Attachments` and `@ImageUrl` handling only records metadata to conversation history. Neither affects what the LLM actually sees — that's entirely up to the Agent interface method signature and the parameters the demo app passes.

### Demo App (`Agent` / `HistoryRecordingAgent` / `AgentSseResource`)

```
AgentSseResource.stream()
  ├── resolveFirstImageUrl(attachments)   // downloads first attachment, base64-encodes
  ├── if image found:
  │     agent.chatWithImage(id, message, imageUrl, attachmentMeta)
  │       └── Agent.chatWithImage(@MemoryId id, message, @ImageUrl imageUrl)
  └── else:
        agent.chatDetailed(id, message)
          └── Agent.chatDetailed(@MemoryId id, message)
```

Problems:
- `resolveFirstImageUrl` returns after the first successful attachment — the rest are skipped.
- Only images are resolved. Audio/video/PDF attachments are ignored for LLM delivery.
- Two separate code paths (`chatWithImage` vs `chatDetailed`) based on whether an image exists.

## Design

### Principle: Push Attachment Resolution into the Extension

Instead of each demo app implementing its own download/convert logic, the extension provides `AttachmentResolver` — a CDI bean that downloads attachments from the memory-service and converts them to LangChain4j `Content` objects based on MIME type. The extension also provides an `Attachments` container that bundles both metadata (for history recording) and Content objects (for LLM delivery).

### Content Type Mapping

| Attachment contentType | LangChain4j Content type | Resolution |
|---|---|---|
| `image/*` | `ImageContent` | Base64 data URI or S3 signed URL |
| `audio/*` | `AudioContent` | Base64 encoded with mime type or URL |
| `video/*` | `VideoContent` | Base64 encoded with mime type or URL |
| `application/pdf` | `PdfFileContent` | Base64 encoded with mime type or URL |
| Other / unknown | Skip (log warning) | Not sent to LLM |

### Extension: New Classes

**`AttachmentRef`** — Simple input record for attachment references:

```java
public record AttachmentRef(String id, String contentType, String name) {}
```

**`Attachments`** — Container holding both metadata and resolved Content:

```java
public class Attachments {
    private final List<Map<String, Object>> metadata;
    private final List<Content> contents;

    public List<Map<String, Object>> metadata() { ... }
    public List<Content> contents() { ... }
    public boolean isEmpty() { ... }
    public static Attachments empty() { ... }
}
```

**`AttachmentResolver`** — CDI bean that downloads attachments and builds `Attachments`:

```java
@ApplicationScoped
public class AttachmentResolver {
    // Injects memory-service URL config + SecurityIdentity
    public Attachments resolve(List<AttachmentRef> refs) {
        // For each ref:
        //   1. Build metadata map (attachmentId, contentType, name)
        //   2. Download from memory-service GET /v1/attachments/{id}
        //   3. Handle 302 (S3 redirect) → URL-based Content
        //   4. Handle 200 → base64-encode → Content from base64
        //   5. Map MIME type to ImageContent/AudioContent/VideoContent/PdfFileContent
    }
}
```

### Extension: Updated ConversationInterceptor

The interceptor detects `Attachments` parameters **by type** (not by annotation). When it finds an `Attachments`-typed parameter, it extracts `.metadata()` for history recording. This replaces the old `@Attachments` annotation which is deleted.

The `@ImageUrl` fallback remains for backward compatibility with simpler apps.

### Demo App: Unified Agent Interface

Replace the current overloaded methods with a single method that always accepts content:

```java
@ApplicationScoped
@RegisterAiService()
public interface Agent {
    Multi<ChatEvent> chat(
            @MemoryId String memoryId,
            @UserMessage String userMessage,
            @UserMessage List<Content> attachments);
}
```

When `attachments` is empty, this behaves identically to a text-only call. LangChain4j concatenates all `@UserMessage` parameters into a single `UserMessage` in declaration order.

### Demo App: Updated HistoryRecordingAgent

```java
@ApplicationScoped
public class HistoryRecordingAgent {
    private final Agent agent;

    @RecordConversation
    public Multi<ChatEvent> chat(
            @ConversationId String conversationId,
            @UserMessage String userMessage,
            Attachments attachments) {
        return agent.chat(conversationId, userMessage, attachments.contents());
    }
}
```

The interceptor detects the `Attachments` parameter and extracts metadata for history. The method passes `attachments.contents()` to the Agent for LLM delivery.

### Demo App: Simplified AgentSseResource

```java
@Inject AttachmentResolver attachmentResolver;

@POST @Path("/{conversationId}/chat")
public Multi<String> stream(...) {
    Attachments attachments = attachmentResolver.resolve(toRefs(request.getAttachments()));
    Multi<ChatEvent> events = agent.chat(conversationId, request.getMessage(), attachments);
    return events.map(this::encode)...;
}
```

All download/convert/metadata logic is gone from the demo app. The `toRefs()` helper just maps the request DTO to extension `AttachmentRef` records.

## Dependencies

None. This is a self-contained improvement to the extension and demo app. No changes to the memory service backend or API contracts.

## Scope of Changes

| File | Change Type |
|---|---|
| `.../history/runtime/AttachmentRef.java` | **New** — input record for attachment references |
| `.../history/runtime/Attachments.java` | **New** — container for metadata + Content objects |
| `.../history/runtime/AttachmentResolver.java` | **New** — CDI bean: download + MIME-to-Content mapping |
| `.../history/runtime/ConversationInterceptor.java` | Modified — detect `Attachments` type parameter instead of `@Attachments` annotation |
| `.../history/annotations/Attachments.java` | **Deleted** — replaced by type detection |
| `chat-quarkus/.../Agent.java` | Replace 3 methods with 1 unified method accepting `List<Content>` |
| `chat-quarkus/.../HistoryRecordingAgent.java` | Replace 3 methods with 1 accepting `Attachments` |
| `chat-quarkus/.../AgentSseResource.java` | Remove all download/convert/metadata logic, use `AttachmentResolver` |
| `chat-quarkus/.../MockCustomerSupportAgent.java` | Updated to match new `Agent` interface |

## Implementation Plan

### Step 1: Create AttachmentRef Record

New file in extension runtime: `AttachmentRef(String id, String contentType, String name)`.

### Step 2: Create Attachments Container

New file in extension runtime: holds `metadata()` and `contents()`, with `empty()` factory.

### Step 3: Create AttachmentResolver CDI Bean

New file in extension runtime. Injects memory-service URL config and `SecurityIdentity`. Implements `resolve(List<AttachmentRef>)` with:
- Metadata building (attachmentId, contentType, name)
- HTTP download from memory-service (`GET /v1/attachments/{id}`)
- S3 302 redirect handling (URL-based Content)
- Base64 encoding for direct downloads
- MIME-to-Content type mapping (`toContentFromUrl`, `toContentFromBase64`)

### Step 4: Update ConversationInterceptor

- Remove `@Attachments` annotation import
- Add type detection: `if (paramTypes[i] == Attachments.class)` alongside annotation scanning
- Extract `.metadata()` from `Attachments` objects for history recording
- Keep `@ImageUrl` fallback for backward compatibility

### Step 5: Delete @Attachments Annotation

Remove `io.github.chirino.memory.history.annotations.Attachments` — replaced by type detection.

### Step 6: Update Agent Interface

Replace 3 methods (`chat`, `chatDetailed`, `chatWithImage`) with single unified method accepting `@UserMessage List<Content>`.

### Step 7: Update HistoryRecordingAgent

Single method accepting `Attachments` parameter. Passes `attachments.contents()` to Agent.

### Step 8: Simplify AgentSseResource

- Inject `AttachmentResolver`
- Remove all private download/convert/metadata methods (~150 lines)
- Remove unused imports (JAX-RS Client, Base64, Content types, etc.)
- Add `toRefs()` helper to map request DTOs to `AttachmentRef` records
- Remove `chat-simple` and `resume-simple` endpoints (unused)

### Step 9: Update Mock Test Agent

Update `MockCustomerSupportAgent` to implement the new single-method `Agent` interface.

### Step 10: Compile and Test

```bash
./mvnw compile -pl quarkus/examples/chat-quarkus -am
./mvnw test -pl quarkus/memory-service-extension/runtime,quarkus/examples/chat-quarkus
```

## Verification

```bash
# Compile all modules
./mvnw compile

# Run extension + demo app tests
./mvnw test -pl quarkus/memory-service-extension/runtime,quarkus/examples/chat-quarkus

# Start demo app in dev mode and test manually
./mvnw quarkus:dev -pl quarkus/examples/chat-quarkus
```

Manual verification:
1. Upload a single image — LLM describes it (regression test)
2. Upload two images — LLM describes both
3. Upload a PDF — LLM summarizes its contents
4. Upload image + PDF together — LLM references both
5. Send text-only message — works as before (no attachments)
6. Check conversation history — attachment metadata recorded for all uploads

## Assumptions

1. The model provider (OpenAI, Anthropic, etc.) supports the content types being sent. If a provider doesn't support audio, LangChain4j will return an appropriate error.
2. Attachment binary data is small enough to base64-encode and send inline. For very large files (video), S3 signed URLs may be preferable where supported.

## Spring Module Changes

The same pattern applies to the Spring Boot autoconfigure module and the Spring demo app. Spring AI uses `Media` objects (with `MimeType` + `URI`) rather than LangChain4j `Content` objects, and model providers fetch the data from the URI themselves, so the Spring `AttachmentResolver` is much simpler — no HTTP downloading or base64 encoding required.

### Spring Autoconfigure: New Classes

**`AttachmentRef`** — Input record with an extra `href` field for explicit URL references:

```java
public record AttachmentRef(String id, String contentType, String name, String href) {
    public AttachmentRef(String id, String contentType, String name) {
        this(id, contentType, name, null);
    }
}
```

**`Attachments`** — Container holding metadata and Spring AI `Media` objects:

```java
public class Attachments {
    public List<Map<String, Object>> metadata() { ... }
    public List<Media> media() { ... }  // Spring AI Media, not LangChain4j Content
    public boolean isEmpty() { ... }
    public static Attachments empty() { ... }
}
```

**`AttachmentResolver`** — Spring bean that builds metadata + Media from refs (no downloading needed):

```java
public class AttachmentResolver {
    public Attachments resolve(List<AttachmentRef> refs) {
        // For each ref: build metadata map + Media(mimeType, URI)
        // Falls back to /v1/attachments/{id} if no explicit href
    }
}
```

### Spring Autoconfigure: Updated ConversationHistoryStreamAdvisor

- New `ATTACHMENTS_KEY` constant replaces `ATTACHMENT_METADATA_KEY`
- `extractAttachments()` detects `Attachments` type in advisor context, extracting `.metadata()` for history recording
- Legacy support: still accepts raw `List<Map<String, Object>>` for backward compatibility

### Spring Autoconfigure: Bean Registration

`ConversationHistoryAutoConfiguration` registers a default `AttachmentResolver` bean (overridable via `@ConditionalOnMissingBean`).

### Spring Demo App: Simplified AgentStreamController

```java
private final AttachmentResolver attachmentResolver;

@PostMapping("/{conversationId}/chat")
public SseEmitter stream(...) {
    Attachments attachments = attachmentResolver.resolve(toRefs(request.getAttachments()));
    // Pass attachments to advisor context for history recording
    advisor.param(ConversationHistoryStreamAdvisor.ATTACHMENTS_KEY, attachments);
    // Pass media to user prompt for LLM delivery
    spec.media(attachments.media().toArray(new Media[0]));
}
```

Removed `buildAttachmentMetadata()` and `toMediaList()` private methods (~45 lines). Inner class renamed from `AttachmentRef` to `RequestAttachmentRef` to avoid collision with the library type.

### Spring Scope of Changes

| File | Change Type |
|---|---|
| `.../history/AttachmentRef.java` | **New** — input record for attachment references |
| `.../history/Attachments.java` | **New** — container for metadata + Media objects |
| `.../history/AttachmentResolver.java` | **New** — builds metadata + Media from refs |
| `.../history/ConversationHistoryStreamAdvisor.java` | Modified — `ATTACHMENTS_KEY`, detects `Attachments` type in context |
| `.../history/ConversationHistoryAutoConfiguration.java` | Modified — registers `AttachmentResolver` bean |
| `chat-spring/.../AgentStreamController.java` | Remove `buildAttachmentMetadata()` and `toMediaList()`, use `AttachmentResolver` |

## Future Considerations

1. **Streaming uploads**: For very large attachments, streaming resolution (rather than buffering the entire file in memory) would reduce memory pressure.
2. **Provider-aware content filtering**: Skip content types the configured model doesn't support, rather than letting the API call fail.
3. **Content in agent responses**: LangChain4j `AiMessage` can also contain multimodal content (e.g., generated images). The history recorder currently captures agent responses as text only. Recording rich agent content is a separate enhancement.
