//go:build !nosqlite && sqlite_fts5

package grpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/operationevent"
	_ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests run the real gRPC attachment handlers against the real SQLite
// store and a probing blob store. They assert persisted rows and blob side
// effects, not only status codes.

const (
	attIdemAlice = "alice"
	attIdemBob   = "bob"
)

type attIdemBlobs struct {
	mu       sync.Mutex
	stores   int
	deletes  int
	keys     []string
	onStore  func(call int)
	failWith error
}

func (b *attIdemBlobs) Store(_ context.Context, reader io.Reader, _ int64, _ string) (*registryattach.FileStoreResult, error) {
	b.mu.Lock()
	b.stores++
	call := b.stores
	hook := b.onStore
	fail := b.failWith
	b.mu.Unlock()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if hook != nil {
		hook(call)
	}
	if fail != nil {
		return nil, fail
	}
	sum := sha256.Sum256(data)
	key := "blob-" + uuid.NewString()
	b.mu.Lock()
	b.keys = append(b.keys, key)
	b.mu.Unlock()
	return &registryattach.FileStoreResult{StorageKey: key, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

func (*attIdemBlobs) Retrieve(context.Context, string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *attIdemBlobs) Delete(context.Context, string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletes++
	return nil
}

func (*attIdemBlobs) GetSignedURL(context.Context, string, time.Duration, *registryattach.SignedURLOptions) (*url.URL, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *attIdemBlobs) storeCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stores
}

func (b *attIdemBlobs) deleteCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.deletes
}

func (b *attIdemBlobs) storedKeys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.keys...)
}

// attIdemStore intercepts the conditional writes to simulate a finalize whose
// commit succeeds but whose acknowledgment is lost, and a replayed transaction
// callback.
type attIdemStore struct {
	registrystore.MemoryStore
	loseAck      bool
	replay       bool
	finalized    atomic.Bool
	marks        atomic.Int32
	creates      atomic.Int32
	legacyCreate atomic.Int32
}

func (s *attIdemStore) FinalizeAttachment(ctx context.Context, userID string, id, attempt uuid.UUID, finalize registrystore.AttachmentFinalize) (*model.Attachment, error) {
	out, err := s.MemoryStore.FinalizeAttachment(ctx, userID, id, attempt, finalize)
	if err == nil {
		s.finalized.Store(true)
	}
	return out, err
}

func (s *attIdemStore) MarkAttachmentFailed(ctx context.Context, userID string, id, attempt uuid.UUID) (bool, error) {
	s.marks.Add(1)
	return s.MemoryStore.MarkAttachmentFailed(ctx, userID, id, attempt)
}

func (s *attIdemStore) CreateAttachmentWithID(ctx context.Context, userID string, id uuid.UUID, a model.Attachment) (*registrystore.CreateAttachmentResult, error) {
	s.creates.Add(1)
	return s.MemoryStore.CreateAttachmentWithID(ctx, userID, id, a)
}

func (s *attIdemStore) CreateAttachment(ctx context.Context, userID, conversationID string, a model.Attachment) (*model.Attachment, error) {
	s.legacyCreate.Add(1)
	return s.MemoryStore.CreateAttachment(ctx, userID, conversationID, a)
}

var errAttIdemRolledBack = errors.New("attempt rolled back for replay")

func (s *attIdemStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	if s.replay {
		err := s.MemoryStore.InWriteTx(ctx, func(txCtx context.Context) error {
			if err := fn(txCtx); err != nil {
				return err
			}
			return errAttIdemRolledBack
		})
		if err != nil && !errors.Is(err, errAttIdemRolledBack) {
			return err
		}
	}
	err := s.MemoryStore.InWriteTx(ctx, fn)
	if err == nil && s.loseAck && s.finalized.CompareAndSwap(true, false) {
		return errors.New("acknowledgment lost after commit")
	}
	return err
}

type attIdemStream struct {
	gogrpc.ServerStream
	ctx      context.Context
	requests []*pb.UploadAttachmentRequest
	// beforeEnd runs when the requests are exhausted, before the stream ends.
	beforeEnd func()
	// endErr is returned instead of io.EOF once the requests are exhausted.
	endErr   error
	response *pb.UploadAttachmentResponse
}

func (s *attIdemStream) Context() context.Context { return s.ctx }

func (s *attIdemStream) Recv() (*pb.UploadAttachmentRequest, error) {
	if len(s.requests) == 0 {
		if s.beforeEnd != nil {
			hook := s.beforeEnd
			s.beforeEnd = nil
			hook()
		}
		if s.endErr != nil {
			return nil, s.endErr
		}
		return nil, io.EOF
	}
	req := s.requests[0]
	s.requests = s.requests[1:]
	return req, nil
}

func (s *attIdemStream) SendAndClose(response *pb.UploadAttachmentResponse) error {
	s.response = response
	return nil
}

