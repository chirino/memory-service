//go:build !nosqlite && sqlite_fts5

package attachments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/operationevent"
	_ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// These tests run the real REST handlers against the real SQLite store and a
// probing blob store. They assert persisted rows and blob side effects, not
// only status codes.

const (
	idemAlice = "alice"
	idemBob   = "bob"
)

// idemBlobs is a blob store that records every Store and Delete call and lets a
// test act while a Store call is in flight.
type idemBlobs struct {
	mu       sync.Mutex
	stores   int
	deletes  int
	keys     []string
	onStore  func(call int)
	failWith error
}

func (b *idemBlobs) Store(_ context.Context, reader io.Reader, _ int64, _ string) (*registryattach.FileStoreResult, error) {
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

func (*idemBlobs) Retrieve(context.Context, string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *idemBlobs) Delete(context.Context, string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletes++
	return nil
}

func (*idemBlobs) GetSignedURL(context.Context, string, time.Duration, *registryattach.SignedURLOptions) (*url.URL, error) {
	return nil, fmt.Errorf("not implemented")
}

func (b *idemBlobs) storeCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stores
}

func (b *idemBlobs) deleteCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.deletes
}

func (b *idemBlobs) storedKeys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.keys...)
}

// idemWrapStore intercepts the conditional attachment writes to simulate a
// finalize whose commit succeeds but whose acknowledgment is lost, a finalize
// that fails outright, and a transaction callback that is replayed.
type idemWrapStore struct {
	registrystore.MemoryStore
	finalizeErr  error
	loseAck      bool
	replay       bool
	finalized    atomic.Bool
	marks        atomic.Int32
	creates      atomic.Int32
	legacyCreate atomic.Int32
}

func (s *idemWrapStore) FinalizeAttachment(ctx context.Context, userID string, id, attempt uuid.UUID, finalize registrystore.AttachmentFinalize) (*model.Attachment, error) {
	if s.finalizeErr != nil {
		return nil, s.finalizeErr
	}
	out, err := s.MemoryStore.FinalizeAttachment(ctx, userID, id, attempt, finalize)
	if err == nil {
		s.finalized.Store(true)
	}
	return out, err
}

func (s *idemWrapStore) MarkAttachmentFailed(ctx context.Context, userID string, id, attempt uuid.UUID) (bool, error) {
	s.marks.Add(1)
	return s.MemoryStore.MarkAttachmentFailed(ctx, userID, id, attempt)
}

func (s *idemWrapStore) CreateAttachmentWithID(ctx context.Context, userID string, id uuid.UUID, a model.Attachment) (*registrystore.CreateAttachmentResult, error) {
	s.creates.Add(1)
	return s.MemoryStore.CreateAttachmentWithID(ctx, userID, id, a)
}

func (s *idemWrapStore) CreateAttachment(ctx context.Context, userID, conversationID string, a model.Attachment) (*model.Attachment, error) {
	s.legacyCreate.Add(1)
	return s.MemoryStore.CreateAttachment(ctx, userID, conversationID, a)
}

var errIdemRolledBack = errors.New("attempt rolled back for replay")

func (s *idemWrapStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	if s.replay {
		// The first attempt runs and is rolled back, like a MongoDB WriteConflict
		// retry. Only the second attempt commits.
		err := s.MemoryStore.InWriteTx(ctx, func(txCtx context.Context) error {
			if err := fn(txCtx); err != nil {
				return err
			}
			return errIdemRolledBack
		})
		if err != nil && !errors.Is(err, errIdemRolledBack) {
			return err
		}
	}
	err := s.MemoryStore.InWriteTx(ctx, fn)
	if err == nil && s.loseAck && s.finalized.CompareAndSwap(true, false) {
		return errors.New("acknowledgment lost after commit")
	}
	return err
}

type idemEnv struct {
	ctx   context.Context
	real  registrystore.MemoryStore
	store registrystore.MemoryStore
	wrap  *idemWrapStore
	blobs *idemBlobs
	cfg   *config.Config
	// event, when set, is attached to every request context like the operation
	// middleware does.
	event *operationevent.Event
	// requestCtx, when set, replaces the request context so a test can cancel a
	// request like a client disconnect without cancelling the test's own reads.
	requestCtx context.Context
}

func newIdemEnv(t *testing.T) *idemEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = filepath.Join(t.TempDir(), "attachment-idempotency.db")
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
	wrap := &idemWrapStore{MemoryStore: real}
	return &idemEnv{ctx: ctx, real: real, store: wrap, wrap: wrap, blobs: &idemBlobs{}, cfg: &cfg}
}

func (e *idemEnv) write(t *testing.T, fn func(context.Context) error) {
	t.Helper()
	require.NoError(t, e.real.InWriteTx(e.ctx, fn))
}

