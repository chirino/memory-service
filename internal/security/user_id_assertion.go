package security

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// HeaderUserID is the REST header used by trusted clients to select the
	// effective user for a normal user-scoped operation.
	HeaderUserID = "X-User-ID"
	// GRPCMetadataUserID is the canonical gRPC metadata spelling.
	GRPCMetadataUserID = "x-user-id"
)

var errMultipleAssertedUsers = errors.New("multiple X-User-ID values are not allowed")

// UserIDAsserter applies user identity metadata only for exact trusted client IDs.
type UserIDAsserter struct {
	trustedClients map[string]bool
}

// NewUserIDAsserter parses a comma-separated exact client allowlist.
func NewUserIDAsserter(configured string) *UserIDAsserter {
	return &UserIDAsserter{trustedClients: splitCSV(configured)}
}

// Enabled reports whether at least one client is trusted to assert users.
func (a *UserIDAsserter) Enabled() bool {
	return a != nil && len(a.trustedClients) > 0
}

func (a *UserIDAsserter) apply(id *Identity, values []string) (*Identity, string, error) {
	if len(values) == 0 {
		return id, "", nil
	}
	if id == nil || a == nil || !a.trustedClients[id.ClientID] {
		return id, "ignored", nil
	}

	nonEmpty := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			nonEmpty = append(nonEmpty, value)
		}
	}
	if len(nonEmpty) == 0 {
		return id, "", nil
	}
	if len(nonEmpty) != 1 {
		return id, "rejected", errMultipleAssertedUsers
	}
	return id.withAssertedUser(nonEmpty[0]), "applied", nil
}

func recordUserIDAssertion(outcome string) {
	if outcome != "" && UserIDAssertionsTotal != nil {
		UserIDAssertionsTotal.WithLabelValues(outcome).Inc()
	}
}

func logUserIDAssertion(outcome, requestID string, before, after *Identity) {
	if outcome == "ignored" {
		clientID := ""
		if before != nil {
			clientID = before.ClientID
		}
		log.Debug("user ID assertion ignored", "request_id", requestID, "client_id", clientID)
		return
	}
	if outcome == "applied" && before != nil && after != nil && after.UserIDAsserted {
		log.Info("trusted user ID assertion applied",
			"request_id", requestID,
			"client_id", before.ClientID,
			"authenticated_user_id", before.AuthenticatedUserID,
			"effective_user_id", after.UserID,
		)
	}
}

// HTTPMiddleware applies X-User-ID after base authentication on normal user routes.
func (a *UserIDAsserter) HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		before := GetIdentity(c)
		after, outcome, err := a.apply(before, c.Request.Header.Values(HeaderUserID))
		recordUserIDAssertion(outcome)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		logUserIDAssertion(outcome, RequestIDFromGin(c), before, after)
		if after != nil && after != before {
			setGinIdentity(c, after)
		}
		c.Next()
	}
}

// ApplyGRPCContext applies x-user-id to an already authenticated gRPC context.
// It is also used inside mixed-scope streaming handlers after their request scope is known.
func (a *UserIDAsserter) ApplyGRPCContext(ctx context.Context) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get(GRPCMetadataUserID)
	before := IdentityFromContext(ctx)
	after, outcome, err := a.apply(before, values)
	recordUserIDAssertion(outcome)
	if err != nil {
		return ctx, status.Error(codes.InvalidArgument, err.Error())
	}
	logUserIDAssertion(outcome, RequestIDFromContext(ctx), before, after)
	if after == nil || after == before {
		return ctx, nil
	}
	return context.WithValue(ctx, grpcIdentityKey{}, after), nil
}

// GRPCUnaryInterceptor applies assertions only to normal user-scoped methods.
func (a *UserIDAsserter) GRPCUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !isUserGRPCMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		asserted, err := a.ApplyGRPCContext(ctx)
		if err != nil {
			return nil, err
		}
		return handler(asserted, req)
	}
}

// GRPCStreamInterceptor applies assertions to normal user-scoped streams. EventStreamService
// is handled after SubscribeEventsRequest.scope is known and is deliberately excluded here.
func (a *UserIDAsserter) GRPCStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !isUserGRPCMethod(info.FullMethod) {
			return handler(srv, ss)
		}
		ctx, err := a.ApplyGRPCContext(ss.Context())
		if err != nil {
			return err
		}
		return handler(srv, &userAssertionServerStream{ServerStream: ss, ctx: ctx})
	}
}

func isUserGRPCMethod(fullMethod string) bool {
	for _, service := range []string{
		"ConversationsService",
		"ConversationMembershipsService",
		"OwnershipTransfersService",
		"EntriesService",
		"SearchService",
		"MemoriesService",
		"ResponseRecorderService",
		"AttachmentsService",
	} {
		if strings.HasPrefix(fullMethod, "/memory.v1."+service+"/") {
			return true
		}
	}
	return false
}

type userAssertionServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *userAssertionServerStream) Context() context.Context {
	return s.ctx
}
