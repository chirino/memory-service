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
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
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
	data, _ := eventstream.MarshalDeliveryJSON(event)
	fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
}

func writeAdminSSEPhaseEvent(c *gin.Context, phase string, cursor ...string) {
	highWater := ""
	if len(cursor) > 0 {
		highWater = cursor[0]
	}
	writeAdminSSEEvent(c, registryeventbus.Event{
		Event: "phase", Kind: "stream", Data: map[string]string{"phase": phase}, OutboxCursor: highWater,
	})
}

// HandleAdminSSEEvents streams all (non-internal) events to an admin user via SSE.
func HandleAdminSSEEvents(c *gin.Context, store registrystore.MemoryStore, episodicStore registryepisodic.EpisodicStore, bus registryeventbus.EventBus, cfg *config.Config) {
	justification := strings.TrimSpace(c.Query("justification"))
	if justification == "" {
		justification = strings.TrimSpace(c.GetHeader("X-Justification"))
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
	initialState := strings.TrimSpace(c.DefaultQuery("initial_state", "none"))
	if initialState == "" {
		initialState = "none"
	}
	if initialState != "none" && initialState != "current" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "initial_state must be one of: none, current"})
		return
	}
	if initialState == "current" {
		if detail != "full" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "initial_state=current requires detail=full"})
			return
		}
		if after != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "initial_state=current cannot be combined with after"})
			return
		}
		if !cfg.OutboxEnabled {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "initial_state=current requires the event outbox to be enabled"})
			return
		}
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
	if initialState == "current" {
		if outbox == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "initial_state=current is not supported by the configured datastore"})
			return
		}
		if err := eventstream.ReplaySupported(c.Request.Context(), store, outbox); err != nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "initial_state=current is not supported by the configured datastore"})
			return
		}
	}

	adminID := security.GetUserID(c)
	log.Info("Admin audit",
		"caller", adminID,
		"action", "event_stream_open",
		"requestId", security.RequestIDFromGin(c),
		"justification", justification,
	)
	operation := security.OperationEventFromGin(c)
	if operation != nil {
		operation.SetConnectionID(uuid.NewString())
		operation.SetCursor(after)
		operation.EmitStart()
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
	entryFilter := eventstream.EntryEventFilterFromQuery(c.Request.URL.Query())
	entryLoader := func(ctx context.Context, conversationID string, entryID uuid.UUID, _ *model.Channel) (*model.Entry, error) {
		return readAdminEntryDetail(ctx, store, conversationID, entryID)
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
	defer func() {
		if operation != nil {
			operation.SetCursor(lastCursor)
		}
	}()
	resumeCursor := after
	initialStatePending := initialState == "current"
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
			security.SetOperationTerminalError(c, "subscribe_failed", err)
			return
		}
		if initialStatePending {
			highWaterStore, ok := store.(registrystore.OutboxHighWaterStore)
			if !ok {
				security.SetOperationTerminalError(c, "snapshot_unsupported", registrystore.ErrOutboxReplayUnsupported)
				return
			}
			var highWater string
			if err := store.InReadTx(c.Request.Context(), func(txCtx context.Context) error {
				var err error
				highWater, err = highWaterStore.CurrentOutboxCursor(txCtx)
				return err
			}); err != nil {
				security.SetOperationTerminalError(c, "snapshot_boundary_failed", err)
				return
			}
			writeAdminSSEPhaseEvent(c, "snapshot", highWater)
			if err := eventstream.StreamAdminCurrentState(c.Request.Context(), store, episodicStore, kindsFilter, nil, entryFilter, func(event registryeventbus.Event) error {
				writeAdminSSEEvent(c, event)
				return nil
			}); err != nil {
				security.SetOperationTerminalError(c, "snapshot_failed", err)
				return
			}
			resumeCursor = highWater
			initialStatePending = false
		}

		if resumeCursor != "" {
			writeAdminSSEPhaseEvent(c, "replay")
			outcome, replayErr := replayAdminSSEEvents(c, store, episodicStore, detail, outbox, sub, resumeCursor, replayBatchSize(cfg), kindsFilter, entryFilter, entryLoader, &lastCursor)
			switch outcome {
			case replayOutcomeClosed:
				if replayErr != nil {
					security.SetOperationTerminalError(c, "replay_failed", replayErr)
				}
				return
			case replayOutcomeRecover:
				if canRecoverSlowConsumer() {
					log.Info("Admin SSE replay recovering from slow consumer", "adminID", adminID, "cursor", lastCursor)
					resumeCursor = lastCursor
					continue streamLoop
				}
				writeAdminSSEEvent(c, registryeventbus.Event{
					Event:        "evicted",
					Kind:         "stream",
					Data:         map[string]string{"reason": "slow consumer"},
					OutboxCursor: lastCursor,
				})
				log.Info("Admin SSE stream evicted", "adminID", adminID)
				return
			case replayOutcomeContinue:
				resumeCursor = ""
			}
		}
		writeAdminSSEPhaseEvent(c, "live")

		for {
			select {
			case <-c.Request.Context().Done():
				return

			case <-keepalive.C:
				fmt.Fprintf(c.Writer, ": keepalive\n\n")
				c.Writer.Flush()

			case <-auditRelog.C:
				log.Info("Admin audit",
					"caller", adminID,
					"action", "event_stream_active",
					"requestId", security.RequestIDFromGin(c),
					"justification", justification,
				)

			case event, ok := <-sub:
				if !ok {
					if canRecoverSlowConsumer() {
						log.Info("Admin SSE stream recovering from slow consumer", "adminID", adminID, "cursor", lastCursor)
						resumeCursor = lastCursor
						continue streamLoop
					}
					writeAdminSSEEvent(c, registryeventbus.Event{
						Event:        "evicted",
						Kind:         "stream",
						Data:         map[string]string{"reason": "slow consumer"},
						OutboxCursor: lastCursor,
					})
					log.Info("Admin SSE stream evicted", "adminID", adminID)
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
				matches, err := entryFilter.Matches(c.Request.Context(), event, entryLoader)
				if err != nil {
					log.Warn("Admin SSE entry filter failed", "err", err, "adminID", adminID)
					continue
				}
				if !matches {
					continue
				}

				enriched, ok, err := enrichAdminEvent(c.Request.Context(), store, episodicStore, detail, event)
				if err != nil {
					security.SetOperationTerminalError(c, "event_enrichment_failed", err)
					return
				}
				if ok {
					writeAdminSSEEvent(c, enriched)
				}
				if event.OutboxCursor != "" {
					lastCursor = event.OutboxCursor
				}
			}
		}
	}
}

