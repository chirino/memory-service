package security

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestCanonicalOperationAndAdminAuditAreDistinct(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware(), OperationEventMiddleware(), ErrorEnvelopeMiddleware(), AdminAuditMiddleware(true))
	router.GET("/v1/admin/test/:conversationId", func(c *gin.Context) {
		c.Set(ContextKeyUserID, "admin-user")
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/test/conversation-1?justification=case-123", nil)
	request.Header.Set(HeaderRequestID, "admin-request")
	router.ServeHTTP(httptest.NewRecorder(), request)

	var operationLine, auditLine string
	for _, line := range strings.Split(output.String(), "\n") {
		switch {
		case strings.Contains(line, "http GET /v1/admin/test/{conversationId}"):
			operationLine = line
		case strings.Contains(line, "Admin audit"):
			auditLine = line
		}
	}
	if operationLine == "" || auditLine == "" {
		t.Fatalf("missing distinct records:\n%s", output.String())
	}
	if !strings.Contains(operationLine, "admin-request") || !strings.Contains(auditLine, "admin-request") {
		t.Fatalf("records did not share request ID:\n%s", output.String())
	}
	if strings.Contains(operationLine, "case-123") || !strings.Contains(auditLine, "case-123") {
		t.Fatalf("justification crossed audit boundary:\n%s", output.String())
	}
}

func TestOperationEventMiddlewareUsesRouteTemplateAndNormalizedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.Use(OperationEventMiddleware())
	router.Use(ErrorEnvelopeMiddleware())
	var event *operationevent.Event
	router.GET("/v1/conversations/:conversationId", func(c *gin.Context) {
		event = OperationEventFromGin(c)
		event.SetConversationID(c.Param("conversationId"))
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/conversations/private-id?token=secret", nil)
	request.Header.Set(HeaderRequestID, "request-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if event == nil {
		t.Fatal("handler did not receive operation event")
	}
	snapshot := event.Snapshot()
	if event.Message() != "http GET /v1/conversations/{conversationId}" {
		t.Fatalf("unexpected operation name: %q", event.Message())
	}
	if snapshot.RequestID != "request-1" || snapshot.ConversationID != "private-id" {
		t.Fatalf("unexpected correlation fields: %#v", snapshot)
	}
	if snapshot.Status != http.StatusNotFound || snapshot.Result != operationevent.ResultNotFound || snapshot.ErrorCode != "not_found" {
		t.Fatalf("unexpected terminal fields: %#v", snapshot)
	}
	if snapshot.Phase != "complete" {
		t.Fatalf("event was not completed: %#v", snapshot)
	}
}

func TestOperationEventMiddlewareRESTOutcomesAndSingleTerminal(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       gin.H
		wantResult operationevent.Result
		wantCode   string
	}{
		{name: "success", status: http.StatusNoContent, wantResult: operationevent.ResultSuccess},
		{name: "auth rejection", status: http.StatusUnauthorized, body: gin.H{"error": "unauthenticated"}, wantResult: operationevent.ResultUnauthenticated, wantCode: "unauthenticated"},
		{name: "rate limit", status: http.StatusTooManyRequests, body: gin.H{"error": "rate_limited"}, wantResult: operationevent.ResultRateLimited, wantCode: "rate_limited"},
		{name: "validation", status: http.StatusUnprocessableEntity, body: gin.H{"error": "validation_error"}, wantResult: operationevent.ResultInvalid, wantCode: "validation_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			log.SetOutput(&output)
			log.SetReportTimestamp(false)
			t.Cleanup(func() {
				log.SetOutput(os.Stderr)
				log.SetReportTimestamp(true)
			})

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(RequestIDMiddleware(), OperationEventMiddleware(), ErrorEnvelopeMiddleware())
			var event *operationevent.Event
			router.Use(func(c *gin.Context) {
				if tt.status >= http.StatusBadRequest {
					event = OperationEventFromGin(c)
					c.AbortWithStatusJSON(tt.status, tt.body)
					return
				}
				c.Next()
			})
			router.GET("/v1/conversations/:conversationId", func(c *gin.Context) {
				event = OperationEventFromGin(c)
				c.Status(tt.status)
			})

			request := httptest.NewRequest(http.MethodGet, "/v1/conversations/conversation-1", nil)
			request.Header.Set(HeaderRequestID, "request-"+tt.name)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if event == nil {
				t.Fatal("request did not receive operation event")
			}
			snapshot := event.Snapshot()
			if snapshot.Result != tt.wantResult || snapshot.Status != tt.status || snapshot.ErrorCode != tt.wantCode {
				t.Fatalf("unexpected terminal fields: %#v", snapshot)
			}
			if count := strings.Count(output.String(), "http GET /v1/conversations/{conversationId}"); count != 1 {
				t.Fatalf("got %d operation records, want 1:\n%s", count, output.String())
			}
		})
	}
}

