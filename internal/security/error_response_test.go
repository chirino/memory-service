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

func TestErrorEnvelopeMiddlewarePreservesFieldAliasInDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"code": "validation_error", "error": "invalid page size", "field": "limit"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderRequestID, "req-2")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"code":"validation_error","error":"invalid page size","field":"limit","details":{"field":"limit"},"requestId":"req-2"}`, rec.Body.String())
}

func TestErrorEnvelopeMiddlewareNormalizesSearchTypeUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":          "search_type_unavailable",
			"message":        "One or more requested search types are not available on this server.",
			"availableTypes": []string{"fulltext"},
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderRequestID, "req-3")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
	require.JSONEq(t, `{"code":"search_type_unavailable","error":"One or more requested search types are not available on this server.","message":"One or more requested search types are not available on this server.","availableTypes":["fulltext"],"details":{"availableTypes":["fulltext"]},"requestId":"req-3"}`, rec.Body.String())
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
