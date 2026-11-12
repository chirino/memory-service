package runtime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// The service accepts resources up to 20 MiB by default. JSON serialization
// can double valid U+2028/U+2029-heavy strings, so reserve another 8 MiB for
// the resource envelope and protobuf framing.
const analyticsGRPCMaxReceiveBytes = 48 << 20

// DialGRPC opens a Memory Service gRPC connection.
func DialGRPC(endpoint string, auth ...*GRPCAuth) (*grpc.ClientConn, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	options := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(analyticsGRPCMaxReceiveBytes))}
	if len(auth) > 0 {
		options = append(options, auth[0].dialOptions()...)
	}
	return grpc.NewClient(endpoint, options...)
}

// GRPCDialConfig applies the processor transport policy. Remote plaintext is
// rejected unless the caller explicitly marks the connection as development-only.
type GRPCDialConfig struct {
	Auth          *GRPCAuth
	TLS           bool
	CAFile        string
	AllowInsecure bool
	// MaxReceiveBytes raises the default receive allowance when a processor
	// accepts individual records larger than the service default.
	MaxReceiveBytes int
}

func DialGRPCWithConfig(endpoint string, cfg GRPCDialConfig) (*grpc.ClientConn, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid gRPC endpoint %q: %w", endpoint, err)
	}
	if !cfg.TLS {
		ip := net.ParseIP(strings.Trim(host, "[]"))
		loopback := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
		if !loopback && !cfg.AllowInsecure {
			return nil, errors.New("gRPC TLS is required for non-loopback endpoints; use --allow-insecure-grpc only for development")
		}
		options := append(cfg.Auth.dialOptions(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(max(analyticsGRPCMaxReceiveBytes, cfg.MaxReceiveBytes))))
		return grpc.NewClient(endpoint, options...)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: strings.Trim(host, "[]")}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read gRPC CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("gRPC CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	options := append(cfg.Auth.dialOptions(), grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(max(analyticsGRPCMaxReceiveBytes, cfg.MaxReceiveBytes))))
	return grpc.NewClient(endpoint, options...)
}

// GRPCEventClient adapts EventStreamService to EventClient.
type GRPCEventClient struct {
	Client pb.EventStreamServiceClient
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
	stream, err := c.Client.SubscribeEvents(ctx, &pb.SubscribeEventsRequest{
		Kinds:             req.Kinds,
		AfterCursor:       optionalString(req.AfterCursor),
		Detail:            optionalString(defaultString(req.Detail, "full")),
		Scope:             &scope,
		Justification:     optionalString(req.Justification),
		EntryChannels:     req.EntryChannels,
		EntryContentTypes: req.EntryContentTypes,
		EntryRoles:        req.EntryRoles,
		InitialState:      optionalString(req.InitialState),
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
		Change: msg.GetChange(),
		Data:   append(json.RawMessage(nil), msg.GetData()...),
		Cursor: msg.GetCursor(),
		Time:   grpcEventTime(msg),
	}, nil
}

func grpcEventTime(msg *pb.EventNotification) time.Time {
	if msg.GetOccurredAt() != nil {
		return msg.GetOccurredAt().AsTime().UTC()
	}
	return time.Now().UTC()
}

// GRPCCheckpointClient adapts AdminCheckpointService to CheckpointClient.
type GRPCCheckpointClient struct {
	Client pb.AdminCheckpointServiceClient
}

// Get loads a checkpoint.
func (c GRPCCheckpointClient) Get(ctx context.Context, clientID string) (Checkpoint, error) {
	if c.Client == nil {
		return Checkpoint{}, errors.New("checkpoint client is required")
	}
	resp, err := c.Client.GetCheckpoint(ctx, &pb.GetCheckpointRequest{ClientId: clientID})
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
	return c.put(ctx, clientID, contentType, value, "", "")
}

func (c GRPCCheckpointClient) PutCAS(ctx context.Context, clientID, contentType string, value json.RawMessage, expectedRevision, leaseToken string) (Checkpoint, error) {
	return c.put(ctx, clientID, contentType, value, expectedRevision, leaseToken)
}

func (c GRPCCheckpointClient) put(ctx context.Context, clientID, contentType string, value json.RawMessage, expectedRevision, leaseToken string) (Checkpoint, error) {
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
	resp, err := c.Client.PutCheckpoint(ctx, &pb.PutCheckpointRequest{
		ClientId:         clientID,
		ContentType:      contentType,
		Value:            pValue,
		ExpectedRevision: optionalString(expectedRevision),
		LeaseToken:       optionalString(leaseToken),
	})
	if err != nil {
		return Checkpoint{}, err
	}
	return checkpointFromProto(resp)
}

func (c GRPCCheckpointClient) AcquireLease(ctx context.Context, clientID string, ttl time.Duration) (CheckpointLease, error) {
	if c.Client == nil {
		return CheckpointLease{}, errors.New("checkpoint client is required")
	}
	resp, err := c.Client.AcquireLease(ctx, &pb.AcquireCheckpointLeaseRequest{ClientId: clientID, TtlSeconds: uint32(ttl / time.Second)})
	if err != nil {
		return CheckpointLease{}, err
	}
	return checkpointLeaseFromProto(resp)
}

func (c GRPCCheckpointClient) RenewLease(ctx context.Context, clientID, leaseToken string, ttl time.Duration) (CheckpointLease, error) {
	if c.Client == nil {
		return CheckpointLease{}, errors.New("checkpoint client is required")
	}
	resp, err := c.Client.RenewLease(ctx, &pb.RenewCheckpointLeaseRequest{ClientId: clientID, LeaseToken: leaseToken, TtlSeconds: uint32(ttl / time.Second)})
	if err != nil {
		return CheckpointLease{}, err
	}
	return checkpointLeaseFromProto(resp)
}

func (c GRPCCheckpointClient) ReleaseLease(ctx context.Context, clientID, leaseToken string) error {
	if c.Client == nil {
		return errors.New("checkpoint client is required")
	}
	_, err := c.Client.ReleaseLease(ctx, &pb.ReleaseCheckpointLeaseRequest{ClientId: clientID, LeaseToken: leaseToken})
	return err
}

func checkpointLeaseFromProto(resp *pb.AdminCheckpointLease) (CheckpointLease, error) {
	if resp == nil || resp.GetExpiresAt() == nil || resp.GetLeaseToken() == "" {
		return CheckpointLease{}, errors.New("invalid checkpoint lease response")
	}
	return CheckpointLease{Token: resp.GetLeaseToken(), Generation: resp.GetGeneration(), ExpiresAt: resp.GetExpiresAt().AsTime().UTC()}, nil
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
		Revision:    resp.GetRevision(),
		UpdatedAt:   updatedAt,
	}, nil
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