func TestOperationEventMiddlewareHonorsCommittedStreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware(), OperationEventMiddleware())
	var event *operationevent.Event
	router.GET("/v1/events", func(c *gin.Context) {
		event = OperationEventFromGin(c)
		c.Status(http.StatusOK)
		c.Writer.WriteHeaderNow()
		err := operationevent.WithErrorDetails(errors.New("private stream failure"), operationevent.ErrorDetails{
			ErrorType: "event_bus",
			ErrorCode: "subscribe_failed",
		})
		SetOperationTerminalError(c, "subscribe_failed", err)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/events", nil))

	require.NotNil(t, event)
	snapshot := event.Snapshot()
	require.Equal(t, http.StatusOK, snapshot.Status)
	require.Equal(t, operationevent.ResultFailed, snapshot.Result)
	require.Equal(t, "subscribe_failed", snapshot.Reason)
	require.Equal(t, "event_bus", snapshot.ErrorType)
	require.Equal(t, "subscribe_failed", snapshot.ErrorCode)
}

func TestOperationEventMiddlewareCollectsRegisteredTypedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware(), OperationEventMiddleware(), ErrorEnvelopeMiddleware())
	var event *operationevent.Event
	router.GET("/v1/admin/stats", func(c *gin.Context) {
		event = OperationEventFromGin(c)
		err := operationevent.WithErrorDetails(errors.New("private provider response"), operationevent.ErrorDetails{
			ErrorType: "provider",
			ErrorCode: "monitoring_provider_error",
			Reason:    "request_rejected",
			Provider: &operationevent.ErrorDetailsProvider{
				Name:          "prometheus",
				StatusCode:    http.StatusServiceUnavailable,
				TransactionID: "provider-request-1",
			},
		})
		_ = c.Error(err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "prometheus_unavailable", "error": "provider unavailable"})
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/admin/stats", nil))
	snapshot := event.Snapshot()
	if snapshot.Result != operationevent.ResultFailed || snapshot.ProviderName != "prometheus" ||
		snapshot.ProviderStatusCode != http.StatusServiceUnavailable || snapshot.ProviderTransactionID != "provider-request-1" ||
		len(snapshot.ErrorDetails) != 1 {
		t.Fatalf("typed terminal error was not collected: %#v", snapshot)
	}
}

