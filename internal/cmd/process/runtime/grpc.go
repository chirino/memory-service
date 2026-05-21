package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// GRPCAuth configures Memory Service gRPC request metadata.
type GRPCAuth struct {
	APIKey      string
	BearerToken string
	ClientID    string
}

// DialGRPC opens a Memory Service gRPC connection.
func DialGRPC(endpoint string) (*grpc.ClientConn, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	return grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// GRPCEventClient adapts EventStreamService to EventClient.
type GRPCEventClient struct {
	Client pb.EventStreamServiceClient
	Auth   GRPCAuth
}

// Subscribe opens a gRPC event stream.
func (c GRPCEventClient) Subscribe(ctx context.Context, req SubscribeRequest) (EventStream, error) {
	if c.Client == nil {
		return nil, errors.New("event stream client is required")
	}
	scope := pb.EventScope_EVENT_SCOPE_ADMIN
	if req.Scope == "user" {
		scope = pb.EventScope_EVENT_SCOPE_AUTHORIZED
	}
	stream, err := c.Client.SubscribeEvents(withAuth(ctx, c.Auth), &pb.SubscribeEventsRequest{
		Kinds:             req.Kinds,
		AfterCursor:       optionalString(req.AfterCursor),
		Detail:            optionalString(defaultString(req.Detail, "full")),
		Scope:             &scope,
		Justification:     optionalString(req.Justification),
		EntryChannels:     req.EntryChannels,
		EntryContentTypes: req.EntryContentTypes,
		EntryRoles:        req.EntryRoles,
	})
	if err != nil {
		return nil, err
	}
	return grpcEventStream{stream: stream}, nil
}

type grpcEventStream struct {
	stream grpc.ServerStreamingClient[pb.EventNotification]
}

func (s grpcEventStream) Recv() (EventEnvelope, error) {
	msg, err := s.stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return EventEnvelope{}, io.EOF
		}
		return EventEnvelope{}, err
	}
	return EventEnvelope{
		Event:  msg.GetEvent(),
		Kind:   msg.GetKind(),
		Data:   append(json.RawMessage(nil), msg.GetData()...),
		Cursor: msg.GetCursor(),
		Time:   time.Now().UTC(),
	}, nil
}

// GRPCCheckpointClient adapts AdminCheckpointService to CheckpointClient.
type GRPCCheckpointClient struct {
	Client pb.AdminCheckpointServiceClient
	Auth   GRPCAuth
}

// Get loads a checkpoint.
func (c GRPCCheckpointClient) Get(ctx context.Context, clientID string) (Checkpoint, error) {
	if c.Client == nil {
		return Checkpoint{}, errors.New("checkpoint client is required")
	}
	resp, err := c.Client.GetCheckpoint(withAuth(ctx, c.Auth), &pb.GetCheckpointRequest{ClientId: clientID})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return Checkpoint{}, ErrCheckpointNotFound
		}
		return Checkpoint{}, err
	}
	return checkpointFromProto(resp)
}

// Put stores a checkpoint.
func (c GRPCCheckpointClient) Put(ctx context.Context, clientID, contentType string, value json.RawMessage) (Checkpoint, error) {
	if c.Client == nil {
		return Checkpoint{}, errors.New("checkpoint client is required")
	}
	var decoded any
	if len(value) > 0 {
		if err := json.Unmarshal(value, &decoded); err != nil {
			return Checkpoint{}, err
		}
	}
	pValue, err := structpb.NewValue(decoded)
	if err != nil {
		return Checkpoint{}, err
	}
	resp, err := c.Client.PutCheckpoint(withAuth(ctx, c.Auth), &pb.PutCheckpointRequest{
		ClientId:    clientID,
		ContentType: contentType,
		Value:       pValue,
	})
	if err != nil {
		return Checkpoint{}, err
	}
	return checkpointFromProto(resp)
}

func checkpointFromProto(resp *pb.AdminCheckpoint) (Checkpoint, error) {
	if resp == nil {
		return Checkpoint{}, ErrCheckpointNotFound
	}
	raw, err := json.Marshal(resp.GetValue().AsInterface())
	if err != nil {
		return Checkpoint{}, fmt.Errorf("marshal checkpoint value: %w", err)
	}
	var updatedAt time.Time
	if resp.GetUpdatedAt() != nil {
		updatedAt = resp.GetUpdatedAt().AsTime().UTC()
	}
	return Checkpoint{
		ClientID:    resp.GetClientId(),
		ContentType: resp.GetContentType(),
		Value:       raw,
		UpdatedAt:   updatedAt,
	}, nil
}

func withAuth(ctx context.Context, auth GRPCAuth) context.Context {
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

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
