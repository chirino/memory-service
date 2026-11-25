package attachments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/route/routetx"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ParseCallerAttachmentIDJSON validates the optional caller-supplied ID of a JSON
// request body. raw is nil only when the key was absent. The contract makes id
// optional but not nullable, so an explicit null, like any other non-string
// value, is rejected instead of silently taking the no-ID path.
func ParseCallerAttachmentIDJSON(raw json.RawMessage) (id uuid.UUID, present bool, err error) {
	if raw == nil {
		return uuid.Nil, false, nil
	}
	var s string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &s) != nil {
		return uuid.Nil, true, fmt.Errorf("id must be a valid UUID string")
	}
	return ParseCallerAttachmentID(&s)
}

// ParseCallerAttachmentID validates the optional caller-supplied attachment ID.
// A nil raw means the caller sent no ID. An empty or malformed value is an
// error, as is the nil UUID, so a present-but-blank field is never mistaken for
// an absent one.
func ParseCallerAttachmentID(raw *string) (id uuid.UUID, present bool, err error) {
	if raw == nil {
		return uuid.Nil, false, nil
	}
	id, err = uuid.Parse(*raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, true, fmt.Errorf("id must be a valid UUID")
	}
	return id, true, nil
}

// recordCallerAttachmentID attaches a validated caller-supplied ID to the
// request's operation event so created and duplicate requests correlate with the
// attachment. A rejected ID is never recorded.
func recordCallerAttachmentID(c *gin.Context, id uuid.UUID, present bool) {
	if !present {
		return
	}
	if event := security.OperationEventFromGin(c); event != nil {
		event.SetAttachmentID(id.String())
	}
}

// attachmentAttemptID returns the attempt that created the row. The store always
// sets it for a caller-supplied ID, so a missing value is a store bug and must
// not be papered over.
func attachmentAttemptID(attachment *model.Attachment) (uuid.UUID, error) {
	if attachment == nil || attachment.AttemptID == nil {
		return uuid.Nil, errors.New("store returned a caller-ID attachment without an attempt ID")
	}
	return *attachment.AttemptID, nil
}

