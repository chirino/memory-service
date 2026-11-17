package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type unsupportedMembershipSource struct{ err error }

func (s unsupportedMembershipSource) GetCapabilities(context.Context, *emptypb.Empty, ...grpc.CallOption) (*pb.CapabilitiesResponse, error) {
	return &pb.CapabilitiesResponse{}, s.err
}

type unusedCapabilityCheckpoint struct{ t *testing.T }

func (s unusedCapabilityCheckpoint) Get(context.Context, string) (processruntime.Checkpoint, error) {
	s.t.Fatal("unsupported membership source reached checkpoint bootstrap")
	return processruntime.Checkpoint{}, nil
}
func (s unusedCapabilityCheckpoint) Put(context.Context, string, string, json.RawMessage) (processruntime.Checkpoint, error) {
	s.t.Fatal("unsupported membership source wrote a checkpoint")
	return processruntime.Checkpoint{}, nil
}
func TestMembershipCapabilityFailsBeforeBootstrap(t *testing.T) {
	for _, err := range []error{nil, errors.New("source unavailable")} {
		cfg := testConfig()
		cfg.ExportMemberships = true
		running, actual := StartProcessor(context.Background(), StartOptions{ClientID: "test", Config: cfg, Sink: &fakeSink{}, Events: &markerEventClient{}, Checkpoints: unusedCapabilityCheckpoint{t}, Capabilities: unsupportedMembershipSource{err}})
		require.Nil(t, running)
		require.ErrorContains(t, actual, "analytics_membership_snapshots")
	}
}

// Stop at the incompatible checkpoint after the capability check succeeds.
// This isolates startup authentication without opening ClickHouse.
func TestMembershipCapabilityUsesRenewableAuthentication(t *testing.T) {
	var grants atomic.Int32
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprint(grants.Add(1)), "expires_in": 300, "token_type": "Bearer"})
	}))
	defer tokens.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var calls atomic.Int32
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		require.Equal(t, []string{"test"}, md.Get("x-client-id"))
		require.Equal(t, []string{"Bearer " + fmt.Sprint(grants.Load())}, md.Get("authorization"))
		if calls.Add(1) == 1 {
			return nil, status.Error(codes.Unauthenticated, "rejected")
		}
		return &pb.CapabilitiesResponse{Features: &pb.CapabilitiesFeatures{AnalyticsMembershipSnapshots: true}}, nil
	}))
	pb.RegisterSystemServiceServer(server, &pb.UnimplementedSystemServiceServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	cfg := testConfig()
	cfg.ExportMemberships = true
	_, err = StartProcessor(t.Context(), StartOptions{
		Endpoint: listener.Addr().String(), ClientID: "test", Config: cfg,
		OIDC:   processruntime.OIDCConfig{TokenURL: tokens.URL, ClientSecret: "secret"},
		Events: &markerEventClient{}, Checkpoints: storedCheckpoint{processruntime.Checkpoint{ContentType: "old-exporter"}},
	})
	require.ErrorContains(t, err, "use a new client ID")
	require.EqualValues(t, 2, grants.Load())
	require.EqualValues(t, 2, calls.Load())
}
