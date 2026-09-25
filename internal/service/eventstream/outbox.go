package eventstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/service/eventing"
	"github.com/google/uuid"
)

type relayOwnedOutboxPublisher interface {
	RelayPublishesOutboxEvents() bool
}

// DeleteConversationGroups prepares the complete started-child/fork closure,
// captures recipients before memberships disappear, and records cleanup work and
// deletion events. Call inside InWriteTx and publish the returned events after
// commit. SQL stores keep the snapshot, outbox, tasks, and delete atomic.
func DeleteConversationGroups(ctx context.Context, store registrystore.MemoryStore, groupIDs []uuid.UUID) ([]registryeventbus.Event, error) {
	groups, err := store.LoadDeletedConversationGroups(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ConversationGroupID)
		if err := store.CreateTask(ctx, "vector_store_delete", map[string]any{"conversationGroupId": group.ConversationGroupID.String()}); err != nil {
			return nil, fmt.Errorf("create vector cleanup for group %s: %w", group.ConversationGroupID, err)
		}
	}
	events := ConversationDeletedEvents(groups)
	appended, used, err := AppendOutboxEvents(ctx, store, events...)
	if err != nil {
		return nil, err
	}
	if err := store.HardDeleteConversationGroups(ctx, ids); err != nil {
		return nil, err
	}
	if used {
		return appended, nil
	}
	return events, nil
}

func ConversationDeletedEvents(groups []registrystore.DeletedConversationGroup) []registryeventbus.Event {
	events := make([]registryeventbus.Event, 0)
	for _, group := range groups {
		for _, conversationID := range group.ConversationIDs {
			events = append(events, registryeventbus.Event{
				Event: "deleted",
				Kind:  "conversation",
				Data: map[string]any{
					"conversation":       conversationID,
					"conversation_group": group.ConversationGroupID,
					"members":            group.MemberUserIDs,
				},
				ConversationGroupID: group.ConversationGroupID,
				UserIDs:             append([]string(nil), group.MemberUserIDs...),
			})
		}
	}
	return events
}

// AppendOutboxEvents writes normalized business events into the store-backed
// outbox when the datastore supports that optional capability.
func AppendOutboxEvents(ctx context.Context, store registrystore.MemoryStore, events ...registryeventbus.Event) ([]registryeventbus.Event, bool, error) {
	if provider, ok := store.(registrystore.OutboxEnabledProvider); ok && !provider.OutboxEnabled() {
		return events, false, nil
	}
	outbox, ok := store.(registrystore.EventOutboxStore)
	if !ok || len(events) == 0 {
		return events, false, nil
	}

	now := time.Now()
	writes := make([]registrystore.OutboxWrite, 0, len(events))
	for _, event := range events {
		raw, err := json.Marshal(event.Data)
		if err != nil {
			return nil, true, err
		}
		writes = append(writes, registrystore.OutboxWrite{
			Event:     event.Event,
			Kind:      event.Kind,
			Data:      raw,
			CreatedAt: now,
		})
	}
	appended, err := outbox.AppendOutboxEvents(ctx, writes)
	if err != nil {
		return nil, true, err
	}
	if len(appended) != len(events) {
		return nil, true, errors.New("outbox append count mismatch")
	}
	published := make([]registryeventbus.Event, 0, len(events))
	for i, event := range events {
		event.OutboxCursor = appended[i].Cursor
		occurredAt := appended[i].CreatedAt.UTC()
		event.OccurredAt = &occurredAt
		published = append(published, event)
	}
	return published, true, nil
}

// PublishEvents publishes committed events using their routing metadata.
func PublishEvents(ctx context.Context, store registrystore.MemoryStore, bus registryeventbus.EventBus, events ...registryeventbus.Event) error {
	if bus == nil || len(events) == 0 {
		return nil
	}
	if relayOwner, ok := store.(relayOwnedOutboxPublisher); ok && relayOwner.RelayPublishesOutboxEvents() {
		return nil
	}
	for _, event := range events {
		switch {
		case len(event.UserIDs) > 0:
			if err := eventing.PublishToUsers(ctx, bus, event.UserIDs, event); err != nil {
				return err
			}
		case event.ConversationGroupID != uuid.Nil:
			if err := eventing.PublishToGroup(ctx, store, bus, event.ConversationGroupID, event); err != nil {
				return err
			}
		default:
			if err := bus.Publish(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReplaySupported reports whether the configured outbox implementation supports
// durable replay reads.
func ReplaySupported(ctx context.Context, store registrystore.MemoryStore, outbox registrystore.EventOutboxStore) error {
	if outbox == nil {
		return registrystore.ErrOutboxReplayUnsupported
	}
	var err error
	readErr := store.InReadTx(ctx, func(txCtx context.Context) error {
		_, err = outbox.ListOutboxEvents(txCtx, registrystore.OutboxQuery{
			AfterCursor: "start",
			Limit:       1,
		})
		return nil
	})
	if readErr != nil {
		return readErr
	}
	if err != nil && errors.Is(err, registrystore.ErrOutboxReplayUnsupported) {
		return err
	}
	return nil
}
