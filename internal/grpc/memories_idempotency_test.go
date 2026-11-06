package grpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/model"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// idemEpisodicStore is a scripted EpisodicStore for PutMemory idempotency handler tests.
// It records call order so tests can assert the pre-flight lookup precedes kind resolution.
type idemEpisodicStore struct {
	registryepisodic.EpisodicStore

	// txRuns is how many times InWriteTx invokes the callback (simulates a Mongo
	// WithTransaction replay). Zero means once.
	txRuns int
	txSeen int

	lookupFn      func(call int, id uuid.UUID, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error)
	lookupCalls   int
	lastLookupID  uuid.UUID
	lastLookupReq registryepisodic.PutMemoryRequest

	predecessor      *registryepisodic.MemoryPredecessor
	predecessorCalls int

	resolveCalls int
	resolveErr   error

	kindVersionCalls int

	putCalls   int
	lastPut    registryepisodic.PutMemoryRequest
	putResult  *registryepisodic.MemoryWriteResult
	putErr     error
	callOrders []string
}

func (s *idemEpisodicStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	runs := s.txRuns
	if runs == 0 {
		runs = 1
	}
	var err error
	for range runs {
		s.txSeen++
		if err = fn(ctx); err != nil {
			return err
		}
	}
	return err
}

func (s *idemEpisodicStore) GetMemoryPredecessor(context.Context, []string, string) (*registryepisodic.MemoryPredecessor, error) {
	s.predecessorCalls++
	s.callOrders = append(s.callOrders, "GetMemoryPredecessor")
	return s.predecessor, nil
}

func (s *idemEpisodicStore) LookupMemoryByID(_ context.Context, id uuid.UUID, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
	s.lookupCalls++
	s.lastLookupID = id
	s.lastLookupReq = req
	s.callOrders = append(s.callOrders, "LookupMemoryByID")
	if s.lookupFn == nil {
		return nil, nil
	}
	return s.lookupFn(s.lookupCalls, id, req)
}

func (s *idemEpisodicStore) ResolveKindForWrite(_ context.Context, sel string) (string, error) {
	s.resolveCalls++
	s.callOrders = append(s.callOrders, "ResolveKindForWrite")
	if s.resolveErr != nil {
		return "", s.resolveErr
	}
	if sel == "" {
		return "default/v1", nil
	}
	return sel, nil
}

func (s *idemEpisodicStore) GetMemoryKindVersion(_ context.Context, name string) (*model.MemoryKindVersion, error) {
	s.kindVersionCalls++
	return &model.MemoryKindVersion{Name: name, AttributeTypes: map[string]string{}, Writable: true}, nil
}

func (s *idemEpisodicStore) PutMemory(_ context.Context, req registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
	s.putCalls++
	s.lastPut = req
	s.callOrders = append(s.callOrders, "PutMemory")
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
		ID: id, Namespace: req.Namespace, Key: req.Key, MemoryKind: req.MemoryKind,
		Revision: 1, CreatedAt: time.Now().UTC(),
	}, nil
}

// idemOutboxStore is an outbox-enabled MemoryStore that records appended outbox rows.
// It must satisfy registrystore.EventOutboxStore in full, otherwise
// eventstream.AppendOutboxEvents silently skips the outbox path.
type idemOutboxStore struct {
	registrystore.MemoryStore
	appended []registrystore.OutboxWrite
}

var _ registrystore.EventOutboxStore = (*idemOutboxStore)(nil)

func (s *idemOutboxStore) OutboxEnabled() bool { return true }

func (s *idemOutboxStore) ListOutboxEvents(context.Context, registrystore.OutboxQuery) (*registrystore.OutboxPage, error) {
	return &registrystore.OutboxPage{}, nil
}

func (s *idemOutboxStore) EvictOutboxEventsBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (s *idemOutboxStore) AppendOutboxEvents(_ context.Context, events []registrystore.OutboxWrite) ([]registrystore.OutboxEvent, error) {
	out := make([]registrystore.OutboxEvent, 0, len(events))
	for _, e := range events {
		s.appended = append(s.appended, e)
		out = append(out, registrystore.OutboxEvent{Cursor: "c", Event: e.Event, Kind: e.Kind, Data: e.Data, CreatedAt: time.Now().UTC()})
	}
	return out, nil
}