// row returns nil when no row has the ID.
func (e *idemEnv) row(t *testing.T, id uuid.UUID) *model.Attachment {
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
func (e *idemEnv) statusOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	if row := e.row(t, id); row != nil {
		return row.Status
	}
	return ""
}

func (e *idemEnv) createRow(t *testing.T, user string, id uuid.UUID, status string) *model.Attachment {
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

func (e *idemEnv) deleteRow(t *testing.T, user string, id uuid.UUID) {
	t.Helper()
	e.write(t, func(ctx context.Context) error { return e.real.DeleteAttachment(ctx, user, "", id) })
}

// claimBlobTasks claims every ready blob-delete task. Claimed tasks are not
// returned again, so callers accumulate across polls.
func (e *idemEnv) claimBlobTasks(t *testing.T) []model.Task {
	t.Helper()
	var out []model.Task
	e.write(t, func(ctx context.Context) error {
		out = nil
		tasks, err := e.real.ClaimReadyTasks(ctx, 1000)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.TaskType == registrystore.AttachmentBlobDeleteTaskType {
				out = append(out, task)
			}
		}
		return nil
	})
	return out
}

// queuedKeys collects the storage keys of claimed blob-delete tasks into seen.
func (e *idemEnv) queuedKeys(t *testing.T, seen map[string]model.Task) {
	t.Helper()
	for _, task := range e.claimBlobTasks(t) {
		seen[task.TaskBody[registrystore.AttachmentBlobDeleteStorageKeyField].(string)] = task
	}
}

// requireBlobSurvives plays the blob-delete worker for a claimed task and
// requires that the live reference keeps the blob.
func (e *idemEnv) requireBlobSurvives(t *testing.T, task model.Task, key string) {
	t.Helper()
	var referenced bool
	e.write(t, func(ctx context.Context) error {
		var err error
		referenced, err = e.real.PrepareAttachmentBlobDelete(ctx, task.ID, key)
		return err
	})
	require.True(t, referenced, "a ready attachment still references the blob, so the delete worker must keep it")
}

func (e *idemEnv) newContext(method, target string, body io.Reader, contentType, user string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	reqCtx := e.ctx
	if e.requestCtx != nil {
		reqCtx = e.requestCtx
	}
	c.Request = httptest.NewRequest(method, target, body).WithContext(reqCtx)
	if e.event != nil {
		c.Request = c.Request.WithContext(operationevent.WithContext(c.Request.Context(), e.event))
	}
	if contentType != "" {
		c.Request.Header.Set("Content-Type", contentType)
	}
	c.Set("userID", user)
	return c, recorder
}

func idemPtr(s string) *string { return &s }

func (e *idemEnv) uploadMultipart(t *testing.T, user string, id *string, content, expiresIn string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "doc.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte(content))
	require.NoError(t, err)
	if id != nil {
		require.NoError(t, writer.WriteField("id", *id))
	}
	require.NoError(t, writer.Close())
	target := "/v1/attachments"
	if expiresIn != "" {
		target += "?expiresIn=" + expiresIn
	}
	c, recorder := e.newContext(http.MethodPost, target, &body, writer.FormDataContentType(), user)
	upload(c, e.store, e.blobs, e.cfg)
	return recorder
}

func (e *idemEnv) postURL(t *testing.T, user string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	c, recorder := e.newContext(http.MethodPost, "/v1/attachments", bytes.NewReader(raw), "application/json", user)
	upload(c, e.store, e.blobs, e.cfg)
	return recorder
}

func (e *idemEnv) metadata(t *testing.T, user, id string) *httptest.ResponseRecorder {
	t.Helper()
	c, recorder := e.newContext(http.MethodGet, "/v1/attachments/"+id+"/metadata", nil, "", user)
	c.Params = gin.Params{{Key: "id", Value: id}}
	getAttachmentMetadata(c, e.store)
	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &out), recorder.Body.String())
	return out
}

func requireNoInternalFields(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	body := recorder.Body.String()
	require.NotContains(t, body, "attemptId")
	require.NotContains(t, body, "attempt_id")
	require.NotContains(t, body, "storageKey")
}

// idemSource is a source-URL server that can hold its response.
type idemSource struct {
	srv    *httptest.Server
	hits   atomic.Int32
	gate   chan struct{}
	status int
	onHit  func()
}

func newIdemSource(t *testing.T, gated bool) *idemSource {
	t.Helper()
	s := &idemSource{status: http.StatusOK}
	if gated {
		s.gate = make(chan struct{})
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		if s.onHit != nil {
			s.onHit()
		}
		if s.gate != nil {
			<-s.gate
		}
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte("content"))
	}))
	t.Cleanup(func() {
		s.release()
		s.srv.Close()
	})
	return s
}

func (s *idemSource) release() {
	if s.gate != nil {
		select {
		case <-s.gate:
		default:
			close(s.gate)
		}
	}
}

