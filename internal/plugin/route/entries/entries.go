package entries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MountRoutes mounts entry routes.
func MountRoutes(r *gin.Engine, store registrystore.MemoryStore, auth gin.HandlerFunc) {
	clientID := security.ClientIDMiddleware()

	g := r.Group("/v1", auth, clientID)

	g.GET("/conversations/:conversationId/entries", func(c *gin.Context) {
		listEntries(c, store)
	})
	g.POST("/conversations/:conversationId/entries", func(c *gin.Context) {
		appendEntry(c, store)
	})
	g.POST("/conversations/:conversationId/entries/sync", func(c *gin.Context) {
		syncMemory(c, store)
	})
}

func listEntries(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 50)

	clientIDParam := queryPtr(c, "clientId")
	if clientIDParam == nil {
		if clientID := security.GetClientID(c); clientID != "" {
			clientIDParam = &clientID
		}
	}

	// Determine channel filter.
	var channelPtr *model.Channel
	channelQueryRaw := strings.TrimSpace(strings.ToLower(c.Query("channel")))
	if channelQueryRaw != "" {
		switch model.Channel(channelQueryRaw) {
		case model.ChannelHistory, model.ChannelMemory:
			ch := model.Channel(channelQueryRaw)
			channelPtr = &ch
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel"})
			return
		}
	}

	// Java parity: when no channel specified and no clientId, default to history only.
	// When clientId is present (agent), show all channels (nil = no filter).
	if channelPtr == nil && clientIDParam == nil {
		ch := model.ChannelHistory
		channelPtr = &ch
	}

	// Memory channel access is client-scoped; without a client id, force to history.
	if channelPtr != nil && *channelPtr == model.ChannelMemory && clientIDParam == nil {
		ch := model.ChannelHistory
		channelPtr = &ch
	}

	allForks := strings.EqualFold(c.DefaultQuery("forks", "none"), "all")

	var epochFilter *registrystore.MemoryEpochFilter
	if channelPtr != nil && *channelPtr == model.ChannelMemory {
		filter, err := registrystore.ParseMemoryEpochFilter(c.Query("epoch"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		epochFilter = filter
	}

	result, err := store.GetEntries(c.Request.Context(), userID, convID, afterCursor, limit, channelPtr, epochFilter, clientIDParam, allForks)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result.Data, "afterCursor": result.AfterCursor})
}

