package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
)

// PagedEntries is a paginated list of entries.
type PagedEntries struct {
	Data        []model.Entry `json:"data"`
	AfterCursor *string       `json:"afterCursor,omitempty"`
}

// SearchResult represents a single search result.
type SearchResult struct {
	EntryID           uuid.UUID    `json:"entryId"`
	ConversationID    uuid.UUID    `json:"conversationId"`
	ConversationTitle *string      `json:"conversationTitle,omitempty"`
	Score             float64      `json:"score"`
	Kind              string       `json:"kind,omitempty"`
	Highlights        *string      `json:"highlights,omitempty"`
	Entry             *model.Entry `json:"entry,omitempty"`
}

// SearchResults is a list of search results.
type SearchResults struct {
	Data        []SearchResult `json:"data"`
	AfterCursor *string        `json:"afterCursor"`
}

// ConversationSummary is a lightweight conversation representation for lists.
type ConversationSummary struct {
	ID                     uuid.UUID              `json:"id"`
	Title                  string                 `json:"title"`
	OwnerUserID            string                 `json:"ownerUserId"`
	Metadata               map[string]interface{} `json:"metadata"`
	ConversationGroupID    uuid.UUID              `json:"-"`
	ForkedAtEntryID        *uuid.UUID             `json:"forkedAtEntryId,omitempty"`
	ForkedAtConversationID *uuid.UUID             `json:"forkedAtConversationId,omitempty"`
	CreatedAt              time.Time              `json:"createdAt"`
	UpdatedAt              time.Time              `json:"updatedAt"`
	DeletedAt              *time.Time             `json:"deletedAt,omitempty"`
	AccessLevel            model.AccessLevel      `json:"accessLevel"`
}

// ConversationForkSummary represents a fork in a list.
type ConversationForkSummary struct {
	ID                     uuid.UUID  `json:"conversationId"`
	Title                  string     `json:"title"`
	ForkedAtEntryID        *uuid.UUID `json:"forkedAtEntryId,omitempty"`
	ForkedAtConversationID *uuid.UUID `json:"forkedAtConversationId,omitempty"`
	CreatedAt              time.Time  `json:"createdAt"`
}

// ConversationDetail is the full conversation for get/create/update.
type ConversationDetail struct {
	ConversationSummary
	HasResponseInProgress bool `json:"hasResponseInProgress,omitempty"`
}

// MemoryEpochFilter filters memory entries by epoch.
type MemoryEpochFilter struct {
	Mode  string // "latest", "all", "epoch"
	Epoch *int64
}

const (
	MemoryEpochModeLatest = "latest"
	MemoryEpochModeAll    = "all"
	MemoryEpochModeEpoch  = "epoch"
)

// ParseMemoryEpochFilter parses API epoch filter values:
// ""/"latest" => latest epoch
// "all"       => all epochs
// "<number>"  => specific epoch
func ParseMemoryEpochFilter(raw string) (*MemoryEpochFilter, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	switch value {
	case "", MemoryEpochModeLatest:
		return &MemoryEpochFilter{Mode: MemoryEpochModeLatest}, nil
	case MemoryEpochModeAll:
		return &MemoryEpochFilter{Mode: MemoryEpochModeAll}, nil
	default:
		epoch, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid epoch filter %q; expected latest, all, or an integer epoch", raw)
		}
		return &MemoryEpochFilter{Mode: MemoryEpochModeEpoch, Epoch: &epoch}, nil
	}
}

// AdminConversationQuery holds parameters for admin conversation listing.
type AdminConversationQuery struct {
	Mode           model.ConversationListMode
	UserID         *string
	IncludeDeleted bool
	OnlyDeleted    bool
	DeletedAfter   *time.Time
	DeletedBefore  *time.Time
	AfterCursor    *string
	Limit          int
}

// AdminMessageQuery holds parameters for admin entry listing.
type AdminMessageQuery struct {
	AfterCursor *string
	Limit       int
	Channel     *model.Channel
	AllForks    bool
}

// AdminSearchQuery holds parameters for admin search.
type AdminSearchQuery struct {
	Query        string
	UserID       *string
	Limit        int
	IncludeEntry bool
}

// AdminAttachmentQuery holds parameters for admin attachment listing.
type AdminAttachmentQuery struct {
	UserID      *string
	EntryID     *uuid.UUID
	Status      string // linked|unlinked|expired|all
	AfterCursor *string
	Limit       int
}

// AdminAttachment is the admin API attachment representation.
type AdminAttachment struct {
	model.Attachment
	RefCount int64 `json:"refCount"`
}

