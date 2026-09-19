package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeStopsWhenLeaseRenewalFails(t *testing.T) {
	want := errors.New("lease service unavailable")
	checkpoints := &failingRenewalCheckpoints{renewErr: want}
	processor := &leaseTestProcessor{}
	runner := Runtime{
		Events: eofEventClient{}, Checkpoints: checkpoints, Processor: processor,
		Config: Config{ClientID: "analytics", AfterCursor: "start", LeaseTTL: 30 * time.Millisecond, ReconnectMin: time.Second},
	}

	err := runner.Run(context.Background())
	require.ErrorIs(t, err, want)
	require.Equal(t, uint64(7), processor.generation)
	require.True(t, checkpoints.released)
}

func TestRuntimeCancelsInflightHandleWhenLeaseRenewalFails(t *testing.T) {
	want := errors.New("lease service unavailable")
	checkpoints := &failingRenewalCheckpoints{renewErr: want}
	processor := &blockingLeaseProcessor{}
	runner := Runtime{
		Events: singleEventClient{}, Checkpoints: checkpoints, Processor: processor,
		Config: Config{ClientID: "analytics", AfterCursor: "start", LeaseTTL: 90 * time.Millisecond},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	err := runner.Run(ctx)
	require.ErrorIs(t, err, want)
	require.Less(t, time.Since(startedAt), time.Second)
	require.ErrorIs(t, processor.handleErr, context.Canceled)
	require.False(t, processor.mutated, "a handler must not mutate its sink after lease loss")
}

func TestConsumeReturnsLeaseErrorWhenCanceledReceiveWins(t *testing.T) {
	want := errors.New("lease renewal failed")
	for range 100 {
		leaseErrors := make(chan error, 1)
		workCtx := leaseErrorOnErrContext{leaseErrors: leaseErrors, leaseErr: want}
		ticker := time.NewTicker(time.Hour)
		afterCursor := "start"
		runner := Runtime{Processor: &leaseTestProcessor{}, Config: Config{ClientID: "analytics"}}

		err := runner.consume(context.Background(), workCtx, canceledEventStream{}, ticker, leaseErrors, &afterCursor)
		ticker.Stop()
		require.ErrorIs(t, err, want)
	}
}

func TestRuntimeStopsAfterStreamInvalidation(t *testing.T) {
	processor := &invalidationTestProcessor{cursor: "durable:stale"}
	events := &invalidationEventClient{}
	runner := Runtime{Events: events, Checkpoints: &memoryCheckpointClient{}, Processor: processor, Config: Config{ClientID: "analytics", AfterCursor: "start", ReconnectMin: time.Millisecond}}
	err := runner.Run(context.Background())
	require.ErrorContains(t, err, "backfill_required")
	require.Equal(t, 1, events.calls)
	require.Equal(t, 1, processor.bootstraps)
}

func TestRuntimeStopsAndBecomesUnreadyOnProcessorBackgroundError(t *testing.T) {
	processor := &backgroundErrorProcessor{errors: make(chan error, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ready := make(chan bool, 4)
	runner := Runtime{
		Events:      blockingEventClient{},
		Checkpoints: &memoryCheckpointClient{},
		Processor:   processor,
		Config:      Config{ClientID: "analytics", OnReady: func(value bool) { ready <- value }},
	}
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	require.False(t, <-ready)
	require.True(t, <-ready)
	want := errors.New("retention ownership lost")
	processor.errors <- want
	require.ErrorIs(t, <-done, want)
	require.False(t, <-ready)
}

type backgroundErrorProcessor struct {
	leaseTestProcessor
	errors chan error
}

func (p *backgroundErrorProcessor) BackgroundErrors() <-chan error { return p.errors }

type blockingEventClient struct{}

func (blockingEventClient) Subscribe(ctx context.Context, _ SubscribeRequest) (EventStream, error) {
	return contextEventStream{ctx: ctx}, nil
}

type invalidationTestProcessor struct {
	bootstraps int
	cursor     string
}

func (*invalidationTestProcessor) ContentType() string { return "application/test+json" }
func (p *invalidationTestProcessor) Load(raw json.RawMessage) error {
	if len(raw) > 0 {
		var v map[string]string
		_ = json.Unmarshal(raw, &v)
		p.cursor = v["lastEventCursor"]
	}
	return nil
}
func (*invalidationTestProcessor) Handle(context.Context, EventEnvelope) error {
	return errors.New("backfill_required: durable event history is incomplete")
}
func (p *invalidationTestProcessor) Snapshot() (json.RawMessage, error) {
	return json.Marshal(map[string]string{"lastEventCursor": p.cursor})
}
func (*invalidationTestProcessor) Flush(context.Context) error { return nil }
func (p *invalidationTestProcessor) Bootstrap(context.Context, EventClient, func(context.Context) error) (string, error) {
	p.bootstraps++
	p.cursor = fmt.Sprintf("bootstrap-%d", p.bootstraps)
	return p.cursor, nil
}

type memoryCheckpointClient struct{ checkpoint Checkpoint }

func (c *memoryCheckpointClient) Get(context.Context, string) (Checkpoint, error) {
	return c.checkpoint, nil
}
func (c *memoryCheckpointClient) Put(_ context.Context, id, content string, value json.RawMessage) (Checkpoint, error) {
	c.checkpoint = Checkpoint{ClientID: id, ContentType: content, Value: value, Revision: "r"}
	return c.checkpoint, nil
}

type invalidationEventClient struct {
	calls int
}

func (c *invalidationEventClient) Subscribe(ctx context.Context, req SubscribeRequest) (EventStream, error) {
	c.calls++
	return &singleEnvelopeStream{event: EventEnvelope{Kind: "stream", Event: "invalidate"}}, nil
}

type singleEnvelopeStream struct {
	sent  bool
	event EventEnvelope
}

func (s *singleEnvelopeStream) Recv() (EventEnvelope, error) {
	if s.sent {
		return EventEnvelope{}, io.EOF
	}
	s.sent = true
	return s.event, nil
}

type contextEventStream struct{ ctx context.Context }

func (s contextEventStream) Recv() (EventEnvelope, error) {
	<-s.ctx.Done()
	return EventEnvelope{}, s.ctx.Err()
}

type leaseTestProcessor struct{ generation uint64 }

func (p *leaseTestProcessor) ContentType() string                         { return "application/test+json" }
func (p *leaseTestProcessor) Load(json.RawMessage) error                  { return nil }
func (p *leaseTestProcessor) Handle(context.Context, EventEnvelope) error { return nil }
func (p *leaseTestProcessor) Snapshot() (json.RawMessage, error) {
	return json.RawMessage(`{"lastEventCursor":"start"}`), nil
}
func (p *leaseTestProcessor) Flush(context.Context) error           { return nil }
func (p *leaseTestProcessor) SetLeaseGeneration(value uint64) error { p.generation = value; return nil }

type blockingLeaseProcessor struct {
	handleErr error
	mutated   bool
}

func (*blockingLeaseProcessor) ContentType() string        { return "application/test+json" }
func (*blockingLeaseProcessor) Load(json.RawMessage) error { return nil }
func (p *blockingLeaseProcessor) Handle(ctx context.Context, _ EventEnvelope) error {
	<-ctx.Done()
	p.handleErr = ctx.Err()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		p.mutated = true
	}
	return ctx.Err()
}
func (*blockingLeaseProcessor) Snapshot() (json.RawMessage, error) {
	return json.RawMessage(`{"lastEventCursor":"start"}`), nil
}
func (*blockingLeaseProcessor) Flush(context.Context) error     { return nil }
func (*blockingLeaseProcessor) SetLeaseGeneration(uint64) error { return nil }

type singleEventClient struct{}

func (singleEventClient) Subscribe(context.Context, SubscribeRequest) (EventStream, error) {
	return &singleEventStream{}, nil
}

type singleEventStream struct{ sent bool }

func (s *singleEventStream) Recv() (EventEnvelope, error) {
	if s.sent {
		return EventEnvelope{}, io.EOF
	}
	s.sent = true
	return EventEnvelope{Event: "deleted", Kind: "memory", Cursor: "cursor-1"}, nil
}

type eofEventClient struct{}

func (eofEventClient) Subscribe(context.Context, SubscribeRequest) (EventStream, error) {
	return eofEventStream{}, nil
}

type eofEventStream struct{}

func (eofEventStream) Recv() (EventEnvelope, error) { return EventEnvelope{}, io.EOF }

type canceledEventStream struct{}

func (canceledEventStream) Recv() (EventEnvelope, error) {
	return EventEnvelope{}, context.Canceled
}

type leaseErrorOnErrContext struct {
	leaseErrors chan<- error
	leaseErr    error
}

func (leaseErrorOnErrContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (leaseErrorOnErrContext) Done() <-chan struct{}       { return nil }
func (c leaseErrorOnErrContext) Err() error {
	select {
	case c.leaseErrors <- c.leaseErr:
	default:
	}
	return context.Canceled
}
func (leaseErrorOnErrContext) Value(any) any { return nil }

type failingRenewalCheckpoints struct {
	renewErr error
	released bool
}

func (c *failingRenewalCheckpoints) Get(context.Context, string) (Checkpoint, error) {
	return Checkpoint{ContentType: "application/test+json", Value: json.RawMessage(`{"lastEventCursor":"start"}`), Revision: "revision-1"}, nil
}

func (c *failingRenewalCheckpoints) Put(context.Context, string, string, json.RawMessage) (Checkpoint, error) {
	return Checkpoint{}, errors.New("unexpected Put")
}

func (c *failingRenewalCheckpoints) PutCAS(context.Context, string, string, json.RawMessage, string, string) (Checkpoint, error) {
	return Checkpoint{}, errors.New("unexpected PutCAS")
}

func (c *failingRenewalCheckpoints) AcquireLease(context.Context, string, time.Duration) (CheckpointLease, error) {
	return CheckpointLease{Token: "token", Generation: 7, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (c *failingRenewalCheckpoints) RenewLease(context.Context, string, string, time.Duration) (CheckpointLease, error) {
	return CheckpointLease{}, c.renewErr
}

func (c *failingRenewalCheckpoints) ReleaseLease(context.Context, string, string) error {
	c.released = true
	return nil
}
