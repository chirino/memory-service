//go:build !nosqlite && sqlite_fts5

package grpc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	_ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// idemSQLitePut describes one PutMemory call made through the real interceptor chain.
type idemSQLitePut struct {
	ns         []string
	key        string
	value      *structpb.Struct
	revisionID uuid.UUID
	expectedID *uuid.UUID
	createOnly bool
}

type idemSQLiteEnv struct {
	ctx           context.Context
	episodicStore registryepisodic.EpisodicStore
	memStore      registrystore.MemoryStore
	surfaces      map[string]func(idemSQLitePut) (*pb.MemoryWriteResult, error)
}

func newIdemSQLiteEnv(t *testing.T) *idemSQLiteEnv {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.LocalUserID = "alice"
	cfg.UnixSocketAuth = "local"
	cfg.AdminUsers = "alice"
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = filepath.Join(t.TempDir(), "idem.db")
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

	resolver, err := security.NewTokenResolver(&cfg)
	require.NoError(t, err)
	interceptor := security.GRPCUnaryInterceptorWithRateLimiter(resolver, nil)
	userServer := &MemoriesServer{Store: episodicStore, Config: &cfg, MemoryStore: memStore}
	adminServer := &AdminMemoriesServer{Store: episodicStore, Config: &cfg, MemoryStore: memStore}

	strPtr := func(s string) *string { return &s }
	return &idemSQLiteEnv{
		ctx:           ctx,
		episodicStore: episodicStore,
		memStore:      memStore,
		surfaces: map[string]func(idemSQLitePut) (*pb.MemoryWriteResult, error){
			"user": func(p idemSQLitePut) (*pb.MemoryWriteResult, error) {
				req := &pb.PutMemoryRequest{Namespace: p.ns, Key: p.key, Value: p.value, TtlSeconds: 3600, RevisionId: strPtr(p.revisionID.String())}
				if p.expectedID != nil {
					req.ExpectedRevisionId = strPtr(p.expectedID.String())
				}
				if p.createOnly {
					req.Revision = i64Ptr(1)
				}
				resp, callErr := interceptor(context.Background(), req, &gogrpc.UnaryServerInfo{}, func(c context.Context, raw any) (any, error) {
					return userServer.PutMemory(c, raw.(*pb.PutMemoryRequest))
				})
				if callErr != nil {
					return nil, callErr
				}
				return resp.(*pb.MemoryWriteResult), nil
			},
			"admin": func(p idemSQLitePut) (*pb.MemoryWriteResult, error) {
				req := &pb.AdminPutMemoryRequest{Namespace: p.ns, Key: p.key, Value: p.value, TtlSeconds: 3600, RevisionId: strPtr(p.revisionID.String())}
				if p.expectedID != nil {
					req.ExpectedRevisionId = strPtr(p.expectedID.String())
				}
				if p.createOnly {
					req.Revision = i64Ptr(1)
				}
				resp, callErr := interceptor(context.Background(), req, &gogrpc.UnaryServerInfo{}, func(c context.Context, raw any) (any, error) {
					return adminServer.PutMemory(c, raw.(*pb.AdminPutMemoryRequest))
				})
				if callErr != nil {
					return nil, callErr
				}
				return resp.(*pb.MemoryWriteResult), nil
			},
		},
	}
}

func (e *idemSQLiteEnv) memoryOutboxCount(t *testing.T) int {
	t.Helper()
	n := 0
	require.NoError(t, e.memStore.InReadTx(e.ctx, func(txCtx context.Context) error {
		page, listErr := e.memStore.(registrystore.EventOutboxStore).ListOutboxEvents(txCtx, registrystore.OutboxQuery{AfterCursor: "start", Limit: 1000})
		require.NoError(t, listErr)
		for _, ev := range page.Events {
			if ev.Kind == "memory" {
				n++
			}
		}
		return nil
	}))
	return n
}

// active returns the active row for (ns, key), or nil.
func (e *idemSQLiteEnv) active(t *testing.T, ns []string, key string) *registryepisodic.MemoryItem {
	t.Helper()
	var item *registryepisodic.MemoryItem
	require.NoError(t, e.episodicStore.InReadTx(e.ctx, func(txCtx context.Context) error {
		got, getErr := e.episodicStore.GetMemory(txCtx, ns, key, registryepisodic.ArchiveFilterExclude)
		require.NoError(t, getErr)
		item = got
		return nil
	}))
	return item
}

