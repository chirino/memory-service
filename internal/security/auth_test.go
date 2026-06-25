package security

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

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

	resolver := NewTokenResolver(&cfg)
	require.Nil(t, resolver.verifier)

	cfg.OIDCTLSSkipCertificateVerify = true
	resolver = NewTokenResolver(&cfg)
	require.NotNil(t, resolver.verifier)
}
