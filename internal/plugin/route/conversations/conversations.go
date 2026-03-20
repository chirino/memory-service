package conversations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/route/routetx"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registryroute "github.com/chirino/memory-service/internal/registry/route"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	internalresumer "github.com/chirino/memory-service/internal/resumer"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() {
	registryroute.Register(registryroute.Plugin{
		Order: 100,
		Loader: func(r *gin.Engine) error {
			return nil // routes are mounted by the serve command after store init
		},
	})
}

// MountRoutes mounts conversation routes on the given router group.
// Called after store initialization so the store is available.
func MountRoutes(r *gin.Engine, store registrystore.MemoryStore, cfg *config.Config, auth gin.HandlerFunc, resumer *internalresumer.Store, resumerEnabled bool) {
	clientID := security.ClientIDMiddleware()

	g := r.Group("/v1", auth, clientID)

	g.GET("/conversations", func(c *gin.Context) {
		listConversations(c, store)
	})
	g.POST("/conversations", func(c *gin.Context) {
		createConversation(c, store, nil)
	})
	g.GET("/conversations/:conversationId", func(c *gin.Context) {
		getConversation(c, store)
	})
	g.PATCH("/conversations/:conversationId", func(c *gin.Context) {
		updateConversation(c, store, nil)
	})
	g.DELETE("/conversations/:conversationId", func(c *gin.Context) {
		deleteConversation(c, store, nil)
	})
	g.GET("/conversations/:conversationId/forks", func(c *gin.Context) {
		listForks(c, store)
	})
	g.DELETE("/conversations/:conversationId/response", func(c *gin.Context) {
		cancelResponse(c, store, resumer, resumerEnabled)
	})
}

// HandleListConversations exposes list conversation handling for wrapper-native adapters.
func HandleListConversations(c *gin.Context, store registrystore.MemoryStore) {
	listConversations(c, store)
}

// HandleCreateConversation exposes create conversation handling for wrapper-native adapters.
func HandleCreateConversation(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	createConversation(c, store, eventBus)
}

// HandleGetConversation exposes get conversation handling for wrapper-native adapters.
func HandleGetConversation(c *gin.Context, store registrystore.MemoryStore) {
	getConversation(c, store)
}

// HandleUpdateConversation exposes update conversation handling for wrapper-native adapters.
func HandleUpdateConversation(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	updateConversation(c, store, eventBus)
}

// HandleDeleteConversation exposes delete conversation handling for wrapper-native adapters.
func HandleDeleteConversation(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	deleteConversation(c, store, eventBus)
}

// HandleListForks exposes list forks handling for wrapper-native adapters.
func HandleListForks(c *gin.Context, store registrystore.MemoryStore) {
	listForks(c, store)
}

// HandleCancelResponse exposes cancel response handling for wrapper-native adapters.
func HandleCancelResponse(c *gin.Context, store registrystore.MemoryStore, resumer *internalresumer.Store, resumerEnabled bool) {
	cancelResponse(c, store, resumer, resumerEnabled)
}

