package memories

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	"github.com/chirino/memory-service/internal/model"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// mockIdempotencyEpisodicStore is a mock EpisodicStore designed for testing
// REST handler execution order, pre-flight lookup, kind resolution, and error propagation.
type mockIdempotencyEpisodicStore struct {
	registryepisodic.EpisodicStore

	mu sync.Mutex

	lookupCalls   int
	lastLookupID  uuid.UUID
	lastLookupReq registryepisodic.PutMemoryRequest
	lookupResult  *registryepisodic.MemoryWriteResult
	lookupErr     error

	predecessorCalls int
	predecessor      *registryepisodic.MemoryPredecessor
	predecessorErr   error

	resolveKindCalls int
	resolveKindRes   string
	resolveKindErr   error

	kindVersions map[string]*model.MemoryKindVersion

	putCalls   int
	lastPutReq registryepisodic.PutMemoryRequest
	putResult  *registryepisodic.MemoryWriteResult
	putErr     error

	txCallbacks     int
	txCallbackCount int
	txErr           error

	callOrder []string
}

func (s *mockIdempotencyEpisodicStore) recordCall(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callOrder = append(s.callOrder, name)
}

func (s *mockIdempotencyEpisodicStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	callbacks := s.txCallbacks
	if callbacks == 0 {
		callbacks = 1
	}
	for range callbacks {
		s.txCallbackCount++
		if err := fn(ctx); err != nil {
			return err
		}
	}
	return s.txErr
}

func (s *mockIdempotencyEpisodicStore) GetMemoryPredecessor(_ context.Context, _ []string, _ string) (*registryepisodic.MemoryPredecessor, error) {
	s.recordCall("GetMemoryPredecessor")
	s.predecessorCalls++
	return s.predecessor, s.predecessorErr
}

func (s *mockIdempotencyEpisodicStore) LookupMemoryByID(_ context.Context, id uuid.UUID, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
	s.recordCall("LookupMemoryByID")
	s.lookupCalls++
	s.lastLookupID = id
	s.lastLookupReq = req
	return s.lookupResult, s.lookupErr
}

func (s *mockIdempotencyEpisodicStore) ResolveKindForWrite(_ context.Context, sel string) (string, error) {
	s.recordCall("ResolveKindForWrite")
	s.resolveKindCalls++
	if s.resolveKindErr != nil {
		return "", s.resolveKindErr
	}
	if s.resolveKindRes != "" {
		return s.resolveKindRes, nil
	}
	if sel == "" {
		return "default/v1", nil
	}
	return sel, nil
}

func (s *mockIdempotencyEpisodicStore) GetMemoryKindVersion(_ context.Context, name string) (*model.MemoryKindVersion, error) {
	s.recordCall("GetMemoryKindVersion")
	if s.kindVersions != nil {
		if kv, ok := s.kindVersions[name]; ok {
			return kv, nil
		}
	}
	return &model.MemoryKindVersion{Name: name, AttributeTypes: map[string]string{}, Writable: true}, nil
}

func (s *mockIdempotencyEpisodicStore) PutMemory(_ context.Context, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
	s.recordCall("PutMemory")
	s.putCalls++
	s.lastPutReq = req
	if s.putErr != nil {
		return nil, s.putErr
	}
	if s.putResult != nil {
		return s.putResult, nil
	}
	id := uuid.New()
	if req.RevisionID != nil {
		id = *req.RevisionID
	}
	return &registryepisodic.MemoryWriteResult{
		ID:         id,
		Namespace:  req.Namespace,
		Key:        req.Key,
		MemoryKind: req.MemoryKind,
		Revision:   1,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

type recordingEventBus struct {
	mu     sync.Mutex
	events []registryeventbus.Event
}

func (b *recordingEventBus) Publish(_ context.Context, event registryeventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	return nil
}

func (b *recordingEventBus) Subscribe(context.Context, string) (<-chan registryeventbus.Event, error) {
	return nil, nil
}

func (b *recordingEventBus) Close() error { return nil }

func (b *recordingEventBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}

func executeUserPut(t *testing.T, store registryepisodic.EpisodicStore, eventBus registryeventbus.EventBus, policy *episodic.PolicyEngine, payload map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/v1/memories", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	cfg := config.DefaultConfig()
	putMemoryWithEvents(c, store, nil, eventBus, policy, &cfg)
	return recorder
}

func executeAdminPut(t *testing.T, store registryepisodic.EpisodicStore, eventBus registryeventbus.EventBus, payload map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/admin/v1/memories", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(security.ContextKeyRoles, map[string]bool{security.RoleAdmin: true})
	cfg := config.DefaultConfig()
	HandleAdminPutMemory(c, store, nil, eventBus, nil, &cfg)
	return recorder
}

// ──────────────────────────────────────────────────────────────────────────────
// User REST Tests
// ──────────────────────────────────────────────────────────────────────────────

func TestUserPutMemory_NewWrite_WithRevisionID_Returns201(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: nil, // no existing row found
	}
	bus := &recordingEventBus{}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "hello"},
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, bus, nil, payload)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, revisionID.String(), resp["id"])
	require.Equal(t, 1, store.lookupCalls)
	require.Equal(t, 1, store.putCalls)
	require.NotNil(t, store.lastPutReq.RevisionID)
	require.Equal(t, revisionID, *store.lastPutReq.RevisionID)
}

