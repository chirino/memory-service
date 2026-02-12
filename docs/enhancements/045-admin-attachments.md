# 045: Admin API for Attachments

## Status
Proposed

## Motivation

The memory-service provides admin APIs for conversations (`/v1/admin/conversations/*`) but has no admin visibility or control over attachments. Platform administrators currently cannot:

- **List all attachments** across all users (for storage auditing, compliance, or debugging).
- **Inspect attachment metadata** regardless of ownership (support scenarios where a user reports issues with uploads).
- **Delete any attachment** regardless of ownership or link status (the user-facing `DELETE /v1/attachments/{id}` only allows the uploader to delete their own unlinked attachments).
- **Download attachment content** for any attachment regardless of ownership (for compliance, support, or legal hold scenarios).
This enhancement introduces `/v1/admin/attachments/*` endpoints that mirror the pattern established by the existing admin conversation APIs (enhancement 014), reusing the same role model, audit logging, and justification infrastructure.

## Design Decisions

### Reuse Existing Admin Infrastructure

All new endpoints use the same authorization and audit patterns as `AdminResource`:

- **Roles**: `auditor` (read-only) and `admin` (read + write). Resolved via `AdminRoleResolver`.
- **Audit logging**: All calls logged via `AdminAuditLogger` with optional `justification`.
- **HTTP auth policy**: Already covered by `quarkus.http.auth.permission.admin.paths=/v1/admin/*`.

No new security infrastructure is needed.

### Separate Resource Class

A new `AdminAttachmentsResource` class handles attachment admin endpoints, keeping `AdminResource` focused on conversations. This follows the same separation used between `AttachmentsResource` and `ConversationsResource` in the user-facing API.

### Admin Delete Semantics

The user-facing `DELETE /v1/attachments/{id}` has two restrictions:
1. Only the uploader can delete.
2. Linked attachments (those with an `entryId`) cannot be deleted.

The admin delete removes **both restrictions**: any admin can delete any attachment, whether or not it is linked to an entry. When an admin deletes a linked attachment:
1. The attachment record and its blob are deleted using the existing `AttachmentDeletionService.deleteAttachment()` protocol (reference-counting safe).
2. The entry's `attachments` array in the stored content is **not modified** -- the `href` remains but will 404 on retrieval. This is acceptable because the admin is intentionally removing content (e.g., policy violation, legal takedown).
3. If the attachment's `storageKey` is shared with other attachment records (fork case), only this reference is removed; the blob is deleted only when the last reference is gone.

### Admin Download Endpoints

The user-facing `GET /v1/attachments/{id}` and `GET /v1/attachments/{id}/download-url` enforce ownership checks for unlinked attachments. Admins need to retrieve attachment content for any attachment regardless of ownership -- for example, when investigating a support ticket or performing a compliance review.

Two admin download endpoints mirror the user-facing pattern:
1. **`GET /v1/admin/attachments/{id}/content`** -- Streams the binary content directly (or redirects to a signed S3 URL). Same behavior as the user-facing `GET /v1/attachments/{id}` but without ownership checks.
2. **`GET /v1/admin/attachments/{id}/download-url`** -- Returns a time-limited signed download URL. Same behavior as the user-facing `GET /v1/attachments/{id}/download-url` but without ownership checks.

Both endpoints require the `auditor` role (read access). The signed download URL approach is preferred for browser-based admin UIs, since the returned URL can be opened in a new tab without requiring an `Authorization` header.

## API Design

### Endpoints

| Endpoint | Method | Role | Description |
|----------|--------|------|-------------|
| `/v1/admin/attachments` | GET | auditor | List attachments with filtering and pagination |
| `/v1/admin/attachments/{id}` | GET | auditor | Get attachment metadata (any attachment) |
| `/v1/admin/attachments/{id}/content` | GET | auditor | Download attachment binary content |
| `/v1/admin/attachments/{id}/download-url` | GET | auditor | Get a signed time-limited download URL |
| `/v1/admin/attachments/{id}` | DELETE | admin | Delete any attachment (including linked) |

### List Attachments

```
GET /v1/admin/attachments?userId=alice&status=linked&after=cursor&limit=50&justification=audit
```

#### Query Parameters

All filter parameters correspond to indexed columns to ensure predictable query performance.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `userId` | string | -- | Filter by uploader user ID (new index) |
| `entryId` | string | -- | Filter by linked entry ID (indexed) |
| `status` | string | `all` | Filter: `linked`, `unlinked`, `expired`, `all` (maps to indexed `entry_id` and `expires_at`) |
| `after` | string | -- | Cursor for pagination (encodes `created_at` + `id`) |
| `limit` | integer | 50 | Page size (max 200) |
| `justification` | string | -- | Audit justification |