type attIdemEnv struct {
	ctx         context.Context
	real        registrystore.MemoryStore
	store       *attIdemStore
	blobs       *attIdemBlobs
	cfg         *config.Config
	server      *AttachmentsServer
	streamIntc  gogrpc.StreamServerInterceptor
	unaryIntc   gogrpc.UnaryServerInterceptor
	sourceHits  atomic.Int32
	sourceGate  chan struct{}
	sourceHook  func()
	sourceCode  int
	sourceStart *httptest.Server
	// event, when set, is attached to every request context like the operation
	// interceptor does.
	event *operationevent.Event
}

func (e *attIdemEnv) reqCtx() context.Context {
	if e.event != nil {
		return operationevent.WithContext(context.Background(), e.event)
	}
	return context.Background()
}

func newAttIdemEnv(t *testing.T) *attIdemEnv {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.LocalUserID = attIdemAlice
	cfg.LocalClientID = "test-client"
	cfg.UnixSocketAuth = "local"
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = filepath.Join(t.TempDir(), "grpc-attachment-idempotency.db")
	cfg.DatastoreMigrateAtStart = true
	cfg.EncryptionDBDisabled = true
	cfg.AllowPrivateSourceURLs = true
	cfg.TempDir = t.TempDir()
	cfg.AttachmentMaxSize = 1024
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	real, err := loader(ctx)
	require.NoError(t, err)
	resolver, err := security.NewTokenResolver(&cfg)
	require.NoError(t, err)

	env := &attIdemEnv{
		ctx:        ctx,
		real:       real,
		store:      &attIdemStore{MemoryStore: real},
		blobs:      &attIdemBlobs{},
		cfg:        &cfg,
		streamIntc: security.GRPCStreamInterceptorWithRateLimiter(resolver, nil),
		unaryIntc:  security.GRPCUnaryInterceptorWithRateLimiter(resolver, nil),
		sourceCode: http.StatusOK,
	}
	env.server = &AttachmentsServer{Store: env.store, AttachStore: env.blobs, Config: &cfg, MaxBodySize: 1024}
	env.sourceStart = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		env.sourceHits.Add(1)
		if env.sourceHook != nil {
			env.sourceHook()
		}
		if env.sourceGate != nil {
			<-env.sourceGate
		}
		w.WriteHeader(env.sourceCode)
		_, _ = w.Write([]byte("content"))
	}))
	t.Cleanup(func() {
		env.releaseSource()
		env.sourceStart.Close()
	})
	return env
}

func (e *attIdemEnv) gateSource() { e.sourceGate = make(chan struct{}) }

func (e *attIdemEnv) releaseSource() {
	if e.sourceGate != nil {
		select {
		case <-e.sourceGate:
		default:
			close(e.sourceGate)
		}
	}
}

func (e *attIdemEnv) write(t *testing.T, fn func(context.Context) error) {
	t.Helper()
	require.NoError(t, e.real.InWriteTx(e.ctx, fn))
}

func (e *attIdemEnv) row(t *testing.T, id uuid.UUID) *model.Attachment {
	t.Helper()
	var out *model.Attachment
	err := e.real.InReadTx(e.ctx, func(ctx context.Context) error {
		a, err := e.real.AdminGetAttachment(ctx, id)
		if err != nil {
			return err
		}
		out = &a.Attachment
		return nil
	})
	if err != nil {
		var nf *registrystore.NotFoundError
		require.ErrorAs(t, err, &nf)
		return nil
	}
	return out
}

// statusOf returns "" when the row does not exist, so polling closures never
// dereference a missing row.
func (e *attIdemEnv) statusOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	if row := e.row(t, id); row != nil {
		return row.Status
	}
	return ""
}

func (e *attIdemEnv) createRow(t *testing.T, user string, id uuid.UUID, status string) *model.Attachment {
	t.Helper()
	expires := time.Now().Add(time.Hour).UTC()
	var res *registrystore.CreateAttachmentResult
	e.write(t, func(ctx context.Context) error {
		var err error
		res, err = e.real.CreateAttachmentWithID(ctx, user, id, model.Attachment{ContentType: "text/plain", Status: status, ExpiresAt: &expires})
		return err
	})
	require.True(t, res.Created)
	return res.Attachment
}

// finalizeAndLink finalizes a pending row under its own attempt and links it to
// an entry, returning the blob key it was finalized with.
func (e *attIdemEnv) finalizeAndLink(t *testing.T, user string, a *model.Attachment) string {
	t.Helper()
	key := "replacement-" + uuid.NewString()
	e.write(t, func(ctx context.Context) error {
		if _, err := e.real.FinalizeAttachment(ctx, user, a.ID, *a.AttemptID, registrystore.AttachmentFinalize{
			StorageKey: key, Size: 5, SHA256: "replacement-sha",
		}); err != nil {
			return err
		}
		_, err := e.real.LinkAttachmentToEntry(ctx, user, a.ID, uuid.New())
		return err
	})
	return key
}