func TestUserPutMemory_NewWrite_WithoutRevisionID_Returns200(t *testing.T) {
	store := &mockIdempotencyEpisodicStore{}
	bus := &recordingEventBus{}

	payload := map[string]interface{}{
		"namespace": []string{"user", "alice", "notes"},
		"key":       "k1",
		"value":     map[string]interface{}{"text": "hello"},
	}

	recorder := executeUserPut(t, store, bus, nil, payload)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 0, store.lookupCalls, "lookup should not run when revision_id is absent")
	require.Equal(t, 1, store.putCalls)
	require.Nil(t, store.lastPutReq.RevisionID)
}

func TestUserPutMemory_ExactRetry_Returns200_NoSideEffects(t *testing.T) {
	revisionID := uuid.New()
	createdAt := time.Now().UTC().Truncate(time.Second)
	store := &mockIdempotencyEpisodicStore{
		lookupResult: &registryepisodic.MemoryWriteResult{
			ID:         revisionID,
			Namespace:  []string{"user", "alice", "notes"},
			Key:        "k1",
			MemoryKind: "default/v1",
			Revision:   1,
			CreatedAt:  createdAt,
			ExactRetry: true,
		},
	}
	bus := &recordingEventBus{}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "hello"},
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, bus, nil, payload)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, revisionID.String(), resp["id"])
	require.Equal(t, float64(1), resp["revision"])

	// Assert execution flow: Lookup ran, but ResolveKindForWrite, PutMemory, and event publishing did NOT run.
	require.Equal(t, 1, store.lookupCalls)
	require.Equal(t, 0, store.resolveKindCalls, "ResolveKindForWrite must be bypassed on exact retry")
	require.Equal(t, 0, store.putCalls, "PutMemory must be bypassed on exact retry")
	require.Equal(t, 0, bus.count(), "no events should be published on exact retry")
}

func TestUserPutMemory_Conflict_ChangedPayload_Returns409(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupErr: &registrystore.MemoryRevisionIDConflictError{
			ConflictError: &registrystore.ConflictError{
				Message: "memory revision ID already in use with different content",
				Code:    registrystore.MemoryRevisionAlreadyExistsCode,
			},
			RevisionID: revisionID.String(),
		},
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "different"},
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, "memory_revision_already_exists", resp["code"])
	details, ok := resp["details"].(map[string]interface{})
	require.True(t, ok, "details must be an object")
	require.Equal(t, revisionID.String(), details["revisionId"])
	require.NotContains(t, resp, "namespace")
	require.NotContains(t, resp, "key")
	require.NotContains(t, resp, "value")
}

func TestUserPutMemory_Conflict_CrossNamespaceCollision_Returns409_NoDataLeak(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupErr: &registrystore.MemoryRevisionIDConflictError{
			ConflictError: &registrystore.ConflictError{
				Message: "memory revision ID already in use with different namespace or key",
				Code:    registrystore.MemoryRevisionAlreadyExistsCode,
			},
			RevisionID: revisionID.String(),
		},
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "bob", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "secret"},
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, "memory_revision_already_exists", resp["code"])
	details, ok := resp["details"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, revisionID.String(), details["revisionId"])
	// Disclosure check: only code, error/message, and caller's revisionId
	require.NotContains(t, resp, "namespace")
	require.NotContains(t, resp, "key")
	require.NotContains(t, resp, "value")
}