#### Response

```json
{
  "data": [
    {
      "id": "att-abc123",
      "storageKey": "12345",
      "filename": "photo.jpg",
      "contentType": "image/jpeg",
      "size": 102400,
      "sha256": "e3b0c44...",
      "userId": "alice",
      "entryId": "entry-456",
      "expiresAt": null,
      "createdAt": "2024-01-15T10:00:00Z",
      "deletedAt": null
    }
  ],
  "nextCursor": "eyJpZCI6..."
}
```

### Get Attachment Metadata

```
GET /v1/admin/attachments/{id}?justification=support+ticket+1234
```

Returns the same schema as a single item in the list response. Unlike the user-facing endpoint, this returns metadata (not binary content) and does not enforce ownership checks. Includes soft-deleted attachments.

#### Response

```json
{
  "id": "att-abc123",
  "storageKey": "12345",
  "filename": "photo.jpg",
  "contentType": "image/jpeg",
  "size": 102400,
  "sha256": "e3b0c44...",
  "userId": "alice",
  "entryId": "entry-456",
  "expiresAt": null,
  "createdAt": "2024-01-15T10:00:00Z",
  "deletedAt": null,
  "refCount": 2
}
```

The `refCount` field indicates how many attachment records share the same `storageKey`. This helps admins understand whether deleting this attachment will free storage or just remove a reference.

### Download Attachment Content

```
GET /v1/admin/attachments/{id}/content?justification=compliance+review
```

Streams the binary file content. Bypasses ownership checks -- any attachment can be downloaded by an auditor or admin.

**Behavior:**
1. Look up the attachment (including soft-deleted records, but `storageKey` must exist).
2. If the file store supports signed URLs (S3), return a `302` redirect to the signed URL.
3. Otherwise, stream the binary content with appropriate `Content-Type`, `Content-Length`, and `Content-Disposition` headers.

**Error responses:**
- `404` if attachment not found or content not available (no `storageKey`).
- `403` if caller lacks auditor role.

### Get Download URL

```
GET /v1/admin/attachments/{id}/download-url?justification=support+ticket+5678
```

Returns a time-limited signed URL for downloading the attachment without authentication. This is the same mechanism as the user-facing `GET /v1/attachments/{id}/download-url` but without ownership checks.

#### Response

```json
{
  "url": "/v1/attachments/download/eyJhbGci.../photo.jpg",
  "expiresIn": 300
}
```

For S3-backed storage, the `url` is a pre-signed S3 URL. For DB-backed storage, it is an HMAC-signed token URL served by the existing `AttachmentDownloadResource`.

**Error responses:**
- `404` if attachment not found or content not available.
- `403` if caller lacks auditor role.

### Delete Attachment

```
DELETE /v1/admin/attachments/{id}
Content-Type: application/json

{
  "justification": "Storage cleanup per policy"
}
```

**Behavior:**
1. Look up the attachment (including soft-deleted records).
2. If the attachment is linked to an entry, clear the `entryId` first (unlink it so `AttachmentDeletionService` can proceed).
3. Delete using the existing `AttachmentDeletionService.deleteAttachment()` protocol (reference-counting safe).
4. Return `204 No Content`.

The entry's stored `attachments` array is not modified -- the `href` remains but will 404 on retrieval.

**Error responses:**
- `404` if attachment not found.
- `403` if caller lacks admin role.

## Store Layer Changes

### AdminAttachmentQuery Model

```java
public class AdminAttachmentQuery {
    private String userId;                 // indexed (new index added by this enhancement)
    private String entryId;                // indexed
    private AttachmentStatus status;       // maps to indexed entry_id and expires_at
    private String after;                  // cursor (encodes created_at + id)
    private int limit = 50;
}

public enum AttachmentStatus {
    LINKED, UNLINKED, EXPIRED, ALL
}
```

### AttachmentStore Interface Additions

```java
public interface AttachmentStore {
    // ... existing methods ...

    // Admin methods
    List<AttachmentDto> adminList(AdminAttachmentQuery query);
    Optional<AttachmentDto> adminFindById(String id);  // no ownership filter, includes soft-deleted
    long adminCount(AdminAttachmentQuery query);
    void adminUnlinkFromEntry(String attachmentId);    // clear entryId, set expiresAt (for admin delete of linked attachments)
}
```

### PostgreSQL Queries