type idemHarness struct {
	store  *idemEpisodicStore
	mem    *idemOutboxStore
	bus    *recordingGRPCEventBus
	policy *episodic.PolicyEngine
}

func newIdemHarness() *idemHarness {
	return &idemHarness{store: &idemEpisodicStore{}, mem: &idemOutboxStore{}, bus: &recordingGRPCEventBus{}}
}

// idemFields is the surface-independent PutMemory request shape.
type idemFields struct {
	namespace          []string
	key                string
	value              map[string]interface{}
	index              map[string]string
	ttl                int32
	kind               *string
	expectedRevision   *int64
	revisionID         *string
	expectedRevisionID *string
	revision           *int64
}

func idemBase() idemFields {
	return idemFields{
		namespace: []string{"user", "alice", "idem"},
		key:       "k1",
		value:     map[string]interface{}{"text": "hello"},
	}
}

func strPtr(s string) *string { return &s }
func i64Ptr(v int64) *int64   { return &v }

func (f idemFields) userReq(t *testing.T) *pb.PutMemoryRequest {
	t.Helper()
	v, err := structpb.NewStruct(f.value)
	require.NoError(t, err)
	return &pb.PutMemoryRequest{
		Namespace: f.namespace, Key: f.key, Value: v, Index: f.index, TtlSeconds: f.ttl, Kind: f.kind,
		ExpectedRevision: f.expectedRevision, RevisionId: f.revisionID,
		ExpectedRevisionId: f.expectedRevisionID, Revision: f.revision,
	}
}

func (f idemFields) adminReq(t *testing.T) *pb.AdminPutMemoryRequest {
	t.Helper()
	v, err := structpb.NewStruct(f.value)
	require.NoError(t, err)
	return &pb.AdminPutMemoryRequest{
		Namespace: f.namespace, Key: f.key, Value: v, Index: f.index, TtlSeconds: f.ttl, Kind: f.kind,
		ExpectedRevision: f.expectedRevision, RevisionId: f.revisionID,
		ExpectedRevisionId: f.expectedRevisionID, Revision: f.revision,
	}
}

func idemInterceptor(t *testing.T) (gogrpc.UnaryServerInterceptor, *config.Config) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.LocalUserID = "alice"
	cfg.UnixSocketAuth = "local"
	cfg.AdminUsers = "alice"
	resolver, err := security.NewTokenResolver(&cfg)
	require.NoError(t, err)
	return security.GRPCUnaryInterceptorWithRateLimiter(resolver, nil), &cfg
}

type idemSurface struct {
	name string
	call func(t *testing.T, h *idemHarness, f idemFields) (*pb.MemoryWriteResult, error)
}

var idemSurfaces = []idemSurface{
	{
		name: "user",
		call: func(t *testing.T, h *idemHarness, f idemFields) (*pb.MemoryWriteResult, error) {
			interceptor, cfg := idemInterceptor(t)
			server := &MemoriesServer{Store: h.store, Config: cfg, MemoryStore: h.mem, EventBus: h.bus, Policy: h.policy}
			req := f.userReq(t)
			resp, err := interceptor(context.Background(), req, &gogrpc.UnaryServerInfo{}, func(ctx context.Context, raw any) (any, error) {
				return server.PutMemory(ctx, raw.(*pb.PutMemoryRequest))
			})
			if err != nil {
				return nil, err
			}
			return resp.(*pb.MemoryWriteResult), nil
		},
	},
	{
		name: "admin",
		call: func(t *testing.T, h *idemHarness, f idemFields) (*pb.MemoryWriteResult, error) {
			interceptor, cfg := idemInterceptor(t)
			server := &AdminMemoriesServer{Store: h.store, Config: cfg, MemoryStore: h.mem, EventBus: h.bus}
			req := f.adminReq(t)
			resp, err := interceptor(context.Background(), req, &gogrpc.UnaryServerInfo{}, func(ctx context.Context, raw any) (any, error) {
				return server.PutMemory(ctx, raw.(*pb.AdminPutMemoryRequest))
			})
			if err != nil {
				return nil, err
			}
			return resp.(*pb.MemoryWriteResult), nil
		},
	},
}

func forEachIdemSurface(t *testing.T, fn func(t *testing.T, s idemSurface)) {
	t.Helper()
	for _, s := range idemSurfaces {
		t.Run(s.name, func(t *testing.T) { fn(t, s) })
	}
}

