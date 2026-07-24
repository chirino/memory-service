package grpc

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/model"
	"github.com/chirino/memory-service/internal/operationevent"
	routeattachments "github.com/chirino/memory-service/internal/plugin/route/attachments"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	internalresumer "github.com/chirino/memory-service/internal/resumer"
	"github.com/chirino/memory-service/internal/security"
	servicecapabilities "github.com/chirino/memory-service/internal/service/capabilities"
	"github.com/chirino/memory-service/internal/service/eventstream"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- UUID conversion helpers ---

func uuidToBytes(id uuid.UUID) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[0:8], uint64(id[0])<<56|uint64(id[1])<<48|uint64(id[2])<<40|uint64(id[3])<<32|uint64(id[4])<<24|uint64(id[5])<<16|uint64(id[6])<<8|uint64(id[7]))
	binary.BigEndian.PutUint64(b[8:16], uint64(id[8])<<56|uint64(id[9])<<48|uint64(id[10])<<40|uint64(id[11])<<32|uint64(id[12])<<24|uint64(id[13])<<16|uint64(id[14])<<8|uint64(id[15]))
	return b
}

func bytesToUUID(b []byte) (uuid.UUID, error) {
	if len(b) != 16 {
		return uuid.Nil, fmt.Errorf("invalid UUID bytes: expected 16, got %d", len(b))
	}
	var id uuid.UUID
	copy(id[:], b)
	return id, nil
}

func uuidFromBytesPtr(b []byte) *uuid.UUID {
	if len(b) == 0 {
		return nil
	}
	id, err := bytesToUUID(b)
	if err != nil {
		return nil
	}
	return &id
}

func uuidFromBytes(b []byte) (*uuid.UUID, error) {
	if len(b) == 0 {
		return nil, nil
	}
	id, err := bytesToUUID(b)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func requiredConversationID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", fmt.Errorf("conversation_id is required")
	}
	return id, nil
}

func optionalConversationID(raw string) *string {
	id := strings.TrimSpace(raw)
	if id == "" {
		return nil
	}
	return &id
}

func stringPtrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func getUserID(ctx context.Context) string {
	if id := security.IdentityFromContext(ctx); id != nil {
		return id.UserID
	}
	return ""
}

func getClientID(ctx context.Context) string {
	if id := security.IdentityFromContext(ctx); id != nil {
		return id.ClientID
	}
	return ""
}

func requireGRPCIdentity(ctx context.Context) (*security.Identity, error) {
	id := security.IdentityFromContext(ctx)
	if id == nil || (strings.TrimSpace(id.UserID) == "" && strings.TrimSpace(id.ClientID) == "") {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	return id, nil
}

// requireGRPCUser requires a non-empty user principal (not just any authenticated identity).
// Use this in user-scoped endpoints where an API-key-only client identity is insufficient.
func requireGRPCUser(ctx context.Context) (string, error) {
	id := security.IdentityFromContext(ctx)
	if id == nil || strings.TrimSpace(id.UserID) == "" {
		return "", status.Error(codes.Unauthenticated, "user authentication required")
	}
	return id.UserID, nil
}

func hasGRPCRole(ctx context.Context, role string) bool {
	if id := security.IdentityFromContext(ctx); id != nil {
		return id.Roles[role]
	}
	return false
}

func hasGRPCAdminEventAccess(ctx context.Context) bool {
	return hasGRPCRole(ctx, security.RoleAdmin) || hasGRPCRole(ctx, security.RoleAuditor)
}

func requireGRPCOIDCScope(ctx context.Context, permission security.Permission) error {
	return security.CheckGRPCOIDCScope(ctx, permission)
}

func mapAccessLevel(level model.AccessLevel) pb.AccessLevel {
	switch level {
	case model.AccessLevelOwner:
		return pb.AccessLevel_OWNER
	case model.AccessLevelManager:
		return pb.AccessLevel_MANAGER
	case model.AccessLevelWriter:
		return pb.AccessLevel_WRITER
	case model.AccessLevelReader:
		return pb.AccessLevel_READER
	default:
		return pb.AccessLevel_ACCESS_LEVEL_UNSPECIFIED
	}
}

func mapChannel(ch model.Channel) pb.Channel {
	switch ch {
	case model.ChannelHistory:
		return pb.Channel_HISTORY
	case model.ChannelContext:
		return pb.Channel_CONTEXT
	case model.ChannelJournal:
		return pb.Channel_JOURNAL
	default:
		return pb.Channel_CHANNEL_UNSPECIFIED
	}
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}

	var notFound *registrystore.NotFoundError
	var forbidden *registrystore.ForbiddenError
	var validation *registrystore.ValidationError
	var conflict *registrystore.ConflictError
	var badRequest *registrystore.BadRequestError

	switch {
	case errors.Is(err, registryepisodic.ErrMemoryRevisionConflict):
		return grpcStatusWithCause(codes.Aborted, err.Error(), err)
	case errors.As(err, &notFound):
		return grpcStatusWithCause(codes.NotFound, err.Error(), err)
	case errors.As(err, &forbidden):
		return grpcStatusWithCause(codes.PermissionDenied, err.Error(), err)
	case errors.As(err, &validation):
		return grpcStatusWithCause(codes.InvalidArgument, err.Error(), err)
	case errors.As(err, &badRequest):
		return grpcStatusWithCause(codes.InvalidArgument, err.Error(), err)
	case errors.As(err, &conflict):
		return grpcStatusWithCause(codes.Aborted, err.Error(), err)
	default:
		log.Error("gRPC request failed", "error", err, "stack", string(debug.Stack()))
		return grpcStatusWithCause(codes.Internal, "internal server error", err)
	}
}

type statusCauseError struct {
	status *status.Status
	cause  error
}

func (e *statusCauseError) Error() string              { return e.status.Err().Error() }
func (e *statusCauseError) GRPCStatus() *status.Status { return e.status }
func (e *statusCauseError) Unwrap() error              { return e.cause }

func grpcStatusWithCause(code codes.Code, message string, cause error) error {
	return &statusCauseError{status: status.New(code, message), cause: cause}
}

func appendOutboxOrUseEvents(ctx context.Context, store registrystore.MemoryStore, events []registryeventbus.Event) ([]registryeventbus.Event, error) {
	appended, used, err := eventstream.AppendOutboxEvents(ctx, store, events...)
	if err != nil {
		return nil, err
	}
	if used {
		return appended, nil
	}
	return events, nil
}

func publishGRPCEvents(ctx context.Context, store registrystore.MemoryStore, eventBus registryeventbus.EventBus, events []registryeventbus.Event, label string) {
	if eventBus == nil || len(events) == 0 {
		return
	}
	if err := eventstream.PublishEvents(ctx, store, eventBus, events...); err != nil {
		log.Warn("Failed to publish gRPC events", "label", label, "err", err)
	}
}

func grpcPageSize(ctx context.Context, requested int32, defaultSize int) (int, error) {
	if requested == 0 {
		return config.ClampPageSize(ctx, defaultSize), nil
	}
	if err := config.ValidatePageSize(ctx, int(requested)); err != nil {
		return 0, status.Error(codes.InvalidArgument, err.Error())
	}
	return int(requested), nil
}

func withMemoryRead[T any](ctx context.Context, store registrystore.MemoryStore, fn func(context.Context) (T, error)) (T, error) {
	security.MarkGRPCOperationResourcesValidated(ctx)
	var out T
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		out, err = fn(txCtx)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

func withMemoryWrite[T any](ctx context.Context, store registrystore.MemoryStore, fn func(context.Context) (T, error)) (T, error) {
	security.MarkGRPCOperationResourcesValidated(ctx)
	var out T
	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		out, err = fn(txCtx)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

func inMemoryWrite(ctx context.Context, store registrystore.MemoryStore, fn func(context.Context) error) error {
	security.MarkGRPCOperationResourcesValidated(ctx)
	return store.InWriteTx(ctx, fn)
}

func withEpisodicRead[T any](ctx context.Context, store registryepisodic.EpisodicStore, fn func(context.Context) (T, error)) (T, error) {
	security.MarkGRPCOperationResourcesValidated(ctx)
	var out T
	err := store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		out, err = fn(txCtx)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

func withEpisodicWrite[T any](ctx context.Context, store registryepisodic.EpisodicStore, fn func(context.Context) (T, error)) (T, error) {
	security.MarkGRPCOperationResourcesValidated(ctx)
	var out T
	err := store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		out, err = fn(txCtx)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

func inEpisodicRead(ctx context.Context, store registryepisodic.EpisodicStore, fn func(context.Context) error) error {
	security.MarkGRPCOperationResourcesValidated(ctx)
	return store.InReadTx(ctx, fn)
}

func inEpisodicWrite(ctx context.Context, store registryepisodic.EpisodicStore, fn func(context.Context) error) error {
	security.MarkGRPCOperationResourcesValidated(ctx)
	return store.InWriteTx(ctx, fn)
}

// --- System Service ---

type SystemServer struct {
	pb.UnimplementedSystemServiceServer
	Config *config.Config
}

func (s *SystemServer) GetHealth(_ context.Context, _ *emptypb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

func (s *SystemServer) GetCapabilities(ctx context.Context, _ *emptypb.Empty) (*pb.CapabilitiesResponse, error) {
	// Accept both user and client-only identities; auth-only (no user required) to match the HTTP route.
	id := security.IdentityFromContext(ctx)
	if id == nil {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if !hasCapabilitiesAccessGRPC(ctx) {
		return nil, status.Error(codes.PermissionDenied, "client context or admin/auditor role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSystemRead); err != nil {
		return nil, err
	}
	return mapCapabilitiesSummary(servicecapabilities.Build(s.Config)), nil
}

func hasCapabilitiesAccessGRPC(ctx context.Context) bool {
	id := security.IdentityFromContext(ctx)
	if id == nil {
		return false
	}
	if id.ClientID != "" {
		return true
	}
	return id.Roles[security.RoleAdmin] || id.Roles[security.RoleAuditor]
}

func mapCapabilitiesSummary(summary servicecapabilities.Summary) *pb.CapabilitiesResponse {
	return &pb.CapabilitiesResponse{
		Version: summary.Version,
		Tech: &pb.CapabilitiesTech{
			Store:       summary.Tech.Store,
			Attachments: summary.Tech.Attachments,
			Cache:       summary.Tech.Cache,
			Vector:      summary.Tech.Vector,
			EventBus:    summary.Tech.EventBus,
			Embedder:    summary.Tech.Embedder,
		},
		Features: &pb.CapabilitiesFeatures{
			OutboxEnabled:             summary.Features.OutboxEnabled,
			SemanticSearchEnabled:     summary.Features.SemanticSearchEnabled,
			FulltextSearchEnabled:     summary.Features.FulltextSearchEnabled,
			CorsEnabled:               summary.Features.CorsEnabled,
			ManagementListenerEnabled: summary.Features.ManagementListenerEnabled,
			PrivateSourceUrlsEnabled:  summary.Features.PrivateSourceURLsEnabled,
			S3DirectDownloadEnabled:   summary.Features.S3DirectDownloadEnabled,
		},
		Auth: &pb.CapabilitiesAuth{
			OidcEnabled:                summary.Auth.OIDCEnabled,
			ApiKeyEnabled:              summary.Auth.APIKeyEnabled,
			AdminJustificationRequired: summary.Auth.AdminJustificationRequired,
			UserIdAssertionEnabled:     summary.Auth.UserIDAssertionEnabled,
		},
		Security: &pb.CapabilitiesSecurity{
			EncryptionEnabled:           summary.Security.EncryptionEnabled,
			DbEncryptionEnabled:         summary.Security.DBEncryptionEnabled,
			AttachmentEncryptionEnabled: summary.Security.AttachmentEncryptionEnabled,
		},
	}
}

// --- Conversations Service ---

type ConversationsServer struct {
	pb.UnimplementedConversationsServiceServer
	Store    registrystore.MemoryStore
	EventBus registryeventbus.EventBus
}

func protoArchiveFilterToEpisodic(filter pb.ArchiveFilter) registryepisodic.ArchiveFilter {
	switch filter {
	case pb.ArchiveFilter_ARCHIVE_FILTER_INCLUDE:
		return registryepisodic.ArchiveFilterInclude
	case pb.ArchiveFilter_ARCHIVE_FILTER_ONLY:
		return registryepisodic.ArchiveFilterOnly
	default:
		return registryepisodic.ArchiveFilterExclude
	}
}

func (s *ConversationsServer) ListConversations(ctx context.Context, req *pb.ListConversationsRequest) (*pb.ListConversationsResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsRead); err != nil {
		return nil, err
	}

	mode := model.ListModeLatestFork
	switch req.GetMode() {
	case pb.ConversationListMode_ALL:
		mode = model.ListModeAll
	case pb.ConversationListMode_ROOTS:
		mode = model.ListModeRoots
	}
	ancestry := model.ConversationAncestryRoots
	switch req.GetAncestry() {
	case pb.ConversationAncestryFilter_CONVERSATION_ANCESTRY_FILTER_CHILDREN:
		ancestry = model.ConversationAncestryChildren
	case pb.ConversationAncestryFilter_CONVERSATION_ANCESTRY_FILTER_ALL:
		ancestry = model.ConversationAncestryAll
	}
	archived := registrystore.ArchiveFilterExclude
	switch req.GetArchived() {
	case pb.ArchiveFilter_ARCHIVE_FILTER_INCLUDE:
		archived = registrystore.ArchiveFilterInclude
	case pb.ArchiveFilter_ARCHIVE_FILTER_ONLY:
		archived = registrystore.ArchiveFilterOnly
	}

	var afterCursor *string
	var query *string
	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 20)
	if err != nil {
		return nil, err
	}
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
	}
	if req.GetQuery() != "" {
		q := req.GetQuery()
		query = &q
	}

	summaries, cursor, err := func() ([]registrystore.ConversationSummary, *string, error) {
		type result struct {
			summaries []registrystore.ConversationSummary
			cursor    *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			summaries, cursor, err := s.Store.ListConversations(txCtx, userID, query, afterCursor, limit, mode, ancestry, archived)
			return result{summaries: summaries, cursor: cursor}, err
		})
		return out.summaries, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListConversationsResponse{
		PageInfo: &pb.PageInfo{},
	}
	for _, cs := range summaries {
		item := &pb.ConversationSummary{
			Id:          string(cs.ID),
			Title:       cs.Title,
			OwnerUserId: cs.OwnerUserID,
			CreatedAt:   cs.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt:   cs.UpdatedAt.Format("2006-01-02T15:04:05Z"),
			AccessLevel: mapAccessLevel(cs.AccessLevel),
			Archived:    cs.ArchivedAt != nil,
		}
		if cs.StartedByConversationID != nil {
			item.StartedByConversationId = string(*cs.StartedByConversationID)
		}
		if cs.StartedByEntryID != nil {
			item.StartedByEntryId = uuidToBytes(*cs.StartedByEntryID)
		}
		if cs.AgentID != nil {
			item.AgentId = cs.AgentID
		}
		if cs.Metadata != nil {
			if metadata, err := structpb.NewStruct(cs.Metadata); err == nil {
				item.Metadata = metadata
			}
		}
		if cs.ForkedAtEntryID != nil {
			item.ForkedAtEntryId = uuidToBytes(*cs.ForkedAtEntryID)
		}
		if cs.ForkedAtConversationID != nil {
			item.ForkedAtConversationId = string(*cs.ForkedAtConversationID)
		}
		resp.Conversations = append(resp.Conversations, item)
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func (s *ConversationsServer) CreateConversation(ctx context.Context, req *pb.CreateConversationRequest) (*pb.Conversation, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsWrite); err != nil {
		return nil, err
	}

	var meta map[string]any
	if req.GetMetadata() != nil {
		meta = req.GetMetadata().AsMap()
	}
	if len(req.GetTitle()) > 500 {
		return nil, status.Error(codes.InvalidArgument, "title exceeds maximum length")
	}

	var convID *string
	if req.Id != nil {
		id := strings.TrimSpace(req.GetId())
		if id == "" {
			return nil, status.Error(codes.InvalidArgument, "invalid id")
		}
		convID = &id
	}

	agentID := stringPtrIfNotEmpty(req.GetAgentId())
	var forkConvID *string
	if req.ForkedAtConversationId != nil {
		id := strings.TrimSpace(req.GetForkedAtConversationId())
		if id == "" {
			return nil, status.Error(codes.InvalidArgument, "invalid forked_at_conversation_id")
		}
		forkConvID = &id
	}
	var forkEntryID *uuid.UUID
	if len(req.GetForkedAtEntryId()) > 0 {
		id, err := bytesToUUID(req.GetForkedAtEntryId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid forked_at_entry_id")
		}
		forkEntryID = &id
	}

	var eventsToPublish []registryeventbus.Event
	conv, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
		clientID := getClientID(ctx)
		var conv *registrystore.ConversationDetail
		var err error
		if convID != nil {
			conv, err = s.Store.CreateConversationWithID(txCtx, userID, clientID, *convID, req.GetTitle(), meta, agentID, forkConvID, forkEntryID)
		} else {
			conv, err = s.Store.CreateConversation(txCtx, userID, clientID, req.GetTitle(), meta, agentID, forkConvID, forkEntryID)
		}
		if err != nil {
			return nil, err
		}
		if s.EventBus != nil && conv != nil {
			events := []registryeventbus.Event{{
				Event: "created",
				Kind:  "conversation",
				Data: map[string]any{
					"conversation":       conv.ID,
					"conversation_group": conv.ConversationGroupID,
				},
				ConversationGroupID: conv.ConversationGroupID,
			}}
			eventsToPublish, err = appendOutboxOrUseEvents(txCtx, s.Store, events)
			if err != nil {
				return nil, err
			}
		}
		return conv, nil
	})
	if err != nil {
		return nil, mapError(err)
	}

	publishGRPCEvents(ctx, s.Store, s.EventBus, eventsToPublish, "conversation")
	return conversationToProto(conv), nil
}

func (s *ConversationsServer) GetConversation(ctx context.Context, req *pb.GetConversationRequest) (*pb.Conversation, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsRead); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	conv, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
		return s.Store.GetConversation(txCtx, userID, convID)
	})
	if err != nil {
		return nil, mapError(err)
	}
	return conversationToProto(conv), nil
}

func (s *ConversationsServer) UpdateConversation(ctx context.Context, req *pb.UpdateConversationRequest) (*pb.Conversation, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsWrite); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var title *string
	if req.Title != nil {
		if len(req.GetTitle()) > 500 {
			return nil, status.Error(codes.InvalidArgument, "title exceeds maximum length")
		}
		title = req.Title
	}
	var archived *bool
	if req.Archived != nil {
		archived = req.Archived
	}
	var metadata map[string]any
	if req.GetMetadata() != nil {
		metadata = req.GetMetadata().AsMap()
	}

	var eventsToPublish []registryeventbus.Event
	conv, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
		var conv *registrystore.ConversationDetail
		var err error
		eventData := map[string]any{}
		var memberUserIDs []string
		if archived != nil {
			conv, err = s.Store.GetConversation(txCtx, userID, convID)
			if err != nil {
				return nil, err
			}
			memberUserIDs, _ = s.Store.GetGroupMemberUserIDs(txCtx, conv.ConversationGroupID)
			if *archived {
				if err := s.Store.ArchiveConversation(txCtx, userID, convID); err != nil {
					return nil, err
				}
				now := time.Now().UTC()
				conv.ArchivedAt = &now
			} else {
				if err := s.Store.UnarchiveConversation(txCtx, userID, convID); err != nil {
					return nil, err
				}
				conv.ArchivedAt = nil
			}
			eventData = map[string]any{
				"conversation":       conv.ID,
				"conversation_group": conv.ConversationGroupID,
				"members":            memberUserIDs,
				"archived":           *archived,
			}
		} else {
			conv, err = s.Store.UpdateConversation(txCtx, userID, convID, title, metadata)
			if err != nil {
				return nil, err
			}
			eventData = map[string]any{
				"conversation":       conv.ID,
				"conversation_group": conv.ConversationGroupID,
			}
		}
		if s.EventBus != nil && conv != nil {
			events := []registryeventbus.Event{{
				Event:               "updated",
				Kind:                "conversation",
				Data:                eventData,
				ConversationGroupID: conv.ConversationGroupID,
				UserIDs:             append([]string(nil), memberUserIDs...),
			}}
			eventsToPublish, err = appendOutboxOrUseEvents(txCtx, s.Store, events)
			if err != nil {
				return nil, err
			}
		}
		return conv, nil
	})
	if err != nil {
		return nil, mapError(err)
	}
	publishGRPCEvents(ctx, s.Store, s.EventBus, eventsToPublish, "conversation")
	return conversationToProto(conv), nil
}

func (s *ConversationsServer) ListForks(ctx context.Context, req *pb.ListForksRequest) (*pb.ListForksResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsRead); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}
	var clientID *string
	if value := strings.TrimSpace(getClientID(ctx)); value != "" {
		clientID = &value
	}

	forks, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationForkNavigation, error) {
		return s.Store.ListForks(txCtx, userID, convID, clientID)
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListForksResponse{ConversationIds: forks.ConversationIDs}
	for _, point := range forks.ForkPoints {
		protoPoint := &pb.ConversationForkPoint{EntryId: uuidToBytes(point.EntryID)}
		for _, option := range point.Options {
			protoOption := &pb.ConversationForkOption{ConversationId: option.ConversationID, Title: option.Title, Preview: option.Preview, CreatedAt: option.CreatedAt.Format("2006-01-02T15:04:05Z")}
			if option.EntryID != nil {
				value := uuidToBytes(*option.EntryID)
				protoOption.EntryId = value
			}
			protoPoint.Options = append(protoPoint.Options, protoOption)
		}
		resp.ForkPoints = append(resp.ForkPoints, protoPoint)
	}
	return resp, nil
}

