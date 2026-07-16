package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type assertionTestServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *assertionTestServerStream) Context() context.Context { return s.ctx }

func TestUserIDAsserterConfigurationAndBlankValues(t *testing.T) {
	require.False(t, NewUserIDAsserter("").Enabled())
	require.False(t, NewUserIDAsserter(" , , ").Enabled())
	require.True(t, NewUserIDAsserter(" first-client, second-client ").Enabled())

	id := newIdentity("", "second-client", nil, nil, CredentialAPIKey)
	asserted, outcome, err := NewUserIDAsserter(" first-client, second-client ").apply(id, []string{"  alice  "})
	require.NoError(t, err)
	require.Equal(t, "applied", outcome)
	require.Equal(t, "alice", asserted.UserID)

	asserted, outcome, err = NewUserIDAsserter("second-client").apply(id, []string{"  "})
	require.NoError(t, err)
	require.Empty(t, outcome)
	require.Same(t, id, asserted)
}

func TestUserIDAsserterDropsUserRolesAndPreservesClientRoles(t *testing.T) {
	id := newIdentity(
		"admin-user",
		"trusted-client",
		map[string]bool{RoleAdmin: true},
		map[string]bool{RoleIndexer: true},
		CredentialOIDC,
	)
	id.HasOIDCToken = true
	id.OIDCScopes = map[string]bool{"memory-service:user": true}
	id.OIDCScopeGates = map[Permission]map[string]bool{
		PermissionUser: {"memory-service:user": true},
	}

	asserted, outcome, err := NewUserIDAsserter("trusted-client").apply(id, []string{" alice "})
	require.NoError(t, err)
	require.Equal(t, "applied", outcome)
	require.NotSame(t, id, asserted)
	require.Equal(t, "admin-user", asserted.AuthenticatedUserID)
	require.Equal(t, "alice", asserted.UserID)
	require.True(t, asserted.UserIDAsserted)
	require.False(t, asserted.Roles[RoleAdmin])
	require.False(t, asserted.Roles[RoleAuditor])
	require.True(t, asserted.Roles[RoleIndexer])
	require.True(t, asserted.HasOIDCToken)
	require.True(t, asserted.OIDCScopes["memory-service:user"])
	require.Equal(t, id.OIDCScopeGates, asserted.OIDCScopeGates)

	// The base identity is immutable and retains its authenticated user roles.
	require.Equal(t, "admin-user", id.UserID)
	require.True(t, id.Roles[RoleAdmin])
}

func TestUserIDAsserterPreservesRolesWhenAssertionMatchesAuthenticatedUser(t *testing.T) {
	id := newIdentity("alice", "trusted-client", map[string]bool{RoleAdmin: true}, nil, CredentialOIDC)
	asserted, outcome, err := NewUserIDAsserter("trusted-client").apply(id, []string{"alice"})
	require.NoError(t, err)
	require.Equal(t, "applied", outcome)
	require.Same(t, id, asserted)
	require.True(t, asserted.Roles[RoleAdmin])
	require.False(t, asserted.UserIDAsserted)
}

func TestUserIDAsserterIgnoresUntrustedValuesBeforeValidation(t *testing.T) {
	id := newIdentity("", "untrusted-client", nil, nil, CredentialAPIKey)
	asserted, outcome, err := NewUserIDAsserter("trusted-client").apply(id, []string{"alice", "bob"})
	require.NoError(t, err)
	require.Equal(t, "ignored", outcome)
	require.Same(t, id, asserted)
}

func TestUserIDAsserterUsesExactCaseSensitiveClientIDs(t *testing.T) {
	id := newIdentity("", "Trusted-Client", nil, nil, CredentialAPIKey)
	asserted, outcome, err := NewUserIDAsserter("trusted-client").apply(id, []string{"alice"})
	require.NoError(t, err)
	require.Equal(t, "ignored", outcome)
	require.Same(t, id, asserted)
}

func TestUserIDAsserterRejectsMultipleValuesForTrustedClient(t *testing.T) {
	id := newIdentity("", "trusted-client", nil, nil, CredentialAPIKey)
	_, outcome, err := NewUserIDAsserter("trusted-client").apply(id, []string{"alice", "bob"})
	require.ErrorIs(t, err, errMultipleAssertedUsers)
	require.Equal(t, "rejected", outcome)
}

