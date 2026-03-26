// Package episodic defines the EpisodicStore interface and its registry.
// EpisodicStore is the primary data access interface for namespaced episodic memories,
// separate from the conversation/entry MemoryStore.
package episodic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrAdminStatsSummaryUnsupported = errors.New("admin stats summary unsupported")

// PutMemoryRequest is the input for creating or updating a memory.
type PutMemoryRequest struct {
	// Namespace is the decoded namespace segments.
	Namespace []string `json:"namespace"`
	// Key uniquely identifies the memory within the namespace.
	Key string `json:"key"`
	// Value is the arbitrary JSON value to store (encrypted at rest).
	Value map[string]interface{} `json:"value"`
	// TTLSeconds is the optional time-to-live in seconds. 0 = no expiry.
	TTLSeconds int `json:"ttl_seconds,omitempty"`
	// Index is the caller-provided, redacted text payload to embed.
	// Empty or nil means no vector indexing for this memory version.
	Index map[string]string `json:"index,omitempty"`
	// PolicyAttributes are the OPA-extracted plaintext attributes (set by the handler).
	PolicyAttributes map[string]interface{} `json:"-"`
}

// MemoryItem is the external representation of an active memory (returned by GET / search).
type MemoryItem struct {
	ID         uuid.UUID              `json:"id"`
	Namespace  []string               `json:"namespace"`
	Key        string                 `json:"key"`
	Value      map[string]interface{} `json:"value,omitempty"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
	Score      *float64               `json:"score,omitempty"` // nil for non-vector results
	Usage      *MemoryUsage           `json:"usage,omitempty"`
	CreatedAt  time.Time              `json:"createdAt"`
	ExpiresAt  *time.Time             `json:"expiresAt"`
	ArchivedAt *time.Time             `json:"archivedAt,omitempty"`
}

// MemoryUsage stores usage counters for one (namespace, key) pair.
type MemoryUsage struct {
	FetchCount    int64     `json:"fetchCount"`
	LastFetchedAt time.Time `json:"lastFetchedAt"`
}

type AdminMemoryStats struct {
	Total            int64      `json:"total"`
	Archived         int64      `json:"archived"`
	OldestArchivedAt *time.Time `json:"oldestArchivedAt"`
}

type AdminStatsSummary struct {
	Memories AdminMemoryStats `json:"memories"`
}

type AdminStatsSummaryProvider interface {
	AdminStatsSummary(ctx context.Context) (*AdminStatsSummary, error)
}

// MemoryKey identifies a memory by decoded namespace + key.
type MemoryKey struct {
	Namespace []string `json:"namespace"`
	Key       string   `json:"key"`
}

// MemoryUsageSort controls sorting for top usage queries.
type MemoryUsageSort string

const (
	MemoryUsageSortFetchCount    MemoryUsageSort = "fetch_count"
	MemoryUsageSortLastFetchedAt MemoryUsageSort = "last_fetched_at"
)

// TopMemoryUsageItem is one ranked usage row.
type TopMemoryUsageItem struct {
	Namespace []string    `json:"namespace"`
	Key       string      `json:"key"`
	Usage     MemoryUsage `json:"usage"`
}

// ListTopMemoryUsageRequest is the input for top usage queries.
type ListTopMemoryUsageRequest struct {
	Prefix []string
	Sort   MemoryUsageSort
	Limit  int
}

// MemoryWriteResult is returned by PutMemory (value omitted for security).
type MemoryWriteResult struct {
	ID         uuid.UUID              `json:"id"`
	Namespace  []string               `json:"namespace"`
	Key        string                 `json:"key"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
	CreatedAt  time.Time              `json:"createdAt"`
	ExpiresAt  *time.Time             `json:"expiresAt"`
}

// SearchRequest is the input for POST /v1/memories/search.
type SearchRequest struct {
	// NamespacePrefix restricts the search to namespaces under this prefix.
	NamespacePrefix []string `json:"namespace_prefix"`
	// Query is the optional free-text query for vector similarity search.
	Query string `json:"query,omitempty"`
	// Filter is the attribute filter expression (flat JSON object).
	Filter json.RawMessage `json:"filter,omitempty"`
	// Limit is the maximum number of results (default 10, max 100).
	Limit int `json:"limit,omitempty"`
	// Offset is the pagination offset (attribute-only mode only).
	Offset int `json:"offset,omitempty"`
	// Archived controls whether archived memories are excluded, included, or returned exclusively.
	Archived ArchiveFilter `json:"archived,omitempty"`
}

