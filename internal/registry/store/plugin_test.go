package store

import (
	"testing"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestConversationsMatch(t *testing.T) {
	userID := "user-123"
	clientID := "client-456"
	title := "Test Conversation"
	agentID := strPtr("agent-789")

	baseConv := model.Conversation{
		ID:          "conv-001",
		OwnerUserID: userID,
		ClientID:    clientID,
		Title:       []byte("encrypted-title"), // encrypted, not used in comparison
		Metadata:    map[string]interface{}{"key": "value"},
		AgentID:     agentID,
	}
	baseDecryptedTitle := "Test Conversation"

	tests := []struct {
		name       string
		existing   model.Conversation
		userID     string
		clientID   string
		title      string
		decTitle   string
		metadata   map[string]interface{}
		agentID    *string
		forkConv   *string
		forkEntry  *uuid.UUID
		startConv  *string
		startEntry *uuid.UUID
		want       bool
	}{
		{
			name:       "exact match - all fields identical",
			existing:   baseConv,
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "value"},
			agentID:    agentID,
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  nil,
			startEntry: nil,
			want:       true,
		},
		{
			name:       "different title",
			existing:   baseConv,
			userID:     userID,
			clientID:   clientID,
			title:      "Different Title",
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "value"},
			agentID:    agentID,
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  nil,
			startEntry: nil,
			want:       false,
		},
		{
			name:       "different user",
			existing:   baseConv,
			userID:     "different-user",
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "value"},
			agentID:    agentID,
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  nil,
			startEntry: nil,
			want:       false,
		},
		{
			name:       "different metadata value",
			existing:   baseConv,
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "different"},
			agentID:    agentID,
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  nil,
			startEntry: nil,
			want:       false,
		},
		{
			name:       "different metadata key",
			existing:   baseConv,
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"different": "value"},
			agentID:    agentID,
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  nil,
			startEntry: nil,
			want:       false,
		},
		{
			name: "nil vs empty metadata",
			existing: func() model.Conversation {
				c := baseConv
				c.Metadata = nil
				return c
			}(),
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{},
			agentID:    agentID,
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  nil,
			startEntry: nil,
			want:       true,
		},
		{
			name:       "different agentID",
			existing:   baseConv,
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "value"},
			agentID:    strPtr("different-agent"),
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  nil,
			startEntry: nil,
			want:       false,
		},
		{
			name: "nil vs non-nil agentID",
			existing: func() model.Conversation {
				c := baseConv
				c.AgentID = nil
				return c
			}(),
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "value"},
			agentID:    strPtr("agent-789"),
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  nil,
			startEntry: nil,
			want:       false,
		},
		{
			name: "fork parameters match",
			existing: func() model.Conversation {
				c := baseConv
				forkConvID := "parent-conv"
				forkEntryID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
				c.ForkedAtConversationID = &forkConvID
				c.ForkedAtEntryID = &forkEntryID
				return c
			}(),
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "value"},
			agentID:    agentID,
			forkConv:   strPtr("parent-conv"),
			forkEntry:  uuidPtr(uuid.MustParse("00000000-0000-0000-0000-000000000001")),
			startConv:  nil,
			startEntry: nil,
			want:       true,
		},
		{
			name: "fork conversation differs",
			existing: func() model.Conversation {
				c := baseConv
				forkConvID := "parent-conv"
				forkEntryID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
				c.ForkedAtConversationID = &forkConvID
				c.ForkedAtEntryID = &forkEntryID
				return c
			}(),
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "value"},
			agentID:    agentID,
			forkConv:   strPtr("different-parent"),
			forkEntry:  uuidPtr(uuid.MustParse("00000000-0000-0000-0000-000000000001")),
			startConv:  nil,
			startEntry: nil,
			want:       false,
		},
		{
			name: "started-by parameters match",
			existing: func() model.Conversation {
				c := baseConv
				startConvID := "original-conv"
				startEntryID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
				c.StartedByConversationID = &startConvID
				c.StartedByEntryID = &startEntryID
				return c
			}(),
			userID:     userID,
			clientID:   clientID,
			title:      title,
			decTitle:   baseDecryptedTitle,
			metadata:   map[string]interface{}{"key": "value"},
			agentID:    agentID,
			forkConv:   nil,
			forkEntry:  nil,
			startConv:  strPtr("original-conv"),
			startEntry: uuidPtr(uuid.MustParse("00000000-0000-0000-0000-000000000002")),
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConversationsMatch(&tt.existing, tt.userID, tt.clientID, tt.title, tt.decTitle,
				tt.metadata, tt.agentID, tt.forkConv, tt.forkEntry, tt.startConv, tt.startEntry)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMetadataEqual(t *testing.T) {
	tests := []struct {
		name string
		a    map[string]interface{}
		b    map[string]interface{}
		want bool
	}{
		{
			name: "both nil",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "both empty",
			a:    map[string]interface{}{},
			b:    map[string]interface{}{},
			want: true,
		},
		{
			name: "nil vs empty",
			a:    nil,
			b:    map[string]interface{}{},
			want: true,
		},
		{
			name: "identical simple values",
			a:    map[string]interface{}{"key": "value"},
			b:    map[string]interface{}{"key": "value"},
			want: true,
		},
		{
			name: "different values",
			a:    map[string]interface{}{"key": "value1"},
			b:    map[string]interface{}{"key": "value2"},
			want: false,
		},
		{
			name: "different keys",
			a:    map[string]interface{}{"key1": "value"},
			b:    map[string]interface{}{"key2": "value"},
			want: false,
		},
		{
			name: "nested objects equal",
			a:    map[string]interface{}{"outer": map[string]interface{}{"inner": "value"}},
			b:    map[string]interface{}{"outer": map[string]interface{}{"inner": "value"}},
			want: true,
		},
		{
			name: "nested objects differ",
			a:    map[string]interface{}{"outer": map[string]interface{}{"inner": "value1"}},
			b:    map[string]interface{}{"outer": map[string]interface{}{"inner": "value2"}},
			want: false,
		},
		{
			name: "arrays equal",
			a:    map[string]interface{}{"arr": []interface{}{"a", "b"}},
			b:    map[string]interface{}{"arr": []interface{}{"a", "b"}},
			want: true,
		},
		{
			name: "arrays differ",
			a:    map[string]interface{}{"arr": []interface{}{"a", "b"}},
			b:    map[string]interface{}{"arr": []interface{}{"a", "c"}},
			want: false,
		},
		{
			name: "number types",
			a:    map[string]interface{}{"num": float64(42)},
			b:    map[string]interface{}{"num": float64(42)},
			want: true,
		},
		{
			name: "boolean values",
			a:    map[string]interface{}{"flag": true},
			b:    map[string]interface{}{"flag": true},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := metadataEqual(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUUIDPointersEqual(t *testing.T) {
	id1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	id2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	tests := []struct {
		name string
		a    *uuid.UUID
		b    *uuid.UUID
		want bool
	}{
		{
			name: "both nil",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "first nil",
			a:    nil,
			b:    &id1,
			want: false,
		},
		{
			name: "second nil",
			a:    &id1,
			b:    nil,
			want: false,
		},
		{
			name: "same UUID",
			a:    &id1,
			b:    &id1,
			want: true,
		},
		{
			name: "different UUIDs",
			a:    &id1,
			b:    &id2,
			want: false,
		},
		{
			name: "same value different pointers",
			a:    uuidPtr(uuid.MustParse("00000000-0000-0000-0000-000000000001")),
			b:    uuidPtr(uuid.MustParse("00000000-0000-0000-0000-000000000001")),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uuidPointersEqual(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Helper functions for tests
func strPtr(s string) *string {
	return &s
}

func uuidPtr(id uuid.UUID) *uuid.UUID {
	return &id
}