func (s *ConversationsServer) ListChildConversations(ctx context.Context, req *pb.ListChildConversationsRequest) (*pb.ListChildConversationsResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsRead); err != nil {
		return nil, err
	}
	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}
	var afterCursor *string
	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 20)
	if err != nil {
		return nil, err
	}
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
	}
	children, cursor, err := func() ([]registrystore.ConversationSummary, *string, error) {
		type result struct {
			items  []registrystore.ConversationSummary
			cursor *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			items, cursor, err := s.Store.ListChildConversations(txCtx, userID, convID, afterCursor, limit)
			return result{items: items, cursor: cursor}, err
		})
		return out.items, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}
	resp := &pb.ListChildConversationsResponse{PageInfo: &pb.PageInfo{}}
	for _, cs := range children {
		item := &pb.ChildConversationSummary{
			Id:          string(cs.ID),
			Title:       cs.Title,
			OwnerUserId: cs.OwnerUserID,
			CreatedAt:   cs.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt:   cs.UpdatedAt.Format("2006-01-02T15:04:05Z"),
			AccessLevel: mapAccessLevel(cs.AccessLevel),
			Archived:    cs.ArchivedAt != nil,
		}
		if cs.StartedByEntryID != nil {
			item.StartedByEntryId = uuidToBytes(*cs.StartedByEntryID)
		}
		if cs.StartedByConversationID != nil {
			item.StartedByConversationId = string(*cs.StartedByConversationID)
		}
		resp.Conversations = append(resp.Conversations, item)
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func conversationToProto(conv *registrystore.ConversationDetail) *pb.Conversation {
	c := &pb.Conversation{
		Id:                    string(conv.ID),
		Title:                 conv.Title,
		OwnerUserId:           conv.OwnerUserID,
		CreatedAt:             conv.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:             conv.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		AccessLevel:           mapAccessLevel(conv.AccessLevel),
		Archived:              conv.ArchivedAt != nil,
		HasResponseInProgress: conv.HasResponseInProgress,
	}
	if conv.ForkedAtEntryID != nil {
		c.ForkedAtEntryId = uuidToBytes(*conv.ForkedAtEntryID)
	}
	if conv.ForkedAtConversationID != nil {
		c.ForkedAtConversationId = string(*conv.ForkedAtConversationID)
	}
	if conv.StartedByConversationID != nil {
		c.StartedByConversationId = string(*conv.StartedByConversationID)
	}
	if conv.StartedByEntryID != nil {
		c.StartedByEntryId = uuidToBytes(*conv.StartedByEntryID)
	}
	if conv.AgentID != nil {
		c.AgentId = *conv.AgentID
	}
	if conv.Metadata != nil {
		if metadata, err := structpb.NewStruct(conv.Metadata); err == nil {
			c.Metadata = metadata
		}
	}
	return c
}

func adminConversationSummaryToProto(cs *registrystore.ConversationSummary) *pb.AdminConversationSummary {
	item := &pb.AdminConversationSummary{
		Id:          string(cs.ID),
		Title:       cs.Title,
		OwnerUserId: cs.OwnerUserID,
		CreatedAt:   cs.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   cs.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		AccessLevel: mapAccessLevel(cs.AccessLevel),
		Archived:    cs.ArchivedAt != nil,
		ClientId:    cs.ClientID,
	}
	if cs.AgentID != nil {
		item.AgentId = cs.AgentID
	}
	if cs.StartedByConversationID != nil {
		item.StartedByConversationId = string(*cs.StartedByConversationID)
	}
	if cs.StartedByEntryID != nil {
		item.StartedByEntryId = uuidToBytes(*cs.StartedByEntryID)
	}
	return item
}

func adminChildConversationSummaryToProto(cs *registrystore.ConversationSummary) *pb.AdminChildConversationSummary {
	item := &pb.AdminChildConversationSummary{
		Id:          string(cs.ID),
		Title:       cs.Title,
		OwnerUserId: cs.OwnerUserID,
		CreatedAt:   cs.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   cs.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		AccessLevel: mapAccessLevel(cs.AccessLevel),
		Archived:    cs.ArchivedAt != nil,
		ClientId:    cs.ClientID,
	}
	if cs.AgentID != nil {
		item.AgentId = cs.AgentID
	}
	if cs.StartedByConversationID != nil {
		item.StartedByConversationId = string(*cs.StartedByConversationID)
	}
	if cs.StartedByEntryID != nil {
		item.StartedByEntryId = uuidToBytes(*cs.StartedByEntryID)
	}
	return item
}

func adminConversationToProto(conv *registrystore.ConversationDetail) *pb.AdminConversation {
	c := &pb.AdminConversation{
		Id:                    string(conv.ID),
		Title:                 conv.Title,
		OwnerUserId:           conv.OwnerUserID,
		CreatedAt:             conv.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:             conv.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		AccessLevel:           mapAccessLevel(conv.AccessLevel),
		Archived:              conv.ArchivedAt != nil,
		ClientId:              conv.ClientID,
		HasResponseInProgress: conv.HasResponseInProgress,
	}
	if conv.AgentID != nil {
		c.AgentId = conv.AgentID
	}
	if conv.Metadata != nil {
		if metadata, err := structpb.NewStruct(conv.Metadata); err == nil {
			c.Metadata = metadata
		}
	}
	if conv.ForkedAtEntryID != nil {
		c.ForkedAtEntryId = uuidToBytes(*conv.ForkedAtEntryID)
	}
	if conv.ForkedAtConversationID != nil {
		c.ForkedAtConversationId = string(*conv.ForkedAtConversationID)
	}
	if conv.StartedByConversationID != nil {
		c.StartedByConversationId = string(*conv.StartedByConversationID)
	}
	if conv.StartedByEntryID != nil {
		c.StartedByEntryId = uuidToBytes(*conv.StartedByEntryID)
	}
	return c
}

// --- Entries Service ---

type EntriesServer struct {
	pb.UnimplementedEntriesServiceServer
	Store    registrystore.MemoryStore
	EventBus registryeventbus.EventBus
}

func (s *EntriesServer) ListEntries(ctx context.Context, req *pb.ListEntriesRequest) (*pb.ListEntriesResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsRead); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 50)
	if err != nil {
		return nil, err
	}

	// Validate mutually exclusive pagination controls.
	paginationCount := 0
	if req.GetPage() != nil && req.GetPage().GetPageToken() != "" {
		paginationCount++
	}
	if req.GetBeforePageToken() != "" {
		paginationCount++
	}
	if req.GetTail() {
		paginationCount++
	}
	if paginationCount > 1 {
		return nil, status.Error(codes.InvalidArgument, "page.page_token, before_page_token, and tail are mutually exclusive")
	}

	var afterCursor *string
	if req.GetPage() != nil && req.GetPage().GetPageToken() != "" {
		t := req.GetPage().GetPageToken()
		if _, err := uuid.Parse(t); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid page.page_token")
		}
		afterCursor = &t
	}
	var beforeCursor *string
	if req.GetBeforePageToken() != "" {
		t := req.GetBeforePageToken()
		if _, err := uuid.Parse(t); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid before_page_token")
		}
		beforeCursor = &t
	}
	tail := req.GetTail()

	var upToEntryID *string
	if len(req.GetUpToEntryId()) > 0 {
		id, err := bytesToUUID(req.GetUpToEntryId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid up_to_entry_id")
		}
		value := id.String()
		upToEntryID = &value
	}

	var channelPtr *model.Channel
	switch req.GetChannel() {
	case pb.Channel_CONTEXT:
		ch := model.ChannelContext
		channelPtr = &ch
	case pb.Channel_JOURNAL:
		ch := model.ChannelJournal
		channelPtr = &ch
	case pb.Channel_HISTORY:
		ch := model.ChannelHistory
		channelPtr = &ch
	case pb.Channel_CHANNEL_UNSPECIFIED:
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid channel")
	}

	clientID := getClientID(ctx)
	var clientIDPtr *string
	if clientID != "" {
		clientIDPtr = &clientID
	}
	var agentIDPtr *string
	if req.AgentId != nil {
		agentID := strings.TrimSpace(req.GetAgentId())
		if agentID != "" {
			if clientIDPtr == nil {
				return nil, status.Error(codes.InvalidArgument, "agent_id requires an authenticated client")
			}
			agentIDPtr = &agentID
		}
	}

	var epochFilter *registrystore.MemoryEpochFilter
	if channelPtr == nil && clientIDPtr == nil {
		ch := model.ChannelHistory
		channelPtr = &ch
	}
	// Explicit context/journal channel requests without a client id are forbidden.
	if channelPtr != nil && (*channelPtr == model.ChannelContext || *channelPtr == model.ChannelJournal) && clientIDPtr == nil {
		return nil, status.Error(codes.PermissionDenied, "channel requires an authenticated client id")
	}
	if channelPtr != nil && *channelPtr == model.ChannelContext {
		filter, err := registrystore.ParseMemoryEpochFilter(req.GetEpochFilter())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		epochFilter = filter
	}

	allForks := req.GetForks() == "all"
	fromSeq := req.FromSeq

	result, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.PagedEntries, error) {
		return s.Store.GetEntries(txCtx, userID, convID, registrystore.EntryListQuery{
			AfterCursor:  afterCursor,
			BeforeCursor: beforeCursor,
			Tail:         tail,
			UpToEntryID:  upToEntryID,
			Limit:        limit,
			Channel:      channelPtr,
			EpochFilter:  epochFilter,
			ClientID:     clientIDPtr,
			AgentID:      agentIDPtr,
			AllForks:     allForks,
			FromSeq:      fromSeq,
		})
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListEntriesResponse{PageInfo: &pb.PageInfo{}}
	for _, e := range result.Data {
		resp.Entries = append(resp.Entries, entryToProto(&e))
	}
	if result.AfterCursor != nil {
		resp.PageInfo.NextPageToken = *result.AfterCursor
	}
	if result.BeforeCursor != nil {
		resp.PageInfo.PreviousPageToken = *result.BeforeCursor
	}
	return resp, nil
}

type AdminEntriesServer struct {
	pb.UnimplementedAdminEntriesServiceServer
	Store registrystore.MemoryStore
}

func (s *AdminEntriesServer) ListEntries(ctx context.Context, req *pb.AdminListEntriesRequest) (*pb.ListEntriesResponse, error) {
	if !hasGRPCAdminEventAccess(ctx) {
		return nil, status.Error(codes.PermissionDenied, "admin or auditor role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminConversationsRead); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	// Validate mutually exclusive pagination controls.
	paginationCount := 0
	if req.GetPage() != nil && req.GetPage().GetPageToken() != "" {
		paginationCount++
	}
	if req.GetBeforePageToken() != "" {
		paginationCount++
	}
	if req.GetTail() {
		paginationCount++
	}
	if paginationCount > 1 {
		return nil, status.Error(codes.InvalidArgument, "page.page_token, before_page_token, and tail are mutually exclusive")
	}

	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 20)
	if err != nil {
		return nil, err
	}
	query := registrystore.AdminMessageQuery{Limit: limit}
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			if _, err := uuid.Parse(t); err != nil {
				return nil, status.Error(codes.InvalidArgument, "invalid page.page_token")
			}
			query.AfterCursor = &t
		}
	}
	if req.GetBeforePageToken() != "" {
		t := req.GetBeforePageToken()
		if _, err := uuid.Parse(t); err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid before_page_token")
		}
		query.BeforeCursor = &t
	}
	query.Tail = req.GetTail()
	if len(req.GetUpToEntryId()) > 0 {
		id, err := bytesToUUID(req.GetUpToEntryId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid up_to_entry_id")
		}
		value := id.String()
		query.UpToEntryID = &value
	}
	switch req.GetChannel() {
	case pb.Channel_CONTEXT:
		ch := model.ChannelContext
		query.Channel = &ch
	case pb.Channel_JOURNAL:
		ch := model.ChannelJournal
		query.Channel = &ch
	case pb.Channel_HISTORY:
		ch := model.ChannelHistory
		query.Channel = &ch
	case pb.Channel_CHANNEL_UNSPECIFIED:
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid channel")
	}
	if req.GetEpochFilter() != "" {
		filter, err := registrystore.ParseMemoryEpochFilter(req.GetEpochFilter())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		query.EpochFilter = filter
	}
	query.AllForks = req.GetForks() == "all"
	query.FromSeq = req.FromSeq

	result, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.PagedEntries, error) {
		return s.Store.AdminGetEntries(txCtx, convID, query)
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListEntriesResponse{PageInfo: &pb.PageInfo{}}
	for _, e := range result.Data {
		resp.Entries = append(resp.Entries, entryToProto(&e))
	}
	if result.AfterCursor != nil {
		resp.PageInfo.NextPageToken = *result.AfterCursor
	}
	if result.BeforeCursor != nil {
		resp.PageInfo.PreviousPageToken = *result.BeforeCursor
	}
	return resp, nil
}

func (s *AdminEntriesServer) GetEntry(ctx context.Context, req *pb.AdminGetEntryRequest) (*pb.Entry, error) {
	if !hasGRPCAdminEventAccess(ctx) {
		return nil, status.Error(codes.PermissionDenied, "admin or auditor role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminConversationsRead); err != nil {
		return nil, err
	}

	entryID, err := bytesToUUID(req.GetEntryId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid entry_id")
	}

	entry, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*model.Entry, error) {
		return s.Store.AdminGetEntryByID(txCtx, entryID)
	})
	if err != nil {
		return nil, mapError(err)
	}

	return entryToProto(entry), nil
}

// --- Admin Conversations Service ---

type AdminConversationsServer struct {
	pb.UnimplementedAdminConversationsServiceServer
	Store  registrystore.MemoryStore
	Config *config.Config
}

func (s *AdminConversationsServer) requireReadAccess(ctx context.Context, justification string) error {
	id, err := requireGRPCIdentity(ctx)
	if err != nil {
		return err
	}
	if !id.Roles[security.RoleAdmin] && !id.Roles[security.RoleAuditor] {
		return status.Error(codes.PermissionDenied, "admin or auditor role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminConversationsRead); err != nil {
		return err
	}
	if s.Config != nil && s.Config.RequireJustification && strings.TrimSpace(justification) == "" {
		return status.Error(codes.InvalidArgument, "admin justification required")
	}
	return nil
}

func (s *AdminConversationsServer) requireAdminAccess(ctx context.Context, justification string) error {
	id, err := requireGRPCIdentity(ctx)
	if err != nil {
		return err
	}
	if !id.Roles[security.RoleAdmin] {
		return status.Error(codes.PermissionDenied, "admin role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminConversationsWrite); err != nil {
		return err
	}
	if s.Config != nil && s.Config.RequireJustification && strings.TrimSpace(justification) == "" {
		return status.Error(codes.InvalidArgument, "admin justification required")
	}
	return nil
}

func (s *AdminConversationsServer) GetConversation(ctx context.Context, req *pb.AdminGetConversationRequest) (*pb.AdminConversation, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	conv, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
		return s.Store.AdminGetConversation(txCtx, convID)
	})
	if err != nil {
		return nil, mapError(err)
	}

	return adminConversationToProto(conv), nil
}

func (s *AdminConversationsServer) ListConversations(ctx context.Context, req *pb.AdminListConversationsRequest) (*pb.AdminListConversationsResponse, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}

	mode := model.ListModeLatestFork
	switch req.GetMode() {
	case pb.ConversationListMode_ALL:
		mode = model.ListModeAll
	case pb.ConversationListMode_ROOTS:
		mode = model.ListModeRoots
	}

	ancestry := model.ConversationAncestryRoots
	switch req.GetAncestry() {
	case pb.ConversationAncestryFilter_CONVERSATION_ANCESTRY_FILTER_CHILDREN:
		ancestry = model.ConversationAncestryChildren
	case pb.ConversationAncestryFilter_CONVERSATION_ANCESTRY_FILTER_ALL:
		ancestry = model.ConversationAncestryAll
	}

	archived := registrystore.ArchiveFilterExclude
	switch req.GetArchived() {
	case pb.ArchiveFilter_ARCHIVE_FILTER_INCLUDE:
		archived = registrystore.ArchiveFilterInclude
	case pb.ArchiveFilter_ARCHIVE_FILTER_ONLY:
		archived = registrystore.ArchiveFilterOnly
	}

	var afterCursor *string
	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 20)
	if err != nil {
		return nil, err
	}
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
	}

	var userID *string
	if req.GetOwnerUserId() != "" {
		u := req.GetOwnerUserId()
		userID = &u
	}

	var archivedAfter *time.Time
	if req.GetArchivedAfter() != nil {
		t := req.GetArchivedAfter().AsTime()
		archivedAfter = &t
	}
	var archivedBefore *time.Time
	if req.GetArchivedBefore() != nil {
		t := req.GetArchivedBefore().AsTime()
		archivedBefore = &t
	}

	query := registrystore.AdminConversationQuery{
		Mode:           mode,
		Ancestry:       ancestry,
		UserID:         userID,
		Archived:       archived,
		ArchivedAfter:  archivedAfter,
		ArchivedBefore: archivedBefore,
		AfterCursor:    afterCursor,
		Limit:          limit,
	}

	summaries, cursor, err := func() ([]registrystore.ConversationSummary, *string, error) {
		type result struct {
			summaries []registrystore.ConversationSummary
			cursor    *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			summaries, cursor, err := s.Store.AdminListConversations(txCtx, query)
			return result{summaries: summaries, cursor: cursor}, err
		})
		return out.summaries, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.AdminListConversationsResponse{
		PageInfo: &pb.PageInfo{},
	}
	for _, cs := range summaries {
		resp.Conversations = append(resp.Conversations, adminConversationSummaryToProto(&cs))
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func (s *AdminConversationsServer) UpdateConversation(ctx context.Context, req *pb.AdminUpdateConversationRequest) (*pb.AdminConversation, error) {
	if err := s.requireAdminAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	conv, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
		if req.Archived != nil {
			if err := s.Store.AdminSetConversationArchived(txCtx, convID, *req.Archived); err != nil {
				return nil, err
			}
		}
		return s.Store.AdminGetConversation(txCtx, convID)
	})
	if err != nil {
		return nil, mapError(err)
	}

	return adminConversationToProto(conv), nil
}

func (s *AdminConversationsServer) ListMemberships(ctx context.Context, req *pb.AdminListMembershipsRequest) (*pb.ListMembershipsResponse, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var afterCursor *string
	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 20)
	if err != nil {
		return nil, err
	}
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
	}

	memberships, cursor, err := func() ([]model.ConversationMembership, *string, error) {
		type result struct {
			memberships []model.ConversationMembership
			cursor      *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			memberships, cursor, err := s.Store.AdminListMemberships(txCtx, convID, afterCursor, limit)
			return result{memberships: memberships, cursor: cursor}, err
		})
		return out.memberships, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListMembershipsResponse{PageInfo: &pb.PageInfo{}}
	for _, m := range memberships {
		resp.Memberships = append(resp.Memberships, &pb.ConversationMembership{
			ConversationId: string(convID),
			UserId:         m.UserID,
			AccessLevel:    mapAccessLevel(m.AccessLevel),
			CreatedAt:      m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func (s *AdminConversationsServer) ListForks(ctx context.Context, req *pb.AdminListForksRequest) (*pb.AdminListForksResponse, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	forks, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationForkNavigation, error) {
		return s.Store.AdminListForks(txCtx, convID)
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.AdminListForksResponse{ConversationIds: forks.ConversationIDs}
	for _, point := range forks.ForkPoints {
		protoPoint := &pb.ConversationForkPoint{EntryId: uuidToBytes(point.EntryID)}
		for _, option := range point.Options {
			protoOption := &pb.ConversationForkOption{
				ConversationId: option.ConversationID,
				Title:          option.Title,
				Preview:        option.Preview,
				CreatedAt:      option.CreatedAt.Format("2006-01-02T15:04:05Z"),
			}
			if option.EntryID != nil {
				protoOption.EntryId = uuidToBytes(*option.EntryID)
			}
			protoPoint.Options = append(protoPoint.Options, protoOption)
		}
		resp.ForkPoints = append(resp.ForkPoints, protoPoint)
	}
	return resp, nil
}

func (s *AdminConversationsServer) ListChildConversations(ctx context.Context, req *pb.AdminListChildConversationsRequest) (*pb.AdminListChildConversationsResponse, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var afterCursor *string
	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 20)
	if err != nil {
		return nil, err
	}
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
	}

	children, cursor, err := func() ([]registrystore.ConversationSummary, *string, error) {
		type result struct {
			items  []registrystore.ConversationSummary
			cursor *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			items, cursor, err := s.Store.AdminListChildConversations(txCtx, convID, afterCursor, limit)
			return result{items: items, cursor: cursor}, err
		})
		return out.items, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.AdminListChildConversationsResponse{PageInfo: &pb.PageInfo{}}
	for _, cs := range children {
		resp.Children = append(resp.Children, adminChildConversationSummaryToProto(&cs))
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func (s *EntriesServer) AppendEntry(ctx context.Context, req *pb.AppendEntryRequest) (*pb.Entry, error) {
	if req.GetEntry() == nil {
		return nil, status.Error(codes.InvalidArgument, "entry is required")
	}
	entries, err := s.appendEntries(ctx, req.GetConversationId(), []*pb.CreateEntryRequest{req.GetEntry()}, req.Epoch)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, status.Error(codes.Internal, "internal server error")
	}
	return entryToProto(&entries[0]), nil
}

func (s *EntriesServer) AppendEntries(ctx context.Context, req *pb.AppendEntriesRequest) (*pb.AppendEntriesResponse, error) {
	if len(req.GetEntries()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one entry is required")
	}
	entries, err := s.appendEntries(ctx, req.GetConversationId(), req.GetEntries(), req.Epoch)
	if err != nil {
		return nil, err
	}
	resp := &pb.AppendEntriesResponse{Entries: make([]*pb.Entry, 0, len(entries))}
	for i := range entries {
		resp.Entries = append(resp.Entries, entryToProto(&entries[i]))
	}
	return resp, nil
}

type grpcAppendEntry struct {
	request *pb.CreateEntryRequest
	store   registrystore.CreateEntryRequest
}

type grpcPendingAttachmentLink struct {
	attachmentID uuid.UUID
	entryIndex   int
}

func (s *EntriesServer) appendEntries(ctx context.Context, conversationID string, reqEntries []*pb.CreateEntryRequest, epoch *int64) ([]model.Entry, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsWrite); err != nil {
		return nil, err
	}
	convID, err := requiredConversationID(conversationID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	clientID := getClientID(ctx)
	var clientIDPtr *string
	if clientID != "" {
		clientIDPtr = &clientID
	}

	appendEntries := make([]grpcAppendEntry, 0, len(reqEntries))
	var agentIDPtr *string
	for i, entry := range reqEntries {
		if entry == nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("entries[%d] is required", i))
		}
		ch := string(model.ChannelHistory)
		explicitHistory := false
		switch entry.GetChannel() {
		case pb.Channel_CONTEXT:
			ch = string(model.ChannelContext)
		case pb.Channel_JOURNAL:
			ch = string(model.ChannelJournal)
		case pb.Channel_HISTORY:
			explicitHistory = true
		}

		if entry.GetUserId() != "" && entry.GetUserId() != userID {
			return nil, status.Error(codes.InvalidArgument, "user_id does not match the authenticated user")
		}
		if (ch == string(model.ChannelContext) || ch == string(model.ChannelJournal)) && clientID == "" {
			return nil, status.Error(codes.PermissionDenied, "client id is required for context/journal channel")
		}
		if entry.GetAgentId() != "" {
			entryAgentID := entry.GetAgentId()
			if agentIDPtr == nil {
				agentIDPtr = &entryAgentID
			} else if *agentIDPtr != entryAgentID {
				return nil, status.Error(codes.InvalidArgument, "all entries in a batch must use the same agent_id")
			}
		}
		if agentIDPtr != nil && clientID == "" {
			return nil, status.Error(codes.InvalidArgument, "agent_id requires an authenticated client")
		}
		if strings.TrimSpace(entry.GetContentType()) == "" {
			return nil, status.Error(codes.InvalidArgument, "content_type is required")
		}
		if ch != string(model.ChannelHistory) && entry.IndexedContent != nil {
			return nil, status.Error(codes.InvalidArgument, "indexed_content is only allowed on history channel")
		}
		if len(entry.GetContent()) > 1000 {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("content array exceeds maximum of 1000 elements (got %d)", len(entry.GetContent())))
		}
		if ch == string(model.ChannelHistory) && explicitHistory {
			ct := entry.GetContentType()
			if ct != "history" && !strings.HasPrefix(ct, "history/") {
				return nil, status.Error(codes.InvalidArgument, "History channel entries must use 'history' or 'history/<subtype>' as the contentType")
			}
			if len(entry.GetContent()) != 1 {
				return nil, status.Error(codes.InvalidArgument, "History channel entries must contain exactly 1 content object")
			}
			c := entry.GetContent()[0]
			if sv := c.GetStructValue(); sv != nil {
				roleField := sv.GetFields()["role"]
				if roleField == nil || (roleField.GetStringValue() != "USER" && roleField.GetStringValue() != "AI") {
					return nil, status.Error(codes.InvalidArgument, "History channel content must have a 'role' field with value 'USER' or 'AI'")
				}
			}
		}
		forkedAtConvID := optionalConversationID(entry.GetForkedAtConversationId())
		if entry.GetForkedAtConversationId() != "" && forkedAtConvID == nil {
			return nil, status.Error(codes.InvalidArgument, "invalid forked_at_conversation_id")
		}
		forkedAtEntryID, err := uuidFromBytes(entry.GetForkedAtEntryId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid forked_at_entry_id")
		}
		startedByEntryID, err := uuidFromBytes(entry.GetStartedByEntryId())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid started_by_entry_id")
		}
		content, err := protoValuesToRawJSON(entry.GetContent())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "content must be valid JSON")
		}
		appendEntries = append(appendEntries, grpcAppendEntry{
			request: entry,
			store: registrystore.CreateEntryRequest{
				Content:                 content,
				ContentType:             entry.GetContentType(),
				Channel:                 ch,
				IndexedContent:          entry.IndexedContent,
				UserID:                  stringPtrIfNotEmpty(entry.GetUserId()),
				AgentID:                 stringPtrIfNotEmpty(entry.GetAgentId()),
				ForkedAtConversationID:  forkedAtConvID,
				ForkedAtEntryID:         forkedAtEntryID,
				Seq:                     entry.Seq,
				StartedByConversationID: optionalConversationID(entry.GetStartedByConversationId()),
				StartedByEntryID:        startedByEntryID,
			},
		})
	}

	var eventsToPublish []registryeventbus.Event
	entries, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) ([]model.Entry, error) {
		existing, _ := s.Store.GetConversation(txCtx, userID, convID)
		convExistedBefore := existing != nil

		storeEntries := make([]registrystore.CreateEntryRequest, 0, len(appendEntries))
		var pendingLinks []grpcPendingAttachmentLink
		for i, appendEntry := range appendEntries {
			if appendEntry.store.Channel == string(model.ChannelHistory) {
				modified, links, err := resolveGRPCAttachmentRefs(txCtx, s.Store, userID, convID, appendEntry.store.Content)
				if err != nil {
					return nil, err
				}
				if modified != nil {
					appendEntry.store.Content = modified
				}
				for _, id := range links {
					pendingLinks = append(pendingLinks, grpcPendingAttachmentLink{attachmentID: id, entryIndex: i})
				}
			}
			storeEntries = append(storeEntries, appendEntry.store)
		}

		entries, err := s.Store.AppendEntries(txCtx, userID, convID, storeEntries, clientIDPtr, agentIDPtr, epoch)
		if err != nil {
			return nil, err
		}
		if err := linkGRPCAttachmentLinks(txCtx, s.Store, userID, entries, pendingLinks); err != nil {
			return nil, err
		}
		if s.EventBus != nil && len(entries) > 0 {
			eventsToPublish, err = s.entryEventsForCreatedEntries(txCtx, convID, entries, !convExistedBefore)
			if err != nil {
				return nil, err
			}
		}
		return entries, nil
	})
	if err != nil {
		return nil, mapError(err)
	}
	s.publishEntryEvents(ctx, eventsToPublish)
	return entries, nil
}

