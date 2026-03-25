package memberships

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/plugin/route/routetx"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/service/eventstream"
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
		shareConversation(c, store, nil)
	})
	g.PATCH("/conversations/:conversationId/memberships/:userId", func(c *gin.Context) {
		updateMembership(c, store, nil)
	})
	g.DELETE("/conversations/:conversationId/memberships/:userId", func(c *gin.Context) {
		deleteMembership(c, store, nil)
	})
}

// HandleListMemberships exposes the list memberships handler for wrapper-native adapters.
func HandleListMemberships(c *gin.Context, store registrystore.MemoryStore) {
	listMemberships(c, store)
}

// HandleShareConversation exposes the share conversation handler for wrapper-native adapters.
func HandleShareConversation(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	shareConversation(c, store, eventBus)
}

// HandleUpdateMembership exposes the membership update handler for wrapper-native adapters.
func HandleUpdateMembership(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	updateMembership(c, store, eventBus)
}

// HandleDeleteMembership exposes the membership delete handler for wrapper-native adapters.
func HandleDeleteMembership(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	deleteMembership(c, store, eventBus)
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

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		memberships, cursor, err := store.ListMemberships(ctx, userID, convID, afterCursor, limit)
		if err != nil {
			return err
		}
		resp := make([]membershipResponse, len(memberships))
		for i := range memberships {
			resp[i] = toMembershipResponse(convID, memberships[i])
		}
		c.JSON(http.StatusOK, gin.H{"data": resp, "afterCursor": cursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func shareConversation(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
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

	var createdMembership *model.ConversationMembership
	var eventsToPublish []registryeventbus.Event
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		membership, err := store.ShareConversation(ctx, userID, convID, req.UserID, req.AccessLevel)
		if err != nil {
			return err
		}
		createdMembership = membership
		if membership != nil {
			events := []registryeventbus.Event{{
				Event: "created",
				Kind:  "membership",
				Data: map[string]any{
					"conversation_group": membership.ConversationGroupID,
					"user":               membership.UserID,
					"role":               membership.AccessLevel,
				},
				ConversationGroupID: membership.ConversationGroupID,
				UserIDs:             []string{membership.UserID},
			}}
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
		c.JSON(http.StatusCreated, toMembershipResponse(convID, *membership))
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if eventBus != nil && createdMembership != nil {
		if err := eventstream.PublishEvents(c.Request.Context(), store, eventBus, eventsToPublish...); err != nil {
			log.Warn("Failed to publish event", "err", err)
		}
	}
}

func updateMembership(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
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

	var updatedMembership *model.ConversationMembership
	var eventsToPublish []registryeventbus.Event
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		membership, err := store.UpdateMembership(ctx, userID, convID, memberUserID, req.AccessLevel)
		if err != nil {
			return err
		}
		updatedMembership = membership
		if membership != nil {
			events := []registryeventbus.Event{{
				Event: "updated",
				Kind:  "membership",
				Data: map[string]any{
					"conversation_group": membership.ConversationGroupID,
					"user":               membership.UserID,
					"role":               membership.AccessLevel,
				},
				ConversationGroupID: membership.ConversationGroupID,
				UserIDs:             []string{membership.UserID},
			}}
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
		c.JSON(http.StatusOK, toMembershipResponse(convID, *membership))
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if eventBus != nil && updatedMembership != nil {
		if err := eventstream.PublishEvents(c.Request.Context(), store, eventBus, eventsToPublish...); err != nil {
			log.Warn("Failed to publish event", "err", err)
		}
	}
}

func deleteMembership(c *gin.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus) {
	userID := security.GetUserID(c)
	convID, err := uuid.Parse(c.Param("conversationId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "not_found", "error": "conversation not found"})
		return
	}
	memberUserID := c.Param("userId")

	// Fetch conversation before deletion to capture the group ID for the event.
	var groupID uuid.UUID
	var eventsToPublish []registryeventbus.Event
	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		conv, err := store.GetConversation(ctx, userID, convID)
		if err != nil {
			return err
		}
		groupID = conv.ConversationGroupID
		if err := store.DeleteMembership(ctx, userID, convID, memberUserID); err != nil {
			return err
		}
		events := []registryeventbus.Event{{
			Event: "deleted",
			Kind:  "membership",
			Data: map[string]any{
				"conversation_group": groupID,
				"user":               memberUserID,
			},
			ConversationGroupID: groupID,
			UserIDs:             []string{memberUserID},
		}}
		appended, used, err := eventstream.AppendOutboxEvents(ctx, store, events...)
		if err != nil {
			return err
		}
		if used {
			eventsToPublish = appended
		} else {
			eventsToPublish = events
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
		return
	}
	if eventBus != nil {
		if err := eventstream.PublishEvents(c.Request.Context(), store, eventBus, eventsToPublish...); err != nil {
			log.Warn("Failed to publish event", "err", err)
		}
	}
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
