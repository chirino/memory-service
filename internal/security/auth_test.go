package security

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLocalUnixSocketIdentityForHTTPAndGRPC(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UnixSocketAuth = "local"
	cfg.LocalUserID = "alice"
	cfg.LocalClientID = "local-agent"
	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)

	router := gin.New()
	router.Use(AuthMiddleware(resolver))
	router.GET("/identity", func(c *gin.Context) {
		id := GetIdentity(c)
		require.Equal(t, "alice", id.UserID)
		require.Equal(t, "local-agent", id.ClientID)
		require.Empty(t, id.Roles)
		c.Status(http.StatusNoContent)
	})
	router.GET("/admin", RequireAdminRole(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/identity", nil))
	require.Equal(t, http.StatusNoContent, recorder.Code)
	adminRecorder := httptest.NewRecorder()
	router.ServeHTTP(adminRecorder, httptest.NewRequest(http.MethodGet, "/admin", nil))
	require.Equal(t, http.StatusForbidden, adminRecorder.Code)

	ctx := resolveGRPCIdentity(context.Background(), resolver)
	id := IdentityFromContext(ctx)
	require.NotNil(t, id)
	require.Equal(t, "alice", id.UserID)
	require.Equal(t, "local-agent", id.ClientID)
	require.False(t, id.IsAdmin)
}

func TestNewTokenResolverOIDCSelfSignedIssuerRequiresExplicitTLSBypass(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/.well-known/openid-configuration", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(map[string]any{
			"issuer":   "https://example.invalid",
			"jwks_uri": "https://example.invalid/certs",
		})
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	cfg := config.DefaultConfig()
	cfg.OIDCIssuer = "https://example.invalid"
	cfg.OIDCDiscoveryURL = server.URL

	resolver, err := NewTokenResolver(&cfg)
	require.Error(t, err) // discovery must fail since the TLS cert is self-signed

	cfg.OIDCTLSSkipCertificateVerify = true
	resolver, err = NewTokenResolver(&cfg)
	require.NoError(t, err)
	require.NotNil(t, resolver.verifier)
	require.Empty(t, resolver.allowedClients)
	require.Empty(t, resolver.allowedAudience)
}
