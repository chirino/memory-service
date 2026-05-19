package turntraces

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	"github.com/stretchr/testify/require"
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
}

func (c *blockingEventClient) Subscribe(ctx context.Context, _ processruntime.SubscribeRequest) (processruntime.EventStream, error) {
	c.mu.Lock()
	c.subscribed = true
	c.mu.Unlock()
	return blockingStream{ctx: ctx}, nil
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
	return failingStream{err: c.err}, nil
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
