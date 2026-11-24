package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// attachmentMetadataRouteStore serves a single attachment and counts reads. Any other MemoryStore
// call panics on the nil embedded interface.
type attachmentMetadataRouteStore struct {
	registrystore.MemoryStore
	attachment *model.Attachment
	reads      int
	gotUser    string
}

func (s *attachmentMetadataRouteStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	s.reads++
	return fn(ctx)
}

func (s *attachmentMetadataRouteStore) GetAttachment(_ context.Context, userID, _ string, _ uuid.UUID) (*model.Attachment, error) {
	s.gotUser = userID
	attachment := *s.attachment
	return &attachment, nil
}

// newAttachmentMetadataRouter registers the real API routes with test middleware that stands in for
// authentication: the identity is whatever the test supplies.
func newAttachmentMetadataRouter(store registrystore.MemoryStore, identity *security.Identity) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := newGinRouter()
	auth := func(c *gin.Context) {
		if identity != nil {
			c.Set(security.ContextKeyIdentity, identity)
			c.Set(security.ContextKeyUserID, identity.UserID)
		}
		c.Next()
	}
	passThrough := func(c *gin.Context) { c.Next() }
	cfg := config.DefaultConfig()
	registerAPIRoutes(router, auth, passThrough, passThrough, &cfg, store, nil, nil, nil, nil, nil, false, nil, nil, nil, nil)
	return router
}

func getAttachmentMetadataRoute(router *gin.Engine, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestAttachmentMetadataRouteIsRegisteredAndReturnsStatus(t *testing.T) {
	id := uuid.New()
	store := &attachmentMetadataRouteStore{attachment: &model.Attachment{
		ID: id, UserID: "alice", ContentType: "text/plain", Status: "uploading",
	}}
	router := newAttachmentMetadataRouter(store, &security.Identity{UserID: "alice"})

	recorder := getAttachmentMetadataRoute(router, "/v1/attachments/"+id.String()+"/metadata")

	// Without the route this is a 404 (or a match on the content route), never this body.
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "uploading", body["status"])
	require.Equal(t, id.String(), body["id"])
	require.Equal(t, "alice", store.gotUser)
	require.Equal(t, 1, store.reads)
}

func TestAttachmentMetadataRouteRejectsInvalidUUID(t *testing.T) {
	store := &attachmentMetadataRouteStore{attachment: &model.Attachment{}}
	router := newAttachmentMetadataRouter(store, &security.Identity{UserID: "alice"})

	recorder := getAttachmentMetadataRoute(router, "/v1/attachments/not-a-uuid/metadata")

	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Zero(t, store.reads, "an invalid id must not reach the store")
}

func TestAttachmentMetadataRouteRequiresAUser(t *testing.T) {
	id := uuid.New()
	store := &attachmentMetadataRouteStore{attachment: &model.Attachment{ID: id, UserID: "alice", Status: "ready"}}
	// A client-only identity has no user principal.
	router := newAttachmentMetadataRouter(store, &security.Identity{ClientID: "agent"})

	recorder := getAttachmentMetadataRoute(router, "/v1/attachments/"+id.String()+"/metadata")

	require.Equal(t, http.StatusUnauthorized, recorder.Code, recorder.Body.String())
	require.Zero(t, store.reads)
}

// The metadata lookup reads attachments, so it is gated by the attachments read scope, like
// GET /v1/attachments/{id}. A write-scoped token must not be enough.
func TestAttachmentMetadataRouteEnforcesAttachmentsReadScope(t *testing.T) {
	id := uuid.New()
	gates := map[security.Permission]map[string]bool{
		security.PermissionAttachmentsRead: {"attachments:read": true},
	}
	identityWithScope := func(scope string) *security.Identity {
		return &security.Identity{
			UserID: "alice", HasOIDCToken: true,
			OIDCScopes:     map[string]bool{scope: true},
			OIDCScopeGates: gates,
		}
	}

	for name, tc := range map[string]struct {
		scope string
		want  int
	}{
		"read scope allowed":      {"attachments:read", http.StatusOK},
		"unrelated scope blocked": {"conversations:read", http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			store := &attachmentMetadataRouteStore{attachment: &model.Attachment{ID: id, UserID: "alice", Status: "ready"}}
			router := newAttachmentMetadataRouter(store, identityWithScope(tc.scope))

			recorder := getAttachmentMetadataRoute(router, "/v1/attachments/"+id.String()+"/metadata")

			require.Equal(t, tc.want, recorder.Code, recorder.Body.String())
			if tc.want == http.StatusForbidden {
				require.Zero(t, store.reads, "a blocked caller must not reach the store")
			}
		})
	}

	// The same gate applies to the existing content route, so the two stay consistent.
	store := &attachmentMetadataRouteStore{attachment: &model.Attachment{ID: id, UserID: "alice", Status: "ready"}}
	router := newAttachmentMetadataRouter(store, identityWithScope("conversations:read"))
	recorder := getAttachmentMetadataRoute(router, "/v1/attachments/"+id.String())
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
}
