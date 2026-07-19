package security

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// GRPCOperationUnaryInterceptor emits one canonical terminal event per unary RPC.
func GRPCOperationUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, retErr error) {
		event := operationevent.New("grpc " + info.FullMethod)
		event.SetRequestID(RequestIDFromContext(ctx))
		resources := operationResourcesFromMessage(info.FullMethod, req)
		state := &grpcOperationState{}
		ctx = context.WithValue(ctx, grpcOperationStateContextKey{}, state)
		ctx = operationevent.WithContext(ctx, event)
		defer func() {
			recovered := recover()
			var stack []byte
			if recovered != nil {
				stack = debug.Stack()
			}
			if shouldEnrichOperationResources(state) {
				resources.apply(event)
			}
			if recovered != nil {
				panicErr := operationevent.RecoveredPanicError(event, "", recovered, stack)
				event.EnrichError(panicErr)
				if errors.Is(panicErr, context.Canceled) {
					retErr = status.Error(codes.Canceled, "request canceled")
				} else {
					retErr = status.Error(codes.Internal, "internal server error")
				}
			}
			finishGRPCOperation(ctx, event, retErr)
		}()
		return handler(ctx, req)
	}
}

// GRPCOperationStreamInterceptor emits bounded start and terminal events per streaming RPC.
func GRPCOperationStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (retErr error) {
		event := operationevent.New("grpc " + info.FullMethod)
		event.SetRequestID(RequestIDFromContext(stream.Context()))
		event.SetConnectionID(uuid.NewString())
		state := &grpcOperationState{}
		ctx := context.WithValue(stream.Context(), grpcOperationStateContextKey{}, state)
		ctx = operationevent.WithContext(ctx, event)
		wrapped := &operationServerStream{ServerStream: stream, ctx: ctx, event: event, fullMethod: info.FullMethod}
		event.EmitStart()
		defer func() {
			recovered := recover()
			var stack []byte
			if recovered != nil {
				stack = debug.Stack()
			}
			if shouldEnrichOperationResources(state) {
				wrapped.applyResources()
			}
			if recovered != nil {
				panicErr := operationevent.RecoveredPanicError(event, "", recovered, stack)
				event.EnrichError(panicErr)
				if errors.Is(panicErr, context.Canceled) {
					retErr = status.Error(codes.Canceled, "request canceled")
				} else {
					retErr = status.Error(codes.Internal, "internal server error")
				}
			}
			finishGRPCOperation(ctx, event, retErr)
		}()
		return handler(srv, wrapped)
	}
}

type operationServerStream struct {
	grpc.ServerStream
	ctx        context.Context
	event      *operationevent.Event
	mu         sync.Mutex
	received   bool
	resources  operationResources
	fullMethod string
}

func (s *operationServerStream) Context() context.Context { return s.ctx }

func (s *operationServerStream) RecvMsg(message any) error {
	err := s.ServerStream.RecvMsg(message)
	if err == nil {
		s.mu.Lock()
		if !s.received {
			s.received = true
			s.resources = operationResourcesFromMessage(s.fullMethod, message)
		}
		s.mu.Unlock()
	}
	return err
}

func (s *operationServerStream) applyResources() {
	s.mu.Lock()
	resources := s.resources
	s.mu.Unlock()
	resources.apply(s.event)
}

func finishGRPCOperation(ctx context.Context, event *operationevent.Event, err error) {
	code := grpcOperationCode(ctx, err)
	event.SetGRPCStatus(code.String())
	if code != codes.OK {
		event.SetErrorCode(grpcCodeName(code))
	}
	if err != nil {
		event.EnrichError(err)
	}
	event.EmitTerminal(operationevent.ResultFromGRPC(code, ctx.Err()))
}

func grpcOperationCode(ctx context.Context, err error) codes.Code {
	code := status.Code(err)
	if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
		code = codes.Canceled
	} else if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		code = codes.DeadlineExceeded
	}
	return code
}

type grpcOperationStateContextKey struct{}

type grpcOperationState struct {
	mu                 sync.Mutex
	resourcesValidated bool
}

// MarkGRPCOperationResourcesValidated records that the service has completed
// authorization and syntactic validation for request-derived resource IDs.
func MarkGRPCOperationResourcesValidated(ctx context.Context) {
	state, _ := ctx.Value(grpcOperationStateContextKey{}).(*grpcOperationState)
	if state == nil {
		return
	}
	state.mu.Lock()
	state.resourcesValidated = true
	state.mu.Unlock()
}

