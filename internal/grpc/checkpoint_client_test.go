package grpc

import (
	"context"
	"testing"

	"github.com/chirino/memory-service/internal/config"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/stretchr/testify/require"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type checkpointFakeStore struct {
	registrystore.MemoryStore
	gets []string
}

func (s *checkpointFakeStore) InReadTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s *checkpointFakeStore) AdminGetCheckpoint(_ context.Context, clientID string) (*registrystore.ClientCheckpoint, error) {
	s.gets = append(s.gets, clientID)
	return &registrystore.ClientCheckpoint{ClientID: clientID, ContentType: "application/json", Value: []byte(`{}`)}, nil
}

func (s *checkpointFakeStore) AdminPutCheckpoint(context.Context, registrystore.ClientCheckpoint) (*registrystore.ClientCheckpoint, error) {
	return nil, nil
}

// apiKeyAdminContext resolves an admin identity for client-a through the real gRPC
// auth interceptor.
func apiKeyAdminContext(t *testing.T) context.Context {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.APIKeys = map[string]string{"key-a": "client-a"}
	cfg.AdminClients = "client-a"
	resolver, err := security.NewTokenResolver(&cfg)
	require.NoError(t, err)

	incoming := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "key-a"))
	var resolved context.Context
	_, err = security.GRPCUnaryInterceptorWithRateLimiter(resolver, nil)(incoming, nil, &grpclib.UnaryServerInfo{},
		func(ctx context.Context, _ any) (any, error) {
			resolved = ctx
			return nil, nil
		})
	require.NoError(t, err)
	require.Equal(t, "client-a", getClientID(resolved))
	return resolved
}

func TestGRPCAllowCheckpointClient(t *testing.T) {
	require.True(t, grpcAllowCheckpointClient(context.Background(), "any-client"))

	ctx := apiKeyAdminContext(t)
	require.True(t, grpcAllowCheckpointClient(ctx, "client-a"))
	require.False(t, grpcAllowCheckpointClient(ctx, "client-b"))
}

// An authenticated client may only read its own checkpoint; other client IDs look
// absent (NotFound) rather than forbidden and never reach the store.
func TestAdminGetCheckpointHidesOtherClientCheckpoints(t *testing.T) {
	ctx := apiKeyAdminContext(t)
	store := &checkpointFakeStore{}
	server := &AdminCheckpointServer{Store: store}

	_, err := server.GetCheckpoint(ctx, &pb.GetCheckpointRequest{ClientId: "client-b"})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Empty(t, store.gets)

	checkpoint, err := server.GetCheckpoint(ctx, &pb.GetCheckpointRequest{ClientId: "client-a"})
	require.NoError(t, err)
	require.Equal(t, "client-a", checkpoint.GetClientId())
	require.Equal(t, []string{"client-a"}, store.gets)
}