func (s *EntriesServer) SyncEntries(ctx context.Context, req *pb.SyncEntriesRequest) (*pb.SyncEntriesResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionConversationsWrite); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	clientID := getClientID(ctx)
	if clientID == "" {
		return nil, status.Error(codes.InvalidArgument, "x-client-id metadata required")
	}

	entry := req.GetEntry()
	if entry.GetChannel() != pb.Channel_CONTEXT {
		return nil, status.Error(codes.InvalidArgument, "sync entry must target context channel")
	}
	var syncContent json.RawMessage
	if len(entry.GetContent()) > 0 {
		list, _ := structpb.NewList(nil)
		list.Values = append(list.Values, entry.GetContent()...)
		syncContent, _ = list.MarshalJSON()
	}

	var eventsToPublish []registryeventbus.Event
	result, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.SyncResult, error) {
		existing, _ := s.Store.GetConversation(txCtx, userID, convID)
		convExistedBefore := existing != nil
		result, err := s.Store.SyncAgentEntry(txCtx, userID, convID, registrystore.CreateEntryRequest{
			Content:     syncContent,
			ContentType: entry.GetContentType(),
			Channel:     string(model.ChannelContext),
			AgentID:     stringPtrIfNotEmpty(entry.GetAgentId()),
			Seq:         entry.Seq,
		}, clientID, stringPtrIfNotEmpty(entry.GetAgentId()))
		if err != nil {
			return nil, err
		}
		if s.EventBus != nil && result.Entry != nil {
			eventsToPublish, err = s.entryEventsForCreatedEntries(txCtx, convID, []model.Entry{*result.Entry}, !convExistedBefore)
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.SyncEntriesResponse{
		NoOp:             result.NoOp,
		EpochIncremented: result.EpochIncremented,
	}
	if result.Epoch != nil {
		resp.Epoch = result.Epoch
	}
	if result.Entry != nil {
		resp.Entry = entryToProto(result.Entry)
	}
	s.publishEntryEvents(ctx, eventsToPublish)
	return resp, nil
}

func (s *EntriesServer) entryEventsForCreatedEntries(ctx context.Context, convID string, entries []model.Entry, includeConversationCreated bool) ([]registryeventbus.Event, error) {
	groupID, err := s.Store.GetEntryGroupID(ctx, entries[0].ID)
	if err != nil {
		return nil, err
	}
	events := make([]registryeventbus.Event, 0, len(entries)+1)
	if includeConversationCreated {
		events = append(events, registryeventbus.Event{
			Event: "created",
			Kind:  "conversation",
			Data: map[string]any{
				"conversation":       convID,
				"conversation_group": groupID,
			},
			ConversationGroupID: groupID,
		})
	}
	for _, entry := range entries {
		events = append(events, registryeventbus.Event{
			Event:               "created",
			Kind:                "entry",
			Data:                eventstream.EntryEventData(entry, groupID),
			ConversationGroupID: groupID,
		})
	}
	appended, used, err := eventstream.AppendOutboxEvents(ctx, s.Store, events...)
	if err != nil {
		return nil, err
	}
	if used {
		return appended, nil
	}
	return events, nil
}

func (s *EntriesServer) publishEntryEvents(ctx context.Context, events []registryeventbus.Event) {
	if s.EventBus == nil || len(events) == 0 {
		return
	}
	if err := eventstream.PublishEvents(ctx, s.Store, s.EventBus, events...); err != nil {
		log.Warn("Failed to publish gRPC entry events", "err", err)
	}
}

func protoValuesToRawJSON(values []*structpb.Value) (json.RawMessage, error) {
	if len(values) == 0 {
		return nil, nil
	}
	list := &structpb.ListValue{Values: append([]*structpb.Value(nil), values...)}
	return list.MarshalJSON()
}

func resolveGRPCAttachmentRefs(ctx context.Context, store registrystore.MemoryStore, userID string, convID string, content json.RawMessage) (json.RawMessage, []uuid.UUID, error) {
	var contentArr []map[string]any
	if err := json.Unmarshal(content, &contentArr); err != nil {
		return nil, nil, nil
	}

	modified := false
	var linkedIDs []uuid.UUID
	for ci, contentObj := range contentArr {
		attachmentsRaw, ok := contentObj["attachments"]
		if !ok {
			continue
		}
		attachmentsJSON, err := json.Marshal(attachmentsRaw)
		if err != nil {
			continue
		}
		var attachments []map[string]any
		if err := json.Unmarshal(attachmentsJSON, &attachments); err != nil {
			continue
		}
		for ai, att := range attachments {
			attachmentIDStr, ok := att["attachmentId"].(string)
			if !ok {
				continue
			}
			attachmentID, err := uuid.Parse(attachmentIDStr)
			if err != nil {
				continue
			}
			attachment, err := store.GetAttachment(ctx, userID, "", attachmentID)
			if err != nil {
				return nil, nil, err
			}
			if attachment.EntryID != nil {
				conv, err := store.GetConversation(ctx, userID, convID)
				if err != nil {
					return nil, nil, err
				}
				sourceGroupID, err := store.GetEntryGroupID(ctx, *attachment.EntryID)
				if err != nil {
					return nil, nil, err
				}
				if sourceGroupID != conv.ConversationGroupID {
					return nil, nil, &registrystore.ForbiddenError{}
				}
				newAttachment, err := store.CreateAttachment(ctx, userID, "", model.Attachment{
					StorageKey:  attachment.StorageKey,
					Filename:    attachment.Filename,
					ContentType: attachment.ContentType,
					Size:        attachment.Size,
					SHA256:      attachment.SHA256,
					Status:      "ready",
					ExpiresAt:   attachment.ExpiresAt,
				})
				if err != nil {
					return nil, nil, err
				}
				linkedIDs = append(linkedIDs, newAttachment.ID)
				att["attachmentId"] = newAttachment.ID.String()
			} else {
				linkedIDs = append(linkedIDs, attachmentID)
			}
			if _, hasCT := att["contentType"]; !hasCT {
				att["contentType"] = attachment.ContentType
			}
			if _, hasName := att["name"]; !hasName && attachment.Filename != nil {
				att["name"] = *attachment.Filename
			}
			attachments[ai] = att
			modified = true
		}
		contentObj["attachments"] = attachments
		contentArr[ci] = contentObj
	}
	if !modified {
		return nil, linkedIDs, nil
	}
	modifiedJSON, err := json.Marshal(contentArr)
	if err != nil {
		return nil, nil, err
	}
	return modifiedJSON, linkedIDs, nil
}

func linkGRPCAttachmentRefs(ctx context.Context, store registrystore.MemoryStore, userID string, entries []model.Entry, attachmentIDs []uuid.UUID) error {
	if len(entries) == 0 || len(attachmentIDs) == 0 {
		return nil
	}
	links := make([]grpcPendingAttachmentLink, 0, len(attachmentIDs))
	for _, attachmentID := range attachmentIDs {
		links = append(links, grpcPendingAttachmentLink{attachmentID: attachmentID})
	}
	return linkGRPCAttachmentLinks(ctx, store, userID, entries, links)
}

func linkGRPCAttachmentLinks(ctx context.Context, store registrystore.MemoryStore, userID string, entries []model.Entry, links []grpcPendingAttachmentLink) error {
	if len(entries) == 0 || len(links) == 0 {
		return nil
	}
	for _, link := range links {
		if link.entryIndex >= len(entries) {
			continue
		}
		entryID := entries[link.entryIndex].ID
		if _, err := store.UpdateAttachment(ctx, userID, link.attachmentID, registrystore.AttachmentUpdate{EntryID: &entryID}); err != nil {
			return err
		}
	}
	return nil
}

func entryToProto(e *model.Entry) *pb.Entry {
	entry := &pb.Entry{
		Id:             uuidToBytes(e.ID),
		ConversationId: string(e.ConversationID),
		Channel:        mapChannel(e.Channel),
		ContentType:    e.ContentType,
		CreatedAt:      e.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if e.UserID != nil {
		entry.UserId = *e.UserID
	}
	if e.ClientID != nil {
		entry.ClientId = e.ClientID
	}
	if e.Epoch != nil {
		entry.Epoch = *e.Epoch
	}
	if e.Seq != nil {
		v := *e.Seq
		entry.Seq = &v
	}
	if e.AgentID != nil {
		entry.AgentId = e.AgentID
	}
	if e.IndexedContent != nil {
		entry.IndexedContent = e.IndexedContent
	}
	if e.IndexedAt != nil {
		entry.IndexedAt = timestamppb.New(e.IndexedAt.UTC())
	}
	// Content is stored as encrypted bytes; try to parse as JSON values
	if e.Content != nil {
		var list structpb.ListValue
		if err := list.UnmarshalJSON(e.Content); err == nil {
			entry.Content = list.Values
		}
	}
	return entry
}

// --- Memberships Service ---

type MembershipsServer struct {
	pb.UnimplementedConversationMembershipsServiceServer
	Store    registrystore.MemoryStore
	EventBus registryeventbus.EventBus
}

func (s *MembershipsServer) ListMemberships(ctx context.Context, req *pb.ListMembershipsRequest) (*pb.ListMembershipsResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingRead); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var afterCursor *string
	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 20)
	if err != nil {
		return nil, err
	}
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
	}

	memberships, cursor, err := func() ([]model.ConversationMembership, *string, error) {
		type result struct {
			memberships []model.ConversationMembership
			cursor      *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			memberships, cursor, err := s.Store.ListMemberships(txCtx, userID, convID, afterCursor, limit)
			return result{memberships: memberships, cursor: cursor}, err
		})
		return out.memberships, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListMembershipsResponse{PageInfo: &pb.PageInfo{}}
	for _, m := range memberships {
		resp.Memberships = append(resp.Memberships, &pb.ConversationMembership{
			ConversationId: string(convID),
			UserId:         m.UserID,
			AccessLevel:    mapAccessLevel(m.AccessLevel),
			CreatedAt:      m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func (s *MembershipsServer) ShareConversation(ctx context.Context, req *pb.ShareConversationRequest) (*pb.ConversationMembership, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingWrite); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	level := protoToAccessLevel(req.GetAccessLevel())
	var eventsToPublish []registryeventbus.Event
	m, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*model.ConversationMembership, error) {
		membership, err := s.Store.ShareConversation(txCtx, userID, convID, req.GetUserId(), level)
		if err != nil {
			return nil, err
		}
		if s.EventBus != nil && membership != nil {
			events := []registryeventbus.Event{{
				Event: "created",
				Kind:  "membership",
				Data: map[string]any{
					"conversation_group": membership.ConversationGroupID,
					"user":               membership.UserID,
					"role":               membership.AccessLevel,
				},
				ConversationGroupID: membership.ConversationGroupID,
				UserIDs:             []string{membership.UserID},
			}}
			eventsToPublish, err = appendOutboxOrUseEvents(txCtx, s.Store, events)
			if err != nil {
				return nil, err
			}
		}
		return membership, nil
	})
	if err != nil {
		return nil, mapError(err)
	}
	publishGRPCEvents(ctx, s.Store, s.EventBus, eventsToPublish, "membership")

	return &pb.ConversationMembership{
		ConversationId: string(convID),
		UserId:         m.UserID,
		AccessLevel:    mapAccessLevel(m.AccessLevel),
		CreatedAt:      m.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (s *MembershipsServer) UpdateMembership(ctx context.Context, req *pb.UpdateMembershipRequest) (*pb.ConversationMembership, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingWrite); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	level := protoToAccessLevel(req.GetAccessLevel())
	var eventsToPublish []registryeventbus.Event
	m, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*model.ConversationMembership, error) {
		membership, err := s.Store.UpdateMembership(txCtx, userID, convID, req.GetMemberUserId(), level)
		if err != nil {
			return nil, err
		}
		if s.EventBus != nil && membership != nil {
			events := []registryeventbus.Event{{
				Event: "updated",
				Kind:  "membership",
				Data: map[string]any{
					"conversation_group": membership.ConversationGroupID,
					"user":               membership.UserID,
					"role":               membership.AccessLevel,
				},
				ConversationGroupID: membership.ConversationGroupID,
				UserIDs:             []string{membership.UserID},
			}}
			eventsToPublish, err = appendOutboxOrUseEvents(txCtx, s.Store, events)
			if err != nil {
				return nil, err
			}
		}
		return membership, nil
	})
	if err != nil {
		return nil, mapError(err)
	}
	publishGRPCEvents(ctx, s.Store, s.EventBus, eventsToPublish, "membership")

	return &pb.ConversationMembership{
		ConversationId: string(convID),
		UserId:         m.UserID,
		AccessLevel:    mapAccessLevel(m.AccessLevel),
		CreatedAt:      m.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (s *MembershipsServer) DeleteMembership(ctx context.Context, req *pb.DeleteMembershipRequest) (*emptypb.Empty, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingWrite); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var eventsToPublish []registryeventbus.Event
	if err := inMemoryWrite(ctx, s.Store, func(txCtx context.Context) error {
		conv, err := s.Store.GetConversation(txCtx, userID, convID)
		if err != nil {
			return err
		}
		if err := s.Store.DeleteMembership(txCtx, userID, convID, req.GetMemberUserId()); err != nil {
			return err
		}
		if s.EventBus != nil {
			events := []registryeventbus.Event{{
				Event: "deleted",
				Kind:  "membership",
				Data: map[string]any{
					"conversation_group": conv.ConversationGroupID,
					"user":               req.GetMemberUserId(),
				},
				ConversationGroupID: conv.ConversationGroupID,
				UserIDs:             []string{req.GetMemberUserId()},
			}}
			eventsToPublish, err = appendOutboxOrUseEvents(txCtx, s.Store, events)
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, mapError(err)
	}
	publishGRPCEvents(ctx, s.Store, s.EventBus, eventsToPublish, "membership")
	return &emptypb.Empty{}, nil
}

func protoToAccessLevel(level pb.AccessLevel) model.AccessLevel {
	switch level {
	case pb.AccessLevel_OWNER:
		return model.AccessLevelOwner
	case pb.AccessLevel_MANAGER:
		return model.AccessLevelManager
	case pb.AccessLevel_WRITER:
		return model.AccessLevelWriter
	case pb.AccessLevel_READER:
		return model.AccessLevelReader
	default:
		return model.AccessLevelReader
	}
}

// --- Ownership Transfers Service ---

type TransfersServer struct {
	pb.UnimplementedOwnershipTransfersServiceServer
	Store registrystore.MemoryStore
}

func (s *TransfersServer) ListOwnershipTransfers(ctx context.Context, req *pb.ListOwnershipTransfersRequest) (*pb.ListOwnershipTransfersResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingRead); err != nil {
		return nil, err
	}

	role := ""
	switch req.GetRole() {
	case pb.TransferRole_SENDER:
		role = "sender"
	case pb.TransferRole_RECIPIENT:
		role = "recipient"
	}

	var afterCursor *string
	limit, err := grpcPageSize(ctx, req.GetPage().GetPageSize(), 20)
	if err != nil {
		return nil, err
	}
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
	}

	transfers, cursor, err := func() ([]registrystore.OwnershipTransferDto, *string, error) {
		type result struct {
			transfers []registrystore.OwnershipTransferDto
			cursor    *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			transfers, cursor, err := s.Store.ListPendingTransfers(txCtx, userID, role, afterCursor, limit)
			return result{transfers: transfers, cursor: cursor}, err
		})
		return out.transfers, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListOwnershipTransfersResponse{PageInfo: &pb.PageInfo{}}
	for _, t := range transfers {
		resp.Transfers = append(resp.Transfers, &pb.OwnershipTransfer{
			Id:             uuidToBytes(t.ID),
			ConversationId: string(t.ConversationID),
			FromUserId:     t.FromUserID,
			ToUserId:       t.ToUserID,
			CreatedAt:      t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func (s *TransfersServer) GetOwnershipTransfer(ctx context.Context, req *pb.GetOwnershipTransferRequest) (*pb.OwnershipTransfer, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingRead); err != nil {
		return nil, err
	}

	transferID, err := bytesToUUID(req.GetTransferId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer_id")
	}

	t, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.OwnershipTransferDto, error) {
		return s.Store.GetTransfer(txCtx, userID, transferID)
	})
	if err != nil {
		return nil, mapError(err)
	}

	return &pb.OwnershipTransfer{
		Id:             uuidToBytes(t.ID),
		ConversationId: string(t.ConversationID),
		FromUserId:     t.FromUserID,
		ToUserId:       t.ToUserID,
		CreatedAt:      t.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (s *TransfersServer) CreateOwnershipTransfer(ctx context.Context, req *pb.CreateOwnershipTransferRequest) (*pb.OwnershipTransfer, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingWrite); err != nil {
		return nil, err
	}

	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	t, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.OwnershipTransferDto, error) {
		return s.Store.CreateOwnershipTransfer(txCtx, userID, convID, req.GetNewOwnerUserId())
	})
	if err != nil {
		return nil, mapError(err)
	}

	return &pb.OwnershipTransfer{
		Id:             uuidToBytes(t.ID),
		ConversationId: string(t.ConversationID),
		FromUserId:     t.FromUserID,
		ToUserId:       t.ToUserID,
		CreatedAt:      t.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (s *TransfersServer) AcceptOwnershipTransfer(ctx context.Context, req *pb.AcceptOwnershipTransferRequest) (*emptypb.Empty, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingWrite); err != nil {
		return nil, err
	}

	transferID, err := bytesToUUID(req.GetTransferId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer_id")
	}

	if err := inMemoryWrite(ctx, s.Store, func(txCtx context.Context) error {
		return s.Store.AcceptTransfer(txCtx, userID, transferID)
	}); err != nil {
		return nil, mapError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *TransfersServer) DeleteOwnershipTransfer(ctx context.Context, req *pb.DeleteOwnershipTransferRequest) (*emptypb.Empty, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSharingWrite); err != nil {
		return nil, err
	}

	transferID, err := bytesToUUID(req.GetTransferId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid transfer_id")
	}

	if err := inMemoryWrite(ctx, s.Store, func(txCtx context.Context) error {
		return s.Store.DeleteTransfer(txCtx, userID, transferID)
	}); err != nil {
		return nil, mapError(err)
	}
	return &emptypb.Empty{}, nil
}

// --- Search Service ---

type SearchServer struct {
	pb.UnimplementedSearchServiceServer
	Store       registrystore.MemoryStore
	Config      *config.Config
	Embedder    registryembed.Embedder
	VectorStore registryvector.VectorStore
}

// isIndexer checks if the caller has the resolved indexer/admin role, matching
// REST role middleware behavior, then falls back to configured user lists for
// compatibility with existing raw-bearer fixture and local-user modes.
func (s *SearchServer) isIndexer(ctx context.Context, userID string) bool {
	if hasGRPCRole(ctx, security.RoleIndexer) || hasGRPCRole(ctx, security.RoleAdmin) {
		return true
	}
	if s.Config == nil {
		return false
	}
	for _, u := range strings.Split(s.Config.AdminUsers, ",") {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if u == userID || (strings.HasSuffix(u, "*") && strings.HasPrefix(userID, strings.TrimSuffix(u, "*"))) {
			return true // admin implies indexer
		}
	}
	for _, u := range strings.Split(s.Config.IndexerUsers, ",") {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if u == userID || (strings.HasSuffix(u, "*") && strings.HasPrefix(userID, strings.TrimSuffix(u, "*"))) {
			return true
		}
	}
	return false
}

func (s *SearchServer) SearchConversations(ctx context.Context, req *pb.SearchEntriesRequest) (*pb.SearchEntriesResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSearchRead); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}

	limit, err := grpcPageSize(ctx, req.GetLimit(), 20)
	if err != nil {
		return nil, err
	}
	includeEntry := true
	if req.IncludeEntry != nil {
		includeEntry = *req.IncludeEntry
	}
	groupByConversation := true
	if req.GroupByConversation != nil {
		groupByConversation = *req.GroupByConversation
	}
	searchTypes, err := grpcSearchTypes(req.GetSearchType())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	cursorMap, cursorTypes, err := decodeGRPCSearchCursor(req.GetAfter(), searchTypes)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	semanticAvailable := s.Config != nil && s.Config.SearchSemanticEnabled && s.Embedder != nil && s.VectorStore != nil && s.VectorStore.IsEnabled()
	fulltextAvailable := s.Config == nil || s.Config.SearchFulltextEnabled

	results, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.SearchResults, error) {
		return s.searchEntriesInTx(txCtx, userID, query, searchTypes, cursorMap, cursorTypes, limit, includeEntry, groupByConversation, semanticAvailable, fulltextAvailable)
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.SearchEntriesResponse{}
	for _, r := range results.Data {
		sr := &pb.SearchResult{
			ConversationId: string(r.ConversationID),
			EntryId:        uuidToBytes(r.EntryID),
			Score:          float32(r.Score),
		}
		if r.ConversationTitle != nil {
			sr.ConversationTitle = *r.ConversationTitle
		}
		if r.Highlights != nil {
			sr.Highlights = *r.Highlights
		}
		if r.Entry != nil {
			sr.Entry = entryToProto(r.Entry)
		}
		resp.Results = append(resp.Results, sr)
	}
	if results.AfterCursor != nil {
		resp.NextCursor = *results.AfterCursor
	}
	return resp, nil
}

const (
	grpcSearchTypeAuto     = "auto"
	grpcSearchTypeSemantic = "semantic"
	grpcSearchTypeFulltext = "fulltext"
)

type grpcSearchCursorToken struct {
	Types   []string          `json:"types"`
	Cursors map[string]string `json:"cursors"`
}

