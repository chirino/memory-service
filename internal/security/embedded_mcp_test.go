package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildEmbeddedResolver creates a minimal resolver with embedded MCP configured.
func buildEmbeddedResolver(t *testing.T) *TokenResolver {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.APIKeys = map[string]string{"embedded-mcp-api-key": "embedded-mcp"}
	cfg.AdminClients = "embedded-mcp"
	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)
	resolver.ConfigureEmbeddedMCP("embedded-mcp-user", "embedded-mcp")
	return resolver
}

// buildTestRouter wraps AuthMiddleware + a trivial 200 OK handler in a gin engine.
// /v1/conversations also applies RequireUser, mirroring the real endpoint.
func buildTestRouter(resolver *TokenResolver) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(resolver))
	r.GET("/v1/conversations", RequireUser(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": GetUserID(c)})
	})
	r.GET("/v1/admin/conversations", RequireAdminRole(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": GetUserID(c)})
	})
	return r
}

// TestEmbeddedMCPContextKeyIsUnforgeable verifies the core invariant: only
// WithEmbeddedMCPContext produces a context that isEmbeddedMCPRequest accepts.
func TestEmbeddedMCPContextKeyIsUnforgeable(t *testing.T) {
	blank := context.Background()
	assert.False(t, isEmbeddedMCPRequest(blank), "blank context must not be embedded MCP")

	// Setting the transport string on a plain context key cannot fool isEmbeddedMCPRequest
	// because the embeddedMCPContextKey type is unexported — no external package can use it.
	// Here we verify the positive case (the exported helper works) and the blank case (no key).
	stamped := WithEmbeddedMCPContext(blank)
	assert.True(t, isEmbeddedMCPRequest(stamped), "WithEmbeddedMCPContext must produce an accepted context")
}

// TestRemoteHTTPEmbeddedMCPHeaderDoesNotAuthenticate proves that a remote HTTP caller
// sending the EmbeddedMCPTransport value in a request header is rejected with 401.
// The resolver has embedded MCP configured so the embedded path would succeed if the
// header were accepted — this test confirms it is not.
func TestRemoteHTTPEmbeddedMCPHeaderDoesNotAuthenticate(t *testing.T) {
	resolver := buildEmbeddedResolver(t)
	router := buildTestRouter(resolver)

	// Attempt 1: send EmbeddedMCPTransport as an Authorization header value.
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+EmbeddedMCPTransport)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"Authorization: Bearer embedded-mcp must not authenticate as embedded MCP")

	// Attempt 2: send EmbeddedMCPTransport as an arbitrary custom header.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req2.Header.Set("X-Embedded-MCP-Transport", EmbeddedMCPTransport)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusUnauthorized, w2.Code,
		"custom header with embedded MCP value must not authenticate as embedded MCP")

	// Attempt 3: send both the custom header and a valid API key — API key auth must still
	// work, but the embedded MCP identity path must NOT be taken.
	req3 := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req3.Header.Set("X-Embedded-MCP-Transport", EmbeddedMCPTransport)
	req3.Header.Set("X-API-Key", "embedded-mcp-api-key")
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)
	// API-key-only identity has no UserID so /v1/conversations (RequireUser) returns 401,
	// but the auth step itself should not have used the embedded MCP path.
	assert.Equal(t, http.StatusUnauthorized, w3.Code,
		"API-key-only with embedded MCP header must not produce an embedded MCP identity with UserID")
}

// TestInProcessEmbeddedMCPTransportAuthenticates proves that requests routed through
// handlerTransport (which calls WithEmbeddedMCPContext) do authenticate as embedded MCP.
// This test directly stamps the context, mirroring what handlerTransport.RoundTrip does.
func TestInProcessEmbeddedMCPTransportAuthenticates(t *testing.T) {
	resolver := buildEmbeddedResolver(t)
	router := buildTestRouter(resolver)

	// Stamp the context as if handlerTransport.RoundTrip had done it.
	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req = req.WithContext(WithEmbeddedMCPContext(req.Context()))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// /v1/conversations requires a user ID; embedded MCP provides one.
	assert.Equal(t, http.StatusOK, w.Code,
		"in-process embedded MCP request must be authenticated and have a UserID")
}
