package turntraces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestStartProcessorWithInjectedClientsCanShutdown(t *testing.T) {
	ctx := context.Background()
	events := &blockingEventClient{}
	checkpoints := newMemoryCheckpointClient()

	running, err := StartProcessor(ctx, StartOptions{
		ClientID:           "turn-traces-test",
		Scope:              "user",
		CheckpointInterval: time.Hour,
		TurnTraces: Config{
			DryRun: true,
		},
		Events:      events,
		Checkpoints: checkpoints,
		Sink:        &captureSink{},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		events.mu.Lock()
		defer events.mu.Unlock()
		return events.subscribed
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"history", "context"}, events.subscribeRequest().EntryChannels)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, running.Shutdown(shutdownCtx))
}

func TestStartProcessorProcessesInjectedEventStream(t *testing.T) {
	sink := &captureSink{}
	checkpoints := newMemoryCheckpointClient()
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	events := staticEventClient{
		events: []processruntime.EventEnvelope{
			entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base),
			entryEnvelope("cursor-2", "ctx1", "conv-1", "context", "LC4J", "", base.Add(time.Second)),
			entryEnvelope("cursor-3", "a1", "conv-1", "history", "history", "AI", base.Add(2*time.Second)),
		},
	}

	running, err := StartProcessor(context.Background(), StartOptions{
		ClientID:    "turn-traces-test",
		Scope:       "user",
		Events:      events,
		Checkpoints: checkpoints,
		Sink:        sink,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(sink.snapshot()) == 1
	}, time.Second, 10*time.Millisecond)
	spans := sink.snapshot()
	require.Equal(t, "agent_history_entry", spans[0].EndReason)
	require.Equal(t, 1, spans[0].ContextCount)

	var state checkpointState
	require.NoError(t, json.Unmarshal(checkpoints.current(), &state))
	require.Equal(t, "cursor-3", state.LastEventCursor)
	require.Empty(t, state.OpenTurns)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, running.Shutdown(shutdownCtx))
}

func TestStartProcessorSkipsUnchangedCheckpointWrites(t *testing.T) {
	sink := &captureSink{}
	checkpoints := newMemoryCheckpointClient()
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	events := staticEventClient{
		events: []processruntime.EventEnvelope{
			entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base),
		},
	}

	running, err := StartProcessor(context.Background(), StartOptions{
		ClientID:           "turn-traces-test",
		Scope:              "user",
		Events:             events,
		Checkpoints:        checkpoints,
		Sink:               sink,
		CheckpointInterval: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return checkpoints.putCount() == 1
	}, time.Second, 10*time.Millisecond)

	time.Sleep(75 * time.Millisecond)
	require.Equal(t, 1, checkpoints.putCount())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, running.Shutdown(shutdownCtx))
	require.Equal(t, 1, checkpoints.putCount())
}

func TestStartProcessorExportFailureDoesNotAdvanceCheckpointPastClosingEvent(t *testing.T) {
	checkpoints := newMemoryCheckpointClient()
	base := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	events := staticEventClient{
		events: []processruntime.EventEnvelope{
			entryEnvelope("cursor-1", "u1", "conv-1", "history", "history", "USER", base),
			entryEnvelope("cursor-2", "a1", "conv-1", "history", "history", "AI", base.Add(time.Second)),
		},
	}

	running, err := StartProcessor(context.Background(), StartOptions{
		ClientID:    "turn-traces-test",
		Scope:       "user",
		Events:      events,
		Checkpoints: checkpoints,
		Sink:        &captureSink{err: errors.New("export failed")},
	})
	require.NoError(t, err)
	require.ErrorContains(t, running.Wait(), "export failed")

	var state checkpointState
	require.NoError(t, json.Unmarshal(checkpoints.current(), &state))
	require.Equal(t, "cursor-1", state.LastEventCursor)
	require.Contains(t, state.OpenTurns, "conv-1")
}

func TestStartProcessorValidatesReusableOptions(t *testing.T) {
	_, err := StartProcessor(context.Background(), StartOptions{
		ClientID: "turn-traces-test",
		Scope:    "invalid",
		Sink:     &captureSink{},
	})
	require.ErrorContains(t, err, "scope must be one of")

	_, err = StartProcessor(context.Background(), StartOptions{
		ClientID: "turn-traces-test",
		TurnTraces: Config{
			SessionIDMode: "invalid",
		},
		Sink: &captureSink{},
	})
	require.ErrorContains(t, err, "langfuse session ID mode")
}

func TestRunningProcessorWaitReturnsRuntimeError(t *testing.T) {
	checkpoints := newMemoryCheckpointClient()
	expected := errors.New("stream failed")
	running, err := StartProcessor(context.Background(), StartOptions{
		ClientID: "turn-traces-test",
		Events: failingEventClient{
			err: expected,
		},
		Checkpoints: checkpoints,
		Sink:        &captureSink{},
	})
	require.NoError(t, err)
	require.ErrorIs(t, running.Wait(), expected)
}

