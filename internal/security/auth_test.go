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

	_, err := NewTokenResolver(&cfg)
	require.Error(t, err) // discovery must fail since the TLS cert is self-signed

	cfg.OIDCTLSSkipCertificateVerify = true
	resolver, err := NewTokenResolver(&cfg)
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
	emptyPointers, err := validateRoleClaimPointers([]string{})
	require.NoError(t, err)
	require.Equal(t, pointers, emptyPointers)
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

// TestJSONPointerValueArrayTraversal verifies RFC 6901 array-index traversal and strict index syntax.
func TestJSONPointerValueArrayTraversal(t *testing.T) {
	doc := map[string]any{
		"identities": []any{
			map[string]any{"userId": "first"},
			map[string]any{"userId": "second"},
		},
	}
	v, ok := jsonPointerValue(doc, "/identities/0/userId")
	require.True(t, ok)
	require.Equal(t, "first", v)

	v, ok = jsonPointerValue(doc, "/identities/1/userId")
	require.True(t, ok)
	require.Equal(t, "second", v)

	// Out-of-bounds
	_, ok = jsonPointerValue(doc, "/identities/2/userId")
	require.False(t, ok)

	// Non-numeric token
	_, ok = jsonPointerValue(doc, "/identities/notAnIndex/userId")
	require.False(t, ok)

	// RFC 6901 strict index: leading zero must be rejected
	_, ok = jsonPointerValue(doc, "/identities/01/userId")
	require.False(t, ok)

	// RFC 6901 strict index: leading plus sign must be rejected
	_, ok = jsonPointerValue(doc, "/identities/+1/userId")
	require.False(t, ok)

	// RFC 6901 strict index: negative zero must be rejected
	_, ok = jsonPointerValue(doc, "/identities/-0/userId")
	require.False(t, ok)
}

// TestIsRFC6901ArrayIndex covers the strict index grammar directly.
func TestIsRFC6901ArrayIndex(t *testing.T) {
	valid := []string{"0", "1", "10", "99", "100"}
	for _, s := range valid {
		require.True(t, isRFC6901ArrayIndex(s), "expected valid: %q", s)
	}
	invalid := []string{"", "01", "+1", "-0", "-1", "1a", "a", " 1"}
	for _, s := range invalid {
		require.False(t, isRFC6901ArrayIndex(s), "expected invalid: %q", s)
	}
}

func TestValidateRoleClaimPointersRejectsInvalidPointer(t *testing.T) {
	_, err := validateRoleClaimPointers([]string{"groups"})
	require.ErrorContains(t, err, "start with '/'")

	_, err = validateRoleClaimPointers([]string{"/bad~escape"})
	require.ErrorContains(t, err, "invalid JSON Pointer escape")
}

// ── OIDC user ID claim tests ──────────────────────────────────────────────────

// TestOIDCUserIDClaimDefaultsToSub verifies that when no claim is configured, /sub is used.
func TestOIDCUserIDClaimDefaultsToSub(t *testing.T) {
	m := newMockOIDCServer(t)
	resolver := buildResolver(t, m, nil)
	require.Equal(t, "/sub", resolver.userIDClaim)

	token := m.issueTokenWithClaims(map[string]any{
		"sub": "user-stable-id",
		"iss": m.server.URL,
		"azp": "memory-service-client",
		"aud": []string{"memory-service"},
	})
	id, err := resolver.Resolve(context.Background(), RequestCredentials{BearerToken: token})
	require.NoError(t, err)
	require.Equal(t, "user-stable-id", id.UserID)
}

// TestOIDCUserIDClaimPreferredUsernameOverride verifies that /preferred_username works for demo environments.
func TestOIDCUserIDClaimPreferredUsernameOverride(t *testing.T) {
	m := newMockOIDCServer(t)
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.OIDCIssuer = m.server.URL
	cfg.OIDCAllowedClients = "memory-service-client"
	cfg.OIDCAllowedAudiences = "memory-service"
	cfg.OIDCUserIDClaim = "/preferred_username"
	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)
	require.Equal(t, "/preferred_username", resolver.userIDClaim)

	token := m.issueTokenWithClaims(map[string]any{
		"sub":                "opaque-sub-id",
		"preferred_username": "alice",
		"iss":                m.server.URL,
		"azp":                "memory-service-client",
		"aud":                []string{"memory-service"},
	})
	id, err := resolver.Resolve(context.Background(), RequestCredentials{BearerToken: token})
	require.NoError(t, err)
	require.Equal(t, "alice", id.UserID)
}

