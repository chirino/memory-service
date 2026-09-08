package tracing_test

import (
	"context"
	"fmt"
	"io"
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
)

// testEventStreamServer is a minimal EventStreamService implementation for testing.
// SubscribeEvents sends one EventNotification then returns, closing the stream cleanly.
// The downstream HTTP call from the handler lets tests inspect outbound propagation.
type testEventStreamServer struct {
	pb.UnimplementedEventStreamServiceServer
	downstreamURL string
	harness       *testutil.Harness
	callCount     int
}

func (s *testEventStreamServer) SubscribeEvents(
	req *pb.SubscribeEventsRequest,
	stream grpc.ServerStreamingServer[pb.EventNotification],
) error {
	s.callCount++
	ctx := stream.Context()
	client := &http.Client{
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithTracerProvider(s.harness.Provider),
			otelhttp.WithPropagators(s.harness.Propagator),
		),
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.downstreamURL, nil)
	if err == nil {
		resp, err := client.Do(httpReq)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
	return stream.Send(&pb.EventNotification{})
}

type grpcStreamTestSetup struct {
	Harness    *testutil.Harness
	Downstream *testutil.DownstreamRecorder
	Client     pb.EventStreamServiceClient
	Server     *testEventStreamServer
}

func setupGRPCStreamTest(t *testing.T, extraInterceptors ...grpc.StreamServerInterceptor) *grpcStreamTestSetup {
	t.Helper()
	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	downstream := testutil.NewDownstreamRecorder()
	t.Cleanup(downstream.Close)

	server := &testEventStreamServer{downstreamURL: downstream.Server.URL, harness: harness}

	interceptors := []grpc.StreamServerInterceptor{
		tracing.GRPCStreamServerInterceptor(harness.Provider, harness.Propagator),
	}
	interceptors = append(interceptors, extraInterceptors...)

	grpcHarness := testutil.NewGRPCBufConnHarness(
		grpc.ChainStreamInterceptor(interceptors...),
	)
	t.Cleanup(grpcHarness.Close)

	pb.RegisterEventStreamServiceServer(grpcHarness.Server, server)
	grpcHarness.Serve()

	conn, err := grpcHarness.Dial(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := pb.NewEventStreamServiceClient(conn)

	return &grpcStreamTestSetup{
		Harness:    harness,
		Downstream: downstream,
		Client:     client,
		Server:     server,
	}
}

// drainStream reads all messages from a server-streaming call until EOF.
func drainStream(stream grpc.ServerStreamingClient[pb.EventNotification]) error {
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func TestGRPCStreamInboundAbsentTraceparent(t *testing.T) {
	setup := setupGRPCStreamTest(t)

	stream, err := setup.Client.SubscribeEvents(context.Background(), &pb.SubscribeEventsRequest{})
	require.NoError(t, err)
	require.NoError(t, drainStream(stream))

	// Positive assertion: server handler and downstream were called.
	require.Equal(t, 1, setup.Server.callCount)
	require.Equal(t, 1, setup.Downstream.CallCount())

	// Negative assertions on executed path.
	spans := setup.Harness.Exporter.GetSpans()
	require.Empty(t, spans, "no spans must be exported for untraced stream request")

	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)
	require.Empty(t, lastHeader.Get("Traceparent"), "no outbound traceparent for untraced request")
}

func TestGRPCStreamInboundInvalidGarbageTraceparent(t *testing.T) {
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
			setup := setupGRPCStreamTest(t)

			ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", tc.traceparent))
			stream, err := setup.Client.SubscribeEvents(ctx, &pb.SubscribeEventsRequest{})
			require.NoError(t, err)
			require.NoError(t, drainStream(stream))

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

func TestGRPCStreamInboundValidUnsampledParent(t *testing.T) {
	setup := setupGRPCStreamTest(t)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewUnsampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	stream, err := setup.Client.SubscribeEvents(ctx, &pb.SubscribeEventsRequest{})
	require.NoError(t, err)
	require.NoError(t, drainStream(stream))

	require.Equal(t, 1, setup.Server.callCount)
	require.Equal(t, 1, setup.Downstream.CallCount())

	// Zero spans exported locally.
	spans := setup.Harness.Exporter.GetSpans()
	require.Empty(t, spans, "no spans for unsampled parent")

	// Outbound traceparent propagated with matching trace ID and sampled=00.
	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)
	outbound := lastHeader.Get("Traceparent")
	require.NotEmpty(t, outbound)
	require.Contains(t, outbound, traceID)
	require.Equal(t, "00", outbound[len(outbound)-2:])
}

func TestGRPCStreamInboundValidSampledParent(t *testing.T) {
	setup := setupGRPCStreamTest(t)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	stream, err := setup.Client.SubscribeEvents(ctx, &pb.SubscribeEventsRequest{})
	require.NoError(t, err)
	require.NoError(t, drainStream(stream))

	require.Equal(t, 1, setup.Server.callCount)
	require.Equal(t, 1, setup.Downstream.CallCount())

	spans := setup.Harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan)

	require.Equal(t, traceID, serverSpan.SpanContext.TraceID().String())
	require.Equal(t, parentSpanID, serverSpan.Parent.SpanID().String())
	require.NotEqual(t, parentSpanID, serverSpan.SpanContext.SpanID().String())

	// Outbound client span carries same trace ID and sampled=01.
	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)
	outbound := lastHeader.Get("Traceparent")
	require.NotEmpty(t, outbound)
	require.Contains(t, outbound, traceID)
	require.Equal(t, "01", outbound[len(outbound)-2:])
}