**adminList** (dynamic query building -- all filters use indexed columns):
```sql
SELECT a.*
FROM attachments a
WHERE 1=1
  AND (:userId IS NULL OR a.user_id = :userId)
  AND (:entryId IS NULL OR a.entry_id = :entryId)
  AND (:status = 'ALL'
       OR (:status = 'LINKED' AND a.entry_id IS NOT NULL)
       OR (:status = 'UNLINKED' AND a.entry_id IS NULL AND (a.expires_at IS NULL OR a.expires_at >= NOW()))
       OR (:status = 'EXPIRED' AND a.expires_at IS NOT NULL AND a.expires_at < NOW()))
  AND (:afterCreatedAt IS NULL
       OR (a.created_at, a.id) < (:afterCreatedAt, :afterId))
ORDER BY a.created_at DESC, a.id DESC
LIMIT :limit
```

### MongoDB Queries

Equivalent aggregation pipelines for `adminList` and `adminCount`. The `adminList` query uses `$lookup` to join with the entries collection for `conversationId` filtering.

### AttachmentRepository Additions (PostgreSQL)

```java
public interface AttachmentRepository {
    // ... existing methods ...

    // Admin queries
    List<AttachmentEntity> adminFind(/* dynamic criteria */);
    Optional<AttachmentEntity> adminFindById(UUID id);   // no deletedAt filter
}
```

Since the admin list query is dynamic (many optional filters), the implementation should use Panache's query builder or criteria API rather than fixed query strings.

### Schema Changes

Add a `user_id` index to the attachments table to support efficient admin filtering by uploader:

```sql
CREATE INDEX IF NOT EXISTS idx_attachments_user_id ON attachments(user_id);
CREATE INDEX IF NOT EXISTS idx_attachments_created_at_id ON attachments(created_at DESC, id DESC);
```

For MongoDB, add indexes on `userId` and `{createdAt: -1, _id: -1}` in the attachments collection.

The composite `(created_at DESC, id DESC)` index supports both the sort order and cursor pagination. The `id` column acts as a tiebreaker for attachments with identical `created_at` timestamps, ensuring deterministic ordering despite `UUID.randomUUID()` (v4) not being time-ordered.

## OpenAPI Specification

Add the new endpoints to `openapi-admin.yml`. Follow the existing patterns for pagination, error responses, and security schemes.

### New Schemas

```yaml
components:
  schemas:
    AdminAttachment:
      type: object
      properties:
        id:
          type: string
          format: uuid
        storageKey:
          type: string
        filename:
          type: string
        contentType:
          type: string
        size:
          type: integer
          format: int64
        sha256:
          type: string
        userId:
          type: string
        entryId:
          type: string
          format: uuid
        expiresAt:
          type: string
          format: date-time
        createdAt:
          type: string
          format: date-time
        deletedAt:
          type: string
          format: date-time
        refCount:
          type: integer
          description: Number of attachment records sharing the same storage blob

    AdminDownloadUrlResponse:
      type: object
      properties:
        url:
          type: string
          description: Time-limited signed URL for downloading the attachment
        expiresIn:
          type: integer
          description: URL validity duration in seconds
```

## Scope of Changes

### New Files

| File | Description |
|------|-------------|
| `memory-service/src/main/java/io/github/chirino/memory/api/AdminAttachmentsResource.java` | REST resource for admin attachment endpoints |
| `memory-service/src/main/java/io/github/chirino/memory/model/AdminAttachmentQuery.java` | Query model for admin attachment listing |
| `memory-service/src/test/resources/features/admin-attachments-rest.feature` | Cucumber test scenarios |

### Modified Files

| File | Change |
|------|--------|
| `memory-service/src/main/java/io/github/chirino/memory/attachment/AttachmentStore.java` | Add admin method signatures |
| `memory-service/src/main/java/io/github/chirino/memory/attachment/PostgresAttachmentStore.java` | Implement admin methods |
| `memory-service/src/main/java/io/github/chirino/memory/attachment/MongoAttachmentStore.java` | Implement admin methods |
| `memory-service/src/main/java/io/github/chirino/memory/attachment/AttachmentDto.java` | Add `refCount` field (or compute in resource layer) |
| `memory-service/src/main/java/io/github/chirino/memory/attachment/DownloadUrlSigner.java` | Reused by admin download-url endpoint |
| `memory-service/src/main/java/io/github/chirino/memory/persistence/repo/AttachmentRepository.java` | Add admin query methods |
| `memory-service/src/main/resources/db/schema.sql` | Add `idx_attachments_user_id` index |
| `memory-service-contracts/src/main/resources/openapi-admin.yml` | Add attachment admin endpoints and schemas |
| `site/src/pages/docs/configuration.md` | Document new admin attachment endpoints |

## Implementation Order

