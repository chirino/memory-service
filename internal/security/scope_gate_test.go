package security

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

// mockOIDCServer is a minimal OIDC provider and JWT issuer for scope-gate unit tests.
type mockOIDCServer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newMockOIDCServer(t *testing.T) *mockOIDCServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	m := &mockOIDCServer{key: key}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":   m.server.URL,
			"jwks_uri": m.server.URL + "/.well-known/jwks",
		})
	})
	mux.HandleFunc("/.well-known/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{m.jwk()},
		})
	})

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockOIDCServer) jwk() map[string]any {
	pub := &m.key.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	eBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(eBuf, uint32(pub.E))
	for len(eBuf) > 1 && eBuf[0] == 0 {
		eBuf = eBuf[1:]
	}
	e := base64.RawURLEncoding.EncodeToString(eBuf)
	return map[string]any{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test-1", "n": n, "e": e,
	}
}

func (m *mockOIDCServer) issueToken(username string, realmRoles []string, scopes string) string {
	now := time.Now()
	payload := map[string]any{
		"sub":                username,
		"preferred_username": username,
		"iss":                m.server.URL,
		"azp":                "memory-service-client",
		"aud":                []string{"memory-service"},
		"iat":                now.Unix(),
		"exp":                now.Add(1 * time.Hour).Unix(),
		"scope":              scopes,
		"realm_access": map[string]any{
			"roles": realmRoles,
		},
	}
	headerB64 := base64url(mustMarshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-1"}))
	payloadB64 := base64url(mustMarshal(payload))
	sigInput := headerB64 + "." + payloadB64
	digest := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func base64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// buildResolver creates a TokenResolver backed by the mock OIDC server with resource/API scope gates.
func buildResolver(t *testing.T, m *mockOIDCServer, oidcScopes map[string]string) *TokenResolver {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.OIDCIssuer = m.server.URL
	cfg.OIDCAllowedClients = "memory-service-client"
	cfg.OIDCAllowedAudiences = "memory-service"
	cfg.AdminOIDCRole = "admin"
	cfg.AuditorOIDCRole = "auditor"
	cfg.IndexerOIDCRole = "indexer"
	cfg.OIDCScopes = oidcScopes
	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)
	return resolver
}

func TestResourceScopeGates_TokenResolverPreservesRoles(t *testing.T) {
	m := newMockOIDCServer(t)
	resolver := buildResolver(t, m, map[string]string{
		string(PermissionAdminConversationsRead): "ms:admin-conversations-read",
	})

	token := m.issueToken("alice", []string{"admin"}, "openid profile")
	creds := RequestCredentials{BearerToken: token}

	id, err := resolver.Resolve(context.Background(), creds)
	require.NoError(t, err)
	require.True(t, id.HasOIDCToken)
	require.True(t, id.Roles[RoleAdmin], "resource scope gates must not remove admin role at resolve time")
	require.True(t, id.Roles[RoleAuditor], "admin implication should still apply independently of resource scopes")
	require.True(t, id.Roles[RoleIndexer], "admin implication should still apply independently of resource scopes")

	err = checkIdentityOIDCScope(id, PermissionAdminConversationsRead)
	require.Error(t, err)
}

func TestResourceScopeGates_AggregateScopeAllowsReadAndWrite(t *testing.T) {
	id := &Identity{
		HasOIDCToken: true,
		OIDCScopes:   splitFields("ms:conversations"),
		OIDCScopeGates: parsePermissionScopes(map[string]string{
			string(PermissionConversations): "ms:conversations",
		}),
	}

	require.NoError(t, checkIdentityOIDCScope(id, PermissionConversationsRead))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionConversationsWrite))
}

func TestResourceScopeGates_UserScopeAllowsAllUserReadAndWrite(t *testing.T) {
	id := &Identity{
		HasOIDCToken: true,
		OIDCScopes:   splitFields("ms:user"),
		OIDCScopeGates: parsePermissionScopes(map[string]string{
			string(PermissionUser):      "ms:user",
			string(PermissionAdminRead): "ms:admin-read",
		}),
	}

	require.NoError(t, checkIdentityOIDCScope(id, PermissionConversationsRead))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionMemoriesWrite))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionAttachmentsRead))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionEventsRead))
	require.Error(t, checkIdentityOIDCScope(id, PermissionAdminConversationsRead))
}