func decodeGRPCSearchCursor(raw string, requestedTypes []string) (map[string]string, []string, error) {
	cursorMap := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cursorMap, nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		// Compatibility with older gRPC clients that used a raw entry UUID cursor.
		if _, uuidErr := uuid.Parse(raw); uuidErr == nil {
			for _, searchType := range requestedTypes {
				if searchType != grpcSearchTypeAuto {
					cursorMap[searchType] = raw
				}
			}
			if len(cursorMap) == 0 {
				cursorMap[grpcSearchTypeSemantic] = raw
				cursorMap[grpcSearchTypeFulltext] = raw
			}
			return cursorMap, requestedTypes, nil
		}
		return nil, nil, fmt.Errorf("invalid after cursor: malformed cursor")
	}
	var token grpcSearchCursorToken
	if err := json.Unmarshal(decoded, &token); err != nil || len(token.Types) == 0 || len(token.Cursors) == 0 {
		return nil, nil, fmt.Errorf("invalid after cursor: malformed cursor")
	}
	for k, v := range token.Cursors {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if _, err := uuid.Parse(v); err != nil {
			return nil, nil, fmt.Errorf("invalid after cursor: malformed cursor")
		}
		cursorMap[k] = v
	}
	if len(cursorMap) == 0 {
		return nil, nil, fmt.Errorf("invalid after cursor: malformed cursor")
	}
	return cursorMap, token.Types, nil
}

func encodeGRPCSearchCursor(searchTypes []string, cursors map[string]string) *string {
	if len(cursors) == 0 {
		return nil
	}
	token := grpcSearchCursorToken{
		Types:   searchTypes,
		Cursors: cursors,
	}
	data, err := json.Marshal(token)
	if err != nil {
		return nil
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return &encoded
}

func grpcCursorPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func grpcSearchTypes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{grpcSearchTypeAuto}, nil
	}
	valid := map[string]struct{}{
		grpcSearchTypeAuto:     {},
		grpcSearchTypeSemantic: {},
		grpcSearchTypeFulltext: {},
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		value := strings.ToLower(strings.TrimSpace(item))
		if value == "" {
			return nil, fmt.Errorf("search_type cannot be empty")
		}
		if _, ok := valid[value]; !ok {
			return nil, fmt.Errorf("invalid search_type %q", item)
		}
		for _, existing := range out {
			if existing == value {
				goto next
			}
		}
		out = append(out, value)
	next:
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("search_type cannot be empty")
	}
	if len(out) > 1 {
		for _, value := range out {
			if value == grpcSearchTypeAuto {
				return nil, fmt.Errorf("search_type 'auto' cannot be combined with other search types")
			}
		}
	}
	return out, nil
}

func (s *SearchServer) searchEntriesInTx(ctx context.Context, userID string, query string, searchTypes []string, cursorMap map[string]string, cursorTypes []string, limit int, includeEntry bool, groupByConversation bool, semanticAvailable bool, fulltextAvailable bool) (*registrystore.SearchResults, error) {
	if len(searchTypes) == 1 && searchTypes[0] == grpcSearchTypeAuto {
		if len(cursorTypes) == 1 && cursorTypes[0] != grpcSearchTypeAuto {
			t := cursorTypes[0]
			var results *registrystore.SearchResults
			var err error
			switch t {
			case grpcSearchTypeSemantic:
				if !semanticAvailable {
					return nil, status.Error(codes.Unimplemented, "semantic search unavailable")
				}
				results, err = s.semanticSearchEntries(ctx, userID, query, grpcCursorPtr(cursorMap[t]), limit, includeEntry, groupByConversation)
			case grpcSearchTypeFulltext:
				if !fulltextAvailable {
					return nil, status.Error(codes.Unimplemented, "full-text search is disabled")
				}
				results, err = s.Store.SearchEntries(ctx, userID, query, grpcCursorPtr(cursorMap[t]), limit, includeEntry, groupByConversation)
			default:
				return nil, status.Error(codes.InvalidArgument, "invalid after cursor")
			}
			if err != nil {
				return nil, err
			}
			nextCursors := map[string]string{}
			if results.AfterCursor != nil {
				nextCursors[t] = *results.AfterCursor
			}
			return &registrystore.SearchResults{Data: results.Data, AfterCursor: encodeGRPCSearchCursor([]string{t}, nextCursors)}, nil
		}
		if semanticAvailable {
			results, err := s.semanticSearchEntries(ctx, userID, query, grpcCursorPtr(cursorMap[grpcSearchTypeSemantic]), limit, includeEntry, groupByConversation)
			if err == nil && (len(results.Data) > 0 || cursorMap[grpcSearchTypeSemantic] != "") {
				nextCursors := map[string]string{}
				if results.AfterCursor != nil {
					nextCursors[grpcSearchTypeSemantic] = *results.AfterCursor
				}
				return &registrystore.SearchResults{Data: results.Data, AfterCursor: encodeGRPCSearchCursor([]string{grpcSearchTypeSemantic}, nextCursors)}, nil
			}
			if err != nil {
				log.Warn("gRPC semantic search failed, falling back to fulltext", "err", err)
			}
		}
		if fulltextAvailable {
			results, err := s.Store.SearchEntries(ctx, userID, query, grpcCursorPtr(cursorMap[grpcSearchTypeFulltext]), limit, includeEntry, groupByConversation)
			if err != nil {
				return nil, err
			}
			nextCursors := map[string]string{}
			if results.AfterCursor != nil {
				nextCursors[grpcSearchTypeFulltext] = *results.AfterCursor
			}
			return &registrystore.SearchResults{Data: results.Data, AfterCursor: encodeGRPCSearchCursor([]string{grpcSearchTypeFulltext}, nextCursors)}, nil
		}
		if semanticAvailable {
			results, err := s.semanticSearchEntries(ctx, userID, query, grpcCursorPtr(cursorMap[grpcSearchTypeSemantic]), limit, includeEntry, groupByConversation)
			if err != nil {
				return nil, err
			}
			nextCursors := map[string]string{}
			if results.AfterCursor != nil {
				nextCursors[grpcSearchTypeSemantic] = *results.AfterCursor
			}
			return &registrystore.SearchResults{Data: results.Data, AfterCursor: encodeGRPCSearchCursor([]string{grpcSearchTypeSemantic}, nextCursors)}, nil
		}
		return nil, status.Error(codes.Unimplemented, "no search types are available")
	}

	combined := make([]registrystore.SearchResult, 0, len(searchTypes)*limit)
	nextCursors := make(map[string]string, len(searchTypes))
	for _, searchType := range searchTypes {
		var results *registrystore.SearchResults
		var err error
		switch searchType {
		case grpcSearchTypeSemantic:
			if !semanticAvailable {
				return nil, status.Error(codes.Unimplemented, "semantic search unavailable")
			}
			results, err = s.semanticSearchEntries(ctx, userID, query, grpcCursorPtr(cursorMap[searchType]), limit, includeEntry, groupByConversation)
		case grpcSearchTypeFulltext:
			if !fulltextAvailable {
				return nil, status.Error(codes.Unimplemented, "full-text search is disabled")
			}
			results, err = s.Store.SearchEntries(ctx, userID, query, grpcCursorPtr(cursorMap[searchType]), limit, includeEntry, groupByConversation)
		}
		if err != nil {
			return nil, err
		}
		combined = append(combined, results.Data...)
		if results.AfterCursor != nil {
			nextCursors[searchType] = *results.AfterCursor
		}
	}
	return &registrystore.SearchResults{Data: combined, AfterCursor: encodeGRPCSearchCursor(searchTypes, nextCursors)}, nil
}

func (s *SearchServer) semanticSearchEntries(ctx context.Context, userID string, query string, afterCursor *string, limit int, includeEntry bool, groupByConversation bool) (*registrystore.SearchResults, error) {
	groupIDs, err := s.Store.ListConversationGroupIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list group IDs: %w", err)
	}
	if len(groupIDs) == 0 {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	embeddings, err := s.Embedder.EmbedTexts(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	searchLimit := limit + 1
	if groupByConversation {
		searchLimit = limit*3 + 1
	}
	if afterCursor != nil && searchLimit < 1000 {
		searchLimit = 1000
	}
	if searchLimit > 5000 {
		searchLimit = 5000
	}
	vectorResults, err := s.VectorStore.Search(ctx, embeddings[0], groupIDs, searchLimit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	if len(vectorResults) == 0 {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	scores := make(map[uuid.UUID]float64, len(vectorResults))
	entryIDs := make([]uuid.UUID, 0, len(vectorResults))
	for _, r := range vectorResults {
		entryIDs = append(entryIDs, r.EntryID)
		scores[r.EntryID] = r.Score
	}
	details, err := s.Store.FetchSearchResultDetails(ctx, userID, entryIDs, includeEntry)
	if err != nil {
		return nil, fmt.Errorf("fetch details: %w", err)
	}
	for i := range details {
		details[i].Score = scores[details[i].EntryID]
		details[i].Kind = s.VectorStore.Name()
	}
	sort.SliceStable(details, func(i, j int) bool {
		if details[i].Score == details[j].Score {
			return details[i].EntryID.String() < details[j].EntryID.String()
		}
		return details[i].Score > details[j].Score
	})
	if groupByConversation {
		details = groupGRPCSearchResultsByConversation(details)
	}
	return paginateGRPCSearchResults(details, afterCursor, limit)
}

func groupGRPCSearchResultsByConversation(results []registrystore.SearchResult) []registrystore.SearchResult {
	out := make([]registrystore.SearchResult, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, r := range results {
		id := string(r.ConversationID)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, r)
	}
	return out
}

func paginateGRPCSearchResults(results []registrystore.SearchResult, afterCursor *string, limit int) (*registrystore.SearchResults, error) {
	start := 0
	if afterCursor != nil {
		cursorID, err := uuid.Parse(*afterCursor)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid after cursor")
		}
		start = len(results)
		for i := range results {
			if results[i].EntryID == cursorID {
				start = i + 1
				break
			}
		}
	}
	if start >= len(results) {
		return &registrystore.SearchResults{Data: []registrystore.SearchResult{}}, nil
	}
	end := start + limit
	if end > len(results) {
		end = len(results)
	}
	page := results[start:end]
	var nextCursor *string
	if end < len(results) && len(page) > 0 {
		v := page[len(page)-1].EntryID.String()
		nextCursor = &v
	}
	return &registrystore.SearchResults{Data: page, AfterCursor: nextCursor}, nil
}

func (s *SearchServer) IndexConversations(ctx context.Context, req *pb.IndexConversationsRequest) (*pb.IndexConversationsResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if !s.isIndexer(ctx, userID) {
		return nil, status.Error(codes.PermissionDenied, "indexer or admin role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSearchWrite); err != nil {
		return nil, err
	}

	if len(req.GetEntries()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one entry required")
	}
	var entries []registrystore.IndexEntryRequest
	for i, e := range req.GetEntries() {
		if e == nil {
			return nil, status.Errorf(codes.InvalidArgument, "entries[%d] is required", i)
		}
		convID, err := requiredConversationID(e.GetConversationId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "entries[%d].conversation_id is required", i)
		}
		entryID, err := bytesToUUID(e.GetEntryId())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "entries[%d].entry_id is required", i)
		}
		if strings.TrimSpace(e.GetIndexedContent()) == "" {
			return nil, status.Errorf(codes.InvalidArgument, "entries[%d].indexed_content is required", i)
		}
		entries = append(entries, registrystore.IndexEntryRequest{
			ConversationID: convID,
			EntryID:        entryID,
			IndexedContent: e.GetIndexedContent(),
		})
	}

	result, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.IndexConversationsResponse, error) {
		return s.Store.IndexEntries(txCtx, entries)
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.IndexConversationsResponse{Indexed: int32(result.Indexed)}, nil
}

func (s *SearchServer) ListUnindexedEntries(ctx context.Context, req *pb.ListUnindexedEntriesRequest) (*pb.ListUnindexedEntriesResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if !s.isIndexer(ctx, userID) {
		return nil, status.Error(codes.PermissionDenied, "indexer or admin role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionSearchRead); err != nil {
		return nil, err
	}

	limit, err := grpcPageSize(ctx, req.GetLimit(), 20)
	if err != nil {
		return nil, err
	}
	var afterCursor *string
	if req.Cursor != nil {
		afterCursor = req.Cursor
	}

	entries, cursor, err := func() ([]model.Entry, *string, error) {
		type result struct {
			entries []model.Entry
			cursor  *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			entries, cursor, err := s.Store.ListUnindexedEntries(txCtx, limit, afterCursor)
			return result{entries: entries, cursor: cursor}, err
		})
		return out.entries, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListUnindexedEntriesResponse{}
	for _, e := range entries {
		resp.Entries = append(resp.Entries, &pb.UnindexedEntry{
			ConversationId: string(e.ConversationID),
			Entry:          entryToProto(&e),
		})
	}
	if cursor != nil {
		resp.Cursor = cursor
	}
	return resp, nil
}

// --- Memories Service ---

type MemoriesServer struct {
	pb.UnimplementedMemoriesServiceServer
	Store    registryepisodic.EpisodicStore
	Policy   *episodic.PolicyEngine
	Config   *config.Config
	Embedder registryembed.Embedder
}

type AdminMemoriesServer struct {
	pb.UnimplementedAdminMemoriesServiceServer
	Store    registryepisodic.EpisodicStore
	Policy   *episodic.PolicyEngine
	Config   *config.Config
	Embedder registryembed.Embedder
}

func (s *MemoriesServer) PutMemory(ctx context.Context, req *pb.PutMemoryRequest) (*pb.MemoryWriteResult, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	if _, err := requireGRPCUser(ctx); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionMemoriesWrite); err != nil {
		return nil, err
	}
	if req.GetValue() == nil {
		return nil, status.Error(codes.InvalidArgument, "value is required")
	}
	if req.GetTtlSeconds() < 0 {
		return nil, status.Error(codes.InvalidArgument, "ttl_seconds must be >= 0")
	}

	namespace := req.GetNamespace()
	if err := validateMemoryNamespace(namespace, s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	key := req.GetKey()
	if key == "" || len(key) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "key must be non-empty and at most 1024 bytes")
	}

	value := req.GetValue().AsMap()
	index := req.GetIndex()
	if index == nil {
		index = map[string]string{}
	}

	pc, err := s.memoryPolicyContext(ctx)
	if err != nil {
		return nil, err
	}
	policyAttrs := map[string]interface{}{}
	if s.Policy != nil {
		decision, err := s.Policy.EvaluateAuthz(ctx, "write", namespace, key, value, index, pc)
		if err != nil {
			return nil, episodicInternalError("policy evaluation error", err)
		}
		if !decision.Allow {
			if decision.Reason != "" {
				return nil, status.Error(codes.PermissionDenied, decision.Reason)
			}
			return nil, status.Error(codes.PermissionDenied, "access denied")
		}
		extracted, err := s.Policy.ExtractAttributes(ctx, namespace, key, value, index, pc)
		if err != nil {
			return nil, episodicInternalError("attribute extraction error", err)
		}
		policyAttrs = extracted
	}

	result, err := withEpisodicWrite(ctx, s.Store, func(txCtx context.Context) (*registryepisodic.MemoryWriteResult, error) {
		return s.Store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace:        namespace,
			Key:              key,
			Value:            value,
			Index:            index,
			TTLSeconds:       int(req.GetTtlSeconds()),
			PolicyAttributes: policyAttrs,
			ExpectedRevision: req.ExpectedRevision,
		})
	})
	if err != nil {
		if errors.Is(err, registryepisodic.ErrMemoryRevisionConflict) {
			return nil, grpcStatusWithCause(codes.Aborted, "memory revision conflict", err)
		}
		return nil, episodicInternalError("failed to store memory", err)
	}
	resp, err := memoryWriteResultToProto(result)
	if err != nil {
		return nil, episodicInternalError("failed to encode memory write response", err)
	}
	return resp, nil
}

func (s *MemoriesServer) GetMemory(ctx context.Context, req *pb.GetMemoryRequest) (*pb.MemoryItem, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	if _, err := requireGRPCUser(ctx); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionMemoriesRead); err != nil {
		return nil, err
	}

	namespace := req.GetNamespace()
	if err := validateMemoryNamespace(namespace, s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	key := req.GetKey()
	if key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}
	archived := protoArchiveFilterToEpisodic(req.GetArchived())
	pc, err := s.memoryPolicyContext(ctx)
	if err != nil {
		return nil, err
	}

	if s.Policy != nil {
		decision, err := s.Policy.EvaluateAuthz(ctx, "read", namespace, key, nil, nil, pc)
		if err != nil {
			return nil, episodicInternalError("policy evaluation error", err)
		}
		if !decision.Allow {
			if decision.Reason != "" {
				return nil, status.Error(codes.PermissionDenied, decision.Reason)
			}
			return nil, status.Error(codes.PermissionDenied, "access denied")
		}
	}

	item, err := withEpisodicWrite(ctx, s.Store, func(txCtx context.Context) (*registryepisodic.MemoryItem, error) {
		item, err := s.Store.GetMemory(txCtx, namespace, key, archived)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return nil, nil
		}

		fetchedAt := time.Now().UTC()
		if err := s.Store.IncrementMemoryLoads(txCtx, []registryepisodic.MemoryKey{{
			Namespace: namespace,
			Key:       key,
		}}, fetchedAt); err != nil {
			log.Warn("failed to increment memory usage counters", "namespace", namespace, "key", key, "err", err)
		}

		if req.GetIncludeUsage() {
			usage, err := s.Store.GetMemoryUsage(txCtx, namespace, key)
			if err != nil {
				log.Warn("failed to load memory usage counters", "namespace", namespace, "key", key, "err", err)
			} else {
				item.Usage = usage
			}
		}
		return item, nil
	})
	if err != nil {
		return nil, episodicInternalError("failed to fetch memory", err)
	}
	if item == nil {
		return nil, status.Error(codes.NotFound, "memory not found")
	}

	resp, err := memoryItemToProto(*item)
	if err != nil {
		return nil, episodicInternalError("failed to encode memory response", err)
	}
	return resp, nil
}

func (s *MemoriesServer) UpdateMemory(ctx context.Context, req *pb.UpdateMemoryRequest) (*emptypb.Empty, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	if _, err := requireGRPCUser(ctx); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionMemoriesWrite); err != nil {
		return nil, err
	}

	namespace := req.GetNamespace()
	if err := validateMemoryNamespace(namespace, s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	key := req.GetKey()
	if key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}
	if req.Archived == nil || !req.GetArchived() {
		return nil, status.Error(codes.InvalidArgument, "archived must be true")
	}
	pc, err := s.memoryPolicyContext(ctx)
	if err != nil {
		return nil, err
	}

	if s.Policy != nil {
		decision, err := s.Policy.EvaluateAuthz(ctx, "update", namespace, key, nil, nil, pc)
		if err != nil {
			return nil, episodicInternalError("policy evaluation error", err)
		}
		if !decision.Allow {
			if decision.Reason != "" {
				return nil, status.Error(codes.PermissionDenied, decision.Reason)
			}
			return nil, status.Error(codes.PermissionDenied, "access denied")
		}
	}

	if err := inEpisodicWrite(ctx, s.Store, func(txCtx context.Context) error {
		return s.Store.ArchiveMemory(txCtx, namespace, key, req.ExpectedRevision)
	}); err != nil {
		if errors.Is(err, registryepisodic.ErrMemoryRevisionConflict) {
			return nil, grpcStatusWithCause(codes.Aborted, "memory revision conflict", err)
		}
		return nil, episodicInternalError("failed to archive memory", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *MemoriesServer) SearchMemories(ctx context.Context, req *pb.SearchMemoriesRequest) (*pb.SearchMemoriesResponse, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	if _, err := requireGRPCUser(ctx); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionMemoriesRead); err != nil {
		return nil, err
	}

	if len(req.GetNamespacePrefix()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "namespace_prefix is required")
	}
	if err := validateMemoryNamespace(req.GetNamespacePrefix(), s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	limit, err := grpcPageSize(ctx, req.GetLimit(), 10)
	if err != nil {
		return nil, err
	}

	filter := map[string]interface{}{}
	if req.GetFilter() != nil {
		filter = req.GetFilter().AsMap()
	}
	archived := protoArchiveFilterToEpisodic(req.GetArchived())
	effectivePrefix := req.GetNamespacePrefix()
	pc, err := s.memoryPolicyContext(ctx)
	if err != nil {
		return nil, err
	}
	policyFilter := map[string]interface{}{}
	if s.Policy != nil {
		var err error
		effectivePrefix, policyFilter, err = s.Policy.InjectFilterParts(ctx, req.GetNamespacePrefix(), filter, pc)
		if err != nil {
			return nil, episodicInternalError("filter injection error", err)
		}
	}
	normalizedFilter, err := registryepisodic.NormalizeAttributeFilters(filter, policyFilter)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	query := strings.TrimSpace(req.GetQuery())
	hasQueries := len(req.GetQueries()) > 0
	if query != "" && hasQueries {
		return nil, status.Error(codes.InvalidArgument, "query and queries are mutually exclusive")
	}
	if hasQueries {
		queries, err := protoMemorySearchQuerySpecs(req.GetQueries())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		perQueryLimit, err := protoPerQueryLimit(req.GetPerQueryLimit(), limit)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if s.Embedder == nil {
			return nil, status.Error(codes.Unavailable, "semantic search unavailable")
		}
		items, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.MemoryItem, error) {
			return multiQuerySemanticSearchMemories(txCtx, s.Store, s.Embedder, effectivePrefix, normalizedFilter, queries, perQueryLimit, limit, archived)
		})
		if err != nil {
			if errors.Is(err, registryepisodic.ErrSemanticSearchUnavailable) {
				return nil, grpcStatusWithCause(codes.Unavailable, "semantic search unavailable", err)
			}
			return nil, episodicInternalError("semantic search error", err)
		}
		if req.GetIncludeUsage() {
			if err := inEpisodicRead(ctx, s.Store, func(txCtx context.Context) error {
				enrichMemoryItemsWithUsage(txCtx, s.Store, items)
				return nil
			}); err != nil {
				return nil, episodicInternalError("failed to enrich memory usage", err)
			}
		}
		return memoryItemsToSearchResponse(items)
	}
	if query != "" {
		if s.Embedder == nil {
			return nil, status.Error(codes.Unavailable, "semantic search unavailable")
		}
		items, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.MemoryItem, error) {
			return semanticSearchMemories(txCtx, s.Store, s.Embedder, effectivePrefix, normalizedFilter, query, limit, archived)
		})
		if err != nil {
			if errors.Is(err, registryepisodic.ErrSemanticSearchUnavailable) {
				return nil, grpcStatusWithCause(codes.Unavailable, "semantic search unavailable", err)
			}
			return nil, episodicInternalError("semantic search error", err)
		}
		if req.GetIncludeUsage() {
			if err := inEpisodicRead(ctx, s.Store, func(txCtx context.Context) error {
				enrichMemoryItemsWithUsage(txCtx, s.Store, items)
				return nil
			}); err != nil {
				return nil, episodicInternalError("failed to enrich memory usage", err)
			}
		}
		return memoryItemsToSearchResponse(items)
	}

	items, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.MemoryItem, error) {
		return s.Store.SearchMemories(txCtx, effectivePrefix, normalizedFilter, limit, archived)
	})
	if err != nil {
		return nil, episodicInternalError("failed to search memories", err)
	}
	if req.GetIncludeUsage() {
		if err := inEpisodicRead(ctx, s.Store, func(txCtx context.Context) error {
			enrichMemoryItemsWithUsage(txCtx, s.Store, items)
			return nil
		}); err != nil {
			return nil, episodicInternalError("failed to enrich memory usage", err)
		}
	}
	return memoryItemsToSearchResponse(items)
}