type staticEventClient struct {
	events []processruntime.EventEnvelope
}

func (c staticEventClient) Subscribe(context.Context, processruntime.SubscribeRequest) (processruntime.EventStream, error) {
	return &staticStream{events: append([]processruntime.EventEnvelope(nil), c.events...)}, nil
}

type staticStream struct {
	events []processruntime.EventEnvelope
}

func (s *staticStream) Recv() (processruntime.EventEnvelope, error) {
	if len(s.events) == 0 {
		time.Sleep(10 * time.Millisecond)
		return processruntime.EventEnvelope{}, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

type blockingEventClient struct {
	mu         sync.Mutex
	subscribed bool
	req        processruntime.SubscribeRequest
}

func (c *blockingEventClient) Subscribe(ctx context.Context, req processruntime.SubscribeRequest) (processruntime.EventStream, error) {
	c.mu.Lock()
	c.subscribed = true
	c.req = req
	c.mu.Unlock()
	return blockingStream{ctx: ctx}, nil
}

func (c *blockingEventClient) subscribeRequest() processruntime.SubscribeRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.req
}

type blockingStream struct {
	ctx context.Context
}

func (s blockingStream) Recv() (processruntime.EventEnvelope, error) {
	<-s.ctx.Done()
	return processruntime.EventEnvelope{}, io.EOF
}

type failingEventClient struct {
	err error
}

func (c failingEventClient) Subscribe(context.Context, processruntime.SubscribeRequest) (processruntime.EventStream, error) {
	return failingStream(c), nil
}

type failingStream struct {
	err error
}

func (s failingStream) Recv() (processruntime.EventEnvelope, error) {
	return processruntime.EventEnvelope{}, s.err
}

type memoryCheckpointClient struct {
	mu    sync.Mutex
	value json.RawMessage
	puts  int
}

func newMemoryCheckpointClient() *memoryCheckpointClient {
	return &memoryCheckpointClient{}
}

func (c *memoryCheckpointClient) Get(context.Context, string) (processruntime.Checkpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.value) == 0 {
		return processruntime.Checkpoint{}, processruntime.ErrCheckpointNotFound
	}
	return processruntime.Checkpoint{
		ContentType: contentType,
		Value:       append(json.RawMessage(nil), c.value...),
	}, nil
}

func (c *memoryCheckpointClient) Put(_ context.Context, clientID, contentType string, value json.RawMessage) (processruntime.Checkpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = append(json.RawMessage(nil), value...)
	c.puts++
	return processruntime.Checkpoint{
		ClientID:    clientID,
		ContentType: contentType,
		Value:       append(json.RawMessage(nil), value...),
	}, nil
}

func (c *memoryCheckpointClient) current() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(json.RawMessage(nil), c.value...)
}

func (c *memoryCheckpointClient) putCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.puts
}

func TestProcessorRunsAcrossOIDCTokenLifetimes(t *testing.T) {
	var issued, requests atomic.Int32
	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		if request == 2 || request == 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprint(issued.Add(1)), "expires_in": 1, "token_type": "Bearer"})
	}))
	defer tokens.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		authorization := md.Get("authorization")
		require.Len(t, authorization, 1)
		token, err := strconv.Atoi(strings.TrimPrefix(authorization[0], "Bearer "))
		require.NoError(t, err)
		require.Greater(t, token, 0)
		require.LessOrEqual(t, token, int(issued.Load()))
		return next(srv, stream)
	}))
	pb.RegisterEventStreamServiceServer(server, emptyEventServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	running, err := StartProcessor(t.Context(), StartOptions{
		Endpoint: listener.Addr().String(), ClientID: "processor", Scope: "user",
		OIDC:        processruntime.OIDCConfig{TokenURL: tokens.URL, ClientSecret: "secret"},
		Checkpoints: newMemoryCheckpointClient(), Sink: &captureSink{},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, running.Shutdown(ctx))
	})
	require.Eventually(t, func() bool { return issued.Load() >= 4 }, 10*time.Second, 50*time.Millisecond)
}

type emptyEventServer struct {
	pb.UnimplementedEventStreamServiceServer
}

func (emptyEventServer) SubscribeEvents(*pb.SubscribeEventsRequest, pb.EventStreamService_SubscribeEventsServer) error {
	return nil
}

type shutdownFlushSink struct {
	started chan struct{}
	release chan struct{}
}