// AttachmentUpdate defines mutable attachment fields.
type AttachmentUpdate struct {
	StorageKey  *string
	Filename    *string
	ContentType *string
	Size        *int64
	SHA256      *string
	Status      *string
	SourceURL   *string
	ExpiresAt   *time.Time
	EntryID     *uuid.UUID
}

// OwnershipTransferDto is the API representation of an ownership transfer.
type OwnershipTransferDto struct {
	ID                  uuid.UUID `json:"id"`
	ConversationGroupID uuid.UUID `json:"-"`
	ConversationID      uuid.UUID `json:"conversationId"`
	FromUserID          string    `json:"fromUserId"`
	ToUserID            string    `json:"toUserId"`
	CreatedAt           time.Time `json:"createdAt"`
}

// MemoryStore defines the primary data access interface for the memory service.
type MemoryStore interface {
	// Conversations
	CreateConversation(ctx context.Context, userID string, title string, metadata map[string]interface{}, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*ConversationDetail, error)
	// CreateConversationWithID creates a conversation with the given ID. Used by gRPC AppendEntry for fork-on-append.
	CreateConversationWithID(ctx context.Context, userID string, convID uuid.UUID, title string, metadata map[string]interface{}, forkedAtConversationID *uuid.UUID, forkedAtEntryID *uuid.UUID) (*ConversationDetail, error)
	ListConversations(ctx context.Context, userID string, query *string, afterCursor *string, limit int, mode model.ConversationListMode) ([]ConversationSummary, *string, error)
	GetConversation(ctx context.Context, userID string, conversationID uuid.UUID) (*ConversationDetail, error)
	UpdateConversation(ctx context.Context, userID string, conversationID uuid.UUID, title *string, metadata map[string]interface{}) (*ConversationDetail, error)
	DeleteConversation(ctx context.Context, userID string, conversationID uuid.UUID) error

	// Memberships
	ListMemberships(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error)
	ShareConversation(ctx context.Context, userID string, conversationID uuid.UUID, targetUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error)
	UpdateMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string, accessLevel model.AccessLevel) (*model.ConversationMembership, error)
	DeleteMembership(ctx context.Context, userID string, conversationID uuid.UUID, memberUserID string) error

	// Forks
	ListForks(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]ConversationForkSummary, *string, error)

	// Ownership Transfers
	ListPendingTransfers(ctx context.Context, userID string, role string, afterCursor *string, limit int) ([]OwnershipTransferDto, *string, error)
	GetTransfer(ctx context.Context, userID string, transferID uuid.UUID) (*OwnershipTransferDto, error)
	CreateOwnershipTransfer(ctx context.Context, userID string, conversationID uuid.UUID, toUserID string) (*OwnershipTransferDto, error)
	AcceptTransfer(ctx context.Context, userID string, transferID uuid.UUID) error
	DeleteTransfer(ctx context.Context, userID string, transferID uuid.UUID) error

	// Entries
	GetEntries(ctx context.Context, userID string, conversationID uuid.UUID, afterEntryID *string, limit int, channel *model.Channel, epochFilter *MemoryEpochFilter, clientID *string, allForks bool) (*PagedEntries, error)
	AppendEntries(ctx context.Context, userID string, conversationID uuid.UUID, entries []CreateEntryRequest, clientID *string, epoch *int64) ([]model.Entry, error)
	GetEntryGroupID(ctx context.Context, entryID uuid.UUID) (uuid.UUID, error)
	SyncAgentEntry(ctx context.Context, userID string, conversationID uuid.UUID, entry CreateEntryRequest, clientID string) (*SyncResult, error)

	// Indexing
	IndexEntries(ctx context.Context, entries []IndexEntryRequest) (*IndexConversationsResponse, error)
	ListUnindexedEntries(ctx context.Context, limit int, afterCursor *string) ([]model.Entry, *string, error)
	FindEntriesPendingVectorIndexing(ctx context.Context, limit int) ([]model.Entry, error)
	SetIndexedAt(ctx context.Context, entryID uuid.UUID, conversationGroupID uuid.UUID, indexedAt time.Time) error

	// Search
	ListConversationGroupIDs(ctx context.Context, userID string) ([]uuid.UUID, error)
	FetchSearchResultDetails(ctx context.Context, userID string, entryIDs []uuid.UUID, includeEntry bool) ([]SearchResult, error)
	SearchEntries(ctx context.Context, userID string, query string, limit int, includeEntry bool) (*SearchResults, error)

	// Admin
	AdminListConversations(ctx context.Context, query AdminConversationQuery) ([]ConversationSummary, *string, error)
	AdminGetConversation(ctx context.Context, conversationID uuid.UUID) (*ConversationDetail, error)
	AdminDeleteConversation(ctx context.Context, conversationID uuid.UUID) error
	AdminRestoreConversation(ctx context.Context, conversationID uuid.UUID) error
	AdminGetEntries(ctx context.Context, conversationID uuid.UUID, query AdminMessageQuery) (*PagedEntries, error)
	AdminListMemberships(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.ConversationMembership, *string, error)
	AdminListForks(ctx context.Context, conversationID uuid.UUID, afterCursor *string, limit int) ([]ConversationForkSummary, *string, error)
	AdminSearchEntries(ctx context.Context, query AdminSearchQuery) (*SearchResults, error)
	AdminListAttachments(ctx context.Context, query AdminAttachmentQuery) ([]AdminAttachment, *string, error)
	AdminGetAttachment(ctx context.Context, attachmentID uuid.UUID) (*AdminAttachment, error)
	AdminDeleteAttachment(ctx context.Context, attachmentID uuid.UUID) error

	// Attachments
	CreateAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachment model.Attachment) (*model.Attachment, error)
	UpdateAttachment(ctx context.Context, userID string, attachmentID uuid.UUID, update AttachmentUpdate) (*model.Attachment, error)
	ListAttachments(ctx context.Context, userID string, conversationID uuid.UUID, afterCursor *string, limit int) ([]model.Attachment, *string, error)
	GetAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) (*model.Attachment, error)
	DeleteAttachment(ctx context.Context, userID string, conversationID uuid.UUID, attachmentID uuid.UUID) error

	// Eviction
	FindEvictableGroupIDs(ctx context.Context, cutoff time.Time, limit int) ([]uuid.UUID, error)
	CountEvictableGroups(ctx context.Context, cutoff time.Time) (int64, error)
	HardDeleteConversationGroups(ctx context.Context, groupIDs []uuid.UUID) error

	// Tasks
	CreateTask(ctx context.Context, taskType string, taskBody map[string]interface{}) error
	ClaimReadyTasks(ctx context.Context, limit int) ([]model.Task, error)
	DeleteTask(ctx context.Context, taskID uuid.UUID) error
	FailTask(ctx context.Context, taskID uuid.UUID, errMsg string, retryDelay time.Duration) error

	// Admin attachment lookup by storage key (for token-based downloads)
	AdminGetAttachmentByStorageKey(ctx context.Context, storageKey string) (*AdminAttachment, error)
}

