---
status: superseded
superseded-by:
  - 043-improved-quarkus-attachments.md
---

# Multi-Modal Input Requests in History Entries

> **Status**: Superseded. Phased attachment approach replaced by simplified multimodal design in
> [043](043-improved-quarkus-attachments.md).

## Status
Implemented (Phase 1 + Phase 2 + Phase 3 + Phase 4)

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

## Phase 2: Embedded Attachment Storage

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
     * Store file data and return the storage key and size.
     * Implementations enforce the maxSize limit and may read the stream
     * in chunks to bound memory usage.
     */
    FileStoreResult store(InputStream data, long maxSize, String contentType)
            throws FileStoreException;

    InputStream retrieve(String storageKey) throws FileStoreException;

    void delete(String storageKey);

    Optional<URI> getSignedUrl(String storageKey, Duration expiry);
}

/** Result of storing a file in a FileStore. */
public record FileStoreResult(String storageKey, long size) {}
```

#### FileStore Implementations

| Implementation | Status | Use Case | Pros | Cons |
|----------------|--------|----------|------|------|
| **DatabaseFileStore** | Implemented | Simple deployments | Single database, no external deps | Size limits, DB load |
| **S3FileStore** | Implemented | Production, scalable | Scalable, CDN integration, pre-signed URLs | External dependency |
| **Filesystem** | Not implemented | Development, testing | Simple, no deps | Not distributed, no access control |

A `FileStoreSelector` bean selects the active implementation based on the `memory-service.attachments.store` config property (`db` or `s3`).

##### DatabaseFileStore Streaming Architecture

The `DatabaseFileStore` uses true streaming to avoid loading entire files into memory:

**PostgreSQL — LargeObject API + temp file buffering:**
- **Upload**: The incoming stream is wrapped in a `CountingInputStream` (enforces `maxSize`) and written to a temp file. A short JDBC transaction then streams the temp file into a PostgreSQL Large Object via the pgjdbc `LargeObjectManager` API. The storage key is the Large Object OID (as a string). The `file_store_blobs` BYTEA table is not used.
- **Download**: The Large Object is opened synchronously (so "not found" errors surface immediately), then a background virtual thread spools the data to a temp file via `TempFileSpool`. The returned `InputStream` reads from the temp file concurrently as it is being written — the JDBC connection is released as soon as the spool finishes, not held for the entire HTTP response.
- **Delete**: `LargeObjectManager.delete(oid)` in a short transaction.

**MongoDB — GridFS (direct streaming, no temp file):**
- **Upload**: The incoming stream is wrapped in a `CountingInputStream` and passed directly to `GridFSBucket.uploadFromStream()`. GridFS handles chunking internally. The storage key is the GridFS `ObjectId` hex string.
- **Download**: `GridFSBucket.openDownloadStream()` returns a streaming cursor. GridFS cursors are lightweight — they don't hold transactions or locks — so no temp file buffering is needed.
- **Delete**: `GridFSBucket.delete(objectId)`.

**Key utilities:**
- **`CountingInputStream`**: Wraps an `InputStream`, counts bytes read, and throws `FileStoreException(FILE_TOO_LARGE)` if the count exceeds `maxSize`. Replaces the old `readWithLimit()` buffering approach.
- **`TempFileSpool`**: Runs a producer in a background virtual thread, writing to a temp file via a `TrackingOutputStream` that auto-flushes every 32 KB and signals a shared `SpoolState`. The returned `SpoolInputStream` reads from the temp file concurrently using `RandomAccessFile`, blocking via `Lock`/`Condition` when caught up to the writer.

##### S3FileStore Chunked Upload

The `S3FileStore` uses S3 multipart upload for files larger than 5 MB, bounding memory to a single 5 MB chunk regardless of file size. Files ≤ 5 MB use a simple `PutObject`.

#### Attachments Table

The datastore maintains an `attachments` table that tracks files stored in the FileStore and their relationship to entries. PostgreSQL stores blob data in `pg_largeobject` (via the LargeObject API) and MongoDB uses GridFS collections — no separate blob table is needed.

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
  "contentType": "image/jpeg",
  "filename": "photo.jpg",
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

When creating an entry, reference pre-uploaded attachments by `attachmentId` in the attachment object:
```json
{
  "role": "USER",
  "text": "What is this?",
  "attachments": [
    {
      "attachmentId": "att-abc123",
      "contentType": "image/jpeg",
      "name": "photo.jpg"
    }
  ]
}
```

The server rewrites `attachmentId` to `href` (e.g., `/v1/attachments/att-abc123`) **before** persisting the entry, so stored content always contains `href`. The attachment is then linked to the entry (setting `entry_id`, clearing `expires_at`) after the entry is persisted.

**Configuration:**
```yaml
memory-service:
  attachments:
    max-size: 10485760                # Maximum file size in bytes (10MB)
    default-expires-in: PT1H          # Default expiration for pre-uploads (1 hour)
    max-expires-in: PT24H             # Maximum allowed expiration period (24 hours)
    upload-expires-in: PT1M           # Short expiration during upload (1 minute)
    upload-refresh-interval: PT30S    # How often to refresh expiresAt during upload
    cleanup-interval: PT5M            # How often to run cleanup job
    store: db                         # FileStore implementation: "db" or "s3"
    s3:
      bucket: memory-service-attachments  # S3 bucket name (when store=s3)
      # prefix: attachments/              # Optional S3 key prefix
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

