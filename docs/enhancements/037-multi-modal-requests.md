# 037: Multi-Modal Input Requests in History Entries

## Status
Draft

## Current State

The memory-service stores history entries with `{role, text}` for USER messages and `{role, text, events?}` for AI messages. This design assumes user input is text-only, but modern LLMs support multi-modal input including images, audio, video, and documents.

## Reason for Change

AI frameworks now support passing multiple content types to LLMs:

### Quarkus LangChain4j

Uses `@ImageUrl` annotation for remote images and `Image` class for base64-encoded content:

```java
@UserMessage("Here is a menu image. Extract the list of items.")
String extractMenu(@ImageUrl String imageUrl);

// Or with Image class:
Image img = Image.builder()
    .base64Data(base64EncodedBytes)
    .mimeType("image/jpeg")
    .build();
String result = agent.analyzeImage(img);
```

### Spring AI

Uses `UserMessage.media` field with `Media` class supporting both URLs and embedded resources:

```java
var userMessage = UserMessage.builder()
    .text("Explain what do you see in this picture?")
    .media(new Media(MimeTypeUtils.IMAGE_PNG, imageResource))
    .build();

// Or with URL:
var userMessage = UserMessage.builder()
    .text("Analyze this image")
    .media(new Media(MimeTypeUtils.IMAGE_PNG, new URI("https://example.com/image.png")))
    .build();
```

### Use Cases

1. **Chat with images**: User uploads a photo and asks questions about it
2. **Document analysis**: User attaches a PDF/document for summarization
3. **Audio transcription**: User sends voice message for processing
4. **Video analysis**: User shares video clip for content extraction

**Applications want to:**
1. **Capture** the full multi-modal context in conversation history
2. **Replay** conversations with original attachments visible
3. **Fork** conversations and maintain attachment references

---

## Phase 1: External Reference Attachments

### Design Goals

Support capturing attachments that reference external resources (URLs). This is the simplest case - no blob storage required, just metadata.

### History Content Schema

Extend the history block schema to include an optional `attachments` array:

```typescript
interface HistoryBlock {
  role: "USER" | "AI";
  text?: string;           // Text content
  events?: Event[];        // Rich events (AI messages, from 036)
  attachments?: Attachment[];  // Media attachments (new)
}

interface Attachment {
  href: string;            // URL to the resource
  contentType: string;     // MIME type (e.g., "image/png", "audio/mp3")
  name?: string;           // Optional display name
  description?: string;    // Optional description/alt text
}
```

### Example: User Message with Image URL

```json
{
  "content_type": "history",
  "content": [
    {
      "role": "USER",
      "text": "What breed is this dog?",
      "attachments": [
        {
          "href": "https://example.com/photos/my-dog.jpg",
          "contentType": "image/jpeg",
          "name": "my-dog.jpg"
        }
      ]
    },
    {
      "role": "AI",
      "text": "This appears to be a Golden Retriever..."
    }
  ]
}
```

### Example: Multiple Attachments

```json
{
  "role": "USER",
  "text": "Compare these two product images",
  "attachments": [
    {
      "href": "https://cdn.example.com/product-a.png",
      "contentType": "image/png",
      "name": "Product A"
    },
    {
      "href": "https://cdn.example.com/product-b.png",
      "contentType": "image/png",
      "name": "Product B"
    }
  ]
}
```

### Quarkus Integration

The `ConversationInterceptor` should detect `@ImageUrl` annotated parameters and capture them:

```java
@RecordConversation
public String analyzeImage(
    @ConversationId String conversationId,
    @UserMessage String userMessage,
    @ImageUrl String imageUrl) {
    return agent.analyze(userMessage, imageUrl);
}
```

**ConversationStore enhancement:**

```java
public void appendUserMessage(
        String conversationId,
        String text,
        List<Attachment> attachments,  // New parameter
        @Nullable String bearerToken) {

    Map<String, Object> block = new HashMap<>();
    block.put("role", "USER");
    block.put("text", text);
    if (attachments != null && !attachments.isEmpty()) {
        block.put("attachments", attachments);
    }
    // ... rest of implementation
}
```

### Spring AI Integration

Intercept `UserMessage.media` content when recording:

```java
private List<Attachment> extractAttachments(UserMessage message) {
    if (message.getMedia() == null || message.getMedia().isEmpty()) {
        return Collections.emptyList();
    }

    return message.getMedia().stream()
        .filter(media -> media.getData() instanceof URI)  // Phase 1: URLs only
        .map(media -> new Attachment(
            ((URI) media.getData()).toString(),
            media.getMimeType().toString(),
            null  // name derived from URL if needed
        ))
        .collect(Collectors.toList());
}
```

### Frontend Rendering

The chat frontend should render attachments inline with the message:

```tsx
interface MessageProps {
  message: HistoryBlock;
}

function MessageBubble({ message }: MessageProps) {
  return (
    <div className={`message ${message.role.toLowerCase()}`}>
      {message.attachments?.map((att, i) => (
        <AttachmentPreview key={i} attachment={att} />
      ))}
      {message.text && <MarkdownRenderer content={message.text} />}
      {message.events && <RichEventRenderer events={message.events} />}
    </div>
  );
}

function AttachmentPreview({ attachment }: { attachment: Attachment }) {
  const isImage = attachment.contentType.startsWith('image/');
  const isAudio = attachment.contentType.startsWith('audio/');
  const isVideo = attachment.contentType.startsWith('video/');

  if (isImage) {
    return <img src={attachment.href} alt={attachment.name || 'Attachment'} />;
  }
  if (isAudio) {
    return <audio controls src={attachment.href} />;
  }
  if (isVideo) {
    return <video controls src={attachment.href} />;
  }
  return (
    <a href={attachment.href} target="_blank" rel="noopener">
      {attachment.name || 'Download attachment'}
    </a>
  );
}
```

---

## Phase 2: Embedded Attachment Storage (Future)

### Problem Statement

Phase 1 only supports external URLs. However, many applications need to:

1. **Upload files with the request**: User posts a multi-part form with image data
2. **Upload files before referencing**: Pre-upload attachments, then reference them in a message
3. **Store files persistently**: External URLs may expire or become unavailable
4. **Control access**: Attachments should follow conversation access control

### Design Considerations

#### Request Format: Multi-Part MIME

Applications typically post multi-modal requests as `multipart/form-data`:

```http
POST /api/chat HTTP/1.1
Content-Type: multipart/form-data; boundary=----FormBoundary

------FormBoundary
Content-Disposition: form-data; name="conversationId"

conv-123
------FormBoundary
Content-Disposition: form-data; name="message"

What breed is this dog?
------FormBoundary
Content-Disposition: form-data; name="attachment"
Content-Type: image/jpeg
Content-Disposition: form-data; name="attachment"; filename="dog.jpg"

<binary image data>
------FormBoundary--
```

The history recorder needs to intercept these uploads before they reach the LLM.

#### FileStore Abstraction

Introduce a `FileStore` interface to abstract file storage. The FileStore is a simple key-value store for binary data - all metadata is tracked in the attachments table.

```java
public interface FileStore {
    /**
     * Store a file and return its storage key.
     */
    String store(InputStream data);

    /**
     * Retrieve a file by storage key.
     */
    InputStream retrieve(String storageKey);

    /**
     * Delete a file by storage key.
     */
    void delete(String storageKey);

    /**
     * Generate a signed URL for direct access (if supported).
     */
    Optional<URI> getSignedUrl(String storageKey, Duration expiry);
}
```

#### FileStore Implementations

| Implementation | Use Case | Pros | Cons |
|----------------|----------|------|------|
| **S3-compatible** | Production, scalable | Scalable, CDN integration, signed URLs | External dependency |
| **SQL BLOB** | Simple deployments | Single database, transactional | Size limits, DB load |
| **Filesystem** | Development, testing | Simple, no deps | Not distributed, no access control |
| **GridFS (MongoDB)** | MongoDB users | Integrated with document store | MongoDB-specific |

#### Attachments Table

The datastore maintains an `attachments` table that tracks files stored in the FileStore and their relationship to entries:

```sql
CREATE TABLE attachments (
    id UUID PRIMARY KEY,
    storage_key VARCHAR(255),               -- Key in FileStore (set after upload completes)
    filename VARCHAR(255),
    content_type VARCHAR(127) NOT NULL,
    size BIGINT,                            -- Set after upload completes
    sha256 VARCHAR(64),                     -- Set after upload completes
    entry_id UUID,                          -- Nullable FK to entries table (set when linked)
    expires_at TIMESTAMP,                   -- Null when linked to an entry
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),

    FOREIGN KEY (entry_id) REFERENCES entries(id)
);

CREATE INDEX idx_attachments_expires_at ON attachments(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_attachments_entry_id ON attachments(entry_id);
```

