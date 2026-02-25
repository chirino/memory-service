package security

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
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
)

const (
	RoleAdmin   = "admin"
	RoleAuditor = "auditor"
	RoleIndexer = "indexer"
)

// Identity holds the resolved caller identity from a bearer token.
type Identity struct {
	UserID   string
	ClientID string
	Roles    map[string]bool
	IsAdmin  bool
}

// grpcIdentityKey is the context key for storing Identity in gRPC contexts.
type grpcIdentityKey struct{}

// IdentityFromContext retrieves the Identity stored in a context by the gRPC interceptor.
func IdentityFromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(grpcIdentityKey{}).(*Identity)
	return id
}

// TokenResolver resolves bearer tokens to caller identities. It is initialized once at startup
// and shared by both the HTTP middleware and gRPC interceptors.
type TokenResolver struct {
	verifier        *oidc.IDTokenVerifier
	apiKeys         map[string]string
	adminOIDCRole   string
	auditorOIDCRole string
	indexerOIDCRole string
	adminUsers      map[string]bool
	auditorUsers    map[string]bool
	indexerUsers    map[string]bool
	adminClients    map[string]bool
	auditorClients  map[string]bool
	indexerClients  map[string]bool
	testingMode     bool
}

// NewTokenResolver creates a TokenResolver from the application config. It performs
// one-time OIDC provider discovery if OIDCIssuer is configured.
func NewTokenResolver(cfg *config.Config) *TokenResolver {
	var verifier *oidc.IDTokenVerifier
	oidcIssuer := cfg.OIDCIssuer

	if oidcIssuer != "" {
		ctx := context.Background()
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
			log.Error("Failed to initialize OIDC provider; falling back to API key auth", "issuer", oidcIssuer, "err", err)
		} else {
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
	}

	adminOIDCRole := strings.TrimSpace(cfg.AdminOIDCRole)
	if adminOIDCRole == "" {
		adminOIDCRole = RoleAdmin
	}
	auditorOIDCRole := strings.TrimSpace(cfg.AuditorOIDCRole)
	if auditorOIDCRole == "" {
		auditorOIDCRole = RoleAuditor
	}

	return &TokenResolver{
		verifier:        verifier,
		apiKeys:         cfg.APIKeys,
		adminOIDCRole:   adminOIDCRole,
		auditorOIDCRole: auditorOIDCRole,
		indexerOIDCRole: strings.TrimSpace(cfg.IndexerOIDCRole),
		adminUsers:      splitCSV(cfg.AdminUsers),
		auditorUsers:    splitCSV(cfg.AuditorUsers),
		indexerUsers:    splitCSV(cfg.IndexerUsers),
		adminClients:    splitCSV(cfg.AdminClients),
		auditorClients:  splitCSV(cfg.AuditorClients),
		indexerClients:  splitCSV(cfg.IndexerClients),
		testingMode:     cfg.Mode == config.ModeTesting,
	}
}

var (
	errInvalidJWT      = errors.New("invalid JWT")
	errMissingIdentity = errors.New("JWT missing identity claims")
)

// Resolve resolves a bearer token (and optional API key / client ID header) into a caller Identity.
// bearerToken is the raw token value (without the "Bearer " prefix).
// apiKey is the value of the X-API-Key header (may be empty).
// clientIDHeader is the value of the X-Client-ID header (may be empty; only used in testing mode).
func (r *TokenResolver) Resolve(ctx context.Context, bearerToken, apiKey, clientIDHeader string) (*Identity, error) {
	roles := map[string]bool{}
	var userID string
	var clientID string
	apiKeyAuth := true

	// Resolve API key to clientID.
	if xAPIKey := strings.TrimSpace(apiKey); xAPIKey != "" {
		if resolved, ok := r.apiKeys[xAPIKey]; ok {
			clientID = resolved
		} else {
			log.Warn("Received invalid API key")
		}
	}

	// X-Client-ID header: only accepted in testing mode (BDD tests).
	if r.testingMode {
		if hdr := strings.TrimSpace(clientIDHeader); hdr != "" && clientID == "" {
			clientID = hdr
		}
	}

	// If OIDC is configured and the token looks like a JWT (has dots), verify it.
	if r.verifier != nil && strings.Count(bearerToken, ".") >= 2 {
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

		// Resolve admin/auditor roles from token claims.
		var rawClaims map[string]any
		if err := idToken.Claims(&rawClaims); err == nil {
			tokenRoles := extractTokenRoles(rawClaims)
			if tokenRoles[r.adminOIDCRole] {
				roles[RoleAdmin] = true
			}
			if tokenRoles[r.auditorOIDCRole] {
				roles[RoleAuditor] = true
			}
			if r.indexerOIDCRole != "" && tokenRoles[r.indexerOIDCRole] {
				roles[RoleIndexer] = true
			}
		}
		apiKeyAuth = false
	} else {
		// API key mode: treat the token as the user ID directly.
		userID = bearerToken
	}

	// User-based role assignment.
	if r.adminUsers[userID] {
		roles[RoleAdmin] = true
	}
	if r.auditorUsers[userID] {
		roles[RoleAuditor] = true
	}
	if r.indexerUsers[userID] {
		roles[RoleIndexer] = true
	}
	// API-key client based role assignment parity with Java.
	if apiKeyAuth && clientID != "" {
		if r.adminClients[clientID] {
			roles[RoleAdmin] = true
		}
		if r.auditorClients[clientID] {
			roles[RoleAuditor] = true
		}
		if r.indexerClients[clientID] {
			roles[RoleIndexer] = true
		}
	}
	// Admin implies auditor and indexer.
	if roles[RoleAdmin] {
		roles[RoleAuditor] = true
		roles[RoleIndexer] = true
	}

	return &Identity{
		UserID:   userID,
		ClientID: clientID,
		Roles:    roles,
		IsAdmin:  roles[RoleAdmin],
	}, nil
}