// --- multipart upload with a caller ID ---

func TestUploadWithIDCreatesUploadingRowBeforeStoringBlob(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	start := time.Now()
	var during *model.Attachment
	var metaDuring *httptest.ResponseRecorder
	env.blobs.onStore = func(int) {
		during = env.row(t, id)
		require.NotNil(t, during, "the row must exist while the blob is being stored")
		metaDuring = env.metadata(t, idemAlice, id.String())
	}

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "PT2H")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, registrystore.AttachmentStatusUploading, during.Status)
	require.NotNil(t, during.AttemptID)
	require.Nil(t, during.StorageKey)
	require.NotNil(t, during.ExpiresAt, "expiry is set before the blob is stored")
	require.WithinDuration(t, start.Add(2*time.Hour), *during.ExpiresAt, 10*time.Second)
	require.Equal(t, http.StatusOK, metaDuring.Code)
	require.Equal(t, "uploading", decodeBody(t, metaDuring)["status"])
	requireNoInternalFields(t, metaDuring)

	body := decodeBody(t, rec)
	require.Equal(t, id.String(), body["id"])
	require.Equal(t, "ready", body["status"])
	requireNoInternalFields(t, rec)

	row := env.row(t, id)
	require.Equal(t, registrystore.AttachmentStatusReady, row.Status)
	require.Equal(t, env.blobs.storedKeys(), []string{*row.StorageKey})
	require.Equal(t, int64(5), *row.Size)
	require.NotEmpty(t, *row.SHA256)
	require.Equal(t, *during.AttemptID, *row.AttemptID, "finalize keeps the attempt that created the row")
	require.False(t, row.ExpiresAt.Before(*during.ExpiresAt), "finalize refreshes the expiry")
	require.Equal(t, 1, env.blobs.storeCalls())
	require.Zero(t, env.blobs.deleteCalls())
}

func TestUploadWithoutIDKeepsLegacyFlow(t *testing.T) {
	env := newIdemEnv(t)
	env.blobs.onStore = func(int) {
		var pending int
		env.write(t, func(ctx context.Context) error {
			rows, _, err := env.real.AdminListAttachments(ctx, registrystore.AdminAttachmentQuery{Status: "all", Limit: 10})
			pending = len(rows)
			return err
		})
		require.Zero(t, pending, "a request without an ID must not create a row before the blob is stored")
	}

	rec := env.uploadMultipart(t, idemAlice, nil, "hello", "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	body := decodeBody(t, rec)
	require.Equal(t, "ready", body["status"])
	id := uuid.MustParse(body["id"].(string))
	require.Nil(t, env.row(t, id).AttemptID)
	require.Equal(t, int32(1), env.wrap.legacyCreate.Load())
	require.Zero(t, env.wrap.creates.Load(), "the no-ID flow must keep using CreateAttachment")
	requireNoInternalFields(t, rec)
}

func TestUploadWithInvalidIDReturns400AndStoresNothing(t *testing.T) {
	for _, raw := range []string{"not-a-uuid", ""} {
		t.Run(fmt.Sprintf("id=%q", raw), func(t *testing.T) {
			env := newIdemEnv(t)
			rec := env.uploadMultipart(t, idemAlice, idemPtr(raw), "hello", "")
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Zero(t, env.blobs.storeCalls())
			require.Empty(t, env.blobs.storedKeys())
		})
	}
}

func TestUploadWithExistingSameOwnerIDReturns409AndStoresNothing(t *testing.T) {
	for _, status := range []string{"uploading", "downloading", "ready", "failed"} {
		t.Run(status, func(t *testing.T) {
			env := newIdemEnv(t)
			id := uuid.New()
			initial := status
			if status == "ready" || status == "failed" {
				initial = "uploading"
			}
			created := env.createRow(t, idemAlice, id, initial)
			if status == "ready" || status == "failed" {
				env.write(t, func(ctx context.Context) error {
					if status == "ready" {
						_, err := env.real.FinalizeAttachment(ctx, idemAlice, id, *created.AttemptID, registrystore.AttachmentFinalize{StorageKey: "existing-key", Size: 1, SHA256: "x"})
						return err
					}
					_, err := env.real.MarkAttachmentFailed(ctx, idemAlice, id, *created.AttemptID)
					return err
				})
			}
			before := env.row(t, id)

			rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			require.Zero(t, env.blobs.storeCalls(), "a duplicate ID must not store a blob")
			after := env.row(t, id)
			require.Equal(t, before.Status, after.Status)
			require.Equal(t, *before.AttemptID, *after.AttemptID)
			require.Equal(t, before.StorageKey, after.StorageKey)
		})
	}
}

func TestUploadWithAnotherUsersIDReturns404AndStoresNothing(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	created := env.createRow(t, idemBob, id, "uploading")

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Zero(t, env.blobs.storeCalls())
	after := env.row(t, id)
	require.Equal(t, idemBob, after.UserID)
	require.Equal(t, "uploading", after.Status)
	require.Equal(t, *created.AttemptID, *after.AttemptID)
}

func TestSecondRequestWithSameIDWhileFirstIsStoringStoresNothing(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	var second *httptest.ResponseRecorder
	env.blobs.onStore = func(call int) {
		if call != 1 {
			return
		}
		second = env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")
	}

	first := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.NotNil(t, second, "the first request must reach the blob store")
	require.Equal(t, http.StatusConflict, second.Code, second.Body.String())
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	require.Equal(t, 1, env.blobs.storeCalls(), "only the request that created the row stores a blob")
	require.Equal(t, registrystore.AttachmentStatusReady, env.row(t, id).Status)
}

func TestConcurrentUploadsWithOneIDProduceOneAttachment(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	const n = 5
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "").Code
		}()
	}
	wg.Wait()

	created, conflicts := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		}
	}
	require.Equal(t, 1, created, "exactly one request creates the attachment: %v", codes)
	require.Equal(t, n-1, conflicts, "every other request is a conflict: %v", codes)
	require.Equal(t, 1, env.blobs.storeCalls())
	require.Len(t, env.blobs.storedKeys(), 1)
	require.Equal(t, registrystore.AttachmentStatusReady, env.row(t, id).Status)
}

