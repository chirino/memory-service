//go:build !noqdrant

package qdrant

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/google/uuid"
	pb "github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/require"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// metadataCapturingServer is a minimal PointsServer that records the
// incoming gRPC metadata on each call so tests can assert on traceparent.
type metadataCapturingServer struct {
	pb.UnimplementedPointsServer
	captured chan metadata.MD
}

func newMetadataCapturingServer() *metadataCapturingServer {
	return &metadataCapturingServer{captured: make(chan metadata.MD, 1)}
}

func (s *metadataCapturingServer) Search(_ context.Context, _ *pb.SearchPoints) (*pb.SearchResponse, error) {
	// metadata.FromIncomingContext is called in the RPC handler but the
	// stats handler (otelgrpc) runs earlier; we capture from the context
	// provided to the handler.
	return &pb.SearchResponse{}, nil
}

// Search with context capture for metadata.
func (s *metadataCapturingServer) searchWithCapture(ctx context.Context, _ *pb.SearchPoints) (*pb.SearchResponse, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		select {
		case s.captured <- md:
		default:
		}
	}
	return &pb.SearchResponse{}, nil
}

// TestQdrantUntracedRequestNoTraceparent verifies that an untraced context does
// not inject a traceparent metadata entry into outbound Qdrant gRPC requests.
//
// The connection uses dialOptions — the production dial-option constructor — so
// removing the otelgrpc stats handler from dialOptions causes this test to fail.
func TestQdrantUntracedRequestNoTraceparent(t *testing.T) {
	srv := newMetadataCapturingServer()
	grpcH := testutil.NewGRPCBufConnHarness(
		grpc.UnaryInterceptor(func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			if md, ok := metadata.FromIncomingContext(ctx); ok {
				select {
				case srv.captured <- md:
				default:
				}
			}
			return handler(ctx, req)
		}),
	)
	pb.RegisterPointsServer(grpcH.Server, srv)
	grpcH.Serve()
	t.Cleanup(grpcH.Close)

	conn := dialQdrantBufconn(t, grpcH, dialOptions(&config.Config{}, nooptrace.NewTracerProvider())...)
	store := NewQdrantStoreForTest(conn, "test-collection")

	groupID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	_, _ = store.Search(context.Background(), []float32{0.1}, []uuid.UUID{groupID}, 1)

	md := <-srv.captured
	require.NotNil(t, md, "server must have been reached")
	require.Empty(t, md.Get("traceparent"), "untraced request must not inject traceparent")
}

// TestQdrantTracedRequestInjectsTraceparent verifies that a participating context
// causes the outbound Qdrant gRPC request to carry a traceparent metadata entry
// containing the caller's trace ID.
//
// The connection uses dialOptions — the production dial-option constructor — so
// removing the otelgrpc stats handler from dialOptions causes this test to fail.
func TestQdrantTracedRequestInjectsTraceparent(t *testing.T) {
	srv := newMetadataCapturingServer()
	grpcH := testutil.NewGRPCBufConnHarness(
		grpc.UnaryInterceptor(func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			if md, ok := metadata.FromIncomingContext(ctx); ok {
				select {
				case srv.captured <- md:
				default:
				}
			}
			return handler(ctx, req)
		}),
	)
	pb.RegisterPointsServer(grpcH.Server, srv)
	grpcH.Serve()
	t.Cleanup(grpcH.Close)

	h := testutil.NewTestHarness()
	t.Cleanup(func() { _ = h.Shutdown(context.Background()) })

	conn := dialQdrantBufconn(t, grpcH, dialOptions(&config.Config{}, h.Provider)...)
	store := NewQdrantStoreForTest(conn, "test-collection")

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inbound := testutil.NewSampledTraceparent(traceID, parentSpanID)

	extractedCtx := h.Propagator.Extract(context.Background(),
		staticGRPCCarrier{"traceparent": []string{inbound}})
	ctx := tracing.MarkParticipating(extractedCtx)

	groupID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	_, _ = store.Search(ctx, []float32{0.1}, []uuid.UUID{groupID}, 1)

	md := <-srv.captured
	outbound := md.Get("traceparent")
	require.NotEmpty(t, outbound, "traced request must inject outbound traceparent")
	require.Contains(t, outbound[0], traceID, "outbound traceparent must carry the caller's trace ID")
}

// dialQdrantBufconn dials the GRPCBufConnHarness with the given client options.
func dialQdrantBufconn(t *testing.T, h *testutil.GRPCBufConnHarness, clientOpts ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()
	conn, err := h.Dial(context.Background(), clientOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// staticGRPCCarrier is a read-only TextMapCarrier backed by gRPC metadata values.
type staticGRPCCarrier map[string][]string

func (c staticGRPCCarrier) Get(key string) string {
	if vals := c[key]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}
func (c staticGRPCCarrier) Set(_, _ string) {}
func (c staticGRPCCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
