//go:build !nosqlite && sqlite_fts5

package memories

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	"github.com/chirino/memory-service/internal/model"
	_ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// restSQLiteEnv wires the real REST handlers to the real SQLite store with the outbox on.
// The mock-based tests cannot catch a mismatch between what the handlers send to
// LookupMemoryByID and what the store's MemoriesMatch expects (for example the memory kind).
type restSQLiteEnv struct {
	ctx           context.Context
	episodicStore registryepisodic.EpisodicStore
	memStore      registrystore.MemoryStore
	router        *gin.Engine
}

func newRESTSQLiteEnv(t *testing.T) *restSQLiteEnv {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = filepath.Join(t.TempDir(), "rest-idem.db")
	cfg.DatastoreMigrateAtStart = true
	cfg.EncryptionDBDisabled = true
	cfg.OutboxEnabled = true
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))

	episodicLoader, err := registryepisodic.Select("sqlite")
	require.NoError(t, err)
	episodicStore, err := episodicLoader(ctx)
	require.NoError(t, err)
	memLoader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	memStore, err := memLoader(ctx)
	require.NoError(t, err)

	policy, err := episodic.NewPolicyEngine(ctx, "")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Identity comes from headers so one router serves several callers.
	identity := func(c *gin.Context) {
		if u := c.GetHeader("X-Test-User"); u != "" {
			c.Set(security.ContextKeyUserID, u)
			c.Set(security.ContextKeyClientID, "agent-"+u)
		}
		if c.GetHeader("X-Test-Admin") == "true" {
			c.Set(security.ContextKeyRoles, map[string]bool{security.RoleAdmin: true})
		}
		c.Next()
	}
	router.PUT("/v1/memories", identity, func(c *gin.Context) {
		HandlePutMemory(c, episodicStore, memStore, nil, policy, &cfg)
	})
	router.PUT("/admin/v1/memories", identity, func(c *gin.Context) {
		HandleAdminPutMemory(c, episodicStore, memStore, nil, nil, &cfg)
	})
	return &restSQLiteEnv{ctx: ctx, episodicStore: episodicStore, memStore: memStore, router: router}
}