func (e *attIdemEnv) deleteRow(t *testing.T, user string, id uuid.UUID) {
	t.Helper()
	e.write(t, func(ctx context.Context) error { return e.real.DeleteAttachment(ctx, user, "", id) })
}

func (e *attIdemEnv) queuedKeys(t *testing.T, seen map[string]model.Task) {
	t.Helper()
	e.write(t, func(ctx context.Context) error {
		tasks, err := e.real.ClaimReadyTasks(ctx, 1000)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.TaskType == registrystore.AttachmentBlobDeleteTaskType {
				seen[task.TaskBody[registrystore.AttachmentBlobDeleteStorageKeyField].(string)] = task
			}
		}
		return nil
	})
}

func (e *attIdemEnv) requireBlobSurvives(t *testing.T, task model.Task, key string) {
	t.Helper()
	var referenced bool
	e.write(t, func(ctx context.Context) error {
		var err error
		referenced, err = e.real.PrepareAttachmentBlobDelete(ctx, task.ID, key)
		return err
	})
	require.True(t, referenced, "a ready attachment still references the blob, so the delete worker must keep it")
}

func attIdemMetadata(id *string) *pb.UploadAttachmentRequest {
	return &pb.UploadAttachmentRequest{Payload: &pb.UploadAttachmentRequest_Metadata{Metadata: &pb.UploadMetadata{
		Filename: "doc.txt", ContentType: "text/plain", ExpiresIn: "PT2H", Id: id,
	}}}
}

func attIdemChunk(data string) *pb.UploadAttachmentRequest {
	return &pb.UploadAttachmentRequest{Payload: &pb.UploadAttachmentRequest_Chunk{Chunk: []byte(data)}}
}

func attIdemStr(s string) *string { return &s }

// upload runs UploadAttachment through the auth interceptor. The caller can
// cancel ctx to simulate a client disconnect.
func (e *attIdemEnv) upload(ctx context.Context, stream *attIdemStream) error {
	stream.ctx = ctx
	return e.streamIntc(e.server, stream, &gogrpc.StreamServerInfo{IsClientStream: true}, func(_ any, authenticated gogrpc.ServerStream) error {
		stream.ctx = authenticated.Context()
		return e.server.UploadAttachment(stream)
	})
}

func (e *attIdemEnv) uploadWithID(t *testing.T, id *string, content string) (*attIdemStream, error) {
	t.Helper()
	stream := &attIdemStream{requests: []*pb.UploadAttachmentRequest{attIdemMetadata(id), attIdemChunk(content)}}
	err := e.upload(e.reqCtx(), stream)
	return stream, err
}

func (e *attIdemEnv) createFromURL(t *testing.T, id *string) (*pb.UploadAttachmentResponse, error) {
	t.Helper()
	req := &pb.CreateAttachmentFromUrlRequest{SourceUrl: e.sourceStart.URL, ContentType: "text/plain", Id: id}
	resp, err := e.unaryIntc(e.reqCtx(), req, &gogrpc.UnaryServerInfo{}, func(ctx context.Context, raw any) (any, error) {
		return e.server.CreateAttachmentFromUrl(ctx, raw.(*pb.CreateAttachmentFromUrlRequest))
	})
	if err != nil {
		return nil, err
	}
	return resp.(*pb.UploadAttachmentResponse), nil
}

// --- UploadAttachment with a caller ID ---

func TestGRPCUploadWithIDCreatesUploadingRowBeforeStoringBlob(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	start := time.Now()
	var during *model.Attachment
	env.blobs.onStore = func(int) { during = env.row(t, id) }

	stream, err := env.uploadWithID(t, attIdemStr(id.String()), "hello")

	require.NoError(t, err)
	require.NotNil(t, during, "the row must exist while the blob is being stored")
	require.Equal(t, registrystore.AttachmentStatusUploading, during.Status)
	require.NotNil(t, during.AttemptID)
	require.Nil(t, during.StorageKey)
	require.NotNil(t, during.ExpiresAt, "expiry is set before the blob is stored")
	require.WithinDuration(t, start.Add(2*time.Hour), *during.ExpiresAt, 10*time.Second)

	require.Equal(t, id.String(), stream.response.Id)
	require.Equal(t, "ready", stream.response.Status)
	row := env.row(t, id)
	require.Equal(t, registrystore.AttachmentStatusReady, row.Status)
	require.Equal(t, env.blobs.storedKeys(), []string{*row.StorageKey})
	require.Equal(t, int64(5), *row.Size)
	require.Equal(t, *during.AttemptID, *row.AttemptID)
	require.False(t, row.ExpiresAt.Before(*during.ExpiresAt), "finalize refreshes the expiry")
	require.Equal(t, 1, env.blobs.storeCalls())
	require.Zero(t, env.blobs.deleteCalls())
}

