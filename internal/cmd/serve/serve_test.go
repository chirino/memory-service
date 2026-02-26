package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsStreamingRequest(t *testing.T) {
	t.Run("multipart attachment upload is streaming", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/attachments", strings.NewReader("abcdef"))
		req.Header.Set("Content-Type", "multipart/form-data; boundary=abc123")
		require.True(t, isStreamingRequest(req))
	})

	t.Run("json attachment create is not streaming", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/attachments", strings.NewReader(`{"sourceUrl":"https://example.com/file"}`))
		req.Header.Set("Content-Type", "application/json")
		require.False(t, isStreamingRequest(req))
	})

	t.Run("non-attachment endpoint is not streaming", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/evict", strings.NewReader(`{"retentionPeriod":"P90D"}`))
		req.Header.Set("Content-Type", "application/json")
		require.False(t, isStreamingRequest(req))
	})
}

func TestMaxBodySizeMiddleware_SkipsForMultipartAttachmentUpload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(maxBodySizeMiddleware(4))
	router.POST("/v1/attachments", readBodyLengthHandler)

	req := httptest.NewRequest(http.MethodPost, "/v1/attachments", strings.NewReader("0123456789"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=abc123")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "10", rec.Body.String())
}

func TestMaxBodySizeMiddleware_EnforcesForNonStreamingEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(maxBodySizeMiddleware(4))
	router.POST("/v1/admin/evict", readBodyLengthHandler)

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/evict", strings.NewReader("0123456789"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func readBodyLengthHandler(c *gin.Context) {
	n, err := io.Copy(io.Discard, c.Request.Body)
	if err != nil {
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}
	c.String(http.StatusOK, "%d", n)
}
