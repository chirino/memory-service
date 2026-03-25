package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

type replayOutcome int

const (
	replayOutcomeContinue replayOutcome = iota
	replayOutcomeClosed
	replayOutcomeRecover
)

func writeAdminSSEEvent(c *gin.Context, event registryeventbus.Event) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
}

func writeAdminSSEPhaseEvent(c *gin.Context, phase string) {
	writeAdminSSEEvent(c, registryeventbus.Event{
		Event: "phase",
		Kind:  "stream",
		Data:  map[string]string{"phase": phase},
	})
}

// HandleAdminSSEEvents streams all (non-internal) events to an admin user via SSE.
// Requires a justification query parameter for audit purposes.
func HandleAdminSSEEvents(c *gin.Context, store registrystore.MemoryStore, bus registryeventbus.EventBus, cfg *config.Config) {
	justification := strings.TrimSpace(c.Query("justification"))
	if justification == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "justification query parameter is required"})
		return
	}
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

	keepalive := time.NewTicker(cfg.SSEKeepaliveInterval)
	defer keepalive.Stop()

	auditRelog := time.NewTicker(5 * time.Minute)
	defer auditRelog.Stop()
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
				log.Warn("Admin SSE slow-consumer recovery disabled", "err", err, "adminID", adminID)
			}
			replayChecked = true
			replayAvailable = false
			return false
		}
		replayChecked = true
		replayAvailable = true
		return true
	}

streamLoop:
	for {
		sub, err := bus.Subscribe(c.Request.Context(), "")
		if err != nil {
			log.Error("Admin SSE subscribe failed", "err", err, "adminID", adminID)
			return
		}

		if resumeCursor != "" {
			writeAdminSSEPhaseEvent(c, "replay")
			outcome := replayAdminSSEEvents(c, store, detail, outbox, sub, resumeCursor, replayBatchSize(cfg), kindsFilter, &lastCursor)
			switch outcome {
			case replayOutcomeClosed:
				return
			case replayOutcomeRecover:
				if canRecoverSlowConsumer() {
					log.Info("Admin SSE replay recovering from slow consumer", "adminID", adminID, "cursor", lastCursor, "justification", justification)
					resumeCursor = lastCursor
					continue streamLoop
				}
				writeAdminSSEEvent(c, registryeventbus.Event{
					Event:        "evicted",
					Kind:         "stream",
					Data:         map[string]string{"reason": "slow consumer"},
					OutboxCursor: lastCursor,
				})
				log.Info("Admin SSE stream evicted", "adminID", adminID, "justification", justification)
				return
			case replayOutcomeContinue:
				resumeCursor = ""
			}
		}
		writeAdminSSEPhaseEvent(c, "live")

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
					if canRecoverSlowConsumer() {
						log.Info("Admin SSE stream recovering from slow consumer", "adminID", adminID, "cursor", lastCursor, "justification", justification)
						resumeCursor = lastCursor
						continue streamLoop
					}
					writeAdminSSEEvent(c, registryeventbus.Event{
						Event:        "evicted",
						Kind:         "stream",
						Data:         map[string]string{"reason": "slow consumer"},
						OutboxCursor: lastCursor,
					})
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

				if event.OutboxCursor != "" {
					lastCursor = event.OutboxCursor
				}
				if enriched, ok := enrichAdminEvent(c.Request.Context(), store, detail, event); ok {
					writeAdminSSEEvent(c, enriched)
				}
			}
		}
	}
}

func replayAdminSSEEvents(c *gin.Context, store registrystore.MemoryStore, detail string, outbox registrystore.EventOutboxStore, sub <-chan registryeventbus.Event, after string, batchSize int, kindsFilter map[string]bool, lastCursor *string) replayOutcome {
	query := registrystore.OutboxQuery{
		AfterCursor: after,
		Limit:       batchSize,
		Kinds:       adminMapKeys(kindsFilter),
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
				writeAdminSSEEvent(c, registryeventbus.Event{
					Event: "invalidate",
					Kind:  "stream",
					Data:  map[string]string{"reason": "cursor beyond retention window"},
				})
				return replayOutcomeClosed
			}
			log.Error("Admin SSE replay failed", "err", err, "after", after)
			return replayOutcomeClosed
		}
		if page == nil {
			break
		}
		for _, replayEvent := range page.Events {
			cursor = replayEvent.Cursor
			if replayEvent.Cursor != "" {
				seen[replayEvent.Cursor] = struct{}{}
				*lastCursor = replayEvent.Cursor
			}
			event := registryeventbus.Event{
				Event:        replayEvent.Event,
				Kind:         replayEvent.Kind,
				Data:         json.RawMessage(replayEvent.Data),
				OutboxCursor: replayEvent.Cursor,
			}
			if enriched, ok := enrichAdminEvent(c.Request.Context(), store, detail, event); ok {
				writeAdminSSEEvent(c, enriched)
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
			if event.Internal {
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
			if enriched, ok := enrichAdminEvent(c.Request.Context(), store, detail, event); ok {
				writeAdminSSEEvent(c, enriched)
			}
		default:
			return replayOutcomeContinue
		}
	}
}

func enrichAdminEvent(ctx context.Context, store registrystore.MemoryStore, detail string, event registryeventbus.Event) (registryeventbus.Event, bool) {
	if detail != "full" || event.Kind == "stream" {
		return event, true
	}
	data, ok := decodeAdminEventData(event.Data)
	if !ok {
		return event, true
	}
	switch event.Kind {
	case "conversation":
		conversationID, ok := decodeAdminUUIDField(data, "conversation")
		if !ok {
			return event, true
		}
		conv, err := readAdminConversationDetail(ctx, store, conversationID)
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
		conversationID, ok := decodeAdminUUIDField(data, "conversation")
		if !ok {
			return event, true
		}
		entryID, ok := decodeAdminUUIDField(data, "entry")
		if !ok {
			return event, true
		}
		entry, err := readAdminEntryDetail(ctx, store, conversationID, entryID)
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

func replayBatchSize(cfg *config.Config) int {
	if cfg == nil || cfg.OutboxReplayBatchSize <= 0 {
		return 1000
	}
	return cfg.OutboxReplayBatchSize
}

func readAdminConversationDetail(ctx context.Context, store registrystore.MemoryStore, conversationID uuid.UUID) (*registrystore.ConversationDetail, error) {
	var conv *registrystore.ConversationDetail
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		conv, err = store.AdminGetConversation(txCtx, conversationID)
		return err
	})
	return conv, err
}

func readAdminEntryDetail(ctx context.Context, store registrystore.MemoryStore, conversationID, entryID uuid.UUID) (*model.Entry, error) {
	var result *registrystore.PagedEntries
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.AdminGetEntries(txCtx, conversationID, registrystore.AdminMessageQuery{
			Limit:    5000,
			AllForks: true,
		})
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

func decodeAdminEventData(data any) (map[string]any, bool) {
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

func decodeAdminUUIDField(data map[string]any, field string) (uuid.UUID, bool) {
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

func adminMapKeys(items map[string]bool) []string {
	if len(items) == 0 {
		return nil
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	return keys
}
