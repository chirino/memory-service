package transfers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/chirino/memory-service/internal/plugin/route/routetx"
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

// HandleListTransfers exposes the transfer list handler for wrapper-native adapters.
func HandleListTransfers(c *gin.Context, store registrystore.MemoryStore) {
	listTransfers(c, store)
}

// HandleCreateTransfer exposes the transfer create handler for wrapper-native adapters.
func HandleCreateTransfer(c *gin.Context, store registrystore.MemoryStore) {
	createTransfer(c, store)
}

// HandleGetTransfer exposes the transfer fetch handler for wrapper-native adapters.
func HandleGetTransfer(c *gin.Context, store registrystore.MemoryStore) {
	getTransfer(c, store)
}

// HandleDeleteTransfer exposes the transfer delete/reject handler for wrapper-native adapters.
func HandleDeleteTransfer(c *gin.Context, store registrystore.MemoryStore) {
	deleteTransfer(c, store)
}

// HandleAcceptTransfer exposes the transfer accept handler for wrapper-native adapters.
func HandleAcceptTransfer(c *gin.Context, store registrystore.MemoryStore) {
	acceptTransfer(c, store)
}

func listTransfers(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	role := c.DefaultQuery("role", "")
	afterCursor := queryPtr(c, "afterCursor")
	limit := queryInt(c, "limit", 20)

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		transfers, cursor, err := store.ListPendingTransfers(ctx, userID, role, afterCursor, limit)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, gin.H{"data": transfers, "afterCursor": cursor})
		return nil
	}); err != nil {
		handleError(c, err)
	}
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

	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		transfer, err := store.CreateOwnershipTransfer(ctx, userID, convID, req.NewOwnerUserId)
		if err != nil {
			return err
		}
		c.JSON(http.StatusCreated, transfer)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func getTransfer(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	transferID, err := uuid.Parse(c.Param("transferId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ownership transfer not found"})
		return
	}

	if err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		transfer, err := store.GetTransfer(ctx, userID, transferID)
		if err != nil {
			return err
		}
		c.JSON(http.StatusOK, transfer)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func deleteTransfer(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	transferID, err := uuid.Parse(c.Param("transferId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ownership transfer not found"})
		return
	}

	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		if err := store.DeleteTransfer(ctx, userID, transferID); err != nil {
			return err
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
	}
}

func acceptTransfer(c *gin.Context, store registrystore.MemoryStore) {
	userID := security.GetUserID(c)
	transferID, err := uuid.Parse(c.Param("transferId"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ownership transfer not found"})
		return
	}

	if err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		if err := store.AcceptTransfer(ctx, userID, transferID); err != nil {
			return err
		}
		c.Status(http.StatusNoContent)
		return nil
	}); err != nil {
		handleError(c, err)
	}
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
