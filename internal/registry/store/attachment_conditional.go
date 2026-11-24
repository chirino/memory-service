package store

import (
	"fmt"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
)

// Pending attachment statuses. FinalizeAttachment and MarkAttachmentFailed only
// act on rows in one of these states.
const (
	AttachmentStatusUploading   = "uploading"
	AttachmentStatusDownloading = "downloading"
	AttachmentStatusReady       = "ready"
	AttachmentStatusFailed      = "failed"
)

// ValidatePendingAttachment checks the input of CreateAttachmentWithID. It is
// shared by every store so an invalid request is rejected the same way, before
// any row or blob reference exists.
func ValidatePendingAttachment(attachmentID uuid.UUID, attachment model.Attachment) error {
	if attachmentID == uuid.Nil {
		return &ValidationError{Field: "id", Message: "attachment ID is required"}
	}
	if attachment.Status != AttachmentStatusUploading && attachment.Status != AttachmentStatusDownloading {
		return &ValidationError{Field: "status", Message: fmt.Sprintf("a caller-supplied attachment must start %q or %q", AttachmentStatusUploading, AttachmentStatusDownloading)}
	}
	if attachment.StorageKey != nil {
		return &ValidationError{Field: "storageKey", Message: "a pending attachment has no stored content yet"}
	}
	if attachment.EntryID != nil {
		return &ValidationError{Field: "entryId", Message: "a pending attachment cannot be linked"}
	}
	if attachment.Size != nil || attachment.SHA256 != nil {
		return &ValidationError{Field: "content", Message: "size and sha256 are set when the attachment is finalized"}
	}
	if attachment.ArchivedAt != nil {
		return &ValidationError{Field: "archivedAt", Message: "a new attachment cannot start archived"}
	}
	// ID, UserID, AttemptID and CreatedAt are store-controlled: each store
	// overwrites whatever the caller put there.
	return nil
}

// ValidateAttachmentFinalize checks the input of FinalizeAttachment.
func ValidateAttachmentFinalize(finalize AttachmentFinalize) error {
	if finalize.StorageKey == "" {
		return &ValidationError{Field: "storageKey", Message: "a finalized attachment requires a storage key"}
	}
	return nil
}

// AttachmentNotReadyConflictCode marks the ConflictError returned when an entry
// references an attachment whose upload or download has not finished.
const AttachmentNotReadyConflictCode = "attachment_not_ready"

// NewAttachmentNotReadyConflict rejects linking an attachment that is not
// "ready". The message names the current status so a client can tell an
// unfinished transfer ("uploading", "downloading") from a failed one.
func NewAttachmentNotReadyConflict(attachmentID uuid.UUID, status string) error {
	message := fmt.Sprintf("attachment %s is %s and cannot be linked to an entry until it is ready", attachmentID, status)
	if status == AttachmentStatusFailed {
		message = fmt.Sprintf("attachment %s failed and cannot be linked to an entry", attachmentID)
	}
	return &ConflictError{Message: message, Code: AttachmentNotReadyConflictCode}
}

// ResolveAttachmentLink decides the outcome of LinkAttachmentToEntry after its
// conditional update ran, from the row as it is stored now. The update already
// enforces every condition atomically, so this only classifies why it did or did
// not match. Ownership is checked first so another user's attachment never
// reveals its status. The caller reports a missing or archived row as not found.
func ResolveAttachmentLink(attachment model.Attachment, userID string, entryID uuid.UUID) (*model.Attachment, error) {
	if attachment.UserID != userID {
		return nil, &ForbiddenError{}
	}
	if attachment.EntryID != nil {
		if *attachment.EntryID == entryID {
			return &attachment, nil
		}
		return nil, &ConflictError{Message: fmt.Sprintf("attachment %s is linked to a different entry", attachment.ID)}
	}
	if attachment.Status != AttachmentStatusReady {
		return nil, NewAttachmentNotReadyConflict(attachment.ID, attachment.Status)
	}
	// Unlinked and ready, yet the update matched nothing: the status changed
	// between the update and this read. The caller can retry.
	return nil, &ConflictError{Message: fmt.Sprintf("attachment %s changed while it was being linked; retry", attachment.ID)}
}
