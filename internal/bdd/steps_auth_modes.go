package bdd

import (
	"context"
	"fmt"
	"strings"

	"github.com/chirino/memory-service/internal/cmd/serve"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/security"
	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
)

type oidcProviderInfo interface {
	GetIssuerURL() string
	GetDiscoveryURL() string
}

// parseResourceScopeTable reads a two-column Gherkin table (| permission | scopes |)
// into the fixed OIDC permission-scope config map.
func parseResourceScopeTable(table *godog.Table) (map[string]string, error) {
	valid := map[string]bool{}
	for _, desc := range security.PermissionDescriptors() {
		valid[string(desc.Permission)] = true
	}
	scopes := map[string]string{}
	for i, row := range table.Rows {
		if len(row.Cells) != 2 {
			return nil, fmt.Errorf("resource scopes table must have exactly 2 columns (permission, scopes), got %d", len(row.Cells))
		}
		permission := strings.TrimSpace(strings.ToLower(row.Cells[0].Value))
		value := strings.TrimSpace(row.Cells[1].Value)
		if i == 0 && permission == "permission" && strings.EqualFold(value, "scopes") {
			continue
		}
		if !valid[permission] {
			return nil, fmt.Errorf("unknown OIDC resource scope permission %q", permission)
		}
		scopes[permission] = value
	}
	return scopes, nil
}

// AuthModeDBURLKey is the extra key for the shared database URL used by auth-mode scenario servers.
const AuthModeDBURLKey = "authModeDBURL"

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		am := &authModeSteps{s: s}
		// Server setup steps
		ctx.Step(`^memory-service is running with OIDC and no allowed client or audience filters$`, am.memoryServiceWithOIDCNoClientOrAudienceFilters)
		ctx.Step(`^memory-service is running with OIDC allowed client "([^"]*)"$`, am.memoryServiceWithOIDCAllowedClient)
		ctx.Step(`^memory-service is running with OIDC allowed clients "([^"]*)"$`, am.memoryServiceWithOIDCAllowedClients)
		ctx.Step(`^memory-service is running with OIDC allowed client "([^"]*)" and API keys$`, am.memoryServiceWithOIDCAllowedClientAndAPIKeys)
		ctx.Step(`^memory-service is running with OIDC allowed client "([^"]*)" and resource scopes:$`, am.memoryServiceWithOIDCAllowedClientAndResourceScopes)
		ctx.Step(`^memory-service is running with OIDC allowed client "([^"]*)" and API keys and resource scopes:$`, am.memoryServiceWithOIDCAllowedClientAndAPIKeysAndResourceScopes)
		ctx.Step(`^memory-service is running with OIDC allowed audience "([^"]*)"$`, am.memoryServiceWithOIDCAllowedAudience)
		ctx.Step(`^memory-service is running with OIDC allowed client "([^"]*)" and allowed audience "([^"]*)"$`, am.memoryServiceWithOIDCAllowedClientAndAudience)
		ctx.Step(`^memory-service is running with API keys and no OIDC$`, am.memoryServiceWithAPIKeysNoOIDC)
		ctx.Step(`^memory-service is running with API keys and embedded MCP identity configured$`, am.memoryServiceWithAPIKeysAndEmbeddedMCPConfigured)
		// API key and client role configuration steps
		ctx.Step(`^API key "([^"]*)" maps to client "([^"]*)"$`, am.apiKeyMapsToClient)
		ctx.Step(`^client "([^"]*)" has the "([^"]*)" role$`, am.clientHasRole)
		// Credential steps
		ctx.Step(`^I authenticate with API key header "([^"]*)"$`, am.authenticateWithAPIKeyHeader)
		ctx.Step(`^I authenticate with bearer API key "([^"]*)"$`, am.authenticateWithBearerAPIKey)
		ctx.Step(`^I authenticate as bearer user "([^"]*)" with API key "([^"]*)"$`, am.authenticateAsBearerUserWithAPIKey)
		ctx.Step(`^I authenticate with both OIDC user "([^"]*)" and API key "([^"]*)"$`, am.authenticateWithOIDCUserAndAPIKey)
		ctx.Step(`^I authenticate with only API key header "([^"]*)"$`, am.authenticateWithOnlyAPIKeyHeader)

		ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
			am.cleanup()
			return ctx, nil
		})
	})
}