2. **Size limits**: Configurable via memory-service settings (e.g., `memory-service.attachments.max-size`). Enforced via `CountingInputStream` during streaming — the upload is rejected as soon as the limit is exceeded, without buffering the entire file first. The REST layer also wraps the upload in a `DigestInputStream` to compute SHA-256 on the fly.

3. **Deduplication**: Not currently implemented. Could be added to individual FileStore implementations in the future (e.g., content-addressed keys in S3).

4. **Virus scanning**: Not currently implemented. Could be added to individual FileStore implementations in the future (e.g., S3 Object Lambda, ClamAV integration).

5. **Format conversion**: No. Store files as-is. Clients can handle display optimization.

6. **Signed URLs**: Generated on-demand when retrieving files. The S3FileStore supports pre-signed GET URLs (1-hour expiry); the DatabaseFileStore streams bytes directly.

### Error Handling

The `FileStore` interface uses `FileStoreException` with an extensible error model. Each exception carries a string error code, HTTP status, human-readable message, and optional details map:

```java
public class FileStoreException extends RuntimeException {
    // Well-known codes (implementations may define additional codes)
    public static final String FILE_TOO_LARGE = "file_too_large";
    public static final String STORAGE_ERROR = "storage_error";

    private final String code;       // Error code sent to API user
    private final int httpStatus;    // HTTP status code for the response
    private final Map<String, Object> details;  // Additional context
}
```

The REST layer forwards these fields directly to the API response without branching on specific codes. This means FileStore implementations can introduce new error codes (e.g., `malware_detected`, `quota_exceeded`) and they'll automatically flow through to API users with the correct HTTP status and details.

Well-known error codes:

| Code | HTTP Status | Details | Description |
|------|-------------|---------|-------------|
| `file_too_large` | 413 | `maxBytes`, `actualBytes` | File exceeds configured size limit |
| `storage_error` | 500 | — | Generic storage backend failure |

Size limits are enforced via `CountingInputStream` inside each FileStore implementation, which rejects the upload as soon as the limit is exceeded without buffering the full file.

### Phase 3: gRPC Attachment API

#### Problem Statement

The REST attachment endpoints use HTTP multipart uploads, which don't have a gRPC equivalent. gRPC messages have a default 4 MB size limit and, even when increased, loading an entire file into a single protobuf message creates memory pressure proportional to file size on both client and server. Large attachments (images, documents, audio) can easily exceed 10 MB.

The current REST implementation streams files through `DigestInputStream` (SHA-256) and `CountingInputStream` (size enforcement) without buffering the entire file in memory. The FileStore implementations also stream internally (LargeObject API for PostgreSQL, GridFS for MongoDB, multipart upload for S3). A gRPC streaming API would follow the same pattern, bridging the chunk stream to an `InputStream` for the existing FileStore interface.

#### Design: Client-Streaming Upload

Use **client streaming** for uploads. The client sends a stream of messages: the first message carries metadata, subsequent messages carry file data chunks. The server responds with a single unary response after the stream completes.

