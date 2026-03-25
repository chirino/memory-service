package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/service/eventstream"
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

type replayOutcome int

const (
	replayOutcomeContinue replayOutcome = iota
	replayOutcomeClosed
	replayOutcomeRecover
)

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

func writeSSEPhaseEvent(c *gin.Context, phase string) {
	writeSSEEvent(c, registryeventbus.Event{
		Event: "phase",
		Kind:  "stream",
		Data:  map[string]string{"phase": phase},
	})
}

// ---- SSE handler ----

// HandleSSEEvents streams real-time events to an authenticated agent user via SSE.
func HandleSSEEvents(c *gin.Context, store registrystore.MemoryStore, bus registryeventbus.EventBus, cfg *config.Config) {
	userID := security.GetUserID(c)
	after := strings.TrimSpace(c.Query("after"))
	if after != "" && !cfg.OutboxEnabled {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "the after query parameter requires the event outbox to be enabled",
		})
		return
	}
	detail := strings.TrimSpace(c.DefaultQuery("detail", "summary"))
	if detail == "" {
		detail = "summary"
	}
	if detail != "summary" && detail != "full" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "detail must be one of: summary, full"})
		return
	}

	outbox, _ := store.(registrystore.EventOutboxStore)
	if after != "" && outbox == nil {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "durable event replay is not supported by the configured datastore",
		})
		return
	}
	if after != "" {
		if err := eventstream.ReplaySupported(c.Request.Context(), store, outbox); err != nil {
			if errors.Is(err, registrystore.ErrOutboxReplayUnsupported) {
				c.JSON(http.StatusNotImplemented, gin.H{
					"error": "durable event replay is not supported by the configured datastore",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize event replay"})
			return
		}
	}

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

	keepalive := time.NewTicker(cfg.SSEKeepaliveInterval)
	defer keepalive.Stop()
	lastCursor := ""
	resumeCursor := after
	replayChecked := after != ""
	replayAvailable := after != ""

	canRecoverSlowConsumer := func() bool {
		if !cfg.OutboxEnabled || outbox == nil || lastCursor == "" {
			return false
		}
		if replayChecked {
			return replayAvailable
		}
		if err := eventstream.ReplaySupported(c.Request.Context(), store, outbox); err != nil {
			if !errors.Is(err, registrystore.ErrOutboxReplayUnsupported) {
				log.Warn("SSE slow-consumer recovery disabled", "err", err, "userID", userID)
			}
			replayChecked = true
			replayAvailable = false
			return false
		}
		replayChecked = true
		replayAvailable = true
		return true
	}

connectionLoop:
	for {
		sub, err := bus.Subscribe(c.Request.Context(), userID)
		if err != nil {
			log.Error("SSE subscribe failed", "err", err, "userID", userID)
			return
		}

		if resumeCursor != "" {
			writeSSEPhaseEvent(c, "replay")
			outcome := replaySSEEvents(c, store, userID, detail, outbox, sub, resumeCursor, cfg.OutboxReplayBatchSize, kindsFilter, &lastCursor, func(event registryeventbus.Event) bool {
				if event.Internal && event.Kind == "session" {
					sessionTracker.handleSessionEvent(event)
					return true
				}
				return event.Internal
			})
			switch outcome {
			case replayOutcomeClosed:
				return
			case replayOutcomeRecover:
				if canRecoverSlowConsumer() {
					log.Info("SSE replay recovering from slow consumer", "connID", session.ConnectionID, "userID", userID, "cursor", lastCursor)
					resumeCursor = lastCursor
					continue connectionLoop
				}
				log.Info("SSE connection evicted", "connID", session.ConnectionID, "userID", userID, "reason", "slow consumer")
				writeSSEEvent(c, registryeventbus.Event{
					Event:        "evicted",
					Kind:         "stream",
					Data:         map[string]string{"reason": "slow consumer"},
					OutboxCursor: lastCursor,
				})
				return
			case replayOutcomeContinue:
				resumeCursor = ""
			}
		}

		writeSSEPhaseEvent(c, "live")

		for {
			select {
			case <-c.Request.Context().Done():
				return

			case <-session.evicted:
				// A newer connection evicted this one — notify client, then close.
				log.Info("SSE connection evicted", "connID", session.ConnectionID, "userID", userID, "reason", "too many connections")
				writeSSEEvent(c, registryeventbus.Event{
					Event:        "evicted",
					Kind:         "stream",
					Data:         map[string]string{"reason": "too many connections"},
					OutboxCursor: lastCursor,
				})
				return

			case <-keepalive.C:
				fmt.Fprintf(c.Writer, ": keepalive\n\n")
				c.Writer.Flush()

			case event, ok := <-sub:
				if !ok {
					// Channel closed — subscriber was evicted (slow consumer).
					if security.EventBusSubscriberEvictionsTotal != nil {
						security.EventBusSubscriberEvictionsTotal.Inc()
					}
					if canRecoverSlowConsumer() {
						log.Info("SSE connection recovering from slow consumer", "connID", session.ConnectionID, "userID", userID, "cursor", lastCursor)
						resumeCursor = lastCursor
						continue connectionLoop
					}
					log.Info("SSE connection evicted", "connID", session.ConnectionID, "userID", userID, "reason", "slow consumer")
					writeSSEEvent(c, registryeventbus.Event{
						Event:        "evicted",
						Kind:         "stream",
						Data:         map[string]string{"reason": "slow consumer"},
						OutboxCursor: lastCursor,
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
				if event.OutboxCursor != "" {
					lastCursor = event.OutboxCursor
				}
				if enriched, ok := enrichUserEvent(c.Request.Context(), store, userID, detail, event); ok {
					writeSSEEvent(c, enriched)
				}
			}
		}
	}
}

func replaySSEEvents(c *gin.Context, store registrystore.MemoryStore, userID, detail string, outbox registrystore.EventOutboxStore, sub <-chan registryeventbus.Event, after string, batchSize int, kindsFilter map[string]bool, lastCursor *string, skipBusEvent func(registryeventbus.Event) bool) replayOutcome {
	if batchSize <= 0 {
		batchSize = 1000
	}
	visibleGroups, err := loadUserReplayGroups(c.Request.Context(), store, userID)
	if err != nil {
		log.Error("SSE replay membership preload failed", "err", err, "userID", userID)
		return replayOutcomeClosed
	}
	query := registrystore.OutboxQuery{
		AfterCursor: after,
		Limit:       batchSize,
		Kinds:       mapKeys(kindsFilter),
	}
	seen := map[string]struct{}{}
	cursor := after

	for {
		query.AfterCursor = cursor
		var page *registrystore.OutboxPage
		err := store.InReadTx(c.Request.Context(), func(txCtx context.Context) error {
			var err error
			page, err = outbox.ListOutboxEvents(txCtx, query)
			return err
		})
		if err != nil {
			if err == registrystore.ErrStaleOutboxCursor {
				writeSSEEvent(c, registryeventbus.Event{
					Event: "invalidate",
					Kind:  "stream",
					Data:  map[string]string{"reason": "cursor beyond retention window"},
				})
				return replayOutcomeClosed
			}
			log.Error("SSE replay failed", "err", err, "after", after)
			return replayOutcomeClosed
		}
		if page == nil {
			break
		}
		for _, replayEvent := range page.Events {
			cursor = replayEvent.Cursor
			if replayEvent.Cursor != "" {
				seen[replayEvent.Cursor] = struct{}{}
			}
			event := registryeventbus.Event{
				Event:        replayEvent.Event,
				Kind:         replayEvent.Kind,
				Data:         json.RawMessage(replayEvent.Data),
				OutboxCursor: replayEvent.Cursor,
			}
			if event.OutboxCursor != "" {
				*lastCursor = event.OutboxCursor
			}
			if !userCanReplayEvent(userID, visibleGroups, event) {
				continue
			}
			if enriched, ok := enrichUserEvent(c.Request.Context(), store, userID, detail, event); ok {
				writeSSEEvent(c, enriched)
			}
		}
		if !page.HasMore || cursor == "" {
			break
		}
	}

	for {
		select {
		case event, ok := <-sub:
			if !ok {
				return replayOutcomeRecover
			}
			if skipBusEvent(event) {
				continue
			}
			if event.OutboxCursor != "" {
				if _, ok := seen[event.OutboxCursor]; ok {
					continue
				}
				*lastCursor = event.OutboxCursor
			}
			if len(kindsFilter) > 0 && !kindsFilter[event.Kind] {
				continue
			}
			if enriched, ok := enrichUserEvent(c.Request.Context(), store, userID, detail, event); ok {
				writeSSEEvent(c, enriched)
			}
		default:
			return replayOutcomeContinue
		}
	}
}

func mapKeys(items map[string]bool) []string {
	if len(items) == 0 {
		return nil
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	return keys
}

func enrichUserEvent(ctx context.Context, store registrystore.MemoryStore, userID, detail string, event registryeventbus.Event) (registryeventbus.Event, bool) {
	if detail != "full" || event.Kind == "stream" {
		return event, true
	}

	data, ok := decodeEventData(event.Data)
	if !ok {
		return event, true
	}

	switch event.Kind {
	case "conversation":
		conversationID, ok := decodeUUIDField(data, "conversation")
		if !ok {
			return event, true
		}
		conv, err := readConversationDetail(ctx, store, userID, conversationID)
		if err != nil {
			return event, false
		}
		raw, err := json.Marshal(conv)
		if err != nil {
			return event, true
		}
		event.Data = json.RawMessage(raw)
		return event, true
	case "entry":
		conversationID, ok := decodeUUIDField(data, "conversation")
		if !ok {
			return event, true
		}
		entryID, ok := decodeUUIDField(data, "entry")
		if !ok {
			return event, true
		}
		entry, err := readEntryDetail(ctx, store, userID, conversationID, entryID)
		if err != nil {
			return event, false
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return event, true
		}
		event.Data = json.RawMessage(raw)
		return event, true
	default:
		return event, true
	}
}

func readConversationDetail(ctx context.Context, store registrystore.MemoryStore, userID string, conversationID uuid.UUID) (*registrystore.ConversationDetail, error) {
	var conv *registrystore.ConversationDetail
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		conv, err = store.GetConversation(txCtx, userID, conversationID)
		return err
	})
	return conv, err
}

func readEntryDetail(ctx context.Context, store registrystore.MemoryStore, userID string, conversationID, entryID uuid.UUID) (*model.Entry, error) {
	var result *registrystore.PagedEntries
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.GetEntries(txCtx, userID, conversationID, nil, 5000, nil, nil, nil, nil, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("entry not found")
	}
	for i := range result.Data {
		if result.Data[i].ID == entryID {
			entry := result.Data[i]
			return &entry, nil
		}
	}
	return nil, fmt.Errorf("entry not found")
}

func decodeEventData(data any) (map[string]any, bool) {
	switch typed := data.(type) {
	case map[string]any:
		return typed, true
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err == nil {
			return out, true
		}
	case []byte:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err == nil {
			return out, true
		}
	}
	return nil, false
}

func decodeUUIDField(data map[string]any, field string) (uuid.UUID, bool) {
	raw, ok := data[field]
	if !ok {
		return uuid.Nil, false
	}
	value, ok := raw.(string)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func loadUserReplayGroups(ctx context.Context, store registrystore.MemoryStore, userID string) (map[uuid.UUID]bool, error) {
	visible := map[uuid.UUID]bool{}
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		groupIDs, err := store.ListConversationGroupIDs(txCtx, userID)
		if err != nil {
			return err
		}
		for _, groupID := range groupIDs {
			visible[groupID] = true
		}
		return nil
	})
	return visible, err
}

func userCanReplayEvent(userID string, visibleGroups map[uuid.UUID]bool, event registryeventbus.Event) bool {
	if event.Kind == "stream" {
		return true
	}
	data, ok := decodeEventData(event.Data)
	if !ok {
		return false
	}
	if event.Kind == "conversation" && event.Event == "deleted" {
		if members, ok := decodeUserListField(data, "members"); ok {
			for _, member := range members {
				if member == userID {
					return true
				}
			}
		}
	}
	groupID, ok := decodeUUIDField(data, "conversation_group")
	if !ok {
		return false
	}
	allowed := visibleGroups[groupID]
	if event.Kind == "membership" {
		targetUser, _ := data["user"].(string)
		if targetUser == userID {
			allowed = true
			switch event.Event {
			case "created", "updated":
				visibleGroups[groupID] = true
			case "deleted":
				delete(visibleGroups, groupID)
			}
		}
	}
	return allowed
}

func decodeUserListField(data map[string]any, field string) ([]string, bool) {
	raw, ok := data[field]
	if !ok {
		return nil, false
	}
	switch typed := raw.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				continue
			}
			out = append(out, value)
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}