// authModeSteps manages per-scenario memory-service instances for auth-mode BDD scenarios.
type authModeSteps struct {
	s           *cucumber.TestScenario
	shutdown    func()
	resolver    *security.TokenResolver // non-nil after startServer
	apiKeys     map[string]string       // key → clientID (configured for this scenario's server)
	clientRoles map[string]string       // clientID → role
}

func (am *authModeSteps) cleanup() {
	if am.shutdown != nil {
		am.shutdown()
		am.shutdown = nil
	}
}

// buildBaseConfig returns a base config shared by all auth-mode scenario servers.
func (am *authModeSteps) buildBaseConfig() (config.Config, error) {
	dbURL, _ := am.s.Extra[AuthModeDBURLKey].(string)
	if dbURL == "" {
		return config.Config{}, fmt.Errorf("auth mode DB URL not set in suite extra key %q; configure in test runner", AuthModeDBURLKey)
	}
	cfg := config.DefaultConfig()
	cfg.Mode = config.ModeProd
	cfg.DBURL = dbURL
	cfg.CacheType = "none"
	cfg.AttachType = "db"
	cfg.SearchSemanticEnabled = false
	cfg.SearchFulltextEnabled = false
	cfg.Listener.Port = 0
	cfg.Listener.EnableTLS = false
	return cfg, nil
}

func (am *authModeSteps) buildOIDCConfig() (config.Config, error) {
	provider, ok := am.s.Extra[OIDCTokenProviderExtraKey]
	if !ok {
		return config.Config{}, fmt.Errorf("OIDC token provider not set in suite extra key %q; this step requires a Keycloak runner", OIDCTokenProviderExtraKey)
	}
	kc, ok := provider.(oidcProviderInfo)
	if !ok {
		return config.Config{}, fmt.Errorf("OIDC token provider does not implement oidcProviderInfo (GetIssuerURL/GetDiscoveryURL); got %T", provider)
	}

	cfg, err := am.buildBaseConfig()
	if err != nil {
		return config.Config{}, err
	}
	cfg.OIDCIssuer = kc.GetIssuerURL()
	cfg.OIDCDiscoveryURL = kc.GetDiscoveryURL()
	cfg.OIDCAllowedAudiences = "memory-service"
	cfg.AdminOIDCRole = "admin"
	cfg.AuditorOIDCRole = "auditor"
	cfg.IndexerOIDCRole = "indexer"
	// Grant admin to alice (she has admin realm role in Keycloak).
	cfg.AdminUsers = am.s.IsolatedUser("alice")
	return cfg, nil
}

// startServer starts a memory-service with the given config and updates the scenario's APIURL.
func (am *authModeSteps) startServer(cfg *config.Config) error {
	am.cleanup()
	ctx := config.WithContext(context.Background(), cfg)
	srv, err := serve.StartServer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start auth-mode scenario server: %w", err)
	}
	am.s.APIURL = fmt.Sprintf("http://localhost:%d", srv.Running.Port)
	am.s.Extra["grpcAddr"] = fmt.Sprintf("localhost:%d", srv.Running.Port)
	am.resolver = serve.GetTokenResolver(srv)
	am.shutdown = func() { _ = srv.Shutdown(context.Background()) }
	return nil
}

// resolveClientRoles returns admin/auditor/indexer user/client role strings for the current scenario.
func (am *authModeSteps) applyClientRoles(cfg *config.Config) {
	var adminClients, auditorClients, indexerClients []string
	for clientID, role := range am.clientRoles {
		switch strings.ToLower(role) {
		case "admin":
			adminClients = append(adminClients, clientID)
		case "auditor":
			auditorClients = append(auditorClients, clientID)
		case "indexer":
			indexerClients = append(indexerClients, clientID)
		}
	}
	if len(adminClients) > 0 {
		cfg.AdminClients = strings.Join(adminClients, ",")
	}
	if len(auditorClients) > 0 {
		cfg.AuditorClients = strings.Join(auditorClients, ",")
	}
	if len(indexerClients) > 0 {
		cfg.IndexerClients = strings.Join(indexerClients, ",")
	}
}