func TestUploadBlobStoreFailureLeavesRowFailed(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	env.blobs.failWith = errors.New("disk full")

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.GreaterOrEqual(t, rec.Code, 400)
	row := env.row(t, id)
	require.NotNil(t, row, "the failed attempt keeps its row so the client can look the outcome up")
	require.Equal(t, registrystore.AttachmentStatusFailed, row.Status)
	require.Nil(t, row.StorageKey)
	require.Empty(t, env.blobs.storedKeys())

	again := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")
	require.Equal(t, http.StatusConflict, again.Code, "a failed row keeps blocking its ID until the owner deletes it")
}

// A client disconnect cancels the request context. The failure bookkeeping is
// exactly what a disconnect needs, so it must still reach the store.
func TestUploadCanceledWhileStoringBlobStillMarksRowFailed(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	reqCtx, cancel := context.WithCancel(env.ctx)
	defer cancel()
	env.requestCtx = reqCtx
	env.blobs.failWith = context.Canceled
	env.blobs.onStore = func(int) { cancel() }

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.GreaterOrEqual(t, rec.Code, 400)
	row := env.row(t, id)
	require.NotNil(t, row)
	require.Equal(t, registrystore.AttachmentStatusFailed, row.Status, "the row must not stay uploading after the client went away")
	require.Equal(t, int32(1), env.wrap.marks.Load())
}

func TestUploadCanceledBeforeFinalizeMarksRowFailedAndQueuesBlob(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	reqCtx, cancel := context.WithCancel(env.ctx)
	defer cancel()
	env.requestCtx = reqCtx
	env.blobs.onStore = func(int) { cancel() }

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.GreaterOrEqual(t, rec.Code, 400, rec.Body.String())
	row := env.row(t, id)
	require.NotNil(t, row)
	require.Equal(t, registrystore.AttachmentStatusFailed, row.Status)
	require.Nil(t, row.StorageKey)
	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, env.blobs.storedKeys()[0], "the stored blob is queued even though the request was canceled")
}

func TestUploadFinalizeRejectedAfterRowDeletedAndReplacedQueuesOwnBlob(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	var replacement *model.Attachment
	env.blobs.onStore = func(int) {
		require.NotNil(t, env.row(t, id), "the row must exist while the blob is being stored")
		env.deleteRow(t, idemAlice, id)
		replacement = env.createRow(t, idemAlice, id, "uploading")
	}

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.Equal(t, http.StatusConflict, rec.Code, "a stale attempt is rejected as a conflict, not as an inconclusive 404: %s", rec.Body.String())
	after := env.row(t, id)
	require.Equal(t, "uploading", after.Status, "the replacement must not be finalized")
	require.Equal(t, *replacement.AttemptID, *after.AttemptID)
	require.Nil(t, after.StorageKey)
	require.Nil(t, after.Size)

	keys := env.blobs.storedKeys()
	require.Len(t, keys, 1)
	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, keys[0], "the late request queues its own blob")
	require.Len(t, queued, 1)
	require.Zero(t, env.blobs.deleteCalls(), "the provider is not called from the request")
}

func TestUploadFinalizeRejectedAfterRowDeletedQueuesOwnBlobAndCreatesNothing(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	env.blobs.onStore = func(int) {
		require.NotNil(t, env.row(t, id), "the row must exist while the blob is being stored")
		env.deleteRow(t, idemAlice, id)
	}

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Nil(t, env.row(t, id), "a late upload must not resurrect a deleted row")
	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, env.blobs.storedKeys()[0])
}