func TestGRPCStreamInboundOperationEventTraceContext(t *testing.T) {
	var capturedSnapshot testutil.OperationSnapshotWrapper
	opInterceptor := func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := stream.Context()
		event := operationevent.New("stream "+info.FullMethod, operationevent.WithEmitter(func(msg string, lvl operationevent.Level, s operationevent.Snapshot) {
			capturedSnapshot.Record(s)
		}))
		ctx = operationevent.WithContext(ctx, event)
		err := handler(srv, &contextOverrideStream{ServerStream: stream, ctx: ctx})
		event.SetTraceContext(tracing.TraceContextFromContext(ctx))
		event.EmitTerminal(operationevent.ResultSuccess)
		return err
	}

	setup := setupGRPCStreamTest(t, opInterceptor)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	stream, err := setup.Client.SubscribeEvents(ctx, &pb.SubscribeEventsRequest{})
	require.NoError(t, err)
	require.NoError(t, drainStream(stream))

	require.Equal(t, 1, capturedSnapshot.Count())
	spans := setup.Harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan)

	snap := capturedSnapshot.GetSnapshot()
	require.Equal(t, traceID, snap.TraceID)
	require.Equal(t, serverSpan.SpanContext.SpanID().String(), snap.SpanID)
}

func TestGRPCStreamInboundOperationEventUntracedOmitsTraceContext(t *testing.T) {
	var capturedSnapshot testutil.OperationSnapshotWrapper
	var h *testutil.Harness
	opInterceptor := func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := stream.Context()
		event := operationevent.New("stream "+info.FullMethod, operationevent.WithEmitter(func(msg string, lvl operationevent.Level, s operationevent.Snapshot) {
			capturedSnapshot.Record(s)
		}))
		ctx = operationevent.WithContext(ctx, event)
		// Start an internal span to verify TraceContextFromContext does not extract
		// the SDK-minted non-participating trace ID.
		tr := h.Provider.Tracer("internal")
		internalCtx, span := tr.Start(ctx, "internal.stream")
		defer span.End()
		err := handler(srv, &contextOverrideStream{ServerStream: stream, ctx: internalCtx})
		event.SetTraceContext(tracing.TraceContextFromContext(internalCtx))
		event.EmitTerminal(operationevent.ResultSuccess)
		return err
	}

	setup := setupGRPCStreamTest(t, opInterceptor)
	h = setup.Harness

	stream, err := setup.Client.SubscribeEvents(context.Background(), &pb.SubscribeEventsRequest{})
	require.NoError(t, err)
	require.NoError(t, drainStream(stream))

	require.Equal(t, 1, capturedSnapshot.Count())
	snap := capturedSnapshot.GetSnapshot()
	require.Empty(t, snap.TraceID, "untraced stream must have empty traceID in OperationEvent")
	require.Empty(t, snap.SpanID, "untraced stream must have empty spanID in OperationEvent")
}

func TestGRPCStreamInboundSemconvAttributes(t *testing.T) {
	setup := setupGRPCStreamTest(t)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	stream, err := setup.Client.SubscribeEvents(ctx, &pb.SubscribeEventsRequest{})
	require.NoError(t, err)
	require.NoError(t, drainStream(stream))

	spans := setup.Harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan)

	require.Equal(t, "/memory.v1.EventStreamService/SubscribeEvents", serverSpan.Name)

	attrs := make(map[string]any)
	for _, kv := range serverSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}

	require.Equal(t, "grpc", attrs["rpc.system"])
	require.Equal(t, "memory.v1.EventStreamService", attrs["rpc.service"])
	require.Equal(t, "SubscribeEvents", attrs["rpc.method"])
	require.Equal(t, int64(0), attrs["rpc.grpc.status_code"])
	require.Equal(t, codes.Unset, serverSpan.Status.Code)
}

func TestGRPCStreamInboundNoPhantomTraceparentWhenUntraced(t *testing.T) {
	setup := setupGRPCStreamTest(t)

	stream, err := setup.Client.SubscribeEvents(context.Background(), &pb.SubscribeEventsRequest{})
	require.NoError(t, err)
	require.NoError(t, drainStream(stream))

	require.Equal(t, 1, setup.Downstream.CallCount())
	lastHeader := setup.Downstream.LastHeader()
	require.NotNil(t, lastHeader)
	require.Empty(t, lastHeader.Get("Traceparent"),
		"untraced stream must not inject phantom outbound traceparent")
}

