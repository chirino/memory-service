package runtime

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestDialGRPCWithConfigReceivesEventAboveDefaultLimit(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pb.RegisterEventStreamServiceServer(server, largeEventStreamServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := DialGRPCWithConfig(listener.Addr().String(), GRPCDialConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := GRPCEventClient{Client: pb.NewEventStreamServiceClient(conn)}
	stream, err := events.Subscribe(ctx, SubscribeRequest{Detail: "full", Scope: "admin"})
	require.NoError(t, err)
	response, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, response.Data, 20<<20)
	require.Equal(t, "pg:large", response.Cursor)
}

type largeEventStreamServer struct {
	pb.UnimplementedEventStreamServiceServer
}

func (largeEventStreamServer) SubscribeEvents(_ *pb.SubscribeEventsRequest, stream pb.EventStreamService_SubscribeEventsServer) error {
	cursor := "pg:large"
	return stream.Send(&pb.EventNotification{Event: "snapshot", Kind: "conversation", Cursor: &cursor, Data: []byte(strings.Repeat("x", 20<<20))})
}
