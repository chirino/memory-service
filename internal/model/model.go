package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Channel represents the type of entry channel.
type Channel string

const (
	ChannelHistory Channel = "history"
	ChannelMemory  Channel = "memory"
)

// AccessLevel represents the level of access a user has to a conversation group.
type AccessLevel string

const (
	AccessLevelOwner   AccessLevel = "owner"
	AccessLevelManager AccessLevel = "manager"
	AccessLevelWriter  AccessLevel = "writer"
	AccessLevelReader  AccessLevel = "reader"
)

// IsAtLeast returns true if the access level is at least the given level.
func (a AccessLevel) IsAtLeast(level AccessLevel) bool {
	return accessRank(a) >= accessRank(level)
}

func accessRank(level AccessLevel) int {
	switch level {
	case AccessLevelOwner:
		return 4
	case AccessLevelManager:
		return 3
	case AccessLevelWriter:
		return 2
	case AccessLevelReader:
		return 1
	default:
		return 0
	}
}

// ConversationListMode controls which conversations from each fork tree are returned.
type ConversationListMode string

const (
	ListModeAll        ConversationListMode = "all"
	ListModeRoots      ConversationListMode = "roots"
	ListModeLatestFork ConversationListMode = "latest-fork"
)

// ConversationGroup is the root of a fork tree.
type ConversationGroup struct {
	ID        uuid.UUID  `json:"id"                  gorm:"primaryKey;type:uuid"`
	CreatedAt time.Time  `json:"createdAt"           gorm:"not null;default:now()"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
}

func (ConversationGroup) TableName() string { return "conversation_groups" }

// Conversation represents a single conversation within a group.
type Conversation struct {
	ID                     uuid.UUID              `json:"id"                               gorm:"primaryKey;type:uuid"`
	Title                  []byte                 `json:"-"                                gorm:"type:bytea"` // encrypted
	OwnerUserID            string                 `json:"ownerUserId"                      gorm:"not null"`
	Metadata               map[string]interface{} `json:"metadata"                         gorm:"type:jsonb;serializer:json;not null;default:'{}'"` // JSONB
	ConversationGroupID    uuid.UUID              `json:"-"                                gorm:"not null;type:uuid"`
	ConversationGroup      *ConversationGroup     `json:"-"                                gorm:"foreignKey:ConversationGroupID"`
	ForkedAtEntryID        *uuid.UUID             `json:"forkedAtEntryId,omitempty"        gorm:"type:uuid"`
	ForkedAtConversationID *uuid.UUID             `json:"forkedAtConversationId,omitempty" gorm:"type:uuid"`
	CreatedAt              time.Time              `json:"createdAt"                        gorm:"not null;default:now()"`
	UpdatedAt              time.Time              `json:"updatedAt"                        gorm:"not null;default:now()"`
	VectorizedAt           *time.Time             `json:"vectorizedAt,omitempty"`
	DeletedAt              *time.Time             `json:"deletedAt,omitempty"`
}

func (Conversation) TableName() string { return "conversations" }

// ConversationMembership tracks per-user access to a conversation group.
type ConversationMembership struct {
	ConversationGroupID uuid.UUID   `json:"-"           gorm:"primaryKey;type:uuid"`
	UserID              string      `json:"userId"      gorm:"primaryKey"`
	AccessLevel         AccessLevel `json:"accessLevel" gorm:"not null"`
	CreatedAt           time.Time   `json:"createdAt"   gorm:"not null;default:now()"`
}

func (ConversationMembership) TableName() string { return "conversation_memberships" }

// Entry represents a message or memory entry in a conversation.
type Entry struct {
	ID                  uuid.UUID  `json:"id"                       gorm:"primaryKey;type:uuid"`
	ConversationID      uuid.UUID  `json:"conversationId"           gorm:"not null;type:uuid"`
	ConversationGroupID uuid.UUID  `json:"-"                        gorm:"primaryKey;type:uuid"`
	UserID              *string    `json:"userId,omitempty"`
	ClientID            *string    `json:"clientId,omitempty"`
	Channel             Channel    `json:"channel"                  gorm:"not null"`
	Epoch               *int64     `json:"epoch,omitempty"`
	ContentType         string     `json:"contentType"              gorm:"not null"`
	Content             []byte     `json:"-"                        gorm:"type:bytea;not null"` // encrypted
	IndexedContent      *string    `json:"indexedContent,omitempty"`
	IndexedAt           *time.Time `json:"indexedAt,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"                gorm:"not null;default:now()"`
}

func (Entry) TableName() string { return "entries" }

// MarshalJSON serializes Entry to JSON, including the decrypted content as a raw JSON value.
// Content is stored as []byte with json:"-" to prevent GORM from leaking encrypted bytes,
// but API responses need to include the decrypted content.
func (e Entry) MarshalJSON() ([]byte, error) {
	type Alias Entry // avoid recursion
	aux := struct {
		Alias
		Content json.RawMessage `json:"content"`
	}{
		Alias: Alias(e),
	}
	if len(e.Content) > 0 {
		// Content is already a JSON value (array/object), use as-is
		if json.Valid(e.Content) {
			aux.Content = e.Content
		} else {
			// Fallback: treat as a plain string
			aux.Content, _ = json.Marshal(string(e.Content))
		}
	}
	return json.Marshal(aux)
}

// UnmarshalJSON restores Entry from JSON including the decrypted content field.
// This keeps cache round-trips lossless for model.Entry values.
func (e *Entry) UnmarshalJSON(data []byte) error {
	type Alias Entry
	aux := struct {
		Alias
		Content json.RawMessage `json:"content"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	*e = Entry(aux.Alias)
	if len(aux.Content) == 0 || string(aux.Content) == "null" {
		e.Content = nil
		return nil
	}

	// If content was encoded as a JSON string fallback, unquote it back to raw bytes.
	if len(aux.Content) > 0 && aux.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(aux.Content, &s); err == nil {
			e.Content = []byte(s)
			return nil
		}
	}

	e.Content = append([]byte(nil), aux.Content...)
	return nil
}

