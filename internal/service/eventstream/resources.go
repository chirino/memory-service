package eventstream

import (
	"encoding/json"

	"github.com/chirino/memory-service/internal/model"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
)

// AgentConversationResource returns the public OpenAPI Conversation shape.
func AgentConversationResource(conv *registrystore.ConversationDetail) map[string]any {
	if conv == nil {
		return nil
	}
	result := conversationResource(&conv.ConversationSummary, false)
	if conv.HasResponseInProgress {
		result["hasResponseInProgress"] = true
	}
	return result
}

// AdminConversationResource returns the admin OpenAPI AdminConversation shape.
func AdminConversationResource(conv *registrystore.ConversationDetail) map[string]any {
	if conv == nil {
		return nil
	}
	result := conversationResource(&conv.ConversationSummary, true)
	if conv.HasResponseInProgress {
		result["hasResponseInProgress"] = true
	}
	return result
}

// AdminConversationSummaryResource returns an admin conversation resource from
// a current-state scan, which does not load transient response progress.
func AdminConversationSummaryResource(conv *registrystore.ConversationSummary) map[string]any {
	return conversationResource(conv, true)
}

func conversationResource(conv *registrystore.ConversationSummary, admin bool) map[string]any {
	if conv == nil {
		return nil
	}
	metadata := conv.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	result := map[string]any{
		"id":          conv.ID,
		"title":       conv.Title,
		"ownerUserId": conv.OwnerUserID,
		"metadata":    metadata,
		"createdAt":   conv.CreatedAt.UTC(),
		"updatedAt":   conv.UpdatedAt.UTC(),
		"archived":    conv.ArchivedAt != nil,
		"accessLevel": conv.AccessLevel,
	}
	if conv.AgentID != nil {
		result["agentId"] = *conv.AgentID
	}
	if conv.ForkedAtEntryID != nil {
		result["forkedAtEntryId"] = conv.ForkedAtEntryID.String()
	}
	if conv.ForkedAtConversationID != nil {
		result["forkedAtConversationId"] = *conv.ForkedAtConversationID
	}
	if conv.StartedByConversationID != nil {
		result["startedByConversationId"] = *conv.StartedByConversationID
	}
	if conv.StartedByEntryID != nil {
		result["startedByEntryId"] = conv.StartedByEntryID.String()
	}
	if admin {
		result["clientId"] = conv.ClientID
		result["conversationGroupId"] = conv.ConversationGroupID.String()
	}
	return result
}

// AgentEntryResource returns the public OpenAPI Entry shape.
func AgentEntryResource(entry *model.Entry) map[string]any {
	return entryResource(entry, false)
}

// AdminEntryResource returns the admin OpenAPI AdminEntry shape.
func AdminEntryResource(entry *model.Entry) map[string]any {
	return entryResource(entry, true)
}

func entryResource(entry *model.Entry, admin bool) map[string]any {
	if entry == nil {
		return nil
	}
	var content any = []any{}
	if len(entry.Content) > 0 {
		_ = json.Unmarshal(entry.Content, &content)
	}
	result := map[string]any{
		"id":             entry.ID.String(),
		"conversationId": entry.ConversationID,
		"channel":        entry.Channel,
		"contentType":    entry.ContentType,
		"content":        content,
		"createdAt":      entry.CreatedAt.UTC(),
	}
	if entry.UserID != nil {
		result["userId"] = *entry.UserID
	}
	if admin && entry.ClientID != nil {
		result["clientId"] = *entry.ClientID
	}
	if entry.AgentID != nil {
		result["agentId"] = *entry.AgentID
	}
	if entry.Epoch != nil {
		result["epoch"] = *entry.Epoch
	}
	if entry.Seq != nil {
		result["seq"] = *entry.Seq
	}
	if entry.IndexedContent != nil {
		result["indexedContent"] = *entry.IndexedContent
	}
	if entry.IndexedAt != nil {
		result["indexedAt"] = entry.IndexedAt.UTC()
	}
	if admin {
		result["conversationGroupId"] = entry.ConversationGroupID.String()
	}
	return result
}

// AdminMemoryResource returns the admin OpenAPI AdminMemoryItem shape.
func AdminMemoryResource(item *registryepisodic.MemoryItem) map[string]any {
	if item == nil {
		return nil
	}
	result := map[string]any{
		"id":        item.ID.String(),
		"namespace": append([]string(nil), item.Namespace...),
		"key":       item.Key,
		"kind":      item.MemoryKind,
		"createdAt": item.CreatedAt.UTC(),
		"archived":  item.ArchivedAt != nil,
		"revision":  item.Revision,
	}
	if item.Value != nil {
		result["value"] = item.Value
	}
	if item.Attributes != nil {
		result["attributes"] = item.Attributes
	}
	if item.Score != nil {
		result["score"] = *item.Score
	}
	if len(item.MatchedQueries) > 0 {
		result["matchedQueries"] = append([]string(nil), item.MatchedQueries...)
	}
	if item.ExpiresAt != nil {
		result["expiresAt"] = item.ExpiresAt.UTC()
	}
	if item.ArchivedAt != nil {
		result["archivedAt"] = item.ArchivedAt.UTC()
	}
	if item.Usage != nil {
		usage := map[string]any{"fetchCount": item.Usage.FetchCount}
		if !item.Usage.LastFetchedAt.IsZero() {
			usage["lastFetchedAt"] = item.Usage.LastFetchedAt.UTC()
		}
		result["usage"] = usage
	}
	return result
}

// EventChange extracts the summary-only change marker before a full resource
// replaces the data field.
func EventChange(data any) string {
	var values map[string]any
	switch value := data.(type) {
	case map[string]any:
		values = value
	case json.RawMessage:
		_ = json.Unmarshal(value, &values)
	case []byte:
		_ = json.Unmarshal(value, &values)
	}
	if change, ok := values["change"].(string); ok {
		return change
	}
	return ""
}