func TestOperationEventMiddlewareUnmatchedRouteDoesNotExposeConcretePath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware(), OperationEventMiddleware(), ErrorEnvelopeMiddleware())
	var event *operationevent.Event
	router.NoRoute(func(c *gin.Context) {
		event = OperationEventFromGin(c)
		c.Status(http.StatusNotFound)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/reset/secret-token?key=value", nil))
	if event == nil || event.Message() != "http GET <unmatched>" || event.Snapshot().Result != operationevent.ResultNotFound {
		t.Fatalf("unmatched event missing: %#v", event)
	}
}

func TestOperationEventMiddlewareSuppressionAndPanic(t *testing.T) {
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware(), OperationEventMiddleware("/health"), OperationRecoveryMiddleware(), ErrorEnvelopeMiddleware())
	var healthEvent, panicEvent, committedPanicEvent, bufferedPanicEvent, middlewarePanicEvent, abortEvent *operationevent.Event
	router.Use(func(c *gin.Context) {
		if c.Request.URL.Path == "/middleware-panic" {
			middlewarePanicEvent = OperationEventFromGin(c)
			panic("middleware boom")
		}
		c.Next()
	})
	router.GET("/health", func(c *gin.Context) {
		healthEvent = OperationEventFromGin(c)
		c.Status(http.StatusOK)
	})
	router.GET("/panic", func(c *gin.Context) {
		panicEvent = OperationEventFromGin(c)
		panicEvent.SetConversationID("conversation-1")
		panic("boom")
	})
	router.GET("/committed-panic", func(c *gin.Context) {
		committedPanicEvent = OperationEventFromGin(c)
		c.Status(http.StatusOK)
		c.Writer.WriteHeaderNow()
		panic("boom after commit")
	})
	router.GET("/buffered-error-panic", func(c *gin.Context) {
		bufferedPanicEvent = OperationEventFromGin(c)
		c.JSON(http.StatusBadRequest, gin.H{"error": "private buffered error"})
		panic("boom after buffered error")
	})
	router.GET("/middleware-panic", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/connection-abort", func(c *gin.Context) {
		abortEvent = OperationEventFromGin(c)
		panic(http.ErrAbortHandler)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	response := httptest.NewRecorder()
	panicRequest := httptest.NewRequest(http.MethodGet, "/panic", nil)
	panicRequest.Header.Set(HeaderRequestID, "panic-request")
	router.ServeHTTP(response, panicRequest)
	committedResponse := httptest.NewRecorder()
	router.ServeHTTP(committedResponse, httptest.NewRequest(http.MethodGet, "/committed-panic", nil))
	bufferedResponse := httptest.NewRecorder()
	router.ServeHTTP(bufferedResponse, httptest.NewRequest(http.MethodGet, "/buffered-error-panic", nil))
	middlewareResponse := httptest.NewRecorder()
	router.ServeHTTP(middlewareResponse, httptest.NewRequest(http.MethodGet, "/middleware-panic", nil))
	abortResponse := httptest.NewRecorder()
	abortRequest := httptest.NewRequest(http.MethodGet, "/connection-abort", nil)
	abortRequest.Header.Set(HeaderRequestID, "abort-request")
	router.ServeHTTP(abortResponse, abortRequest)
	if healthEvent != nil {
		t.Fatal("suppressed path received an operation event")
	}
	if panicEvent == nil || panicEvent.Snapshot().Result != operationevent.ResultFailed || response.Code != http.StatusInternalServerError {
		t.Fatalf("panic was not recorded as failed: status=%d event=%#v", response.Code, panicEvent)
	}
	if committedPanicEvent == nil || committedPanicEvent.Snapshot().Result != operationevent.ResultFailed || committedPanicEvent.Snapshot().ErrorCode != "internal_error" || committedResponse.Code != http.StatusOK {
		t.Fatalf("committed panic was not recorded as failed: status=%d event=%#v", committedResponse.Code, committedPanicEvent)
	}
	if bufferedPanicEvent == nil || bufferedPanicEvent.Snapshot().Result != operationevent.ResultFailed || bufferedPanicEvent.Snapshot().Status != http.StatusInternalServerError || bufferedPanicEvent.Snapshot().ErrorCode != "internal_error" || bufferedResponse.Code != http.StatusInternalServerError {
		t.Fatalf("buffered error panic was not normalized as failed: status=%d event=%#v body=%s", bufferedResponse.Code, bufferedPanicEvent, bufferedResponse.Body.String())
	}
	if strings.Contains(bufferedResponse.Body.String(), "private buffered error") {
		t.Fatalf("buffered error leaked after panic: %s", bufferedResponse.Body.String())
	}
	if middlewarePanicEvent == nil || middlewarePanicEvent.Snapshot().Result != operationevent.ResultFailed || middlewareResponse.Code != http.StatusInternalServerError {
		t.Fatalf("middleware panic was not recorded as failed: status=%d event=%#v", middlewareResponse.Code, middlewarePanicEvent)
	}
	if abortEvent == nil || abortEvent.Snapshot().Result != operationevent.ResultCanceled || abortEvent.Snapshot().Reason != "client_disconnect" {
		t.Fatalf("connection abort was not recorded as canceled: status=%d event=%#v", abortResponse.Code, abortEvent)
	}
	text := output.String()
	for _, expected := range []string{
		"operation panic",
		`operation="http GET /panic"`,
		"requestID=panic-request",
		"conversationID=conversation-1",
		"operation_logging_test.go",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("panic diagnostic missing %q:\n%s", expected, text)
		}
	}
	if count := strings.Count(text, "operation panic"); count != 4 {
		t.Fatalf("got %d stack diagnostics, want 4 non-connection panics:\n%s", count, text)
	}
	if strings.Contains(text, "http: abort Handler") {
		t.Fatalf("connection-abort panic was not suppressed:\n%s", text)
	}
}

