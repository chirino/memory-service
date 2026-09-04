package entries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/route/routetx"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/service/eventstream"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HandleListEntries exposes list entries handling for wrapper-native adapters.
func HandleListEntries(c *gin.Context, store registrystore.MemoryStore) {
	listEntries(c, store)
}

// HandleAppendEntry exposes append entries handling for wrapper-native adapters.
func HandleAppendEntry(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	appendEntry(c, store, eventBus)
}

// HandleSyncMemory exposes sync-context handling for wrapper-native adapters.
func HandleSyncMemory(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	syncMemory(c, store, eventBus)
}

func listEntries(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID := strings.TrimSpace(c.Param("conversationId"))
	if convID == "" {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	afterCursor := queryPtr(c, "afterCursor")
	if afterCursor != nil {
		if _, err := uuid.Parse(*afterCursor); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid afterCursor: must be a UUID"})
			return
		}
	}
	beforeCursor := queryPtr(c, "beforeCursor")
	if beforeCursor != nil {
		if _, err := uuid.Parse(*beforeCursor); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid beforeCursor: must be a UUID"})
			return
		}
	}
	var tail bool
	if tailStr := strings.TrimSpace(c.Query("tail")); tailStr != "" {
		var err error
		tail, err = strconv.ParseBool(tailStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tail value: must be true or false"})
			return
		}
	}
	upToEntryID := queryPtr(c, "upToEntryId")
	if upToEntryID != nil {
		if _, err := uuid.Parse(*upToEntryID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upToEntryId: must be a UUID"})
			return
		}
	}
	limit := queryInt(c, "limit", 50)

	// Mutually exclusive pagination controls.
	paginationCount := 0
	if afterCursor != nil {
		paginationCount++
	}
	if beforeCursor != nil {
		paginationCount++
	}
	if tail {
		paginationCount++
	}
	if paginationCount > 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "afterCursor, beforeCursor, and tail are mutually exclusive"})
		return
	}

	var clientIDParam *string
	if clientID := security.GetClientID(c); clientID != "" {
		clientIDParam = &clientID
	}
	agentIDParam := queryPtr(c, "agentId")

	// Determine channel filter.
	var channelPtr *model.Channel
	channelQueryRaw := strings.TrimSpace(strings.ToLower(c.Query("channel")))
	if channelQueryRaw != "" {
		switch model.Channel(channelQueryRaw) {
		case model.ChannelHistory, model.ChannelContext, model.ChannelJournal:
			ch := model.Channel(channelQueryRaw)
			channelPtr = &ch
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel"})
			return
		}
	}

	// Java parity: when no channel is specified and no authenticated client id
	// exists, default to history only. Authenticated agent callers see all channels.
	if channelPtr == nil && clientIDParam == nil {
		ch := model.ChannelHistory
		channelPtr = &ch
	}

	// Explicit context/journal channel requests without a client id are forbidden.
	// (The implicit default-to-history path above already handled the no-channel-no-client case.)
	if channelPtr != nil && (*channelPtr == model.ChannelContext || *channelPtr == model.ChannelJournal) && clientIDParam == nil {
		c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": "channel requires an authenticated client id"})
		return
	}
	allForks := strings.EqualFold(c.DefaultQuery("forks", "none"), "all")

	var epochFilter *registrystore.MemoryEpochFilter
	if channelPtr != nil && *channelPtr == model.ChannelContext {
		filter, err := registrystore.ParseMemoryEpochFilter(c.Query("epoch"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		epochFilter = filter
	}

	// Parse fromSeq parameter.
	var fromSeq *uint32
	if fromSeqStr := c.Query("fromSeq"); fromSeqStr != "" {
		parsed, err := strconv.ParseUint(fromSeqStr, 10, 32)
		if err != nil || parsed > 4294967295 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid fromSeq: must be an integer between 0 and 4294967295"})
			return
		}
		v := uint32(parsed)
		fromSeq = &v
	}

	// Parse createdAt date filters.
	var createdAtFilter *registrystore.CreatedAtFilter
	createdAtAfterStr := strings.TrimSpace(c.Query("createdAtAfter"))
	createdAtBeforeStr := strings.TrimSpace(c.Query("createdAtBefore"))
	createdAtStr := strings.TrimSpace(c.Query("createdAt"))

	if createdAtStr != "" && (createdAtAfterStr != "" || createdAtBeforeStr != "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "createdAt is mutually exclusive with createdAtAfter and createdAtBefore"})
		return
	}

	if createdAtStr != "" {
		t, err := parseTimestamp(createdAtStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid createdAt: must be a valid RFC 3339 datetime string"})
			return
		}
		createdAtFilter = &registrystore.CreatedAtFilter{Eq: &t}
	} else if createdAtAfterStr != "" || createdAtBeforeStr != "" {
		f := &registrystore.CreatedAtFilter{}
		if createdAtAfterStr != "" {
			t, err := parseTimestamp(createdAtAfterStr)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid createdAtAfter: must be a valid RFC 3339 datetime string"})
				return
			}
			f.After = &t
		}
		if createdAtBeforeStr != "" {
			t, err := parseTimestamp(createdAtBeforeStr)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid createdAtBefore: must be a valid RFC 3339 datetime string"})
				return
			}
			f.Before = &t
		}
		createdAtFilter = f
	}

	if err := routetx.MemoryRead(c, store, func(context.Context) error {
		result, err := store.GetEntries(c.Request.Context(), userID, convID, registrystore.EntryListQuery{
			AfterCursor:     afterCursor,
			BeforeCursor:    beforeCursor,
			Tail:            tail,
			UpToEntryID:     upToEntryID,
			Limit:           limit,
			Channel:         channelPtr,
			EpochFilter:     epochFilter,
			ClientID:        clientIDParam,
			AgentID:         agentIDParam,
			AllForks:        allForks,
			FromSeq:         fromSeq,
			CreatedAtFilter: createdAtFilter,
		})
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": result.Data, "afterCursor": result.AfterCursor, "beforeCursor": result.BeforeCursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func appendEntry(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	userID := security.GetUserID(c)
	convID := strings.TrimSpace(c.Param("conversationId"))
	if convID == "" {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	var req struct {
		Entries []registrystore.CreateEntryRequest `json:"entries"`
		// Single entry mode
		Content                 json.RawMessage `json:"content"`
		ContentType             string          `json:"contentType"`
		Channel                 string          `json:"channel"`
		Epoch                   *int64          `json:"epoch"`
		IndexedContent          *string         `json:"indexedContent,omitempty"`
		Seq                     *uint32         `json:"seq,omitempty"`
		Role                    *string         `json:"role,omitempty"`
		UserID                  *string         `json:"userId,omitempty"`
		AgentID                 *string         `json:"agentId,omitempty"`
		ForkedAtConversationID  *string         `json:"forkedAtConversationId,omitempty"`
		ForkedAtEntryID         *uuid.UUID      `json:"forkedAtEntryId,omitempty"`
		StartedByConversationID *string         `json:"startedByConversationId,omitempty"`
		StartedByEntryID        *uuid.UUID      `json:"startedByEntryId,omitempty"`
		ConversationPatch       json.RawMessage `json:"conversationPatch,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	entries := req.Entries
	if len(entries) == 0 && len(req.Content) > 0 {
		entries = []registrystore.CreateEntryRequest{{
			Content:                 req.Content,
			ContentType:             req.ContentType,
			Channel:                 req.Channel,
			IndexedContent:          req.IndexedContent,
			Seq:                     req.Seq,
			Role:                    req.Role,
			UserID:                  req.UserID,
			AgentID:                 req.AgentID,
			ForkedAtConversationID:  req.ForkedAtConversationID,
			ForkedAtEntryID:         req.ForkedAtEntryID,
			StartedByConversationID: req.StartedByConversationID,
			StartedByEntryID:        req.StartedByEntryID,
		}}
	}
	if len(entries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one entry required"})
		return
	}

	// Validate conversationPatch before any datastore mutations.
	validatedPatch, err := validateConversationPatch(req.ConversationPatch)
	if err != nil {
		handleError(c, err)
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

	var clientID *string
	if cid := security.GetClientID(c); cid != "" {
		clientID = &cid
	}
	agentID := queryPtr(c, "agentId")

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
		if entry.AgentID != nil {
			agentID = entry.AgentID
		}

		// Context and journal channels require clientID.
		if (ch == model.ChannelContext || ch == model.ChannelJournal) && clientID == nil {
			c.JSON(http.StatusForbidden, gin.H{
				"code":    "forbidden",
				"error":   "client id is required for context/journal channel",
				"details": gin.H{"message": "client id is required for context/journal channel"},
			})
			return
		}
		if agentID != nil && clientID == nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "validation_error",
				"details": gin.H{"message": "agentId requires an authenticated client"},
			})
			return
		}
		if entry.AgentID != nil && agentID != nil && *entry.AgentID != *agentID {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "validation_error",
				"details": gin.H{"message": "all entries in a batch must use the same agentId"},
			})
			return
		}

		// Only history channel allows indexedContent.
		if ch != model.ChannelHistory && entry.IndexedContent != nil {
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
	var convExistedBefore bool
	var groupID uuid.UUID
	var result []model.Entry
	var eventsToPublish []registryeventbus.Event
	var retryCreatedAttachmentIDs []uuid.UUID
	retryRequest := registrystore.SequencedAppendRequest{
		Entries:  cloneCreateEntryRequests(entries),
		UserID:   userID,
		ClientID: clientID,
		AgentID:  agentID,
		Epoch:    req.Epoch,
	}
	retryEligible := len(entries) == 1 && entries[0].Seq != nil
	writeErr := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		// Check if conversation exists before append — if not, AppendEntries will auto-create it
		// and we need to publish a conversation/created event. Must be inside the transaction
		// scope for SQLite which requires InReadTx/InWriteTx.
		existingConv, _ := store.GetConversation(ctx, userID, convID)
		convExistedBefore = existingConv != nil

		// If the patch includes archived changes and the conversation exists, verify owner authorization
		// before any writes so MongoDB doesn't persist an entry before the authorization check fails.
		if validatedPatch != nil && validatedPatch.archived != nil && convExistedBefore {
			hasOwnerAccess := existingConv != nil && existingConv.AccessLevel == model.AccessLevelOwner
			if err := validateConversationPatchForOwnerOnly(validatedPatch, hasOwnerAccess); err != nil {
				return err
			}
		}

		// If conversationPatch requests unarchive (archived=false), set preHandledArchived=true.
		// Only archived conversations need the pre-write mutation; active and auto-created
		// conversations already satisfy the requested state.
		preHandledArchived := false
		patchAfterAppend := validatedPatch
		appendWhileArchived := false
		unarchiveChanged := false
		archiveChanged := false
		if validatedPatch != nil && validatedPatch.needsUnarchiveBeforeWrite() {
			if convExistedBefore && existingConv.ArchivedAt != nil {
				appendWhileArchived = true
				patchAfterAppend = validatedPatch.withoutArchived()
			} else {
				// archived=false is already satisfied. Keep applying title/metadata, but
				// do not report an archive mutation or emit an archive update event.
				patchAfterAppend = validatedPatch.withoutArchived()
			}
		}

		// Resolve attachmentId references inside the write scope so SQLite
		// attachment lookups and cross-link record creation see the required tx context.
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
			modified, links, createdIDs, err := resolveAttachmentRefs(ctx, store, userID, convID, entry.Content)
			if err != nil {
				return err
			}
			retryCreatedAttachmentIDs = append(retryCreatedAttachmentIDs, createdIDs...)
			if modified != nil {
				entries[i].Content = modified
			}
			for _, id := range links {
				pendingLinks = append(pendingLinks, pendingLink{attachmentID: id, entryIndex: i})
			}
		}

		var err error
		if appendWhileArchived {
			result, err = store.AppendEntriesBeforeUnarchive(ctx, userID, convID, entries, clientID, agentID, req.Epoch)
		} else {
			result, err = store.AppendEntries(ctx, userID, convID, entries, clientID, agentID, req.Epoch)
		}
		if err != nil {
			return err
		}
		if appendWhileArchived {
			unarchiveResult, err := store.UnarchiveConversationIfNeeded(ctx, userID, convID)
			if err != nil {
				return err
			}
			unarchiveChanged = unarchiveResult.Changed
		}
		if validatedPatch != nil && validatedPatch.archived != nil && *validatedPatch.archived {
			archiveResult, err := store.ArchiveConversationIfNeeded(ctx, userID, convID)
			if err != nil {
				return err
			}
			archiveChanged = archiveResult.Changed
			patchAfterAppend = validatedPatch.withoutArchived()
		}
		if patchAfterAppend != nil {
			current, err := store.GetConversation(ctx, userID, convID)
			if err != nil {
				return err
			}
			patchAfterAppend = patchAfterAppend.changesAgainst(current)
		}

		// Apply inline conversation patch if provided (title/metadata; archived was pre-handled).
		var patchResult conversationPatchResult
		if patchAfterAppend != nil {
			var err error
			patchResult, err = applyValidatedConversationPatch(ctx, store, userID, convID, patchAfterAppend, preHandledArchived)
			if err != nil {
				return err
			}
		}
		if unarchiveChanged {
			patchResult.changed = true
			patchResult.archived = validatedPatch.archived
		}
		if archiveChanged {
			patchResult.changed = true
			patchResult.archived = validatedPatch.archived
		}

		// Link attachments to created entries by updating entry_id.
		for _, link := range pendingLinks {
			if link.entryIndex < len(result) {
				entryID := result[link.entryIndex].ID
				if _, err := store.LinkAttachmentToEntry(ctx, userID, link.attachmentID, entryID); err != nil {
					return err
				}
			}
		}

		// Resolve group ID inside the transaction for SQLite compatibility.
		if eventBus != nil && len(result) > 0 {
			gid, gErr := store.GetEntryGroupID(ctx, result[0].ID)
			if gErr != nil {
				log.Warn("Failed to resolve group ID for entry event", "err", gErr)
			} else {
				groupID = gid
			}
		}
		if len(result) > 0 && groupID != uuid.Nil {
			events := make([]registryeventbus.Event, 0, len(result)+2)
			if !convExistedBefore {
				events = append(events, registryeventbus.Event{
					Event: "created",
					Kind:  "conversation",
					Data: map[string]any{
						"conversation":       convID,
						"conversation_group": groupID,
					},
					ConversationGroupID: groupID,
				})
			}
			for _, entry := range result {
				events = append(events, registryeventbus.Event{
					Event:               "created",
					Kind:                "entry",
					Data:                eventstream.EntryEventData(entry, groupID),
					ConversationGroupID: groupID,
				})
			}
			if patchResult.changed && convExistedBefore {
				if patchResult.archived != nil {
					memberIDs, _ := store.GetGroupMemberUserIDs(ctx, groupID)
					events = append(events, registryeventbus.Event{
						Event: "updated",
						Kind:  "conversation",
						Data: map[string]any{
							"conversation":       convID,
							"conversation_group": groupID,
							"members":            memberIDs,
							"archived":           *patchResult.archived,
						},
						ConversationGroupID: groupID,
						UserIDs:             memberIDs,
					})
				} else {
					events = append(events, registryeventbus.Event{
						Event: "updated",
						Kind:  "conversation",
						Data: map[string]any{
							"conversation":       convID,
							"conversation_group": groupID,
						},
						ConversationGroupID: groupID,
					})
				}
			}
			appended, used, err := eventstream.AppendOutboxEvents(ctx, store, events...)
			if err != nil {
				return err
			}
			if used {
				eventsToPublish = appended
			} else {
				eventsToPublish = events
			}
		}

		if len(result) == 1 {
			c.JSON(http.StatusCreated, result[0])
		} else {
			c.JSON(http.StatusCreated, result)
		}
		return nil
	})
	if writeErr != nil {
		duplicateConflict := registrystore.IsDuplicateSequenceConflict(writeErr)
		archivedRetry := retryEligible && validatedPatch != nil && validatedPatch.archived != nil && isNotFoundError(writeErr)
		if retryEligible && (duplicateConflict || archivedRetry) {
			originalErr := writeErr
			eventsToPublish = nil
			recoveryErr := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
				if err := cleanupRetryAttachments(ctx, store, userID, retryCreatedAttachmentIDs); err != nil {
					return err
				}
				normalizedRequest, err := normalizeRESTAppendRetry(ctx, store, userID, convID, retryRequest)
				if err != nil {
					return err
				}
				match, err := registrystore.FindSequencedAppendMatch(ctx, store, convID, normalizedRequest)
				if err != nil {
					return err
				}
				if !match.Exact {
					if match.AnyExisting {
						return registrystore.NewDuplicateSequenceConflict()
					}
					return originalErr
				}
				// Exact retries normally skip all append-time patches so stale title or
				// metadata cannot overwrite newer conversation state. Explicit
				// archived=false is the exception: it describes the caller's desired
				// current state and must unarchive a conversation archived after the
				// original append.
				if validatedPatch.needsUnarchiveBeforeWrite() {
					conv, err := store.GetConversation(ctx, userID, convID)
					if err != nil {
						return err
					}
					if conv.ArchivedAt != nil {
						unarchiveResult, err := store.UnarchiveConversationIfNeeded(ctx, userID, convID)
						if err != nil {
							return err
						}
						if unarchiveResult.Changed && eventBus != nil {
							eventsToPublish, err = conversationPatchEvents(ctx, store, convID, conv.ConversationGroupID, conversationPatchResult{
								changed:  true,
								archived: validatedPatch.archived,
							})
							if err != nil {
								return err
							}
						}
					}
				}
				for _, storedEntry := range match.Entries {
					if err := registrystore.RepairStoredEntryAttachmentLinks(ctx, store, userID, storedEntry); err != nil {
						return err
					}
				}
				result = match.Entries
				writeAppendResponse(c, result)
				return nil
			})
			if recoveryErr == nil {
				if eventBus != nil && len(eventsToPublish) > 0 {
					if err := eventstream.PublishEvents(c.Request.Context(), store, eventBus, eventsToPublish...); err != nil {
						log.Warn("Failed to publish append retry events", "err", err)
					}
				}
				return
			}
			writeErr = recoveryErr
		}
		if retryEligible && duplicateConflict && len(retryCreatedAttachmentIDs) > 0 {
			cleanupErr := store.InWriteTx(c.Request.Context(), func(ctx context.Context) error {
				return cleanupRetryAttachments(ctx, store, userID, retryCreatedAttachmentIDs)
			})
			if cleanupErr != nil {
				writeErr = cleanupErr
			}
		}
		var attachmentErr *attachmentRefError
		if errors.As(writeErr, &attachmentErr) {
			handleAttachmentError(c, writeErr)
			return
		}
		handleError(c, writeErr)
		return
	}
	// Publish events after successful transaction.
	if eventBus != nil && len(eventsToPublish) > 0 {
		if err := eventstream.PublishEvents(c.Request.Context(), store, eventBus, eventsToPublish...); err != nil {
			log.Warn("Failed to publish append events", "err", err)
		}
	}
}

func isNotFoundError(err error) bool {
	var notFound *registrystore.NotFoundError
	return errors.As(err, &notFound)
}

// parseTimestamp parses an RFC 3339 timestamp string, accepting both
// sub-second (RFC3339Nano) and whole-second (RFC3339) formats.
func parseTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
	}
	return t, err
}

func cloneCreateEntryRequests(entries []registrystore.CreateEntryRequest) []registrystore.CreateEntryRequest {
	cloned := append([]registrystore.CreateEntryRequest(nil), entries...)
	for i := range cloned {
		cloned[i].Content = append(json.RawMessage(nil), entries[i].Content...)
	}
	return cloned
}

func writeAppendResponse(c *gin.Context, entries []model.Entry) {
	if len(entries) == 1 {
		c.JSON(http.StatusCreated, entries[0])
		return
	}
	c.JSON(http.StatusCreated, entries)
}

func conversationPatchEvents(ctx context.Context, store registrystore.MemoryStore, convID string, groupID uuid.UUID, patch conversationPatchResult) ([]registryeventbus.Event, error) {
	var event registryeventbus.Event
	if patch.archived != nil {
		memberIDs, _ := store.GetGroupMemberUserIDs(ctx, groupID)
		event = registryeventbus.Event{
			Event: "updated",
			Kind:  "conversation",
			Data: map[string]any{
				"conversation":       convID,
				"conversation_group": groupID,
				"members":            memberIDs,
				"archived":           *patch.archived,
			},
			ConversationGroupID: groupID,
			UserIDs:             memberIDs,
		}
	} else {
		event = registryeventbus.Event{
			Event: "updated",
			Kind:  "conversation",
			Data: map[string]any{
				"conversation":       convID,
				"conversation_group": groupID,
			},
			ConversationGroupID: groupID,
		}
	}
	events := []registryeventbus.Event{event}
	appended, used, err := eventstream.AppendOutboxEvents(ctx, store, events...)
	if err != nil {
		return nil, err
	}
	if used {
		return appended, nil
	}
	return events, nil
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
// validates access, and creates new attachment records for cross-references.
// Returns modified content (or nil if unchanged),
// the list of attachment IDs to link, and any error.
func resolveAttachmentRefs(ctx context.Context, store registrystore.MemoryStore, userID string, convID string, content json.RawMessage) (json.RawMessage, []uuid.UUID, []uuid.UUID, error) {
	return resolveAttachmentRefsWithMode(ctx, store, userID, convID, content, true)
}

func resolveAttachmentRefsWithMode(ctx context.Context, store registrystore.MemoryStore, userID string, convID string, content json.RawMessage, createCrossReference bool) (json.RawMessage, []uuid.UUID, []uuid.UUID, error) {
	var contentArr []map[string]any
	if err := json.Unmarshal(content, &contentArr); err != nil {
		return nil, nil, nil, nil // Not a JSON array, nothing to resolve
	}

	modified := false
	var linkedIDs []uuid.UUID
	var createdIDs []uuid.UUID

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
			attachment, err := store.GetAttachment(ctx, userID, "", attachmentID)
			if err != nil {
				// Could be deleted or forbidden
				var notFound *registrystore.NotFoundError
				var forbidden *registrystore.ForbiddenError
				if errors.As(err, &notFound) {
					return nil, nil, nil, &attachmentRefError{code: http.StatusNotFound, message: fmt.Sprintf("attachment %s not found", attachmentIDStr)}
				}
				if errors.As(err, &forbidden) {
					return nil, nil, nil, &attachmentRefError{code: http.StatusForbidden, message: fmt.Sprintf("access denied to attachment %s", attachmentIDStr)}
				}
				return nil, nil, nil, err
			}

			// If the attachment is linked to an entry, validate it belongs to the same conversation group.
			if attachment.EntryID != nil {
				// Look up the target conversation to get its group ID.
				conv, err := store.GetConversation(ctx, userID, convID)
				if err != nil {
					return nil, nil, nil, err
				}

				// Look up the source entry's conversation group ID.
				sourceGroupID, err := store.GetEntryGroupID(ctx, *attachment.EntryID)
				if err != nil {
					return nil, nil, nil, &attachmentRefError{code: http.StatusNotFound, message: fmt.Sprintf("attachment %s not found", attachmentIDStr)}
				}

				// Cross-group references are forbidden.
				if sourceGroupID != conv.ConversationGroupID {
					return nil, nil, nil, &attachmentRefError{
						code:    http.StatusForbidden,
						message: fmt.Sprintf("attachment %s belongs to a different conversation group", attachmentIDStr),
					}
				}

				if !createCrossReference {
					if _, hasCT := att["contentType"]; !hasCT {
						att["contentType"] = attachment.ContentType
					}
					if _, hasName := att["name"]; !hasName && attachment.Filename != nil {
						att["name"] = *attachment.Filename
					}
					attachments[ai] = att
					modified = true
					continue
				}

				// Same group — create a new attachment record sharing the same storage key.
				newAttachment, err := store.CreateAttachment(ctx, userID, "", model.Attachment{
					StorageKey:  attachment.StorageKey,
					Filename:    attachment.Filename,
					ContentType: attachment.ContentType,
					Size:        attachment.Size,
					SHA256:      attachment.SHA256,
					Status:      "ready",
					ExpiresAt:   attachment.ExpiresAt,
				})
				if err != nil {
					return nil, nil, nil, err
				}
				linkedIDs = append(linkedIDs, newAttachment.ID)
				createdIDs = append(createdIDs, newAttachment.ID)
				att["attachmentId"] = newAttachment.ID.String()
			} else {
				// Unlinked attachment — link directly.
				linkedIDs = append(linkedIDs, attachmentID)
			}

			// Backfill contentType and name from the attachment record if not already set.
			if _, hasCT := att["contentType"]; !hasCT {
				att["contentType"] = attachment.ContentType
			}
			if _, hasName := att["name"]; !hasName && attachment.Filename != nil {
				att["name"] = *attachment.Filename
			}

			attachments[ai] = att
			modified = true
		}
		contentObj["attachments"] = attachments
		contentArr[ci] = contentObj
	}

	if !modified {
		return nil, nil, nil, nil
	}

	modifiedJSON, err := json.Marshal(contentArr)
	if err != nil {
		return nil, nil, nil, err
	}
	return modifiedJSON, linkedIDs, createdIDs, nil
}

func normalizeRESTAppendRetry(ctx context.Context, store registrystore.MemoryStore, userID, convID string, request registrystore.SequencedAppendRequest) (registrystore.SequencedAppendRequest, error) {
	request.Entries = cloneCreateEntryRequests(request.Entries)
	for i, entry := range request.Entries {
		if model.Channel(strings.ToLower(entry.Channel)) != model.ChannelHistory {
			continue
		}
		modified, _, _, err := resolveAttachmentRefsWithMode(ctx, store, userID, convID, entry.Content, false)
		if err != nil {
			return registrystore.SequencedAppendRequest{}, err
		}
		if modified != nil {
			request.Entries[i].Content = modified
		}
	}
	return request, nil
}

func cleanupRetryAttachments(ctx context.Context, store registrystore.MemoryStore, userID string, attachmentIDs []uuid.UUID) error {
	for _, attachmentID := range attachmentIDs {
		if err := store.DeleteAttachment(ctx, userID, "", attachmentID); err != nil {
			var notFound *registrystore.NotFoundError
			if errors.As(err, &notFound) {
				continue
			}
			return err
		}
	}
	return nil
}

type attachmentRefError struct {
	code    int
	message string
}

func (e *attachmentRefError) Error() string { return e.message }

func handleAttachmentError(c *gin.Context, err error) {
	var refErr *attachmentRefError
	if errors.As(err, &refErr) {
		_ = c.Error(err)
		c.JSON(refErr.code, gin.H{"error": refErr.message})
		return
	}
	handleError(c, err)
}

func syncMemory(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	userID := security.GetUserID(c)
	convID := strings.TrimSpace(c.Param("conversationId"))
	if convID == "" {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	var req struct {
		registrystore.CreateEntryRequest
		ConversationPatch json.RawMessage `json:"conversationPatch,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate conversationPatch before any datastore mutations.
	validatedPatch, err := validateConversationPatch(req.ConversationPatch)
	if err != nil {
		handleError(c, err)
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

	var eventsToPublish []registryeventbus.Event
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		existingConv, _ := store.GetConversation(ctx, userID, convID)
		convExistedBefore := existingConv != nil

		// If the patch includes archived changes and the conversation exists, verify owner authorization
		// before any writes so MongoDB doesn't persist an entry before the authorization check fails.
		if validatedPatch != nil && validatedPatch.archived != nil && convExistedBefore {
			hasOwnerAccess := existingConv != nil && existingConv.AccessLevel == model.AccessLevelOwner
			if err := validateConversationPatchForOwnerOnly(validatedPatch, hasOwnerAccess); err != nil {
				return err
			}
		}

		// If conversationPatch requests unarchive (archived=false), set preHandledArchived=true.
		// If conversation exists, call UnarchiveConversation; if auto-created, skip (already unarchived).
		preHandledArchived := false
		if validatedPatch != nil && validatedPatch.needsUnarchiveBeforeWrite() {
			preHandledArchived = true
			if convExistedBefore {
				if err := store.UnarchiveConversation(ctx, userID, convID); err != nil {
					return err
				}
			}
		}

		result, err := store.SyncAgentEntry(ctx, userID, convID, req.CreateEntryRequest, clientID, req.AgentID)
		if err != nil {
			return err
		}
		// Apply inline conversation patch if provided (title/metadata; archived was pre-handled).
		var patchResult conversationPatchResult
		if validatedPatch != nil {
			var patchErr error
			patchResult, patchErr = applyValidatedConversationPatch(ctx, store, userID, convID, validatedPatch, preHandledArchived)
			if patchErr != nil {
				return patchErr
			}
		}

		if eventBus != nil {
			var groupID uuid.UUID
			if result.Entry != nil {
				gid, err := store.GetEntryGroupID(ctx, result.Entry.ID)
				if err != nil {
					return err
				}
				groupID = gid
			} else if patchResult.changed && convExistedBefore {
				// No-op sync with a conversation patch: we need the group ID for the event.
				conv, err := store.GetConversation(ctx, userID, convID)
				if err != nil {
					return err
				}
				groupID = conv.ConversationGroupID
			}

			if result.Entry != nil || (patchResult.changed && convExistedBefore && groupID != uuid.Nil) {
				events := make([]registryeventbus.Event, 0, 3)
				if result.Entry != nil {
					if !convExistedBefore {
						events = append(events, registryeventbus.Event{
							Event: "created",
							Kind:  "conversation",
							Data: map[string]any{
								"conversation":       convID,
								"conversation_group": groupID,
							},
							ConversationGroupID: groupID,
						})
					}
					events = append(events, registryeventbus.Event{
						Event:               "created",
						Kind:                "entry",
						Data:                eventstream.EntryEventData(*result.Entry, groupID),
						ConversationGroupID: groupID,
					})
				}
				if patchResult.changed && convExistedBefore && groupID != uuid.Nil {
					if patchResult.archived != nil {
						memberIDs, _ := store.GetGroupMemberUserIDs(ctx, groupID)
						events = append(events, registryeventbus.Event{
							Event: "updated",
							Kind:  "conversation",
							Data: map[string]any{
								"conversation":       convID,
								"conversation_group": groupID,
								"members":            memberIDs,
								"archived":           *patchResult.archived,
							},
							ConversationGroupID: groupID,
							UserIDs:             memberIDs,
						})
					} else {
						events = append(events, registryeventbus.Event{
							Event: "updated",
							Kind:  "conversation",
							Data: map[string]any{
								"conversation":       convID,
								"conversation_group": groupID,
							},
							ConversationGroupID: groupID,
						})
					}
				}
				appended, used, err := eventstream.AppendOutboxEvents(ctx, store, events...)
				if err != nil {
					return err
				}
				if used {
					eventsToPublish = appended
				} else {
					eventsToPublish = events
				}
			}
		}
		c.JSON(http.StatusOK, result)
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if eventBus != nil && len(eventsToPublish) > 0 {
		if err := eventstream.PublishEvents(c.Request.Context(), store, eventBus, eventsToPublish...); err != nil {
			log.Warn("Failed to publish sync entry events", "err", err)
		}
	}
}

func handleError(c *gin.Context, err error) {
	_ = c.Error(err)
	var notFound *registrystore.NotFoundError
	var validation *registrystore.ValidationError
	var conflict *registrystore.ConflictError
	var forbidden *registrystore.ForbiddenError
	var badRequest *registrystore.BadRequestError

	switch {
	case errors.As(err, &notFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": err.Error()})
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
	case errors.As(err, &badRequest):
		c.JSON(http.StatusBadRequest, gin.H{"code": "bad_request", "error": err.Error()})
	case errors.As(err, &conflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.As(err, &forbidden):
		c.JSON(http.StatusForbidden, gin.H{"code": "forbidden", "error": err.Error()})
	default:
		log.Printf("[entries] internal error: %v", err)
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
		return config.ClampPageSize(c.Request.Context(), def)
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return config.ClampPageSize(c.Request.Context(), def)
	}
	return config.ClampPageSize(c.Request.Context(), i)
}

// conversationPatchResult carries the outcome of applyValidatedConversationPatch so
// callers can emit the correct SSE event.
type conversationPatchResult struct {
	changed  bool  // any field was actually written
	archived *bool // non-nil when the archived field was patched
}

// applyValidatedConversationPatch applies a pre-validated conversationPatch to the
// conversation inside the current write transaction. It supports title, metadata, and
// the archived field. Because ArchiveConversation/UnarchiveConversation use InWriteTx
// internally they safely compose with the caller's existing write scope.
//
// If archivedAlreadyHandled is true, the archived field in the patch is skipped (it was
// already applied before the entry write, e.g. to unarchive before AppendEntries).
func applyValidatedConversationPatch(ctx context.Context, store registrystore.MemoryStore, userID, convID string, patch *validatedConversationPatch, archivedAlreadyHandled bool) (conversationPatchResult, error) {
	if patch == nil {
		return conversationPatchResult{}, nil
	}

	// Handle archive/unarchive if requested (and not already handled by the caller).
	if patch.archived != nil && !archivedAlreadyHandled {
		if *patch.archived {
			if err := store.ArchiveConversation(ctx, userID, convID); err != nil {
				return conversationPatchResult{}, err
			}
		} else {
			if err := store.UnarchiveConversation(ctx, userID, convID); err != nil {
				return conversationPatchResult{}, err
			}
		}
		// If only archived was set, return now.
		if patch.title == nil && !patch.metadataPresent {
			return conversationPatchResult{changed: true, archived: patch.archived}, nil
		}
	}
	// If archived was already handled before this call, account for it in the result.
	if patch.archived != nil && archivedAlreadyHandled {
		if patch.title == nil && !patch.metadataPresent {
			return conversationPatchResult{changed: true, archived: patch.archived}, nil
		}
	}

	// Apply title and/or metadata merge-patch if provided.
	if patch.title != nil || patch.metadataPresent {
		if _, err := store.UpdateConversation(ctx, userID, convID, patch.title, patch.metadataPatch); err != nil {
			return conversationPatchResult{}, err
		}
	}

	// Return the archived value if archive was also part of this patch, otherwise signal updated.
	if patch.archived != nil {
		return conversationPatchResult{changed: true, archived: patch.archived}, nil
	}
	if patch.title != nil || patch.metadataPresent {
		return conversationPatchResult{changed: true}, nil
	}
	return conversationPatchResult{}, nil
}