// createPending creates the pending row for a caller-supplied ID inside one
// write transaction and returns the committed outcome. Nothing is written to the
// response here, and the outcome is reset at the start of every callback
// invocation, so a replayed transaction cannot leak an aborted attempt's result.
func createPending(c *gin.Context, store registrystore.MemoryStore, userID string, callerID uuid.UUID, pending model.Attachment, respond bool) (*registrystore.CreateAttachmentResult, error) {
	var result *registrystore.CreateAttachmentResult
	err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		result = nil
		res, err := store.CreateAttachmentWithID(ctx, userID, callerID, pending)
		if err != nil {
			return err
		}
		result = res
		if respond && res.Created {
			c.JSON(http.StatusCreated, toUploadResponse(res.Attachment))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !result.Created {
		// An existing ID is never a successful retry. The client reads the
		// status instead.
		return nil, registrystore.NewAttachmentIDExistsConflict(callerID)
	}
	return result, nil
}

// uploadWithCallerID runs a multipart upload for a caller-supplied ID:
//
//  1. create the "uploading" row (rejecting any existing ID before a blob exists),
//  2. store the blob, only for the request that created the row,
//  3. finalize under the attempt fence.
//
// The blob store and the cleanup enqueue happen outside every transaction
// callback, so a replayed callback cannot store or enqueue twice.
func uploadWithCallerID(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, userID string, callerID uuid.UUID, file io.Reader, filename, contentType string, expiresIn time.Duration) {
	var name *string
	if filename != "" {
		name = &filename
	}
	expiresAt := time.Now().Add(expiresIn)
	created, err := createPending(c, store, userID, callerID, model.Attachment{
		Filename:    name,
		ContentType: contentType,
		ExpiresAt:   &expiresAt,
		Status:      registrystore.AttachmentStatusUploading,
	}, false)
	if err != nil {
		handleError(c, err)
		return
	}
	attemptID, err := attachmentAttemptID(created.Attachment)
	if err != nil {
		handleError(c, err)
		return
	}

	result, err := attachStore.Store(c.Request.Context(), file, cfg.AttachmentMaxSize, contentType)
	if err != nil {
		markAttemptFailed(c.Request.Context(), store, userID, callerID, attemptID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	refreshed := time.Now().Add(expiresIn)
	err = routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		attachment, err := store.FinalizeAttachment(ctx, userID, callerID, attemptID, registrystore.AttachmentFinalize{
			StorageKey: result.StorageKey,
			Size:       result.Size,
			SHA256:     result.SHA256,
			ExpiresAt:  &refreshed,
		})
		if err != nil {
			return err
		}
		c.JSON(http.StatusCreated, toUploadResponse(attachment))
		return nil
	})
	if err == nil {
		return
	}
	err = registrystore.AttachmentSupersededOrError(callerID, err)
	// The blob is this request's own. If the finalize actually committed and only
	// its acknowledgment was lost, the worker's reference check keeps the blob.
	if cleanupErr := registrystore.EnqueueAttachmentBlobDeleteAfterFinalizationFailure(c.Request.Context(), store, result.StorageKey); cleanupErr != nil {
		log.Error("Failed to enqueue attachment cleanup after finalize failure", "attachmentId", callerID.String(), "storageKey", result.StorageKey, "err", cleanupErr, "cause", err)
		err = errors.Join(err, fmt.Errorf("enqueue stored attachment cleanup: %w", cleanupErr))
	}
	// Fenced: matches nothing when the finalize committed or the row was replaced.
	markAttemptFailed(c.Request.Context(), store, userID, callerID, attemptID, err)
	handleError(c, err)
}

// markAttemptFailed records the attempt's failure under the attempt fence. A
// matching-nothing result is expected and is not an error.
func markAttemptFailed(ctx context.Context, store registrystore.MemoryStore, userID string, attachmentID, attemptID uuid.UUID, cause error) {
	changed, err := registrystore.MarkAttachmentAttemptFailed(ctx, store, userID, attachmentID, attemptID)
	if err != nil {
		log.Error("Failed to mark attachment as failed", "attachmentId", attachmentID.String(), "err", err, "cause", cause)
		return
	}
	if !changed {
		log.Info("Attachment attempt already finished or replaced; row left unchanged", "attachmentId", attachmentID.String(), "cause", cause)
	}
}

// createSourceURLAttachmentWithCallerID creates the "downloading" row for a
// caller-supplied ID, answers 201 immediately, and starts the download only when
// this request created the row and its transaction committed.
func createSourceURLAttachmentWithCallerID(c *gin.Context, store registrystore.MemoryStore, attachStore registryattach.AttachmentStore, cfg *config.Config, userID string, callerID uuid.UUID, pending model.Attachment) {
	created, err := createPending(c, store, userID, callerID, pending, true)
	if err != nil {
		handleError(c, err)
		return
	}
	attemptID, err := attachmentAttemptID(created.Attachment)
	if err != nil {
		// The response was already committed with the row; the row cannot be
		// finalized without its attempt, so there is nothing safe to start.
		log.Error("Cannot start source URL download", "attachmentId", callerID.String(), "err", err)
		return
	}
	StartFencedSourceURLAttachmentDownload(c.Request.Context(), store, attachStore, cfg, callerID, attemptID, userID, *pending.SourceURL, pending.ContentType)
}

// finalizeFencedSourceURLAttachment finalizes a downloaded caller-ID attachment
// under its attempt fence. The blob is already stored, so every failure path
// queues it for deletion under its own key: a stale or failed finalize leaves it
// unreferenced, and a finalize that committed with a lost acknowledgment keeps
// it, because the delete worker refuses to delete a referenced key.
func finalizeFencedSourceURLAttachment(ctx context.Context, store registrystore.MemoryStore, cfg *config.Config, attachmentID, attemptID uuid.UUID, userID, contentType string, result *registryattach.FileStoreResult) error {
	finalize := registrystore.AttachmentFinalize{
		StorageKey:  result.StorageKey,
		Size:        result.Size,
		SHA256:      result.SHA256,
		ContentType: &contentType,
	}
	if cfg.AttachmentDefaultExpiresIn > 0 {
		refreshed := time.Now().Add(cfg.AttachmentDefaultExpiresIn)
		finalize.ExpiresAt = &refreshed
	}
	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		_, err := store.FinalizeAttachment(txCtx, userID, attachmentID, attemptID, finalize)
		return err
	})
	if err == nil {
		return nil
	}
	err = registrystore.AttachmentSupersededOrError(attachmentID, err)
	if cleanupErr := registrystore.EnqueueAttachmentBlobDeleteAfterFinalizationFailure(ctx, store, result.StorageKey); cleanupErr != nil {
		log.Error("Failed to enqueue downloaded attachment cleanup", "attachmentId", attachmentID.String(), "storageKey", result.StorageKey, "err", cleanupErr, "cause", err)
		err = errors.Join(err, fmt.Errorf("enqueue downloaded attachment cleanup: %w", cleanupErr))
	}
	markAttemptFailed(ctx, store, userID, attachmentID, attemptID, err)
	log.Error("Failed to finalize downloaded attachment", "attachmentId", attachmentID.String(), "err", err)
	return err
}
