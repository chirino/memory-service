package memories

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// executeUserPutAs runs the user PUT handler as the given authenticated user with a real
// policy engine, so authorization outcomes depend on the actual default authz policy.
func executeUserPutAs(t *testing.T, store registryepisodic.EpisodicStore, eventBus registryeventbus.EventBus, policy *episodic.PolicyEngine, userID string, payload map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/v1/memories", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(security.ContextKeyUserID, userID)
	c.Set(security.ContextKeyClientID, "agent-"+userID)
	cfg := config.DefaultConfig()
	putMemoryWithEvents(c, store, nil, eventBus, policy, &cfg)
	return recorder
}

// executeAdminPutWithRoles runs the admin PUT handler with an explicit role set.
func executeAdminPutWithRoles(t *testing.T, store registryepisodic.EpisodicStore, eventBus registryeventbus.EventBus, roles map[string]bool, payload map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/admin/v1/memories", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if roles != nil {
		c.Set(security.ContextKeyRoles, roles)
	}
	cfg := config.DefaultConfig()
	HandleAdminPutMemory(c, store, nil, eventBus, nil, &cfg)
	return recorder
}

// Finding 1: the pre-flight lookup must carry the canonical memory kind, because the stores
// compare it (MemoriesMatch) and treat a mismatch as ID reuse with different content.
func TestPutMemoryPreflight_LookupCarriesCanonicalKind(t *testing.T) {
	cases := []struct {
		name     string
		kind     interface{} // nil means the field is omitted
		wantKind string
	}{
		{"omitted kind uses default/v1", nil, "default/v1"},
		{"empty kind uses default/v1", "", "default/v1"},
		{"whitespace kind uses default/v1", "   ", "default/v1"},
		{"explicit kind is used as given", "profile/v1", "profile/v1"},
		{"padded explicit kind is trimmed like ResolveKindForWrite", "  profile/v1  ", "profile/v1"},
	}
	surfaces := map[string]func(t *testing.T, store *mockIdempotencyEpisodicStore, payload map[string]interface{}) *httptest.ResponseRecorder{
		"user": func(t *testing.T, store *mockIdempotencyEpisodicStore, payload map[string]interface{}) *httptest.ResponseRecorder {
			return executeUserPut(t, store, nil, nil, payload)
		},
		"admin": func(t *testing.T, store *mockIdempotencyEpisodicStore, payload map[string]interface{}) *httptest.ResponseRecorder {
			return executeAdminPut(t, store, nil, payload)
		},
	}
	for surface, run := range surfaces {
		for _, tc := range cases {
			t.Run(surface+"/"+tc.name, func(t *testing.T) {
				revisionID := uuid.New()
				store := &mockIdempotencyEpisodicStore{
					lookupResult: &registryepisodic.MemoryWriteResult{
						ID: revisionID, Namespace: []string{"user", "alice", "notes"}, Key: "k1",
						MemoryKind: tc.wantKind, Revision: 1, CreatedAt: time.Now().UTC(), ExactRetry: true,
					},
				}
				payload := map[string]interface{}{
					"namespace":   []string{"user", "alice", "notes"},
					"key":         "k1",
					"value":       map[string]interface{}{"text": "hello"},
					"revision_id": revisionID.String(),
				}
				if tc.kind != nil {
					payload["kind"] = tc.kind
				}
				recorder := run(t, store, payload)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				require.Equal(t, 1, store.lookupCalls)
				require.Equal(t, tc.wantKind, store.lastLookupReq.MemoryKind,
					"LookupMemoryByID must receive the canonical kind or a real store cannot recognize the retry")
				require.Equal(t, 0, store.resolveKindCalls, "the canonical kind must be computed without ResolveKindForWrite")
			})
		}
	}
}

// Finding 2 (user): authorization must complete before LookupMemoryByID on every path,
// including when no active row exists for the target (namespace, key).
func TestUserPutMemoryPreflight_UnauthorizedNeverReachesStore(t *testing.T) {
	policy, err := episodic.NewPolicyEngine(context.Background(), "")
	require.NoError(t, err)

	revisionID := uuid.New()
	existing := &registryepisodic.MemoryWriteResult{
		ID: revisionID, Namespace: []string{"user", "bob", "secret"}, Key: "k1",
		MemoryKind: "default/v1", Revision: 1, CreatedAt: time.Now().UTC(), ExactRetry: true,
	}
	conflict := &registrystore.MemoryRevisionIDConflictError{
		ConflictError: &registrystore.ConflictError{Message: "in use", Code: registrystore.MemoryRevisionAlreadyExistsCode},
		RevisionID:    revisionID.String(),
	}
	notFound := &registrystore.NotFoundError{Resource: "memory", ID: revisionID.String()}

	// What the store would answer if it were (wrongly) consulted. Every one of these would
	// let an unauthorized caller tell the ID states apart (200 / 409 / 404 / 201).
	storeStates := map[string]*mockIdempotencyEpisodicStore{
		"id exists as an exact match":      {lookupResult: existing},
		"id exists with other content":     {lookupErr: conflict},
		"id exists but archived":           {lookupErr: notFound},
		"id does not exist":                {},
		"id exists and an active row does": {lookupResult: existing, predecessor: &registryepisodic.MemoryPredecessor{ID: uuid.New(), Revision: 1, MemoryKind: "default/v1"}},
	}

	denied := map[string]string{} // state -> response body, to compare for indistinguishability
	for state, store := range storeStates {
		t.Run(state, func(t *testing.T) {
			bus := &recordingEventBus{}
			payload := map[string]interface{}{
				"namespace":   []string{"user", "bob", "secret"}, // caller is alice
				"key":         "k1",
				"value":       map[string]interface{}{"text": "probe"},
				"revision_id": revisionID.String(),
			}
			recorder := executeUserPutAs(t, store, bus, policy, "alice", payload)
			require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
			require.Zero(t, store.lookupCalls, "LookupMemoryByID must not run before authorization succeeds")
			require.Zero(t, store.resolveKindCalls)
			require.Zero(t, store.putCalls, "no write may happen for an unauthorized caller")
			require.Zero(t, bus.count(), "no event may be published")
			require.NotContains(t, store.callOrder, "LookupMemoryByID")
			require.NotContains(t, store.callOrder, "PutMemory")
			denied[state] = recorder.Body.String()
		})
	}
	var reference string
	for state, body := range denied {
		if reference == "" {
			reference = body
		}
		require.Equal(t, reference, body, "the 403 body for %q must be identical to the others, so ID existence is not observable", state)
	}
	require.NotEmpty(t, reference)
}

