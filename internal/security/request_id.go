package security

import (
	"context"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// HeaderRequestID is the canonical HTTP/gRPC metadata key used for request correlation.
	HeaderRequestID = "X-Request-ID"
	// ContextKeyRequestID is the gin context key for the request ID.
	ContextKeyRequestID = "requestID"
)

type requestIDContextKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RequestIDFromContext returns the request ID carried by a context, if any.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}

// RequestIDFromGin returns the request ID carried by a gin context, if any.
func RequestIDFromGin(c *gin.Context) string {
	if c == nil {
		return ""
	}
	id, _ := c.Get(ContextKeyRequestID)
	if value, ok := id.(string); ok {
		return value
	}
	return ""
}

// WithRequestID returns a child context carrying a validated or generated request ID.
func WithRequestID(ctx context.Context, requested string) (context.Context, string) {
	id := normalizeRequestID(requested)
	return context.WithValue(ctx, requestIDContextKey{}, id), id
}

func normalizeRequestID(requested string) string {
	if requestIDPattern.MatchString(requested) {
		return requested
	}
	return uuid.NewString()
}

// RequestIDMiddleware attaches a safe request ID before recovery, auth, and route handlers.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, id := WithRequestID(c.Request.Context(), c.GetHeader(HeaderRequestID))
		c.Request = c.Request.WithContext(ctx)
		c.Set(ContextKeyRequestID, id)
		c.Header(HeaderRequestID, id)
		c.Next()
	}
}

// GRPCRequestIDUnaryInterceptor attaches request ID metadata to unary calls.
func GRPCRequestIDUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		ctx, id := requestIDContextFromMetadata(ctx)
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", id))
		resp, err := handler(ctx, req)
		_ = grpc.SetTrailer(ctx, metadata.Pairs("x-request-id", id))
		return resp, err
	}
}

// GRPCRequestIDStreamInterceptor attaches request ID metadata to streaming calls.
func GRPCRequestIDStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx, id := requestIDContextFromMetadata(ss.Context())
		_ = ss.SetHeader(metadata.Pairs("x-request-id", id))
		ss.SetTrailer(metadata.Pairs("x-request-id", id))
		return handler(srv, &requestIDServerStream{ServerStream: ss, ctx: ctx})
	}
}

func requestIDContextFromMetadata(ctx context.Context) (context.Context, string) {
	md, _ := metadata.FromIncomingContext(ctx)
	requested := ""
	if values := md.Get("x-request-id"); len(values) > 0 {
		requested = values[0]
	}
	return WithRequestID(ctx, requested)
}

type requestIDServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *requestIDServerStream) Context() context.Context {
	return s.ctx
}
