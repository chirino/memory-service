package tracing_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/chirino/memory-service/internal/tracing/testutil"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/codes"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testGRPCServer struct {
	pb.UnimplementedSystemServiceServer
	downstreamURL string
	harness       *testutil.Harness
	callCount     int
}

func (s *testGRPCServer) GetHealth(ctx context.Context, _ *emptypb.Empty) (*pb.HealthResponse, error) {
	s.callCount++
	client := &http.Client{
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithTracerProvider(s.harness.Provider),
			otelhttp.WithPropagators(s.harness.Propagator),
		),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.downstreamURL, nil)
	if err == nil {
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
	return &pb.HealthResponse{Status: "SERVING"}, nil
}

type grpcTestSetup struct {
	Harness    *testutil.Harness
	Downstream *testutil.DownstreamRecorder
	Client     pb.SystemServiceClient
	Server     *testGRPCServer
}

func setupGRPCTest(t *testing.T, extraInterceptors ...grpc.UnaryServerInterceptor) *grpcTestSetup {
	t.Helper()
	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	server := &testGRPCServer{downstreamURL: downstream.Server.URL, harness: harness}

	interceptors := []grpc.UnaryServerInterceptor{
		tracing.GRPCUnaryServerInterceptor(harness.Provider, harness.Propagator),
	}
	interceptors = append(interceptors, extraInterceptors...)

	grpcHarness := testutil.NewGRPCBufConnHarness(
		grpc.ChainUnaryInterceptor(interceptors...),
	)
	t.Cleanup(grpcHarness.Close)

	pb.RegisterSystemServiceServer(grpcHarness.Server, server)
	grpcHarness.Serve()

	conn, err := grpcHarness.Dial(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewSystemServiceClient(conn)

	return &grpcTestSetup{
		Harness:    harness,
		Downstream: downstream,
		Client:     client,
		Server:     server,
	}
}

func TestGRPCInboundAbsentTraceparent(t *testing.T) {
	setup := setupGRPCTest(t)

	resp, err := setup.Client.GetHealth(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, "SERVING", resp.Status)

	// Positive assertion: verify server and downstream were actually called
	require.Equal(t, 1, setup.Server.callCount, "Expected gRPC server handler to be executed")
	require.Equal(t, 1, setup.Downstream.CallCount(), "Expected downstream HTTP service to be called")

	// Negative assertions on executed path
	spans := setup.Harness.Exporter.GetSpans()
	require.Empty(t, spans, "Expected no spans exported for untraced gRPC request")

	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)
	require.Empty(t, lastHeader.Get("Traceparent"), "Expected no outbound traceparent header")
}

func TestGRPCInboundInvalidGarbageTraceparent(t *testing.T) {
	testCases := []struct {
		name        string
		traceparent string
	}{
		{name: "truncated", traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736"},
		{name: "non-hex", traceparent: "00-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz-00f067aa0ba902b7-01"},
		{name: "all-zero-traceid", traceparent: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			setup := setupGRPCTest(t)

			ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", tc.traceparent))
			resp, err := setup.Client.GetHealth(ctx, &emptypb.Empty{})
			require.NoError(t, err)
			require.Equal(t, "SERVING", resp.Status)

			require.Equal(t, 1, setup.Server.callCount)
			require.Equal(t, 1, setup.Downstream.CallCount())

			spans := setup.Harness.Exporter.GetSpans()
			require.Empty(t, spans)

			lastHeader := setup.Downstream.LastHeader()
			require.NotNil(t, lastHeader)
			require.Empty(t, lastHeader.Get("Traceparent"))
		})
	}
}

func TestGRPCInboundValidUnsampledParent(t *testing.T) {
	setup := setupGRPCTest(t)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewUnsampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	resp, err := setup.Client.GetHealth(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, "SERVING", resp.Status)

	require.Equal(t, 1, setup.Server.callCount)
	require.Equal(t, 1, setup.Downstream.CallCount())

	// 1. Zero spans exported locally
	spans := setup.Harness.Exporter.GetSpans()
	require.Empty(t, spans, "Expected zero spans exported for unsampled parent")

	// 2. Outbound traceparent propagated with matching trace ID and sampled=0
	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)
	outboundTraceparent := lastHeader.Get("Traceparent")
	require.NotEmpty(t, outboundTraceparent)
	require.Contains(t, outboundTraceparent, traceID)
	require.True(t, outboundTraceparent[len(outboundTraceparent)-2:] == "00")
}