func (e *restSQLiteEnv) put(t *testing.T, surface, user string, payload map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	path := "/v1/memories"
	if surface == "admin" {
		path = "/admin/v1/memories"
	}
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", user)
	if surface == "admin" {
		req.Header.Set("X-Test-Admin", "true")
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *restSQLiteEnv) outboxCount(t *testing.T) int {
	t.Helper()
	n := 0
	require.NoError(t, e.memStore.InReadTx(e.ctx, func(txCtx context.Context) error {
		page, err := e.memStore.(registrystore.EventOutboxStore).ListOutboxEvents(txCtx, registrystore.OutboxQuery{AfterCursor: "start", Limit: 1000})
		require.NoError(t, err)
		for _, ev := range page.Events {
			if ev.Kind == "memory" {
				n++
			}
		}
		return nil
	}))
	return n
}

func (e *restSQLiteEnv) active(t *testing.T, ns []string, key string) *registryepisodic.MemoryItem {
	t.Helper()
	var item *registryepisodic.MemoryItem
	require.NoError(t, e.episodicStore.InReadTx(e.ctx, func(txCtx context.Context) error {
		got, err := e.episodicStore.GetMemory(txCtx, ns, key, registryepisodic.ArchiveFilterExclude)
		require.NoError(t, err)
		item = got
		return nil
	}))
	return item
}

func (e *restSQLiteEnv) createKind(t *testing.T, name string, writable bool) {
	t.Helper()
	require.NoError(t, e.episodicStore.InWriteTx(e.ctx, func(txCtx context.Context) error {
		_, err := e.episodicStore.CreateMemoryKindVersion(txCtx, model.MemoryKindVersion{
			Name: name, AttributeTypes: map[string]string{}, Writable: writable,
		})
		return err
	}))
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// Finding 1: exact retries through the real store must be 200, for both REST surfaces, with
// the default kind and an explicit kind, and must cause no duplicate side effects.
func TestRESTPutMemoryIdempotency_RealSQLite_ExactRetry(t *testing.T) {
	env := newRESTSQLiteEnv(t)
	env.createKind(t, "profile/v1", true)

	kinds := map[string]interface{}{"default kind (omitted)": nil, "explicit kind": "profile/v1"}
	for _, surface := range []string{"user", "admin"} {
		for kindName, kind := range kinds {
			t.Run(surface+"/"+kindName, func(t *testing.T) {
				ns := []string{"user", "alice", "retry", surface, kindName}
				id := uuid.New()
				payload := map[string]interface{}{
					"namespace": ns, "key": "k1", "value": map[string]interface{}{"text": "hello"},
					"ttl_seconds": 3600, "revision_id": id.String(), "revision": 1,
				}
				if kind != nil {
					payload["kind"] = kind
				}

				first := env.put(t, surface, "alice", payload)
				require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
				firstBody := decodeBody(t, first)
				require.Equal(t, id.String(), firstBody["id"])
				outboxAfterFirst := env.outboxCount(t)
				require.GreaterOrEqual(t, outboxAfterFirst, 1, "the first write must append an outbox record, or the no-growth assertions are vacuous")

				retry := env.put(t, surface, "alice", payload)
				require.Equal(t, http.StatusOK, retry.Code, "an exact retry must be recognized by the real store: %s", retry.Body.String())
				retryBody := decodeBody(t, retry)
				for _, field := range []string{"id", "namespace", "key", "kind", "createdAt", "expiresAt"} {
					require.Equal(t, firstBody[field], retryBody[field], "field %q must be stable across a retry", field)
				}
				require.Equal(t, outboxAfterFirst, env.outboxCount(t), "retry must not append an outbox record")
				row := env.active(t, ns, "k1")
				require.NotNil(t, row)
				require.Equal(t, id, row.ID)
				require.Equal(t, int64(1), row.Revision, "retry must not archive or insert")

				// Several more retries stay stable.
				for i := 0; i < 3; i++ {
					again := env.put(t, surface, "alice", payload)
					require.Equal(t, http.StatusOK, again.Code, again.Body.String())
				}
				require.Equal(t, outboxAfterFirst, env.outboxCount(t))
			})
		}
	}
}

// Existing conflict and ID-reuse behavior must be unchanged by the kind fix.
func TestRESTPutMemoryIdempotency_RealSQLite_ConflictsAndReuse(t *testing.T) {
	env := newRESTSQLiteEnv(t)
	env.createKind(t, "profile/v1", true)

	for _, surface := range []string{"user", "admin"} {
		t.Run(surface, func(t *testing.T) {
			ns := []string{"user", "alice", "conflicts", surface}
			id := uuid.New()
			base := func() map[string]interface{} {
				return map[string]interface{}{
					"namespace": ns, "key": "k1", "value": map[string]interface{}{"text": "secret-stored-value"},
					"ttl_seconds": 3600, "revision_id": id.String(), "revision": 1,
				}
			}
			first := env.put(t, surface, "alice", base())
			require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
			baseline := env.outboxCount(t)

			assertConflict := func(label string, rec *httptest.ResponseRecorder, leaks ...string) {
				t.Helper()
				require.Equal(t, http.StatusConflict, rec.Code, "%s: %s", label, rec.Body.String())
				body := decodeBody(t, rec)
				require.Equal(t, registrystore.MemoryRevisionAlreadyExistsCode, body["code"], label)
				require.Equal(t, map[string]interface{}{"revisionId": id.String()}, body["details"], label)
				for _, leaked := range leaks {
					require.NotContains(t, rec.Body.String(), leaked, "%s: the conflict body must not disclose stored data", label)
				}
				require.Equal(t, baseline, env.outboxCount(t), label)
			}

			changedValue := base()
			changedValue["value"] = map[string]interface{}{"text": "different"}
			assertConflict("changed value", env.put(t, surface, "alice", changedValue), "secret-stored-value")

			changedTTL := base()
			changedTTL["ttl_seconds"] = 60
			assertConflict("changed ttl", env.put(t, surface, "alice", changedTTL))

			changedIndex := base()
			changedIndex["index"] = map[string]string{"summary": "different"}
			assertConflict("changed index", env.put(t, surface, "alice", changedIndex))

			// D8: the stored kind differs from the kind the retry resolves to.
			changedKind := base()
			changedKind["kind"] = "profile/v1"
			assertConflict("changed kind", env.put(t, surface, "alice", changedKind))

			otherKey := base()
			otherKey["key"] = "keyB-marker"
			assertConflict("different key", env.put(t, surface, "alice", otherKey), "k1")

			// Different namespace, still within alice's authorized subtree.
			otherNS := base()
			otherNS["namespace"] = []string{"user", "alice", "conflicts", surface, "nsB-marker"}
			assertConflict("different namespace", env.put(t, surface, "alice", otherNS), "conflicts")

			// Create-only with a new ID while an active row exists: unchanged 409.
			newID := base()
			newID["revision_id"] = uuid.NewString()
			rec := env.put(t, surface, "alice", newID)
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			createOnlyBody := decodeBody(t, rec)
			require.Equal(t, registrystore.RevisionAlreadyExistsCode, createOnlyBody["code"])
			require.NotContains(t, createOnlyBody, "details")
			require.NotContains(t, rec.Body.String(), "secret-stored-value")
			require.Equal(t, baseline, env.outboxCount(t))

			// A numeric expected_revision mismatch is a different conflict: no code.
			numeric := base()
			numeric["revision_id"] = uuid.NewString()
			delete(numeric, "revision") // revision 1 means create-only
			numeric["expected_revision"] = 99
			rec = env.put(t, surface, "alice", numeric)
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			numericBody := decodeBody(t, rec)
			require.NotContains(t, numericBody, "code", rec.Body.String())
			require.Equal(t, "memory revision conflict", numericBody["error"])
			require.Equal(t, baseline, env.outboxCount(t))

			// Spec: create-only plus a failing numeric expected_revision returns the numeric
			// conflict (no code), not revision_already_exists. No active row exists for this key.
			both := base()
			both["key"] = "fresh-key"
			both["revision_id"] = uuid.NewString()
			both["expected_revision"] = 99
			rec = env.put(t, surface, "alice", both)
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			bothBody := decodeBody(t, rec)
			require.NotContains(t, bothBody, "code", rec.Body.String())
			require.Equal(t, "memory revision conflict", bothBody["error"])
			require.Nil(t, env.active(t, ns, "fresh-key"), "the failed write must not create a row")
			require.Equal(t, baseline, env.outboxCount(t))

			row := env.active(t, ns, "k1")
			require.NotNil(t, row)
			require.Equal(t, id, row.ID)
			require.Equal(t, "secret-stored-value", row.Value["text"], "the original row must be unchanged")
			require.Equal(t, int64(1), row.Revision)
			require.Nil(t, env.active(t, ns, "keyB-marker"))
			require.Nil(t, env.active(t, []string{"user", "alice", "conflicts", surface, "nsB-marker"}, "k1"))
		})
	}
}

// expected_revision_id success, mismatch and the retry of the update, on the real store.
func TestRESTPutMemoryIdempotency_RealSQLite_UpdateFlow(t *testing.T) {
	env := newRESTSQLiteEnv(t)
	for _, surface := range []string{"user", "admin"} {
		t.Run(surface, func(t *testing.T) {
			ns := []string{"user", "alice", "update", surface}
			id1, id2, wrong := uuid.New(), uuid.New(), uuid.New()
			mk := func(id uuid.UUID, text string, expected *uuid.UUID) map[string]interface{} {
				p := map[string]interface{}{
					"namespace": ns, "key": "k1", "value": map[string]interface{}{"text": text}, "revision_id": id.String(),
				}
				if expected != nil {
					p["expected_revision_id"] = expected.String()
				}
				return p
			}
			require.Equal(t, http.StatusCreated, env.put(t, surface, "alice", mk(id1, "v1", nil)).Code)

			before := env.outboxCount(t)
			mismatch := env.put(t, surface, "alice", mk(id2, "v2", &wrong))
			require.Equal(t, http.StatusConflict, mismatch.Code, mismatch.Body.String())
			require.Equal(t, registrystore.ExpectedActiveRevisionIDMismatchCode, decodeBody(t, mismatch)["code"])
			require.Equal(t, before, env.outboxCount(t))
			require.Equal(t, id1, env.active(t, ns, "k1").ID)

			ok := env.put(t, surface, "alice", mk(id2, "v2", &id1))
			require.Equal(t, http.StatusCreated, ok.Code, ok.Body.String())
			afterUpdate := env.outboxCount(t)
			require.Greater(t, afterUpdate, before)

			// The retry's expected_revision_id (id1) is no longer active, but it is still an exact retry.
			retry := env.put(t, surface, "alice", mk(id2, "v2", &id1))
			require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
			require.Equal(t, afterUpdate, env.outboxCount(t))
			require.Equal(t, id2, env.active(t, ns, "k1").ID)
		})
	}
}

// Finding 2 on the real store: an unauthorized caller gets the same 403 whether or not the
// revision ID exists, and nothing is written.
func TestRESTPutMemoryIdempotency_RealSQLite_UnauthorizedCannotProbeIDs(t *testing.T) {
	env := newRESTSQLiteEnv(t)

	bobNS := []string{"user", "bob", "secret"}
	existingID := uuid.New()
	// The admin surface may write into bob's namespace.
	seed := env.put(t, "admin", "admin", map[string]interface{}{
		"namespace": bobNS, "key": "k1", "value": map[string]interface{}{"text": "bob-secret"},
		"revision_id": existingID.String(), "revision": 1,
	})
	require.Equal(t, http.StatusCreated, seed.Code, seed.Body.String())
	baseline := env.outboxCount(t)

	probe := func(id uuid.UUID, key string) *httptest.ResponseRecorder {
		return env.put(t, "user", "alice", map[string]interface{}{
			"namespace": bobNS, "key": key, "value": map[string]interface{}{"text": "probe"},
			"revision_id": id.String(),
		})
	}

	exists := probe(existingID, "k1")                 // ID exists, and an active row exists for the target
	existsNoActive := probe(existingID, "other-key")  // ID exists, no active row for the target key
	missing := probe(uuid.New(), "k1")                // ID unknown, active row exists
	missingNoActive := probe(uuid.New(), "other-key") // ID unknown, no active row
	for label, rec := range map[string]*httptest.ResponseRecorder{
		"id exists, active row": exists, "id exists, no active row": existsNoActive,
		"id unknown, active row": missing, "id unknown, no active row": missingNoActive,
	} {
		require.Equal(t, http.StatusForbidden, rec.Code, "%s: %s", label, rec.Body.String())
		require.NotContains(t, rec.Body.String(), "bob-secret", label)
		require.NotContains(t, rec.Body.String(), existingID.String(), label)
	}
	require.Equal(t, exists.Body.String(), existsNoActive.Body.String(), "403 bodies must not reveal whether the ID exists")
	require.Equal(t, exists.Body.String(), missing.Body.String())
	require.Equal(t, exists.Body.String(), missingNoActive.Body.String())

	require.Equal(t, baseline, env.outboxCount(t), "an unauthorized request must not write or emit")
	row := env.active(t, bobNS, "k1")
	require.NotNil(t, row)
	require.Equal(t, existingID, row.ID)
	require.Equal(t, "bob-secret", row.Value["text"])
	require.Nil(t, env.active(t, bobNS, "other-key"))
}

// Admin surface: a non-admin caller cannot probe IDs either, whether or not the ID exists.
func TestRESTPutMemoryIdempotency_RealSQLite_NonAdminCannotProbeAdminSurface(t *testing.T) {
	env := newRESTSQLiteEnv(t)
	ns := []string{"user", "bob", "secret"}
	existingID := uuid.New()
	require.Equal(t, http.StatusCreated, env.put(t, "admin", "admin", map[string]interface{}{
		"namespace": ns, "key": "k1", "value": map[string]interface{}{"text": "bob-secret"}, "revision_id": existingID.String(),
	}).Code)
	baseline := env.outboxCount(t)

	send := func(id uuid.UUID) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]interface{}{
			"namespace": ns, "key": "k1", "value": map[string]interface{}{"text": "probe"}, "revision_id": id.String(),
		})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPut, "/admin/v1/memories", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-User", "alice") // no admin role
		rec := httptest.NewRecorder()
		env.router.ServeHTTP(rec, req)
		return rec
	}
	existing := send(existingID)
	missing := send(uuid.New())
	require.Equal(t, http.StatusForbidden, existing.Code, existing.Body.String())
	require.Equal(t, http.StatusForbidden, missing.Code, missing.Body.String())
	require.Equal(t, existing.Body.String(), missing.Body.String())
	require.Equal(t, baseline, env.outboxCount(t))
}