// ListNamespacesRequest is the input for GET /v1/memories/namespaces.
type ListNamespacesRequest struct {
	Prefix   []string
	Suffix   []string
	MaxDepth int
	Archived ArchiveFilter
}

type ArchiveFilter string

const (
	ArchiveFilterExclude ArchiveFilter = "exclude"
	ArchiveFilterInclude ArchiveFilter = "include"
	ArchiveFilterOnly    ArchiveFilter = "only"
)

func ParseArchiveFilter(raw string) (ArchiveFilter, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	switch ArchiveFilter(value) {
	case "", ArchiveFilterExclude:
		return ArchiveFilterExclude, nil
	case ArchiveFilterInclude:
		return ArchiveFilterInclude, nil
	case ArchiveFilterOnly:
		return ArchiveFilterOnly, nil
	default:
		return "", fmt.Errorf("invalid archive filter %q; expected exclude, include, or only", raw)
	}
}

// MemoryVectorUpsert holds the data for upserting a vector embedding.
type MemoryVectorUpsert struct {
	MemoryID         uuid.UUID
	FieldName        string
	Namespace        string // RS-encoded
	PolicyAttributes map[string]interface{}
	Archived         bool
	Embedding        []float32
}

// MemoryVectorSearch is the result of a vector search over memory_vectors.
type MemoryVectorSearch struct {
	MemoryID uuid.UUID
	Score    float64
}

// PendingMemory is the internal type returned by FindMemoriesPendingIndexing.
type PendingMemory struct {
	ID               uuid.UUID
	Namespace        string // RS-encoded
	PolicyAttributes map[string]interface{}
	IndexedContent   map[string]string
	ArchivedAt       *time.Time
	DeletedReason    *int32
}

// Event kind constants for MemoryEvent.Kind.
const (
	EventKindAdd     = "add"
	EventKindUpdate  = "update"
	EventKindExpired = "expired"
)

// EventCursor is the decoded form of the opaque AfterCursor pagination token.
type EventCursor struct {
	OccurredAt time.Time `json:"t"`
	ID         string    `json:"id"`
}

// ListEventsRequest is the input for GET /v1/memories/events.
type ListEventsRequest struct {
	// NamespacePrefix restricts the event stream to namespaces under this prefix.
	NamespacePrefix []string
	// Kinds filters by event kind. nil or empty = all kinds.
	Kinds []string
	// After filters events with occurred_at strictly after this time.
	After *time.Time
	// Before filters events with occurred_at strictly before this time.
	Before *time.Time
	// AfterCursor is the opaque cursor from a previous page response.
	AfterCursor string
	// Limit is the max events per page (default 50, max 200).
	Limit int
}

// MemoryEvent is a single lifecycle event in the event timeline.
type MemoryEvent struct {
	ID         uuid.UUID
	Namespace  []string
	Key        string
	Kind       string                 // "add", "update", "expired"
	OccurredAt time.Time              // created_at for add/update; archived_at for archive/expired
	Value      map[string]interface{} // nil for expired tombstones
	Attributes map[string]interface{} // nil for expired tombstones
	ExpiresAt  *time.Time
}

// MemoryEventPage is the paginated response from ListMemoryEvents.
type MemoryEventPage struct {
	Events      []MemoryEvent
	AfterCursor string // empty when no more pages
}

