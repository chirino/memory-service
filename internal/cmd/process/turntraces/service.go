package turntraces

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StartOptions configures the turn-trace processor lifecycle.
type StartOptions struct {
	Endpoint           string
	ClientID           string
	APIKey             string
	BearerToken        string
	Scope              string
	AfterCursor        string
	CheckpointInterval time.Duration
	TurnTraces         Config

	// Tests may inject clients/sinks to run without a networked service or OTLP exporter.
	Events         processruntime.EventClient
	Checkpoints    processruntime.CheckpointClient
	Sink           SpanSink
	ContextFetcher ContextFetcher
}

// RunningProcessor is a started turn-trace processor.
type RunningProcessor struct {
	cancel   context.CancelFunc
	done     chan struct{}
	conn     *grpc.ClientConn
	shutdown func(context.Context) error
	mu       sync.Mutex
	err      error
	once     sync.Once
}

// StartProcessor starts a checkpointed turn-trace processor.
func StartProcessor(ctx context.Context, opts StartOptions) (*RunningProcessor, error) {
	if opts.ClientID == "" {
		return nil, errors.New("client ID is required")
	}
	scope := strings.TrimSpace(opts.Scope)
	if scope == "" {
		scope = "admin"
	}
	if scope != "admin" && scope != "user" {
		return nil, fmt.Errorf("scope must be one of: admin, user")
	}
	cfg := opts.TurnTraces
	if cfg.SessionIDMode == "" {
		cfg.SessionIDMode = "conversation"
	}
	if cfg.SessionIDMode != "conversation" && cfg.SessionIDMode != "conversation-group" {
		return nil, fmt.Errorf("langfuse session ID mode must be one of: conversation, conversation-group")
	}
	var sink SpanSink = opts.Sink
	var shutdown func(context.Context) error
	if sink == nil {
		if cfg.DryRun {
			sink = dryRunSink{}
		} else {
			otel, err := newOTELSink(ctx, cfg)
			if err != nil {
				return nil, err
			}
			sink = otel
			shutdown = otel.Shutdown
		}
	}

	events := opts.Events
	checkpoints := opts.Checkpoints
	contextFetcher := opts.ContextFetcher
	var conn *grpc.ClientConn
	if events == nil || checkpoints == nil {
		var err error
		conn, err = processruntime.DialGRPC(opts.Endpoint)
		if err != nil {
			if shutdown != nil {
				_ = shutdown(context.Background())
			}
			return nil, err
		}
		auth := processruntime.GRPCAuth{
			APIKey:      opts.APIKey,
			BearerToken: opts.BearerToken,
			ClientID:    opts.ClientID,
		}
		if events == nil {
			events = processruntime.GRPCEventClient{
				Client: pb.NewEventStreamServiceClient(conn),
				Auth:   auth,
			}
		}
		if checkpoints == nil {
			checkpoints = processruntime.GRPCCheckpointClient{
				Client: pb.NewAdminCheckpointServiceClient(conn),
				Auth:   auth,
			}
		}
		if contextFetcher == nil && scope == "admin" {
			contextFetcher = grpcAdminContextFetcher{
				client: pb.NewAdminEntriesServiceClient(conn),
				auth:   auth,
			}
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	processor := NewProcessor(cfg, sink, contextFetcher)
	runner := processruntime.Runtime{
		Events:      events,
		Checkpoints: checkpoints,
		Processor:   processor,
		Config: processruntime.Config{
			ClientID:           opts.ClientID,
			Kinds:              []string{"entry", "conversation"},
			EntryChannels:      []string{"history", "context"},
			Scope:              scope,
			AfterCursor:        opts.AfterCursor,
			Justification:      "turn-traces processor deriving conversation turn telemetry from event stream",
			CheckpointInterval: opts.CheckpointInterval,
		},
	}

	running := &RunningProcessor{
		cancel:   cancel,
		done:     make(chan struct{}),
		conn:     conn,
		shutdown: shutdown,
	}
	go func() {
		err := runner.Run(runCtx)
		running.mu.Lock()
		running.err = err
		running.mu.Unlock()
		close(running.done)
	}()
	return running, nil
}

// Wait waits for the processor loop to exit.
func (p *RunningProcessor) Wait() error {
	if p == nil {
		return nil
	}
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Shutdown stops the processor and releases its resources.
func (p *RunningProcessor) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var shutdownErr error
	p.once.Do(func() {
		p.cancel()
		select {
		case <-p.done:
			p.mu.Lock()
			shutdownErr = p.err
			p.mu.Unlock()
		case <-ctx.Done():
			shutdownErr = ctx.Err()
		}
		if p.conn != nil {
			if err := p.conn.Close(); err != nil && shutdownErr == nil {
				shutdownErr = err
			}
		}
		if p.shutdown != nil {
			if err := p.shutdown(ctx); err != nil && shutdownErr == nil {
				shutdownErr = err
			}
		}
		if status.Code(shutdownErr) == codes.Canceled {
			shutdownErr = nil
		}
	})
	return shutdownErr
}