**Key design points:**
- `entry_id` is nullable - attachments can exist before being linked to an entry
- `expires_at` is set for unlinked attachments and cleared when linked
- The table row is created *before* storing the file in FileStore (fail-fast)
- `storage_key`, `size`, and `sha256` are set after the file upload to FileStore completes
- No `conversation_id` column - the conversation association is derived through the linked entry

#### Extended Attachment Schema

```typescript
interface Attachment {
  // Exactly one of href or attachmentId should be present
  href?: string;           // External URL (Phase 1)
  attachmentId?: string;   // Internal attachment reference (Phase 2)

  contentType: string;     // MIME type
  name?: string;           // Display name
  description?: string;    // Alt text
  size?: number;           // File size in bytes
  sha256?: string;         // SHA-256 hash of file content (hex-encoded)
}
```

#### API Design

All attachment operations are rooted under `/v1/attachments`. Attachments can be uploaded independently and linked to entries later.

**Upload endpoint:**
```http
POST /v1/attachments?expiresIn=PT1H
Content-Type: multipart/form-data

Returns: {
  "id": "att-abc123",
  "href": "/v1/attachments/att-abc123",
  "size": 102400,
  "sha256": "e3b0c44298fc1c149afbf4c8996fb924...",
  "expiresAt": "2024-01-15T10:30:00Z"
}
```

The `expiresIn` parameter uses ISO 8601 duration format (e.g., `PT1H` for 1 hour, `PT30M` for 30 minutes). If not provided, defaults to the configured default period. The server enforces a maximum expiration period via configuration.

**Retrieval endpoint:**
```http
GET /v1/attachments/{attachmentId}

Returns: Binary data with appropriate Content-Type
```

**Access control**:
- Unlinked attachments: Only the uploader can retrieve them (based on auth token)
- Linked attachments: Anyone with at least READ access to the associated conversation can retrieve them

**Referencing attachments in entries:**

When creating an entry, reference pre-uploaded attachments by ID:
```json
{
  "role": "USER",
  "text": "What is this?",
  "attachments": ["att-abc123"]
}
```

Or upload inline with entry creation using multipart:
```http
POST /v1/conversations/{conversationId}/entries
Content-Type: multipart/form-data

------FormBoundary
Content-Disposition: form-data; name="message"
Content-Type: application/json

{"role": "USER", "text": "What is this?"}
------FormBoundary
Content-Disposition: form-data; name="attachment"; filename="image.jpg"
Content-Type: image/jpeg

<binary data>
------FormBoundary--
```

**Configuration:**
```yaml
memory-service:
  attachments:
    max-size: 10MB                    # Maximum file size
    default-expires-in: PT1H          # Default expiration for pre-uploads (1 hour)
    max-expires-in: PT24H             # Maximum allowed expiration period (24 hours)
    upload-expires-in: PT1M           # Short expiration during upload (1 minute)
    upload-refresh-interval: PT30S    # How often to refresh expiresAt during upload
    cleanup-interval: PT5M            # How often to run cleanup job
```

#### Recording Flow

**Flow 1: Pre-upload then reference**
```
┌─────────┐
│  User   │
└────┬────┘
     │ 1. POST /v1/attachments?expiresIn=PT1H
     ▼
┌──────────────┐
│ Create       │  attachments row: {id, expiresAt=short, entry_id=NULL,
│ attachment   │                    storage_key=NULL, size=NULL, sha256=NULL}
│ record       │
└──────┬───────┘
       │ 2. Store file (refresh expiresAt periodically while streaming)
       ▼
┌──────────────┐
│  FileStore   │  Returns storageKey; size/sha256 computed during stream
└──────┬───────┘
       │ 3. Update row: storage_key, size, sha256, expiresAt=final
       ▼
┌──────────────┐
│ Return       │  { id, href, size, sha256, expiresAt }
│ response     │
└──────────────┘

     │ 4. POST /v1/conversations/{id}/entries (with attachmentId)
     ▼
┌──────────────┐
│ Create entry │  Links attachment: entry_id=X, expiresAt=NULL
└──────────────┘
```

