//go:build !nosqlite && sqlite_fts5

package entries

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	_ "github.com/chirino/memory-service/internal/plugin/store/sqlite"
	registrymigrate "github.com/chirino/memory-service/internal/registry/migrate"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// linkReadyEnv runs the real append handler against the real SQLite store. The
// mock-based tests cannot show that a rejected link takes the entry with it.
type linkReadyEnv struct {
	ctx    context.Context
	store  registrystore.MemoryStore
	router *gin.Engine
}

func newLinkReadyEnv(t *testing.T) *linkReadyEnv {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.DatastoreType = "sqlite"
	cfg.DBURL = filepath.Join(t.TempDir(), "link-ready.db")
	cfg.DatastoreMigrateAtStart = true
	cfg.EncryptionDBDisabled = true
	ctx := config.WithContext(context.Background(), &cfg)
	require.NoError(t, registrymigrate.RunAll(ctx))
	loader, err := registrystore.Select("sqlite")
	require.NoError(t, err)
	store, err := loader(ctx)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/conversations/:conversationId/entries", func(c *gin.Context) {
		c.Set(security.ContextKeyUserID, c.GetHeader("X-Test-User"))
		HandleAppendEntry(c, store, nil)
	})
	return &linkReadyEnv{ctx: ctx, store: store, router: router}
}

func (e *linkReadyEnv) conversation(t *testing.T, user string) string {
	t.Helper()
	var id string
	require.NoError(t, e.store.InWriteTx(e.ctx, func(txCtx context.Context) error {
		conv, err := e.store.CreateConversation(txCtx, user, "", "link ready", nil, nil, nil, nil)
		if err != nil {
			return err
		}
		id = conv.ID
		return nil
	}))
	return id
}

// attachment creates an unlinked attachment through the store. A "ready" status
// is the no-ID flow; the others are the pending and failed rows a transfer leaves.
func (e *linkReadyEnv) attachment(t *testing.T, user, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, e.store.InWriteTx(e.ctx, func(txCtx context.Context) error {
		storageKey := "key-" + uuid.NewString()
		a, err := e.store.CreateAttachment(txCtx, user, "", model.Attachment{ContentType: "text/plain", Status: status, StorageKey: &storageKey})
		if err != nil {
			return err
		}
		id = a.ID
		return nil
	}))
	return id
}

func (e *linkReadyEnv) append(t *testing.T, user, convID string, attachmentID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"channel": "HISTORY", "contentType": "history",
		"content": []any{map[string]any{
			"role": "USER", "text": "see attached",
			"attachments": []any{map[string]any{"attachmentId": attachmentID.String(), "contentType": "text/plain"}},
		}},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+convID+"/entries", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", user)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *linkReadyEnv) entryCount(t *testing.T, user, convID string) int {
	t.Helper()
	var n int
	require.NoError(t, e.store.InReadTx(e.ctx, func(txCtx context.Context) error {
		page, err := e.store.GetEntries(txCtx, user, convID, registrystore.EntryListQuery{Limit: 50})
		if err != nil {
			return err
		}
		n = len(page.Data)
		return nil
	}))
	return n
}

func (e *linkReadyEnv) linked(t *testing.T, user string, id uuid.UUID) *uuid.UUID {
	t.Helper()
	var entryID *uuid.UUID
	require.NoError(t, e.store.InReadTx(e.ctx, func(txCtx context.Context) error {
		a, err := e.store.GetAttachment(txCtx, user, "", id)
		if err != nil {
			return err
		}
		entryID = a.EntryID
		return nil
	}))
	return entryID
}

func TestAppendEntryLinksReadyAttachment(t *testing.T) {
	env := newLinkReadyEnv(t)
	conv := env.conversation(t, "alice")
	id := env.attachment(t, "alice", "ready")

	rec := env.append(t, "alice", conv, id)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, 1, env.entryCount(t, "alice", conv))
	require.NotNil(t, env.linked(t, "alice", id), "the ready attachment is linked to the new entry")
}

func TestAppendEntryRejectsAttachmentThatIsNotReady(t *testing.T) {
	for _, status := range []string{"uploading", "downloading", "failed"} {
		t.Run(status, func(t *testing.T) {
			env := newLinkReadyEnv(t)
			conv := env.conversation(t, "alice")
			id := env.attachment(t, "alice", status)

			rec := env.append(t, "alice", conv, id)

			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), status, "the 409 body names the attachment status")
			require.Zero(t, env.entryCount(t, "alice", conv), "a rejected link must not leave an entry behind")
			require.Nil(t, env.linked(t, "alice", id), "the attachment must stay unlinked")
		})
	}
}

// A later link in the same append fails after an earlier one succeeded, so the
// earlier link has to roll back as well.
func TestAppendEntryRejectedLinkRollsBackEarlierLinks(t *testing.T) {
	env := newLinkReadyEnv(t)
	conv := env.conversation(t, "alice")
	readyID := env.attachment(t, "alice", "ready")
	pendingID := env.attachment(t, "alice", "downloading")

	body, err := json.Marshal(map[string]any{
		"channel": "HISTORY", "contentType": "history",
		"content": []any{map[string]any{
			"role": "USER", "text": "two files",
			"attachments": []any{
				map[string]any{"attachmentId": readyID.String(), "contentType": "text/plain"},
				map[string]any{"attachmentId": pendingID.String(), "contentType": "text/plain"},
			},
		}},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/v1/conversations/"+conv+"/entries", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", "alice")
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Zero(t, env.entryCount(t, "alice", conv))
	require.Nil(t, env.linked(t, "alice", readyID), "the ready attachment's link must roll back with the entry")
	require.Nil(t, env.linked(t, "alice", pendingID))

	// Once the pending attachment is ready, the same request succeeds.
	status := "ready"
	require.NoError(t, env.store.InWriteTx(env.ctx, func(txCtx context.Context) error {
		_, err := env.store.UpdateAttachment(txCtx, "alice", pendingID, registrystore.AttachmentUpdate{Status: &status})
		return err
	}))
	req = httptest.NewRequest(http.MethodPost, "/v1/conversations/"+conv+"/entries", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", "alice")
	rec = httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, 1, env.entryCount(t, "alice", conv))
	require.NotNil(t, env.linked(t, "alice", readyID))
	require.NotNil(t, env.linked(t, "alice", pendingID))
}

func TestAppendEntryAttachmentOwnershipOutranksStatus(t *testing.T) {
	env := newLinkReadyEnv(t)
	conv := env.conversation(t, "bob")
	// Alice's pending attachment: Bob must not learn its status.
	id := env.attachment(t, "alice", "downloading")

	rec := env.append(t, "bob", conv, id)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "downloading")
	require.Zero(t, env.entryCount(t, "bob", conv))
}

func TestAppendEntryMissingAttachmentIsStillNotFound(t *testing.T) {
	env := newLinkReadyEnv(t)
	conv := env.conversation(t, "alice")

	rec := env.append(t, "alice", conv, uuid.New())

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Zero(t, env.entryCount(t, "alice", conv))
}