// apiKeyMapsToClient registers an API key→clientID mapping for the next server start.
func (am *authModeSteps) apiKeyMapsToClient(apiKey, clientID string) error {
	if am.apiKeys == nil {
		am.apiKeys = map[string]string{}
	}
	am.apiKeys[apiKey] = clientID
	return nil
}

// clientHasRole records a role for a client for the next server start.
func (am *authModeSteps) clientHasRole(clientID, role string) error {
	if am.clientRoles == nil {
		am.clientRoles = map[string]string{}
	}
	am.clientRoles[clientID] = role
	return nil
}

// --- Server setup steps ---

func (am *authModeSteps) memoryServiceWithOIDCNoClientOrAudienceFilters() error {
	return am.startOIDCAudienceServer("", "")
}

func (am *authModeSteps) memoryServiceWithOIDCAllowedClient(clientID string) error {
	return am.memoryServiceWithOIDCAllowedClients(clientID)
}

func (am *authModeSteps) memoryServiceWithOIDCAllowedClients(clients string) error {
	return am.startOIDCServer(clients, false)
}

func (am *authModeSteps) memoryServiceWithOIDCAllowedClientAndAPIKeys(clientID string) error {
	return am.startOIDCServer(clientID, true)
}

func (am *authModeSteps) startOIDCServer(allowedClients string, withAPIKeys bool) error {
	cfg, err := am.buildOIDCConfig()
	if err != nil {
		return err
	}
	cfg.OIDCAllowedClients = allowedClients

	if withAPIKeys {
		cfg.APIKeys = copyMap(am.apiKeys)
		am.applyClientRoles(&cfg)
	}

	return am.startServer(&cfg)
}

// memoryServiceWithOIDCAllowedAudience starts a memory-service with OIDC configured for
// audience-only enforcement: no OIDCAllowedClients, only OIDCAllowedAudiences. Tokens whose
// aud claim contains the configured audience are accepted; all others are rejected.
func (am *authModeSteps) memoryServiceWithOIDCAllowedAudience(audience string) error {
	return am.startOIDCAudienceServer("", audience)
}

// memoryServiceWithOIDCAllowedClientAndAudience starts a memory-service with OIDC configured
// for both client and audience enforcement. Both constraints must pass.
func (am *authModeSteps) memoryServiceWithOIDCAllowedClientAndAudience(clientID, audience string) error {
	return am.startOIDCAudienceServer(clientID, audience)
}

// startOIDCAudienceServer starts an OIDC server with the given allowed clients and audiences.
// Either parameter may be empty to omit that filter.
func (am *authModeSteps) startOIDCAudienceServer(allowedClients, allowedAudiences string) error {
	cfg, err := am.buildOIDCConfig()
	if err != nil {
		return err
	}
	cfg.OIDCAllowedClients = allowedClients
	cfg.OIDCAllowedAudiences = allowedAudiences
	cfg.OIDCAllowMissingAudience = strings.TrimSpace(allowedAudiences) == ""

	return am.startServer(&cfg)
}

// memoryServiceWithOIDCAllowedClientAndResourceScopes starts a memory-service with OIDC and
// resource/API scope gates defined by the given table (two columns: permission, scopes).
func (am *authModeSteps) memoryServiceWithOIDCAllowedClientAndResourceScopes(clientID string, table *godog.Table) error {
	scopes, err := parseResourceScopeTable(table)
	if err != nil {
		return err
	}

	cfg, err := am.buildOIDCConfig()
	if err != nil {
		return err
	}
	cfg.OIDCAllowedClients = clientID
	cfg.OIDCScopes = scopes

	return am.startServer(&cfg)
}