**Flow 2: Upload with entry (atomic)**
```
┌─────────┐
│  User   │
└────┬────┘
     │ 1. POST /v1/conversations/{id}/entries (multipart with file)
     ▼
┌──────────────┐
│ Create       │  attachments row: {id, expiresAt=short, entry_id=NULL,
│ attachment   │                    storage_key=NULL, size=NULL, sha256=NULL}
│ record       │
└──────┬───────┘
       │ 2. Store file (refresh expiresAt periodically while streaming)
       ▼
┌──────────────┐
│  FileStore   │  Returns storageKey; size/sha256 computed during stream
└──────┬───────┘
       │ 3. Update row: storage_key, size, sha256
       ▼
┌──────────────┐
│ Create entry │  Create entry, link attachment: entry_id=X, expiresAt=NULL
└──────────────┘
```

**On failure (either flow):**
```
Upload fails → Delete file from FileStore (if any) → Delete attachment record
```

### Upload Reliability

#### Expiration Refresh During Upload

Large files over slow connections may take longer to upload than the initial expiration period. To prevent the attachment record from expiring mid-upload:

1. Set initial `expiresAt` to a short duration (e.g., 1 minute)
2. Periodically refresh `expiresAt` while streaming data (e.g., every 30 seconds)
3. On successful completion, set the final `expiresAt` based on the client's `expiresIn` parameter

```
Upload starts → expiresAt = now + 1 minute
...streaming...
30s elapsed → expiresAt = now + 1 minute (refresh)
...streaming...
30s elapsed → expiresAt = now + 1 minute (refresh)
...streaming...
Upload completes → expiresAt = now + expiresIn (final)
```

This ensures long uploads don't expire prematurely while still allowing quick cleanup of abandoned uploads.

#### Failure Cleanup

If an upload fails (client disconnection, FileStore error, or any other failure), both the attachment record and any partial file must be cleaned up immediately:

```java
try {
    // 1. Create attachment record with short expiration
    AttachmentRecord record = attachmentStore.create(expiresAt);

    try {
        // 2. Stream to FileStore (with periodic expiresAt refresh)
        String storageKey = fileStore.store(inputStream);

        // 3. Update record with final values
        attachmentStore.update(record.id(), storageKey, size, sha256, finalExpiresAt);

    } catch (Exception e) {
        // Upload failed - cleanup both record and any partial file
        fileStore.delete(record.storageKey());  // May be null/no-op if store() failed early
        attachmentStore.delete(record.id());
        throw e;
    }
} catch (Exception e) {
    // Record creation failed - nothing to cleanup
    throw e;
}
```