func idemExactRetryResult(id uuid.UUID) *registryepisodic.MemoryWriteResult {
	created := time.Date(2025, 5, 10, 12, 0, 0, 0, time.UTC)
	expires := created.Add(time.Hour)
	return &registryepisodic.MemoryWriteResult{
		ID: id, Namespace: []string{"user", "alice", "idem"}, Key: "k1", MemoryKind: "profile/v1",
		Revision: 2, CreatedAt: created, ExpiresAt: &expires,
		Attributes: map[string]interface{}{"category": "work"}, ExactRetry: true,
	}
}

func idemUUIDFromResponse(t *testing.T, resp *pb.MemoryWriteResult) uuid.UUID {
	t.Helper()
	id, err := bytesToUUID(resp.GetId())
	require.NoError(t, err)
	return id
}

// --- New write ---

func TestGRPCPutMemoryIdempotency_NewWriteUsesCallerSuppliedID(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		id := uuid.New()
		f := idemBase()
		f.revisionID = strPtr(id.String())

		resp, err := s.call(t, h, f)
		require.NoError(t, err)

		require.Equal(t, id, idemUUIDFromResponse(t, resp), "response id must be the caller-supplied revision_id")
		require.Len(t, resp.GetId(), 16, "MemoryWriteResult.id is 16 raw bytes")
		require.Equal(t, 1, h.store.lookupCalls)
		require.Equal(t, id, h.store.lastLookupID)
		require.Equal(t, 1, h.store.putCalls)
		require.NotNil(t, h.store.lastPut.RevisionID)
		require.Equal(t, id, *h.store.lastPut.RevisionID)
		require.Nil(t, h.store.lastPut.ExpectedRevisionID)
		require.False(t, h.store.lastPut.CreateOnly)
		require.Len(t, h.mem.appended, 1, "a new write appends exactly one outbox record")
		require.Len(t, h.bus.published(), 1, "a new write publishes exactly one event")
	})
}

func TestGRPCPutMemoryIdempotency_LookupPrecedesKindResolution(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		f := idemBase()
		f.revisionID = strPtr(uuid.NewString())

		_, err := s.call(t, h, f)
		require.NoError(t, err)

		lookupAt, resolveAt := -1, -1
		for i, name := range h.store.callOrders {
			switch name {
			case "LookupMemoryByID":
				lookupAt = i
			case "ResolveKindForWrite":
				resolveAt = i
			}
		}
		require.GreaterOrEqual(t, lookupAt, 0, "LookupMemoryByID must be called")
		require.GreaterOrEqual(t, resolveAt, 0, "ResolveKindForWrite must be called for a new write")
		require.Less(t, lookupAt, resolveAt, "pre-flight lookup must run before ResolveKindForWrite: %v", h.store.callOrders)
		require.Equal(t, 1, h.store.resolveCalls, "kind is resolved exactly once")
	})
}

func TestGRPCPutMemoryIdempotency_AbsentRevisionIDKeepsExistingBehavior(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		resp, err := s.call(t, h, idemBase())
		require.NoError(t, err)

		require.Equal(t, 0, h.store.lookupCalls, "no revision_id means no pre-flight lookup")
		require.Equal(t, 1, h.store.putCalls)
		require.Nil(t, h.store.lastPut.RevisionID)
		require.Nil(t, h.store.lastPut.ExpectedRevisionID)
		require.False(t, h.store.lastPut.CreateOnly)
		require.NotEqual(t, uuid.Nil, idemUUIDFromResponse(t, resp))
		require.Len(t, h.mem.appended, 1)
	})
}