func (am *authModeSteps) memoryServiceWithOIDCAllowedClientAndAPIKeysAndResourceScopes(clientID string, table *godog.Table) error {
	scopes, err := parseResourceScopeTable(table)
	if err != nil {
		return err
	}

	cfg, err := am.buildOIDCConfig()
	if err != nil {
		return err
	}
	cfg.OIDCAllowedClients = clientID
	cfg.APIKeys = copyMap(am.apiKeys)
	cfg.OIDCScopes = scopes
	am.applyClientRoles(&cfg)

	return am.startServer(&cfg)
}

func (am *authModeSteps) memoryServiceWithAPIKeysNoOIDC() error {
	cfg, err := am.buildBaseConfig()
	if err != nil {
		return err
	}
	cfg.APIKeys = copyMap(am.apiKeys)
	am.applyClientRoles(&cfg)
	return am.startServer(&cfg)
}

// memoryServiceWithAPIKeysAndEmbeddedMCPConfigured starts a server with API keys AND the
// embedded MCP synthetic identity wired in (as the embedded MCP command does). The server
// is still exposed over normal HTTP so the test can confirm that the X-Embedded-MCP-Transport
// header is not accepted from network traffic.
func (am *authModeSteps) memoryServiceWithAPIKeysAndEmbeddedMCPConfigured() error {
	cfg, err := am.buildBaseConfig()
	if err != nil {
		return err
	}
	cfg.APIKeys = copyMap(am.apiKeys)
	am.applyClientRoles(&cfg)
	// Add the synthetic embedded MCP API key and grant it admin so the embedded path would
	// succeed in-process — this makes the test meaningful: even when ConfigureEmbeddedMCP is
	// set, remote HTTP with only the marker header must still return 401.
	if cfg.APIKeys == nil {
		cfg.APIKeys = map[string]string{}
	}
	cfg.APIKeys["embedded-mcp-api-key"] = "embedded-mcp"
	if cfg.AdminClients == "" {
		cfg.AdminClients = "embedded-mcp"
	} else {
		cfg.AdminClients += ",embedded-mcp"
	}

	if err := am.startServer(&cfg); err != nil {
		return err
	}

	// Wire ConfigureEmbeddedMCP so the resolver has the embedded identity configured.
	// The server is running over plain HTTP, not the in-process transport, so the context
	// key can never be set by a remote caller.
	if am.resolver != nil {
		am.resolver.ConfigureEmbeddedMCP("embedded-mcp-user", "embedded-mcp")
	}
	return nil
}

// --- Credential steps ---

func (am *authModeSteps) authenticateWithAPIKeyHeader(apiKey string) error {
	session := am.s.Session()
	session.Header.Del("Authorization")
	session.Header.Set("X-API-Key", apiKey)
	am.s.CurrentUser = ""
	return nil
}

func (am *authModeSteps) authenticateWithOnlyAPIKeyHeader(apiKey string) error {
	session := am.s.Session()
	session.Header.Del("Authorization")
	session.Header.Set("X-API-Key", apiKey)
	session.TestUser = nil
	am.s.CurrentUser = ""
	return nil
}

func (am *authModeSteps) authenticateWithBearerAPIKey(apiKey string) error {
	session := am.s.Session()
	session.Header.Set("Authorization", "Bearer "+apiKey)
	session.Header.Del("X-API-Key")
	session.TestUser = nil
	am.s.CurrentUser = ""
	return nil
}

func (am *authModeSteps) authenticateAsBearerUserWithAPIKey(userID, apiKey string) error {
	session := am.s.Session()
	session.Header.Set("Authorization", "Bearer "+userID)
	session.Header.Set("X-API-Key", apiKey)
	session.TestUser = nil
	am.s.CurrentUser = ""
	return nil
}

func (am *authModeSteps) authenticateWithOIDCUserAndAPIKey(userID, apiKey string) error {
	// First login via OIDC to get a JWT.
	o := &oidcAuthSteps{s: am.s}
	if err := o.iLoginViaOIDCAsUserWithPassword(userID, userID); err != nil {
		return err
	}
	// Then add the API key header alongside.
	session := am.s.Session()
	session.Header.Set("X-API-Key", apiKey)
	return nil
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