func replayAdminSSEEvents(c *gin.Context, store registrystore.MemoryStore, episodicStore registryepisodic.EpisodicStore, detail string, outbox registrystore.EventOutboxStore, sub <-chan registryeventbus.Event, after string, batchSize int, kindsFilter map[string]bool, entryFilter eventstream.EntryEventFilter, entryLoader eventstream.EntryDetailLoader, lastCursor *string) (replayOutcome, error) {
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
			if errors.Is(err, registrystore.ErrStaleOutboxCursor) {
				writeAdminSSEEvent(c, registryeventbus.Event{
					Event: "invalidate",
					Kind:  "stream",
					Data:  map[string]string{"reason": "cursor beyond retention window"},
				})
				return replayOutcomeClosed, nil
			}
			log.Error("Admin SSE replay failed", "err", err, "after", after)
			return replayOutcomeClosed, err
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
				OccurredAt:   adminSSETimePtr(replayEvent.CreatedAt),
			}
			matches, err := entryFilter.Matches(c.Request.Context(), event, entryLoader)
			if err != nil {
				log.Warn("Admin SSE replay entry filter failed", "err", err)
				continue
			}
			if !matches {
				*lastCursor = replayEvent.Cursor
				continue
			}
			enriched, ok, enrichErr := enrichAdminEvent(c.Request.Context(), store, episodicStore, detail, event)
			if enrichErr != nil {
				return replayOutcomeClosed, enrichErr
			}
			if ok {
				writeAdminSSEEvent(c, enriched)
			}
			*lastCursor = replayEvent.Cursor
		}
		if !page.HasMore || cursor == "" {
			break
		}
	}

	for {
		select {
		case event, ok := <-sub:
			if !ok {
				return replayOutcomeRecover, nil
			}
			if event.Internal {
				continue
			}
			if event.OutboxCursor != "" {
				if _, ok := seen[event.OutboxCursor]; ok {
					continue
				}
			}
			if len(kindsFilter) > 0 && !kindsFilter[event.Kind] {
				continue
			}
			matches, err := entryFilter.Matches(c.Request.Context(), event, entryLoader)
			if err != nil {
				log.Warn("Admin SSE replay tail entry filter failed", "err", err)
				continue
			}
			if !matches {
				*lastCursor = event.OutboxCursor
				continue
			}
			enriched, ok, enrichErr := enrichAdminEvent(c.Request.Context(), store, episodicStore, detail, event)
			if enrichErr != nil {
				return replayOutcomeClosed, enrichErr
			}
			if ok {
				writeAdminSSEEvent(c, enriched)
			}
			if event.OutboxCursor != "" {
				*lastCursor = event.OutboxCursor
			}
		default:
			return replayOutcomeContinue, nil
		}
	}
}