func TestUserPutMemory_DeletedOrArchived_Returns404(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupErr: &registrystore.NotFoundError{
			Resource: "memory",
			ID:       revisionID.String(),
		},
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "hello"},
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, "not_found", resp["code"])
}

func TestUserPutMemory_CreateOnly_Success_Returns201(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: nil,
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "hello"},
		"revision_id": revisionID.String(),
		"revision":    1,
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.True(t, store.lastPutReq.CreateOnly)
}

func TestUserPutMemory_CreateOnly_ActiveRowConflict_Returns409(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: nil,
		putErr:       registryepisodic.ErrMemoryRevisionConflict,
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "hello"},
		"revision_id": revisionID.String(),
		"revision":    1,
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
}

func TestUserPutMemory_ExpectedRevisionID_Match_Returns201(t *testing.T) {
	revisionID := uuid.New()
	expectedID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: nil,
	}

	payload := map[string]interface{}{
		"namespace":            []string{"user", "alice", "notes"},
		"key":                  "k1",
		"value":                map[string]interface{}{"text": "updated"},
		"revision_id":          revisionID.String(),
		"expected_revision_id": expectedID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.NotNil(t, store.lastPutReq.ExpectedRevisionID)
	require.Equal(t, expectedID, *store.lastPutReq.ExpectedRevisionID)
}

func TestUserPutMemory_ExpectedRevisionID_Mismatch_Returns409(t *testing.T) {
	revisionID := uuid.New()
	expectedID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: nil,
		putErr: &registrystore.ExpectedActiveRevisionIDMismatchError{
			ConflictError: &registrystore.ConflictError{
				Message: "active revision ID does not match expected_revision_id",
				Code:    registrystore.ExpectedActiveRevisionIDMismatchCode,
			},
			ExpectedID: expectedID.String(),
			ActualID:   uuid.New().String(),
		},
	}

	payload := map[string]interface{}{
		"namespace":            []string{"user", "alice", "notes"},
		"key":                  "k1",
		"value":                map[string]interface{}{"text": "updated"},
		"revision_id":          revisionID.String(),
		"expected_revision_id": expectedID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, "expected_active_revision_id_mismatch", resp["code"])
}