func (s *MemoriesServer) ListMemoryNamespaces(ctx context.Context, req *pb.ListMemoryNamespacesRequest) (*pb.ListMemoryNamespacesResponse, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	if _, err := requireGRPCUser(ctx); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionMemoriesRead); err != nil {
		return nil, err
	}

	maxDepth := int(req.GetMaxDepth())
	if maxDepth < 0 {
		return nil, status.Error(codes.InvalidArgument, "max_depth must be >= 0")
	}

	prefix := req.GetPrefix()
	if len(prefix) == 0 {
		prefix = []string{}
	} else if err := validateMemoryNamespace(prefix, s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	suffix := req.GetSuffix()
	for i, seg := range suffix {
		if seg == "" {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("suffix segment %d is empty", i))
		}
	}

	archived := protoArchiveFilterToEpisodic(req.GetArchived())
	pc, err := s.memoryPolicyContext(ctx)
	if err != nil {
		return nil, err
	}

	if s.Policy != nil {
		var err error
		prefix, _, err = s.Policy.InjectFilter(ctx, prefix, nil, pc)
		if err != nil {
			return nil, episodicInternalError("filter injection error", err)
		}
	}

	namespaces, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([][]string, error) {
		return s.Store.ListNamespaces(txCtx, registryepisodic.ListNamespacesRequest{
			Prefix:   prefix,
			Suffix:   suffix,
			MaxDepth: maxDepth,
			Archived: archived,
		})
	})
	if err != nil {
		return nil, episodicInternalError("failed to list namespaces", err)
	}
	resp := &pb.ListMemoryNamespacesResponse{}
	for _, ns := range namespaces {
		resp.Namespaces = append(resp.Namespaces, &pb.MemoryNamespace{Segments: ns})
	}
	return resp, nil
}

func (s *MemoriesServer) ListMemoryEvents(ctx context.Context, req *pb.ListMemoryEventsRequest) (*pb.ListMemoryEventsResponse, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	if _, err := requireGRPCUser(ctx); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionMemoriesRead); err != nil {
		return nil, err
	}

	nsPrefix := append([]string(nil), req.GetNamespace()...)
	if len(nsPrefix) > 0 {
		if err := validateMemoryNamespace(nsPrefix, s.maxDepth()); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	pc, err := s.memoryPolicyContext(ctx)
	if err != nil {
		return nil, err
	}
	if s.Policy != nil {
		nsPrefix, _, err = s.Policy.InjectFilter(ctx, nsPrefix, nil, pc)
		if err != nil {
			return nil, episodicInternalError("filter injection error", err)
		}
	}

	limit, err := grpcPageSize(ctx, req.GetLimit(), 50)
	if err != nil {
		return nil, err
	}
	listReq := registryepisodic.ListEventsRequest{
		NamespacePrefix: nsPrefix,
		Kinds:           append([]string(nil), req.GetKinds()...),
		Limit:           limit,
	}
	if req.AfterCursor != nil {
		listReq.AfterCursor = req.GetAfterCursor()
	}
	if req.GetAfter() != nil {
		after := req.GetAfter().AsTime().UTC()
		listReq.After = &after
	}
	if req.GetBefore() != nil {
		before := req.GetBefore().AsTime().UTC()
		listReq.Before = &before
	}

	page, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) (*registryepisodic.MemoryEventPage, error) {
		return s.Store.ListMemoryEvents(txCtx, listReq)
	})
	if err != nil {
		return nil, episodicInternalError("failed to list memory events", err)
	}

	resp := &pb.ListMemoryEventsResponse{Events: make([]*pb.MemoryEventItem, 0, len(page.Events))}
	for _, event := range page.Events {
		item, err := memoryEventToProto(event)
		if err != nil {
			return nil, episodicInternalError("failed to encode memory event", err)
		}
		resp.Events = append(resp.Events, item)
	}
	if page.AfterCursor != "" {
		resp.AfterCursor = &page.AfterCursor
	}
	return resp, nil
}

func (s *AdminMemoriesServer) ListMemories(ctx context.Context, req *pb.AdminListMemoriesRequest) (*pb.AdminListMemoriesResponse, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesRead); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	prefix := req.GetNamespacePrefix()
	if len(prefix) > 0 {
		if err := validateMemoryNamespace(prefix, s.maxDepth()); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	limit, err := grpcPageSize(ctx, req.GetLimit(), 50)
	if err != nil {
		return nil, err
	}
	query := registryepisodic.AdminMemoryQuery{
		NamespacePrefix: prefix,
		KeyPrefix:       req.GetKeyPrefix(),
		Archived:        protoArchiveFilterToEpisodic(req.GetArchived()),
		Limit:           limit,
		AfterCursor:     req.GetAfterCursor(),
		IncludeUsage:    req.GetIncludeUsage(),
	}
	if req.GetCreatedAfter() != nil {
		t := req.GetCreatedAfter().AsTime()
		query.CreatedAfter = &t
	}
	if req.GetCreatedBefore() != nil {
		t := req.GetCreatedBefore().AsTime()
		query.CreatedBefore = &t
	}
	if req.GetExpiresBefore() != nil {
		t := req.GetExpiresBefore().AsTime()
		query.ExpiresBefore = &t
	}
	page, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) (registryepisodic.AdminMemoryPage, error) {
		return s.Store.AdminListMemories(txCtx, query)
	})
	if err != nil {
		return nil, episodicInternalError("failed to list admin memories", err)
	}
	if req.GetIncludeUsage() {
		if err := inEpisodicRead(ctx, s.Store, func(txCtx context.Context) error {
			enrichMemoryItemsWithUsage(txCtx, s.Store, page.Items)
			return nil
		}); err != nil {
			return nil, episodicInternalError("failed to enrich memory usage", err)
		}
	}
	resp := &pb.AdminListMemoriesResponse{AfterCursor: stringPtrIfNotEmpty(page.AfterCursor)}
	for _, item := range page.Items {
		pItem, err := adminMemoryItemToProto(item)
		if err != nil {
			return nil, episodicInternalError("failed to encode memory response", err)
		}
		resp.Items = append(resp.Items, pItem)
	}
	return resp, nil
}

func (s *AdminMemoriesServer) GetMemory(ctx context.Context, req *pb.AdminGetMemoryRequest) (*pb.AdminMemoryItem, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesRead); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	id, err := bytesToUUID(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	item, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) (*registryepisodic.MemoryItem, error) {
		item, err := s.Store.AdminGetMemoryByID(txCtx, id)
		if err != nil || item == nil || !req.GetIncludeUsage() {
			return item, err
		}
		usage, err := s.Store.GetMemoryUsage(txCtx, item.Namespace, item.Key)
		if err != nil {
			log.Warn("failed to load memory usage counters", "namespace", item.Namespace, "key", item.Key, "err", err)
		} else {
			item.Usage = usage
		}
		return item, nil
	})
	if err != nil {
		return nil, episodicInternalError("failed to get admin memory", err)
	}
	if item == nil {
		return nil, status.Error(codes.NotFound, "memory not found")
	}
	resp, err := adminMemoryItemToProto(*item)
	if err != nil {
		return nil, episodicInternalError("failed to encode memory response", err)
	}
	return resp, nil
}

func (s *AdminMemoriesServer) SearchMemories(ctx context.Context, req *pb.AdminSearchMemoriesRequest) (*pb.AdminSearchMemoriesResponse, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesRead); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	prefix := req.GetNamespacePrefix()
	if len(prefix) > 0 {
		if err := validateMemoryNamespace(prefix, s.maxDepth()); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	limit, err := grpcPageSize(ctx, req.GetLimit(), 10)
	if err != nil {
		return nil, err
	}
	filter := map[string]interface{}{}
	if req.GetFilter() != nil {
		filter = req.GetFilter().AsMap()
	}
	effectivePrefix := prefix
	policyFilter := map[string]interface{}{}
	if asUserID := strings.TrimSpace(req.GetAsUserId()); asUserID != "" {
		if s.Policy == nil {
			return nil, status.Error(codes.FailedPrecondition, "memory policy is not configured")
		}
		var err error
		effectivePrefix, policyFilter, err = s.Policy.InjectFilterParts(ctx, prefix, filter, episodic.PolicyContext{
			UserID:   asUserID,
			ClientID: getClientID(ctx),
			JWTClaims: map[string]interface{}{
				"roles": []string{},
			},
		})
		if err != nil {
			return nil, episodicInternalError("filter injection error", err)
		}
	}
	normalizedFilter, err := registryepisodic.NormalizeAttributeFilters(filter, policyFilter)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	archived := protoArchiveFilterToEpisodic(req.GetArchived())
	query := strings.TrimSpace(req.GetQuery())
	hasQueries := len(req.GetQueries()) > 0
	if query != "" && hasQueries {
		return nil, status.Error(codes.InvalidArgument, "query and queries are mutually exclusive")
	}
	var items []registryepisodic.MemoryItem
	if hasQueries {
		queries, queryErr := protoMemorySearchQuerySpecs(req.GetQueries())
		if queryErr != nil {
			return nil, status.Error(codes.InvalidArgument, queryErr.Error())
		}
		perQueryLimit, queryErr := protoPerQueryLimit(req.GetPerQueryLimit(), limit)
		if queryErr != nil {
			return nil, status.Error(codes.InvalidArgument, queryErr.Error())
		}
		if s.Embedder == nil {
			return nil, status.Error(codes.Unavailable, "semantic search unavailable")
		}
		items, err = withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.MemoryItem, error) {
			return multiQuerySemanticSearchMemories(txCtx, s.Store, s.Embedder, effectivePrefix, normalizedFilter, queries, perQueryLimit, limit, archived)
		})
	} else if query != "" {
		if s.Embedder == nil {
			return nil, status.Error(codes.Unavailable, "semantic search unavailable")
		}
		items, err = withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.MemoryItem, error) {
			return semanticSearchMemories(txCtx, s.Store, s.Embedder, effectivePrefix, normalizedFilter, query, limit, archived)
		})
	} else {
		items, err = withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.MemoryItem, error) {
			return s.Store.AdminSearchMemories(txCtx, registryepisodic.AdminMemorySearchQuery{
				NamespacePrefix: effectivePrefix,
				KeyPrefix:       req.GetKeyPrefix(),
				Filter:          normalizedFilter,
				Archived:        archived,
				Limit:           limit,
				IncludeUsage:    req.GetIncludeUsage(),
			})
		})
	}
	if err != nil {
		if errors.Is(err, registryepisodic.ErrSemanticSearchUnavailable) {
			return nil, grpcStatusWithCause(codes.Unavailable, "semantic search unavailable", err)
		}
		return nil, episodicInternalError("failed to search admin memories", err)
	}
	if req.GetKeyPrefix() != "" && (query != "" || hasQueries) {
		items = filterMemoryItemsByKeyPrefix(items, req.GetKeyPrefix())
	}
	if req.GetIncludeUsage() {
		if err := inEpisodicRead(ctx, s.Store, func(txCtx context.Context) error {
			enrichMemoryItemsWithUsage(txCtx, s.Store, items)
			return nil
		}); err != nil {
			return nil, episodicInternalError("failed to enrich memory usage", err)
		}
	}
	resp := &pb.AdminSearchMemoriesResponse{}
	for _, item := range items {
		pItem, err := adminMemoryItemToProto(item)
		if err != nil {
			return nil, episodicInternalError("failed to encode memory response", err)
		}
		resp.Items = append(resp.Items, pItem)
	}
	return resp, nil
}

func (s *AdminMemoriesServer) ListNamespaces(ctx context.Context, req *pb.AdminListMemoryNamespacesRequest) (*pb.AdminListMemoryNamespacesResponse, error) {
	if err := s.requireReadAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesRead); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	prefix := req.GetNamespacePrefix()
	if len(prefix) > 0 {
		if err := validateMemoryNamespace(prefix, s.maxDepth()); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	maxDepth := int(req.GetMaxDepth())
	if maxDepth < 0 || (s.maxDepth() > 0 && maxDepth > s.maxDepth()) {
		return nil, status.Error(codes.InvalidArgument, "max_depth out of range")
	}
	limit, err := grpcPageSize(ctx, req.GetLimit(), 200)
	if err != nil {
		return nil, err
	}
	page, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) (registryepisodic.AdminNamespacePage, error) {
		return s.Store.AdminListNamespaces(txCtx, registryepisodic.AdminNamespaceQuery{
			NamespacePrefix: prefix,
			Suffix:          req.GetSuffix(),
			MaxDepth:        maxDepth,
			Archived:        protoArchiveFilterToEpisodic(req.GetArchived()),
			Limit:           limit,
			AfterCursor:     req.GetAfterCursor(),
		})
	})
	if err != nil {
		return nil, episodicInternalError("failed to list admin memory namespaces", err)
	}
	resp := &pb.AdminListMemoryNamespacesResponse{AfterCursor: stringPtrIfNotEmpty(page.AfterCursor)}
	for _, ns := range page.Namespaces {
		resp.Namespaces = append(resp.Namespaces, &pb.MemoryNamespace{Segments: ns})
	}
	return resp, nil
}

func (s *AdminMemoriesServer) DeleteMemory(ctx context.Context, req *pb.AdminDeleteMemoryRequest) (*emptypb.Empty, error) {
	if err := s.requireAdminAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesWrite); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	id, err := bytesToUUID(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := inEpisodicWrite(ctx, s.Store, func(txCtx context.Context) error {
		return s.Store.AdminForceDeleteMemory(txCtx, id)
	}); err != nil {
		return nil, episodicInternalError("failed to delete memory", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *AdminMemoriesServer) GetMemoryUsage(ctx context.Context, req *pb.AdminGetMemoryUsageRequest) (*pb.MemoryUsage, error) {
	if err := s.requireAdminAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesRead); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	namespace := req.GetNamespace()
	if err := validateMemoryNamespace(namespace, s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.GetKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}
	usage, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) (*registryepisodic.MemoryUsage, error) {
		return s.Store.GetMemoryUsage(txCtx, namespace, req.GetKey())
	})
	if err != nil {
		return nil, episodicInternalError("failed to read memory usage", err)
	}
	if usage == nil {
		return nil, status.Error(codes.NotFound, "memory usage not found")
	}
	return memoryUsageToProto(*usage), nil
}

func (s *AdminMemoriesServer) ListTopMemoryUsage(ctx context.Context, req *pb.AdminListTopMemoryUsageRequest) (*pb.ListTopMemoryUsageResponse, error) {
	if err := s.requireAdminAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesRead); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	prefix := req.GetPrefix()
	if len(prefix) > 0 {
		if err := validateMemoryNamespace(prefix, s.maxDepth()); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	sortBy := registryepisodic.MemoryUsageSortFetchCount
	switch req.GetSort() {
	case pb.MemoryUsageSort_LAST_FETCHED_AT:
		sortBy = registryepisodic.MemoryUsageSortLastFetchedAt
	case pb.MemoryUsageSort_MEMORY_USAGE_SORT_UNSPECIFIED, pb.MemoryUsageSort_FETCH_COUNT:
		sortBy = registryepisodic.MemoryUsageSortFetchCount
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid sort")
	}
	limit, err := grpcPageSize(ctx, req.GetLimit(), 100)
	if err != nil {
		return nil, err
	}
	items, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.TopMemoryUsageItem, error) {
		return s.Store.ListTopMemoryUsage(txCtx, registryepisodic.ListTopMemoryUsageRequest{
			Prefix: prefix,
			Sort:   sortBy,
			Limit:  limit,
		})
	})
	if err != nil {
		return nil, episodicInternalError("failed to list memory usage", err)
	}
	resp := &pb.ListTopMemoryUsageResponse{}
	for _, item := range items {
		resp.Items = append(resp.Items, &pb.TopMemoryUsageItem{
			Namespace: append([]string(nil), item.Namespace...),
			Key:       item.Key,
			Usage:     memoryUsageToProto(item.Usage),
		})
	}
	return resp, nil
}

func (s *AdminMemoriesServer) GetMemoryIndexStatus(ctx context.Context, req *pb.AdminGetMemoryIndexStatusRequest) (*pb.MemoryIndexStatusResponse, error) {
	if err := s.requireAdminAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesRead); err != nil {
		return nil, err
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	count, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) (int64, error) {
		return s.Store.AdminCountPendingIndexing(txCtx)
	})
	if err != nil {
		return nil, episodicInternalError("failed to read memory index status", err)
	}
	return &pb.MemoryIndexStatusResponse{Pending: count}, nil
}

func (s *AdminMemoriesServer) PutMemory(ctx context.Context, req *pb.AdminPutMemoryRequest) (*pb.MemoryWriteResult, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	if err := s.requireAdminAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesWrite); err != nil {
		return nil, err
	}
	if req.GetValue() == nil {
		return nil, status.Error(codes.InvalidArgument, "value is required")
	}
	if req.GetTtlSeconds() < 0 {
		return nil, status.Error(codes.InvalidArgument, "ttl_seconds must be >= 0")
	}

	namespace := req.GetNamespace()
	if err := validateMemoryNamespace(namespace, s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	key := req.GetKey()
	if key == "" || len(key) > 1024 {
		return nil, status.Error(codes.InvalidArgument, "key must be non-empty and at most 1024 bytes")
	}

	value := req.GetValue().AsMap()
	index := req.GetIndex()
	if index == nil {
		index = map[string]string{}
	}

	policyAttrs := map[string]interface{}{}
	if s.Policy != nil {
		// Admin memory writes are authorized by admin role/scope/justification
		// above. Do not run user OPA authz here; only run attribute extraction
		// with a neutral admin context for indexing/search.
		extracted, err := s.Policy.ExtractAttributes(ctx, namespace, key, value, index, adminMemoryPolicyContext(ctx))
		if err != nil {
			return nil, episodicInternalError("attribute extraction error", err)
		}
		policyAttrs = extracted
	}

	result, err := withEpisodicWrite(ctx, s.Store, func(txCtx context.Context) (*registryepisodic.MemoryWriteResult, error) {
		return s.Store.PutMemory(txCtx, registryepisodic.PutMemoryRequest{
			Namespace:        namespace,
			Key:              key,
			Value:            value,
			Index:            index,
			TTLSeconds:       int(req.GetTtlSeconds()),
			PolicyAttributes: policyAttrs,
			ExpectedRevision: req.ExpectedRevision,
		})
	})
	if err != nil {
		if errors.Is(err, registryepisodic.ErrMemoryRevisionConflict) {
			return nil, grpcStatusWithCause(codes.Aborted, "memory revision conflict", err)
		}
		return nil, episodicInternalError("failed to store memory", err)
	}
	resp, err := memoryWriteResultToProto(result)
	if err != nil {
		return nil, episodicInternalError("failed to encode memory write response", err)
	}
	return resp, nil
}

func (s *AdminMemoriesServer) UpdateMemory(ctx context.Context, req *pb.AdminUpdateMemoryRequest) (*emptypb.Empty, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	if err := s.requireAdminAccess(ctx, req.GetJustification()); err != nil {
		return nil, err
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminMemoriesWrite); err != nil {
		return nil, err
	}

	namespace := req.GetNamespace()
	if err := validateMemoryNamespace(namespace, s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	key := req.GetKey()
	if key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}

	if req.Archived == nil || !req.GetArchived() {
		return nil, status.Error(codes.InvalidArgument, "archived must be true")
	}

	// Admin memory updates are authorized by admin role/scope/justification
	// above. They intentionally bypass user OPA authz because archive is an
	// administrative operation across namespaces.
	if err := inEpisodicWrite(ctx, s.Store, func(txCtx context.Context) error {
		return s.Store.ArchiveMemory(txCtx, namespace, key, req.ExpectedRevision)
	}); err != nil {
		if errors.Is(err, registryepisodic.ErrMemoryRevisionConflict) {
			return nil, grpcStatusWithCause(codes.Aborted, "memory revision conflict", err)
		}
		return nil, episodicInternalError("failed to archive memory", err)
	}
	return &emptypb.Empty{}, nil
}

func adminMemoryPolicyContext(ctx context.Context) episodic.PolicyContext {
	return episodic.PolicyContext{
		UserID:   "",
		ClientID: getClientID(ctx),
		JWTClaims: map[string]interface{}{
			"roles": []string{security.RoleAdmin},
		},
	}
}

func (s *AdminMemoriesServer) requireReadAccess(ctx context.Context, justification string) error {
	id, err := requireGRPCIdentity(ctx)
	if err != nil {
		return err
	}
	if !id.Roles[security.RoleAdmin] && !id.Roles[security.RoleAuditor] {
		return status.Error(codes.PermissionDenied, "admin or auditor role required")
	}
	if err := s.requireJustification(justification); err != nil {
		return err
	}
	return nil
}

func (s *AdminMemoriesServer) requireAdminAccess(ctx context.Context, justification string) error {
	id, err := requireGRPCIdentity(ctx)
	if err != nil {
		return err
	}
	if !id.Roles[security.RoleAdmin] {
		return status.Error(codes.PermissionDenied, "admin role required")
	}
	if err := s.requireJustification(justification); err != nil {
		return err
	}
	return nil
}

func (s *AdminMemoriesServer) requireJustification(justification string) error {
	if s.Config != nil && s.Config.RequireJustification && strings.TrimSpace(justification) == "" {
		return status.Error(codes.InvalidArgument, "admin justification required")
	}
	return nil
}

func (s *AdminMemoriesServer) maxDepth() int {
	if s.Config == nil {
		return 0
	}
	return s.Config.EpisodicMaxDepth
}

func (s *MemoriesServer) maxDepth() int {
	if s.Config == nil {
		return 0
	}
	return s.Config.EpisodicMaxDepth
}

func validateMemoryNamespace(ns []string, maxDepth int) error {
	if len(ns) == 0 {
		return fmt.Errorf("namespace must have at least one segment")
	}
	for i, seg := range ns {
		if seg == "" {
			return fmt.Errorf("namespace segment %d is empty", i)
		}
	}
	if maxDepth > 0 && len(ns) > maxDepth {
		return fmt.Errorf("namespace depth %d exceeds configured limit %d", len(ns), maxDepth)
	}
	return nil
}

func (s *MemoriesServer) memoryPolicyContext(ctx context.Context) (episodic.PolicyContext, error) {
	id, err := requireGRPCIdentity(ctx)
	if err != nil {
		return episodic.PolicyContext{}, err
	}
	roles := []string{}
	if id.Roles[security.RoleAdmin] {
		roles = append(roles, security.RoleAdmin)
	}
	if id.Roles[security.RoleAuditor] {
		roles = append(roles, security.RoleAuditor)
	}
	userID := strings.TrimSpace(id.UserID)
	return episodic.PolicyContext{
		UserID:   userID,
		ClientID: strings.TrimSpace(id.ClientID),
		JWTClaims: map[string]interface{}{
			"roles": roles,
		},
	}, nil
}

func episodicInternalError(message string, err error) error {
	log.Error("episodic gRPC error", "message", message, "error", err, "stack", string(debug.Stack()))
	return grpcStatusWithCause(codes.Internal, "internal server error", err)
}

func memoryWriteResultToProto(item *registryepisodic.MemoryWriteResult) (*pb.MemoryWriteResult, error) {
	resp := &pb.MemoryWriteResult{
		Id:        uuidToBytes(item.ID),
		Namespace: append([]string(nil), item.Namespace...),
		Key:       item.Key,
		CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339),
		Revision:  item.Revision,
	}
	if item.Attributes != nil {
		attrs, err := structpb.NewStruct(item.Attributes)
		if err != nil {
			return nil, err
		}
		resp.Attributes = attrs
	}
	if item.ExpiresAt != nil {
		expiresAt := item.ExpiresAt.UTC().Format(time.RFC3339)
		resp.ExpiresAt = &expiresAt
	}
	return resp, nil
}