func TestGRPCOperationUnaryLifecyclePanicAndCause(t *testing.T) {
	ctx, _ := WithRequestID(context.Background(), "grpc-request")
	request := &pb.GetConversationRequest{ConversationId: "conversation-1"}
	interceptor := GRPCOperationUnaryInterceptor()
	var event *operationevent.Event
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	_, err := interceptor(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/memory.v1.ConversationsService/GetConversation"}, func(ctx context.Context, _ any) (any, error) {
		event = operationevent.FromContext(ctx)
		MarkGRPCOperationResourcesValidated(ctx)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected success error: %v", err)
	}
	if snapshot := event.Snapshot(); snapshot.Result != operationevent.ResultSuccess || snapshot.Status != codes.OK.String() || snapshot.RequestID != "grpc-request" || snapshot.ConversationID != "conversation-1" {
		t.Fatalf("unexpected successful unary event: %#v", snapshot)
	}
	if count := strings.Count(output.String(), "grpc /memory.v1.ConversationsService/GetConversation"); count != 1 {
		t.Fatalf("got %d unary success records, want 1:\n%s", count, output.String())
	}

	_, err = interceptor(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/memory.v1.ConversationsService/GetConversation"}, func(ctx context.Context, _ any) (any, error) {
		event = operationevent.FromContext(ctx)
		MarkGRPCOperationResourcesValidated(ctx)
		return nil, status.Error(codes.PermissionDenied, "private")
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unexpected error: %v", err)
	}
	snapshot := event.Snapshot()
	if snapshot.RequestID != "grpc-request" || snapshot.ConversationID != "conversation-1" || snapshot.Result != operationevent.ResultForbidden || snapshot.Status != codes.PermissionDenied.String() {
		t.Fatalf("unexpected unary event: %#v", snapshot)
	}

	_, err = interceptor(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/memory.v1.ConversationsService/GetConversation"}, func(ctx context.Context, _ any) (any, error) {
		event = operationevent.FromContext(ctx)
		return nil, status.Error(codes.PermissionDenied, "authentication rejected")
	})
	if status.Code(err) != codes.PermissionDenied || event.Snapshot().ConversationID != "" {
		t.Fatalf("pre-validation rejection enriched resources: %v %#v", err, event.Snapshot())
	}

	_, err = interceptor(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/memory.v1.ConversationsService/GetConversation"}, func(ctx context.Context, _ any) (any, error) {
		event = operationevent.FromContext(ctx)
		return nil, status.Error(codes.InvalidArgument, "syntactically invalid request")
	})
	if status.Code(err) != codes.InvalidArgument || event.Snapshot().ConversationID != "" {
		t.Fatalf("pre-validation syntactic rejection enriched resources: %v %#v", err, event.Snapshot())
	}

	_, err = interceptor(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/memory.v1.ConversationsService/GetConversation"}, func(ctx context.Context, _ any) (any, error) {
		event = operationevent.FromContext(ctx)
		MarkGRPCOperationResourcesValidated(ctx)
		return nil, status.Error(codes.InvalidArgument, "application validation failed")
	})
	if status.Code(err) != codes.InvalidArgument || event.Snapshot().ConversationID != "conversation-1" {
		t.Fatalf("post-validation rejection lost resources: %v %#v", err, event.Snapshot())
	}

	output.Reset()
	_, err = interceptor(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/memory.v1.ConversationsService/GetConversation"}, func(ctx context.Context, _ any) (any, error) {
		event = operationevent.FromContext(ctx)
		MarkGRPCOperationResourcesValidated(ctx)
		panic("boom")
	})
	if status.Code(err) != codes.Internal || event.Snapshot().Result != operationevent.ResultFailed {
		t.Fatalf("panic not converted to terminal failure: %v %#v", err, event.Snapshot())
	}
	for _, expected := range []string{
		"operation panic",
		`operation="grpc /memory.v1.ConversationsService/GetConversation"`,
		"requestID=grpc-request",
		"conversationID=conversation-1",
		"operation_logging_test.go",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("gRPC panic diagnostic missing %q:\n%s", expected, output.String())
		}
	}
}

