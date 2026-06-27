//go:build auth_testfixtures

package security

import (
	"context"
	"errors"
	"fmt"
)

// resolveRawBearer accepts raw bearer user tokens when the auth_testfixtures build tag is active
// and the resolver is in testing mode. This is used by BDD test fixtures.
func resolveRawBearer(r *TokenResolver, _ context.Context, bearerToken, clientID string, roles map[string]bool) (*Identity, error) {
	if !r.testingMode {
		return nil, fmt.Errorf("invalid credentials: raw bearer user assertions require both testing mode and auth_testfixtures build tag")
	}

	if bearerToken == "" {
		return nil, errors.New("missing credentials")
	}

	// In testing mode: treat the bearer token as the user ID directly.
	userID := bearerToken

	r.addUserRoles(roles, userID)
	r.addClientRoles(roles, clientID)
	applyAdminImplication(roles)

	return newIdentity(userID, clientID, roles, CredentialTesting), nil
}
