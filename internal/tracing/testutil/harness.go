package testutil

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/chirino/memory-service/internal/tracing"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Harness holds test instrumentation components for verifying OpenTelemetry trace participation.
type Harness struct {
	Exporter   *tracetest.InMemoryExporter
	Provider   *sdktrace.TracerProvider
	Propagator propagation.TextMapPropagator
	Shutdown   func(context.Context) error

	RecordedLogs []RecordedLog
	logsMu       sync.Mutex
	logEmitCount int
}

// RecordedLog captures structured log entries emitted during testing.
type RecordedLog struct {
	Message string
	Args    []any
}

// NewTestHarness creates a new shared test harness with ParentBased(NeverSample()) sampling and ParticipatingPropagator.
func NewTestHarness() *Harness {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.NeverSample())),
	)
	baseProp := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	prop := tracing.NewParticipatingPropagator(baseProp)

	h := &Harness{
		Exporter:   exporter,
		Provider:   tp,
		Propagator: prop,
		Shutdown:   tp.Shutdown,
	}
	return h
}

// RecordLog appends an intercepted log entry to the harness.
func (h *Harness) RecordLog(msg string, args ...any) {
	h.logsMu.Lock()
	defer h.logsMu.Unlock()
	h.logEmitCount++
	h.RecordedLogs = append(h.RecordedLogs, RecordedLog{
		Message: msg,
		Args:    append([]any(nil), args...),
	})
}

// LogEmitCount returns the number of log entries recorded.
func (h *Harness) LogEmitCount() int {
	h.logsMu.Lock()
	defer h.logsMu.Unlock()
	return h.logEmitCount
}

// Logs returns a snapshot copy of recorded log entries.
func (h *Harness) Logs() []RecordedLog {
	h.logsMu.Lock()
	defer h.logsMu.Unlock()
	return append([]RecordedLog(nil), h.RecordedLogs...)
}

// NewSampledTraceparent creates a standard W3C traceparent string with sampled=01.
func NewSampledTraceparent(traceIDHex, parentSpanIDHex string) string {
	return "00-" + traceIDHex + "-" + parentSpanIDHex + "-01"
}

// NewUnsampledTraceparent creates a standard W3C traceparent string with sampled=00.
func NewUnsampledTraceparent(traceIDHex, parentSpanIDHex string) string {
	return "00-" + traceIDHex + "-" + parentSpanIDHex + "-00"
}

// DownstreamRecorder creates an httptest.Server that records all incoming HTTP headers.
type DownstreamRecorder struct {
	Server          *httptest.Server
	ReceivedHeaders []http.Header
	callCount       int
	mu              sync.Mutex
}

// NewDownstreamRecorder initializes and starts an httptest.Server recording headers.
func NewDownstreamRecorder() *DownstreamRecorder {
	return newDownstreamRecorder("")
}

// NewDownstreamRecorderWithBody initializes a recorder whose server responds with
// the given fixed body (Content-Type: application/json, status 200) on every request.
// Use this instead of mutating Server.Config.Handler after construction.
func NewDownstreamRecorderWithBody(body string) *DownstreamRecorder {
	return newDownstreamRecorder(body)
}

func newDownstreamRecorder(body string) *DownstreamRecorder {
	rec := &DownstreamRecorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.callCount++
		rec.ReceivedHeaders = append(rec.ReceivedHeaders, r.Header.Clone())
		rec.mu.Unlock()
		if body != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(body))
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	return rec
}

// CallCount returns the total number of requests received by the downstream server.
func (d *DownstreamRecorder) CallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.callCount
}

// LastHeader returns the most recently received request header, if any.
func (d *DownstreamRecorder) LastHeader() http.Header {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.ReceivedHeaders) == 0 {
		return nil
	}
	return d.ReceivedHeaders[len(d.ReceivedHeaders)-1]
}

// Close shuts down the recorded server.
func (d *DownstreamRecorder) Close() {
	if d.Server != nil {
		d.Server.Close()
	}
}

const bufSize = 1024 * 1024

// GRPCBufConnHarness provides an in-memory bufconn listener and dialer for gRPC testing.
// Since bufconn listener is synchronous and in-memory, Dial() connects via the pre-allocated
// listener buffer without needing an asynchronous network readiness handshake.
type GRPCBufConnHarness struct {
	Listener *bufconn.Listener
	Server   *grpc.Server
}

// NewGRPCBufConnHarness creates a new bufconn listener and gRPC server.
func NewGRPCBufConnHarness(serverOpts ...grpc.ServerOption) *GRPCBufConnHarness {
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer(serverOpts...)
	return &GRPCBufConnHarness{
		Listener: lis,
		Server:   srv,
	}
}

// Serve starts the gRPC server on the bufconn listener in a background goroutine.
// bufconn buffers in-memory, so client connections can immediately dial without readiness waiting.
func (g *GRPCBufConnHarness) Serve() {
	go func() {
		_ = g.Server.Serve(g.Listener)
	}()
}

// Dial creates a client connection to the in-memory bufconn server.
func (g *GRPCBufConnHarness) Dial(ctx context.Context, dialOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts := append([]grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return g.Listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, dialOpts...)
	return grpc.NewClient("passthrough://bufnet", opts...)
}

// Close cleans up the server and listener.
func (g *GRPCBufConnHarness) Close() {
	if g.Server != nil {
		g.Server.Stop()
	}
	if g.Listener != nil {
		_ = g.Listener.Close()
	}
}

// NewGinTestRouter creates an engine with the given middlewares in test mode.
func NewGinTestRouter(middlewares ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, m := range middlewares {
		r.Use(m)
	}
	return r
}

// OperationSnapshotWrapper holds a captured OperationEvent snapshot and count.
type OperationSnapshotWrapper struct {
	Snapshot  operationevent.Snapshot
	EmitCount int
	mu        sync.Mutex
}

// Record captures an emitted snapshot.
func (w *OperationSnapshotWrapper) Record(s operationevent.Snapshot) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.EmitCount++
	w.Snapshot = s
}

// GetSnapshot returns a copy of the captured snapshot.
func (w *OperationSnapshotWrapper) GetSnapshot() operationevent.Snapshot {
	if w == nil {
		return operationevent.Snapshot{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Snapshot
}

// Count returns the emit count.
func (w *OperationSnapshotWrapper) Count() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.EmitCount
}

// OperationEventCaptureMiddleware captures the OperationEvent snapshot on terminal emit.
func OperationEventCaptureMiddleware(target *OperationSnapshotWrapper) gin.HandlerFunc {
	return func(c *gin.Context) {
		event := operationevent.New(c.Request.Method+" "+c.Request.URL.Path, operationevent.WithEmitter(func(msg string, lvl operationevent.Level, s operationevent.Snapshot) {
			if target != nil {
				target.Record(s)
			}
		}))
		c.Request = c.Request.WithContext(operationevent.WithContext(c.Request.Context(), event))
		c.Next()
		if reqCtx := c.Request.Context(); reqCtx != nil {
			event.SetTraceContext(tracing.TraceContextFromContext(reqCtx))
		}
		status := c.Writer.Status()
		event.SetHTTPStatus(status)
		event.EmitTerminal(operationevent.ResultFromHTTP(status, c.Request.Context().Err()))
	}
}
