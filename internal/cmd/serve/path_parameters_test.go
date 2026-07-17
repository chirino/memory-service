package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	openapi_runtime "github.com/oapi-codegen/runtime"
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
