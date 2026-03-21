package eventing

import (
	"context"
	"sort"

	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/google/uuid"
)

// PublishToGroup resolves the current member set for a conversation group and
// publishes the event to those users.
func PublishToGroup(ctx context.Context, store registrystore.MemoryStore, bus registryeventbus.EventBus, groupID uuid.UUID, event registryeventbus.Event) error {
	var userIDs []string
	if err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		userIDs, err = store.GetGroupMemberUserIDs(txCtx, groupID)
		return err
	}); err != nil {
		return err
	}
	event.UserIDs = dedupeUsers(userIDs)
	return bus.Publish(ctx, event)
}

// PublishToUsers publishes the event to the provided user IDs after deduping.
func PublishToUsers(ctx context.Context, bus registryeventbus.EventBus, userIDs []string, event registryeventbus.Event) error {
	event.UserIDs = dedupeUsers(userIDs)
	return bus.Publish(ctx, event)
}

func dedupeUsers(userIDs []string) []string {
	if len(userIDs) <= 1 {
		return userIDs
	}
	seen := make(map[string]struct{}, len(userIDs))
	deduped := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		deduped = append(deduped, userID)
	}
	sort.Strings(deduped)
	return deduped
}