func TestGRPCUploadWithoutIDKeepsLegacyFlow(t *testing.T) {
	env := newAttIdemEnv(t)
	env.blobs.onStore = func(int) {
		var rows int
		env.write(t, func(ctx context.Context) error {
			list, _, err := env.real.AdminListAttachments(ctx, registrystore.AdminAttachmentQuery{Status: "all", Limit: 10})
			rows = len(list)
			return err
		})
		require.Zero(t, rows, "a request without an ID must not create a row before the blob is stored")
	}

	stream, err := env.uploadWithID(t, nil, "hello")

	require.NoError(t, err)
	require.Equal(t, "ready", stream.response.Status)
	require.Nil(t, env.row(t, uuid.MustParse(stream.response.Id)).AttemptID)
	require.Equal(t, int32(1), env.store.legacyCreate.Load())
	require.Zero(t, env.store.creates.Load(), "the no-ID flow must keep using CreateAttachment")
}

func TestGRPCUploadWithInvalidIDIsInvalidArgumentAndStoresNothing(t *testing.T) {
	for _, raw := range []string{"not-a-uuid", ""} {
		t.Run(fmt.Sprintf("id=%q", raw), func(t *testing.T) {
			env := newAttIdemEnv(t)
			stream, err := env.uploadWithID(t, attIdemStr(raw), "hello")
			require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
			require.Nil(t, stream.response)
			require.Zero(t, env.blobs.storeCalls())
			require.Empty(t, env.blobs.storedKeys())
		})
	}
}

func TestGRPCUploadWithExistingSameOwnerIDIsAbortedAndStoresNothing(t *testing.T) {
	for _, st := range []string{"uploading", "downloading", "ready", "failed"} {
		t.Run(st, func(t *testing.T) {
			env := newAttIdemEnv(t)
			id := uuid.New()
			initial := st
			if st == "ready" || st == "failed" {
				initial = "uploading"
			}
			created := env.createRow(t, attIdemAlice, id, initial)
			switch st {
			case "ready":
				env.write(t, func(ctx context.Context) error {
					_, err := env.real.FinalizeAttachment(ctx, attIdemAlice, id, *created.AttemptID, registrystore.AttachmentFinalize{StorageKey: "existing-key", Size: 1, SHA256: "x"})
					return err
				})
			case "failed":
				env.write(t, func(ctx context.Context) error {
					_, err := env.real.MarkAttachmentFailed(ctx, attIdemAlice, id, *created.AttemptID)
					return err
				})
			}
			before := env.row(t, id)

			stream, err := env.uploadWithID(t, attIdemStr(id.String()), "hello")

			require.Equal(t, codes.Aborted, status.Code(err), "%v", err)
			require.Nil(t, stream.response)
			require.Zero(t, env.blobs.storeCalls(), "a duplicate ID must not store a blob")
			after := env.row(t, id)
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, *before.AttemptID, *after.AttemptID)
			require.Equal(t, before.StorageKey, after.StorageKey)
		})
	}
}

func TestGRPCUploadWithAnotherUsersIDIsNotFoundAndStoresNothing(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	created := env.createRow(t, attIdemBob, id, "uploading")

	stream, err := env.uploadWithID(t, attIdemStr(id.String()), "hello")

	require.Equal(t, codes.NotFound, status.Code(err), "%v", err)
	require.Nil(t, stream.response)
	require.Zero(t, env.blobs.storeCalls())
	after := env.row(t, id)
	require.Equal(t, attIdemBob, after.UserID)
	require.Equal(t, "uploading", after.Status)
	require.Equal(t, *created.AttemptID, *after.AttemptID)
}

func TestGRPCSecondUploadWithSameIDWhileFirstIsStoringStoresNothing(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	var second *attIdemStream
	var secondErr error
	env.blobs.onStore = func(call int) {
		if call != 1 {
			return
		}
		second, secondErr = env.uploadWithID(t, attIdemStr(id.String()), "hello")
	}

	first, err := env.uploadWithID(t, attIdemStr(id.String()), "hello")

	require.NoError(t, err)
	require.NotNil(t, second, "the first stream must reach the blob store")
	require.Equal(t, codes.Aborted, status.Code(secondErr), "%v", secondErr)
	require.Nil(t, second.response)
	require.Equal(t, "ready", first.response.Status)
	require.Equal(t, 1, env.blobs.storeCalls(), "only the stream that created the row stores a blob")
}

func TestGRPCConcurrentUploadsWithOneIDProduceOneAttachment(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	const n = 5
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = env.uploadWithID(t, attIdemStr(id.String()), "hello")
		}()
	}
	wg.Wait()

	ok, aborted := 0, 0
	for _, err := range errs {
		switch status.Code(err) {
		case codes.OK:
			ok++
		case codes.Aborted:
			aborted++
		}
	}
	require.Equal(t, 1, ok, "exactly one stream creates the attachment: %v", errs)
	require.Equal(t, n-1, aborted, "every other stream is aborted: %v", errs)
	require.Equal(t, 1, env.blobs.storeCalls())
	require.Equal(t, registrystore.AttachmentStatusReady, env.row(t, id).Status)
}