func TestUploadFinalizeFailureMarksRowFailedAndQueuesBlob(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	env.wrap.finalizeErr = errors.New("database unavailable")

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	row := env.row(t, id)
	require.Equal(t, registrystore.AttachmentStatusFailed, row.Status)
	require.Nil(t, row.StorageKey)
	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, env.blobs.storedKeys()[0])
}

func TestUploadLostFinalizeAcknowledgmentKeepsRowReadyAndBlobAlive(t *testing.T) {
	env := newIdemEnv(t)
	id := uuid.New()
	env.wrap.loseAck = true

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.GreaterOrEqual(t, rec.Code, 500, "the client sees an error because the acknowledgment was lost")
	row := env.row(t, id)
	require.Equal(t, registrystore.AttachmentStatusReady, row.Status, "the committed finalize must not be turned into failed")
	keys := env.blobs.storedKeys()
	require.Equal(t, keys, []string{*row.StorageKey})
	require.Equal(t, int32(1), env.wrap.marks.Load(), "the failure path ran and matched nothing")

	queued := map[string]model.Task{}
	env.queuedKeys(t, queued)
	require.Contains(t, queued, keys[0])
	env.requireBlobSurvives(t, queued[keys[0]], keys[0])
	require.Zero(t, env.blobs.deleteCalls())

	meta := env.metadata(t, idemAlice, id.String())
	require.Equal(t, "ready", decodeBody(t, meta)["status"], "the client recovers success by looking the ID up")
}

func TestUploadWithIDIsReplaySafe(t *testing.T) {
	env := newIdemEnv(t)
	env.wrap.replay = true
	id := uuid.New()

	rec := env.uploadMultipart(t, idemAlice, idemPtr(id.String()), "hello", "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, 1, env.blobs.storeCalls(), "the blob is stored once, after the create transaction commits")
	require.Len(t, env.blobs.storedKeys(), 1)
	row := env.row(t, id)
	require.NotNil(t, row, "the committed attempt created the row")
	require.Equal(t, registrystore.AttachmentStatusReady, row.Status)
	require.Equal(t, "ready", decodeBody(t, rec)["status"])
}

// --- metadata lookup against the real store ---

func TestMetadataLookupAuthorizationAndStatuses(t *testing.T) {
	env := newIdemEnv(t)
	for _, status := range []string{"uploading", "downloading", "failed"} {
		id := uuid.New()
		initial := status
		if status == "failed" {
			initial = "uploading"
		}
		env.createRow(t, idemAlice, id, initial)
		if status == "failed" {
			env.write(t, func(ctx context.Context) error {
				a := env.row(t, id)
				_, err := env.real.MarkAttachmentFailed(ctx, idemAlice, id, *a.AttemptID)
				return err
			})
		}
		rec := env.metadata(t, idemAlice, id.String())
		require.Equal(t, http.StatusOK, rec.Code, status)
		require.Equal(t, status, decodeBody(t, rec)["status"])
		requireNoInternalFields(t, rec)

		other := env.metadata(t, idemBob, id.String())
		require.Equal(t, http.StatusForbidden, other.Code, "an unlinked attachment is visible only to its owner")
		require.NotContains(t, other.Body.String(), status, "another user learns nothing about the status")
	}
	require.Equal(t, http.StatusNotFound, env.metadata(t, idemAlice, uuid.NewString()).Code)
	require.Equal(t, http.StatusBadRequest, env.metadata(t, idemAlice, "not-a-uuid").Code)
}

// --- URL creation with a caller ID ---

func TestURLCreateWithIDReturnsDownloadingImmediatelyAndDuplicateStartsNoDownload(t *testing.T) {
	env := newIdemEnv(t)
	source := newIdemSource(t, true)
	id := uuid.New()
	start := time.Now()

	rec := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id.String()})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	body := decodeBody(t, rec)
	require.Equal(t, id.String(), body["id"])
	require.Equal(t, "downloading", body["status"], "the response does not wait for the download")
	requireNoInternalFields(t, rec)
	row := env.row(t, id)
	require.Equal(t, "downloading", row.Status)
	require.NotNil(t, row.AttemptID)
	require.NotNil(t, row.ExpiresAt, "expiry is set when the row is created")
	require.WithinDuration(t, start.Add(env.cfg.AttachmentDefaultExpiresIn), *row.ExpiresAt, 10*time.Second)

	dup := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id.String()})
	require.Equal(t, http.StatusConflict, dup.Code, dup.Body.String())

	require.Eventually(t, func() bool { return source.hits.Load() >= 1 }, 3*time.Second, 5*time.Millisecond)
	require.Never(t, func() bool { return source.hits.Load() > 1 }, 200*time.Millisecond, 10*time.Millisecond, "a duplicate must not start a second download")
	source.release()
	require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), source.hits.Load())
	require.Equal(t, 1, env.blobs.storeCalls())
	final := env.row(t, id)
	require.Equal(t, *row.AttemptID, *final.AttemptID)
	require.Equal(t, env.blobs.storedKeys(), []string{*final.StorageKey})
}

