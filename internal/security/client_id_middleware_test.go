package security

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// buildClientIDRouter wraps AuthMiddleware + a handler that
// echoes the resolved client ID back in JSON so tests can assert what landed in context.
func buildClientIDRouter(resolver *TokenResolver) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(resolver))
	r.GET("/v1/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"client": GetClientID(c)})
	})
	return r
}

// TestClientIDHeaderDoesNotOverrideAPIKeyClientID verifies that a raw X-Client-ID header
// cannot replace the client ID resolved from a valid API key.
func TestClientIDHeaderDoesNotOverrideAPIKeyClientID(t *testing.T) {
	resolver := buildEmbeddedResolver(t)
	router := buildClientIDRouter(resolver)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("X-API-Key", "embedded-mcp-api-key") // resolves to "embedded-mcp"
	req.Header.Set("X-Client-ID", "forged-client")      // must be ignored
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "embedded-mcp", "resolver-derived client ID must be preserved")
	assert.NotContains(t, body, "forged-client", "raw X-Client-ID must not override resolver-derived client ID")
}
