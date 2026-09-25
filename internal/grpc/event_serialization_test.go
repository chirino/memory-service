package grpc

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/model"
	registryeventbus "github.com/chirino/memory-service/internal/registry/eventbus"
	"github.com/chirino/memory-service/internal/service/eventstream"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	googlegrpc "google.golang.org/grpc"
)

func TestSendGRPCEventDoesNotExpandEscapableEntryPastProcessorLimit(t *testing.T) {
	event := receiveSerializedEntry(t, strings.Repeat("<", 6<<20), "pg:escaped")
	require.Equal(t, "pg:escaped", event.Cursor)
	require.Less(t, len(event.Data), 8<<20)

	var resource map[string]any
	require.NoError(t, json.Unmarshal(event.Data, &resource))
	content, ok := resource["content"].(map[string]any)
	require.True(t, ok)
	text, ok := content["text"].(string)
	require.True(t, ok)
	require.Len(t, text, 6<<20)
}

func TestSendGRPCEventReceivesMaximumUnicodeEscaping(t *testing.T) {
	content := strings.Repeat("\u2028", 6<<20) // 18 MiB of UTF-8 input; 36 MiB after JSON escaping.
	event := receiveSerializedEntry(t, content, "pg:unicode")
	require.Equal(t, "pg:unicode", event.Cursor)

	var resource map[string]any
	require.NoError(t, json.Unmarshal(event.Data, &resource))
	entryContent, ok := resource["content"].(map[string]any)
	require.True(t, ok)
	text, ok := entryContent["text"].(string)
	require.True(t, ok)
	require.Equal(t, content, text)
}

func receiveSerializedEntry(t *testing.T, content, cursor string) processruntime.EventEnvelope {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := googlegrpc.NewServer()
	pb.RegisterEventStreamServiceServer(server, escapableEntryEventServer{content: content, cursor: cursor})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := processruntime.DialGRPCWithConfig(listener.Addr().String(), processruntime.GRPCDialConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := processruntime.GRPCEventClient{Client: pb.NewEventStreamServiceClient(conn)}
	stream, err := client.Subscribe(ctx, processruntime.SubscribeRequest{Detail: "full", Scope: "admin"})
	require.NoError(t, err)
	event, err := stream.Recv()
	require.NoError(t, err)
	return event
}

type escapableEntryEventServer struct {
	pb.UnimplementedEventStreamServiceServer
	content string
	cursor  string
}

func (s escapableEntryEventServer) SubscribeEvents(_ *pb.SubscribeEventsRequest, stream pb.EventStreamService_SubscribeEventsServer) error {
	entry := model.Entry{
		ID: uuid.New(), ConversationID: "conversation-1", ConversationGroupID: uuid.New(),
		Channel: model.ChannelHistory, ContentType: "history",
		Content:   json.RawMessage(`{"text":"` + s.content + `"}`),
		CreatedAt: time.Unix(1, 0).UTC(),
	}
	return sendGRPCEvent(stream, registryeventbus.Event{
		Event: "created", Kind: "entry", Data: eventstream.AdminEntryResource(&entry), OutboxCursor: s.cursor,
	})
}