func TestUserPutMemory_Validation_ExpectedRevisionIDWithoutRevisionID_Returns400(t *testing.T) {
	store := &mockIdempotencyEpisodicStore{}
	expectedID := uuid.New()

	payload := map[string]interface{}{
		"namespace":            []string{"user", "alice", "notes"},
		"key":                  "k1",
		"value":                map[string]interface{}{"text": "updated"},
		"expected_revision_id": expectedID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Equal(t, 0, store.lookupCalls)
	require.Equal(t, 0, store.putCalls)
}

func TestUserPutMemory_Validation_InvalidRevisionValue_Returns400(t *testing.T) {
	store := &mockIdempotencyEpisodicStore{}
	revisionID := uuid.New()

	for _, invalidRev := range []int{0, 2, -1, 100} {
		payload := map[string]interface{}{
			"namespace":   []string{"user", "alice", "notes"},
			"key":         "k1",
			"value":       map[string]interface{}{"text": "hello"},
			"revision_id": revisionID.String(),
			"revision":    invalidRev,
		}

		recorder := executeUserPut(t, store, nil, nil, payload)
		require.Equal(t, http.StatusBadRequest, recorder.Code, "revision %d must be rejected with 400", invalidRev)
	}
	require.Equal(t, 0, store.lookupCalls)
	require.Equal(t, 0, store.putCalls)
}

func TestUserPutMemory_Validation_RevisionWithoutRevisionID_Returns400(t *testing.T) {
	store := &mockIdempotencyEpisodicStore{}

	payload := map[string]interface{}{
		"namespace": []string{"user", "alice", "notes"},
		"key":       "k1",
		"value":     map[string]interface{}{"text": "hello"},
		"revision":  1,
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Equal(t, 0, store.lookupCalls)
	require.Equal(t, 0, store.putCalls)
}

func TestUserPutMemory_Validation_InvalidUUID_Returns400(t *testing.T) {
	store := &mockIdempotencyEpisodicStore{}

	t.Run("invalid revision_id", func(t *testing.T) {
		payload := map[string]interface{}{
			"namespace":   []string{"user", "alice", "notes"},
			"key":         "k1",
			"value":       map[string]interface{}{"text": "hello"},
			"revision_id": "not-a-valid-uuid",
		}
		recorder := executeUserPut(t, store, nil, nil, payload)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Equal(t, 0, store.lookupCalls)
	})

	t.Run("invalid expected_revision_id", func(t *testing.T) {
		payload := map[string]interface{}{
			"namespace":            []string{"user", "alice", "notes"},
			"key":                  "k1",
			"value":                map[string]interface{}{"text": "hello"},
			"revision_id":          uuid.New().String(),
			"expected_revision_id": "not-a-valid-uuid",
		}
		recorder := executeUserPut(t, store, nil, nil, payload)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Equal(t, 0, store.lookupCalls)
	})
}

func TestUserPutMemory_FieldPassThrough_ToLookupAndPut(t *testing.T) {
	revisionID := uuid.New()
	expectedRevID := uuid.New()
	expRev := int64(3)
	store := &mockIdempotencyEpisodicStore{
		lookupResult: nil, // simulate new write
	}

	payload := map[string]interface{}{
		"namespace":            []string{"user", "alice", "notes"},
		"key":                  "k1",
		"value":                map[string]interface{}{"text": "content"},
		"index":                map[string]string{"summary": "test"},
		"ttl_seconds":          3600,
		"expected_revision":    expRev,
		"revision_id":          revisionID.String(),
		"expected_revision_id": expectedRevID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusCreated, recorder.Code)

	// Verify fields passed to LookupMemoryByID
	require.Equal(t, 1, store.lookupCalls)
	require.Equal(t, revisionID, store.lastLookupID)
	require.Equal(t, 3600, store.lastLookupReq.TTLSeconds)
	require.Equal(t, map[string]string{"summary": "test"}, store.lastLookupReq.Index)
	require.Equal(t, &expRev, store.lastLookupReq.ExpectedRevision)
	require.Equal(t, &expectedRevID, store.lastLookupReq.ExpectedRevisionID)

	// Verify fields passed to PutMemory
	require.Equal(t, 1, store.putCalls)
	require.Equal(t, 3600, store.lastPutReq.TTLSeconds)
	require.Equal(t, map[string]string{"summary": "test"}, store.lastPutReq.Index)
	require.Equal(t, &expRev, store.lastPutReq.ExpectedRevision)
	require.Equal(t, &revisionID, store.lastPutReq.RevisionID)
	require.Equal(t, &expectedRevID, store.lastPutReq.ExpectedRevisionID)
}

func TestUserPutMemory_ExactRetry_ResponseStability(t *testing.T) {
	revisionID := uuid.New()
	createdAt := time.Date(2025, 5, 10, 12, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2025, 5, 10, 13, 0, 0, 0, time.UTC)
	attrs := map[string]interface{}{"category": "work"}

	store := &mockIdempotencyEpisodicStore{
		lookupResult: &registryepisodic.MemoryWriteResult{
			ID:         revisionID,
			Namespace:  []string{"user", "alice", "work"},
			Key:        "report",
			MemoryKind: "custom/v2",
			Revision:   2, // e.g. after superseding revision
			CreatedAt:  createdAt,
			ExpiresAt:  &expiresAt,
			Attributes: attrs,
			ExactRetry: true,
		},
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "work"},
		"key":         "report",
		"value":       map[string]interface{}{"title": "Quarterly"},
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusOK, recorder.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, revisionID.String(), resp["id"])
	require.Equal(t, "report", resp["key"])
	require.Equal(t, "custom/v2", resp["kind"])
	require.Equal(t, float64(2), resp["revision"])
	require.Equal(t, createdAt.Format(time.RFC3339Nano), resp["createdAt"])
	require.Equal(t, expiresAt.Format(time.RFC3339Nano), resp["expiresAt"])
	require.Equal(t, attrs, resp["attributes"])
}

func TestUserPutMemory_ExactRetry_BypassesNewlyReadOnlyKind(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: &registryepisodic.MemoryWriteResult{
			ID:         revisionID,
			Namespace:  []string{"user", "alice", "notes"},
			Key:        "k1",
			MemoryKind: "profile/v1",
			Revision:   1,
			CreatedAt:  time.Now().UTC(),
			ExactRetry: true,
		},
		resolveKindErr: registryepisodic.ErrMemoryKindNotWritable, // simulate kind became read-only
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "hello"},
		"kind":        "profile/v1",
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusOK, recorder.Code, "exact retry must bypass ResolveKindForWrite and return 200")
	require.Equal(t, 0, store.resolveKindCalls)
}

