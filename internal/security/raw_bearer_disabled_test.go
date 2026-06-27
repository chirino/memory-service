//go:build !auth_testfixtures

package security

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRawBearerDefaultBuildRejectsTestingModeUserAssertion(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting

	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), RequestCredentials{
		BearerToken: "alice",
	})
	require.ErrorContains(t, err, "raw bearer user assertions are not accepted in production builds")
}

func TestClientIDHeaderDefaultBuildDoesNotAuthenticateTestingModeClient(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeTesting

	resolver, err := NewTokenResolver(&cfg)
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), RequestCredentials{
		ClientIDHeader: "forged-client",
	})
	require.ErrorContains(t, err, "missing credentials")
}