func TestGRPCInboundValidSampledParent(t *testing.T) {
	setup := setupGRPCTest(t)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	resp, err := setup.Client.GetHealth(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, "SERVING", resp.Status)

	require.Equal(t, 1, setup.Server.callCount)
	require.Equal(t, 1, setup.Downstream.CallCount())

	spans := setup.Harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan)

	require.Equal(t, traceID, serverSpan.SpanContext.TraceID().String())
	require.Equal(t, parentSpanID, serverSpan.Parent.SpanID().String())
	require.NotEqual(t, parentSpanID, serverSpan.SpanContext.SpanID().String())

	// Outbound client span parented to server span
	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)
	outboundTraceparent := lastHeader.Get("Traceparent")
	require.NotEmpty(t, outboundTraceparent)
	require.Contains(t, outboundTraceparent, traceID)
	require.True(t, outboundTraceparent[len(outboundTraceparent)-2:] == "01")
}

func TestGRPCInboundOperationEventTraceContext(t *testing.T) {
	var capturedSnapshot testutil.OperationSnapshotWrapper
	opInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		event := operationevent.New("grpc "+info.FullMethod, operationevent.WithEmitter(func(msg string, lvl operationevent.Level, s operationevent.Snapshot) {
			capturedSnapshot.Record(s)
		}))
		ctx = operationevent.WithContext(ctx, event)
		resp, err := handler(ctx, req)
		event.SetTraceContext(tracing.TraceContextFromContext(ctx))
		event.EmitTerminal(operationevent.ResultSuccess)
		return resp, err
	}

	setup := setupGRPCTest(t, opInterceptor)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	_, err := setup.Client.GetHealth(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	require.Equal(t, 1, capturedSnapshot.Count(), "Expected OperationEvent to emit exactly 1 terminal snapshot")
	spans := setup.Harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan)

	snap := capturedSnapshot.GetSnapshot()
	require.Equal(t, traceID, snap.TraceID)
	require.Equal(t, serverSpan.SpanContext.SpanID().String(), snap.SpanID)
}

func TestGRPCInboundOperationEventUntracedOmitsTraceContext(t *testing.T) {
	var capturedSnapshot testutil.OperationSnapshotWrapper
	var h *testutil.Harness
	opInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		event := operationevent.New("grpc "+info.FullMethod, operationevent.WithEmitter(func(msg string, lvl operationevent.Level, s operationevent.Snapshot) {
			capturedSnapshot.Record(s)
		}))
		ctx = operationevent.WithContext(ctx, event)
		// Start an internal span to verify TraceContextFromContext does not extract SDK-minted non-participating trace ID
		tr := h.Provider.Tracer("internal")
		internalCtx, span := tr.Start(ctx, "internal.subtask")
		defer span.End()
		resp, err := handler(internalCtx, req)
		event.SetTraceContext(tracing.TraceContextFromContext(internalCtx))
		event.EmitTerminal(operationevent.ResultSuccess)
		return resp, err
	}

	setup := setupGRPCTest(t, opInterceptor)
	h = setup.Harness

	_, err := setup.Client.GetHealth(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)

	require.Equal(t, 1, capturedSnapshot.Count(), "Expected OperationEvent to emit exactly 1 terminal snapshot")
	snap := capturedSnapshot.GetSnapshot()
	require.Empty(t, snap.TraceID, "Untraced gRPC request must have empty traceID in OperationEvent")
	require.Empty(t, snap.SpanID, "Untraced gRPC request must have empty spanID in OperationEvent")
}

func TestGRPCInboundSemconvAttributes(t *testing.T) {
	setup := setupGRPCTest(t)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	_, err := setup.Client.GetHealth(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	spans := setup.Harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan)

	require.Equal(t, "/memory.v1.SystemService/GetHealth", serverSpan.Name)

	attrs := make(map[string]any)
	for _, kv := range serverSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}

	require.Equal(t, "grpc", attrs["rpc.system"])
	require.Equal(t, "memory.v1.SystemService", attrs["rpc.service"])
	require.Equal(t, "GetHealth", attrs["rpc.method"])
	require.Equal(t, int64(0), attrs["rpc.grpc.status_code"])
	require.Equal(t, codes.Unset, serverSpan.Status.Code)
}