func TestGRPCPutMemoryIdempotency_LookupReceivesCanonicalRequestFields(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		for _, tc := range []struct {
			name     string
			kind     *string
			wantKind string
		}{
			{"absent kind uses default/v1", nil, "default/v1"},
			{"explicit kind", strPtr("profile/v1"), "profile/v1"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newIdemHarness()
				id := uuid.New()
				f := idemBase()
				f.value = map[string]interface{}{"text": "content"}
				f.index = map[string]string{"summary": "s"}
				f.ttl = 3600
				f.kind = tc.kind
				f.expectedRevision = i64Ptr(3)
				f.revisionID = strPtr(id.String())
				f.expectedRevisionID = strPtr(uuid.NewString())
				f.revision = nil

				_, err := s.call(t, h, f)
				require.NoError(t, err)

				got := h.store.lastLookupReq
				require.Equal(t, f.namespace, got.Namespace)
				require.Equal(t, "k1", got.Key)
				require.Equal(t, f.value, got.Value)
				require.Equal(t, f.index, got.Index)
				require.Equal(t, 3600, got.TTLSeconds)
				require.Equal(t, tc.wantKind, got.MemoryKind,
					"the lookup request must carry the canonical kind or MemoriesMatch can never match a stored row")
				require.Equal(t, &id, got.RevisionID)
				require.NotNil(t, got.ExpectedRevisionID)
				require.Equal(t, *f.expectedRevisionID, got.ExpectedRevisionID.String())
				require.Equal(t, int64(3), *got.ExpectedRevision)
			})
		}
	})
}

// --- Exact retry ---

func TestGRPCPutMemoryIdempotency_ExactRetryReturnsStoredRevisionWithoutSideEffects(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		id := uuid.New()
		stored := idemExactRetryResult(id)
		h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
			return stored, nil
		}
		f := idemBase()
		f.revisionID = strPtr(id.String())

		resp, err := s.call(t, h, f)
		require.NoError(t, err)

		require.Equal(t, id, idemUUIDFromResponse(t, resp))
		require.Equal(t, []string{"user", "alice", "idem"}, resp.GetNamespace())
		require.Equal(t, "k1", resp.GetKey())
		require.Equal(t, "profile/v1", resp.GetKind())
		require.Equal(t, int64(2), resp.GetRevision(), "revision is the stored row's current revision")
		require.Equal(t, "2025-05-10T12:00:00Z", resp.GetCreatedAt())
		require.Equal(t, "2025-05-10T13:00:00Z", resp.GetExpiresAt())
		require.Equal(t, map[string]interface{}{"category": "work"}, resp.GetAttributes().AsMap())

		require.Equal(t, 0, h.store.putCalls, "exact retry must not write")
		require.Equal(t, 0, h.store.resolveCalls, "exact retry must not resolve the kind")
		require.Equal(t, 0, h.store.kindVersionCalls, "exact retry must not project")
		require.Empty(t, h.mem.appended, "exact retry must not append an outbox record")
		require.Empty(t, h.bus.published(), "exact retry must not publish an event")
	})
}

func TestGRPCPutMemoryIdempotency_ExactRetryBypassesKindWritability(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		id := uuid.New()
		h.store.resolveErr = registryepisodic.ErrMemoryKindNotWritable
		h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
			return idemExactRetryResult(id), nil
		}
		f := idemBase()
		f.kind = strPtr("profile/v1")
		f.revisionID = strPtr(id.String())

		resp, err := s.call(t, h, f)
		require.NoError(t, err, "a recognized retry must succeed even if the kind became read-only")
		require.Equal(t, id, idemUUIDFromResponse(t, resp))
		require.Equal(t, 0, h.store.resolveCalls)
	})
}

func TestGRPCPutMemoryIdempotency_NewWriteStillRejectsNonWritableKind(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		h.store.resolveErr = registryepisodic.ErrMemoryKindNotWritable
		f := idemBase()
		f.kind = strPtr("profile/v1")
		f.revisionID = strPtr(uuid.NewString())

		_, err := s.call(t, h, f)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.Equal(t, 1, h.store.lookupCalls)
		require.Equal(t, 1, h.store.resolveCalls)
		require.Equal(t, 0, h.store.putCalls)
	})
}

func TestGRPCPutMemoryIdempotency_StoreLevelExactRetrySuppressesEvents(t *testing.T) {
	// A concurrent duplicate can pass the pre-flight lookup and be recognized by
	// PutMemory's in-transaction lookup. That result must not emit events either.
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		id := uuid.New()
		h.store.putResult = idemExactRetryResult(id)
		f := idemBase()
		f.revisionID = strPtr(id.String())

		resp, err := s.call(t, h, f)
		require.NoError(t, err)
		require.Equal(t, id, idemUUIDFromResponse(t, resp))
		require.Equal(t, 1, h.store.putCalls)
		require.Empty(t, h.mem.appended, "PutMemory reporting ExactRetry must not append an outbox record")
		require.Empty(t, h.bus.published(), "PutMemory reporting ExactRetry must not publish an event")
	})
}

