package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/chirino/memory-service/internal/plugin/route/routetx"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
)

type checkpointRequest struct {
	ContentType string          `json:"contentType"`
	Value       json.RawMessage `json:"value"`
}

func HandleAdminGetCheckpoint(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAdminRole()) {
		return
	}
	adminGetCheckpoint(c, store)
}

func HandleAdminPutCheckpoint(c *gin.Context, store registrystore.MemoryStore) {
	if !runMiddlewares(c, security.RequireAdminRole()) {
		return
	}
	adminPutCheckpoint(c, store)
}

func adminGetCheckpoint(c *gin.Context, store registrystore.MemoryStore) {
	checkpoints, ok := store.(registrystore.AdminCheckpointStore)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "checkpoint storage unavailable"})
		return
	}
	clientID := strings.TrimSpace(c.Param("clientId"))
	if !allowCheckpointClient(c, clientID) {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}
	var checkpoint *registrystore.ClientCheckpoint
	err := routetx.MemoryRead(c, store, func(ctx context.Context) error {
		var err error
		checkpoint, err = checkpoints.AdminGetCheckpoint(ctx, clientID)
		return err
	})
	if err != nil {
		var notFound *registrystore.NotFoundError
		var validation *registrystore.ValidationError
		switch {
		case errors.As(err, &notFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		case errors.As(err, &validation):
			c.JSON(http.StatusBadRequest, gin.H{"error": validation.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, checkpoint)
}

func adminPutCheckpoint(c *gin.Context, store registrystore.MemoryStore) {
	checkpoints, ok := store.(registrystore.AdminCheckpointStore)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "checkpoint storage unavailable"})
		return
	}
	clientID := strings.TrimSpace(c.Param("clientId"))
	if !allowCheckpointClient(c, clientID) {
		c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
		return
	}
	var body checkpointRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	var checkpoint *registrystore.ClientCheckpoint
	err := routetx.MemoryWrite(c, store, func(ctx context.Context) error {
		var err error
		checkpoint, err = checkpoints.AdminPutCheckpoint(ctx, registrystore.ClientCheckpoint{
			ClientID:    clientID,
			ContentType: body.ContentType,
			Value:       body.Value,
		})
		return err
	})
	if err != nil {
		var notFound *registrystore.NotFoundError
		var validation *registrystore.ValidationError
		switch {
		case errors.As(err, &notFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "checkpoint not found"})
			return
		case errors.As(err, &validation):
			c.JSON(http.StatusBadRequest, gin.H{"error": validation.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, checkpoint)
}

func allowCheckpointClient(c *gin.Context, clientID string) bool {
	authenticatedClientID := strings.TrimSpace(security.GetClientID(c))
	return authenticatedClientID == "" || authenticatedClientID == clientID
}