func enrichAdminEvent(ctx context.Context, store registrystore.MemoryStore, episodicStore registryepisodic.EpisodicStore, detail string, event registryeventbus.Event) (registryeventbus.Event, bool, error) {
	if detail != "full" || event.Kind == "stream" {
		return event, true, nil
	}
	data, ok := decodeAdminEventData(event.Data)
	if !ok {
		return event, true, nil
	}
	event.Change = eventstream.EventChange(event.Data)
	switch event.Kind {
	case "conversation":
		conversationID, ok := decodeAdminConversationIDField(data, "conversation")
		if !ok {
			return event, true, nil
		}
		conv, err := readAdminConversationDetail(ctx, store, conversationID)
		if err != nil {
			var notFound *registrystore.NotFoundError
			if errors.As(err, &notFound) {
				return event, true, nil
			}
			return event, false, err
		}
		if conv == nil {
			return event, true, nil
		}
		event.Data = eventstream.AdminConversationResource(conv)
		return event, true, nil
	case "entry":
		conversationID, ok := decodeAdminConversationIDField(data, "conversation")
		if !ok {
			return event, true, nil
		}
		entryID, ok := decodeAdminUUIDField(data, "entry")
		if !ok {
			return event, true, nil
		}
		entry, err := readAdminEntryDetail(ctx, store, conversationID, entryID)
		if err != nil {
			var notFound *registrystore.NotFoundError
			if errors.As(err, &notFound) {
				return event, true, nil
			}
			return event, false, err
		}
		event.Data = eventstream.AdminEntryResource(entry)
		return event, true, nil
	case "memory":
		if episodicStore == nil {
			return event, true, nil
		}
		memoryID, ok := decodeAdminUUIDField(data, "memory")
		if !ok {
			return event, true, nil
		}
		item, err := readAdminMemoryDetail(ctx, episodicStore, memoryID)
		if err != nil {
			var notFound *registrystore.NotFoundError
			if errors.As(err, &notFound) {
				return event, true, nil
			}
			return event, false, err
		}
		if item == nil {
			return event, true, nil
		}
		event.Data = eventstream.AdminMemoryResource(item)
		return event, true, nil
	default:
		return event, true, nil
	}
}

func replayBatchSize(cfg *config.Config) int {
	if cfg == nil || cfg.OutboxReplayBatchSize <= 0 {
		return 1000
	}
	return cfg.OutboxReplayBatchSize
}

func readAdminConversationDetail(ctx context.Context, store registrystore.MemoryStore, conversationID string) (*registrystore.ConversationDetail, error) {
	var conv *registrystore.ConversationDetail
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		conv, err = store.AdminGetConversation(txCtx, conversationID)
		return err
	})
	return conv, err
}

func readAdminEntryDetail(ctx context.Context, store registrystore.MemoryStore, conversationID string, entryID uuid.UUID) (*model.Entry, error) {
	var result *registrystore.PagedEntries
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		result, err = store.AdminGetEntries(txCtx, conversationID, registrystore.AdminEntryLookupQuery(entryID))
		return err
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, &registrystore.NotFoundError{Resource: "entry", ID: entryID.String()}
	}
	if len(result.Data) == 1 && result.Data[0].ID == entryID {
		entry := result.Data[0]
		return &entry, nil
	}
	return nil, &registrystore.NotFoundError{Resource: "entry", ID: entryID.String()}
}

func readAdminMemoryDetail(ctx context.Context, store registryepisodic.EpisodicStore, memoryID uuid.UUID) (*registryepisodic.MemoryItem, error) {
	var item *registryepisodic.MemoryItem
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		item, err = store.AdminGetMemoryByID(txCtx, memoryID)
		return err
	})
	return item, err
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

func decodeAdminConversationIDField(data map[string]any, field string) (string, bool) {
	raw, ok := data[field]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return string(value), true
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

func adminSSETimePtr(value time.Time) *time.Time { return &value }
