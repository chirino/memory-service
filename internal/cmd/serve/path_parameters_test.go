package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	generatedadmin "github.com/chirino/memory-service/internal/generated/admin"
	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/chirino/memory-service/internal/security"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	openapi_runtime "github.com/oapi-codegen/runtime"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/stretchr/testify/require"
)

func newPathParameterTestRouter() *gin.Engine {
	router := newGinRouter()
	router.GET("/resources/:id", func(c *gin.Context) {
		var id string
		err := openapi_runtime.BindStyledParameterWithOptions(
			"simple",
			"id",
			c.Param("id"),
			&id,
			openapi_runtime.BindStyledParameterOptions{
				ParamLocation: openapi_runtime.ParamLocationPath,
				Required:      true,
				Type:          "string",
			},
		)
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.String(http.StatusOK, id)
	})
	return router
}

func TestExplicitOperationResourceSettersClassifyGenericIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	event := operationevent.New("http GET /resource/{id}", operationevent.WithEmitter(func(string, operationevent.Level, operationevent.Snapshot) {}))
	c.Request = httptest.NewRequest(http.MethodGet, "/resource/id", nil).WithContext(operationevent.WithContext(context.Background(), event))

	setOperationConversationID(c, "conversation-1")
	setOperationEntryID(c, "entry-1")
	setOperationAttachmentID(c, "attachment-1")
	setOperationMemoryID(c, "memory-1")

	snapshot := event.Snapshot()
	require.Equal(t, "conversation-1", snapshot.ConversationID)
	require.Equal(t, "entry-1", snapshot.EntryID)
	require.Equal(t, "attachment-1", snapshot.AttachmentID)
	require.Equal(t, "memory-1", snapshot.MemoryID)
	require.Same(t, event, security.OperationEventFromGin(c))
}

func TestAdminOperationResourcesAreSetOnlyAfterAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &proxyAdminServer{auth: func(c *gin.Context) {
		c.AbortWithStatus(http.StatusForbidden)
	}}
	id := openapi_types.UUID(uuid.New())

	tests := []struct {
		name   string
		invoke func(*gin.Context)
		field  func(operationevent.Snapshot) string
	}{
		{name: "memory", invoke: func(c *gin.Context) { server.AdminGetMemory(c, id, generatedadmin.AdminGetMemoryParams{}) }, field: func(s operationevent.Snapshot) string { return s.MemoryID }},
		{name: "entry", invoke: func(c *gin.Context) { server.AdminGetEntry(c, id, generatedadmin.AdminGetEntryParams{}) }, field: func(s operationevent.Snapshot) string { return s.EntryID }},
		{name: "attachment", invoke: func(c *gin.Context) { server.AdminGetAttachment(c, id, generatedadmin.AdminGetAttachmentParams{}) }, field: func(s operationevent.Snapshot) string { return s.AttachmentID }},
		{name: "conversation", invoke: func(c *gin.Context) {
			server.AdminGetConversation(c, "conversation-1", generatedadmin.AdminGetConversationParams{})
		}, field: func(s operationevent.Snapshot) string { return s.ConversationID }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			event := operationevent.New("http GET /v1/admin/resource/{id}", operationevent.WithEmitter(func(string, operationevent.Level, operationevent.Snapshot) {}))
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/admin/resource/id", nil).WithContext(operationevent.WithContext(context.Background(), event))

			tt.invoke(c)

			require.True(t, c.IsAborted())
			require.Empty(t, tt.field(event.Snapshot()))
		})
	}
}

func TestGinRouterDecodesPathParametersExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newPathParameterTestRouter()

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{name: "slash", path: "/resources/run%2Fbranch", expected: "run/branch"},
		{name: "percent", path: "/resources/run%25branch", expected: "run%branch"},
		{name: "space", path: "/resources/run%20branch", expected: "run branch"},
		{name: "unicode", path: "/resources/caf%C3%A9%E2%98%95", expected: "café☕"},
		{name: "literal plus", path: "/resources/a+b", expected: "a+b"},
		{name: "encoded plus", path: "/resources/a%2Bb", expected: "a+b"},
		{name: "no double decoding", path: "/resources/run%252Fbranch", expected: "run%2Fbranch"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			require.Equal(t, http.StatusOK, resp.Code)
			require.Equal(t, tc.expected, resp.Body.String())
		})
	}
}

func TestGinRouterKeepsUnencodedSeparatorsOutOfPathParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newPathParameterTestRouter()

	for _, path := range []string{
		"/resources/run/branch",
		"/resources/../branch",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		require.Equal(t, http.StatusNotFound, resp.Code, path)
	}
}
