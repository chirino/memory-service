package bdd

import (
	"context"
	"fmt"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

const OIDCTokenProviderExtraKey = "oidcTokenProvider"

type oidcTokenProvider interface {
	AccessToken(ctx context.Context, username, password string) (string, error)
	EnsureUser(ctx context.Context, username, password string, realmRoles []string) error
}

// oidcTokenProviderWithScopes is an optional extension of oidcTokenProvider that supports
// requesting a token with a specific set of scopes.
type oidcTokenProviderWithScopes interface {
	AccessTokenWithScopes(ctx context.Context, username, password, scopes string) (string, error)
}

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		o := &oidcAuthSteps{s: s}
		ctx.Step(`^I login via OIDC as user "([^"]*)" with password "([^"]*)"$`, o.iLoginViaOIDCAsUserWithPassword)
		ctx.Step(`^I login via OIDC as user "([^"]*)" with scopes "([^"]*)"$`, o.iLoginViaOIDCAsUserWithScopes)
		ctx.Step(`^I attempt OIDC login as user "([^"]*)" with password "([^"]*)"$`, o.iAttemptOIDCLoginAsUserWithPassword)
		ctx.Step(`^OIDC login should fail$`, o.oidcLoginShouldFail)
	})
}

type oidcAuthSteps struct {
	s            *cucumber.TestScenario
	lastLoginErr error
}

func (o *oidcAuthSteps) iLoginViaOIDCAsUserWithPassword(userID, password string) error {
	provider, err := o.provider()
	if err != nil {
		return err
	}

	o.s.RegisterCanonicalUsers(userID)
	isolatedUserID := o.s.IsolatedUser(userID)
	if err := provider.EnsureUser(context.Background(), isolatedUserID, password, oidcRealmRoles(userID)); err != nil {
		return fmt.Errorf("OIDC provision failed for user %q: %w", isolatedUserID, err)
	}

	token, err := provider.AccessToken(context.Background(), isolatedUserID, password)
	if err != nil {
		return fmt.Errorf("OIDC login failed for user %q: %w", userID, err)
	}

	o.lastLoginErr = nil
	o.setUserToken(userID, token)
	session := o.s.Session()
	session.Header.Del("Authorization")
	session.Header.Del("X-Client-ID")
	return nil
}

// iLoginViaOIDCAsUserWithScopes logs in via OIDC with a specific set of space-separated scopes.
// The user's password is assumed to equal their username (as set by EnsureUser).
// Scopes are passed directly to the token endpoint; the provider must support
// AccessTokenWithScopes (the Keycloak test server does).
func (o *oidcAuthSteps) iLoginViaOIDCAsUserWithScopes(userID, scopes string) error {
	provider, err := o.provider()
	if err != nil {
		return err
	}
	scopedProvider, ok := provider.(oidcTokenProviderWithScopes)
	if !ok {
		return fmt.Errorf("OIDC token provider does not support AccessTokenWithScopes; got %T", provider)
	}

	o.s.RegisterCanonicalUsers(userID)
	isolatedUserID := o.s.IsolatedUser(userID)
	if err := provider.EnsureUser(context.Background(), isolatedUserID, userID, oidcRealmRoles(userID)); err != nil {
		return fmt.Errorf("OIDC provision failed for user %q: %w", isolatedUserID, err)
	}

	token, err := scopedProvider.AccessTokenWithScopes(context.Background(), isolatedUserID, userID, scopes)
	if err != nil {
		return fmt.Errorf("OIDC scoped login failed for user %q scopes %q: %w", userID, scopes, err)
	}

	o.lastLoginErr = nil
	o.setUserToken(userID, token)
	session := o.s.Session()
	session.Header.Del("Authorization")
	session.Header.Del("X-Client-ID")
	return nil
}

func (o *oidcAuthSteps) iAttemptOIDCLoginAsUserWithPassword(userID, password string) error {
	provider, err := o.provider()
	if err != nil {
		return err
	}

	o.s.RegisterCanonicalUsers(userID)
	isolatedUserID := o.s.IsolatedUser(userID)
	token, loginErr := provider.AccessToken(context.Background(), isolatedUserID, password)
	o.lastLoginErr = loginErr
	if loginErr == nil && token != "" {
		o.setUserToken(userID, token)
	}
	return nil
}

func (o *oidcAuthSteps) oidcLoginShouldFail() error {
	if o.lastLoginErr == nil {
		return fmt.Errorf("expected OIDC login to fail, but it succeeded")
	}
	return nil
}

func (o *oidcAuthSteps) provider() (oidcTokenProvider, error) {
	raw := o.s.Extra[OIDCTokenProviderExtraKey]
	if raw == nil {
		return nil, fmt.Errorf("OIDC token provider not configured in suite extra %q", OIDCTokenProviderExtraKey)
	}
	provider, ok := raw.(oidcTokenProvider)
	if !ok {
		return nil, fmt.Errorf("suite extra %q has unexpected type %T", OIDCTokenProviderExtraKey, raw)
	}
	return provider, nil
}

func (o *oidcAuthSteps) setUserToken(userID, token string) {
	o.s.Suite.Mu.Lock()
	user := o.s.Users[userID]
	if user == nil {
		user = &cucumber.TestUser{Name: userID}
		o.s.Users[userID] = user
	}
	user.Subject = token
	o.s.Suite.Mu.Unlock()
	o.s.CurrentUser = userID
}

func oidcRealmRoles(userID string) []string {
	switch userID {
	case "alice":
		return []string{"user", "admin"}
	case "charlie":
		return []string{"user", "auditor"}
	default:
		return []string{"user"}
	}
}
