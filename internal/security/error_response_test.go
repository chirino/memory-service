package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestErrorEnvelopeMiddlewareAddsCodeAndRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid input"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderRequestID, "req-1")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"code":"invalid_request","error":"invalid input","requestId":"req-1"}`, rec.Body.String())
}

func TestErrorEnvelopeMiddlewarePreservesDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "invalid page size", "details": gin.H{"field": "limit"}})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderRequestID, "req-2")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"code":"validation_error","error":"invalid page size","details":{"field":"limit"},"requestId":"req-2"}`, rec.Body.String())
}

func TestErrorEnvelopeMiddlewarePreservesSearchTypeUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{
			"code":  "search_type_unavailable",
			"error": "One or more requested search types are not available on this server.",
			"details": gin.H{
				"availableTypes": []string{"fulltext"},
			},
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderRequestID, "req-3")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
	require.JSONEq(t, `{"code":"search_type_unavailable","error":"One or more requested search types are not available on this server.","details":{"availableTypes":["fulltext"]},"requestId":"req-3"}`, rec.Body.String())
}

func TestErrorEnvelopeMiddlewareDoesNotModifySuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"ok":true}`, rec.Body.String())
}

func TestErrorEnvelopeMiddlewarePreservesBodylessStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, expected := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(expected), func(t *testing.T) {
			router := gin.New()
			router.Use(ErrorEnvelopeMiddleware())
			router.GET("/test", func(c *gin.Context) {
				c.Status(expected)
			})

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

			require.Equal(t, expected, rec.Code)
			require.Empty(t, rec.Body.String())
		})
	}
}

func TestErrorEnvelopeMiddlewareReportsWrittenSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorEnvelopeMiddleware())
	writtenSize := -1
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "hello")
		writtenSize = c.Writer.Size()
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "hello", rec.Body.String())
	require.Equal(t, len("hello"), writtenSize)
}