func (s shutdownFlushSink) EmitTurnSpan(ctx context.Context, _ SpanData) error {
	close(s.started)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type shutdownCheckpointServer struct {
	pb.UnimplementedAdminCheckpointServiceServer
	checkpoint *pb.AdminCheckpoint
	writes     chan string
}

func (s *shutdownCheckpointServer) GetCheckpoint(context.Context, *pb.GetCheckpointRequest) (*pb.AdminCheckpoint, error) {
	return s.checkpoint, nil
}

func (s *shutdownCheckpointServer) PutCheckpoint(ctx context.Context, req *pb.PutCheckpointRequest) (*pb.AdminCheckpoint, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.writes <- strings.Join(md.Get("authorization"), ",")
	return &pb.AdminCheckpoint{ClientId: req.ClientId, ContentType: req.ContentType, Value: req.Value}, nil
}

func TestShutdownRenewsOIDCDuringFinalFlush(t *testing.T) {
	for _, mode := range []string{"shutdown", "parent cancellation", "shutdown deadline"} {
		t.Run(mode, func(t *testing.T) {
			renewing, canceled, releaseGrant := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			connectionClosed := make(chan struct{}, 1)
			tokens := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"access_token":"old","expires_in":1,"token_type":"Bearer"}`)
					return
				}
				require.NoError(t, r.ParseForm())
				close(renewing)
				select {
				case <-releaseGrant:
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"access_token":"renewed","expires_in":300,"token_type":"Bearer"}`)
				case <-r.Context().Done():
					close(canceled)
				}
			}))
			tokens.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateClosed {
					select {
					case connectionClosed <- struct{}{}:
					default:
					}
				}
			}
			tokens.Start()
			defer tokens.Close()
			seed := NewProcessor(Config{}, &captureSink{})
			require.NoError(t, seed.Handle(t.Context(), entryEnvelope("cursor", "user", "conversation", "history", "history", "USER", time.Now().Add(-time.Minute))))
			raw, err := seed.Snapshot()
			require.NoError(t, err)
			var value any
			require.NoError(t, json.Unmarshal(raw, &value))
			protoValue, err := structpb.NewValue(value)
			require.NoError(t, err)
			checkpoints := &shutdownCheckpointServer{checkpoint: &pb.AdminCheckpoint{ContentType: contentType, Value: protoValue}, writes: make(chan string, 1)}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			server := grpc.NewServer()
			pb.RegisterAdminCheckpointServiceServer(server, checkpoints)
			go func() { _ = server.Serve(listener) }()
			defer server.Stop()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			flush := shutdownFlushSink{started: make(chan struct{}), release: make(chan struct{})}
			events := &blockingEventClient{}
			running, err := StartProcessor(ctx, StartOptions{
				ClientID: "processor", Endpoint: listener.Addr().String(), Scope: "user", Events: events, Sink: flush,
				CheckpointInterval: time.Hour, TurnTraces: Config{IdleTimeout: time.Millisecond},
				OIDC: processruntime.OIDCConfig{TokenURL: tokens.URL, ClientSecret: "secret"},
			})
			require.NoError(t, err)
			expiredBy := time.Now().Add(1200 * time.Millisecond)
			var releaseOnce sync.Once
			defer func() {
				releaseOnce.Do(func() { close(flush.release) })
				shutdownCtx, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				_ = running.Shutdown(shutdownCtx)
			}()
			select {
			case <-renewing:
			case <-time.After(2 * time.Second):
				t.Fatal("early token renewal did not start")
			}
			finished := make(chan error, 1)
			if mode == "parent cancellation" {
				cancel()
				go func() { finished <- running.Wait() }()
			} else {
				shutdownCtx, stop := context.WithTimeout(t.Context(), 3*time.Second)
				if mode == "shutdown deadline" {
					stop()
					shutdownCtx, stop = context.WithTimeout(t.Context(), 50*time.Millisecond)
				}
				defer stop()
				go func() { finished <- running.Shutdown(shutdownCtx) }()
			}
			select {
			case <-flush.started:
			case <-time.After(time.Second):
				t.Fatal("final flush did not start")
			}
			if mode == "shutdown deadline" {
				require.ErrorIs(t, <-finished, context.DeadlineExceeded)
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("shutdown timeout did not cancel token renewal")
				}
				releaseOnce.Do(func() { close(flush.release) })
				require.NoError(t, running.Wait())
				return
			}
			// Keep Flush blocked beyond the old token's actual expiry.
			timer := time.NewTimer(time.Until(expiredBy))
			defer timer.Stop()
			<-timer.C
			close(releaseGrant)
			releaseOnce.Do(func() { close(flush.release) })
			require.NoError(t, <-finished)
			// Completing runtime cleanup must also stop renewal and release its idle connection.
			select {
			case <-connectionClosed:
			case <-time.After(time.Second):
				t.Fatal("completed cleanup left the token connection open")
			}
			select {
			case token := <-checkpoints.writes:
				require.Equal(t, "Bearer renewed", token)
			default:
				t.Fatal("final checkpoint was not saved after token expiry")
			}
		})
	}
}
