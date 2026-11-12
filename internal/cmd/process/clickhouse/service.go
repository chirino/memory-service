package clickhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

type capabilityClient interface {
	GetCapabilities(context.Context, *emptypb.Empty, ...grpc.CallOption) (*pb.CapabilitiesResponse, error)
}

type StartOptions struct {
	Capabilities       capabilityClient
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
	// Build the processor first so startup can reject a stored checkpoint, or an
	// unsupported source, before OpenSink claims an empty ClickHouse database.
	processor, err := newProcessor(opts.Config)
	if err != nil {
		if opts.Sink != nil {
			_ = opts.Sink.Close()
		}
		return nil, err
	}
	events, checkpoints := opts.Events, opts.Checkpoints
	capabilities := opts.Capabilities
	var conn *grpc.ClientConn
	// StartProcessor owns opts.Sink, so early failures close it too.
	closeConn := func() {
		if conn != nil {
			_ = conn.Close()
		}
	}
	abort := func(err error) (*RunningProcessor, error) {
		closeConn()
		if opts.Sink != nil {
			_ = opts.Sink.Close()
		}
		return nil, err
	}
	if events == nil || checkpoints == nil || opts.Config.ExportMemberships && capabilities == nil {
		conn, err = processruntime.DialGRPCWithConfig(opts.Endpoint, processruntime.GRPCDialConfig{
			TLS: opts.GRPCTLS, CAFile: opts.GRPCCAFile, AllowInsecure: opts.AllowInsecureGRPC,
			MaxReceiveBytes: opts.Config.MaxRecordBytes + 4<<20,
		})
		if err != nil {
			return abort(err)
		}
		if capabilities == nil {
			capabilities = pb.NewSystemServiceClient(conn)
		}
		auth := processruntime.GRPCAuth{APIKey: opts.APIKey, BearerToken: opts.BearerToken, ClientID: opts.ClientID}
		if events == nil {
			events = processruntime.GRPCEventClient{Client: pb.NewEventStreamServiceClient(conn), Auth: auth}
		}
		if checkpoints == nil {
			checkpoints = processruntime.GRPCCheckpointClient{Client: pb.NewAdminCheckpointServiceClient(conn), Auth: auth}
		}
	}
	if opts.Config.ExportMemberships {
		capabilityCtx := ctx
		if opts.APIKey != "" {
			capabilityCtx = metadata.AppendToOutgoingContext(capabilityCtx, "x-api-key", opts.APIKey)
		}
		if opts.BearerToken != "" {
			capabilityCtx = metadata.AppendToOutgoingContext(capabilityCtx, "authorization", "Bearer "+opts.BearerToken)
		}
		capabilityCtx = metadata.AppendToOutgoingContext(capabilityCtx, "x-client-id", opts.ClientID)
		response, capErr := capabilities.GetCapabilities(capabilityCtx, &emptypb.Empty{})
		if capErr != nil || !response.GetFeatures().GetAnalyticsMembershipSnapshots() {
			return abort(errors.New("membership export requires source capability analytics_membership_snapshots"))
		}
	}
	if err := precheckCheckpoint(ctx, checkpoints, opts.ClientID, opts.Config); err != nil {
		return abort(err)
	}
	sink := opts.Sink
	if sink == nil {
		opened, err := OpenSink(ctx, opts.Config)
		if err != nil {
			closeConn()
			return nil, err
		}
		sink = opened
	}
	processor.sink = sink
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

// precheckCheckpoint validates a stored checkpoint without touching ClickHouse.
// The runtime loads it again under the lease.
func precheckCheckpoint(ctx context.Context, checkpoints processruntime.CheckpointClient, clientID string, cfg Config) error {
	stored, err := checkpoints.Get(ctx, clientID)
	if errors.Is(err, processruntime.ErrCheckpointNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read checkpoint for client ID %q: %w", clientID, err)
	}
	if stored.ContentType != "" && stored.ContentType != checkpointContentType {
		return fmt.Errorf("checkpoint for client ID %q was written by a different or previous ClickHouse exporter (content type %q); use a new client ID and a new empty analytics database", clientID, stored.ContentType)
	}
	probe, err := newProcessor(cfg)
	if err != nil {
		return err
	}
	if err := probe.Load(stored.Value); err != nil {
		return fmt.Errorf("load checkpoint for client ID %q: %w", clientID, err)
	}
	return nil
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
