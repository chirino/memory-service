package attachments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	internaltracing "github.com/chirino/memory-service/internal/tracing"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type retryingAttachmentMemoryStore struct {
	registrystore.MemoryStore
	attachment  model.Attachment
	createCalls atomic.Int32
	deleteCalls atomic.Int32
	taskCalls   atomic.Int32
	updateCalls atomic.Int32
	taskTypes   []string
	taskBodies  []map[string]any
	writeErr    error
	createErr   error
	updateErr   error
	queueErr    error
}

func (s *retryingAttachmentMemoryStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	if err := fn(ctx); err != nil {
		return err
	}
	if s.writeErr != nil {
		return s.writeErr
	}
	return fn(ctx)
}

func (s *retryingAttachmentMemoryStore) CreateAttachment(_ context.Context, _, _ string, attachment model.Attachment) (*model.Attachment, error) {
	s.createCalls.Add(1)
	if s.createErr != nil {
		return nil, s.createErr
	}
	attachment.ID = s.attachment.ID
	return &attachment, nil
}

func (s *retryingAttachmentMemoryStore) GetAttachment(_ context.Context, _, _ string, _ uuid.UUID) (*model.Attachment, error) {
	attachment := s.attachment
	return &attachment, nil
}

func (s *retryingAttachmentMemoryStore) DeleteAttachment(context.Context, string, string, uuid.UUID) error {
	s.deleteCalls.Add(1)
	if s.attachment.StorageKey != nil {
		return s.QueueAttachmentBlobDelete(context.Background(), *s.attachment.StorageKey)
	}
	return nil
}

func (s *retryingAttachmentMemoryStore) CreateTask(_ context.Context, taskType string, taskBody map[string]any) error {
	s.taskCalls.Add(1)
	s.taskTypes = append(s.taskTypes, taskType)
	s.taskBodies = append(s.taskBodies, taskBody)
	return nil
}

func (s *retryingAttachmentMemoryStore) QueueAttachmentBlobDelete(_ context.Context, storageKey string) error {
	s.taskCalls.Add(1)
	s.taskTypes = append(s.taskTypes, registrystore.AttachmentBlobDeleteTaskType)
	s.taskBodies = append(s.taskBodies, map[string]any{registrystore.AttachmentBlobDeleteStorageKeyField: storageKey})
	return s.queueErr
}

func (s *retryingAttachmentMemoryStore) CreateAttachmentWithID(_ context.Context, _ string, id uuid.UUID, attachment model.Attachment) (*registrystore.CreateAttachmentResult, error) {
	s.createCalls.Add(1)
	if s.createErr != nil {
		return nil, s.createErr
	}
	attemptID := uuid.New()
	attachment.ID = id
	attachment.AttemptID = &attemptID
	return &registrystore.CreateAttachmentResult{Attachment: &attachment, Created: true}, nil
}

func (s *retryingAttachmentMemoryStore) MarkAttachmentFailed(context.Context, string, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

func (s *retryingAttachmentMemoryStore) FinalizeAttachment(_ context.Context, _ string, _ uuid.UUID, _ uuid.UUID, _ registrystore.AttachmentFinalize) (*model.Attachment, error) {
	s.updateCalls.Add(1)
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	attachment := s.attachment
	return &attachment, nil
}

type countingAttachmentStore struct {
	storeCalls  atomic.Int32
	deleteCalls atomic.Int32
}

func (s *countingAttachmentStore) Store(_ context.Context, reader io.Reader, _ int64, _ string) (*registryattach.FileStoreResult, error) {
	s.storeCalls.Add(1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return &registryattach.FileStoreResult{StorageKey: "stored-key", Size: int64(len(data))}, nil
}

func (s *countingAttachmentStore) Retrieve(context.Context, string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *countingAttachmentStore) Delete(context.Context, string) error {
	s.deleteCalls.Add(1)
	return nil
}

func (s *countingAttachmentStore) GetSignedURL(context.Context, string, time.Duration, *registryattach.SignedURLOptions) (*url.URL, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestSourceURLDownloadStartsAfterRetriedTransactionCommits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var sourceCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sourceCalls.Add(1)
		_, _ = w.Write([]byte("content"))
	}))
	t.Cleanup(source.Close)

	attachmentID := uuid.New()
	memoryStore := &retryingAttachmentMemoryStore{attachment: model.Attachment{ID: attachmentID}}
	attachmentStore := &countingAttachmentStore{}
	cfg := &config.Config{
		AllowPrivateSourceURLs:     true,
		AttachmentDefaultExpiresIn: time.Hour,
		AttachmentMaxSize:          1024,
		TempDir:                    t.TempDir(),
	}
	body, err := json.Marshal(map[string]any{"sourceUrl": source.URL})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/attachments", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("userID", "user-1")

	upload(c, memoryStore, attachmentStore, cfg)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, int32(2), memoryStore.createCalls.Load())
	require.Eventually(t, func() bool {
		return sourceCalls.Load() == 1 && attachmentStore.storeCalls.Load() == 1
	}, 3*time.Second, 10*time.Millisecond)
}