// TestGRPCPutMemoryIdempotency_RealSQLite drives both gRPC handlers against the real SQLite
// store. The scripted-store tests cannot catch a mismatch between what the handler
// sends to LookupMemoryByID and what the store's MemoriesMatch expects (for example the
// memory kind), so this asserts end-to-end retry behavior, row counts, and outbox counts.
func TestGRPCPutMemoryIdempotency_RealSQLite(t *testing.T) {
	env := newIdemSQLiteEnv(t)
	value, err := structpb.NewStruct(map[string]interface{}{"text": "hello"})
	require.NoError(t, err)
	changed, err := structpb.NewStruct(map[string]interface{}{"text": "different"})
	require.NoError(t, err)

	for name, put := range env.surfaces {
		t.Run(name, func(t *testing.T) {
			ns := []string{"user", "alice", "e2e", name}
			id := uuid.New()
			call := func(revisionID uuid.UUID, v *structpb.Struct) (*pb.MemoryWriteResult, error) {
				return put(idemSQLitePut{ns: ns, key: "k1", value: v, revisionID: revisionID, createOnly: true})
			}

			first, err := call(id, value)
			require.NoError(t, err)
			require.Equal(t, id, idemUUIDFromResponse(t, first))
			require.Equal(t, int64(1), first.GetRevision())
			outboxAfterFirst := env.memoryOutboxCount(t)
			require.GreaterOrEqual(t, outboxAfterFirst, 1, "the first write must append an outbox record, or the later no-growth assertions are vacuous")

			// Exact retry: same ID and content returns the stored revision, adds nothing.
			retry, err := call(id, value)
			require.NoError(t, err, "an exact retry must be recognized by the real store's MemoriesMatch")
			require.Equal(t, id, idemUUIDFromResponse(t, retry))
			require.Equal(t, first.GetCreatedAt(), retry.GetCreatedAt())
			require.Equal(t, first.GetExpiresAt(), retry.GetExpiresAt())
			require.Equal(t, first.GetKind(), retry.GetKind())
			require.Equal(t, outboxAfterFirst, env.memoryOutboxCount(t), "retry must not append an outbox record")
			require.Equal(t, int64(1), env.active(t, ns, "k1").Revision, "retry must not archive or insert")

			// Same ID, different content.
			_, err = call(id, changed)
			require.Equal(t, codes.Aborted, status.Code(err))
			require.Contains(t, status.Convert(err).Message(), registrystore.MemoryRevisionAlreadyExistsCode)
			require.Equal(t, outboxAfterFirst, env.memoryOutboxCount(t))
			require.Equal(t, int64(1), env.active(t, ns, "k1").Revision)

			// Create-only with a new ID while an active row exists.
			_, err = call(uuid.New(), value)
			require.Equal(t, codes.Aborted, status.Code(err))
			require.Equal(t, outboxAfterFirst, env.memoryOutboxCount(t))
			require.Equal(t, int64(1), env.active(t, ns, "k1").Revision)
		})
	}
}

// TestGRPCPutMemoryIdempotency_RealSQLite_UpdateFlow covers expected_revision_id success and
// mismatch, retry of a superseded revision, and retry after the revision was archived.
func TestGRPCPutMemoryIdempotency_RealSQLite_UpdateFlow(t *testing.T) {
	env := newIdemSQLiteEnv(t)
	v1, err := structpb.NewStruct(map[string]interface{}{"text": "v1"})
	require.NoError(t, err)
	v2, err := structpb.NewStruct(map[string]interface{}{"text": "v2"})
	require.NoError(t, err)

	for name, put := range env.surfaces {
		t.Run(name, func(t *testing.T) {
			ns := []string{"user", "alice", "update", name}
			id1, id2, wrongID := uuid.New(), uuid.New(), uuid.New()

			_, err := put(idemSQLitePut{ns: ns, key: "k1", value: v1, revisionID: id1, createOnly: true})
			require.NoError(t, err)

			// Mismatch: expected_revision_id is not the active revision.
			beforeMismatch := env.memoryOutboxCount(t)
			_, err = put(idemSQLitePut{ns: ns, key: "k1", value: v2, revisionID: id2, expectedID: &wrongID})
			require.Equal(t, codes.Aborted, status.Code(err))
			require.Contains(t, status.Convert(err).Message(), registrystore.ExpectedActiveRevisionIDMismatchCode)
			require.NotContains(t, status.Convert(err).Message(), id1.String(), "the stored active revision ID must not be disclosed")
			require.Equal(t, beforeMismatch, env.memoryOutboxCount(t), "a rejected write must not emit")
			require.Equal(t, id1, env.active(t, ns, "k1").ID, "a rejected write must leave the active row alone")

			// Success: expected_revision_id matches the active revision.
			updated, err := put(idemSQLitePut{ns: ns, key: "k1", value: v2, revisionID: id2, expectedID: &id1})
			require.NoError(t, err)
			require.Equal(t, id2, idemUUIDFromResponse(t, updated))
			require.Equal(t, int64(2), updated.GetRevision())
			require.Equal(t, id2, env.active(t, ns, "k1").ID)
			afterUpdate := env.memoryOutboxCount(t)
			require.Greater(t, afterUpdate, beforeMismatch, "the update must emit")

			// Retry of the update: the active row is now id2 and expected_revision_id (id1) is
			// stale, yet the exact retry must still succeed without writing.
			retry, err := put(idemSQLitePut{ns: ns, key: "k1", value: v2, revisionID: id2, expectedID: &id1})
			require.NoError(t, err, "a retry must not be gated on expected_revision_id still being active")
			require.Equal(t, id2, idemUUIDFromResponse(t, retry))
			require.Equal(t, updated.GetCreatedAt(), retry.GetCreatedAt())
			require.Equal(t, afterUpdate, env.memoryOutboxCount(t))
			require.Equal(t, id2, env.active(t, ns, "k1").ID)

			// Retry of the superseded first revision (still present, deleted_reason 0): 200.
			supersededRetry, err := put(idemSQLitePut{ns: ns, key: "k1", value: v1, revisionID: id1, createOnly: true})
			require.NoError(t, err, "a superseded, not-yet-evicted revision must still be an exact retry")
			require.Equal(t, id1, idemUUIDFromResponse(t, supersededRetry))
			require.Equal(t, afterUpdate, env.memoryOutboxCount(t))
			require.Equal(t, id2, env.active(t, ns, "k1").ID, "retrying a superseded revision must not resurrect it")

			// Archive the key: its revisions now have deleted_reason 1, so a retry is 404.
			require.NoError(t, env.episodicStore.InWriteTx(env.ctx, func(txCtx context.Context) error {
				return env.episodicStore.ArchiveMemory(txCtx, ns, "k1", nil)
			}))
			afterArchive := env.memoryOutboxCount(t)
			_, err = put(idemSQLitePut{ns: ns, key: "k1", value: v2, revisionID: id2})
			require.Equal(t, codes.NotFound, status.Code(err))
			require.Nil(t, env.active(t, ns, "k1"), "a rejected retry must not revive the key")
			require.Equal(t, afterArchive, env.memoryOutboxCount(t))
		})
	}
}