// TestOIDCUserIDClaimMissingRejectsAuth verifies that a missing configured claim is a hard failure.
func TestOIDCUserIDClaimMissingRejectsAuth(t *testing.T) {
	m := newMockOIDCServer(t)
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.OIDCIssuer = m.server.URL
	cfg.OIDCAllowedClients = "memory-service-client"
	cfg.OIDCAllowedAudiences = "memory-service"
	cfg.OIDCUserIDClaim = "/preferred_username"
	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)

	// Token has only "sub", not "preferred_username"
	token := m.issueTokenWithClaims(map[string]any{
		"sub": "opaque-sub-id",
		"iss": m.server.URL,
		"azp": "memory-service-client",
		"aud": []string{"memory-service"},
	})
	_, err = resolver.Resolve(context.Background(), RequestCredentials{BearerToken: token})
	require.Error(t, err)
	require.ErrorIs(t, err, errMissingIdentity)
	require.Contains(t, err.Error(), "preferred_username")
}

// TestOIDCUserIDClaimNonStringRejectsAuth verifies that a non-string claim value is a hard failure.
// When the configured claim is "/sub", the OIDC library itself rejects a numeric sub value
// during token verification (before custom claim extraction). For non-sub claims, our code
// rejects the non-string value explicitly.
func TestOIDCUserIDClaimNonStringRejectsAuth(t *testing.T) {
	m := newMockOIDCServer(t)

	// Test with /sub (default): OIDC library rejects the numeric sub during verification.
	resolverSub := buildResolver(t, m, nil)
	tokenNumericSub := m.issueTokenWithClaims(map[string]any{
		"sub": 12345,
		"iss": m.server.URL,
		"azp": "memory-service-client",
		"aud": []string{"memory-service"},
	})
	_, err := resolverSub.Resolve(context.Background(), RequestCredentials{BearerToken: tokenNumericSub})
	require.Error(t, err)

	// Test with a non-standard claim pointer: our code catches the non-string type.
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.OIDCIssuer = m.server.URL
	cfg.OIDCAllowedClients = "memory-service-client"
	cfg.OIDCAllowedAudiences = "memory-service"
	cfg.OIDCUserIDClaim = "/user_id"
	resolverCustom, err := NewTokenResolver(&cfg)
	require.NoError(t, err)
	tokenNumericCustom := m.issueTokenWithClaims(map[string]any{
		"sub":     "valid-sub",
		"user_id": 42,
		"iss":     m.server.URL,
		"azp":     "memory-service-client",
		"aud":     []string{"memory-service"},
	})
	_, err = resolverCustom.Resolve(context.Background(), RequestCredentials{BearerToken: tokenNumericCustom})
	require.Error(t, err)
	require.ErrorIs(t, err, errMissingIdentity)
	require.Contains(t, err.Error(), "must be a string")
}

// TestOIDCUserIDClaimBlankRejectsAuth verifies that a blank claim value is a hard failure.
func TestOIDCUserIDClaimBlankRejectsAuth(t *testing.T) {
	m := newMockOIDCServer(t)
	resolver := buildResolver(t, m, nil) // uses /sub by default

	token := m.issueTokenWithClaims(map[string]any{
		"sub": "   ",
		"iss": m.server.URL,
		"azp": "memory-service-client",
		"aud": []string{"memory-service"},
	})
	_, err := resolver.Resolve(context.Background(), RequestCredentials{BearerToken: token})
	require.Error(t, err)
	require.ErrorIs(t, err, errMissingIdentity)
	require.Contains(t, err.Error(), "is blank")
}

// TestOIDCUserIDClaimInvalidPointerRejectedAtStartup verifies that a malformed pointer is rejected
// when building the resolver.
func TestOIDCUserIDClaimInvalidPointerRejectedAtStartup(t *testing.T) {
	m := newMockOIDCServer(t)
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.OIDCIssuer = m.server.URL
	cfg.OIDCAllowedAudiences = "memory-service"
	cfg.OIDCUserIDClaim = "sub" // missing leading slash
	_, err := NewTokenResolver(&cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "MEMORY_SERVICE_OIDC_USER_ID_CLAIM")
}

// TestOIDCUserIDClaimNoFallbackToOtherClaims verifies that with /sub configured, even a
// token with preferred_username resolves to sub (not preferred_username).
func TestOIDCUserIDClaimNoFallbackToOtherClaims(t *testing.T) {
	m := newMockOIDCServer(t)
	resolver := buildResolver(t, m, nil) // /sub is default

	token := m.issueTokenWithClaims(map[string]any{
		"sub":                "stable-sub",
		"preferred_username": "alice",
		"upn":                "alice@example.com",
		"iss":                m.server.URL,
		"azp":                "memory-service-client",
		"aud":                []string{"memory-service"},
	})
	id, err := resolver.Resolve(context.Background(), RequestCredentials{BearerToken: token})
	require.NoError(t, err)
	require.Equal(t, "stable-sub", id.UserID)
}