// --- Conflicts and not-found ---

func TestGRPCPutMemoryIdempotency_ReusedIDWithDifferentContentIsAbortedWithoutLeakingStoredData(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		id := uuid.New()
		h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
			return nil, &registrystore.MemoryRevisionIDConflictError{
				ConflictError: &registrystore.ConflictError{
					Message: "memory revision ID already in use with different namespace or key",
					Code:    registrystore.MemoryRevisionAlreadyExistsCode,
				},
				RevisionID: id.String(),
			}
		}
		f := idemBase()
		f.revisionID = strPtr(id.String())

		resp, err := s.call(t, h, f)
		require.Nil(t, resp)
		require.Equal(t, codes.Aborted, status.Code(err))
		msg := status.Convert(err).Message()
		require.Contains(t, msg, registrystore.MemoryRevisionAlreadyExistsCode)
		require.Contains(t, msg, id.String())
		require.NotContains(t, msg, "namespace or key", "message must not reveal why the stored row differs")
		require.Equal(t, 0, h.store.resolveCalls)
		require.Equal(t, 0, h.store.putCalls)
		require.Empty(t, h.mem.appended)
		require.Empty(t, h.bus.published())
	})
}

func TestGRPCPutMemoryIdempotency_ArchivedOrExpiredIDIsNotFound(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		id := uuid.New()
		h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
			return nil, &registrystore.NotFoundError{Resource: "memory", ID: id.String()}
		}
		f := idemBase()
		f.revisionID = strPtr(id.String())

		_, err := s.call(t, h, f)
		require.Equal(t, codes.NotFound, status.Code(err))
		require.Equal(t, 0, h.store.putCalls)
		require.Empty(t, h.mem.appended)
	})
}

func TestGRPCPutMemoryIdempotency_PutMemoryNotFoundIsNotFound(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		h.store.putErr = &registrystore.NotFoundError{Resource: "memory", ID: "x"}
		f := idemBase()
		f.revisionID = strPtr(uuid.NewString())

		_, err := s.call(t, h, f)
		require.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestGRPCPutMemoryIdempotency_UnexpectedLookupErrorIsSanitizedInternal(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
			return nil, errors.New("dial postgres at secret.internal:5432")
		}
		f := idemBase()
		f.revisionID = strPtr(uuid.NewString())

		_, err := s.call(t, h, f)
		require.Equal(t, codes.Internal, status.Code(err))
		require.NotContains(t, status.Convert(err).Message(), "secret.internal")
	})
}

// --- Create-only ---

func TestGRPCPutMemoryIdempotency_CreateOnlySuccess(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		id := uuid.New()
		f := idemBase()
		f.revisionID = strPtr(id.String())
		f.revision = i64Ptr(1)

		resp, err := s.call(t, h, f)
		require.NoError(t, err)
		require.Equal(t, id, idemUUIDFromResponse(t, resp))
		require.True(t, h.store.lastLookupReq.CreateOnly)
		require.True(t, h.store.lastPut.CreateOnly)
		require.Equal(t, &id, h.store.lastPut.RevisionID)
		require.Len(t, h.mem.appended, 1)
	})
}

// The store's typed create-only conflict must keep mapping to ABORTED with the unchanged
// message; only the REST surface distinguishes it (revision_already_exists).
func TestGRPCPutMemoryIdempotency_CreateOnlyConflictIsAborted(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		h.store.putErr = &registryepisodic.CreateOnlyConflictError{}
		f := idemBase()
		f.revisionID = strPtr(uuid.NewString())
		f.revision = i64Ptr(1)

		resp, err := s.call(t, h, f)
		require.Nil(t, resp)
		require.Equal(t, codes.Aborted, status.Code(err))
		require.Equal(t, registryepisodic.ErrMemoryRevisionConflict.Error(), status.Convert(err).Message())
		require.True(t, h.store.lastPut.CreateOnly)
		require.Empty(t, h.mem.appended, "a failed create-only write must not append an outbox record")
		require.Empty(t, h.bus.published())
	})
}

// --- Expected active revision ID ---