func TestURLCreateWithAnotherUsersIDOrInvalidIDStartsNoDownload(t *testing.T) {
	env := newIdemEnv(t)
	source := newIdemSource(t, false)
	id := uuid.New()
	env.createRow(t, idemBob, id, "downloading")

	foreign := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id.String()})
	require.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
	require.Equal(t, idemBob, env.row(t, id).UserID)

	for _, raw := range []string{"not-a-uuid", ""} {
		bad := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": raw})
		require.Equal(t, http.StatusBadRequest, bad.Code, "id=%q: %s", raw, bad.Body.String())
	}

	require.Never(t, func() bool { return source.hits.Load() > 0 }, 200*time.Millisecond, 10*time.Millisecond)
	require.Zero(t, env.blobs.storeCalls())
}

// postURLRaw posts a hand-written JSON body, so a test can send shapes such as
// an explicit null that a Go map round trip would not distinguish from absence.
func (e *idemEnv) postURLRaw(user, body string) *httptest.ResponseRecorder {
	c, recorder := e.newContext(http.MethodPost, "/v1/attachments", strings.NewReader(body), "application/json", user)
	upload(c, e.store, e.blobs, e.cfg)
	return recorder
}

// The contract makes id optional but not nullable. Only an absent key selects
// the server-minted ID path, and every other non-UUID-string shape is a 400
// that happens before any row, download, or blob exists.
func TestURLCreateIDPresenceIsDistinguishedBeforeAnySideEffect(t *testing.T) {
	validID := uuid.NewString()
	rejected := []struct{ name, idJSON string }{
		{"explicit null", `null`},
		{"empty string", `""`},
		{"blank string", `"   "`},
		{"number", `123`},
		{"object", `{}`},
		{"array", `[]`},
		{"boolean", `true`},
		{"null with surrounding whitespace", "\n null\t"},
		{"not a uuid", `"not-a-uuid"`},
		{"nil uuid", `"00000000-0000-0000-0000-000000000000"`},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			env := newIdemEnv(t)
			source := newIdemSource(t, false)

			rec := env.postURLRaw(idemAlice, fmt.Sprintf(`{"sourceUrl": %q, "id": %s}`, source.srv.URL, tc.idJSON))

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, "invalid_request", decodeBody(t, rec)["code"])
			require.Zero(t, env.wrap.creates.Load(), "no attachment row may be created")
			require.Zero(t, env.wrap.legacyCreate.Load(), "no attachment row may be created")
			require.Zero(t, env.blobs.storeCalls(), "no blob operation may happen")
			require.Never(t, func() bool { return source.hits.Load() > 0 }, 200*time.Millisecond, 10*time.Millisecond, "no download may start")
		})
	}

	t.Run("omitted id takes the server-minted path", func(t *testing.T) {
		env := newIdemEnv(t)
		source := newIdemSource(t, false)

		rec := env.postURLRaw(idemAlice, fmt.Sprintf(`{"sourceUrl": %q}`, source.srv.URL))

		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		id, err := uuid.Parse(decodeBody(t, rec)["id"].(string))
		require.NoError(t, err)
		require.NotEqual(t, validID, id.String())
		require.Equal(t, int32(1), env.wrap.creates.Load())
		require.Zero(t, env.wrap.legacyCreate.Load())
		require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("valid uuid is used as the attachment ID", func(t *testing.T) {
		env := newIdemEnv(t)
		source := newIdemSource(t, false)

		rec := env.postURLRaw(idemAlice, fmt.Sprintf(`{"sourceUrl": %q, "id": %q}`, source.srv.URL, validID))

		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		require.Equal(t, validID, decodeBody(t, rec)["id"])
		require.Equal(t, int32(1), env.wrap.creates.Load())
		require.NotNil(t, env.row(t, uuid.MustParse(validID)))
		require.Eventually(t, func() bool { return env.statusOf(t, uuid.MustParse(validID)) == "ready" }, 5*time.Second, 10*time.Millisecond)
	})

	// The same environment serves a null and an omitted ID, so the only
	// difference between the two requests is the presence of the key.
	t.Run("null and omitted take different paths in one environment", func(t *testing.T) {
		env := newIdemEnv(t)
		source := newIdemSource(t, false)
		withNull := fmt.Sprintf(`{"sourceUrl": %q, "id": null}`, source.srv.URL)
		omitted := fmt.Sprintf(`{"sourceUrl": %q}`, source.srv.URL)

		for i := 0; i < 2; i++ {
			require.Equal(t, http.StatusBadRequest, env.postURLRaw(idemAlice, withNull).Code)
		}
		require.Zero(t, env.wrap.creates.Load(), "retrying a null id must never create an attachment")
		require.Never(t, func() bool { return source.hits.Load() > 0 }, 200*time.Millisecond, 10*time.Millisecond)

		rec := env.postURLRaw(idemAlice, omitted)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		id := uuid.MustParse(decodeBody(t, rec)["id"].(string))
		require.Equal(t, int32(1), env.wrap.creates.Load(), "the omitted ID is the only request that created a row")
		require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)
		require.Equal(t, int32(1), source.hits.Load(), "only the omitted-ID request downloaded")
	})
}

