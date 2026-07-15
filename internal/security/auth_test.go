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
	cfg.OIDCAllowedAudiences = "memory-service"

	resolver, err := NewTokenResolver(&cfg)
	require.Error(t, err) // discovery must fail since the TLS cert is self-signed

	cfg.OIDCTLSSkipCertificateVerify = true
	resolver, err = NewTokenResolver(&cfg)
	require.NoError(t, err)
	require.NotNil(t, resolver.verifier)
	require.Empty(t, resolver.allowedClients)
	require.True(t, resolver.allowedAudience["memory-service"])
}

func TestNewTokenResolverRequiresOIDCAudience(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.OIDCIssuer = "https://issuer.example"

	resolver, err := NewTokenResolver(&cfg)
	require.ErrorContains(t, err, "OIDC allowed audiences are required")
	require.Nil(t, resolver)

	cfg.OIDCAllowMissingAudience = true
	resolver, err = NewTokenResolver(&cfg)
	require.ErrorContains(t, err, "OIDC provider discovery failed")
	require.Nil(t, resolver)
}

func TestExtractTokenRolesUsesDefaultRealmAccessPointerOnly(t *testing.T) {
	claims := map[string]any{
		"scope":  "admin",
		"groups": []any{"auditor"},
		"roles":  []any{"indexer"},
		"realm_access": map[string]any{
			"roles": []any{"admin"},
		},
	}

	pointers, err := validateRoleClaimPointers(nil)
	require.NoError(t, err)
	roles, err := extractTokenRoles(claims, pointers)
	require.NoError(t, err)
	require.True(t, roles["admin"])
	require.False(t, roles["auditor"])
	require.False(t, roles["indexer"])
}

func TestExtractTokenRolesSupportsConfiguredJSONPointers(t *testing.T) {
	claims := map[string]any{
		"groups": []any{"admin", "auditor"},
		"custom/claim": map[string]any{
			"tilde~key": "indexer",
		},
	}

	pointers, err := validateRoleClaimPointers([]string{"/groups", "/custom~1claim/tilde~0key"})
	require.NoError(t, err)
	roles, err := extractTokenRoles(claims, pointers)
	require.NoError(t, err)
	require.True(t, roles["admin"])
	require.True(t, roles["auditor"])
	require.True(t, roles["indexer"])
}

func TestExtractTokenRolesRejectsMalformedPresentClaim(t *testing.T) {
	pointers, err := validateRoleClaimPointers([]string{"/groups"})
	require.NoError(t, err)

	_, err = extractTokenRoles(map[string]any{"groups": []any{"admin", 42}}, pointers)
	require.ErrorContains(t, err, "must contain only strings")
}

func TestValidateRoleClaimPointersRejectsInvalidPointer(t *testing.T) {
	_, err := validateRoleClaimPointers([]string{"groups"})
	require.ErrorContains(t, err, "start with '/'")

	_, err = validateRoleClaimPointers([]string{"/bad~escape"})
	require.ErrorContains(t, err, "invalid JSON Pointer escape")
}
