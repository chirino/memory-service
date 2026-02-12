# Enhancement 044: Forked Attachments (Referencing and Copying Attachments Across Entries)

## Summary

When a conversation is forked, entries from the parent are visible to the fork through ancestry traversal — but their attachments remain strictly owned by the original entry. If a user wants to create a **new entry** in a fork (or any conversation) that references an attachment from an existing entry, there is no mechanism for this today. This enhancement explores options for allowing attachments to be referenced or copied across entries while ensuring blobs are deleted from the store when no longer referenced by any entry.

## Motivation

1. **Fork workflows**: A user forks a conversation at a point where the parent had image/file attachments. They want to compose a new message in the fork that references or reuses one of those attachments — for example, asking a different question about the same image. Today there is no way to do this without re-uploading the file.

2. **Entry editing / regeneration**: An agent regenerates a response but the user's original message (with attachments) needs to be re-created in the new branch. The attachments should carry over without re-upload.

3. **Cross-conversation context**: A user wants to reference a file from one conversation in another conversation within the same group.

4. **Cleanup correctness**: If attachments can be shared, the system must track when the last reference is removed so the underlying blob in the FileStore (S3, database) can be safely deleted.

## Current Architecture

### Attachment Lifecycle

```
Upload → AttachmentRecord (expiresAt set, entryId = null)
          ↓
Link to Entry → entryId set, expiresAt = null (permanent)
          ↓
Entry/Conversation Deleted → AttachmentRecord + blob deleted
```

### Key Constraints

- Each `AttachmentRecord` has exactly one `entryId` (or null if unlinked)
- Each `AttachmentRecord` has exactly one `storageKey` pointing to a blob in the FileStore
- Unlinked attachments expire via `AttachmentCleanupJob`
- Linked attachments are permanent until their entry's conversation group is hard-deleted
- The `hardDeleteConversationGroups()` method finds all attachments by entry IDs, deletes blobs, then deletes metadata
- Attachments store a `sha256` hash (computed on upload) but it is currently informational only

### Fork Entry Visibility

Forks don't copy entries. A fork references parent entries via `forkedAtConversationId` / `forkedAtEntryId`, and `getEntriesWithForkSupport()` walks the ancestry chain to assemble entries. Attachments in parent entries are naturally visible, but they remain **owned by** and **linked to** those parent entries.

## Problem Statement

Given:
- Entry A in conversation Root has Attachment X (image.png)
- User forks at entry A, creating Fork conversation
- User wants to create Entry B in Fork that says "Now crop the top-left corner of this image" and references Attachment X

Today the user must re-upload image.png. The system has no concept of "reuse attachment X in a new entry."

Additionally, if we allow sharing, we need to answer: **when can the blob be deleted?** Today, blob deletion is tied to the entry → conversation group lifecycle. With sharing, a blob may be referenced by entries in different conversations (or even different groups), and must survive until the last reference is gone.

## Design: Reference Counting on Shared Storage Keys

**Core idea**: Multiple `AttachmentRecord`s can point to the same `storageKey`. When referencing an existing attachment, the server creates a new record that shares the source's `storageKey` rather than copying the blob. The blob is only deleted from the FileStore when the last record referencing it is removed.

**How "reference an existing attachment" works**:

No new API field is needed. The client uses the existing `attachmentId` field regardless of whether the attachment is freshly uploaded or already linked to another entry. The server detects which case applies:

1. Client creates a new entry with `{ "attachmentId": "<id>" }` — same as today
2. Server looks up the attachment record (excluding soft-deleted records)
3. **If unlinked** (`entryId == null`): existing flow — link it to the new entry
4. **If already linked** (`entryId != null`): the attachment belongs to another entry, so:
   - Validate the user has access to the source attachment's conversation group
   - Create a new `AttachmentRecord` with the **same `storageKey`** (and same sha256/size/contentType) but a new ID
   - Link the new record to the new entry
   - Rewrite `attachmentId` → `href` using the new record's ID

**Schema**: No new tables. Multiple rows in the existing `attachments` table can share the same `storageKey`. Add a `deletedAt` column for soft deletes (if not already present on the attachment entity).

**On reference**: Create a new `AttachmentRecord` copying `storageKey`, `sha256`, `size`, `contentType`, `filename` from the source. All read queries and `createFromSource` lookups exclude soft-deleted records (`deletedAt IS NULL`).

### Deletion Protocol (Soft Delete + Locking)

Deleting a blob requires two operations that cannot be made atomic: removing the database record and removing the file from the FileStore (S3, database blob storage, etc.). A naive approach risks either data loss (file deleted while record still referenced) or orphaned files (record deleted but file remains).

The soft-delete protocol solves this by keeping a record in the database at all times while the file still exists:

```
1. SELECT FOR UPDATE all attachment records with matching storageKey
2. If more than one non-deleted record remains:
     → Hard delete the target record. Done — blob is still referenced.
3. If this is the last non-deleted record:
     → Soft delete it (set deletedAt = now()). Commit transaction.
     → Delete the file from the FileStore.
     → Hard delete the soft-deleted record.
```