// A URL download without a caller ID is still fenced: the server mints the ID
// and the row gets an attempt ID like any other download.
func TestURLCreateWithoutIDIsFenced(t *testing.T) {
	env := newIdemEnv(t)
	source := newIdemSource(t, false)

	rec := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	body := decodeBody(t, rec)
	id := uuid.MustParse(body["id"].(string))
	require.Equal(t, "downloading", body["status"])
	requireNoInternalFields(t, rec)
	require.Zero(t, env.wrap.legacyCreate.Load(), "no URL download may use the unfenced CreateAttachment")
	require.Equal(t, int32(1), env.wrap.creates.Load())
	require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)
	require.NotNil(t, env.row(t, id).AttemptID)
}

// runStaleNoIDDownload starts a URL download without a caller ID. While the
// download is in flight it deletes the row, reuses the ID for a replacement,
// finalizes the replacement and links it to an entry, which is the state the
// stale download must not disturb.
func (e *idemEnv) runStaleNoIDDownload(t *testing.T, source *idemSource) (original, replacement *model.Attachment, replacementKey string) {
	t.Helper()
	var id uuid.UUID
	idReady := make(chan struct{})
	replaced := make(chan struct{})
	source.onHit = func() {
		// Runs on the source server goroutine. Wait for the test to learn the ID.
		<-idReady
		e.deleteRow(t, idemAlice, id)
		replacement = e.createRow(t, idemAlice, id, "uploading")
		replacementKey = "replacement-" + uuid.NewString()
		e.write(t, func(ctx context.Context) error {
			if _, err := e.real.FinalizeAttachment(ctx, idemAlice, id, *replacement.AttemptID, registrystore.AttachmentFinalize{
				StorageKey: replacementKey, Size: 5, SHA256: "replacement-sha",
			}); err != nil {
				return err
			}
			_, err := e.real.LinkAttachmentToEntry(ctx, idemAlice, id, uuid.New())
			return err
		})
		close(replaced)
	}
	rec := e.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	id = uuid.MustParse(decodeBody(t, rec)["id"].(string))
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
func TestStaleNoIDURLDownloadCompletionCannotModifyReplacement(t *testing.T) {
	env := newIdemEnv(t)
	source := newIdemSource(t, false)
	original, replacement, replacementKey := env.runStaleNoIDDownload(t, source)

	queued := map[string]model.Task{}
	// Wait for the stale download to settle: fenced code queues its own blob;
	// unfenced code overwrites the replacement's key instead.
	require.Eventually(t, func() bool {
		env.queuedKeys(t, queued)
		return len(queued) > 0 || *env.row(t, replacement.ID).StorageKey != replacementKey
	}, 5*time.Second, 10*time.Millisecond, "the stale download settled")
	requireReplacementUntouched(t, env.row(t, replacement.ID), original, replacement, replacementKey)
	require.Len(t, env.blobs.storedKeys(), 1)
	require.Contains(t, queued, env.blobs.storedKeys()[0], "the stale download queues its own blob")
	require.NotContains(t, queued, replacementKey, "the replacement's blob is never queued")
	require.Zero(t, env.blobs.deleteCalls())
}

func TestStaleNoIDURLDownloadFailureCannotFailReplacement(t *testing.T) {
	env := newIdemEnv(t)
	source := newIdemSource(t, false)
	source.status = http.StatusInternalServerError
	original, replacement, replacementKey := env.runStaleNoIDDownload(t, source)

	// Wait for the stale failure to settle: fenced code calls MarkAttachmentFailed;
	// unfenced code fails the replacement row instead.
	require.Eventually(t, func() bool {
		return env.wrap.marks.Load() >= 1 || env.row(t, replacement.ID).Status != registrystore.AttachmentStatusReady
	}, 5*time.Second, 5*time.Millisecond, "the stale failure settled")
	require.Never(t, func() bool {
		return env.row(t, replacement.ID).Status != registrystore.AttachmentStatusReady
	}, 200*time.Millisecond, 10*time.Millisecond, "a stale failure must not fail the replacement")
	requireReplacementUntouched(t, env.row(t, replacement.ID), original, replacement, replacementKey)
}

func TestURLDownloadFailureMarksRowFailed(t *testing.T) {
	env := newIdemEnv(t)
	source := newIdemSource(t, false)
	source.status = http.StatusInternalServerError
	id := uuid.New()

	rec := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id.String()})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Eventually(t, func() bool { return env.statusOf(t, id) == "failed" }, 5*time.Second, 10*time.Millisecond)
	require.Zero(t, env.blobs.storeCalls())
	require.Equal(t, int32(1), source.hits.Load())
}