1. **Query model** -- `AdminAttachmentQuery`, `AttachmentStatus`
2. **AttachmentStore interface** -- Add admin method signatures
3. **PostgresAttachmentStore** -- Implement admin methods with dynamic query building
4. **MongoAttachmentStore** -- Implement admin methods with aggregation pipelines
5. **AdminAttachmentsResource** -- REST endpoints (list, get, download, download-url, delete) with role checks and audit logging
6. **OpenAPI admin spec** -- Add endpoints and schemas to `openapi-admin.yml`
7. **Regenerate admin client** -- Rebuild from updated spec
8. **Cucumber tests** -- Admin attachment test scenarios
9. **Site documentation** -- Add admin attachment docs

## Test Plan

### Cucumber Scenarios (`admin-attachments-rest.feature`)

```gherkin
Feature: Admin Attachments REST API

  # --- Listing ---

  Scenario: Admin can list all attachments across users
    Given "alice" uploads an attachment "photo.jpg" with content type "image/jpeg"
    And "bob" uploads an attachment "doc.pdf" with content type "application/pdf"
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments"
    Then the response status should be 200
    And the response body should contain 2 attachments

  Scenario: Admin can filter attachments by userId
    Given "alice" uploads an attachment
    And "bob" uploads an attachment
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments?userId=bob"
    Then the response body should contain 1 attachment
    And all attachments should have userId "bob"

  Scenario: Admin can filter attachments by status
    Given "alice" uploads an attachment that is linked to an entry
    And "alice" uploads an attachment that is unlinked
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments?status=linked"
    Then the response body should contain 1 attachment

  Scenario: Admin can paginate attachment listing
    Given 5 attachments exist
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments?limit=2"
    Then the response body should contain 2 attachments
    And the response body field "nextCursor" should not be null

  # --- Get Metadata ---

  Scenario: Admin can get metadata for any attachment
    Given "bob" uploads an attachment "secret.jpg"
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments/{attachmentId}"
    Then the response status should be 200
    And the response body field "filename" should be "secret.jpg"
    And the response body field "userId" should be "bob"

  Scenario: Admin can see soft-deleted attachment metadata
    Given "bob" uploads an attachment
    And the attachment is soft-deleted
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments/{attachmentId}"
    Then the response status should be 200
    And the response body field "deletedAt" should not be null

  # --- Download ---

  Scenario: Admin can download content for any attachment
    Given "bob" uploads an attachment "photo.jpg" with content "image data"
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments/{attachmentId}/content"
    Then the response status should be 200
    And the response body should contain "image data"

  Scenario: Admin can get download URL for any attachment
    Given "bob" uploads an attachment
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments/{attachmentId}/download-url"
    Then the response status should be 200
    And the response body field "url" should not be null
    And the response body field "expiresIn" should not be null

  Scenario: Download returns 404 for attachment without stored content
    Given an attachment record exists without a storageKey
    When I am authenticated as admin user "alice"
    And I call GET "/v1/admin/attachments/{attachmentId}/content"
    Then the response status should be 404

  # --- Delete ---

  Scenario: Admin can delete an unlinked attachment
    Given "bob" uploads an attachment
    When I am authenticated as admin user "alice"
    And I call DELETE "/v1/admin/attachments/{attachmentId}" with justification "cleanup"
    Then the response status should be 204

  Scenario: Admin can delete a linked attachment
    Given "bob" uploads an attachment linked to an entry
    When I am authenticated as admin user "alice"
    And I call DELETE "/v1/admin/attachments/{attachmentId}" with justification "policy violation"
    Then the response status should be 204

  # --- Authorization ---

  Scenario: Auditor can list and view attachments
    Given "bob" uploads an attachment
    When I am authenticated as auditor user "charlie"
    And I call GET "/v1/admin/attachments"
    Then the response status should be 200

  Scenario: Auditor cannot delete attachments
    Given "bob" uploads an attachment
    When I am authenticated as auditor user "charlie"
    And I call DELETE "/v1/admin/attachments/{attachmentId}"
    Then the response status should be 403

  Scenario: Non-admin user cannot access admin attachment endpoints
    Given "bob" uploads an attachment
    When I am authenticated as user "bob"
    And I call GET "/v1/admin/attachments"
    Then the response status should be 403

```

## Future Considerations

- **Storage statistics endpoint**: Aggregated metrics (total count/bytes, breakdown by status/content-type/user) for dashboards and capacity planning.
- **Bulk operations**: Batch delete by query criteria (e.g., delete all expired attachments for a user) rather than one-by-one.
- **Storage quotas**: Per-user storage limits enforced at upload time, manageable through admin APIs.
- **Content scanning**: Admin-triggered virus/malware scans on stored attachments.
- **Export**: Admin ability to export all attachments for a conversation (for legal discovery or data portability).
