// Package episodic defines the EpisodicStore interface and its registry.
// EpisodicStore is the primary data access interface for namespaced episodic memories,
// separate from the conversation/entry MemoryStore.
package episodic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrAdminStatsSummaryUnsupported = errors.New("admin stats summary unsupported")
var ErrMemoryRevisionConflict = errors.New("memory revision conflict")
var ErrSemanticSearchUnavailable = errors.New("semantic search unavailable")

var attributeFilterFieldPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

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
	// ExpectedRevision gates the write with optimistic concurrency when non-nil.
	ExpectedRevision *int64 `json:"-"`
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
	Revision   int64                  `json:"revision"`
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
	Revision   int64                  `json:"revision"`
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
	// Archived controls whether archived memories are excluded, included, or returned exclusively.
	Archived ArchiveFilter `json:"archived,omitempty"`
}

type AttributeFilterOp string

const (
	AttributeFilterOpEq     AttributeFilterOp = "$eq"
	AttributeFilterOpIn     AttributeFilterOp = "$in"
	AttributeFilterOpExists AttributeFilterOp = "$exists"
	AttributeFilterOpGte    AttributeFilterOp = "$gte"
	AttributeFilterOpLte    AttributeFilterOp = "$lte"
)

type AttributeFilterRangeKind string

const (
	AttributeFilterRangeNumber AttributeFilterRangeKind = "number"
	AttributeFilterRangeTime   AttributeFilterRangeKind = "time"
)

type AttributeFilterValue struct {
	Raw  interface{}
	Text string
}

type AttributeFilterCondition struct {
	Field     string
	Op        AttributeFilterOp
	Values    []AttributeFilterValue
	RangeKind AttributeFilterRangeKind
}

type AttributeFilter struct {
	Conditions []AttributeFilterCondition
}

func (f AttributeFilter) Empty() bool {
	return len(f.Conditions) == 0
}

func NormalizeAttributeFilters(filters ...map[string]interface{}) (AttributeFilter, error) {
	var out AttributeFilter
	rangeKindByField := map[string]AttributeFilterRangeKind{}
	for _, filter := range filters {
		for field, expr := range filter {
			if err := validateAttributeFilterField(field); err != nil {
				return AttributeFilter{}, err
			}
			conditions, err := normalizeAttributeFilterField(field, expr)
			if err != nil {
				return AttributeFilter{}, err
			}
			for _, cond := range conditions {
				if cond.RangeKind != "" {
					if existing := rangeKindByField[cond.Field]; existing != "" && existing != cond.RangeKind {
						return AttributeFilter{}, fmt.Errorf("invalid filter for %q: cannot mix numeric and timestamp range bounds", cond.Field)
					}
					rangeKindByField[cond.Field] = cond.RangeKind
				}
				out.Conditions = append(out.Conditions, cond)
			}
		}
	}
	return out, nil
}

func validateAttributeFilterField(field string) error {
	if field == "" || strings.HasPrefix(field, "$") || !attributeFilterFieldPattern.MatchString(field) {
		return fmt.Errorf("invalid attribute filter field %q", field)
	}
	return nil
}

func normalizeAttributeFilterField(field string, expr interface{}) ([]AttributeFilterCondition, error) {
	switch typed := expr.(type) {
	case map[string]interface{}:
		if len(typed) == 0 {
			return nil, fmt.Errorf("invalid filter for %q: empty operator object", field)
		}
		conditions := make([]AttributeFilterCondition, 0, len(typed))
		for op, raw := range typed {
			switch AttributeFilterOp(op) {
			case AttributeFilterOpEq:
				value, err := normalizeAttributeFilterScalar(raw)
				if err != nil {
					return nil, fmt.Errorf("invalid $eq filter for %q: %w", field, err)
				}
				conditions = append(conditions, AttributeFilterCondition{Field: field, Op: AttributeFilterOpEq, Values: []AttributeFilterValue{value}})
			case AttributeFilterOpIn:
				values, err := normalizeAttributeFilterList(raw)
				if err != nil {
					return nil, fmt.Errorf("invalid $in filter for %q: %w", field, err)
				}
				conditions = append(conditions, AttributeFilterCondition{Field: field, Op: AttributeFilterOpIn, Values: values})
			case AttributeFilterOpExists:
				exists, ok := raw.(bool)
				if !ok || !exists {
					return nil, fmt.Errorf("invalid $exists filter for %q: only true is supported", field)
				}
				conditions = append(conditions, AttributeFilterCondition{Field: field, Op: AttributeFilterOpExists})
			case AttributeFilterOpGte, AttributeFilterOpLte:
				value, kind, err := normalizeAttributeFilterRange(raw)
				if err != nil {
					return nil, fmt.Errorf("invalid %s filter for %q: %w", op, field, err)
				}
				conditions = append(conditions, AttributeFilterCondition{Field: field, Op: AttributeFilterOp(op), Values: []AttributeFilterValue{value}, RangeKind: kind})
			default:
				return nil, fmt.Errorf("unsupported filter operator %q for %q", op, field)
			}
		}
		return conditions, nil
	case []interface{}:
		values, err := normalizeAttributeFilterList(typed)
		if err != nil {
			return nil, fmt.Errorf("invalid $in filter for %q: %w", field, err)
		}
		return []AttributeFilterCondition{{Field: field, Op: AttributeFilterOpIn, Values: values}}, nil
	default:
		value, err := normalizeAttributeFilterScalar(typed)
		if err != nil {
			return nil, fmt.Errorf("invalid $eq filter for %q: %w", field, err)
		}
		return []AttributeFilterCondition{{Field: field, Op: AttributeFilterOpEq, Values: []AttributeFilterValue{value}}}, nil
	}
}

