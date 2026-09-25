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

	"github.com/chirino/memory-service/internal/model"
	"github.com/google/uuid"
)

var ErrAdminStatsSummaryUnsupported = errors.New("admin stats summary unsupported")
var ErrMemoryRevisionConflict = errors.New("memory revision conflict")
var ErrMemoryKindMigrationStateConflict = errors.New("memory kind migration state changed")
var ErrSemanticSearchUnavailable = errors.New("semantic search unavailable")

// ErrMemoryKindNotFound is returned when a write selector names a schema version that does not exist.
var ErrMemoryKindNotFound = errors.New("schema version not found")

// ErrMemoryKindNotWritable is returned when a write selector names a schema version that is not writable.
var ErrMemoryKindNotWritable = errors.New("schema version is not writable")

// ErrMemoryKindInvalid is returned when a write kind is neither empty nor an exact canonical name.
var ErrMemoryKindInvalid = errors.New("invalid memory kind selector")

var attributeFilterFieldPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

const attributeFilterTimestampLayout = "2006-01-02T15:04:05.000000000Z"

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
	// AuthorizedPredecessor records the active row observed during write authorization.
	// A non-nil check is presence-aware: Exists=false means the key was absent and
	// must still be absent; Exists=true requires the same row and revision.
	AuthorizedPredecessor *MemoryPredecessorExpectation `json:"-"`
	// MemoryKind is the resolved canonical schema name for this write (set by the handler).
	// Must be non-empty before PutMemory is called; stores reject empty kind.
	MemoryKind string `json:"-"`
}

type MemoryPredecessor struct {
	ID         uuid.UUID
	Revision   int64
	MemoryKind string
}

type MemoryPredecessorExpectation struct {
	Exists   bool
	ID       uuid.UUID
	Revision int64
}

