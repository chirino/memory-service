package security

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// ContextKeyUserID is the gin context key for the authenticated user ID.
	ContextKeyUserID = "userID"
	// ContextKeyClientID is the gin context key for the agent client ID.
	ContextKeyClientID = "clientID"
	// ContextKeyRoles is the gin context key for resolved caller roles.
	ContextKeyRoles = "roles"
	// ContextKeyIsAdmin is the gin context key for admin authorization.
	ContextKeyIsAdmin = "isAdmin"
	// ContextKeyIdentity is the gin context key for the resolved caller identity.
	ContextKeyIdentity = "identity"
)

const (
	RoleAdmin   = "admin"
	RoleAuditor = "auditor"
	RoleIndexer = "indexer"
)

// CredentialKind describes the accepted credential combination for an identity.
type CredentialKind string

const (
	// CredentialOIDC — OIDC JWT bearer token only.
	CredentialOIDC CredentialKind = "oidc"
	// CredentialAPIKey — X-API-Key only (client-only service principal).
	CredentialAPIKey CredentialKind = "api-key"
	// CredentialOIDCAPIKey — OIDC JWT bearer token paired with X-API-Key.
	CredentialOIDCAPIKey CredentialKind = "oidc-api-key"
	// CredentialBearerAPIKey — bearer API key compatibility for no-OIDC deployments.
	CredentialBearerAPIKey CredentialKind = "bearer-api-key"
	// CredentialEmbeddedMCP — in-process embedded MCP synthetic identity.
	// Accepted only when RequestCredentials.Transport == "embedded-mcp"; never from network traffic.
	CredentialEmbeddedMCP     CredentialKind = "embedded-mcp"
	CredentialLocalUnixSocket CredentialKind = "local-unix-socket"
	// CredentialTesting — raw bearer user accepted only in testing mode with auth_testfixtures build tag.
	CredentialTesting CredentialKind = "testing-bearer-user"
)

// Identity holds the resolved caller identity from a bearer token.
type Identity struct {
	UserID         string
	ClientID       string
	Roles          map[string]bool
	IsAdmin        bool
	CredentialKind CredentialKind
	HasOIDCToken   bool
	OIDCScopes     map[string]bool
	OIDCScopeGates map[Permission]map[string]bool
}

// Permission is a fixed Memory Service API capability that can be additionally
// gated by configured OIDC token scopes.
type Permission string

const (
	PermissionUser                    Permission = "user"
	PermissionUserRead                Permission = "user_read"
	PermissionUserWrite               Permission = "user_write"
	PermissionAdmin                   Permission = "admin"
	PermissionAdminRead               Permission = "admin_read"
	PermissionAdminWrite              Permission = "admin_write"
	PermissionSystemRead              Permission = "system_read"
	PermissionConversations           Permission = "conversations"
	PermissionConversationsRead       Permission = "conversations_read"
	PermissionConversationsWrite      Permission = "conversations_write"
	PermissionSharing                 Permission = "sharing"
	PermissionSharingRead             Permission = "sharing_read"
	PermissionSharingWrite            Permission = "sharing_write"
	PermissionSearch                  Permission = "search"
	PermissionSearchRead              Permission = "search_read"
	PermissionSearchWrite             Permission = "search_write"
	PermissionMemories                Permission = "memories"
	PermissionMemoriesRead            Permission = "memories_read"
	PermissionMemoriesWrite           Permission = "memories_write"
	PermissionAttachments             Permission = "attachments"
	PermissionAttachmentsRead         Permission = "attachments_read"
	PermissionAttachmentsWrite        Permission = "attachments_write"
	PermissionEventsRead              Permission = "events_read"
	PermissionRecordings              Permission = "recordings"
	PermissionRecordingsRead          Permission = "recordings_read"
	PermissionRecordingsWrite         Permission = "recordings_write"
	PermissionAdminConversations      Permission = "admin_conversations"
	PermissionAdminConversationsRead  Permission = "admin_conversations_read"
	PermissionAdminConversationsWrite Permission = "admin_conversations_write"
	PermissionAdminMemories           Permission = "admin_memories"
	PermissionAdminMemoriesRead       Permission = "admin_memories_read"
	PermissionAdminMemoriesWrite      Permission = "admin_memories_write"
	PermissionAdminAttachments        Permission = "admin_attachments"
	PermissionAdminAttachmentsRead    Permission = "admin_attachments_read"
	PermissionAdminAttachmentsWrite   Permission = "admin_attachments_write"
	PermissionAdminEventsRead         Permission = "admin_events_read"
	PermissionAdminCheckpoints        Permission = "admin_checkpoints"
	PermissionAdminCheckpointsRead    Permission = "admin_checkpoints_read"
	PermissionAdminCheckpointsWrite   Permission = "admin_checkpoints_write"
	PermissionAdminStatsRead          Permission = "admin_stats_read"
	PermissionAdminMaintenanceWrite   Permission = "admin_maintenance_write"
)

// PermissionDescriptor describes the explicit flag/env mapping for one fixed permission key.
type PermissionDescriptor struct {
	Permission Permission
	FlagName   string
	EnvVar     string
	Usage      string
}