func TestGRPCUploadCanceledAfterMetadataLeavesRowFailed(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &attIdemStream{
		requests:  []*pb.UploadAttachmentRequest{attIdemMetadata(attIdemStr(id.String()))},
		beforeEnd: cancel,
		endErr:    context.Canceled,
	}

	err := env.upload(ctx, stream)

	require.Error(t, err)
	row := env.row(t, id)
	require.NotNil(t, row, "the canceled stream keeps its row so the client can look the outcome up")
	require.Equal(t, registrystore.AttachmentStatusFailed, row.Status, "marking failed must survive the client disconnect")
	require.Nil(t, row.StorageKey)
	require.Empty(t, env.blobs.storedKeys())
	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Empty(t, queued, "no blob was stored, so nothing is queued")
}

// The blob store fails after the client finished sending. The row must not stay
// "uploading", and the ID stays blocked until the owner deletes the row.
func TestGRPCUploadBlobStoreFailureAtEOFLeavesRowFailed(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	env.blobs.failWith = errors.New("disk full")
	stream := &attIdemStream{
		requests: []*pb.UploadAttachmentRequest{attIdemMetadata(attIdemStr(id.String())), attIdemChunk("hello")},
	}

	err := env.upload(env.reqCtx(), stream)

	require.Error(t, err)
	row := env.row(t, id)
	require.NotNil(t, row, "the failed attempt keeps its row so the client can look the outcome up")
	require.Equal(t, registrystore.AttachmentStatusFailed, row.Status)
	require.Nil(t, row.StorageKey)
	require.Empty(t, env.blobs.storedKeys())

	again, err := env.uploadWithID(t, attIdemStr(id.String()), "hello")
	require.Equal(t, codes.Aborted, status.Code(err), "a failed row keeps blocking its ID until the owner deletes it")
	require.NotNil(t, again)
}

// The client goes away after the blob was stored but before the finalize. The
// failure record and the blob cleanup must both survive the cancellation.
func TestGRPCUploadCanceledAfterBlobStoredMarksRowFailedAndQueuesBlob(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	ctx, cancel := context.WithCancel(env.reqCtx())
	defer cancel()
	env.blobs.onStore = func(int) { cancel() }
	stream := &attIdemStream{
		requests: []*pb.UploadAttachmentRequest{attIdemMetadata(attIdemStr(id.String())), attIdemChunk("hello")},
	}

	err := env.upload(ctx, stream)

	require.Error(t, err)
	row := env.row(t, id)
	require.NotNil(t, row)
	require.Equal(t, registrystore.AttachmentStatusFailed, row.Status, "marking failed must survive the client disconnect")
	require.Nil(t, row.StorageKey)
	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, env.blobs.storedKeys()[0], "the stored blob is queued even though the stream was canceled")
}

func TestGRPCUploadStreamErrorAfterChunksLeavesRowFailed(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	stream := &attIdemStream{
		requests: []*pb.UploadAttachmentRequest{attIdemMetadata(attIdemStr(id.String())), attIdemChunk("hel")},
		endErr:   status.Error(codes.Unavailable, "transport closed"),
	}

	err := env.upload(env.reqCtx(), stream)

	require.Equal(t, codes.Unavailable, status.Code(err))
	row := env.row(t, id)
	require.NotNil(t, row, "the failed stream keeps its row")
	require.Equal(t, registrystore.AttachmentStatusFailed, row.Status)
	require.Empty(t, env.blobs.storedKeys())
}

func TestGRPCUploadFinalizeRejectedAfterRowDeletedAndReplacedQueuesOwnBlob(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	var replacement *model.Attachment
	env.blobs.onStore = func(int) {
		if env.row(t, id) == nil {
			t.Error("the row must exist while the blob is being stored")
			return
		}
		env.deleteRow(t, attIdemAlice, id)
		replacement = env.createRow(t, attIdemAlice, id, "uploading")
	}

	stream, err := env.uploadWithID(t, attIdemStr(id.String()), "hello")

	require.Equal(t, codes.Aborted, status.Code(err), "a stale attempt is a conflict, not an inconclusive NOT_FOUND: %v", err)
	require.Nil(t, stream.response)
	require.NotNil(t, replacement)
	after := env.row(t, id)
	require.Equal(t, "uploading", after.Status, "the replacement must not be finalized")
	require.Equal(t, *replacement.AttemptID, *after.AttemptID)
	require.Nil(t, after.StorageKey)

	keys := env.blobs.storedKeys()
	require.Len(t, keys, 1)
	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, keys[0], "the late stream queues its own blob")
	require.Len(t, queued, 1)
	require.Zero(t, env.blobs.deleteCalls(), "the provider is not called from the request")
}