// TestGRPCInboundHandlerErrorNonOKStatus verifies the unary interceptor's error branch:
// when a sampled parent is present and the handler returns a non-OK gRPC status, the
// interceptor must set rpc.grpc.status_code to the non-zero code, set span status to
// codes.Error, and record the error via RecordError (which produces a span event).
//
// Note on errorDetail span events: enrichSpanFromSnapshot reads operationevent.FromContext(ctx)
// where ctx is the tracing interceptor's local span-context variable.  Because the operation
// event interceptor runs downstream (position 2), its WithContext call never updates the
// tracing interceptor's closed-over ctx.  errorDetail events are therefore only reachable
// through the HTTP path (where the tracing middleware re-reads c.Request.Context() in its
// defer, which does see downstream middleware mutations).  That asymmetry is a separate issue.
func TestGRPCInboundHandlerErrorNonOKStatus(t *testing.T) {
	// Return ResourceExhausted (code 8) directly from the test server; no extra interceptor needed.
	setup := setupGRPCTest(t, func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
		return nil, status.Error(grpccodes.ResourceExhausted, "quota exceeded")
	})

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	_, err := setup.Client.GetHealth(ctx, &emptypb.Empty{})
	require.Error(t, err)

	spans := setup.Harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan, "server span must be exported on error path")

	// rpc.grpc.status_code must carry the non-zero gRPC code (ResourceExhausted = 8).
	attrs := make(map[string]any)
	for _, kv := range serverSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}
	require.Equal(t, int64(grpccodes.ResourceExhausted), attrs["rpc.grpc.status_code"],
		"rpc.grpc.status_code must be non-zero (ResourceExhausted) on error")

	// Span status must be Error.
	require.Equal(t, codes.Error, serverSpan.Status.Code,
		"span status must be codes.Error when handler returns non-OK")

	// RecordError must have produced at least one span event.
	require.NotEmpty(t, serverSpan.Events,
		"span must carry at least one event from RecordError on the error path")
}

// TestGRPCInboundHandlerErrorWithOperationEventErrorDetails verifies that when an
// OperationEvent carrying ErrorDetails IS reachable from the tracing interceptor's ctx
// (i.e., placed upstream via a pre-tracing interceptor), the errorDetails are emitted
// as span events named "memoryservice.errorDetail".
func TestGRPCInboundHandlerErrorWithOperationEventErrorDetails(t *testing.T) {
	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	// Inject an OperationEvent with ErrorDetails into ctx *before* the tracing
	// interceptor sees it, by wrapping the GRPCBufConnHarness chain manually.
	event := operationevent.New("grpc /test")
	event.EnrichError(operationevent.WithErrorDetails(
		fmt.Errorf("provider failure"),
		operationevent.ErrorDetails{
			ErrorType: "provider",
			ErrorCode: "quota_exceeded",
			Reason:    "rate limit",
		},
	))

	// Pre-tracing interceptor: puts the event on ctx, then delegates to tracing interceptor.
	preTracingInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = operationevent.WithContext(ctx, event)
		// Chain continues into the tracing interceptor via handler.
		return handler(ctx, req)
	}

	// Build the server with preTracing -> tracing chain.
	grpcHarness := testutil.NewGRPCBufConnHarness(
		grpc.ChainUnaryInterceptor(
			preTracingInterceptor,
			tracing.GRPCUnaryServerInterceptor(harness.Provider, harness.Propagator),
			func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
				return nil, status.Error(grpccodes.Internal, "downstream failed")
			},
		),
	)
	t.Cleanup(grpcHarness.Close)

	pb.RegisterSystemServiceServer(grpcHarness.Server, &pb.UnimplementedSystemServiceServer{})
	grpcHarness.Serve()

	conn, err := grpcHarness.Dial(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	rpcCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	client := pb.NewSystemServiceClient(conn)
	_, rpcErr := client.GetHealth(rpcCtx, &emptypb.Empty{})
	require.Error(t, rpcErr)

	spans := harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan)

	require.Equal(t, codes.Error, serverSpan.Status.Code)

	var hasErrorDetailEvent bool
	for _, ev := range serverSpan.Events {
		if ev.Name == "memoryservice.errorDetail" {
			hasErrorDetailEvent = true
			break
		}
	}
	require.True(t, hasErrorDetailEvent,
		"span must carry memoryservice.errorDetail event when OperationEvent is on ctx upstream of tracing interceptor")
}
