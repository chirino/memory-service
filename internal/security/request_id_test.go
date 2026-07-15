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

func TestRequestIDMiddlewareAcceptsSafeInboundID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		require.Equal(t, "req_123-abc.def", RequestIDFromGin(c))
		require.Equal(t, "req_123-abc.def", RequestIDFromContext(c.Request.Context()))
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderRequestID, "req_123-abc.def")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "req_123-abc.def", rec.Header().Get(HeaderRequestID))
}

func TestRequestIDMiddlewareReplacesUnsafeInboundID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.GET("/test", func(c *gin.Context) {
		require.NotEqual(t, "bad/request/id", RequestIDFromGin(c))
		require.NotEmpty(t, RequestIDFromGin(c))
		require.Equal(t, RequestIDFromGin(c), RequestIDFromContext(c.Request.Context()))
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderRequestID, "bad/request/id")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.NotEmpty(t, rec.Header().Get(HeaderRequestID))
	require.NotEqual(t, "bad/request/id", rec.Header().Get(HeaderRequestID))
}

func TestGRPCRequestIDUnaryInterceptorPropagatesSafeInboundID(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "grpc-req-1"))
	interceptor := GRPCRequestIDUnaryInterceptor()

	_, err := interceptor(ctx, "request", &grpc.UnaryServerInfo{FullMethod: "/test.Service/Call"}, func(ctx context.Context, req any) (any, error) {
		require.Equal(t, "grpc-req-1", RequestIDFromContext(ctx))
		return "response", nil
	})

	require.NoError(t, err)
}

func TestGRPCRequestIDStreamInterceptorPropagatesGeneratedID(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "bad/request/id"))
	stream := &testServerStream{ctx: ctx}
	interceptor := GRPCRequestIDStreamInterceptor()

	err := interceptor("service", stream, &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}, func(srv any, ss grpc.ServerStream) error {
		id := RequestIDFromContext(ss.Context())
		require.NotEmpty(t, id)
		require.NotEqual(t, "bad/request/id", id)
		return nil
	})

	require.NoError(t, err)
	require.NotEmpty(t, stream.trailer.Get("x-request-id"))
	require.NotEqual(t, "bad/request/id", stream.trailer.Get("x-request-id")[0])
}

type testServerStream struct {
	grpc.ServerStream
	ctx     context.Context
	header  metadata.MD
	trailer metadata.MD
}

func (s *testServerStream) Context() context.Context {
	return s.ctx
}

func (s *testServerStream) SetHeader(md metadata.MD) error {
	s.header = metadata.Join(s.header, md)
	return nil
}

func (s *testServerStream) SetTrailer(md metadata.MD) {
	s.trailer = metadata.Join(s.trailer, md)
}