func TestGRPCPutMemoryIdempotency_ExpectedRevisionIDSuccess(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		id, expected := uuid.New(), uuid.New()
		f := idemBase()
		f.revisionID = strPtr(id.String())
		f.expectedRevisionID = strPtr(expected.String())

		resp, err := s.call(t, h, f)
		require.NoError(t, err)
		require.Equal(t, id, idemUUIDFromResponse(t, resp))
		require.NotNil(t, h.store.lastPut.ExpectedRevisionID)
		require.Equal(t, expected, *h.store.lastPut.ExpectedRevisionID)
		require.False(t, h.store.lastPut.CreateOnly)
	})
}

func TestGRPCPutMemoryIdempotency_ExpectedRevisionIDMismatchIsAborted(t *testing.T) {
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		expected, actual := uuid.New(), uuid.New()
		h.store.putErr = &registrystore.ExpectedActiveRevisionIDMismatchError{
			ConflictError: &registrystore.ConflictError{
				Message: "active revision ID does not match expected_revision_id",
				Code:    registrystore.ExpectedActiveRevisionIDMismatchCode,
			},
			ExpectedID: expected.String(),
			ActualID:   actual.String(),
		}
		f := idemBase()
		f.revisionID = strPtr(uuid.NewString())
		f.expectedRevisionID = strPtr(expected.String())

		resp, err := s.call(t, h, f)
		require.Nil(t, resp)
		require.Equal(t, codes.Aborted, status.Code(err))
		msg := status.Convert(err).Message()
		require.Contains(t, msg, registrystore.ExpectedActiveRevisionIDMismatchCode)
		require.NotContains(t, msg, actual.String(), "message must not disclose the stored active revision ID")
		require.Empty(t, h.mem.appended)
		require.Empty(t, h.bus.published())
	})
}

// --- Validation ---

func TestGRPCPutMemoryIdempotency_InvalidArguments(t *testing.T) {
	valid := uuid.NewString()
	cases := []struct {
		name   string
		mutate func(*idemFields)
	}{
		{"expected_revision_id without revision_id", func(f *idemFields) { f.expectedRevisionID = strPtr(valid) }},
		{"revision without revision_id", func(f *idemFields) { f.revision = i64Ptr(1) }},
		{"revision 2", func(f *idemFields) { f.revisionID = strPtr(valid); f.revision = i64Ptr(2) }},
		{"revision 0", func(f *idemFields) { f.revisionID = strPtr(valid); f.revision = i64Ptr(0) }},
		{"revision negative", func(f *idemFields) { f.revisionID = strPtr(valid); f.revision = i64Ptr(-1) }},
		{"revision_id not a uuid", func(f *idemFields) { f.revisionID = strPtr("not-a-uuid") }},
		{"revision_id empty string", func(f *idemFields) { f.revisionID = strPtr("") }},
		{"revision_id whitespace", func(f *idemFields) { f.revisionID = strPtr("  ") }},
		{"expected_revision_id not a uuid", func(f *idemFields) { f.revisionID = strPtr(valid); f.expectedRevisionID = strPtr("nope") }},
		{"expected_revision_id empty string", func(f *idemFields) { f.revisionID = strPtr(valid); f.expectedRevisionID = strPtr("") }},
	}
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h := newIdemHarness()
				f := idemBase()
				tc.mutate(&f)

				resp, err := s.call(t, h, f)
				require.Nil(t, resp)
				require.Equal(t, codes.InvalidArgument, status.Code(err), "got %v", err)
				require.Equal(t, 0, h.store.txSeen, "validation must fail before any transaction opens")
				require.Equal(t, 0, h.store.lookupCalls)
				require.Equal(t, 0, h.store.putCalls)
				require.Empty(t, h.mem.appended)
			})
		}
	})
}

// --- Authorization ---

