package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
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