var permissionDescriptors = []PermissionDescriptor{
	{PermissionUser, "oidc-scopes-user", "MEMORY_SERVICE_OIDC_SCOPES_USER", "Comma-separated OIDC scopes accepted for all user API reads and writes"},
	{PermissionUserRead, "oidc-scopes-user-read", "MEMORY_SERVICE_OIDC_SCOPES_USER_READ", "Comma-separated OIDC scopes accepted for all user API reads"},
	{PermissionUserWrite, "oidc-scopes-user-write", "MEMORY_SERVICE_OIDC_SCOPES_USER_WRITE", "Comma-separated OIDC scopes accepted for all user API writes"},
	{PermissionAdmin, "oidc-scopes-admin", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN", "Comma-separated OIDC scopes accepted for all admin API reads and writes"},
	{PermissionAdminRead, "oidc-scopes-admin-read", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_READ", "Comma-separated OIDC scopes accepted for all admin API reads"},
	{PermissionAdminWrite, "oidc-scopes-admin-write", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_WRITE", "Comma-separated OIDC scopes accepted for all admin API writes"},
	{PermissionSystemRead, "oidc-scopes-system-read", "MEMORY_SERVICE_OIDC_SCOPES_SYSTEM_READ", "Comma-separated OIDC scopes required for authenticated system reads"},
	{PermissionConversations, "oidc-scopes-conversations", "MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS", "Comma-separated OIDC scopes accepted for conversation reads and writes"},
	{PermissionConversationsRead, "oidc-scopes-conversations-read", "MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS_READ", "Comma-separated OIDC scopes required for conversation reads"},
	{PermissionConversationsWrite, "oidc-scopes-conversations-write", "MEMORY_SERVICE_OIDC_SCOPES_CONVERSATIONS_WRITE", "Comma-separated OIDC scopes required for conversation writes"},
	{PermissionSharing, "oidc-scopes-sharing", "MEMORY_SERVICE_OIDC_SCOPES_SHARING", "Comma-separated OIDC scopes accepted for sharing reads and writes"},
	{PermissionSharingRead, "oidc-scopes-sharing-read", "MEMORY_SERVICE_OIDC_SCOPES_SHARING_READ", "Comma-separated OIDC scopes required for sharing reads"},
	{PermissionSharingWrite, "oidc-scopes-sharing-write", "MEMORY_SERVICE_OIDC_SCOPES_SHARING_WRITE", "Comma-separated OIDC scopes required for sharing writes"},
	{PermissionSearch, "oidc-scopes-search", "MEMORY_SERVICE_OIDC_SCOPES_SEARCH", "Comma-separated OIDC scopes accepted for search reads and indexing writes"},
	{PermissionSearchRead, "oidc-scopes-search-read", "MEMORY_SERVICE_OIDC_SCOPES_SEARCH_READ", "Comma-separated OIDC scopes required for search reads"},
	{PermissionSearchWrite, "oidc-scopes-search-write", "MEMORY_SERVICE_OIDC_SCOPES_SEARCH_WRITE", "Comma-separated OIDC scopes required for search/index writes"},
	{PermissionMemories, "oidc-scopes-memories", "MEMORY_SERVICE_OIDC_SCOPES_MEMORIES", "Comma-separated OIDC scopes accepted for memory reads and writes"},
	{PermissionMemoriesRead, "oidc-scopes-memories-read", "MEMORY_SERVICE_OIDC_SCOPES_MEMORIES_READ", "Comma-separated OIDC scopes required for memory reads"},
	{PermissionMemoriesWrite, "oidc-scopes-memories-write", "MEMORY_SERVICE_OIDC_SCOPES_MEMORIES_WRITE", "Comma-separated OIDC scopes required for memory writes"},
	{PermissionAttachments, "oidc-scopes-attachments", "MEMORY_SERVICE_OIDC_SCOPES_ATTACHMENTS", "Comma-separated OIDC scopes accepted for attachment reads and writes"},
	{PermissionAttachmentsRead, "oidc-scopes-attachments-read", "MEMORY_SERVICE_OIDC_SCOPES_ATTACHMENTS_READ", "Comma-separated OIDC scopes required for attachment reads"},
	{PermissionAttachmentsWrite, "oidc-scopes-attachments-write", "MEMORY_SERVICE_OIDC_SCOPES_ATTACHMENTS_WRITE", "Comma-separated OIDC scopes required for attachment writes"},
	{PermissionEventsRead, "oidc-scopes-events-read", "MEMORY_SERVICE_OIDC_SCOPES_EVENTS_READ", "Comma-separated OIDC scopes required for user event streams"},
	{PermissionRecordings, "oidc-scopes-recordings", "MEMORY_SERVICE_OIDC_SCOPES_RECORDINGS", "Comma-separated OIDC scopes accepted for response recording reads and writes"},
	{PermissionRecordingsRead, "oidc-scopes-recordings-read", "MEMORY_SERVICE_OIDC_SCOPES_RECORDINGS_READ", "Comma-separated OIDC scopes required for response recording reads"},
	{PermissionRecordingsWrite, "oidc-scopes-recordings-write", "MEMORY_SERVICE_OIDC_SCOPES_RECORDINGS_WRITE", "Comma-separated OIDC scopes required for response recording writes"},
	{PermissionAdminConversations, "oidc-scopes-admin-conversations", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CONVERSATIONS", "Comma-separated OIDC scopes accepted for admin conversation reads and writes"},
	{PermissionAdminConversationsRead, "oidc-scopes-admin-conversations-read", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CONVERSATIONS_READ", "Comma-separated OIDC scopes required for admin conversation reads"},
	{PermissionAdminConversationsWrite, "oidc-scopes-admin-conversations-write", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CONVERSATIONS_WRITE", "Comma-separated OIDC scopes required for admin conversation writes"},
	{PermissionAdminMemories, "oidc-scopes-admin-memories", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_MEMORIES", "Comma-separated OIDC scopes accepted for admin memory reads and writes"},
	{PermissionAdminMemoriesRead, "oidc-scopes-admin-memories-read", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_MEMORIES_READ", "Comma-separated OIDC scopes required for admin memory reads"},
	{PermissionAdminMemoriesWrite, "oidc-scopes-admin-memories-write", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_MEMORIES_WRITE", "Comma-separated OIDC scopes required for admin memory writes"},
	{PermissionAdminAttachments, "oidc-scopes-admin-attachments", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_ATTACHMENTS", "Comma-separated OIDC scopes accepted for admin attachment reads and writes"},
	{PermissionAdminAttachmentsRead, "oidc-scopes-admin-attachments-read", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_ATTACHMENTS_READ", "Comma-separated OIDC scopes required for admin attachment reads"},
	{PermissionAdminAttachmentsWrite, "oidc-scopes-admin-attachments-write", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_ATTACHMENTS_WRITE", "Comma-separated OIDC scopes required for admin attachment writes"},
	{PermissionAdminEventsRead, "oidc-scopes-admin-events-read", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_EVENTS_READ", "Comma-separated OIDC scopes required for admin event streams"},
	{PermissionAdminCheckpoints, "oidc-scopes-admin-checkpoints", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CHECKPOINTS", "Comma-separated OIDC scopes accepted for admin checkpoint reads and writes"},
	{PermissionAdminCheckpointsRead, "oidc-scopes-admin-checkpoints-read", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CHECKPOINTS_READ", "Comma-separated OIDC scopes required for admin checkpoint reads"},
	{PermissionAdminCheckpointsWrite, "oidc-scopes-admin-checkpoints-write", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_CHECKPOINTS_WRITE", "Comma-separated OIDC scopes required for admin checkpoint writes"},
	{PermissionAdminStatsRead, "oidc-scopes-admin-stats-read", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_STATS_READ", "Comma-separated OIDC scopes required for admin stats reads"},
	{PermissionAdminMaintenanceWrite, "oidc-scopes-admin-maintenance-write", "MEMORY_SERVICE_OIDC_SCOPES_ADMIN_MAINTENANCE_WRITE", "Comma-separated OIDC scopes required for admin maintenance writes"},
}

// PermissionDescriptors returns the fixed OIDC scope permission vocabulary.
func PermissionDescriptors() []PermissionDescriptor {
	return slices.Clone(permissionDescriptors)
}

// grpcIdentityKey is the context key for storing Identity in gRPC contexts.
type grpcIdentityKey struct{}

// IdentityFromContext retrieves the Identity stored in a context by the gRPC interceptor.
func IdentityFromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(grpcIdentityKey{}).(*Identity)
	return id
}

// embeddedMCPContextKey is unexported so it can only be set by WithEmbeddedMCPContext, which
// is only called by the in-process handlerTransport in internal/cmd/mcp. Remote HTTP/gRPC
// callers cannot set an unexported context key, making this an unforgeable in-process signal.
type embeddedMCPContextKey struct{}

// EmbeddedMCPTransport is the string value stored in the context by WithEmbeddedMCPContext.
// Exported only so internal/cmd/mcp can reference it as documentation; the actual trust gate
// is the unexported context key, not this string.
const EmbeddedMCPTransport = "embedded-mcp"

// WithEmbeddedMCPContext stamps a request context as coming from the in-process embedded MCP
// transport. It must be called only by the in-process handlerTransport.RoundTrip.
// Remote callers cannot replicate this because embeddedMCPContextKey is unexported.
func WithEmbeddedMCPContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, embeddedMCPContextKey{}, EmbeddedMCPTransport)
}

// isEmbeddedMCPRequest returns true if the context was stamped by WithEmbeddedMCPContext.
func isEmbeddedMCPRequest(ctx context.Context) bool {
	v, _ := ctx.Value(embeddedMCPContextKey{}).(string)
	return v == EmbeddedMCPTransport
}

// RequestCredentials is the transport-neutral credential shape extracted from HTTP or gRPC.
type RequestCredentials struct {
	// BearerToken is the value after stripping "Bearer " from Authorization header.
	BearerToken string
	// APIKey is the X-API-Key header value.
	APIKey string
	// ClientIDHeader is the X-Client-ID header value (testing/dev compatibility only).
	ClientIDHeader string
	// Transport, when set to EmbeddedMCPTransport, causes the resolver to produce a
	// CredentialEmbeddedMCP identity using the resolver's embedded user/client config.
	// Set only by AuthMiddleware after reading isEmbeddedMCPRequest; never from headers.
	Transport string
}

// TokenResolver resolves bearer tokens to caller identities. It is initialized once at startup
// and shared by both the HTTP middleware and gRPC interceptors.
type TokenResolver struct {
	verifier        *oidc.IDTokenVerifier
	oidcEnabled     bool
	allowedClients  map[string]bool // empty = no client check
	allowedAudience map[string]bool // empty = no audience check
	roleClaims      []string
	apiKeys         map[string]string
	adminOIDCRole   string
	auditorOIDCRole string
	indexerOIDCRole string
	oidcScopes      map[Permission]map[string]bool
	adminUsers      map[string]bool
	auditorUsers    map[string]bool
	indexerUsers    map[string]bool
	adminClients    map[string]bool
	auditorClients  map[string]bool
	indexerClients  map[string]bool
	testingMode     bool
	// embeddedMCPUserID and embeddedMCPClientID are set only for embedded MCP servers.
	// When non-empty, a request carrying Transport == EmbeddedMCPTransport resolves
	// to CredentialEmbeddedMCP without touching the bearer/API-key paths.
	embeddedMCPUserID   string
	embeddedMCPClientID string
	localUserID         string
	localClientID       string
}

// NewTokenResolver creates a TokenResolver from the application config. It performs
// one-time OIDC provider discovery if OIDCIssuer is configured.
// Returns an error if OIDC is configured but discovery fails.
func NewTokenResolver(cfg *config.Config) (*TokenResolver, error) {
	var verifier *oidc.IDTokenVerifier
	oidcEnabled := false
	oidcIssuer := cfg.OIDCIssuer

	// Fail closed in production: no auth mechanism configured at all.
	if cfg.Mode != config.ModeTesting && cfg.UnixSocketAuth != "local" && oidcIssuer == "" && len(cfg.APIKeys) == 0 {
		return nil, fmt.Errorf("no authentication mechanism configured: set MEMORY_SERVICE_OIDC_ISSUER and/or MEMORY_SERVICE_API_KEYS_<CLIENT_ID>")
	}

	if oidcIssuer != "" {
		oidcEnabled = true
		if len(splitCSV(cfg.OIDCAllowedAudiences)) == 0 && !cfg.OIDCAllowMissingAudience {
			return nil, fmt.Errorf("OIDC allowed audiences are required when OIDC is enabled; set MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES")
		}
		if cfg.OIDCAllowMissingAudience {
			log.Warn("OIDC audience validation compatibility mode is enabled; configure MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES before production use")
		}

		ctx := context.Background()
		if cfg.OIDCTLSSkipCertificateVerify {
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.TLSClientConfig = &tls.Config{
				InsecureSkipVerify: true, // #nosec G402 - explicitly enabled by OIDC TLS skip-verify config.
			}
			ctx = oidc.ClientContext(ctx, &http.Client{Transport: transport})
		}
		expectedIssuer := oidcIssuer // preserve the configured issuer for token validation
		discoveryURL := cfg.OIDCDiscoveryURL
		if discoveryURL != "" && discoveryURL != oidcIssuer {
			// Discovery URL differs from issuer (e.g. internal Docker hostname vs external URL).
			// NewProvider fetches from its issuer arg, so pass the discovery URL there.
			// InsecureIssuerURLContext tells NewProvider to accept a mismatched issuer in the
			// discovery document.
			ctx = oidc.InsecureIssuerURLContext(ctx, oidcIssuer)
			oidcIssuer = discoveryURL
		}
		provider, err := oidc.NewProvider(ctx, oidcIssuer)
		if err != nil {
			// Fail closed: OIDC configured but provider discovery failed.
			return nil, fmt.Errorf("OIDC provider discovery failed for issuer %q: %w", oidcIssuer, err)
		}

		// When the discovery URL differs from the configured issuer, the provider stores the
		// discovery document's issuer (e.g. the internal hostname). Tokens are issued with the
		// external issuer (cfg.OIDCIssuer). Build the verifier with the expected external issuer
		// so token validation doesn't fail on issuer mismatch.
		var providerClaims struct {
			JWKSURI string `json:"jwks_uri"`
		}
		if expectedIssuer != oidcIssuer {
			if err := provider.Claims(&providerClaims); err == nil && providerClaims.JWKSURI != "" {
				keySet := oidc.NewRemoteKeySet(ctx, providerClaims.JWKSURI)
				verifier = oidc.NewVerifier(expectedIssuer, keySet, &oidc.Config{
					SkipClientIDCheck: true,
				})
			}
		}
		if verifier == nil {
			verifier = provider.Verifier(&oidc.Config{
				SkipClientIDCheck: true,
			})
		}
		log.Info("OIDC auth enabled", "issuer", expectedIssuer)
	}

	adminOIDCRole := strings.TrimSpace(cfg.AdminOIDCRole)
	if adminOIDCRole == "" {
		adminOIDCRole = RoleAdmin
	}
	auditorOIDCRole := strings.TrimSpace(cfg.AuditorOIDCRole)
	if auditorOIDCRole == "" {
		auditorOIDCRole = RoleAuditor
	}
	roleClaims, err := validateRoleClaimPointers(cfg.OIDCRoleClaims)
	if err != nil {
		return nil, err
	}

	return &TokenResolver{
		verifier:        verifier,
		oidcEnabled:     oidcEnabled,
		allowedClients:  splitCSV(cfg.OIDCAllowedClients),
		allowedAudience: splitCSV(cfg.OIDCAllowedAudiences),
		roleClaims:      roleClaims,
		apiKeys:         cfg.APIKeys,
		adminOIDCRole:   adminOIDCRole,
		auditorOIDCRole: auditorOIDCRole,
		indexerOIDCRole: strings.TrimSpace(cfg.IndexerOIDCRole),
		oidcScopes:      parsePermissionScopes(cfg.OIDCScopes),
		adminUsers:      splitCSV(cfg.AdminUsers),
		auditorUsers:    splitCSV(cfg.AuditorUsers),
		indexerUsers:    splitCSV(cfg.IndexerUsers),
		adminClients:    splitCSV(cfg.AdminClients),
		auditorClients:  splitCSV(cfg.AuditorClients),
		indexerClients:  splitCSV(cfg.IndexerClients),
		testingMode:     cfg.Mode == config.ModeTesting,
		localUserID:     strings.TrimSpace(cfg.LocalUserID),
		localClientID:   strings.TrimSpace(cfg.LocalClientID),
	}, nil
}

// ConfigureEmbeddedMCP sets the synthetic user and client identity used by the in-process
// embedded MCP transport. Call this after NewTokenResolver when building an embedded MCP server.
// The resolver will return CredentialEmbeddedMCP for requests carrying Transport == EmbeddedMCPTransport.
func (r *TokenResolver) ConfigureEmbeddedMCP(userID, clientID string) {
	r.embeddedMCPUserID = userID
	r.embeddedMCPClientID = clientID
}

var (
	errInvalidJWT      = errors.New("invalid JWT")
	errMissingIdentity = errors.New("JWT missing identity claims")
)

// Resolve resolves a RequestCredentials into a caller Identity.
// It applies all configured credential policies based on the deployment configuration.
func (r *TokenResolver) Resolve(ctx context.Context, creds RequestCredentials) (*Identity, error) {
	if r.localUserID != "" {
		roles := r.rolesForClient(r.localClientID)
		r.addUserRoles(roles, r.localUserID)
		return newIdentity(r.localUserID, r.localClientID, roles, CredentialLocalUnixSocket), nil
	}
	// Embedded MCP in-process transport: resolve synthetic identity without bearer/API-key paths.
	// The Transport value is injected only by the in-process client; remote listeners strip the
	// header that sets it, so this branch is unreachable from network traffic.
	if creds.Transport == EmbeddedMCPTransport {
		if r.embeddedMCPUserID == "" {
			return nil, fmt.Errorf("embedded MCP transport used but embedded identity not configured")
		}
		clientID := r.embeddedMCPClientID
		roles := r.rolesForClient(clientID)
		return newIdentity(r.embeddedMCPUserID, clientID, roles, CredentialEmbeddedMCP), nil
	}

	bearerToken := creds.BearerToken
	apiKey := creds.APIKey
	clientIDHeader := creds.ClientIDHeader

	roles := map[string]bool{}
	var userID string
	var clientID string

	// Resolve API key to clientID.
	// If X-API-Key is present and does not match any configured key, reject immediately.
	// Silently ignoring an invalid API key would allow a caller that intended to assert
	// a paired client identity to succeed on the OIDC token alone.
	if xAPIKey := strings.TrimSpace(apiKey); xAPIKey != "" {
		if resolved, ok := r.apiKeys[xAPIKey]; ok {
			clientID = resolved
		} else {
			return nil, errors.New("invalid API key")
		}
	}

	clientID = resolveClientIDHeader(r, clientID, clientIDHeader)

	// No bearer token: API-key-only (client service principal).
	if bearerToken == "" {
		if clientID == "" {
			return nil, errors.New("missing credentials")
		}
		roles := r.rolesForClient(clientID)
		return newIdentity("", clientID, roles, CredentialAPIKey), nil
	}

	// Bearer token is present. Check if it looks like a JWT.
	isJWT := strings.Count(bearerToken, ".") >= 2

	// If OIDC is configured, we must validate bearer tokens as JWTs.
	// Non-JWT bearer values are rejected when OIDC is enabled.
	if r.oidcEnabled {
		if !isJWT {
			return nil, fmt.Errorf("%w: non-JWT bearer token rejected (OIDC is enabled)", errInvalidJWT)
		}

		idToken, err := r.verifier.Verify(ctx, bearerToken)
		if err != nil {
			return nil, errors.Join(errInvalidJWT, err)
		}

		// Extract user ID from JWT: prefer "preferred_username" (Quarkus OIDC parity),
		// then "upn", then fall back to "sub".
		var claims struct {
			Sub               string `json:"sub"`
			PreferredUsername string `json:"preferred_username"`
			UPN               string `json:"upn"`
			AZP               string `json:"azp"`       // Keycloak authorized party
			ClientID          string `json:"client_id"` // client-credentials style
			Aud               any    `json:"aud"`
			Scope             string `json:"scope"`
		}
		if err := idToken.Claims(&claims); err != nil {
			return nil, errors.Join(errInvalidJWT, err)
		}

		userID = claims.PreferredUsername
		if userID == "" {
			userID = claims.UPN
		}
		if userID == "" {
			userID = claims.Sub
		}
		if userID == "" {
			return nil, errMissingIdentity
		}

		// Resolve OIDC client identity from azp or client_id.
		oidcClientID := claims.AZP
		if oidcClientID == "" {
			oidcClientID = claims.ClientID
		}

		// Check allowed clients when configured.
		if len(r.allowedClients) > 0 && oidcClientID != "" {
			if !r.allowedClients[oidcClientID] {
				return nil, fmt.Errorf("OIDC client %q is not in the allowed clients list", oidcClientID)
			}
		} else if len(r.allowedClients) > 0 && oidcClientID == "" {
			return nil, fmt.Errorf("OIDC token missing client identity claim (azp/client_id); cannot verify against allowed clients list")
		}

		// Check allowed audiences when configured.
		if len(r.allowedAudience) > 0 {
			audSlice := toStringSlice(claims.Aud)
			matched := false
			for _, a := range audSlice {
				if r.allowedAudience[a] {
					matched = true
					break
				}
			}
			if !matched {
				return nil, fmt.Errorf("OIDC token audience does not match any configured allowed audience")
			}
		}

		// If no X-API-Key was provided, use the OIDC client ID as the client identity.
		if clientID == "" && oidcClientID != "" {
			clientID = oidcClientID
		}

		// Resolve roles from token claims.
		var rawClaims map[string]any
		if err := idToken.Claims(&rawClaims); err == nil {
			if err := r.addTokenRoles(roles, rawClaims); err != nil {
				return nil, errors.Join(errInvalidJWT, err)
			}
		}

		r.addUserRoles(roles, userID)

		// Client role assignment (union OIDC client and API-key client).
		r.addClientRoles(roles, clientID)

		applyAdminImplication(roles)

		kind := CredentialOIDC
		if apiKey != "" {
			kind = CredentialOIDCAPIKey
		}
		id := newIdentity(userID, clientID, roles, kind)
		id.HasOIDCToken = true
		id.OIDCScopes = splitFields(claims.Scope)
		id.OIDCScopeGates = r.oidcScopes
		return id, nil
	}

	// OIDC not configured: bearer token mode.
	// If the bearer value matches a configured API key, it's a no-OIDC bearer API-key compatibility request.
	if r.bearerTokenIsAPIKey(bearerToken) {
		// Bearer value is a configured API key: resolve client identity (no-OIDC compat).
		clientID = r.apiKeys[bearerToken]
		roles := r.rolesForClient(clientID)
		return newIdentity("", clientID, roles, CredentialBearerAPIKey), nil
	}

	// No-OIDC mode: treat the bearer token as the user ID (raw bearer user).
	// This is only accepted in testing mode with the auth_testfixtures build tag.
	return resolveRawBearer(r, ctx, bearerToken, clientID, roles)
}

func newIdentity(userID, clientID string, roles map[string]bool, kind CredentialKind) *Identity {
	return &Identity{
		UserID:         userID,
		ClientID:       clientID,
		Roles:          roles,
		IsAdmin:        roles[RoleAdmin],
		CredentialKind: kind,
	}
}

func (r *TokenResolver) rolesForClient(clientID string) map[string]bool {
	roles := map[string]bool{}
	r.addClientRoles(roles, clientID)
	applyAdminImplication(roles)
	return roles
}

func (r *TokenResolver) addTokenRoles(roles map[string]bool, rawClaims map[string]any) error {
	tokenRoles, err := extractTokenRoles(rawClaims, r.roleClaims)
	if err != nil {
		return err
	}
	if tokenRoles[r.adminOIDCRole] {
		roles[RoleAdmin] = true
	}
	if tokenRoles[r.auditorOIDCRole] {
		roles[RoleAuditor] = true
	}
	if r.indexerOIDCRole != "" && tokenRoles[r.indexerOIDCRole] {
		roles[RoleIndexer] = true
	}
	return nil
}

func (r *TokenResolver) addUserRoles(roles map[string]bool, userID string) {
	if matchesConfiguredValue(r.adminUsers, userID) {
		roles[RoleAdmin] = true
	}
	if matchesConfiguredValue(r.auditorUsers, userID) {
		roles[RoleAuditor] = true
	}
	if matchesConfiguredValue(r.indexerUsers, userID) {
		roles[RoleIndexer] = true
	}
}

func (r *TokenResolver) addClientRoles(roles map[string]bool, clientID string) {
	if clientID == "" {
		return
	}
	if matchesConfiguredValue(r.adminClients, clientID) {
		roles[RoleAdmin] = true
	}
	if matchesConfiguredValue(r.auditorClients, clientID) {
		roles[RoleAuditor] = true
	}
	if matchesConfiguredValue(r.indexerClients, clientID) {
		roles[RoleIndexer] = true
	}
}

func applyAdminImplication(roles map[string]bool) {
	if roles[RoleAdmin] {
		roles[RoleAuditor] = true
		roles[RoleIndexer] = true
	}
}

func parsePermissionScopes(raw map[string]string) map[Permission]map[string]bool {
	result := map[Permission]map[string]bool{}
	for _, desc := range permissionDescriptors {
		scopes := splitCSV(raw[string(desc.Permission)])
		if len(scopes) > 0 {
			result[desc.Permission] = scopes
		}
	}
	return result
}

func configuredScopesForPermission(configured map[Permission]map[string]bool, permission Permission) map[string]bool {
	result := map[string]bool{}
	for _, candidate := range scopePermissionCandidates(permission) {
		if scopes := configured[candidate]; len(scopes) > 0 {
			for scope := range scopes {
				result[scope] = true
			}
		}
	}
	return result
}

func scopePermissionCandidates(permission Permission) []Permission {
	candidates := []Permission{permission}
	if aggregate := aggregatePermission(permission); aggregate != "" {
		candidates = append(candidates, aggregate)
	}
	candidates = append(candidates, broadPermissionCandidates(permission)...)
	return candidates
}

func aggregatePermission(permission Permission) Permission {
	raw := string(permission)
	switch {
	case strings.HasSuffix(raw, "_read"):
		return Permission(strings.TrimSuffix(raw, "_read"))
	case strings.HasSuffix(raw, "_write"):
		return Permission(strings.TrimSuffix(raw, "_write"))
	default:
		return ""
	}
}

func broadPermissionCandidates(permission Permission) []Permission {
	raw := string(permission)
	if strings.HasPrefix(raw, "admin_") {
		switch {
		case strings.HasSuffix(raw, "_read"):
			return []Permission{PermissionAdminRead, PermissionAdmin}
		case strings.HasSuffix(raw, "_write"):
			return []Permission{PermissionAdminWrite, PermissionAdmin}
		default:
			return []Permission{PermissionAdmin}
		}
	}
	if isUserPermission(permission) {
		switch {
		case strings.HasSuffix(raw, "_read"):
			return []Permission{PermissionUserRead, PermissionUser}
		case strings.HasSuffix(raw, "_write"):
			return []Permission{PermissionUserWrite, PermissionUser}
		default:
			return []Permission{PermissionUser}
		}
	}
	return nil
}

func isUserPermission(permission Permission) bool {
	switch permission {
	case PermissionConversations,
		PermissionConversationsRead,
		PermissionConversationsWrite,
		PermissionSharing,
		PermissionSharingRead,
		PermissionSharingWrite,
		PermissionSearch,
		PermissionSearchRead,
		PermissionSearchWrite,
		PermissionMemories,
		PermissionMemoriesRead,
		PermissionMemoriesWrite,
		PermissionAttachments,
		PermissionAttachmentsRead,
		PermissionAttachmentsWrite,
		PermissionEventsRead,
		PermissionRecordings,
		PermissionRecordingsRead,
		PermissionRecordingsWrite:
		return true
	default:
		return false
	}
}

func checkIdentityOIDCScope(id *Identity, permission Permission) error {
	if id == nil || !id.HasOIDCToken {
		return nil
	}
	required := configuredScopesForPermission(id.OIDCScopeGates, permission)
	if len(required) == 0 {
		return nil
	}
	for scope := range id.OIDCScopes {
		if required[scope] {
			return nil
		}
	}
	return fmt.Errorf("OIDC token missing required scope for %s", permission)
}

func (r *TokenResolver) bearerTokenIsAPIKey(bearerToken string) bool {
	for key := range r.apiKeys {
		if key == bearerToken {
			return true
		}
	}
	return false
}

// --- Gin HTTP middleware ---

// GetIdentity returns the resolved identity from the gin context.
func GetIdentity(c *gin.Context) *Identity {
	v, _ := c.Get(ContextKeyIdentity)
	id, _ := v.(*Identity)
	return id
}

// GetUserID returns the authenticated user ID from the gin context.
func GetUserID(c *gin.Context) string {
	return c.GetString(ContextKeyUserID)
}

// GetClientID returns the agent client ID from the gin context.
func GetClientID(c *gin.Context) string {
	return c.GetString(ContextKeyClientID)
}

// IsAdmin returns true if the request is from an admin.
func IsAdmin(c *gin.Context) bool {
	v, _ := c.Get(ContextKeyIsAdmin)
	b, _ := v.(bool)
	return b
}

// HasRole returns true if the caller has the given role.
func HasRole(c *gin.Context, role string) bool {
	v, ok := c.Get(ContextKeyRoles)
	if !ok {
		return false
	}
	roles, ok := v.(map[string]bool)
	if !ok {
		return false
	}
	return roles[role]
}

// EffectiveAdminRole returns the highest resolved admin role.
func EffectiveAdminRole(c *gin.Context) string {
	switch {
	case HasRole(c, RoleAdmin):
		return RoleAdmin
	case HasRole(c, RoleAuditor):
		return RoleAuditor
	default:
		return ""
	}
}

// AuthMiddleware returns a gin middleware that extracts user identity from the request headers
// using the provided TokenResolver. Allows X-API-Key-only requests (for admin service principals)
// when no Authorization header is present.
func AuthMiddleware(resolver *TokenResolver) gin.HandlerFunc {
	return AuthMiddlewareWithRateLimiter(resolver, nil)
}

func AuthMiddlewareWithRateLimiter(resolver *TokenResolver, limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if resolver.localUserID != "" {
			id, _ := resolver.Resolve(c.Request.Context(), RequestCredentials{})
			setGinIdentity(c, id)
			if !applyHTTPAuthenticatedRateLimits(c, limiter) {
				return
			}
			c.Next()
			return
		}
		auth := c.GetHeader("Authorization")
		apiKey := c.GetHeader("X-API-Key")
		clientIDHeader := c.GetHeader("X-Client-ID")

		// Embedded MCP in-process transport: the trust signal is a context value set by
		// handlerTransport.RoundTrip using the unexported embeddedMCPContextKey.
		// Remote callers cannot forge this because the key type is unexported.
		if isEmbeddedMCPRequest(c.Request.Context()) {
			creds := RequestCredentials{Transport: EmbeddedMCPTransport}
			id, err := resolver.Resolve(c.Request.Context(), creds)
			if err != nil {
				consumeHTTPAuthFailure(c, limiter)
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
				return
			}
			setGinIdentity(c, id)
			if !applyHTTPAuthenticatedRateLimits(c, limiter) {
				return
			}
			c.Next()
			return
		}

		var bearerToken string
		if auth != "" {
			// Must use Bearer scheme.
			bearerToken = strings.TrimPrefix(auth, "Bearer ")
			if bearerToken == auth {
				log.Info("Auth rejected: invalid Authorization header; expected Bearer token", "method", c.Request.Method, "path", c.Request.URL.Path)
				consumeHTTPAuthFailure(c, limiter)
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization header; expected Bearer token"})
				return
			}
		}

		// Reject requests with no credentials (no bearer token, no API key, no test client ID).
		if bearerToken == "" && strings.TrimSpace(apiKey) == "" && strings.TrimSpace(clientIDHeader) == "" {
			log.Info("Auth rejected: missing credentials", "method", c.Request.Method, "path", c.Request.URL.Path)
			consumeHTTPAuthFailure(c, limiter)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing credentials"})
			return
		}

		creds := RequestCredentials{
			BearerToken:    bearerToken,
			APIKey:         apiKey,
			ClientIDHeader: clientIDHeader,
		}

		id, err := resolver.Resolve(c.Request.Context(), creds)
		if err != nil {
			log.Info("Auth rejected", "method", c.Request.Method, "path", c.Request.URL.Path, "err", err)
			consumeHTTPAuthFailure(c, limiter)
			// Return 403 for allowed-client/audience violations; 401 for other auth failures.
			status := http.StatusUnauthorized
			errStr := err.Error()
			if strings.Contains(errStr, "not in the allowed clients list") ||
				strings.Contains(errStr, "audience does not match") {
				status = http.StatusForbidden
			}
			c.AbortWithStatusJSON(status, gin.H{"error": errStr})
			return
		}

		setGinIdentity(c, id)
		if !applyHTTPAuthenticatedRateLimits(c, limiter) {
			return
		}
		c.Next()
	}
}

func setGinIdentity(c *gin.Context, id *Identity) {
	c.Set(ContextKeyUserID, id.UserID)
	if id.ClientID != "" {
		c.Set(ContextKeyClientID, id.ClientID)
	}
	c.Set(ContextKeyIdentity, id)
	c.Set(ContextKeyRoles, id.Roles)
	c.Set(ContextKeyIsAdmin, id.IsAdmin)
}

// RequireUser requires a non-empty authenticated user principal.
// Client-only (API-key-only) identities are rejected with 401.
// This should be applied to all normal user/agent endpoints.
func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if GetUserID(c) == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user authentication required"})
			return
		}
		c.Next()
	}
}

// RequireAdminRole requires the caller to have admin role.
func RequireAdminRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !HasRole(c, RoleAdmin) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

// RequireAuditorRole requires the caller to have auditor or admin role.
func RequireAuditorRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !HasRole(c, RoleAuditor) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

// RequireOIDCScope requires the configured OIDC scope for a resource/API permission.
// It is a no-op for non-OIDC identities and for permissions with no configured scopes.
func RequireOIDCScope(permission Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := checkIdentityOIDCScope(GetIdentity(c), permission); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		c.Next()
	}
}

// CheckGRPCOIDCScope applies the same resource/API OIDC scope gate for gRPC handlers.
func CheckGRPCOIDCScope(ctx context.Context, permission Permission) error {
	if err := checkIdentityOIDCScope(IdentityFromContext(ctx), permission); err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return nil
}

// --- gRPC interceptors ---

// grpcMetadataValue extracts a single metadata value from the incoming gRPC context.
func grpcMetadataValue(ctx context.Context, key string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// resolveGRPCIdentity extracts auth headers from gRPC metadata and resolves identity.
func resolveGRPCIdentity(ctx context.Context, resolver *TokenResolver) context.Context {
	return resolveGRPCIdentityWithRateLimiter(ctx, resolver, nil)
}

func resolveGRPCIdentityWithRateLimiter(ctx context.Context, resolver *TokenResolver, limiter *RateLimiter) context.Context {
	if resolver.localUserID != "" {
		id, _ := resolver.Resolve(ctx, RequestCredentials{})
		return context.WithValue(ctx, grpcIdentityKey{}, id)
	}
	auth := grpcMetadataValue(ctx, "authorization")
	apiKey := grpcMetadataValue(ctx, "x-api-key")

	var bearerToken string
	if auth != "" {
		bearerToken = strings.TrimPrefix(auth, "Bearer ")
		if bearerToken == auth {
			// Non-Bearer authorization value (e.g. Basic, junk): treat as an auth failure
			// so a valid API key cannot silently succeed alongside a malformed auth header.
			// Do not log the header value — it may contain Basic credentials or other secrets.
			log.Debug("gRPC auth: non-Bearer authorization header rejected")
			consumeGRPCAuthFailure(ctx, limiter)
			return ctx
		}
	}

	if bearerToken == "" && strings.TrimSpace(apiKey) == "" {
		return ctx
	}

	creds := RequestCredentials{
		BearerToken:    bearerToken,
		APIKey:         apiKey,
		ClientIDHeader: grpcMetadataValue(ctx, "x-client-id"),
	}

	id, err := resolver.Resolve(ctx, creds)
	if err != nil {
		log.Debug("gRPC auth: token resolution failed", "err", err)
		consumeGRPCAuthFailure(ctx, limiter)
		return ctx
	}
	return context.WithValue(ctx, grpcIdentityKey{}, id)
}

// GRPCUnaryInterceptor returns a gRPC unary server interceptor that resolves caller identity.
func GRPCUnaryInterceptor(resolver *TokenResolver) grpc.UnaryServerInterceptor {
	return GRPCUnaryInterceptorWithRateLimiter(resolver, nil)
}

// GRPCUnaryInterceptorWithRateLimiter returns a gRPC unary server interceptor that resolves
// caller identity and charges invalid credentials against the auth-failure bucket.
func GRPCUnaryInterceptorWithRateLimiter(resolver *TokenResolver, limiter *RateLimiter) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		return handler(resolveGRPCIdentityWithRateLimiter(ctx, resolver, limiter), req)
	}
}

// GRPCStreamInterceptor returns a gRPC stream server interceptor that resolves caller identity.
func GRPCStreamInterceptor(resolver *TokenResolver) grpc.StreamServerInterceptor {
	return GRPCStreamInterceptorWithRateLimiter(resolver, nil)
}

// GRPCStreamInterceptorWithRateLimiter returns a gRPC stream server interceptor that resolves
// caller identity and charges invalid credentials against the auth-failure bucket.
func GRPCStreamInterceptorWithRateLimiter(resolver *TokenResolver, limiter *RateLimiter) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		wrapped := &wrappedServerStream{
			ServerStream: ss,
			ctx:          resolveGRPCIdentityWithRateLimiter(ss.Context(), resolver, limiter),
		}
		return handler(srv, wrapped)
	}
}

// wrappedServerStream overrides Context() to return the enriched context.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// --- helpers ---

func splitCSV(raw string) map[string]bool {
	result := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		result[item] = true
	}
	return result
}

func splitFields(raw string) map[string]bool {
	result := map[string]bool{}
	for _, field := range strings.Fields(raw) {
		result[field] = true
	}
	return result
}

func matchesConfiguredValue(values map[string]bool, actual string) bool {
	if values[actual] {
		return true
	}
	for value := range values {
		if strings.HasSuffix(value, "*") && strings.HasPrefix(actual, strings.TrimSuffix(value, "*")) {
			return true
		}
	}
	return false
}

func extractTokenRoles(claims map[string]any, pointers []string) (map[string]bool, error) {
	result := map[string]bool{}
	addRole := func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		if len([]byte(value)) > 256 {
			return fmt.Errorf("OIDC role value exceeds 256 bytes")
		}
		if len(result) >= 256 && !result[value] {
			return fmt.Errorf("OIDC token contains more than 256 roles")
		}
		result[value] = true
		return nil
	}
	addList := func(values []string) error {
		for _, v := range values {
			if err := addRole(v); err != nil {
				return err
			}
		}
		return nil
	}

	for _, pointer := range pointers {
		value, found := jsonPointerValue(claims, pointer)
		if !found {
			continue
		}
		switch v := value.(type) {
		case string:
			if err := addRole(v); err != nil {
				return nil, err
			}
		case []string:
			if err := addList(v); err != nil {
				return nil, err
			}
		case []any:
			values := make([]string, 0, len(v))
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("OIDC role claim %q must contain only strings", pointer)
				}
				values = append(values, s)
			}
			if err := addList(values); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("OIDC role claim %q must be a string or string array", pointer)
		}
	}

	return result, nil
}

func validateRoleClaimPointers(configured []string) ([]string, error) {
	if configured == nil {
		return []string{"/realm_access/roles"}, nil
	}
	if len(configured) > 16 {
		return nil, fmt.Errorf("OIDC role claims must contain at most 16 JSON Pointer paths")
	}
	out := make([]string, 0, len(configured))
	seen := map[string]bool{}
	for _, pointer := range configured {
		pointer = strings.TrimSpace(pointer)
		if err := validateJSONPointer(pointer); err != nil {
			return nil, fmt.Errorf("invalid OIDC role claim %q: %w", pointer, err)
		}
		if !seen[pointer] {
			seen[pointer] = true
			out = append(out, pointer)
		}
	}
	return out, nil
}

func validateJSONPointer(pointer string) error {
	if pointer == "" {
		return nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return fmt.Errorf("JSON Pointer must be empty or start with '/'")
	}
	for _, part := range strings.Split(pointer[1:], "/") {
		for i := 0; i < len(part); i++ {
			if part[i] == '~' {
				if i+1 >= len(part) || (part[i+1] != '0' && part[i+1] != '1') {
					return fmt.Errorf("invalid JSON Pointer escape")
				}
				i++
			}
		}
	}
	return nil
}

func jsonPointerValue(doc any, pointer string) (any, bool) {
	if pointer == "" {
		return doc, true
	}
	current := doc
	for _, rawPart := range strings.Split(pointer[1:], "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(rawPart, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			current = next
		default:
			return nil, false
		}
	}
	return current, true
}

func toStringSlice(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{v}
	default:
		// Claims decoding may yield map[string]interface{} with nested json.RawMessage.
		var out []string
		return out
	}
}