func TestUserPutMemory_NewWrite_RespectsKindNotWritable(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult:   nil, // new write (not found)
		resolveKindErr: registryepisodic.ErrMemoryKindNotWritable,
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "hello"},
		"kind":        "profile/v1",
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusBadRequest, recorder.Code, "new write must enforce kind writability")
	require.Equal(t, 1, store.resolveKindCalls)
}

func TestUserPutMemory_AuthzPrecedesLookup(t *testing.T) {
	ctx := context.Background()
	policy, err := episodic.NewPolicyEngine(ctx, "")
	require.NoError(t, err)

	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		predecessor: &registryepisodic.MemoryPredecessor{
			ID:         uuid.New(),
			Revision:   1,
			MemoryKind: "default/v1",
		},
		lookupResult: &registryepisodic.MemoryWriteResult{
			ID:         revisionID,
			Namespace:  []string{"user", "bob", "secret"},
			Key:        "k1",
			MemoryKind: "default/v1",
			Revision:   1,
			CreatedAt:  time.Now().UTC(),
			ExactRetry: true,
		},
	}

	// Request targets user bob namespace, but client has no bob credentials
	payload := map[string]interface{}{
		"namespace":   []string{"user", "bob", "secret"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "probe"},
		"revision_id": revisionID.String(),
	}

	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/v1/memories", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	// Set identity as alice
	c.Set(security.ContextKeyUserID, "alice")
	c.Set(security.ContextKeyClientID, "agent-alice")
	cfg := config.DefaultConfig()
	putMemoryWithEvents(c, store, nil, nil, policy, &cfg)

	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
	require.Equal(t, 0, store.lookupCalls, "LookupMemoryByID must NOT be called when authz fails")
}

// ──────────────────────────────────────────────────────────────────────────────
// Admin REST Tests
// ──────────────────────────────────────────────────────────────────────────────

func TestAdminPutMemory_NewWrite_WithRevisionID_Returns201(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: nil,
	}
	bus := &recordingEventBus{}

	payload := map[string]interface{}{
		"namespace":   []string{"system", "config"},
		"key":         "k1",
		"value":       map[string]interface{}{"mode": "fast"},
		"revision_id": revisionID.String(),
	}

	recorder := executeAdminPut(t, store, bus, payload)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, revisionID.String(), resp["id"])
	require.Equal(t, 1, store.lookupCalls)
	require.Equal(t, 1, store.putCalls)
	require.NotNil(t, store.lastPutReq.RevisionID)
	require.Equal(t, revisionID, *store.lastPutReq.RevisionID)
}

func TestAdminPutMemory_ExactRetry_Returns200_NoSideEffects(t *testing.T) {
	revisionID := uuid.New()
	createdAt := time.Now().UTC().Truncate(time.Second)
	store := &mockIdempotencyEpisodicStore{
		lookupResult: &registryepisodic.MemoryWriteResult{
			ID:         revisionID,
			Namespace:  []string{"system", "config"},
			Key:        "k1",
			MemoryKind: "default/v1",
			Revision:   1,
			CreatedAt:  createdAt,
			ExactRetry: true,
		},
	}
	bus := &recordingEventBus{}

	payload := map[string]interface{}{
		"namespace":   []string{"system", "config"},
		"key":         "k1",
		"value":       map[string]interface{}{"mode": "fast"},
		"revision_id": revisionID.String(),
	}

	recorder := executeAdminPut(t, store, bus, payload)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, revisionID.String(), resp["id"])

	require.Equal(t, 1, store.lookupCalls)
	require.Equal(t, 0, store.putCalls, "PutMemory must be bypassed on exact retry")
	require.Equal(t, 0, bus.count(), "no events should be published on exact retry")
}

func TestAdminPutMemory_Conflict_ChangedPayload_Returns409(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupErr: &registrystore.MemoryRevisionIDConflictError{
			ConflictError: &registrystore.ConflictError{
				Message: "memory revision ID already in use with different content",
				Code:    registrystore.MemoryRevisionAlreadyExistsCode,
			},
			RevisionID: revisionID.String(),
		},
	}

	payload := map[string]interface{}{
		"namespace":   []string{"system", "config"},
		"key":         "k1",
		"value":       map[string]interface{}{"mode": "slow"},
		"revision_id": revisionID.String(),
	}

	recorder := executeAdminPut(t, store, nil, payload)
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, "memory_revision_already_exists", resp["code"])
	details, ok := resp["details"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, revisionID.String(), details["revisionId"])
}