```protobuf
service AttachmentsService {
  // Upload a file as a stream of chunks. First message must contain metadata.
  // Subsequent messages contain file data chunks.
  // Server responds once the upload is complete or an error occurs.
  rpc UploadAttachment(stream UploadAttachmentRequest) returns (UploadAttachmentResponse);

  // Retrieve attachment metadata (not the file content).
  rpc GetAttachment(GetAttachmentRequest) returns (AttachmentInfo);

  // Download a file as a stream of chunks.
  rpc DownloadAttachment(DownloadAttachmentRequest) returns (stream DownloadAttachmentResponse);
}

message UploadAttachmentRequest {
  oneof payload {
    // First message: upload metadata (required)
    UploadMetadata metadata = 1;
    // Subsequent messages: file data chunks
    bytes chunk = 2;
  }
}

message UploadMetadata {
  string filename = 1;
  string content_type = 2;
  // ISO 8601 duration (e.g., "PT1H"). Empty = server default.
  string expires_in = 3;
}

message UploadAttachmentResponse {
  string id = 1;
  string href = 2;             // "/v1/attachments/{id}"
  string content_type = 3;
  string filename = 4;
  int64 size = 5;
  string sha256 = 6;
  string expires_at = 7;       // ISO 8601 timestamp
}

message GetAttachmentRequest {
  string id = 1;
}

message AttachmentInfo {
  string id = 1;
  string href = 2;
  string content_type = 3;
  string filename = 4;
  int64 size = 5;
  string sha256 = 6;
  string expires_at = 7;
  string created_at = 8;
}

message DownloadAttachmentRequest {
  string id = 1;
}

message DownloadAttachmentResponse {
  oneof payload {
    // First message: attachment metadata
    AttachmentInfo metadata = 1;
    // Subsequent messages: file data chunks
    bytes chunk = 2;
  }
}
```

#### Upload Flow

```
Client                                 Server
  │                                      │
  │─── UploadAttachmentRequest ─────────>│  metadata: {filename, content_type, expires_in}
  │    (metadata)                        │  → Create attachment record (short expiresAt)
  │                                      │  → Initialize SHA-256 digest
  │                                      │
  │─── UploadAttachmentRequest ─────────>│  chunk: <bytes 0..65535>
  │    (chunk 1)                         │  → Write to FileStore, update digest
  │                                      │
  │─── UploadAttachmentRequest ─────────>│  chunk: <bytes 65536..131071>
  │    (chunk 2)                         │  → Write to FileStore, update digest
  │                                      │
  │─── ... more chunks ... ─────────────>│
  │                                      │
  │─── (stream completes) ─────────────>│  → Finalize FileStore write
  │                                      │  → Compute final SHA-256
  │                                      │  → Update attachment record
  │                                      │
  │<── UploadAttachmentResponse ────────│  {id, href, size, sha256, expiresAt}
  │                                      │
```

#### Download Flow

```
Client                                 Server
  │                                      │
  │─── DownloadAttachmentRequest ──────>│  {id: "att-123"}
  │                                      │  → Verify access control
  │                                      │  → Open FileStore stream
  │                                      │
  │<── DownloadAttachmentResponse ─────│  metadata: {id, content_type, filename, size, ...}
  │    (metadata)                        │
  │                                      │
  │<── DownloadAttachmentResponse ─────│  chunk: <bytes 0..65535>
  │    (chunk 1)                         │
  │                                      │
  │<── DownloadAttachmentResponse ─────│  chunk: <bytes 65536..131071>
  │    (chunk 2)                         │
  │                                      │
  │<── ... more chunks ... ────────────│
  │                                      │
  │<── (stream completes) ────────────│
  │                                      │
```

#### Key Design Decisions

**1. Chunk size**: Recommended 64 KB per chunk. This balances gRPC framing overhead against memory usage. Clients may use smaller or larger chunks (up to the per-message limit), but 64 KB is a good default.

**2. `oneof` for first-message metadata**: The `oneof payload` pattern distinguishes the metadata-only first message from data chunks. This is cleaner than the approach used in `StreamResponseTokenRequest` (where `conversation_id` is sent in the first message alongside data) because uploads have a distinct metadata phase.

**3. Server-side buffering**: The gRPC service bridges the chunk stream to an `InputStream` and passes it to `FileStore.store()`. Each FileStore already handles streaming internally:

| FileStore | Strategy | Memory |
|-----------|----------|--------|
| **S3** | S3 multipart upload with 5 MB chunks; simple PutObject for files ≤ 5 MB | Bounded to one 5 MB chunk |
| **PostgreSQL** | `CountingInputStream` → temp file → LargeObject API in short transaction | Bounded by temp disk |
| **MongoDB** | `CountingInputStream` → GridFS `uploadFromStream()` (chunked internally) | GridFS chunk size (255 KB default) |

No FileStore implementation requires full in-memory buffering.

**4. SHA-256 computation**: The digest is updated incrementally as each chunk arrives, so it never requires the full file in memory.

