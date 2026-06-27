//go:build !auth_testfixtures

package security

import (
	"context"
	"errors"
	"fmt"
)

// resolveRawBearer always rejects raw bearer user tokens in production/default builds.
// Raw bearer user fixture support requires both the auth_testfixtures build tag and testing mode.
func resolveRawBearer(_ *TokenResolver, _ context.Context, bearerToken, _ string, _ map[string]bool) (*Identity, error) {
	if bearerToken == "" {
		return nil, errors.New("missing credentials")
	}
	return nil, fmt.Errorf("invalid credentials: raw bearer user assertions are not accepted in production builds")
}