func listConversations(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	mode := model.ConversationListMode(c.DefaultQuery("mode", "latest-fork"))
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)
	query := queryPtr(c, "query")

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		summaries, cursor, err := store.ListConversations(ctx, userID, query, afterCursor, limit, mode)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": summaries, "afterCursor": cursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func createConversation(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	userID := security.GetUserID(c)
	var req struct {
		ID                     *string                `json:"id"`
		Title                  string                 `json:"title"`
		Metadata               map[string]interface{} `json:"metadata"`
		ForkedAtConversationId *string                `json:"forkedAtConversationId"`
		ForkedAtEntryId        *string                `json:"forkedAtEntryId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Title) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "title exceeds maximum length"})
		return
	}

	var convID *uuid.UUID
	if req.ID != nil {
		id, err := uuid.Parse(*req.ID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
			return
		}
		convID = &id
	}

	var forkConvID, forkEntryID *uuid.UUID
	if req.ForkedAtConversationId != nil {
		id, err := uuid.Parse(*req.ForkedAtConversationId)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid forkedAtConversationId"})
			return
		}
		forkConvID = &id
	}
	if req.ForkedAtEntryId != nil {
		id, err := uuid.Parse(*req.ForkedAtEntryId)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid forkedAtEntryId"})
			return
		}
		forkEntryID = &id
	}

	var createdConv *registrystore.ConversationDetail
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		var (
			conv *registrystore.ConversationDetail
			err  error
		)
		if convID != nil {
			conv, err = store.CreateConversationWithID(ctx, userID, *convID, req.Title, req.Metadata, forkConvID, forkEntryID)
		} else {
			conv, err = store.CreateConversation(ctx, userID, req.Title, req.Metadata, forkConvID, forkEntryID)
		}
		if err != nil {
			return err
		}
		createdConv = conv
		// Java parity: fork creation returns 200, regular creation returns 201.
		if forkConvID != nil {
			c.JSON(http.StatusOK, conv)
		} else {
			c.JSON(http.StatusCreated, conv)
		}
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if eventBus != nil && createdConv != nil {
		if err := eventBus.Publish(c.Request.Context(), registryeventbus.Event{
			Event: "created",
			Kind:  "conversation",
			Data: map[string]any{
				"conversation":       createdConv.ID,
				"conversation_group": createdConv.ConversationGroupID,
			},
			ConversationGroupID: createdConv.ConversationGroupID,
		}); err != nil {
			log.Warn("Failed to publish event", "err", err)
		}
	}
}

func getConversation(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		conv, err := store.GetConversation(ctx, userID, convID)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, conv)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func updateConversation(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var title *string
	if data, ok := raw["title"]; ok {
		trimmed := bytes.TrimSpace(data)
		if bytes.Equal(trimmed, []byte("null")) {
			empty := ""
			title = &empty
		} else {
			var value string
			if err := json.Unmarshal(data, &value); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid title"})
				return
			}
			if len(value) > 500 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "title exceeds maximum length"})
				return
			}
			title = &value
		}
	}
	var metadata map[string]interface{}
	if data, ok := raw["metadata"]; ok {
		if err := json.Unmarshal(data, &metadata); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid metadata"})
			return
		}
	}

	var updatedConv *registrystore.ConversationDetail
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		conv, err := store.UpdateConversation(ctx, userID, convID, title, metadata)
		if err != nil {
			return err
		}
		updatedConv = conv
		c.JSON(http.StatusOK, conv)
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if eventBus != nil && updatedConv != nil {
		if err := eventBus.Publish(c.Request.Context(), registryeventbus.Event{
			Event: "updated",
			Kind:  "conversation",
			Data: map[string]any{
				"conversation":       updatedConv.ID,
				"conversation_group": updatedConv.ConversationGroupID,
			},
			ConversationGroupID: updatedConv.ConversationGroupID,
		}); err != nil {
			log.Warn("Failed to publish event", "err", err)
		}
	}
}

func deleteConversation(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	var groupID uuid.UUID
	var memberUserIDs []string
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		// Fetch conversation and members before deletion — memberships are
		// hard-deleted in the same transaction, so we must capture them first.
		conv, err := store.GetConversation(ctx, userID, convID)
		if err != nil {
			return err
		}
		groupID = conv.ConversationGroupID
		memberUserIDs, _ = store.GetGroupMemberUserIDs(ctx, groupID)
		if err := store.DeleteConversation(ctx, userID, convID); err != nil {
			return err
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if eventBus != nil {
		if err := eventBus.Publish(c.Request.Context(), registryeventbus.Event{
			Event: "deleted",
			Kind:  "conversation",
			Data: map[string]any{
				"conversation":       convID,
				"conversation_group": groupID,
				"members":            memberUserIDs,
			},
			ConversationGroupID: groupID,
		}); err != nil {
			log.Warn("Failed to publish event", "err", err)
		}
	}
}

func listForks(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		forks, cursor, err := store.ListForks(ctx, userID, convID, afterCursor, limit)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": forks, "afterCursor": cursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func cancelResponse(c *gin.Context, store registrystore.MemoryStore, resumer *internalresumer.Store, resumerEnabled bool) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		conv, err := store.GetConversation(ctx, userID, convID)
		if err != nil {
			return err
		}
		if !conv.AccessLevel.IsAtLeast(model.AccessLevelWriter) {
			handleError(c, &registrystore.ForbiddenError{})
			return nil
		}
		if !resumerEnabled {
			c.JSON(http.StatusConflict, gin.H{"error": "response recording disabled"})
			return nil
		}
		if _, err := resumer.RequestCancelWithAddress(ctx, convID.String(), ""); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return nil
		}

		// Contract expects 200 when cancellation request is accepted.
		c.Status(http.StatusOK)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

// --- Helpers ---

func handleError(c *gin.Context, err error) {
	var notFound *registrystore.NotFoundError
	var validation *registrystore.ValidationError
	var conflict *registrystore.ConflictError
	var forbidden *registrystore.ForbiddenError

	switch {
	case errors.As(err, &notFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": err.Error()})
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error(), "field": validation.Field})
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
	var i int
	if _, err := fmt.Sscanf(v, "%d", &i); err != nil {
		return def
	}
	return i
}