func memoryItemToProto(item registryepisodic.MemoryItem) (*pb.MemoryItem, error) {
	value, err := structpb.NewStruct(item.Value)
	if err != nil {
		return nil, err
	}
	resp := &pb.MemoryItem{
		Id:        uuidToBytes(item.ID),
		Namespace: append([]string(nil), item.Namespace...),
		Key:       item.Key,
		Value:     value,
		CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339),
		Archived:  item.ArchivedAt != nil,
		Revision:  item.Revision,
	}
	if item.Attributes != nil {
		attrs, err := structpb.NewStruct(item.Attributes)
		if err != nil {
			return nil, err
		}
		resp.Attributes = attrs
	}
	if item.Score != nil {
		resp.Score = item.Score
	}
	if len(item.MatchedQueries) > 0 {
		resp.MatchedQueries = append([]string(nil), item.MatchedQueries...)
	}
	if item.ExpiresAt != nil {
		expiresAt := item.ExpiresAt.UTC().Format(time.RFC3339)
		resp.ExpiresAt = &expiresAt
	}
	if item.Usage != nil {
		resp.Usage = memoryUsageToProto(*item.Usage)
	}
	return resp, nil
}

func adminMemoryItemToProto(item registryepisodic.MemoryItem) (*pb.AdminMemoryItem, error) {
	resp := &pb.AdminMemoryItem{
		Id:        uuidToBytes(item.ID),
		Namespace: append([]string(nil), item.Namespace...),
		Key:       item.Key,
		CreatedAt: timestamppb.New(item.CreatedAt.UTC()),
		Archived:  item.ArchivedAt != nil,
		Revision:  item.Revision,
	}
	if item.Value != nil {
		value, err := structpb.NewStruct(item.Value)
		if err != nil {
			return nil, err
		}
		resp.Value = value
	}
	if item.Attributes != nil {
		attrs, err := structpb.NewStruct(item.Attributes)
		if err != nil {
			return nil, err
		}
		resp.Attributes = attrs
	}
	if item.Score != nil {
		resp.Score = item.Score
	}
	if len(item.MatchedQueries) > 0 {
		resp.MatchedQueries = append([]string(nil), item.MatchedQueries...)
	}
	if item.ExpiresAt != nil {
		resp.ExpiresAt = timestamppb.New(item.ExpiresAt.UTC())
	}
	if item.ArchivedAt != nil {
		resp.ArchivedAt = timestamppb.New(item.ArchivedAt.UTC())
	}
	if item.Usage != nil {
		resp.Usage = memoryUsageToProto(*item.Usage)
	}
	return resp, nil
}

func memoryUsageToProto(usage registryepisodic.MemoryUsage) *pb.MemoryUsage {
	return &pb.MemoryUsage{
		FetchCount:    usage.FetchCount,
		LastFetchedAt: timestamppb.New(usage.LastFetchedAt.UTC()),
	}
}

func memoryItemsToSearchResponse(items []registryepisodic.MemoryItem) (*pb.SearchMemoriesResponse, error) {
	resp := &pb.SearchMemoriesResponse{}
	for _, item := range items {
		pItem, err := memoryItemToProto(item)
		if err != nil {
			return nil, err
		}
		resp.Items = append(resp.Items, pItem)
	}
	return resp, nil
}

func memoryEventToProto(event registryepisodic.MemoryEvent) (*pb.MemoryEventItem, error) {
	item := &pb.MemoryEventItem{
		Id:         uuidToBytes(event.ID),
		Namespace:  append([]string(nil), event.Namespace...),
		Key:        event.Key,
		Kind:       event.Kind,
		OccurredAt: timestamppb.New(event.OccurredAt.UTC()),
	}
	if event.Value != nil {
		value, err := structpb.NewStruct(event.Value)
		if err != nil {
			return nil, err
		}
		item.Value = value
	}
	if event.Attributes != nil {
		attrs, err := structpb.NewStruct(event.Attributes)
		if err != nil {
			return nil, err
		}
		item.Attributes = attrs
	}
	if event.ExpiresAt != nil {
		item.ExpiresAt = timestamppb.New(event.ExpiresAt.UTC())
	}
	return item, nil
}

func enrichMemoryItemsWithUsage(ctx context.Context, store registryepisodic.EpisodicStore, items []registryepisodic.MemoryItem) {
	for i := range items {
		usage, err := store.GetMemoryUsage(ctx, items[i].Namespace, items[i].Key)
		if err != nil {
			log.Warn("failed to load memory usage counters", "namespace", items[i].Namespace, "key", items[i].Key, "err", err)
			continue
		}
		items[i].Usage = usage
	}
}

func filterMemoryItemsByKeyPrefix(items []registryepisodic.MemoryItem, keyPrefix string) []registryepisodic.MemoryItem {
	if keyPrefix == "" {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if strings.HasPrefix(item.Key, keyPrefix) {
			out = append(out, item)
		}
	}
	return out
}

func semanticSearchMemories(ctx context.Context, store registryepisodic.EpisodicStore, embedder registryembed.Embedder, namespacePrefix []string, filter registryepisodic.AttributeFilter, query string, limit int, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryItem, error) {
	embeddings, err := embedder.EmbedTexts(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, nil
	}

	nsEncoded := ""
	if len(namespacePrefix) > 0 {
		var err error
		nsEncoded, err = episodic.EncodeNamespace(namespacePrefix, 0)
		if err != nil {
			return nil, err
		}
	}
	vectorResults, err := store.SearchMemoryVectors(ctx, nsEncoded, embeddings[0], filter, limit, archived)
	if err != nil {
		return nil, fmt.Errorf("search memory vectors: %w", err)
	}
	if len(vectorResults) == 0 {
		return nil, nil
	}

	scoreByID := make(map[uuid.UUID]float64, len(vectorResults))
	orderedIDs := make([]uuid.UUID, 0, len(vectorResults))
	for _, vr := range vectorResults {
		if prev, exists := scoreByID[vr.MemoryID]; !exists {
			scoreByID[vr.MemoryID] = vr.Score
			orderedIDs = append(orderedIDs, vr.MemoryID)
		} else if vr.Score > prev {
			scoreByID[vr.MemoryID] = vr.Score
		}
	}
	if len(orderedIDs) == 0 {
		return nil, nil
	}

	items, err := store.GetMemoriesByIDs(ctx, orderedIDs, archived)
	if err != nil {
		return nil, fmt.Errorf("get memories by ids: %w", err)
	}
	itemByID := make(map[uuid.UUID]registryepisodic.MemoryItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}

	results := make([]registryepisodic.MemoryItem, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		item, ok := itemByID[id]
		if !ok {
			continue
		}
		score := scoreByID[id]
		item.Score = &score
		results = append(results, item)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return *results[i].Score > *results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

type memorySearchQuerySpec struct {
	Text    string
	Purpose string
}

func protoMemorySearchQuerySpecs(queries []*pb.MemorySearchQuery) ([]memorySearchQuerySpec, error) {
	out := make([]memorySearchQuerySpec, 0, len(queries))
	for i, q := range queries {
		if q == nil {
			return nil, fmt.Errorf("queries[%d].text must not be empty", i)
		}
		text := strings.TrimSpace(q.GetText())
		if text == "" {
			return nil, fmt.Errorf("queries[%d].text must not be empty", i)
		}
		purpose := text
		if p := strings.TrimSpace(q.GetPurpose()); p != "" {
			purpose = p
		}
		out = append(out, memorySearchQuerySpec{Text: text, Purpose: purpose})
	}
	return out, nil
}

func protoPerQueryLimit(raw int32, limit int) (int, error) {
	if raw == 0 {
		return min(limit, 100), nil
	}
	if raw < 1 || raw > 100 {
		return 0, fmt.Errorf("per_query_limit must be between 1 and 100")
	}
	return int(raw), nil
}

func multiQuerySemanticSearchMemories(
	ctx context.Context,
	store registryepisodic.EpisodicStore,
	embedder registryembed.Embedder,
	namespacePrefix []string,
	filter registryepisodic.AttributeFilter,
	queries []memorySearchQuerySpec,
	perQueryLimit int,
	limit int,
	archived registryepisodic.ArchiveFilter,
) ([]registryepisodic.MemoryItem, error) {
	texts := make([]string, 0, len(queries))
	for _, q := range queries {
		texts = append(texts, q.Text)
	}

	embeddings, err := embedder.EmbedTexts(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed queries: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, nil
	}
	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("embed queries: expected %d embeddings, got %d", len(texts), len(embeddings))
	}

	nsEncoded := ""
	if len(namespacePrefix) > 0 {
		nsEncoded, err = episodic.EncodeNamespace(namespacePrefix, 0)
		if err != nil {
			return nil, err
		}
	}

	const rrfK = 60.0
	type rrfEntry struct {
		rrfScore   float64
		bestRaw    float64
		purposes   []string
		purposeSet map[string]struct{}
		firstSeen  int
	}

	accum := make(map[uuid.UUID]*rrfEntry)
	seenOrder := make([]uuid.UUID, 0)
	for qi, q := range queries {
		vectorResults, err := store.SearchMemoryVectors(ctx, nsEncoded, embeddings[qi], filter, perQueryLimit, archived)
		if err != nil {
			return nil, fmt.Errorf("search memory vectors (query %d): %w", qi, err)
		}
		seenForQuery := make(map[uuid.UUID]bool, len(vectorResults))
		rank := 1
		for _, vr := range vectorResults {
			if seenForQuery[vr.MemoryID] {
				continue
			}
			seenForQuery[vr.MemoryID] = true
			entry, ok := accum[vr.MemoryID]
			if !ok {
				entry = &rrfEntry{
					purposeSet: make(map[string]struct{}),
					firstSeen:  len(seenOrder),
				}
				accum[vr.MemoryID] = entry
				seenOrder = append(seenOrder, vr.MemoryID)
			}
			entry.rrfScore += 1.0 / (rrfK + float64(rank))
			if vr.Score > entry.bestRaw {
				entry.bestRaw = vr.Score
			}
			if _, already := entry.purposeSet[q.Purpose]; !already {
				entry.purposeSet[q.Purpose] = struct{}{}
				entry.purposes = append(entry.purposes, q.Purpose)
			}
			rank++
		}
	}

	if len(seenOrder) == 0 {
		return nil, nil
	}
	sort.SliceStable(seenOrder, func(i, j int) bool {
		ei, ej := accum[seenOrder[i]], accum[seenOrder[j]]
		if ei.rrfScore != ej.rrfScore {
			return ei.rrfScore > ej.rrfScore
		}
		if ei.bestRaw != ej.bestRaw {
			return ei.bestRaw > ej.bestRaw
		}
		return ei.firstSeen < ej.firstSeen
	})
	topIDs := seenOrder
	if len(topIDs) > limit {
		topIDs = topIDs[:limit]
	}

	items, err := store.GetMemoriesByIDs(ctx, topIDs, archived)
	if err != nil {
		return nil, fmt.Errorf("get memories by ids: %w", err)
	}
	itemByID := make(map[uuid.UUID]registryepisodic.MemoryItem, len(items))
	for _, item := range items {
		itemByID[item.ID] = item
	}

	results := make([]registryepisodic.MemoryItem, 0, len(topIDs))
	for _, id := range topIDs {
		item, ok := itemByID[id]
		if !ok {
			continue
		}
		entry := accum[id]
		score := entry.rrfScore
		item.Score = &score
		item.MatchedQueries = append([]string(nil), entry.purposes...)
		results = append(results, item)
	}
	return results, nil
}

// --- Attachments Service ---

type AttachmentsServer struct {
	pb.UnimplementedAttachmentsServiceServer
	Store       registrystore.MemoryStore
	AttachStore registryattach.AttachmentStore
	MaxBodySize int64
	Config      *config.Config
	SigningKeys [][]byte
}

func (s *AttachmentsServer) UploadAttachment(stream pb.AttachmentsService_UploadAttachmentServer) error {
	if s.AttachStore == nil {
		return status.Error(codes.FailedPrecondition, "attachment store is not configured")
	}
	userID := getUserID(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(stream.Context(), security.PermissionAttachmentsWrite); err != nil {
		return err
	}

	var metadata *pb.UploadMetadata
	var resultCh chan struct {
		res *registryattach.FileStoreResult
		err error
	}
	var writer *io.PipeWriter
	defer func() {
		if writer != nil {
			// Ensure the background store goroutine unblocks if the handler returns early.
			_ = writer.CloseWithError(io.ErrUnexpectedEOF)
		}
	}()

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			if metadata == nil {
				return status.Error(codes.InvalidArgument, "metadata is required in first message")
			}
			_ = writer.Close()
			out := <-resultCh
			if out.err != nil {
				if errors.Is(out.err, operationevent.ErrRecoveredPanic) {
					return grpcStatusWithCause(codes.Internal, "internal server error", out.err)
				}
				if errors.Is(out.err, context.Canceled) {
					return grpcStatusWithCause(codes.Canceled, "request canceled", out.err)
				}
				return grpcStatusWithCause(codes.InvalidArgument, out.err.Error(), out.err)
			}

			contentType := metadata.GetContentType()
			if strings.TrimSpace(contentType) == "" {
				contentType = "application/octet-stream"
			}
			var filename *string
			if strings.TrimSpace(metadata.GetFilename()) != "" {
				v := metadata.GetFilename()
				filename = &v
			}

			expiresIn, err := parseGRPCExpiresIn(metadata.GetExpiresIn(), s.Config)
			if err != nil {
				return grpcStatusWithCause(codes.InvalidArgument, err.Error(), err)
			}
			expiresAt := time.Now().Add(expiresIn)

			attachment, err := withMemoryWrite(stream.Context(), s.Store, func(txCtx context.Context) (*model.Attachment, error) {
				return s.Store.CreateAttachment(txCtx, userID, "", model.Attachment{
					Filename:    filename,
					ContentType: contentType,
					Size:        &out.res.Size,
					SHA256:      &out.res.SHA256,
					StorageKey:  &out.res.StorageKey,
					ExpiresAt:   &expiresAt,
					Status:      "ready",
				})
			})
			if err != nil {
				return mapError(err)
			}

			return stream.SendAndClose(uploadAttachmentResponseToProto(attachment))
		}
		if err != nil {
			return err
		}

		switch payload := req.GetPayload().(type) {
		case *pb.UploadAttachmentRequest_Metadata:
			if metadata != nil {
				return status.Error(codes.InvalidArgument, "metadata can only be sent once")
			}
			metadata = payload.Metadata

			contentType := metadata.GetContentType()
			if strings.TrimSpace(contentType) == "" {
				contentType = "application/octet-stream"
			}
			reader, pipeWriter := io.Pipe()
			writer = pipeWriter
			resultCh = make(chan struct {
				res *registryattach.FileStoreResult
				err error
			}, 1)
			go func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						stack := debug.Stack()
						err := operationevent.RecoveredPanicError(operationevent.FromContext(stream.Context()), "", recovered, stack)
						_ = reader.CloseWithError(err)
						resultCh <- struct {
							res *registryattach.FileStoreResult
							err error
						}{err: err}
					}
				}()
				res, err := s.AttachStore.Store(stream.Context(), reader, s.MaxBodySize, contentType)
				_ = reader.Close()
				resultCh <- struct {
					res *registryattach.FileStoreResult
					err error
				}{res: res, err: err}
			}()
		case *pb.UploadAttachmentRequest_Chunk:
			if metadata == nil || writer == nil {
				return status.Error(codes.InvalidArgument, "metadata must be sent before chunks")
			}
			if len(payload.Chunk) == 0 {
				continue
			}
			if _, err := writer.Write(payload.Chunk); err != nil {
				if errors.Is(err, context.Canceled) {
					return grpcStatusWithCause(codes.Canceled, "request canceled", err)
				}
				return grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
		default:
			return status.Error(codes.InvalidArgument, "invalid upload payload")
		}
	}
}

func (s *AttachmentsServer) CreateAttachmentFromUrl(ctx context.Context, req *pb.CreateAttachmentFromUrlRequest) (*pb.UploadAttachmentResponse, error) {
	if s.AttachStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "attachment store is not configured")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAttachmentsWrite); err != nil {
		return nil, err
	}
	sourceURL := strings.TrimSpace(req.GetSourceUrl())
	if sourceURL == "" {
		return nil, status.Error(codes.InvalidArgument, "source_url is required")
	}
	if _, err := url.ParseRequestURI(sourceURL); err != nil {
		return nil, grpcStatusWithCause(codes.InvalidArgument, "invalid source_url", err)
	}
	contentType := strings.TrimSpace(req.GetContentType())
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var filename *string
	if name := strings.TrimSpace(req.GetFilename()); name != "" {
		filename = &name
	}
	defaultExpires := time.Hour
	if s.Config != nil && s.Config.AttachmentDefaultExpiresIn > 0 {
		defaultExpires = s.Config.AttachmentDefaultExpiresIn
	}
	expiresAt := time.Now().Add(defaultExpires)

	attachment, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*model.Attachment, error) {
		return s.Store.CreateAttachment(txCtx, userID, "", model.Attachment{
			Filename:    filename,
			ContentType: contentType,
			SourceURL:   &sourceURL,
			ExpiresAt:   &expiresAt,
			Status:      "downloading",
		})
	})
	if err != nil {
		return nil, mapError(err)
	}
	routeattachments.StartSourceURLAttachmentDownload(ctx, s.Store, s.AttachStore, s.Config, attachment.ID, userID, sourceURL, contentType)
	return uploadAttachmentResponseToProto(attachment), nil
}

func (s *AttachmentsServer) GetAttachment(ctx context.Context, req *pb.GetAttachmentRequest) (*pb.AttachmentInfo, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAttachmentsRead); err != nil {
		return nil, err
	}
	attachmentID, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, grpcStatusWithCause(codes.InvalidArgument, "invalid attachment id", err)
	}

	attachment, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*model.Attachment, error) {
		return s.Store.GetAttachment(txCtx, userID, "", attachmentID)
	})
	if err != nil {
		return nil, mapError(err)
	}
	return attachmentToProto(attachment), nil
}

func (s *AttachmentsServer) DownloadAttachment(req *pb.DownloadAttachmentRequest, stream pb.AttachmentsService_DownloadAttachmentServer) error {
	if s.AttachStore == nil {
		return status.Error(codes.FailedPrecondition, "attachment store is not configured")
	}
	userID := getUserID(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(stream.Context(), security.PermissionAttachmentsRead); err != nil {
		return err
	}

	attachmentID, err := uuid.Parse(req.GetId())
	if err != nil {
		return grpcStatusWithCause(codes.InvalidArgument, "invalid attachment id", err)
	}
	attachment, err := withMemoryRead(stream.Context(), s.Store, func(txCtx context.Context) (*model.Attachment, error) {
		return s.Store.GetAttachment(txCtx, userID, "", attachmentID)
	})
	if err != nil {
		return mapError(err)
	}
	if strings.EqualFold(attachment.Status, "downloading") {
		return status.Error(codes.FailedPrecondition, "attachment download in progress")
	}
	if strings.EqualFold(attachment.Status, "failed") {
		return status.Error(codes.FailedPrecondition, "attachment download failed")
	}
	if strings.EqualFold(attachment.Status, "uploading") {
		return status.Error(codes.FailedPrecondition, "attachment upload in progress")
	}
	if attachment.StorageKey == nil {
		return status.Error(codes.NotFound, "attachment content not available")
	}

	if err := stream.Send(&pb.DownloadAttachmentResponse{
		Payload: &pb.DownloadAttachmentResponse_Metadata{Metadata: attachmentToProto(attachment)},
	}); err != nil {
		return err
	}

	reader, err := s.AttachStore.Retrieve(stream.Context(), *attachment.StorageKey)
	if err != nil {
		return grpcStatusWithCause(codes.Internal, "internal server error", err)
	}
	defer reader.Close()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := stream.Send(&pb.DownloadAttachmentResponse{
				Payload: &pb.DownloadAttachmentResponse_Chunk{Chunk: chunk},
			}); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return grpcStatusWithCause(codes.Internal, "internal server error", readErr)
		}
	}
	return nil
}

func (s *AttachmentsServer) DeleteAttachment(ctx context.Context, req *pb.DeleteAttachmentRequest) (*emptypb.Empty, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAttachmentsWrite); err != nil {
		return nil, err
	}
	attachmentID, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, grpcStatusWithCause(codes.InvalidArgument, "invalid attachment id", err)
	}
	if err := inMemoryWrite(ctx, s.Store, func(txCtx context.Context) error {
		attachment, err := s.Store.GetAttachment(txCtx, userID, "", attachmentID)
		if err != nil {
			return err
		}
		if attachment.StorageKey != nil && s.AttachStore != nil {
			if err := s.AttachStore.Delete(txCtx, *attachment.StorageKey); err != nil {
				return err
			}
		}
		return s.Store.DeleteAttachment(txCtx, userID, "", attachmentID)
	}); err != nil {
		return nil, mapError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *AttachmentsServer) GetAttachmentDownloadUrl(ctx context.Context, req *pb.GetAttachmentDownloadUrlRequest) (*pb.AttachmentDownloadUrlResponse, error) {
	if s.AttachStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "attachment store is not configured")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAttachmentsRead); err != nil {
		return nil, err
	}
	attachmentID, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, grpcStatusWithCause(codes.InvalidArgument, "invalid attachment id", err)
	}
	disposition, err := parseGRPCDisposition(req.GetDisposition())
	if err != nil {
		return nil, grpcStatusWithCause(codes.InvalidArgument, err.Error(), err)
	}
	attachment, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*model.Attachment, error) {
		return s.Store.GetAttachment(txCtx, userID, "", attachmentID)
	})
	if err != nil {
		return nil, mapError(err)
	}
	if strings.EqualFold(attachment.Status, "downloading") && attachment.SourceURL != nil && strings.TrimSpace(*attachment.SourceURL) != "" {
		return &pb.AttachmentDownloadUrlResponse{Url: *attachment.SourceURL, Status: "downloading"}, nil
	}
	if strings.EqualFold(attachment.Status, "failed") {
		return nil, status.Error(codes.FailedPrecondition, "attachment download failed")
	}
	if strings.EqualFold(attachment.Status, "uploading") {
		return nil, status.Error(codes.FailedPrecondition, "attachment upload in progress")
	}
	if attachment.StorageKey == nil {
		return nil, status.Error(codes.NotFound, "attachment content not available")
	}
	expires := 5 * time.Minute
	if s.Config != nil && s.Config.AttachmentDownloadURLExpiresIn > 0 {
		expires = s.Config.AttachmentDownloadURLExpiresIn
	}
	policy := routeattachments.AttachmentResponsePolicy(attachment.ContentType, disposition)
	if s.Config != nil && s.Config.S3DirectDownload && policy.AllowDirect {
		if signedURL, err := s.AttachStore.GetSignedURL(ctx, *attachment.StorageKey, expires, routeattachments.SignedURLOptions(policy.Disposition, attachment.Filename)); err == nil {
			return &pb.AttachmentDownloadUrlResponse{Url: signedURL.String(), ExpiresIn: int32(expires.Seconds())}, nil
		}
	}
	if len(s.SigningKeys) == 0 || len(s.SigningKeys[0]) == 0 {
		return nil, status.Error(codes.Unavailable, "download URLs are not available: encryption key is not configured")
	}
	filename := "download"
	if attachment.Filename != nil && strings.TrimSpace(*attachment.Filename) != "" {
		filename = *attachment.Filename
	}
	token := routeattachments.SignDownloadToken(*attachment.StorageKey, s.SigningKeys[0], time.Now().Add(expires))
	downloadURL := fmt.Sprintf("/v1/attachments/download/%s/%s", url.PathEscape(token), url.PathEscape(filename))
	if policy.Disposition != "" {
		downloadURL += "?disposition=" + url.QueryEscape(policy.Disposition)
	}
	return &pb.AttachmentDownloadUrlResponse{Url: downloadURL, ExpiresIn: int32(expires.Seconds())}, nil
}

