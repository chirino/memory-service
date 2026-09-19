package eventstream

import (
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/google/uuid"
)

// MemoryChangedEvent creates the payload-safe durable event used by analytics
// and other processors. Namespace, key, values, and policy attributes are
// intentionally absent from the outbox payload.
func MemoryChangedEvent(action, change string, item *registryepisodic.MemoryItem) registryeventbus.Event {
	data := map[string]any{"change": change}
	if item != nil {
		data["memory"] = item.ID.String()
		data["memoryKind"] = item.MemoryKind
		data["revision"] = item.Revision
		data["createdAt"] = item.CreatedAt.UTC()
		if item.ExpiresAt != nil {
			data["expiresAt"] = item.ExpiresAt.UTC()
		}
		if item.ArchivedAt != nil {
			data["archivedAt"] = item.ArchivedAt.UTC()
		}
	}
	return registryeventbus.Event{Event: action, Kind: "memory", Data: data, AdminOnly: true}
}

func MemoryWriteEvent(action, change string, result *registryepisodic.MemoryWriteResult) registryeventbus.Event {
	if result == nil {
		return MemoryChangedEvent(action, change, nil)
	}
	return MemoryChangedEvent(action, change, &registryepisodic.MemoryItem{
		ID: result.ID, MemoryKind: result.MemoryKind, Revision: result.Revision,
		CreatedAt: result.CreatedAt, ExpiresAt: result.ExpiresAt,
	})
}

func MemoryDeletedEvent(id uuid.UUID, change string) registryeventbus.Event {
	return registryeventbus.Event{Event: "deleted", Kind: "memory", Data: map[string]any{"memory": id.String(), "change": change}, AdminOnly: true}
}