func TestBlobDeleteIsDurablyEnqueuedDuringRetriedTransaction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	attachmentID := uuid.New()
	storageKey := "stored-key"
	memoryStore := &retryingAttachmentMemoryStore{attachment: model.Attachment{ID: attachmentID, StorageKey: &storageKey}}
	attachmentStore := &countingAttachmentStore{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v1/attachments/"+attachmentID.String(), nil)
	c.Params = gin.Params{{Key: "id", Value: attachmentID.String()}}
	c.Set("userID", "user-1")

	deleteAttachment(c, memoryStore, attachmentStore)

	require.Equal(t, http.StatusNoContent, c.Writer.Status())
	require.Equal(t, int32(2), memoryStore.deleteCalls.Load())
	require.Equal(t, int32(2), memoryStore.taskCalls.Load())
	require.Equal(t, []string{registrystore.AttachmentBlobDeleteTaskType, registrystore.AttachmentBlobDeleteTaskType}, memoryStore.taskTypes)
	for _, body := range memoryStore.taskBodies {
		require.Equal(t, storageKey, body[registrystore.AttachmentBlobDeleteStorageKeyField])
	}
	require.Zero(t, attachmentStore.deleteCalls.Load(), "the provider must not be called from the request transaction")
}

func TestBlobDeleteDoesNotCallProviderWhenTransactionFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	attachmentID := uuid.New()
	storageKey := "stored-key"
	memoryStore := &retryingAttachmentMemoryStore{
		attachment: model.Attachment{ID: attachmentID, StorageKey: &storageKey},
		writeErr:   errors.New("commit failed"),
	}
	attachmentStore := &countingAttachmentStore{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v1/attachments/"+attachmentID.String(), nil)
	c.Params = gin.Params{{Key: "id", Value: attachmentID.String()}}
	c.Set("userID", "user-1")

	deleteAttachment(c, memoryStore, attachmentStore)

	require.Equal(t, int32(1), memoryStore.deleteCalls.Load())
	require.Equal(t, int32(1), memoryStore.taskCalls.Load())
	require.Zero(t, attachmentStore.deleteCalls.Load(), "an aborted transaction must not call the provider")
}

func TestSourceURLFinalizationFailureDurablyQueuesStoredBlob(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	t.Cleanup(source.Close)

	attachmentID := uuid.New()
	memoryStore := &retryingAttachmentMemoryStore{
		attachment: model.Attachment{ID: attachmentID},
		updateErr:  &registrystore.NotFoundError{Resource: "attachment", ID: attachmentID.String()},
	}
	attachmentStore := &countingAttachmentStore{}
	cfg := &config.Config{AllowPrivateSourceURLs: true, AttachmentMaxSize: 1024, TempDir: t.TempDir()}

	ctx := context.Background()
	err := completeSourceURLAttachment(ctx, memoryStore, attachmentStore, cfg, attachmentID, uuid.New(), "user-1", source.URL, "text/plain", internaltracing.ProviderFromContextOrNoop(ctx))
	require.Error(t, err)
	require.Equal(t, int32(1), attachmentStore.storeCalls.Load())
	require.Equal(t, int32(2), memoryStore.taskCalls.Load(), "detached cleanup transaction is retry-safe")
	for _, body := range memoryStore.taskBodies {
		require.Equal(t, "stored-key", body[registrystore.AttachmentBlobDeleteStorageKeyField])
	}
	require.Zero(t, attachmentStore.deleteCalls.Load())
}

func TestSourceURLFinalizationReportsCleanupEnqueueFailure(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	t.Cleanup(source.Close)

	attachmentID := uuid.New()
	updateErr := errors.New("finalize failed")
	queueErr := errors.New("cleanup queue unavailable")
	memoryStore := &retryingAttachmentMemoryStore{
		attachment: model.Attachment{ID: attachmentID},
		updateErr:  updateErr,
		queueErr:   queueErr,
	}
	attachmentStore := &countingAttachmentStore{}
	cfg := &config.Config{AllowPrivateSourceURLs: true, AttachmentMaxSize: 1024, TempDir: t.TempDir()}

	ctx := context.Background()
	err := completeSourceURLAttachment(ctx, memoryStore, attachmentStore, cfg, attachmentID, uuid.New(), "user-1", source.URL, "text/plain", internaltracing.ProviderFromContextOrNoop(ctx))
	require.ErrorIs(t, err, updateErr)
	require.ErrorIs(t, err, queueErr)
	require.Equal(t, int32(1), memoryStore.taskCalls.Load())
	require.Equal(t, int32(1), attachmentStore.storeCalls.Load())
	require.Zero(t, attachmentStore.deleteCalls.Load())
}

func TestMultipartMetadataCreationFailureDurablyQueuesStoredBlob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "upload.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("content"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	createErr := errors.New("metadata create failed")
	memoryStore := &retryingAttachmentMemoryStore{createErr: createErr}
	attachmentStore := &countingAttachmentStore{}
	cfg := &config.Config{AttachmentMaxSize: 1024, AttachmentDefaultExpiresIn: time.Hour}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/attachments", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Set("userID", "user-1")

	upload(c, memoryStore, attachmentStore, cfg)

	require.Equal(t, int32(1), attachmentStore.storeCalls.Load())
	require.Equal(t, int32(2), memoryStore.taskCalls.Load(), "detached cleanup transaction is retry-safe")
	for _, taskBody := range memoryStore.taskBodies {
		require.Equal(t, "stored-key", taskBody[registrystore.AttachmentBlobDeleteStorageKeyField])
	}
	require.Zero(t, attachmentStore.deleteCalls.Load())
}

func TestMultipartInvalidExpiryDoesNotStoreBlob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "upload.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("content"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	memoryStore := &retryingAttachmentMemoryStore{}
	attachmentStore := &countingAttachmentStore{}
	cfg := &config.Config{AttachmentMaxSize: 1024, AttachmentDefaultExpiresIn: time.Hour, AttachmentMaxExpiresIn: 24 * time.Hour}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/attachments?expiresIn=not-a-duration", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Set("userID", "user-1")

	upload(c, memoryStore, attachmentStore, cfg)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, attachmentStore.storeCalls.Load())
	require.Zero(t, memoryStore.createCalls.Load())
	require.Zero(t, memoryStore.taskCalls.Load())
}