func uploadAttachmentResponseToProto(attachment *model.Attachment) *pb.UploadAttachmentResponse {
	resp := &pb.UploadAttachmentResponse{
		Id:          attachment.ID.String(),
		Href:        "/v1/attachments/" + attachment.ID.String(),
		ContentType: attachment.ContentType,
		Status:      attachment.Status,
	}
	if attachment.Filename != nil {
		resp.Filename = *attachment.Filename
	}
	if attachment.Size != nil {
		resp.Size = *attachment.Size
	}
	if attachment.SHA256 != nil {
		resp.Sha256 = *attachment.SHA256
	}
	if attachment.ExpiresAt != nil {
		resp.ExpiresAt = attachment.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if attachment.SourceURL != nil {
		resp.SourceUrl = *attachment.SourceURL
	}
	return resp
}

func attachmentToProto(attachment *model.Attachment) *pb.AttachmentInfo {
	info := &pb.AttachmentInfo{
		Id:          attachment.ID.String(),
		Href:        "/v1/attachments/" + attachment.ID.String(),
		ContentType: attachment.ContentType,
		CreatedAt:   attachment.CreatedAt.UTC().Format(time.RFC3339),
		Status:      attachment.Status,
	}
	if attachment.Filename != nil {
		info.Filename = *attachment.Filename
	}
	if attachment.Size != nil {
		info.Size = *attachment.Size
	}
	if attachment.SHA256 != nil {
		info.Sha256 = *attachment.SHA256
	}
	if attachment.ExpiresAt != nil {
		info.ExpiresAt = attachment.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if attachment.SourceURL != nil {
		info.SourceUrl = *attachment.SourceURL
	}
	if attachment.EntryID != nil {
		info.EntryId = uuidToBytes(*attachment.EntryID)
	}
	return info
}

func parseGRPCDisposition(raw string) (string, error) {
	disposition := strings.ToLower(strings.TrimSpace(raw))
	if disposition == "" {
		return "", nil
	}
	if disposition != "inline" && disposition != "attachment" {
		return "", fmt.Errorf("disposition must be 'inline' or 'attachment'")
	}
	return disposition, nil
}

func parseGRPCExpiresIn(raw string, cfg *config.Config) (time.Duration, error) {
	value := strings.TrimSpace(strings.ToUpper(raw))
	defaultDuration := time.Hour
	maxDuration := 24 * time.Hour
	if cfg != nil {
		if cfg.AttachmentDefaultExpiresIn > 0 {
			defaultDuration = cfg.AttachmentDefaultExpiresIn
		}
		if cfg.AttachmentMaxExpiresIn > 0 {
			maxDuration = cfg.AttachmentMaxExpiresIn
		}
	}
	if value == "" {
		return defaultDuration, nil
	}
	if strings.HasPrefix(value, "PT") && strings.HasSuffix(value, "H") {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(value, "PT"), "H"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid expires_in value")
		}
		d := time.Duration(n) * time.Hour
		if d > maxDuration {
			return 0, fmt.Errorf("expires_in cannot exceed PT24H")
		}
		return d, nil
	}
	if strings.HasPrefix(value, "PT") && strings.HasSuffix(value, "M") {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(value, "PT"), "M"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid expires_in value")
		}
		d := time.Duration(n) * time.Minute
		if d > maxDuration {
			return 0, fmt.Errorf("expires_in cannot exceed PT24H")
		}
		return d, nil
	}
	return 0, fmt.Errorf("invalid expires_in value")
}

// --- Response Recorder Service ---

type ResponseRecorderServer struct {
	pb.UnimplementedResponseRecorderServiceServer
	Resumer  *internalresumer.Store
	Store    registrystore.MemoryStore
	Config   *config.Config
	Enabled  bool
	EventBus registryeventbus.EventBus
}

func (s *ResponseRecorderServer) publishResponseEvent(ctx context.Context, eventName string, statusValue string, convUUID string, recordingID string) {
	userID := getUserID(ctx)
	if s.EventBus == nil || userID == "" {
		return
	}

	var eventsToPublish []registryeventbus.Event
	err := inMemoryWrite(ctx, s.Store, func(txCtx context.Context) error {
		conv, err := s.Store.GetConversation(txCtx, userID, convUUID)
		if err != nil {
			return err
		}
		events := []registryeventbus.Event{{
			Event: eventName,
			Kind:  "response",
			Data: map[string]any{
				"conversation":       convUUID,
				"conversation_group": conv.ConversationGroupID,
				"recording":          recordingID,
				"status":             statusValue,
			},
			ConversationGroupID: conv.ConversationGroupID,
		}}
		appended, used, err := eventstream.AppendOutboxEvents(txCtx, s.Store, events...)
		if err != nil {
			return err
		}
		if used {
			eventsToPublish = appended
		} else {
			eventsToPublish = events
		}
		return nil
	})
	if err != nil {
		log.Warn("Failed to build response event", "err", err)
		return
	}
	if err := eventstream.PublishEvents(ctx, s.Store, s.EventBus, eventsToPublish...); err != nil {
		log.Warn("Failed to publish response event", "err", err)
	}
}

func (s *ResponseRecorderServer) Record(stream pb.ResponseRecorderService_RecordServer) (retErr error) {
	if !s.Enabled {
		return stream.SendAndClose(&pb.RecordResponse{
			Status:       pb.RecordStatus_RECORD_STATUS_ERROR,
			ErrorMessage: "response recorder disabled",
		})
	}
	var convID string
	var convUUID string
	var recorder *internalresumer.Recorder
	var cancelStream <-chan struct{}
	defer func() {
		if retErr == nil || recorder == nil || convID == "" {
			return
		}
		if err := recorder.Complete(); err != nil {
			log.Warn("failed to clean up response recorder after stream error", "conversation_id", convID, "error", err)
		}
		s.publishResponseEvent(stream.Context(), "deleted", "failed", convUUID, convID)
	}()

	type recvResult struct {
		req *pb.RecordRequest
		err error
	}
	done := make(chan struct{})
	defer close(done)
	recvCh := make(chan recvResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				stack := debug.Stack()
				err := operationevent.RecoveredPanicError(operationevent.FromContext(stream.Context()), "", recovered, stack)
				select {
				case recvCh <- recvResult{err: err}:
				case <-done:
				}
			}
		}()
		for {
			req, err := stream.Recv()
			select {
			case recvCh <- recvResult{req: req, err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-cancelStream:
			if recorder != nil {
				if err := recorder.Complete(); err != nil {
					log.Warn("record stream: complete failed after cancel", "conversation_id", convID, "error", err)
					return grpcStatusWithCause(codes.Internal, "internal server error", err)
				}
			}
			s.publishResponseEvent(stream.Context(), "deleted", "failed", convUUID, convID)
			return stream.SendAndClose(&pb.RecordResponse{
				Status: pb.RecordStatus_RECORD_STATUS_CANCELLED,
			})
		case result := <-recvCh:
			req, err := result.req, result.err
			if err == io.EOF {
				if recorder != nil {
					if err := recorder.Complete(); err != nil {
						log.Warn("record stream: complete failed on EOF", "conversation_id", convID, "error", err)
						return grpcStatusWithCause(codes.Internal, "internal server error", err)
					}
				}
				s.publishResponseEvent(stream.Context(), "deleted", "completed", convUUID, convID)
				return stream.SendAndClose(&pb.RecordResponse{
					Status: pb.RecordStatus_RECORD_STATUS_SUCCESS,
				})
			}
			if err != nil {
				if errors.Is(err, operationevent.ErrRecoveredPanic) {
					return grpcStatusWithCause(codes.Internal, "internal server error", err)
				}
				if errors.Is(err, context.Canceled) {
					return grpcStatusWithCause(codes.Canceled, "request canceled", err)
				}
				return err
			}

			if convID == "" && len(req.GetConversationId()) > 0 {
				id, err := requiredConversationID(req.GetConversationId())
				if err != nil {
					return grpcStatusWithCause(codes.InvalidArgument, "invalid conversation_id", err)
				}
				if err := s.requireConversationAccess(stream.Context(), id, model.AccessLevelWriter); err != nil {
					return err
				}
				convUUID = id
				convID = string(id)
				advertised := s.resolveAdvertisedAddress(stream.Context())
				recorder, err = s.Resumer.RecorderWithAddress(stream.Context(), convID, advertised)
				if err != nil {
					log.Warn("record stream: failed to create recorder", "conversation_id", convID, "error", err)
					return grpcStatusWithCause(codes.Internal, "internal server error", err)
				}
				cancelStream, err = s.Resumer.CancelStream(stream.Context(), convID)
				if err != nil {
					log.Warn("record stream: failed to subscribe to cancel channel", "conversation_id", convID, "error", err)
					return grpcStatusWithCause(codes.Internal, "internal server error", err)
				}
				s.publishResponseEvent(stream.Context(), "created", "started", convUUID, convID)
			}
			if convID == "" {
				return status.Error(codes.InvalidArgument, "conversation_id is required in first record chunk")
			}

			if recorder != nil && req.GetContent() != "" {
				if err := recorder.Record(req.GetContent()); err != nil {
					log.Warn("record stream: failed to write chunk", "conversation_id", convID, "error", err)
					return grpcStatusWithCause(codes.Internal, "internal server error", err)
				}
			}

			if req.GetComplete() && recorder != nil {
				if err := s.requireConversationAccess(stream.Context(), convUUID, model.AccessLevelWriter); err != nil {
					return err
				}
				if err := recorder.Complete(); err != nil {
					log.Warn("record stream: complete failed", "conversation_id", convID, "error", err)
					return grpcStatusWithCause(codes.Internal, "internal server error", err)
				}
				s.publishResponseEvent(stream.Context(), "deleted", "completed", convUUID, convID)
				return stream.SendAndClose(&pb.RecordResponse{
					Status: pb.RecordStatus_RECORD_STATUS_SUCCESS,
				})
			}
		}
	}
}

func (s *ResponseRecorderServer) Replay(req *pb.ReplayRequest, stream pb.ResponseRecorderService_ReplayServer) error {
	if !s.Enabled {
		return nil
	}
	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return grpcStatusWithCause(codes.InvalidArgument, "invalid conversation_id", err)
	}
	if err := s.requireConversationAccess(stream.Context(), convID, model.AccessLevelReader); err != nil {
		return err
	}

	advertised := s.resolveAdvertisedAddress(stream.Context())
	ch, redirectAddress, err := s.Resumer.ReplayWithAddress(stream.Context(), string(convID), advertised)
	if err != nil {
		return grpcStatusWithCause(codes.Internal, "internal server error", err)
	}
	if redirectAddress != "" {
		return stream.Send(&pb.ReplayResponse{RedirectAddress: redirectAddress})
	}
	for result := range ch {
		if result.Err != nil {
			if errors.Is(result.Err, operationevent.ErrRecoveredPanic) {
				return grpcStatusWithCause(codes.Internal, "internal server error", result.Err)
			}
			if errors.Is(result.Err, context.Canceled) {
				return grpcStatusWithCause(codes.Canceled, "request canceled", result.Err)
			}
			return grpcStatusWithCause(codes.Internal, "internal server error", result.Err)
		}
		if err := stream.Send(&pb.ReplayResponse{Content: result.Content}); err != nil {
			return err
		}
	}
	return nil
}

func (s *ResponseRecorderServer) Cancel(ctx context.Context, req *pb.CancelRecordRequest) (*pb.CancelRecordResponse, error) {
	if !s.Enabled {
		return nil, status.Error(codes.FailedPrecondition, "response recorder disabled")
	}
	convID, err := requiredConversationID(req.GetConversationId())
	if err != nil {
		return nil, grpcStatusWithCause(codes.InvalidArgument, "invalid conversation_id", err)
	}
	if err := s.requireConversationAccess(ctx, convID, model.AccessLevelWriter); err != nil {
		return nil, err
	}

	if _, err := s.Resumer.RequestCancelWithAddress(ctx, string(convID), ""); err != nil {
		return nil, grpcStatusWithCause(codes.Internal, "internal server error", err)
	}
	return &pb.CancelRecordResponse{Accepted: true}, nil
}

func (s *ResponseRecorderServer) IsEnabled(_ context.Context, _ *emptypb.Empty) (*pb.IsEnabledResponse, error) {
	return &pb.IsEnabledResponse{Enabled: s.Enabled}, nil
}

func (s *ResponseRecorderServer) CheckRecordings(ctx context.Context, req *pb.CheckRecordingsRequest) (*pb.CheckRecordingsResponse, error) {
	if !s.Enabled {
		return &pb.CheckRecordingsResponse{}, nil
	}
	var ids []string
	for _, raw := range req.GetConversationIds() {
		id, err := requiredConversationID(raw)
		if err != nil {
			continue
		}
		if err := s.requireConversationAccess(ctx, id, model.AccessLevelReader); err != nil {
			continue
		}
		ids = append(ids, string(id))
	}

	active, err := s.Resumer.Check(ctx, ids)
	if err != nil {
		return nil, grpcStatusWithCause(codes.Internal, "internal server error", err)
	}

	resp := &pb.CheckRecordingsResponse{}
	resp.ConversationIds = append(resp.ConversationIds, active...)
	return resp, nil
}

func (s *ResponseRecorderServer) requireConversationAccess(ctx context.Context, conversationID string, minLevel model.AccessLevel) error {
	if s.Store == nil {
		return status.Error(codes.Internal, "internal server error")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return status.Error(codes.Unauthenticated, "missing authorization")
	}
	permission := security.PermissionRecordingsRead
	if minLevel.IsAtLeast(model.AccessLevelWriter) {
		permission = security.PermissionRecordingsWrite
	}
	if err := requireGRPCOIDCScope(ctx, permission); err != nil {
		return err
	}
	conv, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
		return s.Store.GetConversation(txCtx, userID, conversationID)
	})
	if err != nil {
		return mapError(err)
	}
	if !conv.AccessLevel.IsAtLeast(minLevel) {
		return status.Error(codes.PermissionDenied, "forbidden")
	}
	return nil
}

func (s *ResponseRecorderServer) resolveAdvertisedAddress(ctx context.Context) string {
	if s.Config != nil {
		if explicit := strings.TrimSpace(s.Config.ResumerAdvertisedAddress); explicit != "" {
			return explicit
		}
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range []string{"x-resumer-advertised-address", "x-advertised-address"} {
			if values := md.Get(key); len(values) > 0 {
				if v := strings.TrimSpace(values[0]); v != "" {
					return v
				}
			}
		}
	}

	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "localhost"
	}
	port := 8080
	if s.Config != nil && s.Config.Listener.Port > 0 {
		port = s.Config.Listener.Port
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// --- Event Stream Service ---

type EventStreamServer struct {
	pb.UnimplementedEventStreamServiceServer
	Store          registrystore.MemoryStore
	EventBus       registryeventbus.EventBus
	Config         *config.Config
	UserIDAsserter *security.UserIDAsserter
	RateLimiter    *security.RateLimiter
}

var grpcEventStreamNodeID = uuid.New().String()

type grpcEventStreamSession struct {
	ConnectionID string
	UserID       string
	NodeID       string
	CreatedAt    time.Time
	cancel       context.CancelFunc
	evicted      chan struct{}
}

type grpcEventStreamSessionTracker struct {
	mu       sync.Mutex
	sessions map[string]*grpcEventStreamSession
}

var grpcEventStreamSessions = &grpcEventStreamSessionTracker{
	sessions: make(map[string]*grpcEventStreamSession),
}

func eventSessionEvicted(session *grpcEventStreamSession) <-chan struct{} {
	if session == nil {
		return nil
	}
	return session.evicted
}

func (t *grpcEventStreamSessionTracker) trackSession(s *grpcEventStreamSession) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessions[s.ConnectionID] = s
}

func (t *grpcEventStreamSessionTracker) removeSession(connectionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sessions, connectionID)
}

func (t *grpcEventStreamSessionTracker) countForUser(userID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, s := range t.sessions {
		if s.UserID == userID {
			count++
		}
	}
	return count
}

func (t *grpcEventStreamSessionTracker) evictOldestLocal(userID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	var oldest *grpcEventStreamSession
	for _, s := range t.sessions {
		if s.UserID == userID && s.NodeID == grpcEventStreamNodeID && s.evicted != nil {
			if oldest == nil || s.CreatedAt.Before(oldest.CreatedAt) {
				oldest = s
			}
		}
	}
	if oldest == nil {
		return false
	}
	delete(t.sessions, oldest.ConnectionID)
	close(oldest.evicted)
	return true
}

type assertedEventStreamServer struct {
	pb.EventStreamService_SubscribeEventsServer
	ctx context.Context
}

func (s *assertedEventStreamServer) Context() context.Context {
	return s.ctx
}

type AdminCheckpointServer struct {
	pb.UnimplementedAdminCheckpointServiceServer
	Store registrystore.MemoryStore
}

func (s *AdminCheckpointServer) GetCheckpoint(ctx context.Context, req *pb.GetCheckpointRequest) (*pb.AdminCheckpoint, error) {
	if !hasGRPCRole(ctx, security.RoleAdmin) {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminCheckpointsRead); err != nil {
		return nil, err
	}
	checkpoints, ok := s.Store.(registrystore.AdminCheckpointStore)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "checkpoint storage unavailable")
	}
	clientID := strings.TrimSpace(req.GetClientId())
	if clientID == "" {
		return nil, status.Error(codes.InvalidArgument, "client_id is required")
	}
	if !grpcAllowCheckpointClient(ctx, clientID) {
		return nil, status.Error(codes.NotFound, "checkpoint not found")
	}
	var checkpoint *registrystore.ClientCheckpoint
	err := s.Store.InReadTx(ctx, func(txCtx context.Context) error {
		var err error
		checkpoint, err = checkpoints.AdminGetCheckpoint(txCtx, clientID)
		return err
	})
	if err != nil {
		return nil, mapCheckpointError(err)
	}
	return checkpointToProto(checkpoint)
}

func (s *AdminCheckpointServer) PutCheckpoint(ctx context.Context, req *pb.PutCheckpointRequest) (*pb.AdminCheckpoint, error) {
	if !hasGRPCRole(ctx, security.RoleAdmin) {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	if err := requireGRPCOIDCScope(ctx, security.PermissionAdminCheckpointsWrite); err != nil {
		return nil, err
	}
	checkpoints, ok := s.Store.(registrystore.AdminCheckpointStore)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "checkpoint storage unavailable")
	}
	clientID := strings.TrimSpace(req.GetClientId())
	if clientID == "" {
		return nil, status.Error(codes.InvalidArgument, "client_id is required")
	}
	if !grpcAllowCheckpointClient(ctx, clientID) {
		return nil, status.Error(codes.NotFound, "checkpoint not found")
	}
	contentType := strings.TrimSpace(req.GetContentType())
	if contentType == "" {
		return nil, status.Error(codes.InvalidArgument, "content_type is required")
	}
	if req.GetValue() == nil {
		return nil, status.Error(codes.InvalidArgument, "value is required")
	}
	raw, err := json.Marshal(req.GetValue().AsInterface())
	if err != nil {
		return nil, grpcStatusWithCause(codes.InvalidArgument, "value must be valid JSON", err)
	}
	var checkpoint *registrystore.ClientCheckpoint
	err = s.Store.InWriteTx(ctx, func(txCtx context.Context) error {
		var err error
		checkpoint, err = checkpoints.AdminPutCheckpoint(txCtx, registrystore.ClientCheckpoint{
			ClientID:    clientID,
			ContentType: contentType,
			Value:       json.RawMessage(raw),
		})
		return err
	})
	if err != nil {
		return nil, mapCheckpointError(err)
	}
	return checkpointToProto(checkpoint)
}

func grpcAllowCheckpointClient(ctx context.Context, clientID string) bool {
	authenticatedClientID := strings.TrimSpace(getClientID(ctx))
	return authenticatedClientID == "" || authenticatedClientID == clientID
}

func mapCheckpointError(err error) error {
	var notFound *registrystore.NotFoundError
	var validation *registrystore.ValidationError
	switch {
	case errors.As(err, &notFound):
		return grpcStatusWithCause(codes.NotFound, "checkpoint not found", err)
	case errors.As(err, &validation):
		return grpcStatusWithCause(codes.InvalidArgument, validation.Error(), err)
	default:
		return grpcStatusWithCause(codes.Internal, "internal server error", err)
	}
}

func checkpointToProto(checkpoint *registrystore.ClientCheckpoint) (*pb.AdminCheckpoint, error) {
	if checkpoint == nil {
		return nil, status.Error(codes.NotFound, "checkpoint not found")
	}
	var value any
	if len(checkpoint.Value) > 0 {
		if err := json.Unmarshal(checkpoint.Value, &value); err != nil {
			return nil, grpcStatusWithCause(codes.Internal, "internal server error", err)
		}
	}
	pValue, err := structpb.NewValue(value)
	if err != nil {
		return nil, grpcStatusWithCause(codes.Internal, "internal server error", err)
	}
	return &pb.AdminCheckpoint{
		ClientId:    checkpoint.ClientID,
		ContentType: checkpoint.ContentType,
		Value:       pValue,
		UpdatedAt:   timestamppb.New(checkpoint.UpdatedAt.UTC()),
	}, nil
}

