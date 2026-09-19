package clickhouse

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// analyticsRecordFromResource converts a full admin OpenAPI resource carried
// by the event stream into the processor's internal normalized representation.
func analyticsRecordFromResource(kind string, raw json.RawMessage) (*pb.AnalyticsRecord, error) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("decode full %s resource: %w", kind, err)
	}
	id := firstString(data, "id")
	if id == "" {
		return nil, fmt.Errorf("full %s resource has no id", kind)
	}
	switch kind {
	case "conversation":
		value := &pb.AnalyticsConversationRecord{
			Id: id, ConversationGroupId: firstString(data, "conversationGroupId"),
			OwnerUserId: firstString(data, "ownerUserId"), ClientId: firstString(data, "clientId"),
			CreatedAt: timestampFromResource(data, "createdAt"), UpdatedAt: timestampFromResource(data, "updatedAt"),
		}
		value.AgentId = optionalResourceString(data, "agentId")
		value.Title = optionalResourceString(data, "title")
		value.ForkedAtConversationId = optionalResourceString(data, "forkedAtConversationId")
		value.ForkedAtEntryId = optionalResourceString(data, "forkedAtEntryId")
		value.StartedByConversationId = optionalResourceString(data, "startedByConversationId")
		value.StartedByEntryId = optionalResourceString(data, "startedByEntryId")
		if metadata, ok := data["metadata"].(map[string]any); ok {
			value.Metadata, _ = structpb.NewStruct(metadata)
		}
		if firstBool(data, "archived") {
			value.ArchivedAt = timestamppb.New(time.Now().UTC())
		}
		return &pb.AnalyticsRecord{Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_CONVERSATION, Id: id}, SnapshotAvailable: true, Record: &pb.AnalyticsRecord_Conversation{Conversation: value}}, nil
	case "entry":
		value := &pb.AnalyticsEntryRecord{
			Id: id, ConversationId: firstString(data, "conversationId"), ConversationGroupId: firstString(data, "conversationGroupId"),
			Channel: resourceChannel(firstString(data, "channel")), ContentType: firstString(data, "contentType"),
			CreatedAt: timestampFromResource(data, "createdAt"),
		}
		value.UserId = optionalResourceString(data, "userId")
		value.ClientId = optionalResourceString(data, "clientId")
		value.AgentId = optionalResourceString(data, "agentId")
		value.IndexedContent = optionalResourceString(data, "indexedContent")
		if epoch, ok := resourceInt64(data["epoch"]); ok {
			value.Epoch = &epoch
		}
		if seq, ok := resourceUint32(data["seq"]); ok {
			value.Seq = &seq
		}
		value.IndexedAt = timestampFromOptionalResource(data, "indexedAt")
		if content, ok := data["content"]; ok {
			value.Content, _ = structpb.NewValue(content)
		}
		return &pb.AnalyticsRecord{Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_ENTRY, Id: id}, SnapshotAvailable: true, Record: &pb.AnalyticsRecord_Entry{Entry: value}}, nil
	case "memory":
		namespace := resourceStrings(data["namespace"])
		key := firstString(data, "key")
		logicalDigest := sha256.Sum256([]byte(strings.Join(namespace, "\x1e") + "\n" + key))
		value := &pb.AnalyticsMemoryRecord{
			Id: id, Namespace: namespace, Key: key, MemoryKind: firstString(data, "kind"),
			Revision: resourceInt64OrZero(data["revision"]), CreatedAt: timestampFromResource(data, "createdAt"), LogicalIdSource: logicalDigest[:],
		}
		value.ExpiresAt = timestampFromOptionalResource(data, "expiresAt")
		value.ArchivedAt = timestampFromOptionalResource(data, "archivedAt")
		if value.ArchivedAt == nil && firstBool(data, "archived") {
			value.ArchivedAt = timestamppb.New(time.Now().UTC())
		}
		if payload, ok := data["value"].(map[string]any); ok {
			value.Value, _ = structpb.NewStruct(payload)
		}
		if attributes, ok := data["attributes"].(map[string]any); ok {
			value.Attributes, _ = structpb.NewStruct(attributes)
		}
		return &pb.AnalyticsRecord{Reference: &pb.AnalyticsResourceReference{Kind: pb.AnalyticsResourceKind_ANALYTICS_RESOURCE_KIND_MEMORY, Id: id}, SnapshotAvailable: true, Record: &pb.AnalyticsRecord_Memory{Memory: value}}, nil
	default:
		return nil, fmt.Errorf("unsupported full resource kind %q", kind)
	}
}

func optionalResourceString(data map[string]any, key string) *string {
	value, ok := data[key].(string)
	if !ok {
		return nil
	}
	return &value
}

func timestampFromResource(data map[string]any, key string) *timestamppb.Timestamp {
	value := timestampFromOptionalResource(data, key)
	if value == nil {
		return timestamppb.New(time.Unix(0, 0).UTC())
	}
	return value
}

func timestampFromOptionalResource(data map[string]any, key string) *timestamppb.Timestamp {
	raw, ok := data[key].(string)
	if !ok {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil
	}
	return timestamppb.New(parsed.UTC())
}

func resourceChannel(value string) pb.Channel {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "history":
		return pb.Channel_HISTORY
	case "context":
		return pb.Channel_CONTEXT
	case "journal":
		return pb.Channel_JOURNAL
	default:
		return pb.Channel_CHANNEL_UNSPECIFIED
	}
}

func resourceInt64(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok {
		return 0, false
	}
	return int64(number), true
}

func resourceInt64OrZero(value any) int64 {
	result, _ := resourceInt64(value)
	return result
}

func resourceUint32(value any) (uint32, bool) {
	number, ok := resourceInt64(value)
	if !ok || number < 0 || number > 1<<32-1 {
		return 0, false
	}
	return uint32(number), true
}

func resourceStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
