package turntraces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

type grpcAdminContextFetcher struct {
	client pb.AdminEntriesServiceClient
	auth   processruntime.GRPCAuth
}

func (f grpcAdminContextFetcher) FetchContextEntries(ctx context.Context, conversationID string, upToEntryID string) ([]ContextEntryData, error) {
	if f.client == nil {
		return nil, errors.New("admin entries client is required")
	}
	conversationBytes, err := uuidStringBytes(conversationID)
	if err != nil {
		return nil, fmt.Errorf("parse conversation ID: %w", err)
	}
	upToBytes, err := uuidStringBytes(upToEntryID)
	if err != nil {
		return nil, fmt.Errorf("parse upper-bound entry ID: %w", err)
	}

	var out []ContextEntryData
	pageToken := ""
	for {
		resp, err := f.client.ListEntries(authContext(ctx, f.auth), &pb.AdminListEntriesRequest{
			ConversationId: conversationBytes,
			Channel:        pb.Channel_CONTEXT,
			EpochFilter:    "all",
			UpToEntryId:    upToBytes,
			Page: &pb.PageRequest{
				PageToken: pageToken,
				PageSize:  1000,
			},
		})
		if err != nil {
			return nil, err
		}
		for _, entry := range resp.GetEntries() {
			data, err := contextEntryFromProto(entry)
			if err != nil {
				return nil, err
			}
			out = append(out, data)
		}
		pageToken = resp.GetPageInfo().GetNextPageToken()
		if pageToken == "" {
			break
		}
	}
	log.Debug("bounded context entries listed",
		"conversationID", conversationID,
		"upToEntryID", upToEntryID,
		"contextEntries", len(out),
	)
	return out, nil
}

func contextEntryFromProto(entry *pb.Entry) (ContextEntryData, error) {
	if entry == nil {
		return ContextEntryData{}, errors.New("entry is nil")
	}
	id, err := uuidFromBytes(entry.GetId())
	if err != nil {
		return ContextEntryData{}, fmt.Errorf("parse entry ID: %w", err)
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, entry.GetCreatedAt())
	rawContent := make([]json.RawMessage, 0, len(entry.GetContent()))
	for _, value := range entry.GetContent() {
		raw, err := protojson.Marshal(value)
		if err != nil {
			return ContextEntryData{}, fmt.Errorf("marshal context entry content: %w", err)
		}
		rawContent = append(rawContent, json.RawMessage(raw))
	}
	event := entryEvent{
		ID:          id,
		Channel:     "context",
		ContentType: entry.GetContentType(),
		Epoch:       ptrInt64(entry.GetEpoch()),
		Content:     rawContent,
		CreatedAt:   createdAt,
	}
	return ContextEntryData{
		ID:          id,
		ContentType: entry.GetContentType(),
		Epoch:       entry.GetEpoch(),
		Text:        entryText(event),
		Messages:    contextMessages(event),
		CreatedAt:   createdAt,
	}, nil
}

func uuidStringBytes(value string) ([]byte, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), id[:]...), nil
}

func uuidFromBytes(value []byte) (string, error) {
	id, err := uuid.FromBytes(value)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func ptrInt64(value int64) *int64 {
	return &value
}

func authContext(ctx context.Context, auth processruntime.GRPCAuth) context.Context {
	pairs := make([]string, 0, 6)
	if auth.BearerToken != "" {
		pairs = append(pairs, "authorization", "Bearer "+auth.BearerToken)
	}
	if auth.APIKey != "" {
		pairs = append(pairs, "x-api-key", auth.APIKey)
	}
	if auth.ClientID != "" {
		pairs = append(pairs, "x-client-id", auth.ClientID)
	}
	if len(pairs) == 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}