func (s *EventStreamServer) SubscribeEvents(req *pb.SubscribeEventsRequest, stream pb.EventStreamService_SubscribeEventsServer) error {
	if s.EventBus == nil {
		return status.Error(codes.Unavailable, "event bus not available")
	}
	if req.GetAfterCursor() != "" && (s.Config == nil || !s.Config.OutboxEnabled) {
		return status.Error(codes.Unimplemented, "after_cursor requires the event outbox to be enabled")
	}
	detail := strings.TrimSpace(req.GetDetail())
	if detail == "" {
		detail = "summary"
	}
	if detail != "summary" && detail != "full" {
		return status.Error(codes.InvalidArgument, "detail must be one of: summary, full")
	}
	adminScope := req.GetScope() == pb.EventScope_EVENT_SCOPE_ADMIN
	switch req.GetScope() {
	case pb.EventScope_EVENT_SCOPE_UNSPECIFIED, pb.EventScope_EVENT_SCOPE_AUTHORIZED, pb.EventScope_EVENT_SCOPE_ADMIN:
	default:
		return status.Error(codes.InvalidArgument, "invalid event scope")
	}
	if !adminScope && s.UserIDAsserter != nil {
		assertedCtx, err := s.UserIDAsserter.ApplyGRPCContext(stream.Context())
		if err != nil {
			return err
		}
		stream = &assertedEventStreamServer{EventStreamService_SubscribeEventsServer: stream, ctx: assertedCtx}
	}
	if err := security.ApplyGRPCAuthenticatedRateLimits(
		stream.Context(),
		s.RateLimiter,
		"/memory.v1.EventStreamService/SubscribeEvents",
		true,
	); err != nil {
		return err
	}
	if adminScope {
		if !hasGRPCAdminEventAccess(stream.Context()) {
			return status.Error(codes.PermissionDenied, "admin or auditor role required")
		}
		if err := requireGRPCOIDCScope(stream.Context(), security.PermissionAdminEventsRead); err != nil {
			return err
		}
		justification := strings.TrimSpace(req.GetJustification())
		if s.Config != nil && s.Config.RequireJustification && justification == "" {
			return status.Error(codes.InvalidArgument, "admin justification required")
		}
		if justification != "" {
			log.Info("Admin audit",
				"caller", getUserID(stream.Context()),
				"clientID", getClientID(stream.Context()),
				"operation", "grpc /memory.v1.EventStreamService/SubscribeEvents",
				"requestID", security.RequestIDFromContext(stream.Context()),
				"justification", justification,
			)
		}
	}

	userID := getUserID(stream.Context())
	if !adminScope && userID == "" {
		return status.Error(codes.Unauthenticated, "missing authorization")
	}
	if !adminScope {
		if err := requireGRPCOIDCScope(stream.Context(), security.PermissionEventsRead); err != nil {
			return err
		}
	}
	var eventSession *grpcEventStreamSession
	if !adminScope && s.Config != nil && s.Config.SSEMaxConnectionsPerUser > 0 {
		streamCtx, cancel := context.WithCancel(stream.Context())
		stream = &assertedEventStreamServer{EventStreamService_SubscribeEventsServer: stream, ctx: streamCtx}
		session := &grpcEventStreamSession{
			ConnectionID: grpcOperationConnectionID(stream.Context()),
			UserID:       userID,
			NodeID:       grpcEventStreamNodeID,
			CreatedAt:    time.Now(),
			cancel:       cancel,
			evicted:      make(chan struct{}),
		}
		grpcEventStreamSessions.trackSession(session)
		defer func() {
			cancel()
			grpcEventStreamSessions.removeSession(session.ConnectionID)
		}()
		for grpcEventStreamSessions.countForUser(userID) > s.Config.SSEMaxConnectionsPerUser {
			if !grpcEventStreamSessions.evictOldestLocal(userID) {
				break
			}
		}
		eventSession = session
	}

	grpcClientID := getClientID(stream.Context())
	var grpcClientIDPtr *string
	if grpcClientID != "" {
		grpcClientIDPtr = &grpcClientID
	}

	// Parse kinds filter from request.
	kindsFilter := make(map[string]bool)
	for _, k := range req.GetKinds() {
		if k != "" {
			kindsFilter[k] = true
		}
	}
	conversationFilter := make(map[string]bool)
	for _, raw := range req.GetConversationIds() {
		id, err := requiredConversationID(raw)
		if err != nil {
			return status.Error(codes.InvalidArgument, "conversation_ids must contain non-empty values")
		}
		conversationFilter[id] = true
	}
	entryFilter := eventstream.NewEntryEventFilter(req.GetEntryChannels(), req.GetEntryContentTypes(), req.GetEntryRoles())
	security.MarkGRPCOperationResourcesValidated(stream.Context())
	userEntryLoader := func(ctx context.Context, conversationID string, entryID uuid.UUID, channel *model.Channel) (*model.Entry, error) {
		var found *model.Entry
		err := s.Store.InReadTx(ctx, func(txCtx context.Context) error {
			txCtx = config.WithContext(txCtx, s.Config)
			page, err := s.Store.GetEntries(txCtx, userID, conversationID, registrystore.EntryLookupQuery(entryID, channel, grpcClientIDPtr))
			if err != nil {
				return err
			}
			for i := range page.Data {
				if page.Data[i].ID == entryID {
					found = &page.Data[i]
					return nil
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return found, nil
	}
	adminEntryLoader := func(ctx context.Context, conversationID string, entryID uuid.UUID, _ *model.Channel) (*model.Entry, error) {
		var found *model.Entry
		err := s.Store.InReadTx(ctx, func(txCtx context.Context) error {
			txCtx = config.WithContext(txCtx, s.Config)
			page, err := s.Store.AdminGetEntries(txCtx, conversationID, registrystore.AdminEntryLookupQuery(entryID))
			if err != nil {
				return err
			}
			for i := range page.Data {
				if page.Data[i].ID == entryID {
					found = &page.Data[i]
					return nil
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return found, nil
	}

	lastCursor := ""
	defer func() {
		if operation := operationevent.FromContext(stream.Context()); operation != nil {
			operation.SetCursor(lastCursor)
		}
	}()
	outbox, _ := s.Store.(registrystore.EventOutboxStore)
	resumeCursor := req.GetAfterCursor()
	replayChecked := resumeCursor != ""
	replayAvailable := resumeCursor != ""

	if resumeCursor != "" {
		if outbox == nil {
			return status.Error(codes.Unimplemented, "durable event replay is not supported by the configured datastore")
		}
		if err := eventstream.ReplaySupported(stream.Context(), s.Store, outbox); err != nil {
			if errors.Is(err, registrystore.ErrOutboxReplayUnsupported) {
				return grpcStatusWithCause(codes.Unimplemented, "durable event replay is not supported by the configured datastore", err)
			}
			return grpcStatusWithCause(codes.Internal, "internal server error", err)
		}
	}

	canRecoverSlowConsumer := func() bool {
		if s.Config == nil || !s.Config.OutboxEnabled || outbox == nil || lastCursor == "" {
			return false
		}
		if replayChecked {
			return replayAvailable
		}
		if err := eventstream.ReplaySupported(stream.Context(), s.Store, outbox); err != nil {
			replayChecked = true
			replayAvailable = false
			return false
		}
		replayChecked = true
		replayAvailable = true
		return true
	}

streamLoop:
	for {
		subscribeUserID := userID
		if adminScope {
			subscribeUserID = ""
		}
		sub, err := s.EventBus.Subscribe(stream.Context(), subscribeUserID)
		if err != nil {
			return grpcStatusWithCause(codes.Internal, "internal server error", err)
		}

		if resumeCursor != "" {
			if err := sendGRPCPhaseEvent(stream, "replay"); err != nil {
				return err
			}
			var outcome replayGRPCOutcome
			var err error
			if adminScope {
				outcome, err = s.replayGRPCAdminEvents(stream, outbox, sub, resumeCursor, detail, s.replayBatchSize(), kindsFilter, conversationFilter, entryFilter, adminEntryLoader, &lastCursor)
			} else {
				outcome, err = s.replayGRPCEvents(stream, outbox, sub, resumeCursor, detail, userID, grpcClientIDPtr, s.replayBatchSize(), kindsFilter, conversationFilter, entryFilter, userEntryLoader, &lastCursor)
			}
			if err != nil {
				return err
			}
			switch outcome {
			case replayGRPCClosed:
				return nil
			case replayGRPCRecover:
				if canRecoverSlowConsumer() {
					resumeCursor = lastCursor
					continue streamLoop
				}
				evictData, _ := json.Marshal(map[string]string{"reason": "slow consumer"})
				_ = stream.Send(&pb.EventNotification{
					Event:  "evicted",
					Kind:   "stream",
					Data:   evictData,
					Cursor: eventCursorPtr(lastCursor),
				})
				return nil
			case replayGRPCContinue:
				resumeCursor = ""
			}
		}

		if err := sendGRPCPhaseEvent(stream, "live"); err != nil {
			return err
		}

		for {
			select {
			case <-stream.Context().Done():
				return nil
			case <-eventSessionEvicted(eventSession):
				log.Info("gRPC event stream evicted", "connID", eventSession.ConnectionID, "userID", userID, "reason", "too many connections")
				evictData, _ := json.Marshal(map[string]string{"reason": "too many connections"})
				_ = stream.Send(&pb.EventNotification{
					Event:  "evicted",
					Kind:   "stream",
					Data:   evictData,
					Cursor: eventCursorPtr(lastCursor),
				})
				return nil
			case event, ok := <-sub:
				if !ok {
					if canRecoverSlowConsumer() {
						resumeCursor = lastCursor
						continue streamLoop
					}
					evictData, _ := json.Marshal(map[string]string{"reason": "slow consumer"})
					_ = stream.Send(&pb.EventNotification{
						Event:  "evicted",
						Kind:   "stream",
						Data:   evictData,
						Cursor: eventCursorPtr(lastCursor),
					})
					return nil
				}

				if event.Internal {
					continue
				}
				if len(kindsFilter) > 0 && !kindsFilter[event.Kind] {
					continue
				}
				if !grpcEventMatchesConversationFilter(event, conversationFilter) {
					continue
				}
				loader := userEntryLoader
				if adminScope {
					loader = adminEntryLoader
				}
				matches, filterErr := entryFilter.Matches(stream.Context(), event, loader)
				if filterErr != nil {
					return grpcStatusWithCause(codes.Internal, "internal server error", filterErr)
				}
				if !matches {
					continue
				}

				// Stream events bypass membership filtering.
				if event.Kind == "stream" {
					if err := sendGRPCEvent(stream, event); err != nil {
						return err
					}
					continue
				}

				if event.OutboxCursor != "" {
					lastCursor = event.OutboxCursor
				}
				var enriched registryeventbus.Event
				var enrichedOK bool
				var err error
				if adminScope {
					enriched, enrichedOK, err = s.enrichGRPCAdminEvent(stream.Context(), detail, event)
				} else {
					enriched, enrichedOK, err = s.enrichGRPCEvent(stream.Context(), userID, grpcClientIDPtr, detail, event)
				}
				if err != nil {
					return grpcStatusWithCause(codes.Internal, "internal server error", err)
				}
				if !enrichedOK {
					continue
				}
				if err := sendGRPCEvent(stream, enriched); err != nil {
					return err
				}
			}
		}
	}
}

func grpcOperationConnectionID(ctx context.Context) string {
	if operation := operationevent.FromContext(ctx); operation != nil {
		if connectionID := operation.Snapshot().ConnectionID; connectionID != "" {
			return connectionID
		}
	}
	return uuid.NewString()
}

func eventCursorPtr(cursor string) *string {
	if cursor == "" {
		return nil
	}
	return &cursor
}

func sendGRPCEvent(stream pb.EventStreamService_SubscribeEventsServer, event registryeventbus.Event) error {
	data, err := json.Marshal(event.Data)
	if err != nil {
		return err
	}
	return stream.Send(&pb.EventNotification{
		Event:  event.Event,
		Kind:   event.Kind,
		Data:   data,
		Cursor: eventCursorPtr(event.OutboxCursor),
	})
}

func sendGRPCPhaseEvent(stream pb.EventStreamService_SubscribeEventsServer, phase string) error {
	return sendGRPCEvent(stream, registryeventbus.Event{
		Event: "phase",
		Kind:  "stream",
		Data:  map[string]string{"phase": phase},
	})
}

type replayGRPCOutcome int

const (
	replayGRPCContinue replayGRPCOutcome = iota
	replayGRPCClosed
	replayGRPCRecover
)

func (s *EventStreamServer) replayGRPCEvents(stream pb.EventStreamService_SubscribeEventsServer, outbox registrystore.EventOutboxStore, sub <-chan registryeventbus.Event, afterCursor, detail, userID string, clientID *string, batchSize int, kindsFilter map[string]bool, conversationFilter map[string]bool, entryFilter eventstream.EntryEventFilter, entryLoader eventstream.EntryDetailLoader, lastCursor *string) (replayGRPCOutcome, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	visibleGroups, err := s.loadReplayGroups(stream.Context(), userID)
	if err != nil {
		return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
	}
	query := registrystore.OutboxQuery{
		AfterCursor: afterCursor,
		Limit:       batchSize,
		Kinds:       grpcMapKeys(kindsFilter),
	}
	seen := map[string]struct{}{}
	cursor := afterCursor

	for {
		query.AfterCursor = cursor
		var page *registrystore.OutboxPage
		err := s.Store.InReadTx(stream.Context(), func(txCtx context.Context) error {
			var err error
			page, err = outbox.ListOutboxEvents(txCtx, query)
			return err
		})
		if err != nil {
			if errors.Is(err, registrystore.ErrStaleOutboxCursor) {
				invalidateData, _ := json.Marshal(map[string]string{"reason": "cursor beyond retention window"})
				_ = stream.Send(&pb.EventNotification{
					Event: "invalidate",
					Kind:  "stream",
					Data:  invalidateData,
				})
				return replayGRPCClosed, nil
			}
			return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
		}
		if page == nil {
			break
		}
		for _, replayEvent := range page.Events {
			cursor = replayEvent.Cursor
			event := registryeventbus.Event{
				Event:        replayEvent.Event,
				Kind:         replayEvent.Kind,
				Data:         json.RawMessage(replayEvent.Data),
				OutboxCursor: replayEvent.Cursor,
			}
			if replayEvent.Cursor != "" {
				seen[replayEvent.Cursor] = struct{}{}
				*lastCursor = replayEvent.Cursor
			}
			if !grpcUserCanReplayEvent(userID, visibleGroups, event) {
				continue
			}
			if !grpcEventMatchesConversationFilter(event, conversationFilter) {
				continue
			}
			matches, err := entryFilter.Matches(stream.Context(), event, entryLoader)
			if err != nil {
				return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
			if !matches {
				continue
			}
			enriched, ok, err := s.enrichGRPCEvent(stream.Context(), userID, clientID, detail, event)
			if err != nil {
				return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
			if !ok {
				continue
			}
			if err := sendGRPCEvent(stream, enriched); err != nil {
				return replayGRPCClosed, err
			}
		}
		if !page.HasMore || cursor == "" {
			break
		}
	}

	for {
		select {
		case event, ok := <-sub:
			if !ok {
				return replayGRPCRecover, nil
			}
			if event.Internal {
				continue
			}
			if event.OutboxCursor != "" {
				if _, ok := seen[event.OutboxCursor]; ok {
					continue
				}
				*lastCursor = event.OutboxCursor
			}
			if len(kindsFilter) > 0 && !kindsFilter[event.Kind] {
				continue
			}
			if !grpcEventMatchesConversationFilter(event, conversationFilter) {
				continue
			}
			matches, err := entryFilter.Matches(stream.Context(), event, entryLoader)
			if err != nil {
				return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
			if !matches {
				continue
			}
			enriched, ok, err := s.enrichGRPCEvent(stream.Context(), userID, clientID, detail, event)
			if err != nil {
				return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
			if !ok {
				continue
			}
			if err := sendGRPCEvent(stream, enriched); err != nil {
				return replayGRPCClosed, err
			}
		default:
			return replayGRPCContinue, nil
		}
	}
}

func (s *EventStreamServer) replayGRPCAdminEvents(stream pb.EventStreamService_SubscribeEventsServer, outbox registrystore.EventOutboxStore, sub <-chan registryeventbus.Event, afterCursor, detail string, batchSize int, kindsFilter map[string]bool, conversationFilter map[string]bool, entryFilter eventstream.EntryEventFilter, entryLoader eventstream.EntryDetailLoader, lastCursor *string) (replayGRPCOutcome, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	query := registrystore.OutboxQuery{
		AfterCursor: afterCursor,
		Limit:       batchSize,
		Kinds:       grpcMapKeys(kindsFilter),
	}
	seen := map[string]struct{}{}
	cursor := afterCursor

	for {
		query.AfterCursor = cursor
		var page *registrystore.OutboxPage
		err := s.Store.InReadTx(stream.Context(), func(txCtx context.Context) error {
			var err error
			page, err = outbox.ListOutboxEvents(txCtx, query)
			return err
		})
		if err != nil {
			if errors.Is(err, registrystore.ErrStaleOutboxCursor) {
				invalidateData, _ := json.Marshal(map[string]string{"reason": "cursor beyond retention window"})
				_ = stream.Send(&pb.EventNotification{
					Event: "invalidate",
					Kind:  "stream",
					Data:  invalidateData,
				})
				return replayGRPCClosed, nil
			}
			return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
		}
		if page == nil {
			break
		}
		for _, replayEvent := range page.Events {
			cursor = replayEvent.Cursor
			event := registryeventbus.Event{
				Event:        replayEvent.Event,
				Kind:         replayEvent.Kind,
				Data:         json.RawMessage(replayEvent.Data),
				OutboxCursor: replayEvent.Cursor,
			}
			if replayEvent.Cursor != "" {
				seen[replayEvent.Cursor] = struct{}{}
				*lastCursor = replayEvent.Cursor
			}
			if !grpcEventMatchesConversationFilter(event, conversationFilter) {
				continue
			}
			matches, err := entryFilter.Matches(stream.Context(), event, entryLoader)
			if err != nil {
				return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
			if !matches {
				continue
			}
			enriched, ok, err := s.enrichGRPCAdminEvent(stream.Context(), detail, event)
			if err != nil {
				return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
			if !ok {
				continue
			}
			if err := sendGRPCEvent(stream, enriched); err != nil {
				return replayGRPCClosed, err
			}
		}
		if !page.HasMore || cursor == "" {
			break
		}
	}

	for {
		select {
		case event, ok := <-sub:
			if !ok {
				return replayGRPCRecover, nil
			}
			if event.Internal {
				continue
			}
			if event.OutboxCursor != "" {
				if _, ok := seen[event.OutboxCursor]; ok {
					continue
				}
				*lastCursor = event.OutboxCursor
			}
			if len(kindsFilter) > 0 && !kindsFilter[event.Kind] {
				continue
			}
			if !grpcEventMatchesConversationFilter(event, conversationFilter) {
				continue
			}
			matches, err := entryFilter.Matches(stream.Context(), event, entryLoader)
			if err != nil {
				return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
			if !matches {
				continue
			}
			enriched, ok, err := s.enrichGRPCAdminEvent(stream.Context(), detail, event)
			if err != nil {
				return replayGRPCClosed, grpcStatusWithCause(codes.Internal, "internal server error", err)
			}
			if !ok {
				continue
			}
			if err := sendGRPCEvent(stream, enriched); err != nil {
				return replayGRPCClosed, err
			}
		default:
			return replayGRPCContinue, nil
		}
	}
}

func (s *EventStreamServer) enrichGRPCEvent(ctx context.Context, userID string, clientID *string, detail string, event registryeventbus.Event) (registryeventbus.Event, bool, error) {
	if detail != "full" || event.Kind == "stream" {
		return event, true, nil
	}
	data, ok := decodeGRPCEventData(event.Data)
	if !ok {
		return event, true, nil
	}

	switch event.Kind {
	case "conversation":
		conversationID, ok := decodeGRPCConversationIDField(data, "conversation")
		if !ok {
			return event, true, nil
		}
		conv, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
			return s.Store.GetConversation(txCtx, userID, conversationID)
		})
		if err != nil {
			return event, false, nil
		}
		event.Data = grpcFullEventData(conv, data)
		return event, true, nil
	case "entry":
		conversationID, ok := decodeGRPCConversationIDField(data, "conversation")
		if !ok {
			return event, true, nil
		}
		entryID, ok := decodeGRPCUUIDField(data, "entry")
		if !ok {
			return event, true, nil
		}
		channel := grpcChannelFromEventData(data)
		page, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.PagedEntries, error) {
			txCtx = config.WithContext(txCtx, s.Config)
			return s.Store.GetEntries(txCtx, userID, conversationID, registrystore.EntryLookupQuery(entryID, channel, clientID))
		})
		if err != nil {
			return event, false, nil
		}
		if page == nil {
			return event, false, nil
		}
		for i := range page.Data {
			if page.Data[i].ID == entryID {
				event.Data = grpcFullEventData(page.Data[i], data)
				return event, true, nil
			}
		}
		return event, false, nil
	default:
		return event, true, nil
	}
}

func (s *EventStreamServer) enrichGRPCAdminEvent(ctx context.Context, detail string, event registryeventbus.Event) (registryeventbus.Event, bool, error) {
	if detail != "full" || event.Kind == "stream" {
		return event, true, nil
	}
	data, ok := decodeGRPCEventData(event.Data)
	if !ok {
		return event, true, nil
	}
	switch event.Kind {
	case "conversation":
		conversationID, ok := decodeGRPCConversationIDField(data, "conversation")
		if !ok {
			return event, true, nil
		}
		conv, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
			return s.Store.AdminGetConversation(txCtx, conversationID)
		})
		if err != nil {
			return event, false, nil
		}
		event.Data = grpcFullEventData(conv, data)
		return event, true, nil
	case "entry":
		conversationID, ok := decodeGRPCConversationIDField(data, "conversation")
		if !ok {
			return event, true, nil
		}
		entryID, ok := decodeGRPCUUIDField(data, "entry")
		if !ok {
			return event, true, nil
		}
		page, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.PagedEntries, error) {
			txCtx = config.WithContext(txCtx, s.Config)
			return s.Store.AdminGetEntries(txCtx, conversationID, registrystore.AdminEntryLookupQuery(entryID))
		})
		if err != nil || page == nil {
			return event, false, nil
		}
		for i := range page.Data {
			if page.Data[i].ID == entryID {
				event.Data = grpcFullEventData(page.Data[i], data)
				return event, true, nil
			}
		}
		return event, false, nil
	default:
		return event, true, nil
	}
}

func grpcFullEventData(entity any, summary map[string]any) any {
	raw, err := json.Marshal(entity)
	if err != nil {
		return entity
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return entity
	}
	if group, ok := summary["conversation_group"].(string); ok && strings.TrimSpace(group) != "" {
		out["conversationGroupId"] = group
	}
	return out
}

func grpcEventMatchesConversationFilter(event registryeventbus.Event, filter map[string]bool) bool {
	if len(filter) == 0 || event.Kind == "stream" {
		return true
	}
	data, ok := decodeGRPCEventData(event.Data)
	if !ok {
		return false
	}
	if conversationID, ok := decodeGRPCConversationIDField(data, "conversation"); ok && filter[conversationID] {
		return true
	}
	if conversationID, ok := decodeGRPCConversationIDField(data, "conversation_id"); ok && filter[conversationID] {
		return true
	}
	return false
}

func decodeGRPCEventData(data any) (map[string]any, bool) {
	switch typed := data.(type) {
	case map[string]any:
		return typed, true
	case json.RawMessage:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err == nil {
			return out, true
		}
	case []byte:
		var out map[string]any
		if err := json.Unmarshal(typed, &out); err == nil {
			return out, true
		}
	}
	return nil, false
}

func decodeGRPCUUIDField(data map[string]any, field string) (uuid.UUID, bool) {
	raw, ok := data[field]
	if !ok {
		return uuid.Nil, false
	}
	value, ok := raw.(string)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func decodeGRPCConversationIDField(data map[string]any, field string) (string, bool) {
	raw, ok := data[field]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	id := strings.TrimSpace(value)
	if id == "" {
		return "", false
	}
	return string(id), true
}

func grpcChannelFromEventData(data map[string]any) *model.Channel {
	raw, _ := data["entry_channel"].(string)
	switch model.Channel(strings.ToLower(strings.TrimSpace(raw))) {
	case model.ChannelHistory:
		ch := model.ChannelHistory
		return &ch
	case model.ChannelContext:
		ch := model.ChannelContext
		return &ch
	case model.ChannelJournal:
		ch := model.ChannelJournal
		return &ch
	default:
		ch := model.ChannelHistory
		return &ch
	}
}

func grpcMapKeys(items map[string]bool) []string {
	if len(items) == 0 {
		return nil
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	return keys
}

func (s *EventStreamServer) replayBatchSize() int {
	if s.Config == nil || s.Config.OutboxReplayBatchSize <= 0 {
		return 1000
	}
	return s.Config.OutboxReplayBatchSize
}

func (s *EventStreamServer) loadReplayGroups(ctx context.Context, userID string) (map[uuid.UUID]bool, error) {
	visible := map[uuid.UUID]bool{}
	err := s.Store.InReadTx(ctx, func(txCtx context.Context) error {
		groupIDs, err := s.Store.ListConversationGroupIDs(txCtx, userID)
		if err != nil {
			return err
		}
		for _, groupID := range groupIDs {
			visible[groupID] = true
		}
		return nil
	})
	return visible, err
}

func grpcUserCanReplayEvent(userID string, visibleGroups map[uuid.UUID]bool, event registryeventbus.Event) bool {
	if event.Kind == "stream" {
		return true
	}
	data, ok := decodeGRPCEventData(event.Data)
	if !ok {
		return false
	}
	if event.Kind == "conversation" && event.Event == "deleted" {
		if members, ok := decodeGRPCUserListField(data, "members"); ok {
			for _, member := range members {
				if member == userID {
					return true
				}
			}
		}
	}
	groupID, ok := decodeGRPCUUIDField(data, "conversation_group")
	if !ok {
		return false
	}
	allowed := visibleGroups[groupID]
	if event.Kind == "membership" {
		targetUser, _ := data["user"].(string)
		if targetUser == userID {
			allowed = true
			switch event.Event {
			case "created", "updated":
				visibleGroups[groupID] = true
			case "deleted":
				delete(visibleGroups, groupID)
			}
		}
	}
	return allowed
}

func decodeGRPCUserListField(data map[string]any, field string) ([]string, bool) {
	raw, ok := data[field]
	if !ok {
		return nil, false
	}
	switch typed := raw.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				continue
			}
			out = append(out, value)
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}