func TestUserIDAssertionHTTPMiddlewareTrustedAndUntrustedBehavior(t *testing.T) {
	gin.SetMode(gin.TestMode)

	request := func(clientID string, values ...string) *httptest.ResponseRecorder {
		router := gin.New()
		base := newIdentity("", clientID, nil, nil, CredentialAPIKey)
		router.Use(func(c *gin.Context) {
			setGinIdentity(c, base)
			c.Next()
		})
		router.Use(NewUserIDAsserter("trusted-client").HTTPMiddleware())
		router.Use(RequireUser())
		router.GET("/v1/test", func(c *gin.Context) {
			c.String(http.StatusOK, GetUserID(c))
		})
		req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
		for _, value := range values {
			req.Header.Add(HeaderUserID, value)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	trusted := request("trusted-client", "alice")
	require.Equal(t, http.StatusOK, trusted.Code)
	require.Equal(t, "alice", trusted.Body.String())

	untrusted := request("untrusted-client", "alice", "bob")
	require.Equal(t, http.StatusUnauthorized, untrusted.Code)
	absent := request("untrusted-client")
	require.Equal(t, absent.Code, untrusted.Code)
	require.JSONEq(t, absent.Body.String(), untrusted.Body.String())

	duplicate := request("trusted-client", "alice", "bob")
	require.Equal(t, http.StatusBadRequest, duplicate.Code)
}

func TestUserIDAsserterGRPCStreamInterceptorAppliesOnlyToUserServices(t *testing.T) {
	id := newIdentity("", "trusted-client", nil, nil, CredentialAPIKey)
	baseCtx := context.WithValue(context.Background(), grpcIdentityKey{}, id)
	baseCtx = metadata.NewIncomingContext(baseCtx, metadata.Pairs(GRPCMetadataUserID, "alice"))
	asserter := NewUserIDAsserter("trusted-client")

	var seen *Identity
	handler := func(_ any, stream grpc.ServerStream) error {
		seen = IdentityFromContext(stream.Context())
		return nil
	}

	err := asserter.GRPCStreamInterceptor()(nil, &assertionTestServerStream{ctx: baseCtx}, &grpc.StreamServerInfo{
		FullMethod: "/memory.v1.ResponseRecorderService/Replay",
	}, handler)
	require.NoError(t, err)
	require.Equal(t, "alice", seen.UserID)

	seen = nil
	err = asserter.GRPCStreamInterceptor()(nil, &assertionTestServerStream{ctx: baseCtx}, &grpc.StreamServerInfo{
		FullMethod: "/memory.v1.AdminMemoriesService/ListMemories",
	}, handler)
	require.NoError(t, err)
	require.Empty(t, seen.UserID)
}

func TestUserIDAsserterAppliesGRPCMetadata(t *testing.T) {
	id := newIdentity("", "trusted-client", nil, nil, CredentialAPIKey)
	ctx := context.WithValue(context.Background(), grpcIdentityKey{}, id)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(GRPCMetadataUserID, "alice"))

	assertedCtx, err := NewUserIDAsserter("trusted-client").ApplyGRPCContext(ctx)
	require.NoError(t, err)
	asserted := IdentityFromContext(assertedCtx)
	require.Equal(t, "alice", asserted.UserID)
	require.True(t, asserted.UserIDAsserted)
}

func TestUserGRPCMethodClassificationExcludesAdminSystemAndMixedEventScope(t *testing.T) {
	for _, method := range []string{
		"/memory.v1.ConversationsService/ListConversations",
		"/memory.v1.ConversationMembershipsService/ListMemberships",
		"/memory.v1.OwnershipTransfersService/ListOwnershipTransfers",
		"/memory.v1.EntriesService/ListEntries",
		"/memory.v1.SearchService/SearchConversations",
		"/memory.v1.MemoriesService/GetMemory",
		"/memory.v1.ResponseRecorderService/Replay",
		"/memory.v1.AttachmentsService/GetAttachment",
	} {
		require.Truef(t, isUserGRPCMethod(method), "expected user method %s", method)
	}
	for _, method := range []string{
		"/memory.v1.SystemService/GetCapabilities",
		"/memory.v1.AdminConversationsService/ListConversations",
		"/memory.v1.AdminMemoriesService/GetMemory",
		"/memory.v1.AdminCheckpointService/GetCheckpoint",
		"/memory.v1.EventStreamService/SubscribeEvents",
	} {
		require.Falsef(t, isUserGRPCMethod(method), "expected non-user method %s", method)
	}
}