func TestStaleURLDownloadCannotModifyReplacementAndQueuesItsOwnBlob(t *testing.T) {
	env := newIdemEnv(t)
	source := newIdemSource(t, false)
	id := uuid.New()
	var replacement *model.Attachment
	source.onHit = func() {
		// Runs on the HTTP server goroutine, so it must not call FailNow.
		if env.row(t, id) == nil {
			t.Error("the row must exist while the download runs")
			return
		}
		env.deleteRow(t, idemAlice, id)
		replacement = env.createRow(t, idemAlice, id, "downloading")
	}

	rec := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id.String()})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

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

func TestStaleURLDownloadFailureCannotFailReplacement(t *testing.T) {
	env := newIdemEnv(t)
	source := newIdemSource(t, false)
	source.status = http.StatusInternalServerError
	id := uuid.New()
	var replacement *model.Attachment
	source.onHit = func() {
		// Runs on the HTTP server goroutine, so it must not call FailNow.
		if env.row(t, id) == nil {
			t.Error("the row must exist while the download runs")
			return
		}
		env.deleteRow(t, idemAlice, id)
		replacement = env.createRow(t, idemAlice, id, "downloading")
	}

	rec := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id.String()})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	require.Eventually(t, func() bool { return env.wrap.marks.Load() >= 1 }, 5*time.Second, 5*time.Millisecond, "the failure path ran")
	require.NotNil(t, replacement)
	after := env.row(t, id)
	require.Equal(t, "downloading", after.Status, "a stale failure must not fail the replacement")
	require.Equal(t, *replacement.AttemptID, *after.AttemptID)
}

func TestURLDownloadLostFinalizeAcknowledgmentKeepsRowReady(t *testing.T) {
	env := newIdemEnv(t)
	env.wrap.loseAck = true
	source := newIdemSource(t, false)
	id := uuid.New()

	rec := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id.String()})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	require.Eventually(t, func() bool { return env.wrap.marks.Load() >= 1 }, 5*time.Second, 5*time.Millisecond, "the failure path ran")
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

func TestURLCreateWithIDIsReplaySafe(t *testing.T) {
	env := newIdemEnv(t)
	env.wrap.replay = true
	source := newIdemSource(t, false)
	id := uuid.New()

	rec := env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id.String()})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, 1, bytes.Count(rec.Body.Bytes(), []byte(`"id"`)), "only the committed attempt's response is written")
	require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return source.hits.Load() > 1 }, 200*time.Millisecond, 10*time.Millisecond, "the download starts once, for the committed attempt")
	require.Equal(t, 1, env.blobs.storeCalls())
}

// --- operation event correlation ---

func TestRESTUploadRecordsCallerIDOnOperationEvent(t *testing.T) {
	multipart := func(env *idemEnv, id string) *httptest.ResponseRecorder {
		return env.uploadMultipart(t, idemAlice, idemPtr(id), "hello", "")
	}
	url := func(env *idemEnv, id string) *httptest.ResponseRecorder {
		source := newIdemSource(t, false)
		return env.postURL(t, idemAlice, map[string]any{"sourceUrl": source.srv.URL, "id": id})
	}
	for name, send := range map[string]func(*idemEnv, string) *httptest.ResponseRecorder{"multipart": multipart, "url": url} {
		t.Run(name, func(t *testing.T) {
			env := newIdemEnv(t)
			env.event = operationevent.New("http POST /v1/attachments")
			id := uuid.New()
			require.Equal(t, http.StatusCreated, send(env, id.String()).Code)
			require.Equal(t, id.String(), env.event.Snapshot().AttachmentID, "a created attachment is correlated by its ID")
			require.Eventually(t, func() bool { return env.statusOf(t, id) == "ready" }, 5*time.Second, 10*time.Millisecond)

			// A duplicate is rejected, and the operation is still about that ID.
			env.event = operationevent.New("http POST /v1/attachments")
			require.Equal(t, http.StatusConflict, send(env, id.String()).Code)
			require.Equal(t, id.String(), env.event.Snapshot().AttachmentID)

			// An ID that fails validation is never applied to the event.
			env.event = operationevent.New("http POST /v1/attachments")
			require.Equal(t, http.StatusBadRequest, send(env, "not-a-uuid").Code)
			require.Empty(t, env.event.Snapshot().AttachmentID, "a rejected ID must not be recorded")
		})
	}
}
