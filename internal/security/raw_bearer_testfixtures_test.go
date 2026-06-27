//go:build auth_testfixtures

package security

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRawBearerTestingFixtureResolvesUserAndClientRoles(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting
	cfg.AdminUsers = "alice"
	cfg.AdminClients = "fixture-client"

	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)

	id, err := resolver.Resolve(context.Background(), RequestCredentials{
		BearerToken:    "alice",
		ClientIDHeader: "fixture-client",
	})
	require.NoError(t, err)
	require.Equal(t, "alice", id.UserID)
	require.Equal(t, "fixture-client", id.ClientID)
	require.Equal(t, CredentialTesting, id.CredentialKind)
	require.True(t, id.Roles[RoleAdmin])
	require.True(t, id.Roles[RoleAuditor])
	require.True(t, id.Roles[RoleIndexer])
}

func TestRawBearerTestingFixtureRejectsProductionMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.APIKeys = map[string]string{"known-key": "known-client"}

	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), RequestCredentials{
		BearerToken: "alice",
	})
	require.ErrorContains(t, err, "require both testing mode and auth_testfixtures")
}

func TestClientIDHeaderTestingFixtureResolvesClientWhenUnset(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting

	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)

	id, err := resolver.Resolve(context.Background(), RequestCredentials{
		ClientIDHeader: "fixture-client",
	})
	require.NoError(t, err)
	require.Empty(t, id.UserID)
	require.Equal(t, "fixture-client", id.ClientID)
	require.Equal(t, CredentialAPIKey, id.CredentialKind)
}
