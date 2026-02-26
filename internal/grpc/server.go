package grpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chirino/memory-service/internal/config"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/model"
	registryattach "github.com/chirino/memory-service/internal/registry/attach"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	internalresumer "github.com/chirino/memory-service/internal/resumer"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
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
	case model.ChannelMemory:
		return pb.Channel_MEMORY
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

// --- System Service ---

type SystemServer struct {
	pb.UnimplementedSystemServiceServer
}

func (s *SystemServer) GetHealth(_ context.Context, _ *emptypb.Empty) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok"}, nil
}

// --- Conversations Service ---

type ConversationsServer struct {
	pb.UnimplementedConversationsServiceServer
	Store registrystore.MemoryStore
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

	summaries, cursor, err := s.Store.ListConversations(ctx, userID, query, afterCursor, limit, mode)
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
		})
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

	conv, err := s.Store.CreateConversation(ctx, userID, req.GetTitle(), meta, nil, nil)
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

	conv, err := s.Store.GetConversation(ctx, userID, convID)
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

	conv, err := s.Store.UpdateConversation(ctx, userID, convID, title, nil)
	if err != nil {
		return nil, mapError(err)
	}
	return conversationToProto(conv), nil
}

func (s *ConversationsServer) DeleteConversation(ctx context.Context, req *pb.DeleteConversationRequest) (*emptypb.Empty, error) {
	userID := getUserID(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	convID, err := bytesToUUID(req.GetConversationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid conversation_id")
	}

	if err := s.Store.DeleteConversation(ctx, userID, convID); err != nil {
		return nil, mapError(err)
	}
	return &emptypb.Empty{}, nil
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

	forks, cursor, err := s.Store.ListForks(ctx, userID, convID, afterCursor, limit)
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

func conversationToProto(conv *registrystore.ConversationDetail) *pb.Conversation {
	c := &pb.Conversation{
		Id:          uuidToBytes(conv.ID),
		Title:       conv.Title,
		OwnerUserId: conv.OwnerUserID,
		CreatedAt:   conv.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   conv.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		AccessLevel: mapAccessLevel(conv.AccessLevel),
	}
	if conv.ForkedAtEntryID != nil {
		c.ForkedAtEntryId = uuidToBytes(*conv.ForkedAtEntryID)
	}
	if conv.ForkedAtConversationID != nil {
		c.ForkedAtConversationId = uuidToBytes(*conv.ForkedAtConversationID)
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
	case pb.Channel_MEMORY:
		channel = model.ChannelMemory
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

	var epochFilter *registrystore.MemoryEpochFilter
	if channel == model.ChannelMemory {
		// Keep parity with REST list behavior: memory reads without a client id
		// degrade to history channel to avoid cross-agent memory visibility.
		if clientIDPtr == nil {
			channel = model.ChannelHistory
		}
	}
	if channel == model.ChannelMemory {
		filter, err := registrystore.ParseMemoryEpochFilter(req.GetEpochFilter())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		epochFilter = filter
	}

	allForks := req.GetForks() == "all"

	result, err := s.Store.GetEntries(ctx, userID, convID, afterCursor, limit, &channel, epochFilter, clientIDPtr, allForks)
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
	ch := "history"
	if entry.GetChannel() == pb.Channel_MEMORY {
		ch = "memory"
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
		_, _ = s.Store.CreateConversationWithID(ctx, userID, convID, "", nil, &forkedAtConvID, forkedAtEntryID)
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

	entries, err := s.Store.AppendEntries(ctx, userID, convID, []registrystore.CreateEntryRequest{{
		Content:     content,
		ContentType: entry.GetContentType(),
		Channel:     ch,
	}}, &clientID, nil)
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
	if entry.GetChannel() != pb.Channel_MEMORY {
		return nil, status.Error(codes.InvalidArgument, "sync entry must target memory channel")
	}
	var syncContent json.RawMessage
	if len(entry.GetContent()) > 0 {
		list, _ := structpb.NewList(nil)
		for _, v := range entry.GetContent() {
			list.Values = append(list.Values, v)
		}
		syncContent, _ = list.MarshalJSON()
	}

	result, err := s.Store.SyncAgentEntry(ctx, userID, convID, registrystore.CreateEntryRequest{
		Content:     syncContent,
		ContentType: entry.GetContentType(),
		Channel:     "memory",
	}, clientID)
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

	memberships, cursor, err := s.Store.ListMemberships(ctx, userID, convID, afterCursor, limit)
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
	m, err := s.Store.ShareConversation(ctx, userID, convID, req.GetUserId(), level)
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
	m, err := s.Store.UpdateMembership(ctx, userID, convID, req.GetMemberUserId(), level)
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

	if err := s.Store.DeleteMembership(ctx, userID, convID, req.GetMemberUserId()); err != nil {
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

	transfers, cursor, err := s.Store.ListPendingTransfers(ctx, userID, role, afterCursor, limit)
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

	t, err := s.Store.GetTransfer(ctx, userID, transferID)
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

	t, err := s.Store.CreateOwnershipTransfer(ctx, userID, convID, req.GetNewOwnerUserId())
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

	if err := s.Store.AcceptTransfer(ctx, userID, transferID); err != nil {
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

	if err := s.Store.DeleteTransfer(ctx, userID, transferID); err != nil {
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
		if strings.TrimSpace(u) == userID {
			return true // admin implies indexer
		}
	}
	for _, u := range strings.Split(s.Config.IndexerUsers, ",") {
		if strings.TrimSpace(u) == userID {
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

	results, err := s.Store.SearchEntries(ctx, userID, req.GetQuery(), limit, includeEntry)
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

	result, err := s.Store.IndexEntries(ctx, entries)
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

	entries, cursor, err := s.Store.ListUnindexedEntries(ctx, limit, afterCursor)
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

			attachment, err := s.Store.CreateAttachment(stream.Context(), userID, uuid.Nil, model.Attachment{
				Filename:    filename,
				ContentType: contentType,
				Size:        &out.res.Size,
				SHA256:      &out.res.SHA256,
				StorageKey:  &out.res.StorageKey,
				ExpiresAt:   &expiresAt,
				Status:      "ready",
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

	attachment, err := s.Store.GetAttachment(ctx, userID, uuid.Nil, attachmentID)
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
	attachment, err := s.Store.GetAttachment(stream.Context(), userID, uuid.Nil, attachmentID)
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
	Resumer *internalresumer.Store
	Store   registrystore.MemoryStore
	Config  *config.Config
	Enabled bool
}

func (s *ResponseRecorderServer) Record(stream pb.ResponseRecorderService_RecordServer) error {
	if !s.Enabled {
		return stream.SendAndClose(&pb.RecordResponse{
			Status:       pb.RecordStatus_RECORD_STATUS_ERROR,
			ErrorMessage: "response recorder disabled",
		})
	}
	var convID string
	var convUUID uuid.UUID
	var recorder *internalresumer.Recorder

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			if recorder != nil {
				if err := recorder.Complete(); err != nil {
					return status.Error(codes.Internal, err.Error())
				}
			}
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
				return status.Error(codes.Internal, err.Error())
			}
		}
		if convID == "" {
			return status.Error(codes.InvalidArgument, "conversation_id is required in first record chunk")
		}

		if recorder != nil && req.GetContent() != "" {
			if err := recorder.Record(req.GetContent()); err != nil {
				return status.Error(codes.Internal, err.Error())
			}
		}

		if req.GetComplete() && recorder != nil {
			if err := s.requireConversationAccess(stream.Context(), convUUID, model.AccessLevelWriter); err != nil {
				return err
			}
			if err := recorder.Complete(); err != nil {
				return status.Error(codes.Internal, err.Error())
			}
			return stream.SendAndClose(&pb.RecordResponse{
				Status: pb.RecordStatus_RECORD_STATUS_SUCCESS,
			})
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
	conv, err := s.Store.GetConversation(ctx, userID, conversationID)
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
