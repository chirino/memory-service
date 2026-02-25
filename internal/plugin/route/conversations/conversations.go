package conversations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
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
		createConversation(c, store)
	})
	g.GET("/conversations/:conversationId", func(c *gin.Context) {
		getConversation(c, store)
	})
	g.PATCH("/conversations/:conversationId", func(c *gin.Context) {
		updateConversation(c, store)
	})
	g.DELETE("/conversations/:conversationId", func(c *gin.Context) {
		deleteConversation(c, store)
	})
	g.GET("/conversations/:conversationId/forks", func(c *gin.Context) {
		listForks(c, store)
	})
	g.DELETE("/conversations/:conversationId/response", func(c *gin.Context) {
		cancelResponse(c, store, resumer, resumerEnabled)
	})
}

func listConversations(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	mode := model.ConversationListMode(c.DefaultQuery("mode", "latest-fork"))
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)
	query := queryPtr(c, "query")

	summaries, cursor, err := store.ListConversations(c.Request.Context(), userID, query, afterCursor, limit, mode)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": summaries, "afterCursor": cursor})
}

func createConversation(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	var req struct {
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

	conv, err := store.CreateConversation(c.Request.Context(), userID, req.Title, req.Metadata, forkConvID, forkEntryID)
	if err != nil {
		handleError(c, err)
		return
	}
	// Java parity: fork creation returns 200, regular creation returns 201.
	if forkConvID != nil {
		c.JSON(http.StatusOK, conv)
	} else {
		c.JSON(http.StatusCreated, conv)
	}
}

func getConversation(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	conv, err := store.GetConversation(c.Request.Context(), userID, convID)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, conv)
}

func updateConversation(c *gin.Context, store registrystore.MemoryStore) {
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

	conv, err := store.UpdateConversation(c.Request.Context(), userID, convID, title, metadata)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, conv)
}

func deleteConversation(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	if err := store.DeleteConversation(c.Request.Context(), userID, convID); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
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

	forks, cursor, err := store.ListForks(c.Request.Context(), userID, convID, afterCursor, limit)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": forks, "afterCursor": cursor})
}

func cancelResponse(c *gin.Context, store registrystore.MemoryStore, resumer *internalresumer.Store, resumerEnabled bool) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	conv, err := store.GetConversation(c.Request.Context(), userID, convID)
	if err != nil {
		handleError(c, err)
		return
	}
	if !conv.AccessLevel.IsAtLeast(model.AccessLevelWriter) {
		handleError(c, &registrystore.ForbiddenError{})
		return
	}
	if !resumerEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "response resumer disabled"})
		return
	}
	if _, err := resumer.RequestCancelWithAddress(c.Request.Context(), convID.String(), ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// Contract expects 200 when cancellation request is accepted.
	c.Status(http.StatusOK)
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
