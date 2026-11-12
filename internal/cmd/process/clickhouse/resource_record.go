package clickhouse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
)

// observation is the one normalized boundary for bootstrap, replay, and live events.
// Record contains only the declared, payload-policy-filtered Rego fields.
type observation struct {
	Kind, ID, ConversationID, GroupID       string
	Action, Change, ContentType, MemoryKind string
	Cursor                                  string
	OccurredAt                              time.Time
	Deleted, HardDeleted                    bool
	Record                                  map[string]any
	UserID, AccessLevel                     string
	CreatedAt                               *time.Time
}

func decodeObservation(cfg Config, event processruntime.EventEnvelope) (observation, error) {
	o := observation{Kind: event.Kind, Action: event.Event, Change: event.Change, Cursor: event.Cursor, OccurredAt: event.Time.UTC()}
	if len(event.Data) > cfg.MaxRecordBytes {
		return o, errors.New("record_too_large")
	}
	data := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(event.Data))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return o, fmt.Errorf("invalid_resource_json: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return o, errors.New("invalid_resource_json: trailing input")
	}
	if o.Change == "" {
		o.Change = stringField(data, "change")
	}
	if o.Change == "" {
		o.Change = o.Action
	}
	o.Deleted = o.Action == "deleted" || o.Change == "evicted" || o.Change == "hard_deleted"
	o.HardDeleted = o.Change == "hard_deleted" || o.Action == "deleted" && (o.Kind == "conversation" || o.Kind == "membership")
	full := stringField(data, "id") != "" && !o.Deleted
	if full {
		o.ID = stringField(data, "id")
		o.ConversationID = stringField(data, "conversationId")
		o.GroupID = stringField(data, "conversationGroupId")
	} else {
		o.ConversationID = stringField(data, "conversation")
		o.GroupID = stringField(data, "conversation_group")
		switch o.Kind {
		case "conversation":
			o.ID = o.ConversationID
		case "entry":
			o.ID = stringField(data, "entry")
		case "memory":
			o.ID = stringField(data, "memory")
		}
	}
	switch o.Kind {
	case "conversation":
		o.ConversationID = o.ID
	case "entry":
	case "memory":
		o.GroupID = ""
		o.ConversationID = ""
	case "membership":
		if full {
			o.UserID = stringField(data, "userId")
			o.AccessLevel = stringField(data, "accessLevel")
		} else {
			o.UserID = stringField(data, "user")
			o.AccessLevel = stringField(data, "role")
		}
		group, err := uuid.Parse(o.GroupID)
		if err != nil || group == uuid.Nil || o.UserID == "" {
			return o, errors.New("missing_membership_identity")
		}
		identity := model.MembershipResourceID(group, o.UserID)
		if full && o.ID != identity {
			return o, errors.New("invalid_membership_identity")
		}
		o.ID = identity
	default:
		return o, errors.New("unsupported_resource_kind")
	}
	if o.ID == "" {
		return o, errors.New("missing_resource_identity")
	}
	if o.Kind == "entry" || o.Kind == "conversation" || o.Kind == "membership" {
		group, err := uuid.Parse(o.GroupID)
		if err != nil || group == uuid.Nil {
			return o, errors.New("missing_group_identity")
		}
		o.GroupID = group.String()
		if o.Kind != "membership" && o.ConversationID == "" {
			return o, errors.New("missing_conversation_identity")
		}
	}
	o.ContentType = stringField(data, "contentType")
	if !full {
		o.ContentType = stringField(data, "entry_content_type")
	}
	o.MemoryKind = stringField(data, "kind")
	if !full {
		o.MemoryKind = stringField(data, "memoryKind")
	}
	if !full {
		return o, nil
	}
	r := map[string]any{"id": o.ID}
	fields := []string{}
	switch o.Kind {
	case "conversation":
		fields = []string{"conversationGroupId", "ownerUserId", "clientId", "agentId", "createdAt", "updatedAt", "archived", "forkedAtConversationId", "forkedAtEntryId", "startedByConversationId", "startedByEntryId"}
	case "entry":
		fields = []string{"conversationId", "conversationGroupId", "userId", "clientId", "agentId", "channel", "contentType", "createdAt", "epoch", "seq", "indexedAt"}
	case "memory":
		fields = []string{"namespace", "key", "kind", "revision", "createdAt", "expiresAt", "archived"}
	case "membership":
		fields = []string{"conversationGroupId", "userId", "accessLevel", "createdAt"}
	}
	for _, field := range fields {
		if value, ok := data[field]; ok && value != nil {
			r[field] = value
		}
	}
	if o.Kind == "conversation" || o.Kind == "entry" || o.Kind == "membership" {
		r["conversationGroupId"] = o.GroupID
	}
	for _, field := range []string{"createdAt", "updatedAt", "expiresAt", "indexedAt"} {
		v, present := r[field]
		required := field == "createdAt" || field == "updatedAt" && o.Kind == "conversation"
		if !present {
			if required {
				return o, fmt.Errorf("invalid_source_timestamp: %s required", field)
			}
			continue
		}
		raw, ok := v.(string)
		if !ok {
			return o, fmt.Errorf("invalid_source_timestamp: %s", field)
		}
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return o, fmt.Errorf("invalid_source_timestamp: %s", field)
		}
		r[field] = parsed.UTC().Format(time.RFC3339Nano)
		if field == "createdAt" {
			o.CreatedAt = &parsed
		}
	}
	for _, field := range []string{"epoch", "seq", "revision"} {
		if value, present := r[field]; present {
			number, ok := value.(json.Number)
			if !ok {
				return o, fmt.Errorf("invalid_source_integer: %s", field)
			}
			if field == "seq" {
				parsed, err := strconv.ParseUint(string(number), 10, 32)
				if err != nil {
					return o, fmt.Errorf("invalid_source_integer: %s", field)
				}
				r[field] = parsed
			} else {
				parsed, err := number.Int64()
				if err != nil {
					return o, fmt.Errorf("invalid_source_integer: %s", field)
				}
				r[field] = parsed
			}
		} else if field == "revision" && o.Kind == "memory" {
			return o, errors.New("invalid_source_integer: revision required")
		}
	}
	if o.Kind == "conversation" || o.Kind == "memory" {
		archived := false
		if value, present := data["archived"]; present {
			var ok bool
			archived, ok = value.(bool)
			if !ok {
				return o, errors.New("invalid_source_boolean: archived")
			}
		}
		r["archived"] = archived
	}
	if o.Kind == "entry" {
		channel := strings.ToUpper(stringField(data, "channel"))
		if channel != "HISTORY" && channel != "CONTEXT" && channel != "JOURNAL" {
			return o, errors.New("invalid_entry_channel")
		}
		r["channel"] = channel
	}
	if o.Kind == "conversation" {
		metadata := map[string]any{}
		if values, ok := data["metadata"].(map[string]any); ok {
			for _, key := range cfg.MetadataKeys {
				if value, present := values[key]; present {
					metadata[key] = value
				}
			}
		}
		r["metadata"] = metadata
		if cfg.PayloadMode == PayloadFull {
			if value, present := data["title"]; present {
				r["title"] = value
			}
		}
	}
	if cfg.PayloadMode != PayloadMetadata {
		if o.Kind == "entry" {
			if value, present := data["content"]; present {
				r["content"] = value
			}
		}
		if o.Kind == "memory" {
			for _, field := range []string{"value", "attributes"} {
				if value, present := data[field]; present {
					r[field] = value
				}
			}
		}
	}
	if o.Kind == "entry" && cfg.PayloadMode == PayloadFull {
		if value, present := data["indexedContent"]; present {
			r["indexedContent"] = value
		}
	}
	if o.Kind == "membership" && !validAccessLevel(o.AccessLevel) {
		return o, errors.New("invalid_membership_access_level")
	}
	o.Record = r
	return o, nil
}

func stringField(data map[string]any, field string) string {
	value, _ := data[field].(string)
	return value
}
func validAccessLevel(value string) bool {
	return value == "owner" || value == "manager" || value == "writer" || value == "reader"
}