func TestGRPCOperationStreamStartFinalAndFirstMessage(t *testing.T) {
	ctx, _ := WithRequestID(context.Background(), "stream-request")
	incoming := &pb.ReplayRequest{ConversationId: "conversation-2"}
	stream := &operationTestServerStream{ctx: ctx, incoming: incoming}
	interceptor := GRPCOperationStreamInterceptor()
	var event *operationevent.Event
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetReportTimestamp(false)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetReportTimestamp(true)
	})

	err := interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/memory.v1.ResponseRecorderService/Replay", IsServerStream: true}, func(_ any, serverStream grpc.ServerStream) error {
		event = operationevent.FromContext(serverStream.Context())
		var request pb.ReplayRequest
		if err := serverStream.RecvMsg(&request); err != nil {
			return err
		}
		MarkGRPCOperationResourcesValidated(serverStream.Context())
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected stream success error: %v", err)
	}
	if snapshot := event.Snapshot(); snapshot.ConversationID != "conversation-2" || snapshot.ConnectionID == "" || snapshot.Result != operationevent.ResultSuccess {
		t.Fatalf("unexpected successful stream event: %#v", snapshot)
	}
	if count := strings.Count(output.String(), "grpc /memory.v1.ResponseRecorderService/Replay"); count != 2 {
		t.Fatalf("got %d stream success records, want start and terminal:\n%s", count, output.String())
	}

	incoming = &pb.ReplayRequest{ConversationId: "conversation-2"}
	stream = &operationTestServerStream{ctx: ctx, incoming: incoming}
	err = interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/memory.v1.ResponseRecorderService/Replay", IsServerStream: true}, func(_ any, serverStream grpc.ServerStream) error {
		event = operationevent.FromContext(serverStream.Context())
		var request pb.ReplayRequest
		if err := serverStream.RecvMsg(&request); err != nil {
			return err
		}
		MarkGRPCOperationResourcesValidated(serverStream.Context())
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected stream error: %v", err)
	}
	snapshot := event.Snapshot()
	if snapshot.ConversationID != "conversation-2" || snapshot.ConnectionID == "" || snapshot.Result != operationevent.ResultCanceled || snapshot.Phase != "complete" {
		t.Fatalf("unexpected stream event: %#v", snapshot)
	}

	output.Reset()
	incoming = &pb.ReplayRequest{ConversationId: "conversation-2"}
	stream = &operationTestServerStream{ctx: ctx, incoming: incoming}
	err = interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/memory.v1.ResponseRecorderService/Replay", IsServerStream: true}, func(_ any, serverStream grpc.ServerStream) error {
		event = operationevent.FromContext(serverStream.Context())
		var request pb.ReplayRequest
		if err := serverStream.RecvMsg(&request); err != nil {
			return err
		}
		MarkGRPCOperationResourcesValidated(serverStream.Context())
		panic("stream boom")
	})
	if status.Code(err) != codes.Internal || event.Snapshot().Result != operationevent.ResultFailed {
		t.Fatalf("stream panic not converted to terminal failure: %v %#v", err, event.Snapshot())
	}
	for _, expected := range []string{
		"operation panic",
		`operation="grpc /memory.v1.ResponseRecorderService/Replay"`,
		"requestID=stream-request",
		"conversationID=conversation-2",
		"connectionID=",
		"operation_logging_test.go",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("gRPC stream panic diagnostic missing %q:\n%s", expected, output.String())
		}
	}
}

