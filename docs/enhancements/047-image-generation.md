---
status: implemented
---

# Enhancement 047: Image Generation Support

> **Status**: Implemented.

## Status
Implemented

## Goal

Add image generation capabilities to the chat-quarkus and chat-spring demo apps and the chat-frontend. When a user asks the chat agent to create an image, the LLM invokes an image generation tool, the generated image is stored as an attachment, and the frontend renders it inline in the conversation.

## Current State

- The chat apps support **multi-modal input** (users can attach images, documents, etc.) via the attachments system (see 037).
- The chat apps support **rich event streaming** with tool execution events (see 036).
- Neither chat app can **generate** images. There is no image generation model configured, no tool registered, and no frontend rendering for generated images.

## Design Overview

### Approach: Tool-Based Image Generation

Image generation is exposed as a **tool** available to the chat LLM. This is the most natural fit because:

1. The LLM decides *when* to generate an image based on conversation context.
2. Tool execution events (`BeforeToolExecution`, `ToolExecuted`) already flow through the rich event streaming pipeline and are displayed in the frontend.
3. The generated image is stored as an attachment for persistence, forking, and resumption.
4. No new event types are needed — the existing architecture handles it.

### Flow

```
User: "Generate an image of a cat wearing a top hat"
    │
    ▼
Chat LLM recognizes image request
    │
    ▼
LLM calls `generate_image` tool with prompt
    │  SSE: {"eventType":"BeforeToolExecution","toolName":"generate_image","input":{"prompt":"a cat wearing a top hat"}}
    ▼
Tool implementation:
  1. Calls image generation model (DALL-E 3 / GPT-Image-1)
  2. Gets ephemeral image URL from provider (e.g. OpenAI URL, expires ~1hr)
  3. Calls POST /v1/attachments with {"sourceUrl":"https://openai-url...", "contentType":"image/png"}
     → Attachments API returns attachmentId immediately
     → Attachments API downloads the image in the background
  4. Returns attachmentId to LLM (fast — no waiting for download)
    │  SSE: {"eventType":"ToolExecuted","toolName":"generate_image","output":{"attachmentId":"...","contentType":"image/png"}}
    ▼
LLM generates follow-up text: "Here's your image of a cat wearing a top hat!"
    │  SSE: {"eventType":"PartialResponse","chunk":"Here's your image..."}
    ▼
Recording layer creates AI history entry:
  - text: "Here's your image of a cat wearing a top hat!"
  - attachments: [{"href":"/v1/attachments/...","contentType":"image/png","attachmentId":"..."}]
    ▼
Frontend requests signed download URL for attachment:
  - If background download still in progress → returns signed URL to the original sourceUrl
  - If background download complete → returns signed URL to the stored file
    ▼
Frontend renders assistant message with inline image attachment (existing AttachmentPreview)
```

### AI Attachments via History Entry (No New Event Type)

Instead of introducing a new `ImageGenerated` event type, generated images are stored as **attachments on the AI history entry's content block** — the same `attachments` array already used for user messages. This means:

1. **No frontend changes for rendering** — the existing `AttachmentPreview` component already renders image attachments with signed URLs on any message (user or assistant).
2. **History, forking, and resume work automatically** — attachments are part of the entry, so they persist and carry over.
3. **Generic pattern** — any tool that produces files (not just images) can use the same mechanism.

#### How the Recording Layer Collects AI Attachments

The recording layer (which wraps the event stream) delegates to a **pluggable `ToolAttachmentExtractor` strategy** to inspect `ToolExecuted` events and extract attachment references. At stream completion, collected attachments are added to the AI entry's `attachments` array.

```
Stream events:
  ToolExecuted { output: {"attachmentId":"abc","contentType":"image/png"} }  ← extractor returns attachment
  ToolExecuted { output: {"result": 42} }                                    ← extractor returns empty
  PartialResponse { chunk: "Here's your image!" }
  ChatCompleted

On completion → appendAgentMessage(text, events, attachments=[{attachmentId:"abc", ...}])
```

