package clickhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/operationevent"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
)

type StartOptions struct {
	Endpoint           string
	ClientID           string
	APIKey             string
	BearerToken        string
	AfterCursor        string
	CheckpointInterval time.Duration
	LeaseTTL           time.Duration
	HealthAddress      string
	GRPCTLS            bool
	GRPCCAFile         string
	AllowInsecureGRPC  bool
	Config             Config
	Events             processruntime.EventClient
	Checkpoints        processruntime.CheckpointClient
	Sink               Sink
}

type RunningProcessor struct {
	cancel context.CancelFunc
	done   chan struct{}
	conn   *grpc.ClientConn
	sink   Sink
	health *http.Server
	mu     sync.Mutex
	err    error
	once   sync.Once
}

func StartProcessor(ctx context.Context, opts StartOptions) (*RunningProcessor, error) {
	if opts.ClientID == "" {
		return nil, errors.New("client ID is required")
	}
	if opts.Config.ExporterID == "" {
		opts.Config.ExporterID = opts.ClientID
	}
	if opts.LeaseTTL == 0 {
		opts.LeaseTTL = 30 * time.Second
	}
	if opts.LeaseTTL < 15*time.Second || opts.LeaseTTL > 5*time.Minute {
		return nil, errors.New("lease TTL must be between 15 seconds and 5 minutes")
	}
	if err := opts.Config.Validate(); err != nil {
		return nil, err
	}
	sink := opts.Sink
	if sink == nil {
		opened, err := OpenSink(ctx, opts.Config)
		if err != nil {
			return nil, err
		}
		sink = opened
	}
	processor, err := NewProcessor(opts.Config, sink)
	if err != nil {
		_ = sink.Close()
		return nil, err
	}
	events, checkpoints := opts.Events, opts.Checkpoints
	var conn *grpc.ClientConn
	if events == nil || checkpoints == nil {
		conn, err = processruntime.DialGRPCWithConfig(opts.Endpoint, processruntime.GRPCDialConfig{
			TLS: opts.GRPCTLS, CAFile: opts.GRPCCAFile, AllowInsecure: opts.AllowInsecureGRPC,
			MaxReceiveBytes: opts.Config.MaxRecordBytes + 4<<20,
		})
		if err != nil {
			_ = sink.Close()
			return nil, err
		}
		auth := processruntime.GRPCAuth{APIKey: opts.APIKey, BearerToken: opts.BearerToken, ClientID: opts.ClientID}
		if events == nil {
			events = processruntime.GRPCEventClient{Client: pb.NewEventStreamServiceClient(conn), Auth: auth}
		}
		if checkpoints == nil {
			checkpoints = processruntime.GRPCCheckpointClient{Client: pb.NewAdminCheckpointServiceClient(conn), Auth: auth}
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	var ready atomic.Bool
	setReady := func(value bool) {
		if opts.Config.TailOnlyDevelopment {
			value = false
		}
		ready.Store(value)
		if value {
			processorReady.WithLabelValues(opts.Config.ExporterID).Set(1)
		} else {
			processorReady.WithLabelValues(opts.Config.ExporterID).Set(0)
		}
	}
	setReady(false)
	var healthServer *http.Server
	healthErrors := make(chan error, 1)
	if opts.HealthAddress != "" {
		listener, listenErr := net.Listen("tcp", opts.HealthAddress)
		if listenErr != nil {
			cancel()
			if conn != nil {
				_ = conn.Close()
			}
			_ = sink.Close()
			return nil, listenErr
		}
		healthServer = &http.Server{Handler: healthHandler(&ready), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if serveErr := healthServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				healthErrors <- serveErr
				cancel()
			}
		}()
	}
	runner := processruntime.Runtime{Events: events, Checkpoints: checkpoints, Processor: processor, Config: clickHouseRuntimeConfig(opts, setReady)}
	if opts.Config.BatchDelay > 0 && (runner.Config.CheckpointInterval == 0 || opts.Config.BatchDelay < runner.Config.CheckpointInterval) {
		runner.Config.CheckpointInterval = opts.Config.BatchDelay
	}
	running := &RunningProcessor{cancel: cancel, done: make(chan struct{}), conn: conn, sink: sink, health: healthServer}
	go func() {
		runEvent := operationevent.New("job.process.clickhouse")
		runEvent.SetClientID(opts.Config.ExporterID)
		runEvent.SetProtocol(opts.Config.Protocol)
		runEvent.SetPayloadMode(string(opts.Config.PayloadMode))
		runEvent.SetDestinationClass(clickHouseDestinationClass(opts.Config.Addresses))
		digest := sha256.Sum256([]byte(string(opts.Config.PayloadMode) + "\x00" + opts.Config.Protocol + "\x00" + strings.Join(opts.Config.enabledOutputs(), ",") + "\x00" + opts.Config.RetentionMode + "\x00" + opts.Config.PurgeMode + "\x00" + processor.projections.Digest))
		runEvent.SetConfigDigest(hex.EncodeToString(digest[:]))
		runEvent.EmitStart()
		err := runner.Run(runCtx)
		cancel()
		if err != nil {
			runEvent.SetReason("processor_failed")
			runEvent.EnrichError(err)
			runEvent.SetFailureCount(1)
			runEvent.EmitTerminal(operationevent.ResultFailed)
		} else {
			runEvent.EmitTerminal(operationevent.ResultSuccess)
		}
		if healthServer != nil {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = healthServer.Shutdown(shutdownCtx)
			shutdownCancel()
		}
		select {
		case healthErr := <-healthErrors:
			if err == nil {
				err = healthErr
			}
		default:
		}
		running.mu.Lock()
		running.err = err
		running.mu.Unlock()
		close(running.done)
	}()
	return running, nil
}

func clickHouseRuntimeConfig(opts StartOptions, onReady func(bool)) processruntime.Config {
	return processruntime.Config{
		ClientID: opts.ClientID, Kinds: opts.Config.subscriptionKinds(), Detail: "full", Scope: "admin",
		AfterCursor: opts.AfterCursor, EntryChannels: clickHouseEntryChannels, Justification: "ClickHouse analytics export", CheckpointInterval: opts.CheckpointInterval,
		LeaseTTL: opts.LeaseTTL,
		OnReady:  onReady,
	}
}

func clickHouseDestinationClass(addresses []string) string {
	for _, address := range addresses {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !isLoopbackHost(host) {
			return "remote"
		}
	}
	return "local"
}

func healthHandler(ready *atomic.Bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}

func (p *RunningProcessor) Wait() error { <-p.done; p.mu.Lock(); defer p.mu.Unlock(); return p.err }

func (p *RunningProcessor) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var result error
	p.once.Do(func() {
		p.cancel()
		select {
		case <-p.done:
			p.mu.Lock()
			result = p.err
			p.mu.Unlock()
		case <-ctx.Done():
			result = ctx.Err()
		}
		if p.conn != nil {
			if err := p.conn.Close(); result == nil {
				result = err
			}
		}
		if p.health != nil {
			if err := p.health.Shutdown(ctx); result == nil && err != nil {
				result = err
			}
		}
		if p.sink != nil {
			if err := p.sink.Close(); result == nil {
				result = err
			}
		}
	})
	return result
}