func TestGRPCPutMemoryIdempotency_UserAuthorizationPrecedesLookup(t *testing.T) {
	policy, err := episodic.NewPolicyEngine(context.Background(), "")
	require.NoError(t, err)

	t.Run("no active row", func(t *testing.T) {
		h := newIdemHarness()
		h.policy = policy
		id := uuid.New()
		h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
			return idemExactRetryResult(id), nil
		}
		f := idemBase()
		f.namespace = []string{"user", "bob", "secret"} // alice has no write access
		f.revisionID = strPtr(id.String())

		_, err := idemSurfaces[0].call(t, h, f)
		require.Equal(t, codes.PermissionDenied, status.Code(err))
		require.Equal(t, 0, h.store.lookupCalls, "an unauthorized caller must not be able to probe revision IDs")
		require.Equal(t, 0, h.store.putCalls)
	})

	t.Run("active row exists", func(t *testing.T) {
		h := newIdemHarness()
		h.policy = policy
		h.store.predecessor = &registryepisodic.MemoryPredecessor{ID: uuid.New(), Revision: 1, MemoryKind: "default/v1"}
		f := idemBase()
		f.namespace = []string{"user", "bob", "secret"}
		f.revisionID = strPtr(uuid.NewString())

		_, err := idemSurfaces[0].call(t, h, f)
		require.Equal(t, codes.PermissionDenied, status.Code(err))
		require.Equal(t, 0, h.store.lookupCalls)
	})

	t.Run("authorized caller still gets idempotent retry", func(t *testing.T) {
		h := newIdemHarness()
		h.policy = policy
		id := uuid.New()
		h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
			return idemExactRetryResult(id), nil
		}
		f := idemBase()
		f.revisionID = strPtr(id.String())

		resp, err := idemSurfaces[0].call(t, h, f)
		require.NoError(t, err)
		require.Equal(t, id, idemUUIDFromResponse(t, resp))
		require.Equal(t, 1, h.store.lookupCalls)
		require.Equal(t, 0, h.store.putCalls)
	})
}

// --- Transaction replay safety ---

func TestGRPCPutMemoryIdempotency_ReplayedCallbackDoesNotLeakAbortedAttemptState(t *testing.T) {
	// Attempt 1 performs a new write (events appended); the transaction is then
	// replayed and attempt 2 finds the winner's committed row (exact retry). Only the
	// committed attempt's outcome may escape: no events, and the exact-retry response.
	forEachIdemSurface(t, func(t *testing.T, s idemSurface) {
		h := newIdemHarness()
		h.store.txRuns = 2
		id := uuid.New()
		h.store.lookupFn = func(call int, _ uuid.UUID, _ registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
			if call == 1 {
				return nil, nil
			}
			return idemExactRetryResult(id), nil
		}
		f := idemBase()
		f.revisionID = strPtr(id.String())

		resp, err := s.call(t, h, f)
		require.NoError(t, err)
		require.Equal(t, 2, h.store.txSeen)
		require.Equal(t, 1, h.store.putCalls, "only the first attempt wrote")
		require.Equal(t, "profile/v1", resp.GetKind(), "response must come from the committed (second) attempt")
		require.Equal(t, int64(2), resp.GetRevision())
		require.Empty(t, h.bus.published(), "the aborted attempt's event must not be published")
	})
}

// --- Parity ---

func TestGRPCPutMemoryIdempotency_UserAndAdminReturnIdenticalPayloads(t *testing.T) {
	id := uuid.New()
	run := func(s idemSurface, scripted func(*idemHarness)) (*pb.MemoryWriteResult, error) {
		h := newIdemHarness()
		scripted(h)
		f := idemBase()
		f.revisionID = strPtr(id.String())
		return s.call(t, h, f)
	}
	scenarios := map[string]func(*idemHarness){
		"exact retry": func(h *idemHarness) {
			h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
				return idemExactRetryResult(id), nil
			}
		},
		"conflict": func(h *idemHarness) {
			h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
				return nil, &registrystore.MemoryRevisionIDConflictError{
					ConflictError: &registrystore.ConflictError{Message: "m", Code: registrystore.MemoryRevisionAlreadyExistsCode},
					RevisionID:    id.String(),
				}
			}
		},
		"not found": func(h *idemHarness) {
			h.store.lookupFn = func(int, uuid.UUID, registryepisodic.PutMemoryRequest) (*registryepisodic.MemoryWriteResult, error) {
				return nil, &registrystore.NotFoundError{Resource: "memory", ID: id.String()}
			}
		},
	}
	for name, scripted := range scenarios {
		t.Run(name, func(t *testing.T) {
			userResp, userErr := run(idemSurfaces[0], scripted)
			adminResp, adminErr := run(idemSurfaces[1], scripted)
			require.Equal(t, status.Code(userErr), status.Code(adminErr))
			if userErr != nil {
				require.Equal(t, status.Convert(userErr).Message(), status.Convert(adminErr).Message())
				return
			}
			require.True(t, proto.Equal(userResp, adminResp), "user=%v admin=%v", userResp, adminResp)
		})
	}
}