#### ToolAttachmentExtractor Strategy

```java
/**
 * Strategy for extracting attachment references from tool execution results.
 * Implement this interface to customize how tool outputs are mapped to
 * attachments on the AI history entry.
 */
public interface ToolAttachmentExtractor {

    /**
     * Inspect a tool execution result and return any attachments that should
     * be associated with the AI response entry.
     *
     * @param toolName  the name of the executed tool
     * @param result    the raw tool result string
     * @return list of attachment metadata maps (may be empty, never null)
     */
    List<Map<String, Object>> extract(String toolName, String result);
}
```

**Default implementation** — looks for `attachmentId` in JSON tool output:

```java
public class DefaultToolAttachmentExtractor implements ToolAttachmentExtractor {

    private final ObjectMapper objectMapper;

    @Override
    public List<Map<String, Object>> extract(String toolName, String result) {
        try {
            JsonNode output = objectMapper.readTree(result);
            if (output.has("attachmentId")) {
                Map<String, Object> attachment = new LinkedHashMap<>();
                attachment.put("attachmentId", output.get("attachmentId").asText());
                attachment.put("href", "/v1/attachments/" + output.get("attachmentId").asText());
                if (output.has("contentType")) {
                    attachment.put("contentType", output.get("contentType").asText());
                }
                if (output.has("name")) {
                    attachment.put("name", output.get("name").asText());
                }
                return List.of(attachment);
            }
        } catch (JsonProcessingException ignored) {
            // Tool output is not JSON — no attachment to extract
        }
        return List.of();
    }
}
```

Users can provide their own implementation as a CDI bean (Quarkus) or Spring bean to handle custom tool output formats. For example, a tool that returns multiple attachments, or one that uses a different field name.

---

## Quarkus Implementation

### Dependencies

Add the image model dependency to `quarkus/examples/chat-quarkus/pom.xml`:

```xml
<!-- Image generation support (uses same OpenAI provider) -->
<!-- No additional dependency needed - quarkus-langchain4j-openai already includes image model support -->
```

The existing `quarkus-langchain4j-openai` dependency already bundles image model support. Only configuration is needed.

### Configuration

Add to `application.properties`:

```properties
# Chat model (new — not currently configured, uses provider default)
quarkus.langchain4j.openai.chat-model.model-name=${OPENAI_MODEL:gpt-4o}

# Image generation model
quarkus.langchain4j.openai.image-model.model-name=${OPENAI_IMAGE_MODEL:dall-e-3}
quarkus.langchain4j.openai.timeout=120s
```

The existing properties already use `OPENAI_API_KEY` and `OPENAI_BASE_URL`. The new `OPENAI_MODEL` and `OPENAI_IMAGE_MODEL` env vars follow the same convention. The timeout increase is important because image generation takes significantly longer than text completions (10-30 seconds typical).

### Image Generation Tool

Create a new tool class that the chat Agent can invoke:

```java
// quarkus/examples/chat-quarkus/src/main/java/example/ImageGenerationTool.java

@ApplicationScoped
public class ImageGenerationTool {

    @Inject
    ImageModel imageModel;  // dev.langchain4j.model.image.ImageModel - auto-configured by quarkus-langchain4j-openai

    @Inject
    AttachmentClient attachmentClient;

    @Tool("Generate an image from a text description. Use this when the user asks you to create, draw, or generate an image or picture.")
    public String generateImage(
            @P("A detailed description of the image to generate") String prompt) {

        Response<Image> response = imageModel.generate(prompt);
        Image image = response.content();

        // Create attachment from the provider's URL — the attachments API downloads it in the background.
        // This returns immediately with an attachment ID; no need to download bytes ourselves.
        String contentType = image.mimeType() != null ? image.mimeType() : "image/png";
        AttachmentInfo attachment = attachmentClient.createFromUrl(
                image.url().toString(), contentType, "generated-image.png");

        // Return structured JSON with attachmentId.
        // The recording layer will detect the attachmentId and add it to the AI entry's attachments array.
        // The LLM sees this result and can reference the image in its response text.
        return """
                {"attachmentId":"%s","contentType":"%s","prompt":"%s"}
                """.formatted(attachment.id(), contentType, prompt.replace("\"", "\\\""));
    }
}
```