func appendEntry(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	var req struct {
		Entries []registrystore.CreateEntryRequest `json:"entries"`
		// Single entry mode
		Content                json.RawMessage `json:"content"`
		ContentType            string          `json:"contentType"`
		Channel                string          `json:"channel"`
		Epoch                  *int64          `json:"epoch"`
		IndexedContent         *string         `json:"indexedContent,omitempty"`
		Role                   *string         `json:"role,omitempty"`
		UserID                 *string         `json:"userId,omitempty"`
		ForkedAtConversationID *uuid.UUID      `json:"forkedAtConversationId,omitempty"`
		ForkedAtEntryID        *uuid.UUID      `json:"forkedAtEntryId,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entries := req.Entries
	if len(entries) == 0 && len(req.Content) > 0 {
		entries = []registrystore.CreateEntryRequest{{
			Content:                req.Content,
			ContentType:            req.ContentType,
			Channel:                req.Channel,
			IndexedContent:         req.IndexedContent,
			Role:                   req.Role,
			UserID:                 req.UserID,
			ForkedAtConversationID: req.ForkedAtConversationID,
			ForkedAtEntryID:        req.ForkedAtEntryID,
		}}
	}
	if len(entries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one entry required"})
		return
	}

	// Track which entries had an explicit channel (for validation scoping).
	explicitChannel := make([]bool, len(entries))
	for i := range entries {
		explicitChannel[i] = strings.TrimSpace(entries[i].Channel) != ""
		if !explicitChannel[i] {
			entries[i].Channel = string(model.ChannelHistory)
		}
	}

	clientID := queryPtr(c, "clientId")
	if clientID == nil {
		cid := security.GetClientID(c)
		if cid != "" {
			clientID = &cid
		}
	}

	// Validate each entry before calling store.
	for i, entry := range entries {
		// Content element count limit (max 1000).
		var contentElements []json.RawMessage
		if json.Unmarshal(entry.Content, &contentElements) == nil && len(contentElements) > 1000 {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    "validation_error",
				"error":   "validation_error",
				"details": gin.H{"message": fmt.Sprintf("content array exceeds maximum of 1000 elements (got %d)", len(contentElements))},
			})
			return
		}

		ch := model.Channel(strings.ToLower(entry.Channel))

		// userId validation
		if entry.UserID != nil && *entry.UserID != "" && *entry.UserID != userID {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "validation_error",
				"details": gin.H{"message": "userId does not match the authenticated user"},
			})
			return
		}

		// Memory channel requires clientID.
		if ch == model.ChannelMemory && clientID == nil {
			c.JSON(http.StatusForbidden, gin.H{
				"code":    "forbidden",
				"error":   "client id is required for memory channel",
				"details": gin.H{"message": "client id is required for memory channel"},
			})
			return
		}

		// Memory channel cannot have indexedContent.
		if ch == model.ChannelMemory && entry.IndexedContent != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "validation_error",
				"details": gin.H{"message": "indexedContent is only allowed on history channel"},
			})
			return
		}

		// ContentType is required.
		if strings.TrimSpace(entry.ContentType) == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "validation_error",
				"details": gin.H{"message": "contentType is required"},
			})
			return
		}

		// History channel validation — only when channel was explicitly set to HISTORY.
		// Entries defaulted to HISTORY (no channel specified) skip strict contentType
		// validation for Java parity (Java allows arbitrary contentTypes on default channel).
		if ch == model.ChannelHistory && explicitChannel[i] {
			if err := validateHistoryEntry(entry, i); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "validation_error",
					"details": gin.H{"message": err.Error()},
				})
				return
			}
		}
	}

	// Resolve attachmentId references in content before creating entries.
	// This replaces attachmentId with href and tracks attachment IDs to link after creation.
	type pendingLink struct {
		attachmentID uuid.UUID
		entryIndex   int
	}
	var pendingLinks []pendingLink

	for i, entry := range entries {
		ch := model.Channel(strings.ToLower(entry.Channel))
		if ch != model.ChannelHistory {
			continue
		}
		modified, links, err := resolveAttachmentRefs(c.Request.Context(), store, userID, convID, entry.Content)
		if err != nil {
			handleAttachmentError(c, err)
			return
		}
		if modified != nil {
			entries[i].Content = modified
		}
		for _, id := range links {
			pendingLinks = append(pendingLinks, pendingLink{attachmentID: id, entryIndex: i})
		}
	}

	result, err := store.AppendEntries(c.Request.Context(), userID, convID, entries, clientID, req.Epoch)
	if err != nil {
		handleError(c, err)
		return
	}

	// Link attachments to created entries by updating entry_id.
	for _, link := range pendingLinks {
		if link.entryIndex < len(result) {
			entryID := result[link.entryIndex].ID
			store.UpdateAttachment(c.Request.Context(), userID, link.attachmentID, registrystore.AttachmentUpdate{
				EntryID: &entryID,
			})
		}
	}

	if len(result) == 1 {
		c.JSON(http.StatusCreated, result[0])
	} else {
		c.JSON(http.StatusCreated, result)
	}
}

// validateHistoryEntry validates content structure for history channel entries.
func validateHistoryEntry(entry registrystore.CreateEntryRequest, _ int) error {
	// ContentType must be "history" or "history/<subtype>".
	ct := strings.ToLower(strings.TrimSpace(entry.ContentType))
	if ct != "history" && !strings.HasPrefix(ct, "history/") {
		return fmt.Errorf("History channel entries must use 'history' or 'history/<subtype>' as the contentType")
	}

	// Parse content as JSON array.
	var contentArr []json.RawMessage
	if err := json.Unmarshal(entry.Content, &contentArr); err != nil {
		return fmt.Errorf("History channel content must be a JSON array")
	}

	// Must have exactly 1 content object.
	if len(contentArr) != 1 {
		return fmt.Errorf("History channel entries must contain exactly 1 content object")
	}

	// Parse the single content object.
	var obj map[string]any
	if err := json.Unmarshal(contentArr[0], &obj); err != nil {
		return fmt.Errorf("History channel content[0] must be a JSON object")
	}

	// Must have text, events, or attachments.
	_, hasText := obj["text"]
	_, hasEvents := obj["events"]
	_, hasAttachments := obj["attachments"]
	if !hasText && !hasEvents && !hasAttachments {
		return fmt.Errorf("History channel content must have at least one of 'text', 'events', or 'attachments'")
	}

	// Validate role if present.
	if roleVal, ok := obj["role"]; ok {
		role, _ := roleVal.(string)
		role = strings.ToUpper(role)
		if role != "USER" && role != "AI" && role != "SYSTEM" {
			return fmt.Errorf("History channel content must have a 'role' field with value 'USER' or 'AI'")
		}
	}

	// Validate attachments if present.
	if hasAttachments {
		attachRaw, ok := obj["attachments"]
		if !ok {
			return nil
		}

		// Check if attachments is an array.
		attachJSON, err := json.Marshal(attachRaw)
		if err != nil {
			return fmt.Errorf("History channel 'attachments' field must be an array")
		}

		var attachments []json.RawMessage
		if err := json.Unmarshal(attachJSON, &attachments); err != nil {
			return fmt.Errorf("History channel 'attachments' field must be an array")
		}

		for i, raw := range attachments {
			var att map[string]any
			if err := json.Unmarshal(raw, &att); err != nil {
				return fmt.Errorf("History channel attachment at index %d must be a JSON object", i)
			}

			_, hasHref := att["href"]
			_, hasAttachmentID := att["attachmentId"]
			if !hasHref && !hasAttachmentID {
				return fmt.Errorf("History channel attachment at index %d must have an 'href' or 'attachmentId' field", i)
			}

			// contentType is required for href attachments, optional for attachmentId
			// (it's already stored on the attachment record)
			if hasHref {
				if _, hasCT := att["contentType"]; !hasCT {
					return fmt.Errorf("History channel attachment at index %d must have a 'contentType' field", i)
				}
			}
		}
	}

	return nil
}

// resolveAttachmentRefs scans content JSON for attachmentId references,
// validates access, creates new attachment records for cross-references,
// and replaces attachmentId with href. Returns modified content (or nil if unchanged),
// the list of attachment IDs to link, and any error.
func resolveAttachmentRefs(ctx context.Context, store registrystore.MemoryStore, userID string, convID uuid.UUID, content json.RawMessage) (json.RawMessage, []uuid.UUID, error) {
	var contentArr []map[string]any
	if err := json.Unmarshal(content, &contentArr); err != nil {
		return nil, nil, nil // Not a JSON array, nothing to resolve
	}

	modified := false
	var linkedIDs []uuid.UUID

	for ci, contentObj := range contentArr {
		attachmentsRaw, ok := contentObj["attachments"]
		if !ok {
			continue
		}
		attachmentsJSON, err := json.Marshal(attachmentsRaw)
		if err != nil {
			continue
		}
		var attachments []map[string]any
		if err := json.Unmarshal(attachmentsJSON, &attachments); err != nil {
			continue
		}

		for ai, att := range attachments {
			attachmentIDStr, ok := att["attachmentId"].(string)
			if !ok {
				continue
			}
			attachmentID, err := uuid.Parse(attachmentIDStr)
			if err != nil {
				continue
			}

			// Look up the attachment. First try as user (for unlinked attachments they own).
			attachment, err := store.GetAttachment(ctx, userID, uuid.Nil, attachmentID)
			if err != nil {
				// Could be deleted or forbidden
				var notFound *registrystore.NotFoundError
				var forbidden *registrystore.ForbiddenError
				if errors.As(err, &notFound) {
					return nil, nil, &attachmentRefError{code: http.StatusNotFound, message: fmt.Sprintf("attachment %s not found", attachmentIDStr)}
				}
				if errors.As(err, &forbidden) {
					return nil, nil, &attachmentRefError{code: http.StatusForbidden, message: fmt.Sprintf("access denied to attachment %s", attachmentIDStr)}
				}
				return nil, nil, err
			}

			// If the attachment is linked to an entry, validate it belongs to the same conversation group.
			if attachment.EntryID != nil {
				// Look up the target conversation to get its group ID.
				conv, err := store.GetConversation(ctx, userID, convID)
				if err != nil {
					return nil, nil, err
				}

				// Look up the source entry's conversation group ID.
				sourceGroupID, err := store.GetEntryGroupID(ctx, *attachment.EntryID)
				if err != nil {
					return nil, nil, &attachmentRefError{code: http.StatusNotFound, message: fmt.Sprintf("attachment %s not found", attachmentIDStr)}
				}

				// Cross-group references are forbidden.
				if sourceGroupID != conv.ConversationGroupID {
					return nil, nil, &attachmentRefError{
						code:    http.StatusForbidden,
						message: fmt.Sprintf("attachment %s belongs to a different conversation group", attachmentIDStr),
					}
				}

				// Same group — create a new attachment record sharing the same storage key.
				newAttachment, err := store.CreateAttachment(ctx, userID, uuid.Nil, model.Attachment{
					StorageKey:  attachment.StorageKey,
					Filename:    attachment.Filename,
					ContentType: attachment.ContentType,
					Size:        attachment.Size,
					SHA256:      attachment.SHA256,
					Status:      "ready",
					ExpiresAt:   attachment.ExpiresAt,
				})
				if err != nil {
					return nil, nil, err
				}
				linkedIDs = append(linkedIDs, newAttachment.ID)
				att["href"] = "/v1/attachments/" + newAttachment.ID.String()
			} else {
				// Unlinked attachment — link directly.
				linkedIDs = append(linkedIDs, attachmentID)
				att["href"] = "/v1/attachments/" + attachmentID.String()
			}

			// Backfill contentType and name from the attachment record if not already set.
			if _, hasCT := att["contentType"]; !hasCT {
				att["contentType"] = attachment.ContentType
			}
			if _, hasName := att["name"]; !hasName && attachment.Filename != nil {
				att["name"] = *attachment.Filename
			}

			// Remove attachmentId from the response content.
			delete(att, "attachmentId")
			attachments[ai] = att
			modified = true
		}
		contentObj["attachments"] = attachments
		contentArr[ci] = contentObj
	}

	if !modified {
		return nil, nil, nil
	}

	modifiedJSON, err := json.Marshal(contentArr)
	if err != nil {
		return nil, nil, err
	}
	return modifiedJSON, linkedIDs, nil
}

type attachmentRefError struct {
	code    int
	message string
}

func (e *attachmentRefError) Error() string { return e.message }

func handleAttachmentError(c *gin.Context, err error) {
	var refErr *attachmentRefError
	if errors.As(err, &refErr) {
		c.JSON(refErr.code, gin.H{"error": refErr.message})
		return
	}
	handleError(c, err)
}

func syncMemory(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	var req registrystore.CreateEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	clientID := security.GetClientID(c)
	if clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "X-Client-ID header required for sync"})
		return
	}

	// userId validation for sync.
	if req.UserID != nil && *req.UserID != "" && *req.UserID != userID {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "validation_error",
			"details": gin.H{"message": "userId does not match the authenticated user"},
		})
		return
	}

	result, err := store.SyncAgentEntry(c.Request.Context(), userID, convID, req, clientID)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func handleError(c *gin.Context, err error) {
	var notFound *registrystore.NotFoundError
	var validation *registrystore.ValidationError
	var conflict *registrystore.ConflictError
	var forbidden *registrystore.ForbiddenError

	switch {
	case errors.As(err, &notFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": err.Error()})
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
	case errors.As(err, &conflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.As(err, &forbidden):
		c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

func queryPtr(c *gin.Context, key string) *string {
	v := c.Query(key)
	if v == "" {
		return nil
	}
	return &v
}

func queryInt(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}