**Why this works**:

| Scenario | What happens | Outcome |
|----------|-------------|---------|
| Normal case (not last reference) | Hard delete, blob still referenced | Clean — no file operation needed |
| Last reference, file delete succeeds | Soft delete → file delete → hard delete | Clean |
| Last reference, crash after soft delete but before file delete | Soft-deleted record still points to file | Cleanup job retries: finds soft-deleted records, deletes file, then hard deletes record |
| Last reference, crash after file delete but before hard delete | Soft-deleted record points to already-deleted file | Cleanup job hard deletes the stale record (file already gone) |
| Concurrent delete of last two references | `SELECT FOR UPDATE` serializes the two transactions — only one sees itself as the last | No race condition |

**Soft-deleted records are invisible**: all read queries, `createFromSource` lookups, and access control checks filter on `deletedAt IS NULL`. No one can take a new reference to a blob that is being deleted.

**Cleanup job**: A periodic scheduled task finds soft-deleted attachment records, attempts to delete their files from the FileStore (idempotent — no error if already gone), then hard deletes the records.

## API Design

### No Contract Changes Required

The existing `attachmentId` field already serves both purposes. No new fields are needed in the OpenAPI spec, proto definitions, or client SDKs.

**Existing flow** (fresh upload):
```json
POST /v1/conversations/{id}/entries
{
  "content": [{
    "role": "USER",
    "text": "Check out this image",
    "attachments": [{ "attachmentId": "aaa-bbb-ccc" }]
  }]
}
```

**New flow** (reference existing attachment) — identical from the client's perspective:
```json
POST /v1/conversations/{id}/entries
{
  "content": [{
    "role": "USER",
    "text": "Now crop the top-left corner",
    "attachments": [{ "attachmentId": "aaa-bbb-ccc" }]
  }]
}
```

The server detects that `aaa-bbb-ccc` is already linked to another entry and automatically creates a new record sharing the same blob.

### Access Control

- **Unlinked attachment** (`entryId == null`): only the uploader can use it (existing behavior)
- **Already-linked attachment** (`entryId != null`): user must have **READER** access to the conversation group containing the source attachment's entry
- Cross-group references are **not allowed** — source and target must be in the same conversation group (this keeps the deletion lifecycle within the group boundary)

## Implementation Plan

### Step 1: Add `deletedAt` to Attachment Entity

Add a `deletedAt` timestamp column to the `attachments` table (if not already present). Update all read queries to filter on `deletedAt IS NULL`.

```sql
ALTER TABLE attachments ADD COLUMN deleted_at TIMESTAMPTZ;
```

Apply the equivalent change to the MongoDB model (`MongoAttachment`).

### Step 2: Update Entry Append Logic

In `ConversationsResource.appendEntry()` (and the gRPC equivalent), update the `attachmentId` handling to detect already-linked attachments:

```java
for (AttachmentRef att : attachments) {
    if (att.getAttachmentId() != null) {
        AttachmentDto source = attachmentStore.findById(att.getAttachmentId())
            .orElseThrow(() -> new ResourceNotFoundException("attachment", att.getAttachmentId()));

        if (source.entryId() != null) {
            // Already linked to another entry — create a shared reference
            validateAttachmentAccess(userId, source);
            AttachmentDto newAtt = attachmentStore.createFromSource(userId, source);
            attachmentStore.linkToEntry(newAtt.id(), entryId);
            att.setHref("/v1/attachments/" + newAtt.id());
        } else {
            // Unlinked (fresh upload) — existing flow
            attachmentStore.linkToEntry(source.id(), entryId);
            att.setHref("/v1/attachments/" + source.id());
        }
        att.setAttachmentId(null);
    }
    // ... existing href handling ...
}
```

### Step 4: Update AttachmentStore

Add a method to create an attachment record from an existing source:

```java
AttachmentDto createFromSource(String userId, AttachmentDto source);
```

Creates a new record copying `storageKey`, `sha256`, `size`, `contentType`, `filename` from the source, with:
- New UUID
- `entryId = null` (linked in the next step)
- `expiresAt` set to a short duration (cleared on link)
- `deletedAt = null`

The source lookup must exclude soft-deleted records (`deletedAt IS NULL`) to prevent referencing a blob that is being deleted.

### Step 5: Update Deletion Logic

Implement the soft-delete protocol described in the design section:

```java
@Transactional
void deleteAttachment(String attachmentId) {
    AttachmentDto att = attachmentStore.findById(attachmentId).orElseThrow();
    String storageKey = att.storageKey();

    // Lock ALL records sharing this storageKey to prevent concurrent races
    List<AttachmentDto> siblings = attachmentStore.findByStorageKeyForUpdate(storageKey);

    long activeCount = siblings.stream()
        .filter(a -> a.deletedAt() == null)
        .count();

    if (activeCount > 1) {
        // Other references remain — just hard delete this record
        attachmentStore.hardDelete(attachmentId);
    } else {
        // Last reference — soft delete, then clean up file outside the transaction
        attachmentStore.softDelete(attachmentId);
    }
}

// Called after the transaction commits, or by the cleanup job
void cleanupSoftDeletedAttachment(AttachmentDto att) {
    fileStore.delete(att.storageKey());  // Idempotent — no error if already gone
    attachmentStore.hardDelete(att.id());
}
```

Update `AttachmentCleanupJob` and `hardDeleteConversationGroups()` to use the same protocol.

### Step 6: Add Soft-Delete Cleanup Job

A periodic job that retries file deletion for any soft-deleted attachment records. This handles crashes where the soft delete committed but the file deletion or hard delete didn't complete.

```java
@Scheduled(every = "${memory-service.attachments.cleanup-interval:5m}")
void cleanupSoftDeletedAttachments() {
    List<AttachmentDto> softDeleted = attachmentStore.findSoftDeleted();
    for (AttachmentDto att : softDeleted) {
        fileStore.delete(att.storageKey());   // Idempotent
        attachmentStore.hardDelete(att.id());
    }
}
```

### Step 7: Update Chat Frontend for Fork Attachments

When a user forks a message in the chat frontend, the forked message editor should carry over the attachments from the original message:

- Populate the attachment list in the editor with the parent message's attachments
- Display each attachment with an **[X]** remove button, identical to how freshly uploaded attachments appear
- The user can remove any attachment before submitting the forked message
- On submit, send the remaining attachments using the existing `attachmentId` field — the server handles creating shared references automatically

This gives the user a seamless fork experience: attachments carry over by default, but they have full control to remove any they don't want in the new message.

### Step 8: Add Tests

- Cucumber scenarios for REST and gRPC
- Reference attachment from parent conversation entry
- Reference attachment from sibling fork entry
- Reject cross-group references
- Reject references to attachments the user cannot access
- Verify blob survives when one referencing record is deleted but another remains
- Verify blob file is deleted when the last referencing record is deleted
- Verify soft-deleted records are invisible to reads and `createFromSource`
- Verify cleanup job retries soft-deleted records

### Step 9: Compile and Verify

```bash
./mvnw compile
./mvnw test > test.log 2>&1
# Grep for errors
```

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Crash after soft delete, before file deletion | Soft-deleted record lingers, file remains in store | Cleanup job retries: finds soft-deleted records, deletes file, hard deletes record |
| Crash after file deletion, before hard delete | Soft-deleted record points to nonexistent file | Cleanup job hard deletes the stale record; `fileStore.delete()` is idempotent |
| Concurrent deletes of last two references | Race on which is "last" | `SELECT ... FOR UPDATE` on all records with the same `storageKey` serializes the transactions |
| New reference created to a blob being deleted | New record points to a file about to be removed | Soft-deleted records are excluded from all lookups, so `createFromSource` cannot reference them |
| Cross-group reference attempts | Security: accessing blobs from other users | Strict same-group validation on the source attachment's conversation group |
| Source attachment deleted during reference creation | New record points to soft-deleted blob | `createFromSource` filters on `deletedAt IS NULL`; `SELECT FOR UPDATE` serializes with concurrent deletes |

## Open Questions

1. **Should attachment references work cross-group?** This document assumes no (same group only) to keep deletion scoped. Cross-group support would require global reference tracking.

2. **What about external URL attachments?** Attachments with `href` pointing to external URLs (not `/v1/attachments/...`) don't have a blob in our store. Referencing only applies to uploaded attachments.

## Future Work: SHA-256 Storage Deduplication Job

When the same file is uploaded multiple times independently, each upload creates a separate blob in the FileStore with a different `storageKey` — but the `sha256` hash will be identical. A background deduplication job could consolidate these:

1. Find attachment records where multiple `storageKey` values share the same `sha256`
2. Pick one `storageKey` as the canonical copy
3. Update all records to point to the canonical `storageKey`
4. Delete the duplicate files from the FileStore

**Deferred**: This optimization suffers from the same cross-system consistency problem as deletion — updating the DB records and deleting duplicate files cannot be done atomically. A crash between steps 3 and 4 leaves orphaned files; a crash during step 3 could leave some records pointing to a file that gets deleted. Solving this correctly requires the same soft-delete/locking patterns applied here, plus careful ordering to ensure no data loss. Since duplicate uploads are expected to be infrequent, this is deferred to a later enhancement.

## References

- [001-conversation-forking-design.md](001-conversation-forking-design.md) — Fork data model
- [034-forked-entry-retrieval.md](034-forked-entry-retrieval.md) — Fork-aware entry retrieval
- [043-improved-quarkus-attachments.md](043-improved-quarkus-attachments.md) — Multimodal attachment handling