// TestGRPCPutMemoryIdempotency_RealSQLite_IDReuse covers spec D4: a revision_id is globally
// unique, so reusing it under another key or namespace is a generic conflict that discloses
// nothing about the stored row and leaves it untouched.
func TestGRPCPutMemoryIdempotency_RealSQLite_IDReuse(t *testing.T) {
	env := newIdemSQLiteEnv(t)
	stored, err := structpb.NewStruct(map[string]interface{}{"text": "secret-stored-value"})
	require.NoError(t, err)
	attempt, err := structpb.NewStruct(map[string]interface{}{"text": "attempted-value"})
	require.NoError(t, err)

	for name, put := range env.surfaces {
		t.Run(name, func(t *testing.T) {
			nsA := []string{"user", "alice", "reuse", name, "nsA-marker"}
			nsB := []string{"user", "alice", "reuse", name, "nsB-marker"}
			id := uuid.New()

			first, err := put(idemSQLitePut{ns: nsA, key: "keyA-marker", value: stored, revisionID: id, createOnly: true})
			require.NoError(t, err)
			baseline := env.memoryOutboxCount(t)

			cases := map[string]idemSQLitePut{
				"different key, same namespace": {ns: nsA, key: "keyB-marker", value: attempt, revisionID: id, createOnly: true},
				"different namespace":           {ns: nsB, key: "keyA-marker", value: attempt, revisionID: id, createOnly: true},
				"different namespace, update":   {ns: nsB, key: "keyA-marker", value: attempt, revisionID: id},
			}
			for label, p := range cases {
				_, err := put(p)
				require.Equal(t, codes.Aborted, status.Code(err), label)
				msg := status.Convert(err).Message()
				require.Contains(t, msg, registrystore.MemoryRevisionAlreadyExistsCode, label)
				require.Contains(t, msg, id.String(), label)
				for _, leaked := range []string{"nsA-marker", "keyA-marker", "secret-stored-value", first.GetCreatedAt()} {
					require.NotContains(t, msg, leaked, "%s: the conflict message must not disclose stored data", label)
				}
				require.Equal(t, baseline, env.memoryOutboxCount(t), label)
				require.Nil(t, env.active(t, nsB, "keyA-marker"), "%s: nothing may be written under the caller's namespace", label)
				require.Nil(t, env.active(t, nsA, "keyB-marker"), label)
			}

			row := env.active(t, nsA, "keyA-marker")
			require.NotNil(t, row)
			require.Equal(t, id, row.ID)
			require.Equal(t, int64(1), row.Revision)
			require.Equal(t, "secret-stored-value", row.Value["text"], "the original row must be unchanged")
		})
	}
}