**5. FileStore interface reuse**: The current `FileStore.store(InputStream, maxSize, contentType)` signature already supports streaming. The gRPC service implementation can bridge the chunk stream to an `InputStream` (e.g., using `PipedInputStream`/`PipedOutputStream` or by collecting chunks), so no FileStore changes are needed.

**6. Error handling**: gRPC errors are mapped via `GrpcStatusMapper`:

| FileStoreException code | gRPC Status |
|------------------------|-------------|
| `file_too_large` | `RESOURCE_EXHAUSTED` |
| `storage_error` | `INTERNAL` |
| Other codes | `INTERNAL` (with message) |

Access control errors use `PERMISSION_DENIED` and `NOT_FOUND` as with existing services.

**7. No signed URL redirect**: Unlike the REST `GET /v1/attachments/{id}` endpoint which can issue a 302 redirect to a signed S3 URL, gRPC doesn't support redirects. The gRPC download always streams bytes through the server. For performance-sensitive use cases, clients can use the REST endpoint's `href` for direct S3 access.

#### Implementation Notes

The gRPC service class follows the existing patterns:

```java
@GrpcService
@Blocking
public class AttachmentsGrpcService extends AbstractGrpcService {

    @Inject AttachmentStoreSelector attachmentStoreSelector;
    @Inject FileStoreSelector fileStoreSelector;
    @Inject AttachmentConfig config;

    public Uni<UploadAttachmentResponse> uploadAttachment(
            Multi<UploadAttachmentRequest> requestStream) {
        // Collect metadata from first message
        // Stream chunks through SHA-256 digest
        // Forward to FileStore
        // Return response on completion
    }

    public Multi<DownloadAttachmentResponse> downloadAttachment(
            DownloadAttachmentRequest request) {
        // Verify access control
        // Send metadata as first response
        // Stream file chunks from FileStore
    }
}
```

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

- [x] Add `Attachment` record class
- [x] Update `ConversationStore.appendUserMessage()` to accept attachments
- [x] Add `@ImageUrl` parameter detection in `ConversationInterceptor`
- [x] Update example chat app to demonstrate image URL support

#### 1.3 Spring Implementation

- [x] Add `Attachment` record class
- [x] Update `ConversationStore.appendUserMessage()` to accept attachments
- [x] Add media extraction from `UserMessage` in advisor
- [x] Update example chat app to demonstrate image URL support

#### 1.4 Frontend

- [x] Add `AttachmentPreview` component
- [x] Update `MessageBubble` to render attachments
- [x] Support image, audio, video, and generic file previews

### Phase 2 Tasks

**Configuration:**
- [x] Add `memory-service.attachments.max-size` config property (default: 10MB)
- [x] Add `memory-service.attachments.default-expires-in` config property (default: PT1H)
- [x] Add `memory-service.attachments.max-expires-in` config property (default: PT24H)
- [x] Add `memory-service.attachments.upload-expires-in` config property (default: PT1M)
- [x] Add `memory-service.attachments.upload-refresh-interval` config property (default: PT30S)
- [x] Add `memory-service.attachments.cleanup-interval` config property (default: PT5M)
- [x] Add `memory-service.attachments.store` config to select FileStore implementation (`db` or `s3`)
- [x] Add S3 configuration (bucket, prefix)

**Attachments Table:**
- [x] Add `attachments` table with `entry_id` (nullable FK), `expires_at`, `sha256` columns
- [x] Attachments schema squashed into migration 1 (`schema.sql`); no separate blob table needed
- [x] Add index on `expires_at` for efficient cleanup queries
- [x] Add `AttachmentEntity` JPA entity and `MongoAttachment` document
- [x] Add `AttachmentRepository`

**FileStore:**
- [x] Define `FileStore` interface (`store()` accepts `contentType` and returns `FileStoreResult` with storage key + size)
- [x] Define `FileStoreException` with extensible string-based error codes
- [x] Implement `CountingInputStream` for streaming size enforcement
- [x] Implement `TempFileSpool` for concurrent temp file I/O (background virtual thread writer + concurrent reader)
- [x] Implement `DatabaseFileStore` — PostgreSQL LargeObject API + temp file buffering; MongoDB GridFS
- [x] Implement `S3FileStore` with chunked multipart upload (5 MB parts), content type metadata, and pre-signed URL generation
- [x] Implement `FileStoreSelector` to choose implementation based on config
- [x] REST upload uses `DigestInputStream` for on-the-fly SHA-256 (no buffering in REST layer)

