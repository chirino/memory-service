package memberships

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type membershipResponse struct {
	ConversationID uuid.UUID         `json:"conversationId"`
	UserID         string            `json:"userId"`
	AccessLevel    model.AccessLevel `json:"accessLevel"`
	CreatedAt      time.Time         `json:"createdAt"`
}

func toMembershipResponse(conversationID uuid.UUID, membership model.ConversationMembership) membershipResponse {
	return membershipResponse{
		ConversationID: conversationID,
		UserID:         membership.UserID,
		AccessLevel:    membership.AccessLevel,
		CreatedAt:      membership.CreatedAt,
	}
}

// MountRoutes mounts membership routes.
func MountRoutes(r *gin.Engine, store registrystore.MemoryStore, auth gin.HandlerFunc) {
	g := r.Group("/v1", auth)

	g.GET("/conversations/:conversationId/memberships", func(c *gin.Context) {
		listMemberships(c, store)
	})
	g.POST("/conversations/:conversationId/memberships", func(c *gin.Context) {
		shareConversation(c, store)
	})
	g.PATCH("/conversations/:conversationId/memberships/:userId", func(c *gin.Context) {
		updateMembership(c, store)
	})
	g.DELETE("/conversations/:conversationId/memberships/:userId", func(c *gin.Context) {
		deleteMembership(c, store)
	})
}

// HandleListMemberships exposes the list memberships handler for wrapper-native adapters.
func HandleListMemberships(c *gin.Context, store registrystore.MemoryStore) {
	listMemberships(c, store)
}

// HandleShareConversation exposes the share conversation handler for wrapper-native adapters.
func HandleShareConversation(c *gin.Context, store registrystore.MemoryStore) {
	shareConversation(c, store)
}

// HandleUpdateMembership exposes the membership update handler for wrapper-native adapters.
func HandleUpdateMembership(c *gin.Context, store registrystore.MemoryStore) {
	updateMembership(c, store)
}

// HandleDeleteMembership exposes the membership delete handler for wrapper-native adapters.
func HandleDeleteMembership(c *gin.Context, store registrystore.MemoryStore) {
	deleteMembership(c, store)
}

func listMemberships(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	memberships, cursor, err := store.ListMemberships(c.Request.Context(), userID, convID, afterCursor, limit)
	if err != nil {
		handleError(c, err)
		return
	}
	resp := make([]membershipResponse, len(memberships))
	for i := range memberships {
		resp[i] = toMembershipResponse(convID, memberships[i])
	}
	c.JSON(http.StatusOK, gin.H{"data": resp, "afterCursor": cursor})
}

func shareConversation(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}

	var req struct {
		UserID      string            `json:"userId"      binding:"required"`
		AccessLevel model.AccessLevel `json:"accessLevel" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
		return
	}

	membership, err := store.ShareConversation(c.Request.Context(), userID, convID, req.UserID, req.AccessLevel)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toMembershipResponse(convID, *membership))
}

func updateMembership(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}
	memberUserID := c.Param("userId")

	var req struct {
		AccessLevel model.AccessLevel `json:"accessLevel" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	membership, err := store.UpdateMembership(c.Request.Context(), userID, convID, memberUserID, req.AccessLevel)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, toMembershipResponse(convID, *membership))
}

func deleteMembership(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}
	memberUserID := c.Param("userId")

	if err := store.DeleteMembership(c.Request.Context(), userID, convID, memberUserID); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
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
	var i int
	if n, _ := fmt.Sscanf(v, "%d", &i); n == 1 {
		return i
	}
	return def
}