### Attachment Client

Thin wrapper around the memory-service attachments API. Supports both existing file uploads and the new URL-based creation:

```java
// quarkus/examples/chat-quarkus/src/main/java/example/AttachmentClient.java

@ApplicationScoped
public class AttachmentClient {

    @Inject
    @RestClient
    MemoryServiceAttachmentsClient attachmentsClient;

    @Inject
    SecurityIdentity securityIdentity;

    /** Create an attachment from a remote URL. The server downloads it asynchronously. */
    public AttachmentInfo createFromUrl(String sourceUrl, String contentType, String filename) {
        String bearerToken = bearerToken(securityIdentity);
        return attachmentsClient.createFromUrl(bearerToken, sourceUrl, contentType, filename);
    }
}
```

### Recording Layer: Collecting AI Attachments

Update `ConversationEventStreamAdapter` (or equivalent) to use the `ToolAttachmentExtractor` strategy:

```java
// In the stream processing pipeline:
private final ToolAttachmentExtractor extractor;  // injected
private final List<Map<String, Object>> collectedAttachments = new ArrayList<>();

private void onEvent(ChatEvent event) {
    // ... existing event recording logic ...

    // Delegate to extractor strategy for attachment collection
    if (event instanceof ToolExecutedEvent toolEvent) {
        collectedAttachments.addAll(
                extractor.extract(toolEvent.toolName(), toolEvent.result()));
    }
}

// On stream completion:
store.appendAgentMessage(conversationId, text, events, collectedAttachments);
```

Update `ConversationStore.appendAgentMessage()` to accept an attachments parameter:

```java
public void appendAgentMessage(String conversationId, String text,
        List<Map<String, Object>> events, List<Map<String, Object>> attachments) {
    Map<String, Object> block = new HashMap<>();
    block.put("role", "AI");
    block.put("text", text);
    if (events != null && !events.isEmpty()) {
        block.put("events", events);
    }
    if (attachments != null && !attachments.isEmpty()) {
        block.put("attachments", attachments);
    }
    // ... create entry request ...
}
```

### Register Tool with Agent

Update the Agent interface to reference the tool:

```java
@ApplicationScoped
@RegisterAiService(tools = ImageGenerationTool.class)
public interface Agent {
    Multi<ChatEvent> chat(
            @MemoryId String memoryId,
            @UserMessage String userMessage,
            @UserMessage List<Content> attachments);
}
```

### Quarkus LangChain4j Reference

