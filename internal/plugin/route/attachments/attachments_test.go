package attachments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/plugin/route/attachments"
	"github.com/chirino/memory-service/internal/plugin/store/postgres"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/testutil/testpg"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type memAttachmentStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemAttachmentStore() *memAttachmentStore {
	return &memAttachmentStore{
		data: map[string][]byte{},
	}
}

func (s *memAttachmentStore) Store(_ context.Context, r io.Reader, maxSize int64, _ string) (*registryattach.FileStoreResult, error) {
	buf := bytes.Buffer{}
	n, err := io.CopyN(&buf, r, maxSize+1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n > maxSize {
		return nil, fmt.Errorf("file exceeds maximum size")
	}
	key := fmt.Sprintf("key-%d", time.Now().UnixNano())
	s.mu.Lock()
	s.data[key] = buf.Bytes()
	s.mu.Unlock()
	return &registryattach.FileStoreResult{
		StorageKey: key,
		Size:       int64(len(buf.Bytes())),
		SHA256:     "",
	}, nil
}

func (s *memAttachmentStore) Retrieve(_ context.Context, storageKey string) (io.ReadCloser, error) {
	s.mu.RLock()
	data, ok := s.data[storageKey]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memAttachmentStore) Delete(_ context.Context, storageKey string) error {
	s.mu.Lock()
	delete(s.data, storageKey)
	s.mu.Unlock()
	return nil
}

func (s *memAttachmentStore) GetSignedURL(_ context.Context, _ string, _ time.Duration) (*url.URL, error) {
	return nil, fmt.Errorf("signed url unsupported")
}

func setupAttachmentsRouter(t *testing.T) *gin.Engine {
	t.Helper()

	dbURL := testpg.StartPostgres(t)

	cfg := config.DefaultConfig()
	cfg.DBURL = dbURL
	cfg.MaxBodySize = 1024 * 1024
	cfg.AllowPrivateSourceURLs = true
	cfg.EncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cfg.EncryptionDBDisabled = true
	cfg.EncryptionAttachmentsDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)

	_ = postgres.ForceImport
	require.NoError(t, registrymigrate.RunAll(ctx))

	loader, err := registrystore.Select("postgres")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := func(c *gin.Context) { c.Set("userID", "test-user"); c.Next() }
	attachments.MountRoutes(router, store, newMemAttachmentStore(), &cfg, auth)
	return router
}

func doJSON(t *testing.T, router *gin.Engine, method, path, userID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestSourceURLAttachment_CreateAndDownload(t *testing.T) {
	router := setupAttachmentsRouter(t)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello-from-source"))
	}))
	defer source.Close()

	create := doJSON(t, router, http.MethodPost, "/v1/attachments", "alice", map[string]any{
		"sourceUrl":   source.URL,
		"contentType": "text/plain",
		"name":        "hello.txt",
	})
	require.Equal(t, http.StatusCreated, create.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(create.Body.Bytes(), &created))
	require.Equal(t, "downloading", created["status"])
	id, _ := created["id"].(string)
	require.NotEmpty(t, id)

	// Wait for background downloader to complete.
	deadline := time.Now().Add(3 * time.Second)
	for {
		req := httptest.NewRequest(http.MethodGet, "/v1/attachments/"+id+"/download-url", nil)
		req.Header.Set("Authorization", "Bearer alice")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			var payload map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
			if status, _ := payload["status"].(string); status == "downloading" {
				if time.Now().After(deadline) {
					t.Fatalf("attachment still downloading after timeout")
				}
				time.Sleep(30 * time.Millisecond)
				continue
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("download-url never became ready, status=%d body=%s", w.Code, w.Body.String())
		}
		time.Sleep(30 * time.Millisecond)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/attachments/"+id, nil)
	getReq.Header.Set("Authorization", "Bearer alice")
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)
	require.Equal(t, http.StatusOK, getResp.Code)
	require.Equal(t, "hello-from-source", getResp.Body.String())
}

func TestSourceURLAttachment_InvalidURL(t *testing.T) {
	router := setupAttachmentsRouter(t)

	create := doJSON(t, router, http.MethodPost, "/v1/attachments", "alice", map[string]any{
		"sourceUrl": "::not-a-url::",
	})
	require.Equal(t, http.StatusBadRequest, create.Code)
}

func TestAttachmentTokenDownloadAndDelete(t *testing.T) {
	router := setupAttachmentsRouter(t)

	// Multipart upload.
	form := &bytes.Buffer{}
	writer := multipart.NewWriter(form)
	part, err := writer.CreateFormFile("file", "hello.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("hello-token-download"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	uploadReq := httptest.NewRequest(http.MethodPost, "/v1/attachments", form)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("Authorization", "Bearer alice")
	uploadResp := httptest.NewRecorder()
	router.ServeHTTP(uploadResp, uploadReq)
	require.Equal(t, http.StatusCreated, uploadResp.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(uploadResp.Body.Bytes(), &created))
	id, _ := created["id"].(string)
	require.NotEmpty(t, id)

	// Request tokenized download URL.
	urlReq := httptest.NewRequest(http.MethodGet, "/v1/attachments/"+id+"/download-url", nil)
	urlReq.Header.Set("Authorization", "Bearer alice")
	urlResp := httptest.NewRecorder()
	router.ServeHTTP(urlResp, urlReq)
	require.Equal(t, http.StatusOK, urlResp.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(urlResp.Body.Bytes(), &payload))
	downloadPath, _ := payload["url"].(string)
	require.NotEmpty(t, downloadPath)
	require.Contains(t, downloadPath, "/v1/attachments/download/")

	// Token download route.
	downloadReq := httptest.NewRequest(http.MethodGet, downloadPath, nil)
	downloadResp := httptest.NewRecorder()
	router.ServeHTTP(downloadResp, downloadReq)
	require.Equal(t, http.StatusOK, downloadResp.Code)
	require.Equal(t, "hello-token-download", downloadResp.Body.String())

	// Delete attachment.
	deleteReq := httptest.NewRequest(http.MethodDelete, "/v1/attachments/"+id, nil)
	deleteReq.Header.Set("Authorization", "Bearer alice")
	deleteResp := httptest.NewRecorder()
	router.ServeHTTP(deleteResp, deleteReq)
	require.Equal(t, http.StatusNoContent, deleteResp.Code)

	// It should no longer be accessible.
	getReq := httptest.NewRequest(http.MethodGet, "/v1/attachments/"+id, nil)
	getReq.Header.Set("Authorization", "Bearer alice")
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)
	require.Equal(t, http.StatusNotFound, getResp.Code)
}
