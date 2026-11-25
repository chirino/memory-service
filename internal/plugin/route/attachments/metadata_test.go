package attachments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// metadataStore serves GetAttachment from a fixed result and records what the handler asked for.
// Any other MemoryStore call panics on the nil embedded interface, which proves the metadata
// handler reads nothing else (no blob store, no writes, no tasks).
type metadataStore struct {
	registrystore.MemoryStore
	attachment *model.Attachment
	err        error
	readTx     int
	writeTx    int
	gotUser    string
	gotConv    string
	gotID      uuid.UUID
}

func (s *metadataStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	s.readTx++
	return fn(ctx)
}

func (s *metadataStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	s.writeTx++
	return fn(ctx)
}

func (s *metadataStore) GetAttachment(_ context.Context, userID, conversationID string, id uuid.UUID) (*model.Attachment, error) {
	s.gotUser, s.gotConv, s.gotID = userID, conversationID, id
	if s.err != nil {
		return nil, s.err
	}
	attachment := *s.attachment
	return &attachment, nil
}

func getMetadata(t *testing.T, store *metadataStore, idParam string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/attachments/"+idParam+"/metadata", nil)
	c.Params = gin.Params{{Key: "id", Value: idParam}}
	c.Set("userID", "user-1")
	getAttachmentMetadata(c, store)
	return recorder
}

func TestGetAttachmentMetadataReturnsStatusForEveryState(t *testing.T) {
	t.Parallel()
	expires := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, status := range []string{"uploading", "downloading", "ready", "failed"} {
		t.Run(status, func(t *testing.T) {
			id := uuid.New()
			filename := "doc.txt"
			size := int64(5)
			sha := "abc123"
			storageKey := "blob-key"
			sourceURL := "https://example.com/doc.txt"
			store := &metadataStore{attachment: &model.Attachment{
				ID: id, UserID: "user-1", Filename: &filename, ContentType: "text/plain",
				Size: &size, SHA256: &sha, StorageKey: &storageKey, SourceURL: &sourceURL,
				ExpiresAt: &expires, Status: status,
			}}

			recorder := getMetadata(t, store, id.String())

			// Unlike the content endpoint, an unfinished or failed attachment is a normal 200 here:
			// the whole point is to let a client read the status.
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
			var body map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.Equal(t, status, body["status"])
			require.Equal(t, id.String(), body["id"])
			require.Equal(t, "/v1/attachments/"+id.String(), body["href"])
			require.Equal(t, "text/plain", body["contentType"])
			require.Equal(t, "doc.txt", body["filename"])
			require.EqualValues(t, 5, body["size"])
			require.Equal(t, "abc123", body["sha256"])
			require.Equal(t, "2030-01-02T03:04:05Z", body["expiresAt"])
			require.Equal(t, sourceURL, body["sourceUrl"])
			// Same shape as the upload response: no internal fields leak.
			require.NotContains(t, body, "storageKey")
			require.NotContains(t, body, "attemptId")
			require.NotContains(t, body, "userId")
		})
	}
}

func TestGetAttachmentMetadataUsesTheAttachmentReadRulesAndOnlyReads(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := &metadataStore{attachment: &model.Attachment{ID: id, UserID: "user-1", ContentType: "text/plain", Status: "ready"}}

	recorder := getMetadata(t, store, id.String())

	require.Equal(t, http.StatusOK, recorder.Code)
	// GetAttachment is the same access check as GET /v1/attachments/{id}: caller identity, no
	// conversation scoping.
	require.Equal(t, "user-1", store.gotUser)
	require.Empty(t, store.gotConv)
	require.Equal(t, id, store.gotID)
	require.Equal(t, 1, store.readTx)
	require.Zero(t, store.writeTx, "a metadata lookup must not open a write transaction")
}

func TestGetAttachmentMetadataMapsStoreErrors(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		// Another user's unlinked attachment, or a linked one in an unreadable conversation.
		"forbidden": {&registrystore.ForbiddenError{}, http.StatusForbidden},
		// Never created, deleted, archived, or hidden entry.
		"not found": {&registrystore.NotFoundError{Resource: "attachment", ID: id.String()}, http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			store := &metadataStore{err: tc.err}

			recorder := getMetadata(t, store, id.String())

			require.Equal(t, tc.want, recorder.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			require.NotEmpty(t, body["error"])
			require.NotContains(t, body, "status", "an error must not look like attachment metadata")
		})
	}
}

func TestGetAttachmentMetadataRejectsInvalidID(t *testing.T) {
	t.Parallel()
	store := &metadataStore{attachment: &model.Attachment{}}

	recorder := getMetadata(t, store, "not-a-uuid")

	// The contract documents 400 for an id that is not a UUID.
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, store.readTx, "an invalid id must not reach the store")
}