// OwnershipTransfer represents a pending conversation ownership transfer.
type OwnershipTransfer struct {
	ID                  uuid.UUID `json:"id"         gorm:"primaryKey;type:uuid"`
	ConversationGroupID uuid.UUID `json:"-"          gorm:"not null;type:uuid"`
	FromUserID          string    `json:"fromUserId" gorm:"not null"`
	ToUserID            string    `json:"toUserId"   gorm:"not null"`
	CreatedAt           time.Time `json:"createdAt"  gorm:"not null;default:now()"`
}

func (OwnershipTransfer) TableName() string { return "conversation_ownership_transfers" }

// Task represents a background task in the task queue.
type Task struct {
	ID         uuid.UUID              `json:"id"                  gorm:"primaryKey;type:uuid"`
	TaskName   *string                `json:"taskName,omitempty"  gorm:"unique"`
	TaskType   string                 `json:"taskType"            gorm:"not null"`
	TaskBody   map[string]interface{} `json:"taskBody"            gorm:"type:jsonb;serializer:json;not null"`
	CreatedAt  time.Time              `json:"createdAt"           gorm:"not null;default:now()"`
	RetryAt    time.Time              `json:"retryAt"             gorm:"not null;default:now()"`
	LastError  *string                `json:"lastError,omitempty"`
	RetryCount int                    `json:"retryCount"          gorm:"not null;default:0"`
}

func (Task) TableName() string { return "tasks" }

// Attachment represents file attachment metadata.
type Attachment struct {
	ID          uuid.UUID  `json:"id"                   gorm:"primaryKey;type:uuid"`
	StorageKey  *string    `json:"storageKey,omitempty"`
	Filename    *string    `json:"filename,omitempty"`
	ContentType string     `json:"contentType"          gorm:"not null"`
	Size        *int64     `json:"size,omitempty"`
	SHA256      *string    `json:"sha256,omitempty"`
	UserID      string     `json:"userId"               gorm:"not null"`
	EntryID     *uuid.UUID `json:"entryId,omitempty"    gorm:"type:uuid"`
	Status      string     `json:"status"               gorm:"not null;default:'ready'"`
	SourceURL   *string    `json:"sourceUrl,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"            gorm:"not null;default:now()"`
	DeletedAt   *time.Time `json:"deletedAt,omitempty"`
}

func (Attachment) TableName() string { return "attachments" }