From the [image generation guide](https://docs.quarkiverse.io/quarkus-langchain4j/dev/guide-generating-image.html):

- `dev.langchain4j.model.image.ImageModel` is auto-injected when an image-capable provider is configured
- `Response<Image>` contains the generated `Image` with either `url()` or `base64Data()`
- Model names: `dall-e-3`, `gpt-image-1`
- The `@RegisterAiService` pattern supports returning `Image` directly, but for tool-based integration we use `ImageModel` directly inside the tool

---

## Spring Implementation

### Dependencies

Add the Spring AI image model dependency to `spring/examples/chat-spring/pom.xml`:

```xml
<!-- Image generation is included in the spring-ai-openai-spring-boot-starter -->
<!-- No additional dependency needed -->
```

The existing `spring-ai-openai-spring-boot-starter` already includes `ImageModel` support.

### Configuration

Add to `application.properties`:

```properties
# Chat model (new — not currently configured, uses provider default)
spring.ai.openai.chat.options.model=${OPENAI_MODEL:gpt-4o}

# Image generation model
spring.ai.openai.image.options.model=${OPENAI_IMAGE_MODEL:dall-e-3}
spring.ai.openai.image.options.response-format=url
```

The existing properties already use `OPENAI_API_KEY` and `OPENAI_BASE_URL`. The new `OPENAI_MODEL` and `OPENAI_IMAGE_MODEL` env vars follow the same convention.

### Image Generation Tool

```java
// spring/examples/chat-spring/src/main/java/example/agent/ImageGenerationTool.java

@Component
public class ImageGenerationTool {

    private final ImageModel imageModel;
    private final AttachmentClient attachmentClient;

    public ImageGenerationTool(ImageModel imageModel, AttachmentClient attachmentClient) {
        this.imageModel = imageModel;
        this.attachmentClient = attachmentClient;
    }

    @Tool(description = "Generate an image from a text description. Use this when the user asks you to create, draw, or generate an image or picture.")
    public String generateImage(
            @ToolParam(description = "A detailed description of the image to generate") String prompt) {

        ImagePrompt imagePrompt = new ImagePrompt(prompt,
                ImageOptionsBuilder.builder()
                        .model("dall-e-3")
                        .build());

        ImageResponse response = imageModel.call(imagePrompt);
        Image image = response.getResult().getOutput();

        // Create attachment from the provider's URL — downloaded asynchronously by memory-service.
        String contentType = "image/png";
        AttachmentInfo attachment = attachmentClient.createFromUrl(
                image.getUrl(), contentType, "generated-image.png");

        return """
                {"attachmentId":"%s","contentType":"%s","prompt":"%s"}
                """.formatted(attachment.id(), contentType, prompt.replace("\"", "\\\""));
    }
}
```

### Register Tool with ChatClient

Update the `AgentStreamController` to register the tool:

```java
ChatClient.RequestSpec requestSpec = chatClient
        .prompt()
        .advisors(advisors)
        .tools(imageGenerationTool)   // <-- Register tool
        .user(userSpec -> {
            userSpec.text(message);
            // ... attachments
        });
```

### Recording Layer: Collecting AI Attachments (Spring)

Same pattern as Quarkus — uses the `ToolAttachmentExtractor` strategy. The `ConversationHistoryStreamAdvisor` passes tool results through the extractor. At stream completion, it passes collected attachments to `ConversationStore.appendAgentMessage()`.

```java
// In ConversationHistoryStreamAdvisor, during stream processing:
private final ToolAttachmentExtractor extractor;  // injected

private void collectAttachmentsFromToolCalls(ChatClientResponse response,
        List<Map<String, Object>> collectedAttachments) {
    ChatResponse chat = response.chatResponse();
    if (chat == null) return;

    for (Generation gen : chat.getResults()) {
        AssistantMessage msg = (AssistantMessage) gen.getOutput();
        if (msg.getToolCalls() == null) continue;

        for (ToolCall tool : msg.getToolCalls()) {
            collectedAttachments.addAll(
                    extractor.extract(tool.name(), tool.arguments()));
        }
    }
}
```

### Spring AI Image Model Reference

From the [Spring AI Image Model API](https://docs.spring.io/spring-ai/reference/api/imageclient.html):

| Class | Purpose |
|-------|---------|
| `ImageModel` | Main interface — `call(ImagePrompt)` returns `ImageResponse` |
| `ImagePrompt` | Request with `List<ImageMessage>` and `ImageOptions` |
| `ImageOptions` | Model, width, height, responseFormat (`url` or `b64_json`), N |
| `ImageResponse` | Contains `List<ImageGeneration>` results |
| `ImageGeneration` | Single result with `Image` output |
| `Image` | Generated image with `getUrl()` or `getB64Json()` |

Supported providers: OpenAI, Azure OpenAI, StabilityAI, ZhiPuAI, QianFan.

---

## Frontend Implementation

### No New Event Types or Components Needed

The frontend **already handles this** with no changes required:

1. The `entryContent()` function in `chat-panel.tsx` already extracts `attachments` from any entry content block (user or AI).
2. The `AttachmentPreview` component already renders image attachments with signed download URLs.
3. The `ConversationMessage` type already includes `attachments?: ChatAttachment[]`.

When the recording layer adds attachments to the AI history entry, the frontend renders them automatically — image thumbnails with preview/download buttons appear on the assistant message, exactly as they do on user messages today.

### Potential Enhancement: Inline Image Rendering

Currently, `AttachmentPreview` renders images as clickable chips (icon + filename + preview/download buttons). For AI-generated images, it may be more natural to render them **inline** as `<img>` tags within the message. This would be a small enhancement to `AttachmentPreview` or the message row component to detect image attachments on assistant messages and render them larger/inline.

This is optional polish — the existing chip rendering works correctly out of the box.

---

## Attachment Storage Strategy

Generated images are stored as attachments in the memory-service. This provides:

1. **Persistence** — Images survive server restarts, available in conversation history
2. **Forking** — Forked conversations reference the same attachment IDs
3. **Access control** — Same auth/permissions as other attachments
4. **Signed URLs** — Frontend uses the existing download URL flow for secure display

### Explicit `status` Field on Attachments

Currently, attachment state is **implicit** — derived from combinations of `storageKey`, `entryId`, `expiresAt`, and `deletedAt` (see [Attachments concept docs](/docs/concepts/attachments/)). The URL-based creation flow introduces async states (`downloading`, `failed`) that don't fit cleanly into the implicit model. Rather than adding more implicit field combinations, we add an explicit `status` field that tracks **content readiness**:

| `status` | Meaning | Current implicit equivalent |
|----------|---------|----------------------------|
| `uploading` | Multipart upload in progress | `storageKey == null` |
| `downloading` | Server fetching from `sourceUrl` | **new** |
| `ready` | File stored and available | `storageKey != null` |
| `failed` | Download/upload failed | **new** |

The `status` field is orthogonal to the existing lifecycle concerns:
- **Linked/unlinked** — still tracked by `entryId`
- **Expiration** — still tracked by `expiresAt`
- **Soft deletion** — still tracked by `deletedAt`

For existing multipart uploads, the transition is `uploading → ready` within a single request (no behavioral change). The `status` field is most useful for the new async URL-based path.

**Migration**: Existing attachments get `status = 'ready'` (they already have `storageKey` set). Attachments with `storageKey == null` that survived (orphans) get `status = 'uploading'`.

### URL-Based Attachment Creation (New API)

The existing `POST /v1/attachments` accepts multipart file uploads. For image generation, we add support for **URL-based creation** where the caller provides a `sourceUrl` and the server downloads the content asynchronously:

```
POST /v1/attachments
Content-Type: application/json

{
  "sourceUrl": "https://oaidalleapiprodscus.blob.core.windows.net/...",
  "contentType": "image/png",
  "name": "generated-image.png"
}

Response (immediate):
{
  "id": "att-abc123",
  "status": "downloading",
  "sourceUrl": "https://oaidalleapiprodscus.blob.core.windows.net/..."
}
```

**Signed URL behavior by status**:

| `status` | `GET /v1/attachments/{id}/download-url` returns |
|----------|--------------------------------------------------|
| `uploading` | Error (not yet available) |
| `downloading` | Signed URL pointing to `sourceUrl` (the original provider URL) |
| `ready` | Signed URL pointing to the stored file in the attachment store |
| `failed` | Error response (download failed, URL expired, etc.) |

**Why this matters**: Image generation models (DALL-E 3, GPT-Image-1) return ephemeral URLs that expire after ~1 hour. The tool needs to hand off the URL quickly and get back to the LLM. Without async download, the tool would block for seconds downloading and re-uploading the image — adding latency to the tool call that the user sees as a pause. With async download:

1. Tool gets OpenAI URL → calls attachments API → gets back attachment ID instantly
2. User sees the image immediately (via the OpenAI URL while download is in progress)
3. Memory-service downloads and stores the image in the background
4. After download completes, subsequent signed URL requests point to the permanent stored copy
5. The OpenAI URL expires, but the image is safely stored

The recording layer adds the attachment reference to the AI entry's content block, so it shows up alongside the response text.

### Async Download Implementation

The download must **not buffer in memory**. Following the existing patterns in `DatabaseFileStore` and `S3FileStore`, the download streams through a temp file:

```java
// Async download job (runs on virtual thread)
void downloadFromSource(String attachmentId, String sourceUrl) {
    Path tempFile = Files.createTempFile("attachment-download-", ".tmp");
    try {
        // 1. Stream HTTP response → temp file (no memory buffering)
        try (InputStream httpStream = httpClient.open(sourceUrl);
             OutputStream out = Files.newOutputStream(tempFile)) {
            httpStream.transferTo(out);
        }

        // 2. Stream temp file → FileStore (same as multipart upload path)
        try (InputStream fileStream = new DigestInputStream(
                Files.newInputStream(tempFile), MessageDigest.getInstance("SHA-256"))) {
            FileStoreResult result = fileStore.store(fileStream, maxSize, contentType);
            attachmentStore.updateAfterUpload(attachmentId, result.storageKey(),
                    result.size(), sha256Hex, expiresAt);
            attachmentStore.updateStatus(attachmentId, AttachmentStatus.READY);
        }
    } catch (Exception e) {
        attachmentStore.updateStatus(attachmentId, AttachmentStatus.FAILED);
        log.error("Failed to download attachment from source URL", e);
    } finally {
        Files.deleteIfExists(tempFile);
    }
}
```

This matches the existing `DatabaseFileStore` pattern where PostgreSQL uploads spool through `Files.createTempFile("attachment-upload-", ".tmp")` and the `CountingInputStream` enforces size limits during streaming.

### Cucumber Tests

Add scenarios to `attachments-rest.feature`:

```gherkin
Scenario: Create attachment from source URL
  When I create an attachment from URL "https://httpbin.org/image/png" with content type "image/png" and name "test.png"
  Then the response status should be 201
  And the response body field "id" should not be null
  And the response body field "status" should be "downloading"

Scenario: Attachment transitions from downloading to ready
  When I create an attachment from URL "https://httpbin.org/image/png" with content type "image/png" and name "test.png"
  Then the response status should be 201
  Then set "attachmentId" to the json response field "id"
  And I wait for attachment "${attachmentId}" to have status "ready"
  When I call GET "/v1/attachments/${attachmentId}" expecting binary
  Then the response status should be 200
  And the response header "Content-Type" should contain "image/png"

Scenario: Download URL returns source URL while downloading
  When I create an attachment from URL "https://httpbin.org/image/png" with content type "image/png" and name "test.png"
  Then the response status should be 201
  Then set "attachmentId" to the json response field "id"
  When I call GET "/v1/attachments/${attachmentId}/download-url"
  Then the response status should be 200
  And the response body field "url" should contain "httpbin.org"

Scenario: Download URL returns stored file after download completes
  When I create an attachment from URL "https://httpbin.org/image/png" with content type "image/png" and name "test.png"
  Then the response status should be 201
  Then set "attachmentId" to the json response field "id"
  And I wait for attachment "${attachmentId}" to have status "ready"
  When I call GET "/v1/attachments/${attachmentId}/download-url"
  Then the response status should be 200
  And the response body field "url" should not contain "httpbin.org"

Scenario: Attachment from invalid URL transitions to failed
  When I create an attachment from URL "https://httpbin.org/status/404" with content type "image/png" and name "bad.png"
  Then the response status should be 201
  Then set "attachmentId" to the json response field "id"
  And I wait for attachment "${attachmentId}" to have status "failed"

Scenario: Cannot retrieve unlinked URL attachment as different user
  When I create an attachment from URL "https://httpbin.org/image/png" with content type "image/png" and name "test.png"
  Then the response status should be 201
  Then set "attachmentId" to the json response field "id"
  Given I am authenticated as user "bob"
  When I call GET "/v1/attachments/${attachmentId}/download-url"
  Then the response status should be 403

Scenario: Link URL-created attachment to entry
  Given I am authenticated as agent with API key "test-agent-key"
  And the conversation exists
  When I create an attachment from URL "https://httpbin.org/image/png" with content type "image/png" and name "generated.png"
  Then the response status should be 201
  Then set "uploadedAttachmentId" to the json response field "id"
  And I wait for attachment "${uploadedAttachmentId}" to have status "ready"
  When I call POST "/v1/conversations/${conversationId}/entries" with body:
  """
  {
    "channel": "HISTORY",
    "contentType": "history",
    "content": [{
      "role": "AI",
      "text": "Here is your image",
      "attachments": [
        {
          "attachmentId": "${uploadedAttachmentId}"
        }
      ]
    }]
  }
  """
  Then the response status should be 201
  And the response body field "content[0].attachments[0].href" should contain "/v1/attachments/"
```

---

## Implementation Plan

### Phase 1: Attachment `status` Field + URL-Based Creation

1. **Add `status` field**: Add `status` enum (`uploading`, `downloading`, `ready`, `failed`) to `AttachmentEntity` / `MongoAttachment`
2. **Migration**: Backfill existing records — `storageKey != null` → `ready`, `storageKey == null` → `uploading`
3. **Update multipart upload**: Set `status = 'uploading'` on create, `status = 'ready'` after `updateAfterUpload()`
4. **New endpoint**: `POST /v1/attachments` with JSON body `{sourceUrl, contentType, name}` — creates record with `status = 'downloading'`
5. **Async download**: Background virtual thread downloads from `sourceUrl`, spools to temp file (no memory buffering), streams temp file to `FileStore.store()`, transitions to `ready` (or `failed`). Follow existing `DatabaseFileStore` temp file pattern.
6. **Signed URL behavior**: `GET /v1/attachments/{id}/download-url` returns based on `status`:
   - `uploading` → error (not yet available)
   - `downloading` → signed URL to `sourceUrl` (passthrough to provider)
   - `ready` → signed URL to stored file (permanent)
   - `failed` → error response
7. **Store `sourceUrl`**: New nullable column on attachment record (needed for download-url passthrough and retry)
8. **Cucumber tests**: Add scenarios to `attachments-rest.feature` covering URL-based creation, status transitions, download-url behavior by status, access control, and entry linking

### Phase 2: Recording Layer (Quarkus + Spring)

1. **ConversationStore**: Add `attachments` parameter to `appendAgentMessage()` / `appendAgentMessageWithEvents()`
2. **Event stream adapter**: Inspect `ToolExecuted` events for `attachmentId` in output, collect them
3. **On stream completion**: Pass collected attachments to `appendAgentMessage()`
4. **Test**: Unit test that `ToolExecuted` with `attachmentId` output produces an entry with attachments

### Phase 3: Quarkus Chat App

1. **Configuration**: Add image model config to `application.properties`
2. **ImageGenerationTool**: Implement tool class with `@Tool` annotation
3. **AttachmentClient**: Wrapper to call the URL-based attachments API
4. **Agent registration**: Add `tools = ImageGenerationTool.class` to `@RegisterAiService`
5. **Test**: Verify tool appears in LLM's tool list, generates images, creates attachments

### Phase 4: Spring Chat App

1. **Configuration**: Add image model config to `application.properties`
2. **ImageGenerationTool**: Implement tool with `@Tool` annotation
3. **AttachmentClient**: Same pattern, Spring HTTP client
4. **ChatClient registration**: Register tool with `.tools(imageGenerationTool)`
5. **Test**: Same verification as Quarkus

### Phase 5: Frontend Polish (Optional)

1. **Inline image rendering**: Render image attachments on assistant messages as inline `<img>` tags instead of chips
2. **Loading placeholder**: Show skeleton while image loads
3. **Verify history**: Generated images display correctly when loading conversation from history
4. **Verify resume**: Generated images display correctly on stream resume
5. **Verify fork**: Forked conversations retain generated images

### Phase 6: Documentation

1. **Update `site/src/pages/docs/concepts/attachments.mdx`**: Add section covering URL-based attachment creation (`sourceUrl`), the `status` field lifecycle, and signed URL behavior by status
2. **Update configuration docs**: Document `OPENAI_MODEL` and `OPENAI_IMAGE_MODEL` env vars
3. **Update OpenAPI spec**: Add `sourceUrl`, `status` fields to attachment schemas; document the JSON-body variant of `POST /v1/attachments`

---

## Test Plan

### Manual Testing

1. **Basic generation**: Send "Generate an image of a sunset over mountains" — verify image appears on assistant message
2. **Tool visibility**: Verify `BeforeToolExecution` event shows `generate_image` with the prompt
3. **Attachment persistence**: Reload conversation — verify generated image is still visible in the assistant message's attachments
4. **Fork with image**: Fork a conversation that contains a generated image — verify image carries over
5. **Resume**: Interrupt a generation mid-stream, resume — verify image appears on resume
6. **Error case**: Test with invalid API key — verify graceful error message
7. **Spring parity**: Repeat tests 1-6 with chat-spring

### Automated Tests

| Test | Scope | Description |
|------|-------|-------------|
| URL-based attachment creation | Integration (memory-service) | `POST /v1/attachments` with `sourceUrl` → returns ID, status=`downloading` |
| Signed URL during download | Integration (memory-service) | While downloading, `GET .../download-url` returns sourceUrl-based signed URL |
| Signed URL after download | Integration (memory-service) | After download completes, `GET .../download-url` returns stored file URL |
| `ConversationEventStreamAdapterTest` | Unit (Quarkus) | `ToolExecuted` with `attachmentId` → entry has attachments |
| `ConversationHistoryStreamAdvisorTest` | Unit (Spring) | Same as above |
| `ImageGenerationToolTest` | Unit (Quarkus) | Mock `ImageModel`, verify tool calls `createFromUrl` and returns structured JSON |
| `ImageGenerationToolTest` | Unit (Spring) | Same as above with Spring AI `ImageModel` mock |
| `AgentSseResourceIT` | Integration (Quarkus) | Send image request, verify AI history entry has attachment |
| `AgentStreamControllerIT` | Integration (Spring) | Same as above |

---

## Open Questions

1. **Image size/quality options**: Should the tool accept parameters for size (1024x1024, 1792x1024) and quality (standard, hd)? Or keep it simple with defaults?
2. **Rate limiting**: Image generation is expensive (~$0.04-0.08 per image). Should there be any rate limiting at the tool level?
3. **Model selection**: Should the image model be configurable per-request, or always use the configured default?
4. **Download failure handling**: If the background download fails (e.g., provider URL expires before download starts), should the attachment be marked `failed` and left as-is, or automatically retried?
5. **Inline rendering**: Should AI-generated image attachments render inline as `<img>` tags, or is the existing chip-style attachment display sufficient?

---

## References

- [Quarkus LangChain4j Image Generation Guide](https://docs.quarkiverse.io/quarkus-langchain4j/dev/guide-generating-image.html)
- [Spring AI Image Model API](https://docs.spring.io/spring-ai/reference/api/imageclient.html)
- [036: Rich Event Types](./036-rich-types-responses.md) — Event streaming architecture
- [037: Attachments](./037-attachments.md) — Multi-modal input and attachment storage