// CreateEntryRequest is the input for creating an entry.
type CreateEntryRequest struct {
	Content                json.RawMessage `json:"content"`
	ContentType            string          `json:"contentType"`
	Channel                string          `json:"channel"`
	IndexedContent         *string         `json:"indexedContent,omitempty"`
	Role                   *string         `json:"role,omitempty"`
	UserID                 *string         `json:"userId,omitempty"`
	ForkedAtConversationID *uuid.UUID      `json:"forkedAtConversationId,omitempty"`
	ForkedAtEntryID        *uuid.UUID      `json:"forkedAtEntryId,omitempty"`
}

// SyncResult is the result of a sync operation.
type SyncResult struct {
	Entry            *model.Entry `json:"entry,omitempty"`
	Epoch            *int64       `json:"epoch"`
	NoOp             bool         `json:"noOp"`
	EpochIncremented bool         `json:"epochIncremented"`
}

// IndexEntryRequest is the input for indexing an entry.
type IndexEntryRequest struct {
	EntryID        uuid.UUID `json:"entryId"`
	ConversationID uuid.UUID `json:"conversationId"`
	IndexedContent string    `json:"indexedContent"`
}

// IndexConversationsResponse is the result of indexing.
type IndexConversationsResponse struct {
	Indexed int `json:"indexed"`
}

// Loader creates a MemoryStore from config.
type Loader func(ctx context.Context) (MemoryStore, error)

// Plugin represents a store plugin.
type Plugin struct {
	Name   string
	Loader Loader
}

var plugins []Plugin

// Register adds a store plugin.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

// Names returns all registered store plugin names.
func Names() []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

// Select returns the loader for the named store plugin.
func Select(name string) (Loader, error) {
	for _, p := range plugins {
		if p.Name == name {
			return p.Loader, nil
		}
	}
	return nil, fmt.Errorf("unknown store %q; valid: %v", name, Names())
}
