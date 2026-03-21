package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
)

// HandleAdminSSEEvents streams all (non-internal) events to an admin user via SSE.
// Requires a justification query parameter for audit purposes.
func HandleAdminSSEEvents(c *gin.Context, bus registryeventbus.EventBus, cfg *config.Config) {
	justification := strings.TrimSpace(c.Query("justification"))
	if justification == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "justification query parameter is required"})
		return
	}

	adminID := security.GetUserID(c)
	log.Info("Admin SSE stream opened", "adminID", adminID, "justification", justification)

	// Parse optional kinds filter.
	kindsFilter := make(map[string]bool)
	if raw := strings.TrimSpace(c.Query("kinds")); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				kindsFilter[k] = true
			}
		}
	}

	// Set SSE headers.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Flush()

	// Subscribe to the event bus.
	sub, err := bus.Subscribe(c.Request.Context(), "")
	if err != nil {
		log.Error("Admin SSE subscribe failed", "err", err, "adminID", adminID)
		return
	}

	keepalive := time.NewTicker(cfg.SSEKeepaliveInterval)
	defer keepalive.Stop()

	auditRelog := time.NewTicker(5 * time.Minute)
	defer auditRelog.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			log.Info("Admin SSE stream closed", "adminID", adminID, "justification", justification)
			return

		case <-keepalive.C:
			fmt.Fprintf(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()

		case <-auditRelog.C:
			log.Info("Admin SSE stream active", "adminID", adminID, "justification", justification)

		case event, ok := <-sub:
			if !ok {
				// Channel closed — subscriber was evicted (slow consumer).
				data, _ := json.Marshal(registryeventbus.Event{
					Event: "evicted",
					Kind:  "stream",
					Data:  map[string]string{"reason": "slow consumer"},
				})
				fmt.Fprintf(c.Writer, "data: %s\n\n", data)
				c.Writer.Flush()
				log.Info("Admin SSE stream evicted", "adminID", adminID, "justification", justification)
				return
			}

			// Skip internal events.
			if event.Internal {
				continue
			}

			// Apply kinds filter.
			if len(kindsFilter) > 0 && !kindsFilter[event.Kind] {
				continue
			}

			data, _ := json.Marshal(event)
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			c.Writer.Flush()
		}
	}
}