func TestGRPCUploadLostFinalizeAcknowledgmentKeepsRowReadyAndBlobAlive(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	env.store.loseAck = true

	stream, err := env.uploadWithID(t, attIdemStr(id.String()), "hello")

	require.Error(t, err, "the client sees an error because the acknowledgment was lost")
	require.Nil(t, stream.response)
	row := env.row(t, id)
	require.Equal(t, registrystore.AttachmentStatusReady, row.Status, "the committed finalize must not be turned into failed")
	keys := env.blobs.storedKeys()
	require.Equal(t, keys, []string{*row.StorageKey})
	require.Equal(t, int32(1), env.store.marks.Load(), "the failure path ran and matched nothing")

	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, keys[0])
	env.requireBlobSurvives(t, queued[keys[0]], keys[0])
	require.Zero(t, env.blobs.deleteCalls())
}

func TestGRPCUploadWithIDIsReplaySafe(t *testing.T) {
	env := newAttIdemEnv(t)
	env.store.replay = true
	id := uuid.New()

	stream, err := env.uploadWithID(t, attIdemStr(id.String()), "hello")

	require.NoError(t, err)
	require.Equal(t, 1, env.blobs.storeCalls(), "the blob is stored once, after the create transaction commits")
	row := env.row(t, id)
	require.NotNil(t, row, "the committed attempt created the row")
	require.Equal(t, registrystore.AttachmentStatusReady, row.Status)
	require.Equal(t, "ready", stream.response.Status)
}

// --- CreateAttachmentFromUrl with a caller ID ---

func TestGRPCCreateFromURLWithIDReturnsDownloadingAndDuplicateStartsNoDownload(t *testing.T) {
	env := newAttIdemEnv(t)
	env.gateSource()
	id := uuid.New()
	start := time.Now()

	resp, err := env.createFromURL(t, attIdemStr(id.String()))

	require.NoError(t, err)
	require.Equal(t, id.String(), resp.Id)
	require.Equal(t, "downloading", resp.Status, "the call does not wait for the download")
	row := env.row(t, id)
	require.Equal(t, "downloading", row.Status)
	require.NotNil(t, row.AttemptID)
	require.NotNil(t, row.ExpiresAt, "expiry is set when the row is created")
	require.WithinDuration(t, start.Add(env.cfg.AttachmentDefaultExpiresIn), *row.ExpiresAt, 10*time.Second)

	_, err = env.createFromURL(t, attIdemStr(id.String()))
	require.Equal(t, codes.Aborted, status.Code(err), "%v", err)

	require.Eventually(t, func() bool { return env.sourceHits.Load() >= 1 }, 3*time.Second, 5*time.Millisecond)
	require.Never(t, func() bool { return env.sourceHits.Load() > 1 }, 200*time.Millisecond, 10*time.Millisecond, "a duplicate must not start a second download")
	env.releaseSource()
	require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), env.sourceHits.Load())
	require.Equal(t, 1, env.blobs.storeCalls())
	final := env.row(t, id)
	require.Equal(t, *row.AttemptID, *final.AttemptID)
	require.Equal(t, env.blobs.storedKeys(), []string{*final.StorageKey})
}

func TestGRPCCreateFromURLWithAnotherUsersIDOrInvalidIDStartsNoDownload(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	env.createRow(t, attIdemBob, id, "downloading")

	_, err := env.createFromURL(t, attIdemStr(id.String()))
	require.Equal(t, codes.NotFound, status.Code(err), "%v", err)
	require.Equal(t, attIdemBob, env.row(t, id).UserID)

	for _, raw := range []string{"not-a-uuid", ""} {
		_, err := env.createFromURL(t, attIdemStr(raw))
		require.Equal(t, codes.InvalidArgument, status.Code(err), "id=%q: %v", raw, err)
	}

	require.Never(t, func() bool { return env.sourceHits.Load() > 0 }, 200*time.Millisecond, 10*time.Millisecond)
	require.Zero(t, env.blobs.storeCalls())
}

// A URL download without a caller ID is still fenced: the server mints the ID
// and the row gets an attempt ID like any other download.
func TestGRPCCreateFromURLWithoutIDIsFenced(t *testing.T) {
	env := newAttIdemEnv(t)

	resp, err := env.createFromURL(t, nil)

	require.NoError(t, err)
	id := uuid.MustParse(resp.Id)
	require.NotEqual(t, uuid.Nil, id)
	require.Equal(t, "downloading", resp.Status)
	require.Zero(t, env.store.legacyCreate.Load(), "no URL download may use the unfenced CreateAttachment")
	require.Equal(t, int32(1), env.store.creates.Load())
	require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)
	require.NotNil(t, env.row(t, id).AttemptID)
}