func TestResourceScopeGates_UserReadAndWriteScopesAreDistinct(t *testing.T) {
	id := &Identity{
		HasOIDCToken: true,
		OIDCScopes:   splitFields("ms:user-read"),
		OIDCScopeGates: parsePermissionScopes(map[string]string{
			string(PermissionUserRead):  "ms:user-read",
			string(PermissionUserWrite): "ms:user-write",
		}),
	}

	require.NoError(t, checkIdentityOIDCScope(id, PermissionConversationsRead))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionMemoriesRead))
	require.Error(t, checkIdentityOIDCScope(id, PermissionConversationsWrite))
	require.Error(t, checkIdentityOIDCScope(id, PermissionMemoriesWrite))
}

func TestResourceScopeGates_AdminScopeAllowsAllAdminReadAndWrite(t *testing.T) {
	id := &Identity{
		HasOIDCToken: true,
		OIDCScopes:   splitFields("ms:admin"),
		OIDCScopeGates: parsePermissionScopes(map[string]string{
			string(PermissionAdmin):    "ms:admin",
			string(PermissionUserRead): "ms:user-read",
		}),
	}

	require.NoError(t, checkIdentityOIDCScope(id, PermissionAdminConversationsRead))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionAdminMemoriesWrite))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionAdminStatsRead))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionAdminMaintenanceWrite))
	require.Error(t, checkIdentityOIDCScope(id, PermissionConversationsRead))
}

func TestResourceScopeGates_AdminReadAndWriteScopesAreDistinct(t *testing.T) {
	id := &Identity{
		HasOIDCToken: true,
		OIDCScopes:   splitFields("ms:admin-read"),
		OIDCScopeGates: parsePermissionScopes(map[string]string{
			string(PermissionAdminRead):  "ms:admin-read",
			string(PermissionAdminWrite): "ms:admin-write",
		}),
	}

	require.NoError(t, checkIdentityOIDCScope(id, PermissionAdminConversationsRead))
	require.NoError(t, checkIdentityOIDCScope(id, PermissionAdminStatsRead))
	require.Error(t, checkIdentityOIDCScope(id, PermissionAdminConversationsWrite))
	require.Error(t, checkIdentityOIDCScope(id, PermissionAdminMaintenanceWrite))
}

func TestResourceScopeGates_SpecificReadAndWriteScopes(t *testing.T) {
	id := &Identity{
		HasOIDCToken: true,
		OIDCScopes:   splitFields("ms:conversations-read"),
		OIDCScopeGates: parsePermissionScopes(map[string]string{
			string(PermissionConversationsRead):  "ms:conversations-read",
			string(PermissionConversationsWrite): "ms:conversations-write",
		}),
	}

	require.NoError(t, checkIdentityOIDCScope(id, PermissionConversationsRead))
	require.Error(t, checkIdentityOIDCScope(id, PermissionConversationsWrite))
}

func TestResourceScopeGates_RejectsMissingConfiguredScope(t *testing.T) {
	id := &Identity{
		HasOIDCToken: true,
		OIDCScopes:   splitFields("openid profile"),
		OIDCScopeGates: parsePermissionScopes(map[string]string{
			string(PermissionAdminMemoriesRead): "ms:admin-memories-read",
		}),
	}

	require.Error(t, checkIdentityOIDCScope(id, PermissionAdminMemoriesRead))
}

func TestResourceScopeGates_DoNotApplyToNonOIDCIdentities(t *testing.T) {
	gates := parsePermissionScopes(map[string]string{
		string(PermissionAdminConversationsRead): "ms:admin-conversations-read",
		string(PermissionConversationsRead):      "ms:conversations-read",
	})

	apiKeyIdentity := &Identity{
		CredentialKind: CredentialAPIKey,
		HasOIDCToken:   false,
		OIDCScopeGates: gates,
	}
	embeddedIdentity := &Identity{
		CredentialKind: CredentialEmbeddedMCP,
		HasOIDCToken:   false,
		OIDCScopeGates: gates,
	}

	require.NoError(t, checkIdentityOIDCScope(apiKeyIdentity, PermissionAdminConversationsRead))
	require.NoError(t, checkIdentityOIDCScope(embeddedIdentity, PermissionConversationsRead))
}

func TestResourceScopeGates_EventStreamUserAndAdminScopesAreDistinct(t *testing.T) {
	id := &Identity{
		HasOIDCToken: true,
		OIDCScopes:   splitFields("ms:events-read"),
		OIDCScopeGates: parsePermissionScopes(map[string]string{
			string(PermissionEventsRead):      "ms:events-read",
			string(PermissionAdminEventsRead): "ms:admin-events-read",
		}),
	}

	require.NoError(t, checkIdentityOIDCScope(id, PermissionEventsRead))
	require.Error(t, checkIdentityOIDCScope(id, PermissionAdminEventsRead))
}
