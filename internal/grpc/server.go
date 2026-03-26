package grpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/config"
	"github.com/chirino/memory-service/internal/episodic"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/model"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registryembed "github.com/chirino/memory-service/internal/registry/embed"
	registryepisodic "github.com/chirino/memory-service/internal/registry/episodic"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
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

	switch {
	case errors.As(err, &notFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.As(err, &forbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.As(err, &validation):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.As(err, &conflict):
		// ALREADY_EXISTS or FAILED_PRECONDITION are both valid mappings for HTTP 409/422 style conflicts.
		if strings.EqualFold(conflict.Code, "failed_precondition") {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func withMemoryRead[T any](ctx context.Context, store registrystore.MemoryStore, fn func(context.Context) (T, error)) (T, error) {
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

func inMemoryRead(ctx context.Context, store registrystore.MemoryStore, fn func(context.Context) error) error {
	return store.InReadTx(ctx, fn)
}

func inMemoryWrite(ctx context.Context, store registrystore.MemoryStore, fn func(context.Context) error) error {
	return store.InWriteTx(ctx, fn)
}

func withEpisodicRead[T any](ctx context.Context, store registryepisodic.EpisodicStore, fn func(context.Context) (T, error)) (T, error) {
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
	return store.InReadTx(ctx, fn)
}

func inEpisodicWrite(ctx context.Context, store registryepisodic.EpisodicStore, fn func(context.Context) error) error {
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
	if getUserID(ctx) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if !hasCapabilitiesAccessGRPC(ctx) {
		return nil, status.Error(codes.PermissionDenied, "client context or admin/auditor role required")
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
	Store registrystore.MemoryStore
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
	limit := 20
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
		if req.GetPage().GetPageSize() > 0 {
			limit = int(req.GetPage().GetPageSize())
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
		resp.Conversations = append(resp.Conversations, &pb.ConversationSummary{
			Id:          uuidToBytes(cs.ID),
			Title:       cs.Title,
			OwnerUserId: cs.OwnerUserID,
			CreatedAt:   cs.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt:   cs.UpdatedAt.Format("2006-01-02T15:04:05Z"),
			AccessLevel: mapAccessLevel(cs.AccessLevel),
			Archived:    cs.ArchivedAt != nil,
		})
		if cs.StartedByConversationID != nil {
			resp.Conversations[len(resp.Conversations)-1].StartedByConversationId = uuidToBytes(*cs.StartedByConversationID)
		}
		if cs.StartedByEntryID != nil {
			resp.Conversations[len(resp.Conversations)-1].StartedByEntryId = uuidToBytes(*cs.StartedByEntryID)
		}
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

	var meta map[string]any
	if req.GetMetadata() != nil {
		meta = req.GetMetadata().AsMap()
	}

	conv, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
		clientID := getClientID(ctx)
		return s.Store.CreateConversation(txCtx, userID, clientID, req.GetTitle(), meta, nil, nil, nil)
	})
	if err != nil {
		return nil, mapError(err)
	}

	return conversationToProto(conv), nil
}

func (s *ConversationsServer) GetConversation(ctx context.Context, req *pb.GetConversationRequest) (*pb.Conversation, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	convID, err := bytesToUUID(req.GetConversationId())
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

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var title *string
	if req.Title != nil {
		title = req.Title
	}
	var archived *bool
	if req.Archived != nil {
		archived = req.Archived
	}

	conv, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
		if archived != nil {
			conv, err := s.Store.GetConversation(txCtx, userID, convID)
			if err != nil {
				return nil, err
			}
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
			return conv, nil
		}
		return s.Store.UpdateConversation(txCtx, userID, convID, title, nil)
	})
	if err != nil {
		return nil, mapError(err)
	}
	return conversationToProto(conv), nil
}

func (s *ConversationsServer) ListForks(ctx context.Context, req *pb.ListForksRequest) (*pb.ListForksResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var afterCursor *string
	limit := 20
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
		if req.GetPage().GetPageSize() > 0 {
			limit = int(req.GetPage().GetPageSize())
		}
	}

	forks, cursor, err := func() ([]registrystore.ConversationForkSummary, *string, error) {
		type result struct {
			forks  []registrystore.ConversationForkSummary
			cursor *string
		}
		out, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (result, error) {
			forks, cursor, err := s.Store.ListForks(txCtx, userID, convID, afterCursor, limit)
			return result{forks: forks, cursor: cursor}, err
		})
		return out.forks, out.cursor, err
	}()
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.ListForksResponse{PageInfo: &pb.PageInfo{}}
	for _, f := range forks {
		fork := &pb.ConversationForkSummary{
			ConversationId: uuidToBytes(f.ID),
			Title:          f.Title,
			CreatedAt:      f.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
		if f.ForkedAtEntryID != nil {
			fork.ForkedAtEntryId = uuidToBytes(*f.ForkedAtEntryID)
		}
		if f.ForkedAtConversationID != nil {
			fork.ForkedAtConversationId = uuidToBytes(*f.ForkedAtConversationID)
		}
		resp.Forks = append(resp.Forks, fork)
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func (s *ConversationsServer) ListChildConversations(ctx context.Context, req *pb.ListChildConversationsRequest) (*pb.ListChildConversationsResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}
	var afterCursor *string
	limit := 20
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
		if req.GetPage().GetPageSize() > 0 {
			limit = int(req.GetPage().GetPageSize())
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
			Id:          uuidToBytes(cs.ID),
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
		resp.Conversations = append(resp.Conversations, item)
	}
	if cursor != nil {
		resp.PageInfo.NextPageToken = *cursor
	}
	return resp, nil
}

func conversationToProto(conv *registrystore.ConversationDetail) *pb.Conversation {
	c := &pb.Conversation{
		Id:          uuidToBytes(conv.ID),
		Title:       conv.Title,
		OwnerUserId: conv.OwnerUserID,
		CreatedAt:   conv.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   conv.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		AccessLevel: mapAccessLevel(conv.AccessLevel),
		Archived:    conv.ArchivedAt != nil,
	}
	if conv.ForkedAtEntryID != nil {
		c.ForkedAtEntryId = uuidToBytes(*conv.ForkedAtEntryID)
	}
	if conv.ForkedAtConversationID != nil {
		c.ForkedAtConversationId = uuidToBytes(*conv.ForkedAtConversationID)
	}
	if conv.StartedByConversationID != nil {
		c.StartedByConversationId = uuidToBytes(*conv.StartedByConversationID)
	}
	if conv.StartedByEntryID != nil {
		c.StartedByEntryId = uuidToBytes(*conv.StartedByEntryID)
	}
	return c
}

// --- Entries Service ---

type EntriesServer struct {
	pb.UnimplementedEntriesServiceServer
	Store registrystore.MemoryStore
}

func (s *EntriesServer) ListEntries(ctx context.Context, req *pb.ListEntriesRequest) (*pb.ListEntriesResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var afterCursor *string
	limit := 20
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
		if req.GetPage().GetPageSize() > 0 {
			limit = int(req.GetPage().GetPageSize())
		}
	}

	channel := model.ChannelHistory
	switch req.GetChannel() {
	case pb.Channel_CONTEXT:
		channel = model.ChannelContext
	case pb.Channel_HISTORY, pb.Channel_CHANNEL_UNSPECIFIED:
		channel = model.ChannelHistory
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid channel")
	}

	clientID := getClientID(ctx)
	var clientIDPtr *string
	if clientID != "" {
		clientIDPtr = &clientID
	}
	var agentIDPtr *string

	var epochFilter *registrystore.MemoryEpochFilter
	if channel == model.ChannelContext {
		// Keep parity with REST list behavior: context reads without a client id
		// degrade to history channel to avoid cross-agent context visibility.
		if clientIDPtr == nil {
			channel = model.ChannelHistory
		}
	}
	if channel == model.ChannelContext {
		filter, err := registrystore.ParseMemoryEpochFilter(req.GetEpochFilter())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		epochFilter = filter
	}

	allForks := req.GetForks() == "all"

	result, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.PagedEntries, error) {
		return s.Store.GetEntries(txCtx, userID, convID, afterCursor, limit, &channel, epochFilter, clientIDPtr, agentIDPtr, allForks)
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
	return resp, nil
}

func (s *EntriesServer) AppendEntry(ctx context.Context, req *pb.AppendEntryRequest) (*pb.Entry, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	clientID := getClientID(ctx)
	if clientID == "" {
		return nil, status.Error(codes.PermissionDenied, "API key (X-Client-ID) required for append")
	}

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	entry := req.GetEntry()
	var agentIDPtr *string
	if entry.GetAgentId() != "" {
		value := entry.GetAgentId()
		agentIDPtr = &value
	}
	ch := "history"
	if entry.GetChannel() == pb.Channel_CONTEXT {
		ch = string(model.ChannelContext)
	}

	// Validate history channel entries
	if ch == "history" {
		ct := entry.GetContentType()
		if ct != "history" && !strings.HasPrefix(ct, "history/") {
			return nil, status.Error(codes.InvalidArgument, "History channel entries must use 'history' or 'history/<subtype>' as the contentType")
		}
		if len(entry.GetContent()) != 1 {
			return nil, status.Error(codes.InvalidArgument, "History channel entries must contain exactly 1 content object")
		}
		// Validate role field
		c := entry.GetContent()[0]
		if sv := c.GetStructValue(); sv != nil {
			roleField := sv.GetFields()["role"]
			if roleField == nil || (roleField.GetStringValue() != "USER" && roleField.GetStringValue() != "AI") {
				return nil, status.Error(codes.InvalidArgument, "History channel content must have a 'role' field with value 'USER' or 'AI'")
			}
		}
	}

	// Handle fork metadata: if conversation doesn't exist and fork metadata is provided, create it
	if len(entry.GetForkedAtConversationId()) > 0 {
		forkedAtConvID, ferr := bytesToUUID(entry.GetForkedAtConversationId())
		if ferr != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid forked_at_conversation_id")
		}
		var forkedAtEntryID *uuid.UUID
		if len(entry.GetForkedAtEntryId()) > 0 {
			id, ferr := bytesToUUID(entry.GetForkedAtEntryId())
			if ferr != nil {
				return nil, status.Error(codes.InvalidArgument, "invalid forked_at_entry_id")
			}
			forkedAtEntryID = &id
		}
		// Try to create the fork conversation with the specified conversation ID
		_, _ = withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
			clientID := getClientID(ctx)
			return s.Store.CreateConversationWithID(txCtx, userID, clientID, convID, "", nil, nil, &forkedAtConvID, forkedAtEntryID)
		})
		// Ignore error — conversation may already exist
	}

	var content json.RawMessage
	if len(entry.GetContent()) > 0 {
		list, _ := structpb.NewList(nil)
		for _, v := range entry.GetContent() {
			list.Values = append(list.Values, v)
		}
		content, _ = list.MarshalJSON()
	}

	entries, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) ([]model.Entry, error) {
		return s.Store.AppendEntries(txCtx, userID, convID, []registrystore.CreateEntryRequest{{
			Content:                 content,
			ContentType:             entry.GetContentType(),
			Channel:                 ch,
			AgentID:                 agentIDPtr,
			StartedByConversationID: uuidFromBytesPtr(entry.GetStartedByConversationId()),
			StartedByEntryID:        uuidFromBytesPtr(entry.GetStartedByEntryId()),
		}}, &clientID, agentIDPtr, nil)
	})
	if err != nil {
		return nil, mapError(err)
	}
	if len(entries) == 0 {
		return nil, status.Error(codes.Internal, "no entry created")
	}
	return entryToProto(&entries[0]), nil
}

