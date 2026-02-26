package transfers

import (
	"errors"
	"fmt"
	"net/http"

	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MountRoutes mounts ownership transfer routes.
func MountRoutes(r *gin.Engine, store registrystore.MemoryStore, auth gin.HandlerFunc) {
	g := r.Group("/v1", auth)

	g.GET("/ownership-transfers", func(c *gin.Context) {
		listTransfers(c, store)
	})
	g.POST("/ownership-transfers", func(c *gin.Context) {
		createTransfer(c, store)
	})
	g.GET("/ownership-transfers/:transferId", func(c *gin.Context) {
		getTransfer(c, store)
	})
	g.DELETE("/ownership-transfers/:transferId", func(c *gin.Context) {
		deleteTransfer(c, store)
	})
	g.POST("/ownership-transfers/:transferId/accept", func(c *gin.Context) {
		acceptTransfer(c, store)
	})
}

func listTransfers(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	role := c.DefaultQuery("role", "")
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	transfers, cursor, err := store.ListPendingTransfers(c.Request.Context(), userID, role, afterCursor, limit)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": transfers, "afterCursor": cursor})
}

func createTransfer(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	var req struct {
		ConversationId string `json:"conversationId" binding:"required"`
		NewOwnerUserId string `json:"newOwnerUserId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": err.Error()})
		return
	}
	convID, err := uuid.Parse(req.ConversationId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversationId"})
		return
	}

	transfer, err := store.CreateOwnershipTransfer(c.Request.Context(), userID, convID, req.NewOwnerUserId)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, transfer)
}

func getTransfer(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	transferID, err := uuid.Parse(c.Param("transferId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ownership transfer not found"})
		return
	}

	transfer, err := store.GetTransfer(c.Request.Context(), userID, transferID)
	if err != nil {
		handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, transfer)
}

func deleteTransfer(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	transferID, err := uuid.Parse(c.Param("transferId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ownership transfer not found"})
		return
	}

	if err := store.DeleteTransfer(c.Request.Context(), userID, transferID); err != nil {
		handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func acceptTransfer(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	transferID, err := uuid.Parse(c.Param("transferId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ownership transfer not found"})
		return
	}

	if err := store.AcceptTransfer(c.Request.Context(), userID, transferID); err != nil {
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
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, gin.H{"code": "bad_request", "error": validation.Message})
	case errors.As(err, &conflict):
		resp := gin.H{"error": conflict.Message}
		if conflict.Code != "" {
			resp["code"] = conflict.Code
		}
		for k, v := range conflict.Details {
			resp[k] = v
		}
		c.JSON(http.StatusConflict, resp)
	case errors.As(err, &forbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
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