**AttachmentStore:**
- [x] Define `AttachmentStore` interface for attachment metadata CRUD
- [x] Implement `PostgresAttachmentStore` and `MongoAttachmentStore`
- [x] Implement `AttachmentStoreSelector` to choose based on datastore type

**API Endpoints:**
- [x] Add upload endpoint: `POST /v1/attachments?expiresIn=PT1H`
- [x] Add retrieval endpoint: `GET /v1/attachments/{id}`
- [x] Add `attachmentId` support in entry creation (rewritten to `href` before persistence)
- [x] Add access control: uploader-only for unlinked, conversation-level for linked

**Error Handling:**
- [x] Map `FileStoreException` to HTTP responses in REST layer
- [x] Add error response schemas to OpenAPI spec
- [x] Add validation for size limits in both REST layer and FileStore
- [x] Validate `expiresIn` parameter against max-expires-in config

**Expired Attachment Cleanup:**
- [x] Add `findExpired()` query method to attachment store
- [x] Add `AttachmentCleanupJob` scheduled cleanup job
- [x] Delete file from FileStore before deleting attachment record

**Lifecycle Management:**
- [x] Link attachment to entry: set `entry_id`, clear `expires_at`
- [x] Cascade: delete FileStore blobs before conversation group hard-deletion (PostgreSQL + MongoDB)
- [x] PostgreSQL: `ON DELETE CASCADE` handles attachment record cleanup when entries are deleted

### Phase 3 Tasks

**Proto Definitions:**
- [x] Add `AttachmentsService` with `UploadAttachment`, `GetAttachment`, `DownloadAttachment` RPCs
- [x] Add `UploadAttachmentRequest` with `oneof payload` (metadata or chunk)
- [x] Add `UploadMetadata`, `UploadAttachmentResponse`, `GetAttachmentRequest`, `AttachmentInfo`
- [x] Add `DownloadAttachmentRequest`, `DownloadAttachmentResponse` with `oneof payload` (metadata or chunk)
- [x] Rebuild proto stubs via `./mvnw compile -pl quarkus/memory-service-proto-quarkus`

**GrpcStatusMapper:**
- [x] Add `FileStoreException` mapping (`FILE_TOO_LARGE` → `RESOURCE_EXHAUSTED`, others → `INTERNAL`)

**AttachmentsGrpcService:**
- [x] `uploadAttachment`: Client streaming → unary. Collect messages reactively, validate metadata, store via FileStore with SHA-256 digest
- [x] `getAttachment`: Unary → unary. Lookup, access control, map to `AttachmentInfo`
- [x] `downloadAttachment`: Unary → server streaming. Metadata first, then 64 KB chunks from FileStore

**Tests:**
- [x] Cucumber step definitions for gRPC attachment upload, download, and get metadata
- [x] `attachments-grpc.feature` with upload, download, metadata, and access control scenarios

### Phase 1.5: Frontend File Upload

Adds drag-and-drop file upload to the chat frontend so users can attach files
directly from the UI.

**Backend Changes:**
- `DELETE /v1/attachments/{id}` endpoint: allows the uploader to remove unlinked
  attachments (returns 409 if already linked to an entry)
- Quarkus & Spring example apps: streaming attachment proxy resources that forward
  `POST`, `GET`, and `DELETE` requests to the memory-service without buffering file
  content in memory

**Frontend Changes:**
- `useAttachments` hook: manages pending attachment state (upload via XHR with
  progress, abort, server-side delete on remove)
- `AttachmentChip` component: shows file icon, name, progress bar, and X button
- `ConversationsUIComposer`: drag-and-drop zone, attachment strip above textarea,
  paperclip file-picker button, attachment IDs included in message POST body
- Edit/fork composer: same attachment capabilities in the inline edit textarea
- `StreamStartParams.attachments`: optional `{ attachmentId }[]` forwarded in the
  SSE POST body; Quarkus and Spring example chat endpoints accept and pass them to
  the LLM

**Phase 1.5 Tasks:**
- [x] Add `DELETE /v1/attachments/{id}` to OpenAPI spec and `AttachmentsResource`
- [x] Create `AttachmentsProxyResource` (Quarkus) — streaming HTTP proxy
- [x] Create `AttachmentsProxyController` (Spring) — streaming WebClient proxy
- [x] Create `useAttachments` hook (XHR upload with progress)
- [x] Add `AttachmentChip` component and update Composer with drag-drop + strip
- [x] Thread attachment IDs through `StreamStartParams` → `useSseStream` → backend
- [x] Add `attachmentId` to Quarkus `MessageRequest` and Spring `AttachmentRef`
- [x] Update edit/fork composer with attachment support

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