// runStaleNoIDDownload starts a URL download without a caller ID. While the
// download is in flight it deletes the row, reuses the ID for a replacement,
// finalizes the replacement and links it to an entry, which is the state the
// stale download must not disturb. It returns the original row, the replacement
// and the replacement's blob key once the replacement is in place.
func (e *attIdemEnv) runStaleNoIDDownload(t *testing.T) (original, replacement *model.Attachment, replacementKey string) {
	t.Helper()
	var id uuid.UUID
	idReady := make(chan struct{})
	replaced := make(chan struct{})
	e.sourceHook = func() {
		// Runs on the source server goroutine. Wait for the test to learn the ID.
		<-idReady
		e.deleteRow(t, attIdemAlice, id)
		replacement = e.createRow(t, attIdemAlice, id, "uploading")
		replacementKey = e.finalizeAndLink(t, attIdemAlice, replacement)
		close(replaced)
	}
	resp, err := e.createFromURL(t, nil)
	require.NoError(t, err)
	id = uuid.MustParse(resp.Id)
	original = e.row(t, id)
	require.NotNil(t, original.AttemptID, "a no-ID download must still carry an attempt ID")
	close(idReady)
	select {
	case <-replaced:
	case <-time.After(5 * time.Second):
		t.Fatal("the download never reached the source")
	}
	return original, replacement, replacementKey
}

// requireReplacementUntouched asserts that the finalized, linked replacement
// still has exactly the state it was given.
func requireReplacementUntouched(t *testing.T, after, original, replacement *model.Attachment, replacementKey string) {
	t.Helper()
	require.Equal(t, registrystore.AttachmentStatusReady, after.Status)
	require.Equal(t, replacementKey, *after.StorageKey, "the replacement keeps its own blob")
	require.Equal(t, int64(5), *after.Size)
	require.Equal(t, "replacement-sha", *after.SHA256)
	require.Equal(t, *replacement.AttemptID, *after.AttemptID)
	require.NotEqual(t, *original.AttemptID, *after.AttemptID)
	require.NotNil(t, after.EntryID, "the replacement stays linked to its entry")
}

// The reported race: a download started without an ID is deleted, its ID is
// reused by a caller, the replacement is finalized and linked, and only then
// does the original download finish. It must not touch the replacement.
func TestGRPCStaleNoIDURLDownloadCompletionCannotModifyReplacement(t *testing.T) {
	env := newAttIdemEnv(t)
	original, replacement, replacementKey := env.runStaleNoIDDownload(t)

	queued := map[string]model.Task{}
	// Wait for the stale download to settle: fenced code queues its own blob;
	// unfenced code overwrites the replacement's key instead.
	require.Eventually(t, func() bool {
		env.queuedKeys(t, queued)
		return len(queued) > 0 || *env.row(t, replacement.ID).StorageKey != replacementKey
	}, 5*time.Second, 10*time.Millisecond, "the stale download settled")
	after := env.row(t, replacement.ID)
	requireReplacementUntouched(t, after, original, replacement, replacementKey)
	require.Len(t, env.blobs.storedKeys(), 1)
	require.Contains(t, queued, env.blobs.storedKeys()[0], "the stale download queues its own blob")
	require.NotContains(t, queued, replacementKey, "the replacement's blob is never queued")
	require.Zero(t, env.blobs.deleteCalls())
}

func TestGRPCStaleNoIDURLDownloadFailureCannotFailReplacement(t *testing.T) {
	env := newAttIdemEnv(t)
	env.sourceCode = http.StatusInternalServerError
	original, replacement, replacementKey := env.runStaleNoIDDownload(t)

	// Wait for the stale failure to settle: fenced code calls MarkAttachmentFailed;
	// unfenced code fails the replacement row instead.
	require.Eventually(t, func() bool {
		return env.store.marks.Load() >= 1 || env.row(t, replacement.ID).Status != registrystore.AttachmentStatusReady
	}, 5*time.Second, 5*time.Millisecond, "the stale failure settled")
	require.Never(t, func() bool {
		return env.row(t, replacement.ID).Status != registrystore.AttachmentStatusReady
	}, 200*time.Millisecond, 10*time.Millisecond, "a stale failure must not fail the replacement")
	requireReplacementUntouched(t, env.row(t, replacement.ID), original, replacement, replacementKey)
}

func TestGRPCURLDownloadFailureMarksRowFailed(t *testing.T) {
	env := newAttIdemEnv(t)
	env.sourceCode = http.StatusInternalServerError
	id := uuid.New()

	_, err := env.createFromURL(t, attIdemStr(id.String()))

	require.NoError(t, err)
	require.Eventually(t, func() bool { return env.statusOf(t, id) == "failed" }, 5*time.Second, 10*time.Millisecond)
	require.Zero(t, env.blobs.storeCalls())
	require.Equal(t, int32(1), env.sourceHits.Load())
}