func TestAdminPutMemory_ExpectedActiveRevisionIDMismatch_Returns409(t *testing.T) {
	revisionID := uuid.New()
	expectedID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: nil,
		putErr: &registrystore.ExpectedActiveRevisionIDMismatchError{
			ConflictError: &registrystore.ConflictError{
				Message: "active revision ID does not match expected_revision_id",
				Code:    registrystore.ExpectedActiveRevisionIDMismatchCode,
			},
			ExpectedID: expectedID.String(),
			ActualID:   uuid.New().String(),
		},
	}

	payload := map[string]interface{}{
		"namespace":            []string{"system", "config"},
		"key":                  "k1",
		"value":                map[string]interface{}{"mode": "fast"},
		"revision_id":          revisionID.String(),
		"expected_revision_id": expectedID.String(),
	}

	recorder := executeAdminPut(t, store, nil, payload)
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, "expected_active_revision_id_mismatch", resp["code"])
}

func TestAdminPutMemory_Validation_ExpectedRevisionIDWithoutRevisionID_Returns400(t *testing.T) {
	store := &mockIdempotencyEpisodicStore{}
	expectedID := uuid.New()

	payload := map[string]interface{}{
		"namespace":            []string{"system", "config"},
		"key":                  "k1",
		"value":                map[string]interface{}{"mode": "fast"},
		"expected_revision_id": expectedID.String(),
	}

	recorder := executeAdminPut(t, store, nil, payload)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Equal(t, 0, store.lookupCalls)
	require.Equal(t, 0, store.putCalls)
}

func TestAdminPutMemory_Validation_InvalidUUID_Returns400(t *testing.T) {
	store := &mockIdempotencyEpisodicStore{}

	t.Run("invalid revision_id", func(t *testing.T) {
		payload := map[string]interface{}{
			"namespace":   []string{"system", "config"},
			"key":         "k1",
			"value":       map[string]interface{}{"mode": "fast"},
			"revision_id": "not-a-valid-uuid",
		}
		recorder := executeAdminPut(t, store, nil, payload)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Equal(t, 0, store.lookupCalls)
	})

	t.Run("invalid expected_revision_id", func(t *testing.T) {
		payload := map[string]interface{}{
			"namespace":            []string{"system", "config"},
			"key":                  "k1",
			"value":                map[string]interface{}{"mode": "fast"},
			"revision_id":          uuid.New().String(),
			"expected_revision_id": "not-a-valid-uuid",
		}
		recorder := executeAdminPut(t, store, nil, payload)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Equal(t, 0, store.lookupCalls)
	})
}

func TestAdminPutMemory_Validation_InvalidRevisionValue_Returns400(t *testing.T) {
	store := &mockIdempotencyEpisodicStore{}
	revisionID := uuid.New()

	for _, invalidRev := range []int{0, 2, -1, 5} {
		payload := map[string]interface{}{
			"namespace":   []string{"system", "config"},
			"key":         "k1",
			"value":       map[string]interface{}{"mode": "fast"},
			"revision_id": revisionID.String(),
			"revision":    invalidRev,
		}

		recorder := executeAdminPut(t, store, nil, payload)
		require.Equal(t, http.StatusBadRequest, recorder.Code, "revision %d must return 400", invalidRev)
	}
	require.Equal(t, 0, store.lookupCalls)
	require.Equal(t, 0, store.putCalls)
}

// ──────────────────────────────────────────────────────────────────────────────
// MongoDB Callback Replay Safety
// ──────────────────────────────────────────────────────────────────────────────

func TestCallbackReplaySafety_UserPutMemory_ResetsStateOnReplay(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		txCallbacks: 2, // will invoke callback twice
	}

	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "replay"},
		"key":         "k1",
		"value":       map[string]interface{}{"attempt": "first"},
		"revision_id": revisionID.String(),
	}

	recorder := executeUserPut(t, store, nil, nil, payload)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, 2, store.txCallbackCount)
}

func TestCallbackReplaySafety_AdminPutMemory_ResetsStateOnReplay(t *testing.T) {
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		txCallbacks: 2,
	}

	payload := map[string]interface{}{
		"namespace":   []string{"system", "replay"},
		"key":         "k1",
		"value":       map[string]interface{}{"attempt": "first"},
		"revision_id": revisionID.String(),
	}

	recorder := executeAdminPut(t, store, nil, payload)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, 2, store.txCallbackCount)
}