func TestGRPCOperationResourcesMapMethodSpecificIDs(t *testing.T) {
	attachmentID := uuid.New()
	memoryID := uuid.New()
	conversationID := "conversation-created"
	tests := []struct {
		name       string
		fullMethod string
		message    proto.Message
		assert     func(t *testing.T, resources operationResources)
	}{
		{
			name:       "attachment",
			fullMethod: "/memory.v1.AttachmentsService/GetAttachment",
			message:    &pb.GetAttachmentRequest{Id: attachmentID.String()},
			assert: func(t *testing.T, resources operationResources) {
				if resources.attachmentID != attachmentID.String() {
					t.Fatalf("attachment ID = %q", resources.attachmentID)
				}
			},
		},
		{
			name:       "admin memory",
			fullMethod: "/memory.v1.AdminMemoriesService/GetMemory",
			message:    &pb.AdminGetMemoryRequest{Id: memoryID[:]},
			assert: func(t *testing.T, resources operationResources) {
				if resources.memoryID != memoryID.String() {
					t.Fatalf("memory ID = %q", resources.memoryID)
				}
			},
		},
		{
			name:       "created conversation",
			fullMethod: "/memory.v1.ConversationsService/CreateConversation",
			message:    &pb.CreateConversationRequest{Id: &conversationID},
			assert: func(t *testing.T, resources operationResources) {
				if resources.conversationID != conversationID {
					t.Fatalf("conversation ID = %q", resources.conversationID)
				}
			},
		},
		{
			name:       "unknown generic ID",
			fullMethod: "/memory.v1.UnknownService/Get",
			message:    &pb.GetAttachmentRequest{Id: attachmentID.String()},
			assert: func(t *testing.T, resources operationResources) {
				if resources.attachmentID != "" || resources.memoryID != "" || resources.conversationID != "" {
					t.Fatalf("unknown method guessed a resource: %#v", resources)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.assert(t, operationResourcesFromMessage(tt.fullMethod, tt.message))
		})
	}
}

type operationTestServerStream struct {
	ctx      context.Context
	incoming proto.Message
}

func (s *operationTestServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *operationTestServerStream) SendHeader(metadata.MD) error { return nil }
func (s *operationTestServerStream) SetTrailer(metadata.MD)       {}
func (s *operationTestServerStream) Context() context.Context     { return s.ctx }
func (s *operationTestServerStream) SendMsg(any) error            { return nil }
func (s *operationTestServerStream) RecvMsg(value any) error {
	if s.incoming == nil {
		return io.EOF
	}
	target := value.(proto.Message)
	proto.Merge(target, s.incoming)
	s.incoming = nil
	return nil
}
