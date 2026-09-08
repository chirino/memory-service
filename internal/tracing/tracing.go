package tracing

import (
	"context"
	"fmt"
	"strconv"

	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type traceParticipationKey struct{}
type tracerProviderKey struct{}

// WithProviderContext stores the given TracerProvider in ctx so plugin loaders
// can retrieve it via ProviderFromContext.
func WithProviderContext(ctx context.Context, tp trace.TracerProvider) context.Context {
	return context.WithValue(ctx, tracerProviderKey{}, tp)
}

// ProviderFromContext retrieves the TracerProvider stored by WithProviderContext.
// Returns nil if none was stored.
func ProviderFromContext(ctx context.Context) trace.TracerProvider {
	tp, _ := ctx.Value(tracerProviderKey{}).(trace.TracerProvider)
	return tp
}

// ProviderFromContextOrNoop retrieves the TracerProvider stored by WithProviderContext.
// Returns a noop TracerProvider if ctx is nil or none was stored.
func ProviderFromContextOrNoop(ctx context.Context) trace.TracerProvider {
	if ctx == nil {
		return nooptrace.NewTracerProvider()
	}
	tp := ProviderFromContext(ctx)
	if tp == nil {
		return nooptrace.NewTracerProvider()
	}
	return tp
}

// MarkParticipating marks the context as an active participant in an upstream trace.
func MarkParticipating(ctx context.Context) context.Context {
	return context.WithValue(ctx, traceParticipationKey{}, true)
}

// IsParticipating returns true if the context is participating in an upstream trace.
func IsParticipating(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	val, ok := ctx.Value(traceParticipationKey{}).(bool)
	return ok && val
}

// ParticipatingPropagator wraps a TextMapPropagator, making Inject a no-op unless IsParticipating(ctx) is true.
type ParticipatingPropagator struct {
	base propagation.TextMapPropagator
}

// NewParticipatingPropagator creates a new ParticipatingPropagator.
func NewParticipatingPropagator(base propagation.TextMapPropagator) propagation.TextMapPropagator {
	return &ParticipatingPropagator{base: base}
}

// Inject writes the trace context if the context is participating in an upstream trace.
func (p *ParticipatingPropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	if !IsParticipating(ctx) {
		return
	}
	p.base.Inject(ctx, carrier)
}

// Extract delegates directly to the underlying propagator.
func (p *ParticipatingPropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	return p.base.Extract(ctx, carrier)
}

// Fields returns the underlying propagator fields.
func (p *ParticipatingPropagator) Fields() []string {
	return p.base.Fields()
}

// TraceContextFromContext returns the active traceID and spanID only if the context is participating and valid.
func TraceContextFromContext(ctx context.Context) (traceID string, spanID string) {
	if !IsParticipating(ctx) {
		return "", ""
	}
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}

// HTTPMiddleware creates a Gin middleware for passive OpenTelemetry trace participation.
func HTTPMiddleware(tp trace.TracerProvider, propagator propagation.TextMapPropagator) gin.HandlerFunc {
	tracer := tp.Tracer("memory-service/http")

	return func(c *gin.Context) {
		extractedCtx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))
		spanCtx := trace.SpanContextFromContext(extractedCtx)

		// Branch 1: Absent or invalid traceparent
		if !spanCtx.IsValid() {
			c.Next()
			return
		}

		// Mark context as participating since we received a valid remote parent
		participatingCtx := MarkParticipating(extractedCtx)

		// Branch 2: Valid but NOT sampled (traceparent flags != 01)
		if !spanCtx.IsSampled() {
			c.Request = c.Request.WithContext(participatingCtx)
			c.Next()
			return
		}

		// Branch 3: Valid and sampled (traceparent flags == 01)
		spanName := c.FullPath()
		if spanName == "" {
			spanName = "HTTP " + c.Request.Method
		} else {
			spanName = c.Request.Method + " " + spanName
		}

		ctx, span := tracer.Start(
			participatingCtx,
			spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", c.Request.Method),
				attribute.String("url.path", c.Request.URL.Path),
			),
		)
		defer func() {
			status := c.Writer.Status()
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if len(c.Errors) > 0 {
				span.RecordError(c.Errors.Last().Err)
				span.SetStatus(codes.Error, c.Errors.Last().Error())
			} else if status >= 400 {
				span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", status))
			}
			if event := operationevent.FromContext(c.Request.Context()); event != nil {
				enrichSpanFromSnapshot(span, event.Snapshot())
			}
			span.End()
		}()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// GRPCUnaryServerInterceptor returns a unary server interceptor for passive OpenTelemetry trace participation.