// contextOverrideStream wraps a ServerStream replacing its context.
// Used in stream interceptor tests to inject an OperationEvent into the stream context.
type contextOverrideStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextOverrideStream) Context() context.Context {
	return s.ctx
}

// Ensure the stream interceptor test handler gets the right context.
// This is the same wrapper used inside GRPCStreamServerInterceptor itself.
var _ grpc.ServerStream = (*contextOverrideStream)(nil)

// TestGRPCStreamInboundHandlerErrorNonOKStatus verifies the stream interceptor's error
// branch: when a sampled parent is present and the stream handler returns a non-OK gRPC
// status, the interceptor must set rpc.grpc.status_code to the non-zero code, set span
// status to codes.Error, and record the error via RecordError.
//
// Note on errorDetail span events: same asymmetry as on the unary path applies here.
// See TestGRPCInboundHandlerErrorWithOperationEventErrorDetails for the upstream-event variant.
func TestGRPCStreamInboundHandlerErrorNonOKStatus(t *testing.T) {
	// Extra stream interceptor that replaces the handler with one that returns an error.
	errorInterceptor := grpc.StreamServerInterceptor(func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, _ grpc.StreamHandler) error {
		return status.Error(grpccodes.ResourceExhausted, "quota exceeded")
	})
	setup := setupGRPCStreamTest(t, errorInterceptor)

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	stream, err := setup.Client.SubscribeEvents(ctx, &pb.SubscribeEventsRequest{})
	require.NoError(t, err)
	err = drainStream(stream)
	require.Error(t, err, "stream must return the handler's error to the client")

	spans := setup.Harness.Exporter.GetSpans()
	serverSpan := findServerSpan(spans)
	require.NotNil(t, serverSpan, "server span must be exported on stream error path")

	// rpc.grpc.status_code must carry the non-zero gRPC code.
	attrs := make(map[string]any)
	for _, kv := range serverSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}
	require.Equal(t, int64(grpccodes.ResourceExhausted), attrs["rpc.grpc.status_code"],
		"rpc.grpc.status_code must be non-zero (ResourceExhausted) on stream error")

	// Span status must be Error.
	require.Equal(t, codes.Error, serverSpan.Status.Code,
		"span status must be codes.Error when stream handler returns non-OK")

	// RecordError must have produced at least one span event.
	require.NotEmpty(t, serverSpan.Events,
		"span must carry at least one event from RecordError on the stream error path")
}

// TestGRPCStreamInboundHandlerErrorWithOperationEventErrorDetails verifies that when an
// OperationEvent carrying ErrorDetails is placed on the context upstream of the stream
// tracing interceptor, the errorDetails appear as span events named "memoryservice.errorDetail".
func TestGRPCStreamInboundHandlerErrorWithOperationEventErrorDetails(t *testing.T) {
	harness := testutil.NewTestHarness()
	t.Cleanup(func() { _ = harness.Shutdown(context.Background()) })

	event := operationevent.New("grpc /test/stream")
	event.EnrichError(operationevent.WithErrorDetails(
		fmt.Errorf("provider failure"),
		operationevent.ErrorDetails{
			ErrorType: "provider",
			ErrorCode: "quota_exceeded",
			Reason:    "rate limit",
		},
	))

	// Pre-tracing stream interceptor: injects the event before the tracing interceptor.
	preTracingInterceptor := grpc.StreamServerInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := operationevent.WithContext(stream.Context(), event)
		return handler(srv, &contextOverrideStream{ServerStream: stream, ctx: ctx})
	})

	grpcHarness := testutil.NewGRPCBufConnHarness(
		grpc.ChainStreamInterceptor(
			preTracingInterceptor,
			tracing.GRPCStreamServerInterceptor(harness.Provider, harness.Propagator),
			// Terminal interceptor: returns a non-OK error so the span records it.
			grpc.StreamServerInterceptor(func(_ any, _ grpc.ServerStream, _ *grpc.StreamServerInfo, _ grpc.StreamHandler) error {
				return status.Error(grpccodes.Internal, "downstream stream failed")
			}),
		),
	)
	t.Cleanup(grpcHarness.Close)

	pb.RegisterEventStreamServiceServer(grpcHarness.Server, &pb.UnimplementedEventStreamServiceServer{})
	grpcHarness.Serve()

	conn, err := grpcHarness.Dial(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	parentSpanID := "00f067aa0ba902b7"
	inboundTraceparent := testutil.NewSampledTraceparent(traceID, parentSpanID)

	rpcCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", inboundTraceparent))
	client := pb.NewEventStreamServiceClient(conn)
	rpcStream, rpcErr := client.SubscribeEvents(rpcCtx, &pb.SubscribeEventsRequest{})
	require.NoError(t, rpcErr)
	require.Error(t, drainStream(rpcStream))

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
		"span must carry memoryservice.errorDetail event when OperationEvent is upstream of stream tracing interceptor")
}