func normalizeAttributeFilterList(raw interface{}) ([]AttributeFilterValue, error) {
	list, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected array")
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("expected non-empty array")
	}
	values := make([]AttributeFilterValue, 0, len(list))
	for _, item := range list {
		value, err := normalizeAttributeFilterScalar(item)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func normalizeAttributeFilterRange(raw interface{}) (AttributeFilterValue, AttributeFilterRangeKind, error) {
	value, err := normalizeAttributeFilterScalar(raw)
	if err != nil {
		return AttributeFilterValue{}, "", err
	}
	switch value.Raw.(type) {
	case int64, float64:
		return value, AttributeFilterRangeNumber, nil
	case string:
		if _, err := time.Parse(time.RFC3339, value.Text); err != nil {
			return AttributeFilterValue{}, "", fmt.Errorf("expected numeric value or RFC3339 timestamp string")
		}
		return value, AttributeFilterRangeTime, nil
	default:
		return AttributeFilterValue{}, "", fmt.Errorf("expected numeric value or RFC3339 timestamp string")
	}
}

func normalizeAttributeFilterScalar(raw interface{}) (AttributeFilterValue, error) {
	switch typed := raw.(type) {
	case nil:
		return AttributeFilterValue{}, fmt.Errorf("null values are not supported")
	case string:
		return AttributeFilterValue{Raw: typed, Text: typed}, nil
	case bool:
		if typed {
			return AttributeFilterValue{Raw: typed, Text: "true"}, nil
		}
		return AttributeFilterValue{Raw: typed, Text: "false"}, nil
	case int:
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case int8:
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case int16:
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case int32:
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case int64:
		return AttributeFilterValue{Raw: typed, Text: fmt.Sprintf("%d", typed)}, nil
	case uint:
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case uint8:
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case uint16:
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case uint32:
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case uint64:
		if typed > math.MaxInt64 {
			return AttributeFilterValue{Raw: float64(typed), Text: fmt.Sprintf("%d", typed)}, nil
		}
		return AttributeFilterValue{Raw: int64(typed), Text: fmt.Sprintf("%d", typed)}, nil
	case float32:
		return AttributeFilterValue{Raw: float64(typed), Text: formatAttributeFilterFloat(float64(typed))}, nil
	case float64:
		return AttributeFilterValue{Raw: typed, Text: formatAttributeFilterFloat(typed)}, nil
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return AttributeFilterValue{Raw: i, Text: typed.String()}, nil
		}
		f, err := typed.Float64()
		if err != nil {
			return AttributeFilterValue{}, fmt.Errorf("invalid number")
		}
		return AttributeFilterValue{Raw: f, Text: typed.String()}, nil
	default:
		return AttributeFilterValue{}, fmt.Errorf("expected scalar value")
	}
}

func formatAttributeFilterFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
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
	ArchiveMemory(ctx context.Context, namespace []string, key string, expectedRevision *int64) error

	// SearchMemories performs an attribute-filter-only search within the namespace prefix.
	// filter is a parsed attribute filter map (nil = no filter).
	SearchMemories(ctx context.Context, namespacePrefix []string, filter AttributeFilter, limit int, archived ArchiveFilter) ([]MemoryItem, error)

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
	SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter AttributeFilter, limit int, archived ArchiveFilter) ([]MemoryVectorSearch, error)

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
