package eventstream

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/chirino/memory-service/internal/model"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/google/uuid"
)

type EntryEventFilter struct {
	channels     map[string]struct{}
	contentTypes map[string]struct{}
	roles        map[string]struct{}
}

type EntryDetailLoader func(ctx context.Context, conversationID, entryID uuid.UUID) (*model.Entry, error)

func NewEntryEventFilter(channels, contentTypes, roles []string) EntryEventFilter {
	filter := EntryEventFilter{
		channels:     makeStringSet(channels, true),
		contentTypes: makeStringSet(contentTypes, false),
		roles:        makeStringSet(roles, true),
	}
	if len(filter.channels) == 0 {
		filter.channels = map[string]struct{}{string(model.ChannelHistory): {}}
	}
	return filter
}

func EntryEventFilterFromQuery(values url.Values) EntryEventFilter {
	return NewEntryEventFilter(
		splitQueryValues(values["entry_channels"]),
		splitQueryValues(values["entry_content_types"]),
		splitQueryValues(values["entry_roles"]),
	)
}

func (f EntryEventFilter) Matches(ctx context.Context, event registryeventbus.Event, load EntryDetailLoader) (bool, error) {
	if event.Kind != "entry" {
		return true, nil
	}
	metadata, ok := entryMetadataFromEvent(event)
	if !ok && load != nil {
		entry, err := loadEntryForEvent(ctx, event, load)
		if err != nil {
			return false, err
		}
		if entry == nil {
			return false, nil
		}
		metadata = EntryMetadataFromEntry(*entry)
		ok = true
	}
	if !ok {
		return false, nil
	}
	if !setContains(f.channels, metadata.Channel, true) {
		return false, nil
	}
	if len(f.contentTypes) > 0 && !setContains(f.contentTypes, metadata.ContentType, false) {
		return false, nil
	}
	if len(f.roles) > 0 {
		if metadata.Role == "" || !setContains(f.roles, metadata.Role, true) {
			return false, nil
		}
	}
	return true, nil
}

type EntryEventMetadata struct {
	ConversationID uuid.UUID
	EntryID        uuid.UUID
	Channel        string
	ContentType    string
	Role           string
}

func EntryEventData(entry model.Entry, groupID uuid.UUID) map[string]any {
	data := map[string]any{
		"conversation":       entry.ConversationID,
		"conversation_group": groupID,
		"entry":              entry.ID,
		"entry_channel":      string(entry.Channel),
		"entry_content_type": entry.ContentType,
	}
	if role := EntryRoleFromContent(entry.Content); role != "" {
		data["entry_role"] = role
	}
	return data
}

func EntryMetadataFromEntry(entry model.Entry) EntryEventMetadata {
	return EntryEventMetadata{
		ConversationID: entry.ConversationID,
		EntryID:        entry.ID,
		Channel:        string(entry.Channel),
		ContentType:    entry.ContentType,
		Role:           EntryRoleFromContent(entry.Content),
	}
}

func EntryRoleFromContent(content []byte) string {
	var items []map[string]any
	if err := json.Unmarshal(content, &items); err != nil || len(items) == 0 {
		return ""
	}
	role, _ := items[0]["role"].(string)
	return strings.TrimSpace(role)
}

func splitQueryValues(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func makeStringSet(values []string, fold bool) map[string]struct{} {
	out := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if fold {
			value = strings.ToLower(value)
		}
		out[value] = struct{}{}
	}
	return out
}

func setContains(set map[string]struct{}, value string, fold bool) bool {
	value = strings.TrimSpace(value)
	if fold {
		value = strings.ToLower(value)
	}
	_, ok := set[value]
	return ok
}

func entryMetadataFromEvent(event registryeventbus.Event) (EntryEventMetadata, bool) {
	data, ok := decodeEventDataMap(event.Data)
	if !ok {
		return EntryEventMetadata{}, false
	}
	channel, _ := data["entry_channel"].(string)
	contentType, _ := data["entry_content_type"].(string)
	role, _ := data["entry_role"].(string)
	conversationID, _ := decodeEventUUID(data["conversation"])
	entryID, _ := decodeEventUUID(data["entry"])
	if strings.TrimSpace(channel) == "" || strings.TrimSpace(contentType) == "" {
		return EntryEventMetadata{}, false
	}
	return EntryEventMetadata{
		ConversationID: conversationID,
		EntryID:        entryID,
		Channel:        channel,
		ContentType:    contentType,
		Role:           role,
	}, true
}

func loadEntryForEvent(ctx context.Context, event registryeventbus.Event, load EntryDetailLoader) (*model.Entry, error) {
	data, ok := decodeEventDataMap(event.Data)
	if !ok {
		return nil, nil
	}
	conversationID, ok := decodeEventUUID(data["conversation"])
	if !ok {
		return nil, nil
	}
	entryID, ok := decodeEventUUID(data["entry"])
	if !ok {
		return nil, nil
	}
	return load(ctx, conversationID, entryID)
}

func decodeEventDataMap(raw any) (map[string]any, bool) {
	switch v := raw.(type) {
	case map[string]any:
		return v, true
	case map[string]string:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = value
		}
		return out, true
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(v, &out); err != nil {
			return nil, false
		}
		return out, true
	case []byte:
		var out map[string]any
		if err := json.Unmarshal(v, &out); err != nil {
			return nil, false
		}
		return out, true
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return nil, false
		}
		var out map[string]any
		if err := json.Unmarshal(bytes, &out); err != nil {
			return nil, false
		}
		return out, true
	}
}

func decodeEventUUID(raw any) (uuid.UUID, bool) {
	switch v := raw.(type) {
	case uuid.UUID:
		return v, true
	case string:
		id, err := uuid.Parse(v)
		return id, err == nil
	default:
		return uuid.Nil, false
	}
}