// --- Gin HTTP middleware ---

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

// AuthMiddleware returns a gin middleware that extracts user identity from the Authorization header
// using the provided TokenResolver.
func AuthMiddleware(resolver *TokenResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			log.Info("Auth rejected: missing Authorization header", "method", c.Request.Method, "path", c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		if token == auth {
			log.Info("Auth rejected: invalid Authorization header; expected Bearer token", "method", c.Request.Method, "path", c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization header; expected Bearer token"})
			return
		}

		id, err := resolver.Resolve(
			c.Request.Context(),
			token,
			c.GetHeader("X-API-Key"),
			c.GetHeader("X-Client-ID"),
		)
		if err != nil {
			log.Info("Auth rejected", "method", c.Request.Method, "path", c.Request.URL.Path, "err", err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}

		c.Set(ContextKeyUserID, id.UserID)
		if id.ClientID != "" {
			c.Set(ContextKeyClientID, id.ClientID)
		}
		c.Set(ContextKeyRoles, id.Roles)
		c.Set(ContextKeyIsAdmin, id.IsAdmin)
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

// ClientIDMiddleware extracts the X-Client-ID header and sets it in context.
func ClientIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientID := c.GetHeader("X-Client-ID")
		if clientID != "" {
			c.Set(ContextKeyClientID, clientID)
		}
		c.Next()
	}
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
	auth := grpcMetadataValue(ctx, "authorization")
	if auth == "" {
		return ctx
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == auth {
		return ctx
	}

	id, err := resolver.Resolve(
		ctx,
		token,
		grpcMetadataValue(ctx, "x-api-key"),
		grpcMetadataValue(ctx, "x-client-id"),
	)
	if err != nil {
		log.Debug("gRPC auth: token resolution failed", "err", err)
		return ctx
	}
	return context.WithValue(ctx, grpcIdentityKey{}, id)
}

// GRPCUnaryInterceptor returns a gRPC unary server interceptor that resolves caller identity.
func GRPCUnaryInterceptor(resolver *TokenResolver) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		return handler(resolveGRPCIdentity(ctx, resolver), req)
	}
}

// GRPCStreamInterceptor returns a gRPC stream server interceptor that resolves caller identity.
func GRPCStreamInterceptor(resolver *TokenResolver) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		wrapped := &wrappedServerStream{
			ServerStream: ss,
			ctx:          resolveGRPCIdentity(ss.Context(), resolver),
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

func extractTokenRoles(claims map[string]any) map[string]bool {
	result := map[string]bool{}
	addList := func(values []string) {
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			result[v] = true
		}
	}

	// Common top-level claims.
	addList(toStringSlice(claims["roles"]))
	addList(toStringSlice(claims["groups"]))

	// RFC 8693 / OAuth style scope claim.
	if scope, ok := claims["scope"].(string); ok {
		addList(strings.Fields(scope))
	}

	// Keycloak-style realm_access.roles.
	if realm, ok := claims["realm_access"].(map[string]any); ok {
		addList(toStringSlice(realm["roles"]))
	}

	return result
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
		if data, err := json.Marshal(v); err == nil {
			_ = json.Unmarshal(data, &out)
		}
		return out
	}
}
