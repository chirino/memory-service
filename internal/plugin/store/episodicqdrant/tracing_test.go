package episodicqdrant

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	pb "github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/require"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type mdCapturingServer struct {
	pb.UnimplementedPointsServer
	captured chan metadata.MD
}

func newMDCapturingServer() *mdCapturingServer {
	return &mdCapturingServer{captured: make(chan metadata.MD, 1)}
}

func (s *mdCapturingServer) Search(_ context.Context, _ *pb.SearchPoints) (*pb.SearchResponse, error) {
	return &pb.SearchResponse{}, nil
}

// TestEpisodicQdrantUntracedRequestNoTraceparent verifies that an untraced context
// does not inject a traceparent metadata entry into outbound Qdrant gRPC requests.
//
// The connection uses dialOptions — the production dial-option constructor — so
// removing the otelgrpc stats handler from dialOptions causes this test to fail.
func TestEpisodicQdrantUntracedRequestNoTraceparent(t *testing.T) {
	srv := newMDCapturingServer()
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

	conn, err := grpcH.Dial(context.Background(), dialOptions(&config.Config{}, nooptrace.NewTracerProvider())...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := NewEpisodicQdrantClientForTest(conn, "test-collection")

	_, _ = client.SearchMemoryVectors(context.Background(), "", []float32{0.1}, registryepisodic.AttributeFilter{}, "", 1, registryepisodic.ArchiveFilterExclude)

	md := <-srv.captured
	require.NotNil(t, md, "server must have been reached")
	require.Empty(t, md.Get("traceparent"), "untraced request must not inject traceparent")
}

// TestEpisodicQdrantTracedRequestInjectsTraceparent verifies that a participating
// context causes the outbound Qdrant gRPC request to carry a traceparent metadata
// entry containing the caller's trace ID.
//
// The connection uses dialOptions — the production dial-option constructor — so
// removing the otelgrpc stats handler from dialOptions causes this test to fail.
func TestEpisodicQdrantTracedRequestInjectsTraceparent(t *testing.T) {
	srv := newMDCapturingServer()
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

	conn, err := grpcH.Dial(context.Background(), dialOptions(&config.Config{}, h.Provider)...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := NewEpisodicQdrantClientForTest(conn, "test-collection")

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inbound := testutil.NewSampledTraceparent(traceID, parentSpanID)

	extractedCtx := h.Propagator.Extract(context.Background(),
		staticMDCarrier{"traceparent": []string{inbound}})
	ctx := tracing.MarkParticipating(extractedCtx)

	_, _ = client.SearchMemoryVectors(ctx, "", []float32{0.1}, registryepisodic.AttributeFilter{}, "", 1, registryepisodic.ArchiveFilterExclude)

	md := <-srv.captured
	outbound := md.Get("traceparent")
	require.NotEmpty(t, outbound, "traced request must inject outbound traceparent")
	require.Contains(t, outbound[0], traceID, "outbound traceparent must carry the caller's trace ID")
}

type staticMDCarrier map[string][]string

func (c staticMDCarrier) Get(key string) string {
	if vals := c[key]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}
func (c staticMDCarrier) Set(_, _ string) {}
func (c staticMDCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