func TestUserPutMemoryPreflight_PolicyDenialReasonDoesNotDependOnIDState(t *testing.T) {
	policy, err := episodic.NewPolicyEngine(context.Background(), "")
	require.NoError(t, err)
	index := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} { // 9 > the policy's max of 8
		index[k] = "v"
	}
	revisionID := uuid.New()
	for name, store := range map[string]*mockIdempotencyEpisodicStore{
		"id exists":         {lookupResult: &registryepisodic.MemoryWriteResult{ID: revisionID, ExactRetry: true, MemoryKind: "default/v1"}},
		"id does not exist": {},
	} {
		t.Run(name, func(t *testing.T) {
			payload := map[string]interface{}{
				"namespace":   []string{"user", "alice", "notes"}, // alice's own namespace, denied only for the index size
				"key":         "k1",
				"value":       map[string]interface{}{"text": "x"},
				"index":       index,
				"revision_id": revisionID.String(),
			}
			recorder := executeUserPutAs(t, store, nil, policy, "alice", payload)
			require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), "too many index fields")
			require.Zero(t, store.lookupCalls)
			require.Zero(t, store.putCalls)
		})
	}
}

// An authorized caller with no active row must still reach the lookup, after authorization.
func TestUserPutMemoryPreflight_AuthorizedWithoutActiveRowLooksUpAfterAuthz(t *testing.T) {
	policy, err := episodic.NewPolicyEngine(context.Background(), "")
	require.NoError(t, err)
	revisionID := uuid.New()
	store := &mockIdempotencyEpisodicStore{
		lookupResult: &registryepisodic.MemoryWriteResult{
			ID: revisionID, Namespace: []string{"user", "alice", "notes"}, Key: "k1",
			MemoryKind: "default/v1", Revision: 1, CreatedAt: time.Now().UTC(), ExactRetry: true,
		},
	}
	bus := &recordingEventBus{}
	payload := map[string]interface{}{
		"namespace":   []string{"user", "alice", "notes"},
		"key":         "k1",
		"value":       map[string]interface{}{"text": "hello"},
		"revision_id": revisionID.String(),
	}
	recorder := executeUserPutAs(t, store, bus, policy, "alice", payload)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, store.lookupCalls)
	require.Equal(t, []string{"GetMemoryPredecessor", "LookupMemoryByID"}, store.callOrder)
	require.Zero(t, store.resolveKindCalls)
	require.Zero(t, store.putCalls)
	require.Zero(t, bus.count())
}

// Finding 2 (admin): admin writes are authorized by the admin role (ensureAdmin), which runs
// before any store access. There is no policy check on this surface by design.
func TestAdminPutMemoryPreflight_NonAdminNeverReachesStore(t *testing.T) {
	revisionID := uuid.New()
	existing := &registryepisodic.MemoryWriteResult{
		ID: revisionID, Namespace: []string{"user", "bob", "secret"}, Key: "k1",
		MemoryKind: "default/v1", Revision: 1, CreatedAt: time.Now().UTC(), ExactRetry: true,
	}
	roles := map[string]map[string]bool{
		"no roles":     nil,
		"empty roles":  {},
		"auditor only": {security.RoleAuditor: true},
	}
	storeStates := map[string]func() *mockIdempotencyEpisodicStore{
		"id exists":         func() *mockIdempotencyEpisodicStore { return &mockIdempotencyEpisodicStore{lookupResult: existing} },
		"id does not exist": func() *mockIdempotencyEpisodicStore { return &mockIdempotencyEpisodicStore{} },
	}
	var reference string
	for roleName, r := range roles {
		for stateName, newStore := range storeStates {
			t.Run(roleName+"/"+stateName, func(t *testing.T) {
				store := newStore()
				bus := &recordingEventBus{}
				payload := map[string]interface{}{
					"namespace":   []string{"user", "bob", "secret"},
					"key":         "k1",
					"value":       map[string]interface{}{"text": "probe"},
					"revision_id": revisionID.String(),
				}
				recorder := executeAdminPutWithRoles(t, store, bus, r, payload)
				require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
				require.Zero(t, store.lookupCalls, "LookupMemoryByID must not run for a non-admin caller")
				require.Zero(t, store.predecessorCalls)
				require.Zero(t, store.resolveKindCalls)
				require.Zero(t, store.putCalls)
				require.Zero(t, bus.count())
				require.Zero(t, store.txCallbackCount, "no transaction may open for a non-admin caller")
				if reference == "" {
					reference = recorder.Body.String()
				}
				require.Equal(t, reference, recorder.Body.String(), "403 body must not depend on ID state or role set")
			})
		}
	}
}