**Key points:**
- Immediate cleanup on failure (don't wait for cleanup job)
- Delete FileStore data first, then attachment record
- Handle partial FileStore writes (some implementations may have incomplete data)

### Zombie Attachment Prevention

The attachments table design prevents zombie files in the FileStore through a two-phase approach:

1. **Create attachment record first**: Before storing the file, create a row with `expires_at` set
2. **Immediate cleanup on failure**: If upload fails, delete both the record and any partial file
3. **Link on entry creation**: When an entry references the attachment, set `entry_id` and clear `expires_at`
4. **Periodic cleanup**: Delete expired attachments (and their files) that were never linked

#### Expiration Flow

**Pre-upload scenario:**
```
User uploads file → attachment created with short expiresAt during upload
Upload completes → expiresAt set to requested duration (e.g., now + 1 hour)
User creates entry → attachment linked, expiresAt cleared
```

**Upload with entry scenario:**
```
Request received → attachment created with short expiresAt
Upload completes → expiresAt remains short (will be cleared shortly)
Entry created → attachment linked, expiresAt cleared
```

The short expiration protects against partial failures - if the entry creation fails after the file is stored, the attachment will be cleaned up quickly.

#### Cleanup Job

```java
@Scheduled(every = "${memory-service.attachments.cleanup-interval:5m}")
void cleanupExpiredAttachments() {
    List<AttachmentRecord> expired = attachmentStore.findExpired();

    for (AttachmentRecord attachment : expired) {
        try {
            fileStore.delete(attachment.storageKey());
            attachmentStore.delete(attachment.id());
            LOG.info("Cleaned up expired attachment: {}", attachment.id());
        } catch (Exception e) {
            LOG.warn("Failed to cleanup attachment {}: {}", attachment.id(), e.getMessage());
        }
    }
}
```

**Why this approach:**

| Aspect | Benefit |
|--------|---------|
| **Fail-fast** | Record created before file storage; failures leave no orphans |
| **Explicit tracking** | Every file in FileStore has a corresponding attachment record |
| **Configurable expiration** | Clients control how long pre-uploads remain valid |
| **Simple cleanup** | Single query finds all expired attachments |
| **No reconciliation needed** | Unlike blob verification, no cross-referencing required |

**Trade-off:** Expired attachments persist until the cleanup job runs. With a 5-minute cleanup interval and reasonable expiration periods, this is acceptable.

### Design Decisions

1. **Attachment lifecycle**: Attachments are deleted before entries are hard-deleted. The entry deletion flow must delete associated attachments (and their files) first.

2. **Size limits**: Configurable via memory-service settings (e.g., `memory-service.attachments.max-size`). Uploads exceeding the limit return a clear error to the API caller.

3. **Deduplication**: Delegated to the `FileStore` implementation. Some stores (e.g., S3 with content-addressed keys) may deduplicate; others won't. **Error handling**: If deduplication fails, the error must propagate to the API caller.

4. **Virus scanning**: Delegated to the `FileStore` implementation. Stores can integrate with scanning services (e.g., S3 Object Lambda, ClamAV). **Error handling**: If scanning detects malware or fails, the upload must be rejected with a clear error to the API caller.

5. **Format conversion**: No. Store files as-is. Clients can handle display optimization.

6. **Signed URLs**: Generate on-demand when retrieving files. Note: S3 store is not in the initial implementation scope.

### Error Handling Requirements

For size limits, deduplication, and virus scanning, the `FileStore` interface must define clear error types that propagate to API responses:

```java
public sealed interface FileStoreError {
    record FileTooLarge(long maxBytes, long actualBytes) implements FileStoreError {}
    record MalwareDetected(String filename, String threatName) implements FileStoreError {}
    record DeduplicationFailed(String reason) implements FileStoreError {}
    record StorageError(String message, Throwable cause) implements FileStoreError {}
}
```

API responses must translate these to appropriate HTTP status codes:
| Error | HTTP Status | Response |
|-------|-------------|----------|
| `FileTooLarge` | 413 Payload Too Large | `{"error": "file_too_large", "maxBytes": 10485760, "actualBytes": 15000000}` |
| `MalwareDetected` | 422 Unprocessable Entity | `{"error": "malware_detected", "filename": "...", "threat": "..."}` |
| `DeduplicationFailed` | 500 Internal Server Error | `{"error": "storage_error", "message": "..."}` |
| `StorageError` | 500 Internal Server Error | `{"error": "storage_error", "message": "..."}` |

---

## Implementation Plan

### Phase 1 Tasks

#### 1.1 Schema Updates

**OpenAPI spec update:**
```yaml
components:
  schemas:
    Attachment:
      type: object
      required:
        - href
        - contentType
      properties:
        href:
          type: string
          format: uri
          description: URL to the attachment resource
        contentType:
          type: string
          description: MIME type of the attachment
        name:
          type: string
          description: Optional display name
        description:
          type: string
          description: Optional alt text or description

    HistoryBlock:
      type: object
      required:
        - role
      properties:
        role:
          type: string
          enum: [USER, AI]
        text:
          type: string
        events:
          type: array
          items:
            type: object
        attachments:
          type: array
          items:
            $ref: '#/components/schemas/Attachment'
```

#### 1.2 Quarkus Implementation

- [ ] Add `Attachment` record class
- [ ] Update `ConversationStore.appendUserMessage()` to accept attachments
- [ ] Add `@ImageUrl` parameter detection in `ConversationInterceptor`
- [ ] Update example chat app to demonstrate image URL support

#### 1.3 Spring Implementation

- [ ] Add `Attachment` record class
- [ ] Update `ConversationStore.appendUserMessage()` to accept attachments
- [ ] Add media extraction from `UserMessage` in advisor
- [ ] Update example chat app to demonstrate image URL support

#### 1.4 Frontend

- [ ] Add `AttachmentPreview` component
- [ ] Update `MessageBubble` to render attachments
- [ ] Support image, audio, video, and generic file previews

### Phase 2 Tasks (Future)

**Configuration:**
- [ ] Add `memory-service.attachments.max-size` config property (default: 10MB)
- [ ] Add `memory-service.attachments.default-expires-in` config property (default: PT1H)
- [ ] Add `memory-service.attachments.max-expires-in` config property (default: PT24H)
- [ ] Add `memory-service.attachments.upload-expires-in` config property (default: PT1M)
- [ ] Add `memory-service.attachments.upload-refresh-interval` config property (default: PT30S)
- [ ] Add `memory-service.attachments.cleanup-interval` config property (default: PT5M)
- [ ] Add `memory-service.attachments.store` config to select FileStore implementation

**Attachments Table:**
- [ ] Add `attachments` table with `entry_id` (nullable FK), `expires_at`, `sha256` columns
- [ ] Add index on `expires_at` for efficient cleanup queries
- [ ] Add `AttachmentRecord` entity class

**FileStore:**
- [ ] Define `FileStore` interface for file storage abstraction
- [ ] Define `FileStoreError` sealed interface for typed errors
- [ ] Implement SQL BLOB FileStore (PostgreSQL, MongoDB GridFS) - initial implementation
- [ ] (Future) Implement S3 FileStore with signed URL generation

**API Endpoints:**
- [ ] Add upload endpoint: `POST /v1/attachments?expiresIn=PT1H`
- [ ] Add retrieval endpoint: `GET /v1/attachments/{id}`
- [ ] Add entry creation with attachments: `POST /v1/conversations/{id}/entries` (multipart)
- [ ] Add access control: uploader-only for unlinked, conversation READ for linked

**Error Handling:**
- [ ] Map `FileStoreError` to HTTP responses in REST layer
- [ ] Add error response schemas to OpenAPI spec
- [ ] Add validation for size limits before FileStore invocation
- [ ] Validate `expiresIn` parameter against max-expires-in config

**Upload Reliability:**
- [ ] Refresh `expiresAt` periodically during upload (e.g., every 30 seconds)
- [ ] Set final `expiresAt` on successful upload completion
- [ ] On upload failure: delete partial file from FileStore, delete attachment record
- [ ] Handle client disconnection during upload

**Expired Attachment Cleanup:**
- [ ] Add `findExpired()` query method to attachment store
- [ ] Add scheduled cleanup job to delete expired attachments
- [ ] Delete file from FileStore before deleting attachment record

**Lifecycle Management:**
- [ ] Link attachment to entry: set `entry_id`, clear `expires_at`
- [ ] Delete attachments (and files) before entry hard-deletion
- [ ] Cascade attachment deletion when conversation entries are deleted
- [ ] Update recording flow to store uploaded files

---

## Test Plan

### Phase 1 Tests

#### Unit Tests

**AttachmentValidationTest:**
| Test Case | Description |
|-----------|-------------|
| `testValidAttachmentWithHref` | Accept attachment with valid URL |
| `testAttachmentRequiresHref` | Reject attachment without href |
| `testAttachmentRequiresContentType` | Reject attachment without contentType |
| `testMultipleAttachments` | Accept array of attachments |
| `testOptionalNameAndDescription` | Accept attachments with/without name and description |

#### Integration Tests (Cucumber)

```gherkin
Feature: Multi-modal history entries

  Scenario: Store user message with image attachment
    Given a conversation exists
    When I append a user message with text "What is this?" and attachment:
      | href        | https://example.com/image.jpg |
      | contentType | image/jpeg                     |
      | name        | test-image.jpg                 |
    Then the entry should have text "What is this?"
    And the entry should have 1 attachment
    And the attachment should have href "https://example.com/image.jpg"
    And the attachment should have contentType "image/jpeg"

  Scenario: Store user message with multiple attachments
    Given a conversation exists
    When I append a user message with 3 image attachments
    Then the entry should have 3 attachments

  Scenario: Store user message without attachments (backward compat)
    Given a conversation exists
    When I append a user message with text "Hello"
    Then the entry should have text "Hello"
    And the entry should not have attachments
```

### Phase 2 Tests (Future)

**FileStore Tests:**
- FileStore unit tests for each implementation (S3, SQL, GridFS)
- File upload/retrieval integration tests
- Access control tests for attachment endpoints

**Attachment Pre-Upload Tests:**

```gherkin
Feature: Attachment pre-upload and linking

  Scenario: Pre-upload attachment with custom expiration
    When I upload an attachment with expiresIn "PT2H"
    Then the attachment should have an expiresAt approximately 2 hours from now
    And the attachment should not be linked to an entry

  Scenario: Pre-upload attachment with default expiration
    Given the default expiration is configured as "PT1H"
    When I upload an attachment without specifying expiresIn
    Then the attachment should have an expiresAt approximately 1 hour from now

  Scenario: Reject expiration exceeding maximum
    Given the max expiration is configured as "PT24H"
    When I upload an attachment with expiresIn "PT48H"
    Then the response status should be 400
    And the error should indicate the expiration exceeds the maximum

  Scenario: Link pre-uploaded attachment to entry
    Given I have pre-uploaded an attachment with expiresAt set
    And a conversation exists
    When I create an entry referencing the attachment
    Then the attachment should be linked to the entry
    And the attachment expiresAt should be null

  Scenario: Only uploader can access unlinked attachment
    Given user A uploads an attachment
    When user B tries to retrieve the attachment
    Then the response status should be 403

  Scenario: Conversation readers can access linked attachment
    Given user A uploads an attachment
    And user A links the attachment to an entry in a conversation
    And user B has READ access to the conversation
    When user B retrieves the attachment
    Then the response status should be 200
```

**Expired Attachment Cleanup Tests:**

```gherkin
Feature: Expired attachment cleanup

  Scenario: Cleanup job deletes expired attachments
    Given I have an attachment with expiresAt in the past
    And the attachment is not linked to an entry
    When the cleanup job runs
    Then the attachment record should be deleted
    And the file should be deleted from the FileStore

  Scenario: Cleanup job preserves linked attachments
    Given I have an attachment linked to an entry
    And the attachment has no expiresAt
    When the cleanup job runs
    Then the attachment should still exist

  Scenario: Cleanup job preserves unexpired attachments
    Given I have an unlinked attachment with expiresAt in the future
    When the cleanup job runs
    Then the attachment should still exist
```

**Upload with Entry Tests:**

```gherkin
Feature: Upload attachment with entry creation

  Scenario: Upload file with entry creation
    Given a conversation exists
    When I create an entry with an attached file
    Then the entry should be created with the attachment
    And the attachment should be linked to the entry
    And the attachment expiresAt should be null

  Scenario: Failed entry creation cleans up attachment
    Given a conversation exists
    When I attempt to create an entry with an attached file
    And the entry creation fails
    Then the attachment should have a short expiresAt
    And the cleanup job should eventually delete it
```

**Upload Reliability Tests:**

```gherkin
Feature: Upload reliability

  Scenario: Long upload refreshes expiration
    Given I start uploading a large file
    And the upload takes longer than the initial expiration period
    Then the expiresAt should be refreshed during upload
    And the upload should complete successfully

  Scenario: Client disconnection cleans up immediately
    Given I start uploading a file
    When the client disconnects mid-upload
    Then the attachment record should be deleted
    And no partial file should remain in the FileStore

  Scenario: FileStore failure cleans up attachment record
    Given I start uploading a file
    And the attachment record is created
    When the FileStore fails during upload
    Then the attachment record should be deleted

  Scenario: Final expiration set on completion
    When I upload an attachment with expiresIn "PT2H"
    Then during upload the expiresAt should be short
    And after completion the expiresAt should be approximately 2 hours from now
```

**Error Handling Tests:**

```gherkin
Feature: Attachment upload error handling

  Scenario: Reject file exceeding size limit
    Given max attachment size is configured as 1MB
    When I upload a 2MB file
    Then the response status should be 413
    And the error should be "file_too_large"
    And the error should include maxBytes and actualBytes

  Scenario: Reject malware-infected file
    Given the attachment store has virus scanning enabled
    When I upload a file containing malware
    Then the response status should be 422
    And the error should be "malware_detected"
    And the error should include the threat name

  Scenario: Handle storage errors gracefully
    Given the attachment store is unavailable
    When I upload a file
    Then the response status should be 500
    And the error should be "storage_error"
```

**Lifecycle Tests:**
- Attachment (and file) deleted before entry is hard-deleted
- Attachments cascade-deleted when conversation entries are deleted
- Attachment retained when entry is forked (reference copied)

---

## References

- [Quarkus LangChain4j: Passing Images](https://docs.quarkiverse.io/quarkus-langchain4j/dev/guide-passing-image.html)
- [Spring AI: Multimodality](https://docs.spring.io/spring-ai/reference/api/multimodality.html)
- [036: Rich Event Types in History Entries](./036-rich-types-responses.md)
- [015: Background Task Queue](./015-task-queue.md)
