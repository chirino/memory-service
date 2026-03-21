package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---- SSE session tracking ----

// nodeID uniquely identifies this server instance. Generated once at startup.
var nodeID = uuid.New().String()

// sseSession represents a tracked SSE connection, either local or remote.
type sseSession struct {
	ConnectionID string    // unique ID for this connection
	UserID       string    // owning user
	NodeID       string    // which node owns this connection
	CreatedAt    time.Time // when the connection was established
	evicted      chan struct{}
}

// sseSessionTracker maintains an eventually-consistent view of all SSE sessions
// across nodes, populated from internal bus events.
type sseSessionTracker struct {
	mu       sync.Mutex
	sessions map[string]*sseSession // connectionID -> session
}

var sessionTracker = &sseSessionTracker{
	sessions: make(map[string]*sseSession),
}

// trackSession adds a session to the tracker.
func (t *sseSessionTracker) trackSession(s *sseSession) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessions[s.ConnectionID] = s
}

// removeSession removes a session from the tracker.
func (t *sseSessionTracker) removeSession(connectionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, connectionID)
}

// countForUser returns the total number of known sessions for a user.
func (t *sseSessionTracker) countForUser(userID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, s := range t.sessions {
		if s.UserID == userID {
			count++
		}
	}
	return count
}

// evictOldestLocal finds the oldest local session for the given user and
// signals it to close. The session is removed from the tracker so it won't
// be selected again. Returns true if a session was evicted.
func (t *sseSessionTracker) evictOldestLocal(userID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	var oldest *sseSession
	for _, s := range t.sessions {
		if s.UserID == userID && s.NodeID == nodeID && s.evicted != nil {
			if oldest == nil || s.CreatedAt.Before(oldest.CreatedAt) {
				oldest = s
			}
		}
	}
	if oldest != nil {
		delete(t.sessions, oldest.ConnectionID)
		close(oldest.evicted)
		return true
	}
	return false
}

// handleSessionEvent processes internal session bus events to maintain the
// cross-node view of active SSE sessions.
func (t *sseSessionTracker) handleSessionEvent(event registryeventbus.Event) {
	data, ok := event.Data.(map[string]any)
	if !ok {
		return
	}

	connID, _ := data["connection"].(string)
	if connID == "" {
		return
	}

	switch event.Event {
	case "created":
		userID, _ := data["user"].(string)
		eventNodeID, _ := data["node"].(string)
		createdAtStr, _ := data["createdAt"].(string)
		createdAt, _ := time.Parse(time.RFC3339Nano, createdAtStr)
		if createdAt.IsZero() {
			createdAt = time.Now()
		}

		// Only track remote sessions here; local sessions are tracked directly.
		if eventNodeID != nodeID {
			t.mu.Lock()
			t.sessions[connID] = &sseSession{
				ConnectionID: connID,
				UserID:       userID,
				NodeID:       eventNodeID,
				CreatedAt:    createdAt,
			}
			t.mu.Unlock()
		}

	case "shutdown":
		eventNodeID, _ := data["node"].(string)
		// Only remove remote sessions here; local sessions are cleaned up directly.
		if eventNodeID != nodeID {
			t.removeSession(connID)
		}
	}
}

// ---- SSE event writing ----

// writeSSEEvent marshals an event and writes it as an SSE data line.
func writeSSEEvent(c *gin.Context, event registryeventbus.Event) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
	if security.EventBusDeliveredTotal != nil {
		security.EventBusDeliveredTotal.Inc()
	}
}

// ---- SSE handler ----

// HandleSSEEvents streams real-time events to an authenticated agent user via SSE.
func HandleSSEEvents(c *gin.Context, _ registrystore.MemoryStore, bus registryeventbus.EventBus, cfg *config.Config) {
	userID := security.GetUserID(c)

	// Register this session.
	session := &sseSession{
		ConnectionID: uuid.New().String(),
		UserID:       userID,
		NodeID:       nodeID,
		CreatedAt:    time.Now(),
		evicted:      make(chan struct{}),
	}
	sessionTracker.trackSession(session)
	log.Info("SSE connection opened", "connID", session.ConnectionID, "userID", userID, "sessions", sessionTracker.countForUser(userID))
	defer func() {
		sessionTracker.removeSession(session.ConnectionID)
		log.Info("SSE connection closed", "connID", session.ConnectionID, "userID", userID, "sessions", sessionTracker.countForUser(userID))
	}()

	// Publish session created event (internal — not forwarded to clients).
	if bus != nil {
		_ = bus.Publish(c.Request.Context(), registryeventbus.Event{
			Event:     "created",
			Kind:      "session",
			Internal:  true,
			Broadcast: true,
			Data: map[string]any{
				"connection": session.ConnectionID,
				"user":       session.UserID,
				"node":       session.NodeID,
				"createdAt":  session.CreatedAt.Format(time.RFC3339Nano),
			},
		})
	}

	// Publish session shutdown on exit.
	defer func() {
		if bus != nil {
			_ = bus.Publish(c.Request.Context(), registryeventbus.Event{
				Event:     "shutdown",
				Kind:      "session",
				Internal:  true,
				Broadcast: true,
				Data: map[string]any{
					"connection": session.ConnectionID,
					"user":       session.UserID,
					"node":       session.NodeID,
				},
			})
		}
	}()

	// Evict oldest local connections while the user exceeds the limit.
	// The count includes remote sessions (eventually consistent), so eviction
	// is best-effort but converges over time.
	for sessionTracker.countForUser(userID) > cfg.SSEMaxConnectionsPerUser {
		if !sessionTracker.evictOldestLocal(userID) {
			break // no more local sessions to evict
		}
	}

	if security.SSEConnectionsActive != nil {
		security.SSEConnectionsActive.Inc()
		defer security.SSEConnectionsActive.Dec()
	}

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
	sub, err := bus.Subscribe(c.Request.Context(), userID)
	if err != nil {
		log.Error("SSE subscribe failed", "err", err, "userID", userID)
		return
	}

	keepalive := time.NewTicker(cfg.SSEKeepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return

		case <-session.evicted:
			// A newer connection evicted this one — notify client, then close.
			log.Info("SSE connection evicted", "connID", session.ConnectionID, "userID", userID, "reason", "replaced by newer connection")
			writeSSEEvent(c, registryeventbus.Event{
				Event: "evicted",
				Kind:  "stream",
				Data:  map[string]string{"reason": "replaced by newer connection"},
			})
			return

		case <-keepalive.C:
			fmt.Fprintf(c.Writer, ": keepalive\n\n")
			c.Writer.Flush()

		case event, ok := <-sub:
			if !ok {
				// Channel closed — subscriber was evicted (slow consumer).
				log.Info("SSE connection evicted", "connID", session.ConnectionID, "userID", userID, "reason", "slow consumer")
				if security.EventBusSubscriberEvictionsTotal != nil {
					security.EventBusSubscriberEvictionsTotal.Inc()
				}
				writeSSEEvent(c, registryeventbus.Event{
					Event: "evicted",
					Kind:  "stream",
					Data:  map[string]string{"reason": "slow consumer"},
				})
				return
			}

			// Process internal session events to update cross-node session tracker.
			if event.Internal && event.Kind == "session" {
				sessionTracker.handleSessionEvent(event)
				continue
			}

			// Skip other internal events.
			if event.Internal {
				continue
			}

			// Apply kinds filter.
			if len(kindsFilter) > 0 && !kindsFilter[event.Kind] {
				continue
			}

			writeSSEEvent(c, event)
		}
	}
}