### Phase 2 Tests

**Implemented Cucumber tests** (`features/attachments-rest.feature`):

```gherkin
Feature: Attachments REST API

  Scenario: Upload a file attachment
    When I upload a file "test.txt" with content type "text/plain" and content "Hello World"
    Then the response status should be 201
    And the response body field "id" should not be null
    And the response body field "href" should not be null
    And the response body field "contentType" should be "text/plain"
    And the response body field "filename" should be "test.txt"
    And the response body field "size" should be "11"
    And the response body field "sha256" should not be null
    And the response body field "expiresAt" should not be null

  Scenario: Retrieve an uploaded attachment
    # Upload, then GET the binary content back

  Scenario: Cannot retrieve unlinked attachment as different user
    # Upload as alice, attempt GET as bob → 403

  Scenario: Link attachment to entry via attachmentId reference
    # Upload, create entry with attachmentId, verify href is rewritten in response

  Scenario: History channel rejects attachment missing both href and attachmentId
    # Entry with attachment missing both fields → 400
```

**Future test areas:**
- Expired attachment cleanup job verification
- Upload reliability (long uploads, client disconnection)
- Cascade deletion when conversation groups are hard-deleted
- S3FileStore integration tests (`PostgresqlInfinispanS3CucumberTest` using LocalStack devservices)

---

## Phase 4: Signed Download URLs

### Problem Statement

When a user clicks a preview or download link for an attachment in the chat UI, the browser opens a new tab via `<a href>`. The new tab has no `Authorization` header because the app uses OIDC bearer tokens (no session cookies). This results in a 401 error.

### Solution

Two new endpoints provide signed, time-limited download URLs:

1. **`GET /v1/attachments/{id}/download-url`** (authenticated) — Returns a time-limited download URL.
   - For S3 storage: returns the S3 pre-signed URL directly (already implemented in `S3FileStore.getSignedUrl()`).
   - For DB storage: generates an HMAC-signed token encoding attachment ID + expiry.

2. **`GET /v1/attachments/download/{token}/{filename}`** (unauthenticated) — Serves the file using a verified signed token.

### Token Signing (`DownloadUrlSigner`)

An `@ApplicationScoped` bean using HMAC-SHA256 for token generation and verification.

**Token format**: Base64url-encoded `attachmentId.expiryEpochSeconds.hmacSignature`

**Key management**:
- Config: `memory-service.attachments.download-url-secret` (optional string)
- If not set: generates a random 256-bit key at startup (ephemeral — URLs don't survive restart, acceptable for short-lived tokens)
- If set: derives key via `SecretKeySpec`

**Verification**: Decode, split on `.`, recompute HMAC, constant-time compare via `MessageDigest.isEqual()`, check expiry.

### Configuration

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.attachments.download-url-expires-in` | `PT5M` | Duration for signed download URLs |
| `memory-service.attachments.download-url-secret` | (none) | Optional HMAC secret; random if unset |

### Frontend Changes

The `AttachmentPreview` component replaces direct `<a href>` links with click handlers that:
1. Call `GET /v1/attachments/{id}/download-url` with the bearer token
2. Open the returned signed URL in a new tab (preview) or trigger a download

### Phase 4 Tasks

- [x] Create `DownloadUrlSigner` utility class (HMAC-SHA256 token generation/verification)
- [x] Add config properties (`download-url-expires-in`, `download-url-secret`)
- [x] Add `GET /v1/attachments/{id}/download-url` endpoint to `AttachmentsResource`
- [x] Create `AttachmentDownloadResource` (unauthenticated download endpoint)
- [x] Update OpenAPI spec with new endpoints and schema
- [x] Update auth permissions (`application.properties`) to allow unauthenticated downloads
- [x] Update Quarkus proxy (download-url proxy + download proxy resource + auth config)
- [x] Update Spring proxy (download-url proxy + download proxy controller + security config)
- [x] Update frontend `AttachmentPreview` to use signed download URLs

---

## References

- [Quarkus LangChain4j: Passing Images](https://docs.quarkiverse.io/quarkus-langchain4j/dev/guide-passing-image.html)
- [Spring AI: Multimodality](https://docs.spring.io/spring-ai/reference/api/multimodality.html)
- [036: Rich Event Types in History Entries](./036-rich-types-responses.md)
- [015: Background Task Queue](./015-task-queue.md)