func (s *EntriesServer) SyncEntries(ctx context.Context, req *pb.SyncEntriesRequest) (*pb.SyncEntriesResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	convID, err := bytesToUUID(req.GetConversationId())
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
		for _, v := range entry.GetContent() {
			list.Values = append(list.Values, v)
		}
		syncContent, _ = list.MarshalJSON()
	}

	result, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*registrystore.SyncResult, error) {
		return s.Store.SyncAgentEntry(txCtx, userID, convID, registrystore.CreateEntryRequest{
			Content:     syncContent,
			ContentType: entry.GetContentType(),
			Channel:     string(model.ChannelContext),
			AgentID:     stringPtrIfNotEmpty(entry.GetAgentId()),
		}, clientID, stringPtrIfNotEmpty(entry.GetAgentId()))
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
	return resp, nil
}

func entryToProto(e *model.Entry) *pb.Entry {
	entry := &pb.Entry{
		Id:             uuidToBytes(e.ID),
		ConversationId: uuidToBytes(e.ConversationID),
		Channel:        mapChannel(e.Channel),
		ContentType:    e.ContentType,
		CreatedAt:      e.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if e.UserID != nil {
		entry.UserId = *e.UserID
	}
	if e.Epoch != nil {
		entry.Epoch = *e.Epoch
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
	Store registrystore.MemoryStore
}

func (s *MembershipsServer) ListMemberships(ctx context.Context, req *pb.ListMembershipsRequest) (*pb.ListMembershipsResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	var afterCursor *string
	limit := 20
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
		if req.GetPage().GetPageSize() > 0 {
			limit = int(req.GetPage().GetPageSize())
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
			ConversationId: uuidToBytes(convID),
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

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	level := protoToAccessLevel(req.GetAccessLevel())
	m, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*model.ConversationMembership, error) {
		return s.Store.ShareConversation(txCtx, userID, convID, req.GetUserId(), level)
	})
	if err != nil {
		return nil, mapError(err)
	}

	return &pb.ConversationMembership{
		ConversationId: uuidToBytes(convID),
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

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	level := protoToAccessLevel(req.GetAccessLevel())
	m, err := withMemoryWrite(ctx, s.Store, func(txCtx context.Context) (*model.ConversationMembership, error) {
		return s.Store.UpdateMembership(txCtx, userID, convID, req.GetMemberUserId(), level)
	})
	if err != nil {
		return nil, mapError(err)
	}

	return &pb.ConversationMembership{
		ConversationId: uuidToBytes(convID),
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

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	if err := inMemoryWrite(ctx, s.Store, func(txCtx context.Context) error {
		return s.Store.DeleteMembership(txCtx, userID, convID, req.GetMemberUserId())
	}); err != nil {
		return nil, mapError(err)
	}
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

	role := ""
	switch req.GetRole() {
	case pb.TransferRole_SENDER:
		role = "sender"
	case pb.TransferRole_RECIPIENT:
		role = "recipient"
	}

	var afterCursor *string
	limit := 20
	if req.GetPage() != nil {
		if req.GetPage().GetPageToken() != "" {
			t := req.GetPage().GetPageToken()
			afterCursor = &t
		}
		if req.GetPage().GetPageSize() > 0 {
			limit = int(req.GetPage().GetPageSize())
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
			ConversationId: uuidToBytes(t.ConversationID),
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
		ConversationId: uuidToBytes(t.ConversationID),
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

	convID, err := bytesToUUID(req.GetConversationId())
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
		ConversationId: uuidToBytes(t.ConversationID),
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
	Store  registrystore.MemoryStore
	Config *config.Config
}

// isIndexer checks if the user has the indexer or admin role based on config.
func (s *SearchServer) isIndexer(userID string) bool {
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
	if s.Config != nil && !s.Config.SearchFulltextEnabled {
		return nil, status.Error(codes.Unavailable, "full-text search is disabled")
	}

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 20
	}
	includeEntry := true
	if req.IncludeEntry != nil {
		includeEntry = *req.IncludeEntry
	}
	var afterCursor *string
	if v := strings.TrimSpace(req.GetAfter()); v != "" {
		afterCursor = &v
	}

	results, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.SearchResults, error) {
		return s.Store.SearchEntries(txCtx, userID, req.GetQuery(), afterCursor, limit, includeEntry, false)
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &pb.SearchEntriesResponse{}
	for _, r := range results.Data {
		sr := &pb.SearchResult{
			ConversationId: uuidToBytes(r.ConversationID),
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

func (s *SearchServer) IndexConversations(ctx context.Context, req *pb.IndexConversationsRequest) (*pb.IndexConversationsResponse, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	if !s.isIndexer(userID) {
		return nil, status.Error(codes.PermissionDenied, "indexer or admin role required")
	}

	var entries []registrystore.IndexEntryRequest
	for _, e := range req.GetEntries() {
		convID, err := bytesToUUID(e.GetConversationId())
		if err != nil {
			continue
		}
		entryID, err := bytesToUUID(e.GetEntryId())
		if err != nil {
			continue
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
	if !s.isIndexer(userID) {
		return nil, status.Error(codes.PermissionDenied, "indexer or admin role required")
	}

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 20
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
			ConversationId: uuidToBytes(e.ConversationID),
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

func (s *MemoriesServer) PutMemory(ctx context.Context, req *pb.PutMemoryRequest) (*pb.MemoryWriteResult, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
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

	pc := memoryPolicyContext(ctx)
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
		})
	})
	if err != nil {
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
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
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

	if s.Policy != nil {
		decision, err := s.Policy.EvaluateAuthz(ctx, "read", namespace, key, nil, nil, memoryPolicyContext(ctx))
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
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
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

	if s.Policy != nil {
		decision, err := s.Policy.EvaluateAuthz(ctx, "update", namespace, key, nil, nil, memoryPolicyContext(ctx))
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
		return s.Store.ArchiveMemory(txCtx, namespace, key)
	}); err != nil {
		return nil, episodicInternalError("failed to archive memory", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *MemoriesServer) SearchMemories(ctx context.Context, req *pb.SearchMemoriesRequest) (*pb.SearchMemoriesResponse, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	if len(req.GetNamespacePrefix()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "namespace_prefix is required")
	}
	if err := validateMemoryNamespace(req.GetNamespacePrefix(), s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	limit := int(req.GetLimit())
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	offset := int(req.GetOffset())

	filter := map[string]interface{}{}
	if req.GetFilter() != nil {
		filter = req.GetFilter().AsMap()
	}
	archived := protoArchiveFilterToEpisodic(req.GetArchived())
	effectivePrefix := req.GetNamespacePrefix()
	if s.Policy != nil {
		var err error
		effectivePrefix, filter, err = s.Policy.InjectFilter(ctx, req.GetNamespacePrefix(), filter, memoryPolicyContext(ctx))
		if err != nil {
			return nil, episodicInternalError("filter injection error", err)
		}
	}

	query := strings.TrimSpace(req.GetQuery())
	if query != "" && s.Embedder != nil {
		items, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.MemoryItem, error) {
			return semanticSearchMemories(txCtx, s.Store, s.Embedder, effectivePrefix, filter, query, limit, archived)
		})
		if err != nil {
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
		if len(items) > 0 {
			return memoryItemsToSearchResponse(items)
		}
	}

	items, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) ([]registryepisodic.MemoryItem, error) {
		return s.Store.SearchMemories(txCtx, effectivePrefix, filter, limit, offset, archived)
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
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
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

	if s.Policy != nil {
		var err error
		prefix, _, err = s.Policy.InjectFilter(ctx, prefix, nil, memoryPolicyContext(ctx))
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

func (s *MemoriesServer) GetMemoryIndexStatus(ctx context.Context, _ *emptypb.Empty) (*pb.MemoryIndexStatusResponse, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	id := security.IdentityFromContext(ctx)
	if id == nil || !id.Roles[security.RoleAdmin] {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}

	count, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) (int64, error) {
		return s.Store.AdminCountPendingIndexing(txCtx)
	})
	if err != nil {
		return nil, episodicInternalError("failed to read memory index status", err)
	}
	return &pb.MemoryIndexStatusResponse{Pending: count}, nil
}

func (s *MemoriesServer) GetMemoryUsage(ctx context.Context, req *pb.GetMemoryUsageRequest) (*pb.MemoryUsage, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	id := security.IdentityFromContext(ctx)
	if id == nil || !id.Roles[security.RoleAdmin] {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}

	namespace := req.GetNamespace()
	if err := validateMemoryNamespace(namespace, s.maxDepth()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	key := req.GetKey()
	if key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}

	usage, err := withEpisodicRead(ctx, s.Store, func(txCtx context.Context) (*registryepisodic.MemoryUsage, error) {
		return s.Store.GetMemoryUsage(txCtx, namespace, key)
	})
	if err != nil {
		return nil, episodicInternalError("failed to read memory usage", err)
	}
	if usage == nil {
		return nil, status.Error(codes.NotFound, "memory usage not found")
	}
	return memoryUsageToProto(*usage), nil
}

func (s *MemoriesServer) ListTopMemoryUsage(ctx context.Context, req *pb.ListTopMemoryUsageRequest) (*pb.ListTopMemoryUsageResponse, error) {
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "episodic store is not configured")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	id := security.IdentityFromContext(ctx)
	if id == nil || !id.Roles[security.RoleAdmin] {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
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

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
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

func memoryPolicyContext(ctx context.Context) episodic.PolicyContext {
	roles := []string{}
	if id := security.IdentityFromContext(ctx); id != nil {
		if id.Roles[security.RoleAdmin] {
			roles = append(roles, security.RoleAdmin)
		}
	}
	return episodic.PolicyContext{
		UserID:   getUserID(ctx),
		ClientID: getClientID(ctx),
		JWTClaims: map[string]interface{}{
			"roles": roles,
		},
	}
}

func episodicInternalError(message string, err error) error {
	log.Error("episodic gRPC error", "error", err, "stack", string(debug.Stack()))
	return status.Error(codes.Internal, message)
}

func memoryWriteResultToProto(item *registryepisodic.MemoryWriteResult) (*pb.MemoryWriteResult, error) {
	resp := &pb.MemoryWriteResult{
		Id:        uuidToBytes(item.ID),
		Namespace: append([]string(nil), item.Namespace...),
		Key:       item.Key,
		CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339),
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
	if item.ExpiresAt != nil {
		expiresAt := item.ExpiresAt.UTC().Format(time.RFC3339)
		resp.ExpiresAt = &expiresAt
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

func semanticSearchMemories(ctx context.Context, store registryepisodic.EpisodicStore, embedder registryembed.Embedder, namespacePrefix []string, filter map[string]interface{}, query string, limit int, archived registryepisodic.ArchiveFilter) ([]registryepisodic.MemoryItem, error) {
	embeddings, err := embedder.EmbedTexts(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, nil
	}

	nsEncoded, err := episodic.EncodeNamespace(namespacePrefix, 0)
	if err != nil {
		return nil, err
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
	return results, nil
}

// --- Attachments Service ---

type AttachmentsServer struct {
	pb.UnimplementedAttachmentsServiceServer
	Store       registrystore.MemoryStore
	AttachStore registryattach.AttachmentStore
	MaxBodySize int64
	Config      *config.Config
}

func (s *AttachmentsServer) UploadAttachment(stream pb.AttachmentsService_UploadAttachmentServer) error {
	if s.AttachStore == nil {
		return status.Error(codes.FailedPrecondition, "attachment store is not configured")
	}
	userID := getUserID(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "missing authorization")
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
				return status.Error(codes.InvalidArgument, out.err.Error())
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
				return status.Error(codes.InvalidArgument, err.Error())
			}
			expiresAt := time.Now().Add(expiresIn)

			attachment, err := withMemoryWrite(stream.Context(), s.Store, func(txCtx context.Context) (*model.Attachment, error) {
				return s.Store.CreateAttachment(txCtx, userID, uuid.Nil, model.Attachment{
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

			resp := &pb.UploadAttachmentResponse{
				Id:          attachment.ID.String(),
				Href:        "/v1/attachments/" + attachment.ID.String(),
				ContentType: attachment.ContentType,
				Size:        out.res.Size,
				Sha256:      out.res.SHA256,
			}
			if attachment.Filename != nil {
				resp.Filename = *attachment.Filename
			}
			if attachment.ExpiresAt != nil {
				resp.ExpiresAt = attachment.ExpiresAt.UTC().Format(time.RFC3339)
			}
			return stream.SendAndClose(resp)
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
				return status.Error(codes.Internal, err.Error())
			}
		default:
			return status.Error(codes.InvalidArgument, "invalid upload payload")
		}
	}
}

func (s *AttachmentsServer) GetAttachment(ctx context.Context, req *pb.GetAttachmentRequest) (*pb.AttachmentInfo, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	attachmentID, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid attachment id")
	}

	attachment, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*model.Attachment, error) {
		return s.Store.GetAttachment(txCtx, userID, uuid.Nil, attachmentID)
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

	attachmentID, err := uuid.Parse(req.GetId())
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid attachment id")
	}
	attachment, err := withMemoryRead(stream.Context(), s.Store, func(txCtx context.Context) (*model.Attachment, error) {
		return s.Store.GetAttachment(txCtx, userID, uuid.Nil, attachmentID)
	})
	if err != nil {
		return mapError(err)
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
		return status.Error(codes.Internal, err.Error())
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
			return status.Error(codes.Internal, readErr.Error())
		}
	}
	return nil
}

func attachmentToProto(attachment *model.Attachment) *pb.AttachmentInfo {
	info := &pb.AttachmentInfo{
		Id:          attachment.ID.String(),
		Href:        "/v1/attachments/" + attachment.ID.String(),
		ContentType: attachment.ContentType,
		CreatedAt:   attachment.CreatedAt.UTC().Format(time.RFC3339),
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
	return info
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

func (s *ResponseRecorderServer) publishResponseEvent(ctx context.Context, eventName string, statusValue string, convUUID uuid.UUID, recordingID string) {
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
	var convUUID uuid.UUID
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
					return status.Error(codes.Internal, err.Error())
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
						return status.Error(codes.Internal, err.Error())
					}
				}
				s.publishResponseEvent(stream.Context(), "deleted", "completed", convUUID, convID)
				return stream.SendAndClose(&pb.RecordResponse{
					Status: pb.RecordStatus_RECORD_STATUS_SUCCESS,
				})
			}
			if err != nil {
				return err
			}

			if convID == "" && len(req.GetConversationId()) > 0 {
				id, err := bytesToUUID(req.GetConversationId())
				if err != nil {
					return status.Error(codes.InvalidArgument, "invalid conversation_id")
				}
				if err := s.requireConversationAccess(stream.Context(), id, model.AccessLevelWriter); err != nil {
					return err
				}
				convUUID = id
				convID = id.String()
				advertised := s.resolveAdvertisedAddress(stream.Context())
				recorder, err = s.Resumer.RecorderWithAddress(stream.Context(), convID, advertised)
				if err != nil {
					log.Warn("record stream: failed to create recorder", "conversation_id", convID, "error", err)
					return status.Error(codes.Internal, err.Error())
				}
				cancelStream, err = s.Resumer.CancelStream(stream.Context(), convID)
				if err != nil {
					log.Warn("record stream: failed to subscribe to cancel channel", "conversation_id", convID, "error", err)
					return status.Error(codes.Internal, err.Error())
				}
				s.publishResponseEvent(stream.Context(), "created", "started", convUUID, convID)
			}
			if convID == "" {
				return status.Error(codes.InvalidArgument, "conversation_id is required in first record chunk")
			}

			if recorder != nil && req.GetContent() != "" {
				if err := recorder.Record(req.GetContent()); err != nil {
					log.Warn("record stream: failed to write chunk", "conversation_id", convID, "error", err)
					return status.Error(codes.Internal, err.Error())
				}
			}

			if req.GetComplete() && recorder != nil {
				if err := s.requireConversationAccess(stream.Context(), convUUID, model.AccessLevelWriter); err != nil {
					return err
				}
				if err := recorder.Complete(); err != nil {
					log.Warn("record stream: complete failed", "conversation_id", convID, "error", err)
					return status.Error(codes.Internal, err.Error())
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
	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid conversation_id")
	}
	if err := s.requireConversationAccess(stream.Context(), convID, model.AccessLevelReader); err != nil {
		return err
	}

	advertised := s.resolveAdvertisedAddress(stream.Context())
	ch, redirectAddress, err := s.Resumer.ReplayWithAddress(stream.Context(), convID.String(), advertised)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if redirectAddress != "" {
		return stream.Send(&pb.ReplayResponse{RedirectAddress: redirectAddress})
	}
	for token := range ch {
		if err := stream.Send(&pb.ReplayResponse{Content: token}); err != nil {
			return err
		}
	}
	return nil
}

func (s *ResponseRecorderServer) Cancel(ctx context.Context, req *pb.CancelRecordRequest) (*pb.CancelRecordResponse, error) {
	if !s.Enabled {
		return nil, status.Error(codes.FailedPrecondition, "response recorder disabled")
	}
	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}
	if err := s.requireConversationAccess(ctx, convID, model.AccessLevelWriter); err != nil {
		return nil, err
	}

	advertised := s.resolveAdvertisedAddress(ctx)
	redirectAddress, err := s.Resumer.RequestCancelWithAddress(ctx, convID.String(), advertised)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if redirectAddress != "" {
		return &pb.CancelRecordResponse{Accepted: false, RedirectAddress: redirectAddress}, nil
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
	for _, b := range req.GetConversationIds() {
		id, err := bytesToUUID(b)
		if err != nil {
			continue
		}
		if err := s.requireConversationAccess(ctx, id, model.AccessLevelReader); err != nil {
			continue
		}
		ids = append(ids, id.String())
	}

	active, err := s.Resumer.Check(ctx, ids)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	resp := &pb.CheckRecordingsResponse{}
	for _, idStr := range active {
		id, _ := uuid.Parse(idStr)
		resp.ConversationIds = append(resp.ConversationIds, uuidToBytes(id))
	}
	return resp, nil
}

func (s *ResponseRecorderServer) requireConversationAccess(ctx context.Context, conversationID uuid.UUID, minLevel model.AccessLevel) error {
	if s.Store == nil {
		return status.Error(codes.Internal, "response recorder store not configured")
	}
	userID := getUserID(ctx)
	if userID == "" {
		return status.Error(codes.Unauthenticated, "missing authorization")
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
	Store    registrystore.MemoryStore
	EventBus registryeventbus.EventBus
	Config   *config.Config
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

	userID := getUserID(stream.Context())

	// Parse kinds filter from request.
	kindsFilter := make(map[string]bool)
	for _, k := range req.GetKinds() {
		if k != "" {
			kindsFilter[k] = true
		}
	}

	lastCursor := ""
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
				return status.Error(codes.Unimplemented, "durable event replay is not supported by the configured datastore")
			}
			return status.Error(codes.Internal, "failed to initialize event replay")
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
		sub, err := s.EventBus.Subscribe(stream.Context(), userID)
		if err != nil {
			return status.Error(codes.Internal, "failed to subscribe to event bus")
		}

		if resumeCursor != "" {
			if err := sendGRPCPhaseEvent(stream, "replay"); err != nil {
				return err
			}
			outcome, err := s.replayGRPCEvents(stream, outbox, sub, resumeCursor, detail, userID, s.replayBatchSize(), kindsFilter, &lastCursor)
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
				enriched, ok, err := s.enrichGRPCEvent(stream.Context(), userID, detail, event)
				if err != nil {
					return status.Error(codes.Internal, "failed to enrich event")
				}
				if !ok {
					continue
				}
				if err := sendGRPCEvent(stream, enriched); err != nil {
					return err
				}
			}
		}
	}
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

func (s *EventStreamServer) replayGRPCEvents(stream pb.EventStreamService_SubscribeEventsServer, outbox registrystore.EventOutboxStore, sub <-chan registryeventbus.Event, afterCursor, detail, userID string, batchSize int, kindsFilter map[string]bool, lastCursor *string) (replayGRPCOutcome, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	visibleGroups, err := s.loadReplayGroups(stream.Context(), userID)
	if err != nil {
		return replayGRPCClosed, status.Error(codes.Internal, "failed to preload replay membership state")
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
			if err == registrystore.ErrStaleOutboxCursor {
				invalidateData, _ := json.Marshal(map[string]string{"reason": "cursor beyond retention window"})
				_ = stream.Send(&pb.EventNotification{
					Event: "invalidate",
					Kind:  "stream",
					Data:  invalidateData,
				})
				return replayGRPCClosed, nil
			}
			return replayGRPCClosed, status.Error(codes.Internal, "failed to replay outbox events")
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
			enriched, ok, err := s.enrichGRPCEvent(stream.Context(), userID, detail, event)
			if err != nil {
				return replayGRPCClosed, status.Error(codes.Internal, "failed to enrich replayed event")
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
			enriched, ok, err := s.enrichGRPCEvent(stream.Context(), userID, detail, event)
			if err != nil {
				return replayGRPCClosed, status.Error(codes.Internal, "failed to enrich replayed event")
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

func (s *EventStreamServer) enrichGRPCEvent(ctx context.Context, userID, detail string, event registryeventbus.Event) (registryeventbus.Event, bool, error) {
	if detail != "full" || event.Kind == "stream" {
		return event, true, nil
	}
	data, ok := decodeGRPCEventData(event.Data)
	if !ok {
		return event, true, nil
	}

	switch event.Kind {
	case "conversation":
		conversationID, ok := decodeGRPCUUIDField(data, "conversation")
		if !ok {
			return event, true, nil
		}
		conv, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.ConversationDetail, error) {
			return s.Store.GetConversation(txCtx, userID, conversationID)
		})
		if err != nil {
			return event, false, nil
		}
		event.Data = conv
		return event, true, nil
	case "entry":
		conversationID, ok := decodeGRPCUUIDField(data, "conversation")
		if !ok {
			return event, true, nil
		}
		entryID, ok := decodeGRPCUUIDField(data, "entry")
		if !ok {
			return event, true, nil
		}
		page, err := withMemoryRead(ctx, s.Store, func(txCtx context.Context) (*registrystore.PagedEntries, error) {
			return s.Store.GetEntries(txCtx, userID, conversationID, nil, 5000, nil, nil, nil, nil, true)
		})
		if err != nil {
			return event, false, nil
		}
		if page == nil {
			return event, false, nil
		}
		for i := range page.Data {
			if page.Data[i].ID == entryID {
				event.Data = page.Data[i]
				return event, true, nil
			}
		}
		return event, false, nil
	default:
		return event, true, nil
	}
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
