//go:build site_tests

package sitebdd

import (
	"encoding/json"
	"net/http"
)

// handleOIDCDiscovery serves a minimal OIDC discovery document.
// Quarkus fetches this to find the introspection endpoint when discovery is enabled.
func (m *MockServer) handleOIDCDiscovery(w http.ResponseWriter, r *http.Request) {
	base := m.server.URL
	doc := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/auth",
		"token_endpoint":                        base + "/token",
		"jwks_uri":                              base + "/jwks",
		"introspection_endpoint":                base + "/introspect",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

// handleIntrospect accepts any bearer token and returns it as the active principal.
// Spring (opaquetoken) and Quarkus (OIDC introspection path) both call this endpoint.
// The raw token value becomes sub/preferred_username, so memory-service in testing mode
// treats it directly as the user ID.
func (m *MockServer) handleIntrospect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	token := r.FormValue("token")
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
		return
	}
	resp := map[string]any{
		"active":             true,
		"sub":                token,
		"preferred_username": token,
		"username":           token,
		"token_type":         "Bearer",
		"scope":              "openid profile",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleJWKS returns the RSA public key as a JWK Set for JWT signature validation.
func (m *MockServer) handleJWKS(w http.ResponseWriter, r *http.Request) {
	jwks := map[string]any{"keys": []map[string]any{m.jwkPublicKey()}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jwks)
}