func shouldEnrichOperationResources(state *grpcOperationState) bool {
	if state == nil {
		return false
	}
	state.mu.Lock()
	validated := state.resourcesValidated
	state.mu.Unlock()
	return validated
}

func grpcCodeName(code codes.Code) string {
	name := code.String()
	var result strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteByte('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

func enrichOperationIdentity(ctx context.Context, identity *Identity) {
	event := operationevent.FromContext(ctx)
	if event == nil || identity == nil {
		return
	}
	event.SetUserID(identity.UserID)
	event.SetClientID(identity.ClientID)
}

type operationResources struct {
	agentID        string
	conversationID string
	entryID        string
	attachmentID   string
	memoryID       string
	cursor         string
}

func (r operationResources) apply(event *operationevent.Event) {
	if event == nil {
		return
	}
	current := event.Snapshot()
	if current.AgentID == "" && r.agentID != "" {
		event.SetAgentID(r.agentID)
	}
	if current.ConversationID == "" && r.conversationID != "" {
		event.SetConversationID(r.conversationID)
	}
	if current.EntryID == "" && r.entryID != "" {
		event.SetEntryID(r.entryID)
	}
	if current.AttachmentID == "" && r.attachmentID != "" {
		event.SetAttachmentID(r.attachmentID)
	}
	if current.MemoryID == "" && r.memoryID != "" {
		event.SetMemoryID(r.memoryID)
	}
	if current.Cursor == "" && r.cursor != "" {
		event.SetCursor(r.cursor)
	}
}

func operationResourcesFromMessage(fullMethod string, value any) operationResources {
	message, ok := value.(proto.Message)
	if !ok || message == nil {
		return operationResources{}
	}
	resources := operationResources{}
	collectOperationResources(&resources, message.ProtoReflect(), 0)
	collectMethodOperationResource(&resources, fullMethod, message.ProtoReflect())
	return resources
}

func collectMethodOperationResource(resources *operationResources, fullMethod string, reflection protoreflect.Message) {
	field := reflection.Descriptor().Fields().ByName("id")
	if field == nil || field.IsList() || field.IsMap() || !reflection.Has(field) {
		return
	}
	var id string
	switch field.Kind() {
	case protoreflect.StringKind:
		id = strings.TrimSpace(reflection.Get(field).String())
	case protoreflect.BytesKind:
		if parsed, err := uuid.FromBytes(reflection.Get(field).Bytes()); err == nil {
			id = parsed.String()
		}
	}
	if id == "" {
		return
	}
	switch fullMethod {
	case "/memory.v1.ConversationsService/CreateConversation":
		resources.conversationID = id
	case "/memory.v1.AttachmentsService/GetAttachment",
		"/memory.v1.AttachmentsService/DownloadAttachment",
		"/memory.v1.AttachmentsService/DeleteAttachment",
		"/memory.v1.AttachmentsService/GetAttachmentDownloadUrl":
		resources.attachmentID = id
	case "/memory.v1.AdminMemoriesService/GetMemory",
		"/memory.v1.AdminMemoriesService/DeleteMemory":
		resources.memoryID = id
	}
}

func collectOperationResources(resources *operationResources, reflection protoreflect.Message, depth int) {
	if !reflection.IsValid() || depth > 4 {
		return
	}
	fields := reflection.Descriptor().Fields()
	for _, spec := range []struct {
		name   string
		target *string
	}{
		{"agent_id", &resources.agentID},
		{"conversation_id", &resources.conversationID},
		{"entry_id", &resources.entryID},
		{"attachment_id", &resources.attachmentID},
		{"memory_id", &resources.memoryID},
		{"after_cursor", &resources.cursor},
	} {
		field := fields.ByName(protoreflect.Name(spec.name))
		if *spec.target != "" || field == nil || field.IsList() || field.IsMap() || !reflection.Has(field) {
			continue
		}
		switch field.Kind() {
		case protoreflect.StringKind:
			if value := strings.TrimSpace(reflection.Get(field).String()); value != "" {
				*spec.target = value
			}
		case protoreflect.BytesKind:
			raw := reflection.Get(field).Bytes()
			if id, err := uuid.FromBytes(raw); err == nil {
				*spec.target = id.String()
			}
		}
	}
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if !field.IsList() && !field.IsMap() && field.Kind() == protoreflect.MessageKind && reflection.Has(field) {
			collectOperationResources(resources, reflection.Get(field).Message(), depth+1)
		}
	}
}
