//go:build !nosqlite

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/charmbracelet/log"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventing"
	"github.com/chirino/memory-service/internal/txscope"
	"github.com/google/uuid"
)

// SQLite publishes committed outbox rows in append order while holding its
// process-local writer lock. Route publication is suppressed in durable mode.
func (s *SQLiteStore) StartOutboxRelay(_ context.Context, bus registryeventbus.EventBus) error {
	s.handle.outboxBus = bus
	return nil
}
func (s *SQLiteStore) RelayPublishesOutboxEvents() bool {
	return s.OutboxEnabled() && s.handle != nil && s.handle.outboxBus != nil
}
func (s *SQLiteStore) publishCommittedOutbox(ctx context.Context, rows []registrystore.OutboxEvent) {
	if !s.RelayPublishesOutboxEvents() {
		return
	}
	txscope.AfterCommit(ctx, func(commitCtx context.Context) {
		for _, row := range rows {
			if err := s.publishOutboxEvent(commitCtx, row); err != nil {
				log.Warn("SQLite outbox publication failed", "errorType", fmt.Sprintf("%T", err))
				return
			}
		}
	})
}
func (s *SQLiteStore) publishOutboxEvent(ctx context.Context, row registrystore.OutboxEvent) error {
	var data map[string]any
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return err
	}
	occurred := row.CreatedAt.UTC()
	event := registryeventbus.Event{Event: row.Event, Kind: row.Kind, Data: data, OutboxCursor: row.Cursor, OccurredAt: &occurred}
	if group, ok := data["conversation_group"].(string); ok {
		event.ConversationGroupID, _ = uuid.Parse(group)
	}
	if row.Kind == "membership" {
		if user, ok := data["user"].(string); ok {
			event.UserIDs = []string{user}
		}
	}
	if row.Kind == "conversation" && row.Event == "deleted" {
		if users, ok := data["members"].([]any); ok {
			for _, value := range users {
				if user, ok := value.(string); ok {
					event.UserIDs = append(event.UserIDs, user)
				}
			}
		}
	}
	bus := s.handle.outboxBus
	if len(event.UserIDs) > 0 {
		return eventing.PublishToUsers(ctx, bus, event.UserIDs, event)
	}
	if event.ConversationGroupID != uuid.Nil {
		return eventing.PublishToGroup(ctx, s, bus, event.ConversationGroupID, event)
	}
	return bus.Publish(ctx, event)
}