// EpisodicStore defines the primary data access interface for namespaced episodic memories.
type EpisodicStore interface {
	// InReadTx runs fn in a read transaction scope.
	InReadTx(ctx context.Context, fn func(context.Context) error) error

	// InWriteTx runs fn in a write transaction scope.
	InWriteTx(ctx context.Context, fn func(context.Context) error) error

	// PutMemory upserts a memory. On update, the previous active row is archived.
	PutMemory(ctx context.Context, req PutMemoryRequest) (*MemoryWriteResult, error)

	// GetMemory retrieves the current memory for the given (namespace, key), filtered by archive state.
	// Returns nil, nil if no matching current row exists.
	GetMemory(ctx context.Context, namespace []string, key string, archived ArchiveFilter) (*MemoryItem, error)

	// IncrementMemoryLoads increments direct-fetch usage counters for one or more memory keys.
	IncrementMemoryLoads(ctx context.Context, keys []MemoryKey, fetchedAt time.Time) error

	// GetMemoryUsage retrieves usage counters for one memory key.
	// Returns nil, nil if no usage stats exist.
	GetMemoryUsage(ctx context.Context, namespace []string, key string) (*MemoryUsage, error)

	// ListTopMemoryUsage returns ranked usage rows under an optional namespace prefix.
	ListTopMemoryUsage(ctx context.Context, req ListTopMemoryUsageRequest) ([]TopMemoryUsageItem, error)

	// ArchiveMemory archives the active memory for the given (namespace, key).
	// Returns nil if no active row exists (idempotent).
	ArchiveMemory(ctx context.Context, namespace []string, key string) error

	// SearchMemories performs an attribute-filter-only search within the namespace prefix.
	// filter is a parsed attribute filter map (nil = no filter).
	SearchMemories(ctx context.Context, namespacePrefix []string, filter map[string]interface{}, limit, offset int, archived ArchiveFilter) ([]MemoryItem, error)

	// ListNamespaces returns the distinct current namespaces that match the prefix/suffix constraints.
	ListNamespaces(ctx context.Context, req ListNamespacesRequest) ([][]string, error)

	// --- Background indexer support ---

	// FindMemoriesPendingIndexing returns up to limit memories where indexed_at IS NULL.
	FindMemoriesPendingIndexing(ctx context.Context, limit int) ([]PendingMemory, error)

	// SetMemoryIndexedAt marks a memory row as indexed (sets indexed_at).
	SetMemoryIndexedAt(ctx context.Context, memoryID uuid.UUID, indexedAt time.Time) error

	// --- Vector search support (when query != "") ---

	// UpsertMemoryVectors upserts vector embeddings for one or more (memory_id, field_name) pairs.
	UpsertMemoryVectors(ctx context.Context, items []MemoryVectorUpsert) error

	// DeleteMemoryVectors removes all vector rows for the given memory_id.
	DeleteMemoryVectors(ctx context.Context, memoryID uuid.UUID) error

	// SearchMemoryVectors performs ANN search within the namespace prefix,
	// optionally filtered by policy_attributes. Returns memory IDs ranked by score.
	SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter map[string]interface{}, limit int, archived ArchiveFilter) ([]MemoryVectorSearch, error)

	// GetMemoriesByIDs retrieves current memories by UUID, decrypting values and filtering by archive state.
	GetMemoriesByIDs(ctx context.Context, ids []uuid.UUID, archived ArchiveFilter) ([]MemoryItem, error)

	// --- TTL / eviction ---

	// ExpireMemories archives memories whose expires_at <= NOW() and sets indexed_at = NULL.
	ExpireMemories(ctx context.Context) (int64, error)

	// HardDeleteEvictableUpdates hard-deletes rows with deleted_reason=0 (superseded by update)
	// that have been re-indexed (indexed_at IS NOT NULL). Returns the number deleted.
	HardDeleteEvictableUpdates(ctx context.Context, limit int) (int64, error)

	// TombstoneDeletedMemories clears encrypted data from rows with deleted_reason IN (1,2)
	// that have been re-indexed (indexed_at IS NOT NULL). Returns the number tombstoned.
	TombstoneDeletedMemories(ctx context.Context, limit int) (int64, error)

	// HardDeleteExpiredTombstones hard-deletes tombstone rows (deleted_reason IN (1,2),
	// value_encrypted IS NULL) whose archived_at is older than olderThan. Returns the number deleted.
	HardDeleteExpiredTombstones(ctx context.Context, olderThan time.Time, limit int) (int64, error)

	// --- Event timeline ---

	// ListMemoryEvents returns a paginated, time-ordered stream of memory lifecycle events.
	ListMemoryEvents(ctx context.Context, req ListEventsRequest) (*MemoryEventPage, error)

	// --- Admin ---

	// AdminGetMemoryByID retrieves any memory (active or archived) by UUID.
	AdminGetMemoryByID(ctx context.Context, memoryID uuid.UUID) (*MemoryItem, error)

	// AdminForceDeleteMemory hard-deletes a memory by UUID regardless of state.
	AdminForceDeleteMemory(ctx context.Context, memoryID uuid.UUID) error

	// AdminCountPendingIndexing returns the number of memories with indexed_at IS NULL.
	AdminCountPendingIndexing(ctx context.Context) (int64, error)
}

// Loader creates an EpisodicStore from context (config + encryption service injected via context).
type Loader func(ctx context.Context) (EpisodicStore, error)

// Plugin represents an episodic store plugin.
type Plugin struct {
	Name   string
	Loader Loader
}

var plugins []Plugin

// Register adds an episodic store plugin.
func Register(p Plugin) {
	plugins = append(plugins, p)
}

// Names returns all registered plugin names.
func Names() []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

// Select returns the loader for the named plugin.
func Select(name string) (Loader, error) {
	for _, p := range plugins {
		if p.Name == name {
			return p.Loader, nil
		}
	}
	return nil, fmt.Errorf("unknown episodic store %q; valid: %v", name, Names())
}