func TestGRPCStaleURLDownloadCannotModifyReplacementAndQueuesItsOwnBlob(t *testing.T) {
	env := newAttIdemEnv(t)
	id := uuid.New()
	var replacement *model.Attachment
	env.sourceHook = func() {
		if env.row(t, id) == nil {
			t.Error("the row must exist while the download runs")
			return
		}
		env.deleteRow(t, attIdemAlice, id)
		replacement = env.createRow(t, attIdemAlice, id, "downloading")
	}

	_, err := env.createFromURL(t, attIdemStr(id.String()))
	require.NoError(t, err)

	queued := map[string]model.Task{}
	require.Eventually(t, func() bool {
		env.queuedKeys(t, queued)
		return len(queued) > 0
	}, 5*time.Second, 10*time.Millisecond, "the stale download queues its own blob")
	require.NotNil(t, replacement)
	require.Contains(t, queued, env.blobs.storedKeys()[0])
	after := env.row(t, id)
	require.Equal(t, "downloading", after.Status, "the replacement must not be finalized")
	require.Equal(t, *replacement.AttemptID, *after.AttemptID)
	require.Nil(t, after.StorageKey)
	require.Zero(t, env.blobs.deleteCalls())
}

func TestGRPCStaleURLDownloadFailureCannotFailReplacement(t *testing.T) {
	env := newAttIdemEnv(t)
	env.sourceCode = http.StatusInternalServerError
	id := uuid.New()
	var replacement *model.Attachment
	env.sourceHook = func() {
		if env.row(t, id) == nil {
			t.Error("the row must exist while the download runs")
			return
		}
		env.deleteRow(t, attIdemAlice, id)
		replacement = env.createRow(t, attIdemAlice, id, "downloading")
	}

	_, err := env.createFromURL(t, attIdemStr(id.String()))
	require.NoError(t, err)

	require.Eventually(t, func() bool { return env.store.marks.Load() >= 1 }, 5*time.Second, 5*time.Millisecond, "the failure path ran")
	require.NotNil(t, replacement)
	after := env.row(t, id)
	require.Equal(t, "downloading", after.Status, "a stale failure must not fail the replacement")
	require.Equal(t, *replacement.AttemptID, *after.AttemptID)
}

func TestGRPCURLDownloadLostFinalizeAcknowledgmentKeepsRowReady(t *testing.T) {
	env := newAttIdemEnv(t)
	env.store.loseAck = true
	id := uuid.New()

	_, err := env.createFromURL(t, attIdemStr(id.String()))
	require.NoError(t, err)

	require.Eventually(t, func() bool { return env.store.marks.Load() >= 1 }, 5*time.Second, 5*time.Millisecond, "the failure path ran")
	row := env.row(t, id)
	require.Equal(t, registrystore.AttachmentStatusReady, row.Status, "the committed finalize must not be turned into failed")
	keys := env.blobs.storedKeys()
	require.Equal(t, keys, []string{*row.StorageKey})
	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, keys[0])
	env.requireBlobSurvives(t, queued[keys[0]], keys[0])
	require.Zero(t, env.blobs.deleteCalls())
}

func TestGRPCCreateFromURLWithIDIsReplaySafe(t *testing.T) {
	env := newAttIdemEnv(t)
	env.store.replay = true
	id := uuid.New()

	resp, err := env.createFromURL(t, attIdemStr(id.String()))

	require.NoError(t, err)
	require.Equal(t, id.String(), resp.Id)
	require.Eventually(t, func() bool {
		row := env.row(t, id)
		return row != nil && row.Status == "ready"
	}, 5*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return env.sourceHits.Load() > 1 }, 200*time.Millisecond, 10*time.Millisecond, "the download starts once, for the committed attempt")
	require.Equal(t, 1, env.blobs.storeCalls())
}

// --- operation event correlation ---

func TestGRPCAttachmentCreationRecordsCallerIDOnOperationEvent(t *testing.T) {
	upload := func(env *attIdemEnv, id string) error {
		_, err := env.uploadWithID(t, attIdemStr(id), "hello")
		return err
	}
	fromURL := func(env *attIdemEnv, id string) error {
		_, err := env.createFromURL(t, attIdemStr(id))
		return err
	}
	for name, send := range map[string]func(*attIdemEnv, string) error{"upload": upload, "fromUrl": fromURL} {
		t.Run(name, func(t *testing.T) {
			env := newAttIdemEnv(t)
			env.event = operationevent.New("grpc /memory.v1.AttachmentsService/Create")
			id := uuid.New()
			require.NoError(t, send(env, id.String()))
			require.Equal(t, id.String(), env.event.Snapshot().AttachmentID, "a created attachment is correlated by its ID")
			require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)

			// A duplicate is rejected, and the operation is still about that ID.
			env.event = operationevent.New("grpc /memory.v1.AttachmentsService/Create")
			require.Equal(t, codes.Aborted, status.Code(send(env, id.String())))
			require.Equal(t, id.String(), env.event.Snapshot().AttachmentID)

			// An ID that fails validation is never applied to the event.
			env.event = operationevent.New("grpc /memory.v1.AttachmentsService/Create")
			require.Equal(t, codes.InvalidArgument, status.Code(send(env, "not-a-uuid")))
			require.Empty(t, env.event.Snapshot().AttachmentID, "a rejected ID must not be recorded")
		})
	}
}