// MemoryItem is the external representation of an active memory (returned by GET / search).
type MemoryItem struct {
	ID             uuid.UUID              `json:"id"`
	Namespace      []string               `json:"namespace"`
	Key            string                 `json:"key"`
	Value          map[string]interface{} `json:"value,omitempty"`
	Attributes     map[string]interface{} `json:"attributes,omitempty"`
	MemoryKind     string                 `json:"memoryKind,omitempty"`
	Score          *float64               `json:"score,omitempty"`          // nil for non-vector results
	MatchedQueries []string               `json:"matchedQueries,omitempty"` // attribution for multi-query results
	Usage          *MemoryUsage           `json:"usage,omitempty"`
	CreatedAt      time.Time              `json:"createdAt"`
	ExpiresAt      *time.Time             `json:"expiresAt"`
	ArchivedAt     *time.Time             `json:"archivedAt,omitempty"`
	Revision       int64                  `json:"revision"`
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

type AdminMemoryQuery struct {
	NamespacePrefix []string
	KeyPrefix       string
	Archived        ArchiveFilter
	CreatedAfter    *time.Time
	CreatedBefore   *time.Time
	ExpiresBefore   *time.Time
	IncludeUsage    bool
	Limit           int
	AfterCursor     string
	// MemoryKind is the optional top-level kind selector (canonical name, family, or empty for all).
	MemoryKind string
	// Filter is the optional projected-attribute filter (same semantics as SearchMemories/AdminSearch).
	Filter AttributeFilter
}

// MemoryAttributeSort is a one-field typed sort for attribute-only searches.
type MemoryAttributeSort struct {
	Field     string // declared attribute name
	Direction string // "asc" or "desc"
	// Type is the declared attribute type from the selected schema version.
	// Empty string or "string" means text/binary collation sort.
	// Use episodic.AttributeType* constants ("number", "boolean", "timestamp").
	Type string
}

// MemorySearchQuery is the input for attribute-only SearchMemories calls.
type MemorySearchQuery struct {
	NamespacePrefix []string
	Filter          AttributeFilter
	Limit           int
	Archived        ArchiveFilter
	MemoryKind      string // canonical name, family, or empty for all
	Sort            *MemoryAttributeSort
}

type AdminMemorySearchQuery struct {
	NamespacePrefix []string
	KeyPrefix       string
	Query           string
	Filter          AttributeFilter
	Archived        ArchiveFilter
	IncludeUsage    bool
	Limit           int
	MemoryKind      string // canonical name, family, or empty for all
	Sort            *MemoryAttributeSort
}

type AdminMemoryPage struct {
	Items       []MemoryItem
	AfterCursor string
}

// EventSnapshotStore exposes the global memory scan required by
// initial_state=current admin event subscriptions.
type EventSnapshotStore interface {
	AdminListEventSnapshotMemories(ctx context.Context, query AdminMemoryQuery) (AdminMemoryPage, error)
}

type AdminNamespaceQuery struct {
	NamespacePrefix []string
	Suffix          []string
	MaxDepth        int
	Archived        ArchiveFilter
	Limit           int
	AfterCursor     string
}

type AdminNamespacePage struct {
	Namespaces  [][]string
	AfterCursor string
}

// MemoryWriteResult is returned by PutMemory (value omitted for security).
type MemoryWriteResult struct {
	ID         uuid.UUID              `json:"id"`
	Namespace  []string               `json:"namespace"`
	Key        string                 `json:"key"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
	MemoryKind string                 `json:"memoryKind,omitempty"`
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

// MergeAttributeFilters combines two already-normalized AttributeFilters into one.
// It preserves range-kind conflict detection (cannot mix numeric and timestamp bounds
// on the same field across the two inputs).
// This is used to combine a schema-validated+normalized caller filter with a
// separately-normalized policy filter without re-parsing raw JSON.
func MergeAttributeFilters(a, b AttributeFilter) (AttributeFilter, error) {
	var out AttributeFilter
	rangeKindByField := map[string]AttributeFilterRangeKind{}
	for _, cond := range a.Conditions {
		if cond.RangeKind != "" {
			rangeKindByField[cond.Field] = cond.RangeKind
		}
		out.Conditions = append(out.Conditions, cond)
	}
	for _, cond := range b.Conditions {
		if cond.RangeKind != "" {
			if existing := rangeKindByField[cond.Field]; existing != "" && existing != cond.RangeKind {
				return AttributeFilter{}, fmt.Errorf("invalid filter for %q: cannot mix numeric and timestamp range bounds", cond.Field)
			}
			rangeKindByField[cond.Field] = cond.RangeKind
		}
		out.Conditions = append(out.Conditions, cond)
	}
	return out, nil
}

// ValidateAttributeFilterField validates an attribute field name for use in filters and sorts.
func ValidateAttributeFilterField(field string) error {
	if field == "" || strings.HasPrefix(field, "$") || !attributeFilterFieldPattern.MatchString(field) {
		return fmt.Errorf("invalid attribute filter field %q", field)
	}
	return nil
}

func validateAttributeFilterField(field string) error {
	return ValidateAttributeFilterField(field)
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
		parsed, err := time.Parse(time.RFC3339, value.Text)
		if err != nil {
			return AttributeFilterValue{}, "", fmt.Errorf("expected numeric value or RFC3339 timestamp string")
		}
		canonical := parsed.UTC().Format(attributeFilterTimestampLayout)
		return AttributeFilterValue{Raw: canonical, Text: canonical}, AttributeFilterRangeTime, nil
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
	// MemoryKind is an exact or family selector injected by filter.rego.
	MemoryKind string
	// Filter is the normalized attribute filter injected by filter.rego.
	Filter AttributeFilter
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
	MemoryKind       string // canonical schema name; always non-empty after fresh write
	MemoryRevision   int64  // memory revision at time of indexing
}

// MemoryVectorSearch is the result of a vector search over memory_vectors.
type MemoryVectorSearch struct {
	MemoryID         uuid.UUID
	Score            float64
	MemoryRevision   int64  // positive revision; missing/nonpositive candidates are invalid
	MemoryKind       string // "" = unknown
	PrimaryValidated bool   // true = SQL JOIN already validated freshness & schema; skip client checks
}

// PendingMemory is the internal type returned by FindMemoriesPendingIndexing.
type PendingMemory struct {
	ID               uuid.UUID
	Namespace        string // RS-encoded
	PolicyAttributes map[string]interface{}
	IndexedContent   map[string]string
	ArchivedAt       *time.Time
	DeletedReason    *int32
	MemoryKind       string // canonical schema name; always non-empty after fresh write
	Revision         int64  // memory revision, used by SetMemoryKindIndexedAtCAS
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
	// Limit is the max events per page (default 50, capped by server configuration).
	Limit int
	// MemoryKind is an exact or family selector injected by filter.rego.
	MemoryKind string
	// Filter is the normalized attribute filter injected by filter.rego.
	Filter AttributeFilter
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
	MemoryKind string // canonical schema name; always non-empty after fresh write
}

// MemoryEventPage is the paginated response from ListMemoryEvents.
type MemoryEventPage struct {
	Events      []MemoryEvent
	AfterCursor string // empty when no more pages
}

// EpisodicKindStore is the subset of EpisodicStore that handles schema version persistence.
// All EpisodicStore implementations must implement every method in this interface.
type EpisodicKindStore interface {
	// CreateMemoryKindVersion persists an immutable schema version.
	// If a version with the same name already exists and is byte-identical, it returns the existing resource.
	// If a version exists with a different definition it returns ErrMemoryKindVersionConflict.
	CreateMemoryKindVersion(ctx context.Context, version model.MemoryKindVersion) (*model.MemoryKindVersion, error)

	// GetMemoryKindVersion retrieves a schema version by canonical name.
	// Returns nil, nil if not found.
	GetMemoryKindVersion(ctx context.Context, name string) (*model.MemoryKindVersion, error)

	// ListMemoryKindVersions lists all known schema versions, optionally filtered by family.
	ListMemoryKindVersions(ctx context.Context, family string) ([]model.MemoryKindVersion, error)

	// CreateMemoryKindMigration persists only a new migration resource.
	// Returns ErrMemoryKindMigrationActiveForSource if an active migration for source already exists.
	CreateMemoryKindMigration(ctx context.Context, m model.MemoryKindMigration) (*model.MemoryKindMigration, error)

	// CreateMemoryKindMigrationAndTask atomically persists a queued migration and
	// its uniquely named initial task in the same datastore transaction.
	CreateMemoryKindMigrationAndTask(ctx context.Context, m model.MemoryKindMigration) (*model.MemoryKindMigration, error)
	// CreateMemoryKindMigrationTask queues a continuation/restart task using the
	// same transaction and physical datastore as migration state.
	CreateMemoryKindMigrationTask(ctx context.Context, body map[string]interface{}) error

	// DeleteMemoryKindMigration permanently removes a migration record by UUID.
	// No-ops if the migration does not exist.
	DeleteMemoryKindMigration(ctx context.Context, id uuid.UUID) error

	// GetMemoryKindMigration retrieves a migration by UUID.
	// Returns nil, nil if not found.
	GetMemoryKindMigration(ctx context.Context, id uuid.UUID) (*model.MemoryKindMigration, error)

	// ListMemoryKindMigrations returns migrations, optionally filtered by state.
	ListMemoryKindMigrations(ctx context.Context, state string) ([]model.MemoryKindMigration, error)

	// UpdateMemoryKindMigrationCancelRequested marks a queued or running migration as canceling.
	// Returns ErrMemoryKindMigrationNotFound if no migration with the given ID exists.
	// If the migration already has state canceling/canceled/succeeded/failed, the call is a no-op (returns nil).
	UpdateMemoryKindMigrationCancelRequested(ctx context.Context, id uuid.UUID) error

	// UpdateMemoryKindMigrationState atomically updates state and timestamps on a migration.
	UpdateMemoryKindMigrationState(ctx context.Context, id uuid.UUID, state string, startedAt, completedAt *time.Time) error

	// UpdateMemoryKindMigrationStateFailed atomically sets state=failed, last_error_code, and
	// completed_at in one operation.  Use instead of UpdateMemoryKindMigrationState for terminal
	// failures so the stable error code is persisted atomically with the state transition.
	UpdateMemoryKindMigrationStateFailed(ctx context.Context, id uuid.UUID, errorCode string, completedAt time.Time) error

	// UpdateMemoryKindMigrationSucceeded atomically sets state=succeeded, completed_at, and
	// persists the ABSOLUTE (not delta) skipped_tombstone_count observed during the final
	// verification sweep.  Using an absolute count means retries are idempotent: re-sweeping
	// and re-running finalization overwrites with the same value rather than accumulating.
	// Does NOT take or update migrated_count (increments happen per-row via
	// UpdateMemoryKindMigrationIncrementMigrated during the batch phase).
	UpdateMemoryKindMigrationSucceeded(ctx context.Context, id uuid.UUID, completedAt time.Time, absoluteSkippedTombstoneCount int64) error

	// UpdateMemoryKindMigrationIncrementMigrated increments migrated_count by 1.
	// Must be called inside the same InWriteTx callback as MigrateOneMemoryKindCAS so
	// both the schema cutover and the counter increment are in the same SQL transaction
	// and roll back together on failure.  Does NOT touch tombstone counters;
	// the absolute tombstone count is computed once during the final verification sweep.
	UpdateMemoryKindMigrationIncrementMigrated(ctx context.Context, id uuid.UUID) error

	// UpdateMemoryKindMigrationCounters increments per-migration progress counters.
	// Retained for vector_pending_count updates from the indexer path.
	UpdateMemoryKindMigrationCounters(ctx context.Context, id uuid.UUID, migratedDelta, tombstoneDelta, vectorPendingDelta int64) error

	// UpdateMemoryKindMigrationRetry increments retry_count by 1 and sets last_error_code.
	// Called when a migration batch fails transiently and the task is rescheduled for retry.
	UpdateMemoryKindMigrationRetry(ctx context.Context, id uuid.UUID, errorCode string) error

	// ResolveKindForWrite resolves the canonical schema name to use for a write.
	// sel is empty for the fixed default/v1 kind or an exact canonical name.
	ResolveKindForWrite(ctx context.Context, sel string) (string, error)

	// SetMemoryKindIndexedAtCAS sets indexed_at on a memory row only if schema, revision, and lifecycle
	// still match expected values. Returns ErrMemoryRevisionConflict if the row changed.
	SetMemoryKindIndexedAtCAS(ctx context.Context, memoryID uuid.UUID, expectedKind string, expectedRevision int64, indexedAt time.Time) error

	// FindMemoriesToMigrateByKind returns up to limit rows whose effective schema is sourceKind
	// and optionally matching the given namespace prefix. Tombstones (null ValueEncrypted) are
	// included so the cursor advances past them; the absolute tombstone count is computed during
	// the final verification sweep after the cursor is exhausted, not accumulated per batch.
	FindMemoriesToMigrateByKind(ctx context.Context, sourceKind string, namespacePrefix []string, afterCreatedAt time.Time, afterID uuid.UUID, limit int) ([]MigrationCandidate, error)

	// MigrateOneMemoryKindCAS atomically updates the projected attributes and memory_schema on a
	// memory row, only if it still has expectedKind and expectedRevision. Returns
	// ErrMemoryRevisionConflict if the row has changed. The operation also clears indexed_at
	// so the existing indexer re-syncs the vector payload.
	MigrateOneMemoryKindCAS(ctx context.Context, id uuid.UUID, expectedKind string, expectedRevision int64, newAttributes map[string]interface{}, newSchema string) error

	// CountMemoriesPendingIndexByKind returns the number of memory rows with the given
	// targetKind and indexed_at IS NULL (i.e. awaiting the ordinary vector indexer).
	// namespacePrefix scopes the count to the migration's target namespace; nil/empty
	// counts across all namespaces.
	// This is used to compute an accurate race-safe vector_pending_count at read time.
	CountMemoriesPendingIndexByKind(ctx context.Context, targetKind string, namespacePrefix []string) (int64, error)
}

// MigrationCandidate is a row returned by FindMemoriesToMigrateByKind.
type MigrationCandidate struct {
	ID               uuid.UUID
	CreatedAt        time.Time
	Namespace        string // RS-encoded
	Key              string
	ValueEncrypted   []byte // nil for tombstones
	PolicyAttributes map[string]interface{}
	IndexedContent   map[string]string
	MemoryKind       string // effective schema; never empty
	Revision         int64
}

var (
	ErrMemoryKindVersionConflict          = errors.New("schema version already exists with a different definition")
	ErrMemoryKindMigrationActiveForSource = errors.New("an active migration for this source version already exists")
	ErrMemoryKindMigrationNotFound        = errors.New("migration not found")
)

// EpisodicStore defines the primary data access interface for namespaced episodic memories.
type EpisodicStore interface {
	// InReadTx runs fn in a read transaction scope.
	InReadTx(ctx context.Context, fn func(context.Context) error) error

	// InWriteTx runs fn in a write transaction scope.
	InWriteTx(ctx context.Context, fn func(context.Context) error) error

	// EpisodicKindStore provides schema version/default/migration operations.
	EpisodicKindStore

	// PutMemory upserts a memory. On update, the previous active row is archived.
	PutMemory(ctx context.Context, req PutMemoryRequest) (*MemoryWriteResult, error)

	// GetMemoryRowKind returns the exact canonical kind of the latest row for
	// (namespace, key) that satisfies the archive filter, without loading or decrypting
	// the value.  Returns ("", false, nil) if no matching row exists.
	//
	// For write-path authz (ArchiveMemory) pass ArchiveFilterExclude so only the
	// active row is checked; PutMemory uses GetMemoryPredecessor instead. For
	// read-path authz (GetMemory) pass the caller's requested archive filter so the
	// row authorized matches the row read.
	GetMemoryRowKind(ctx context.Context, namespace []string, key string, archived ArchiveFilter) (kind string, found bool, err error)

	// GetMemoryPredecessor returns the active row identity used for optimistic
	// authorization-to-mutation validation. It returns nil when the key is absent.
	GetMemoryPredecessor(ctx context.Context, namespace []string, key string) (*MemoryPredecessor, error)

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
	SearchMemories(ctx context.Context, query MemorySearchQuery) ([]MemoryItem, error)

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
	SearchMemoryVectors(ctx context.Context, namespacePrefix string, embedding []float32, filter AttributeFilter, memoryKind string, limit int, archived ArchiveFilter) ([]MemoryVectorSearch, error)

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

	// AdminListMemories retrieves latest memory rows across users without policy injection.
	AdminListMemories(ctx context.Context, query AdminMemoryQuery) (AdminMemoryPage, error)

	// AdminGetMemoryByID retrieves any memory (active or archived) by UUID.
	AdminGetMemoryByID(ctx context.Context, memoryID uuid.UUID) (*MemoryItem, error)

	// AdminSearchMemories retrieves latest matching memory rows across users without policy injection.
	AdminSearchMemories(ctx context.Context, query AdminMemorySearchQuery) ([]MemoryItem, error)

	// AdminListNamespaces retrieves memory namespaces across users without policy injection.
	AdminListNamespaces(ctx context.Context, query AdminNamespaceQuery) (AdminNamespacePage, error)

	// AdminForceDeleteMemory hard-deletes a memory by UUID regardless of state.
	AdminForceDeleteMemory(ctx context.Context, memoryID uuid.UUID) error

	// AdminCountPendingIndexing returns the number of memories with indexed_at IS NULL.
	AdminCountPendingIndexing(ctx context.Context) (int64, error)
}

// MemoryLifecycleChange contains only the payload-safe fields needed to emit
// durable analytics events for background expiry and eviction work.
type MemoryLifecycleChange struct {
	ID         uuid.UUID
	MemoryKind string
	Revision   int64
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	ArchivedAt *time.Time
}

// BackgroundMemoryLifecycleStore returns the exact rows changed by a
// maintenance pass so its outbox events can be appended in the same write
// transaction.
type BackgroundMemoryLifecycleStore interface {
	ExpireMemoriesWithChanges(ctx context.Context) ([]MemoryLifecycleChange, error)
	TombstoneDeletedMemoriesWithChanges(ctx context.Context, limit int) ([]MemoryLifecycleChange, error)
	HardDeleteExpiredTombstonesWithChanges(ctx context.Context, olderThan time.Time, limit int) ([]MemoryLifecycleChange, error)
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