func GRPCUnaryServerInterceptor(tp trace.TracerProvider, propagator propagation.TextMapPropagator) grpc.UnaryServerInterceptor {
	tracer := tp.Tracer("memory-service/grpc")

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, retErr error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.MD{}
		}
		extractedCtx := propagator.Extract(ctx, &metadataCarrier{md: md})
		spanCtx := trace.SpanContextFromContext(extractedCtx)

		// Branch 1: Absent or invalid traceparent
		if !spanCtx.IsValid() {
			return handler(ctx, req)
		}

		// Mark context as participating since we received a valid remote parent
		participatingCtx := MarkParticipating(extractedCtx)

		// Branch 2: Valid but NOT sampled (traceparent flags != 01)
		if !spanCtx.IsSampled() {
			return handler(participatingCtx, req)
		}

		// Branch 3: Valid and sampled (traceparent flags == 01)
		spanName := info.FullMethod
		ctx, span := tracer.Start(
			participatingCtx,
			spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.service", grpcServiceFromMethod(info.FullMethod)),
				attribute.String("rpc.method", grpcMethodFromMethod(info.FullMethod)),
			),
		)
		defer func() {
			if retErr != nil {
				st, _ := status.FromError(retErr)
				span.SetAttributes(attribute.Int64("rpc.grpc.status_code", int64(st.Code())))
				span.RecordError(retErr)
				span.SetStatus(codes.Error, retErr.Error())
			} else {
				span.SetAttributes(attribute.Int64("rpc.grpc.status_code", 0))
			}
			if event := operationevent.FromContext(ctx); event != nil {
				enrichSpanFromSnapshot(span, event.Snapshot())
			}
			span.End()
		}()

		return handler(ctx, req)
	}
}

// GRPCStreamServerInterceptor returns a stream server interceptor for passive OpenTelemetry trace participation.
func GRPCStreamServerInterceptor(tp trace.TracerProvider, propagator propagation.TextMapPropagator) grpc.StreamServerInterceptor {
	tracer := tp.Tracer("memory-service/grpc")

	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (retErr error) {
		ctx := stream.Context()
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.MD{}
		}
		extractedCtx := propagator.Extract(ctx, &metadataCarrier{md: md})
		spanCtx := trace.SpanContextFromContext(extractedCtx)

		// Branch 1: Absent or invalid traceparent
		if !spanCtx.IsValid() {
			return handler(srv, stream)
		}

		// Mark context as participating
		participatingCtx := MarkParticipating(extractedCtx)

		// Branch 2: Valid but NOT sampled
		if !spanCtx.IsSampled() {
			return handler(srv, &contextWrappedServerStream{ServerStream: stream, ctx: participatingCtx})
		}

		// Branch 3: Valid and sampled
		spanName := info.FullMethod
		ctx, span := tracer.Start(
			participatingCtx,
			spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("rpc.system", "grpc"),
				attribute.String("rpc.service", grpcServiceFromMethod(info.FullMethod)),
				attribute.String("rpc.method", grpcMethodFromMethod(info.FullMethod)),
			),
		)
		defer func() {
			if retErr != nil {
				st, _ := status.FromError(retErr)
				span.SetAttributes(attribute.Int64("rpc.grpc.status_code", int64(st.Code())))
				span.RecordError(retErr)
				span.SetStatus(codes.Error, retErr.Error())
			} else {
				span.SetAttributes(attribute.Int64("rpc.grpc.status_code", 0))
			}
			if event := operationevent.FromContext(ctx); event != nil {
				enrichSpanFromSnapshot(span, event.Snapshot())
			}
			span.End()
		}()

		return handler(srv, &contextWrappedServerStream{ServerStream: stream, ctx: ctx})
	}
}

type contextWrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextWrappedServerStream) Context() context.Context {
	return s.ctx
}

type metadataCarrier struct {
	md metadata.MD
}

func (c *metadataCarrier) Get(key string) string {
	vals := c.md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (c *metadataCarrier) Set(key, val string) {
	c.md.Set(key, val)
}

func (c *metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c.md))
	for k := range c.md {
		keys = append(keys, k)
	}
	return keys
}

func grpcServiceFromMethod(fullMethod string) string {
	if len(fullMethod) > 0 && fullMethod[0] == '/' {
		fullMethod = fullMethod[1:]
	}
	for i := 0; i < len(fullMethod); i++ {
		if fullMethod[i] == '/' {
			return fullMethod[:i]
		}
	}
	return ""
}

func grpcMethodFromMethod(fullMethod string) string {
	for i := len(fullMethod) - 1; i >= 0; i-- {
		if fullMethod[i] == '/' {
			return fullMethod[i+1:]
		}
	}
	return fullMethod
}

// enrichSpanFromSnapshot sets memoryservice.* span attributes from an OperationEvent snapshot.
// OTel-owned fields (duration, result, status, phase, traceID, spanID) are omitted.
// errorDetails entries are recorded as span events rather than flat attributes.
func enrichSpanFromSnapshot(span trace.Span, s operationevent.Snapshot) {
	set := func(key, val string) {
		if val != "" {
			span.SetAttributes(attribute.String("memoryservice."+key, val))
		}
	}
	setInt := func(key string, val int) {
		if val != 0 {
			span.SetAttributes(attribute.String("memoryservice."+key, strconv.Itoa(val)))
		}
	}
	setInt64 := func(key string, val int64) {
		if val != 0 {
			span.SetAttributes(attribute.String("memoryservice."+key, strconv.FormatInt(val, 10)))
		}
	}
	set("requestID", s.RequestID)
	set("userID", s.UserID)
	set("clientID", s.ClientID)
	set("agentID", s.AgentID)
	set("conversationID", s.ConversationID)
	set("entryID", s.EntryID)
	set("attachmentID", s.AttachmentID)
	set("memoryID", s.MemoryID)
	set("taskID", s.TaskID)
	set("connectionID", s.ConnectionID)
	set("cursor", s.Cursor)
	set("reason", s.Reason)
	set("errorCode", s.ErrorCode)
	set("errorType", s.ErrorType)
	set("rateLimiter", s.RateLimiter)
	set("providerName", s.ProviderName)
	setInt("providerStatusCode", s.ProviderStatusCode)
	set("providerErrorCode", s.ProviderErrorCode)
	set("providerTransactionID", s.ProviderTransactionID)
	setInt("retryAttempt", s.RetryAttempt)
	setInt64("workCount", s.WorkCount)
	setInt64("failureCount", s.FailureCount)

	// errorDetails are recorded as structured span events rather than flat key=value attributes.
	for i, d := range s.ErrorDetails {
		evAttrs := []attribute.KeyValue{
			attribute.String("index", strconv.Itoa(i)),
			attribute.String("errorType", d.ErrorType),
			attribute.String("errorCode", d.ErrorCode),
			attribute.String("reason", d.Reason),
		}
		if d.Provider != nil {
			evAttrs = append(evAttrs,
				attribute.String("provider.name", d.Provider.Name),
				attribute.Int("provider.statusCode", d.Provider.StatusCode),
				attribute.String("provider.transactionID", d.Provider.TransactionID),
			)
		}
		span.AddEvent("memoryservice.errorDetail", trace.WithAttributes(evAttrs...))
	}
}
