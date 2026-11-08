package clickhouse

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	"github.com/chirino/memory-service/internal/operationevent"
)

const checkpointContentType = "application/vnd.memory-service.clickhouse-checkpoint+json"

var errBackfillRequired = errors.New("backfill_required: durable event history was invalidated; rebuild the analytics dataset with a fresh checkpoint")
var clickHouseEntryChannels = []string{"history", "context", "journal"}

type replayTarget struct {
	ProjectionDigest string            `json:"projectionDigest"`
	Projections      map[string]string `json:"projections"`
	EnabledOutputs   []string          `json:"enabledOutputs"`
	ReplayID         string            `json:"replayId"`
}
type bootstrapCheckpoint struct {
	State               string        `json:"state"`
	BackfillStartCursor string        `json:"backfillStartCursor"`
	Target              *replayTarget `json:"target"`
}
type checkpoint struct {
	ExporterID          string              `json:"exporterId"`
	SafeCursor          string              `json:"lastEventCursor"`
	ObservationSequence uint32              `json:"observationSequence"`
	ProjectionDigest    string              `json:"projectionDigest"`
	Projections         map[string]string   `json:"projections"`
	ProjectionReplayID  string              `json:"projectionReplayId"`
	EnabledOutputs      []string            `json:"enabledOutputs"`
	PurgeMode           string              `json:"purgeMode"`
	Bootstrap           bootstrapCheckpoint `json:"bootstrap"`
}
type Processor struct {
	mu              sync.Mutex
	cfg             Config
	sink            Sink
	state           checkpoint
	pending         *Batch
	bytes           int
	observations    int
	leaseGeneration uint64
	projections     *ProjectionSet
}

func NewProcessor(cfg Config, sink Sink) (*Processor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if sink == nil {
		return nil, errors.New("ClickHouse sink is required")
	}
	projections, err := LoadProjectionSet(context.Background(), cfg.ProjectionPaths)
	if err != nil {
		return nil, err
	}
	p := &Processor{cfg: cfg, sink: sink, projections: projections}
	target := p.configuredTarget()
	p.state = checkpoint{ExporterID: cfg.ExporterID, ProjectionDigest: target.ProjectionDigest, Projections: target.Projections, EnabledOutputs: target.EnabledOutputs, PurgeMode: cfg.PurgeMode, Bootstrap: bootstrapCheckpoint{State: "new"}}
	return p, nil
}
func (p *Processor) ContentType() string { return checkpointContentType }
func (p *Processor) BackgroundErrors() <-chan error {
	if s, ok := p.sink.(processruntime.BackgroundErrorSource); ok {
		return s.BackgroundErrors()
	}
	return nil
}
func (p *Processor) configuredTarget() replayTarget {
	manifests := map[string]string{}
	digest := sha256.New()
	for _, item := range p.projections.Items {
		if !p.cfg.exportsResourceKind(item.Resource) || p.cfg.featureDisabled(item.Resource, disableProjections) {
			continue
		}
		manifests[item.Name] = item.Digest
		_, _ = digest.Write([]byte(item.Name + "\x00" + item.Digest + "\n"))
	}
	return replayTarget{ProjectionDigest: hex.EncodeToString(digest.Sum(nil)), Projections: manifests, EnabledOutputs: p.cfg.enabledOutputs(), ReplayID: p.cfg.ProjectionReplayID}
}
func (p *Processor) SetLeaseGeneration(generation uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation == 0 || generation > uint64(^uint32(0)) {
		return errors.New("invalid ClickHouse lease generation")
	}
	if p.pending != nil {
		return errors.New("cannot change lease generation with a pending batch")
	}
	p.leaseGeneration = generation
	p.state.ObservationSequence = 0
	processorLeaseGeneration.WithLabelValues(p.cfg.ExporterID).Set(float64(generation))
	return nil
}
func (p *Processor) Load(raw json.RawMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return errors.New("invalid ClickHouse checkpoint")
	}
	for _, field := range []string{"exporterId", "lastEventCursor", "observationSequence", "projectionDigest", "projections", "projectionReplayId", "enabledOutputs", "purgeMode", "bootstrap"} {
		if value, ok := fields[field]; !ok || string(value) == "null" {
			return fmt.Errorf("incompatible ClickHouse checkpoint: %s required; use a fresh checkpoint", field)
		}
	}
	var bootstrapFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["bootstrap"], &bootstrapFields); err != nil {
		return errors.New("invalid checkpoint bootstrap")
	}
	for _, key := range []string{"state", "backfillStartCursor", "target"} {
		if _, ok := bootstrapFields[key]; !ok {
			return errors.New("incompatible checkpoint bootstrap structure")
		}
	}
	if target := bootstrapFields["target"]; string(target) != "null" {
		var targetFields map[string]json.RawMessage
		if err := json.Unmarshal(target, &targetFields); err != nil {
			return errors.New("invalid checkpoint replay target")
		}
		for _, key := range []string{"projectionDigest", "projections", "enabledOutputs", "replayId"} {
			if value, ok := targetFields[key]; !ok || string(value) == "null" {
				return errors.New("incompatible checkpoint replay target")
			}
		}
	}
	var state checkpoint
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return fmt.Errorf("incompatible ClickHouse checkpoint: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid trailing checkpoint input")
	}
	if state.ExporterID != p.cfg.ExporterID {
		return errors.New("checkpoint exporter identity mismatch")
	}
	if state.ProjectionDigest == "" || state.Projections == nil || len(state.EnabledOutputs) == 0 {
		return errors.New("incompatible checkpoint output definition")
	}
	switch state.Bootstrap.State {
	case "new", "scanning", "catchup", "complete":
	default:
		return errors.New("invalid checkpoint bootstrap state")
	}
	if (state.Bootstrap.State == "catchup" || state.Bootstrap.State == "complete") && (state.SafeCursor == "" || state.Bootstrap.BackfillStartCursor == "") {
		return errors.New("checkpoint catchup boundary is required")
	}
	if (state.Bootstrap.State == "scanning" || state.Bootstrap.State == "catchup") && state.Bootstrap.Target == nil {
		return errors.New("checkpoint replay target is required")
	}
	if state.Bootstrap.State == "complete" && state.Bootstrap.Target != nil {
		return errors.New("completed checkpoint has a pending replay target")
	}
	if state.PurgeMode != PurgeManaged && state.PurgeMode != PurgeRecordOnly && state.PurgeMode != PurgeExternal {
		return errors.New("invalid checkpoint purge mode")
	}
	if state.PurgeMode != PurgeManaged && p.cfg.PurgeMode == PurgeManaged {
		return errors.New("managed purge cannot resume a dataset that delegated deletion; use a fresh dataset/checkpoint")
	}
	target := p.configuredTarget()
	if pending := state.Bootstrap.Target; pending != nil {
		if !reflect.DeepEqual(*pending, target) {
			return errors.New("configuration changed during a pending replay; resume its original configuration and replay ID")
		}
	} else {
		needsReplay := false
		for name, digest := range target.Projections {
			if state.Projections[name] != digest {
				needsReplay = true
			}
		}
		if len(addedStrings(state.EnabledOutputs, target.EnabledOutputs)) > 0 {
			needsReplay = true
		}
		requested := target.ReplayID != "" && target.ReplayID != state.ProjectionReplayID
		if needsReplay && !requested {
			return errors.New("new projections or re-enabled outputs require a new --projection-replay-id")
		}
		if requested && state.Bootstrap.State == "complete" {
			state.Bootstrap.Target = &target
			state.Bootstrap.State = "scanning"
			state.Bootstrap.BackfillStartCursor = state.SafeCursor
		} else if !needsReplay {
			state.ProjectionDigest = target.ProjectionDigest
			state.Projections = target.Projections
			state.EnabledOutputs = target.EnabledOutputs
		}
	}
	state.PurgeMode = p.cfg.PurgeMode
	p.state = state
	return nil
}
func addedStrings(previous, current []string) []string {
	var added []string
	for _, value := range current {
		if !slices.Contains(previous, value) {
			added = append(added, value)
		}
	}
	return added
}
func (p *Processor) Snapshot() (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return json.Marshal(p.state)
}

func (p *Processor) Bootstrap(ctx context.Context, events processruntime.EventClient, save func(context.Context) error) (string, error) {
	p.mu.Lock()
	state := p.state.Bootstrap
	safe := p.state.SafeCursor
	p.mu.Unlock()
	if state.State == "complete" || state.State == "catchup" {
		return safe, nil
	}
	if p.cfg.TailOnlyDevelopment {
		return p.developmentTail(ctx, events, save)
	}
	p.mu.Lock()
	target := p.configuredTarget()
	p.state.Bootstrap.State = "scanning"
	if p.state.Bootstrap.Target == nil {
		p.state.Bootstrap.Target = &target
	}
	boundary := p.state.Bootstrap.BackfillStartCursor
	p.mu.Unlock()
	if err := save(ctx); err != nil {
		return "", err
	}
	scanCtx, cancelScan := context.WithCancel(ctx)
	defer cancelScan()
	stream, err := events.Subscribe(scanCtx, processruntime.SubscribeRequest{Kinds: p.cfg.resourceKinds(), Detail: "full", Scope: "admin", EntryChannels: clickHouseEntryChannels, InitialState: "current", Justification: "ClickHouse analytics bootstrap"})
	if err != nil {
		return "", err
	}
	for {
		event, err := stream.Recv()
		if err != nil {
			return "", err
		}
		if event.Kind == "stream" && event.Event == "phase" {
			var phase struct {
				Phase string `json:"phase"`
			}
			if err := json.Unmarshal(event.Data, &phase); err != nil {
				return "", errors.New("invalid bootstrap phase")
			}
			if phase.Phase == "snapshot" && boundary == "" {
				boundary = event.Cursor
				p.mu.Lock()
				p.state.Bootstrap.BackfillStartCursor = boundary
				p.mu.Unlock()
				if err := save(ctx); err != nil {
					return "", err
				}
			}
			if phase.Phase == "replay" || phase.Phase == "live" {
				if boundary == "" {
					return "", errors.New("bootstrap boundary missing")
				}
				if err := p.Flush(ctx); err != nil {
					return "", err
				}
				p.mu.Lock()
				p.state.Bootstrap.State = "catchup"
				p.state.SafeCursor = boundary
				p.mu.Unlock()
				if err := save(ctx); err != nil {
					return "", err
				}
				return boundary, nil
			}
			continue
		}
		if event.Event != "snapshot" || !p.cfg.exportsResourceKind(event.Kind) {
			continue
		}
		event.Cursor = ""
		o, err := decodeObservation(p.cfg, event)
		if err != nil {
			return "", err
		}
		p.mu.Lock()
		err = p.appendObservation(ctx, o)
		flushed := p.pending == nil
		p.mu.Unlock()
		if err != nil {
			return "", err
		}
		processorBootstrapRows.WithLabelValues(p.cfg.ExporterID, event.Kind).Inc()
		if flushed {
			processorBootstrapPages.WithLabelValues(p.cfg.ExporterID, event.Kind).Inc()
			if err := save(ctx); err != nil {
				return "", err
			}
		}
	}
}
func (p *Processor) developmentTail(ctx context.Context, events processruntime.EventClient, save func(context.Context) error) (string, error) {
	stream, err := events.Subscribe(ctx, processruntime.SubscribeRequest{Kinds: p.cfg.subscriptionKinds(), Detail: "full", Scope: "admin", EntryChannels: clickHouseEntryChannels, Justification: "ClickHouse analytics development tail"})
	if err != nil {
		return "", err
	}
	for {
		event, err := stream.Recv()
		if err != nil {
			return "", err
		}
		var phase struct {
			Phase string `json:"phase"`
		}
		if event.Kind == "stream" && event.Event == "phase" && json.Unmarshal(event.Data, &phase) == nil && phase.Phase == "live" && event.Cursor != "" {
			p.mu.Lock()
			p.state.SafeCursor = event.Cursor
			p.state.Bootstrap = bootstrapCheckpoint{State: "complete", BackfillStartCursor: event.Cursor}
			p.mu.Unlock()
			return event.Cursor, save(ctx)
		}
	}
}
func (p *Processor) Handle(ctx context.Context, event processruntime.EventEnvelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Kind == "stream" {
		if event.Event == "invalidate" {
			return errBackfillRequired
		}
		var phase struct {
			Phase string `json:"phase"`
		}
		if event.Event == "phase" && event.Cursor != "" && json.Unmarshal(event.Data, &phase) == nil && phase.Phase == "live" {
			p.mu.Lock()
			defer p.mu.Unlock()
			if err := p.flushLocked(ctx); err != nil {
				return err
			}
			p.state.SafeCursor = event.Cursor
			if p.state.Bootstrap.State == "catchup" {
				if target := p.state.Bootstrap.Target; target != nil {
					p.state.ProjectionDigest = target.ProjectionDigest
					p.state.Projections = target.Projections
					p.state.EnabledOutputs = target.EnabledOutputs
					p.state.ProjectionReplayID = target.ReplayID
				}
				p.state.Bootstrap.State = "complete"
				p.state.Bootstrap.Target = nil
			}
		}
		return nil
	}
	if event.Cursor == "" {
		return nil
	}
	o, err := decodeObservation(p.cfg, event)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.appendObservation(ctx, o); err != nil {
		return err
	}
	processorEvents.WithLabelValues(p.cfg.ExporterID, event.Kind, event.Event).Inc()
	return nil
}
func (p *Processor) appendObservation(ctx context.Context, o observation) error {
	if p.leaseGeneration == 0 {
		return errors.New("checkpoint lease generation is not initialized")
	}
	if p.state.ObservationSequence == ^uint32(0) {
		return errors.New("observation sequence exhausted; restart for a new lease generation")
	}
	p.state.ObservationSequence++
	p.observations++
	if p.pending == nil {
		var err error
		p.pending, err = newBatch(p.cfg.ExporterID, o.Cursor)
		if err != nil {
			return err
		}
	}
	version := p.leaseGeneration<<32 | uint64(p.state.ObservationSequence)
	cursor := o.Cursor
	if cursor == "" {
		cursor = "bootstrap:" + o.ID
	}
	common := Common{ExporterID: p.cfg.ExporterID, BatchID: p.pending.ID, EventID: eventID(p.cfg.ExporterID, cursor, o.Kind, o.Action, o.Change), SourceCursor: o.Cursor, IngestVersion: version, ObservedAt: p.pending.ObservedAt}
	if o.HardDeleted && p.cfg.PurgeMode != PurgeExternal {
		scope := purgeResource
		if o.Kind == "conversation" {
			scope = purgeGroup
		}
		purge := PurgeRow{ExporterID: p.cfg.ExporterID, BatchID: common.BatchID, PurgeID: common.EventID, EventID: common.EventID, ResourceKind: o.Kind, AnalyticsResourceID: o.ID, ConversationID: o.ConversationID, ConversationGroupID: o.GroupID, Scope: scope, CutoffIngestVersion: version, RequestedAt: o.OccurredAt}
		if purge.RequestedAt.IsZero() {
			purge.RequestedAt = common.ObservedAt
		}
		resolved := false
		for _, prior := range p.pending.Purges {
			if prior.PurgeID == purge.PurgeID {
				purge = prior
				resolved = true
				break
			}
		}
		if !resolved {
			var err error
			purge, err = p.sink.ResolvePurge(ctx, purge)
			if err != nil {
				return err
			}
			p.pending.Purges = append(p.pending.Purges, purge)
		}
		common.IngestVersion = purge.CutoffIngestVersion
	}
	if o.Deleted {
		p.pending.Fences = append(p.pending.Fences, FenceRow{Common: common, ResourceKind: o.Kind, ResourceID: o.ID, ConversationID: o.ConversationID, ConversationGroupID: o.GroupID})
		if o.Kind == "conversation" && o.HardDeleted {
			p.pending.Fences = append(p.pending.Fences, FenceRow{Common: common, ResourceKind: "conversation_group", ResourceID: o.GroupID, ConversationGroupID: o.GroupID})
		}
	}
	if p.cfg.exportsResourceKind(o.Kind) {
		if o.Action != "snapshot" && !p.cfg.featureDisabled(o.Kind, disableLifecycleEvents) {
			occurred := o.OccurredAt
			if occurred.IsZero() {
				occurred = common.ObservedAt
			}
			p.pending.Lifecycle = append(p.pending.Lifecycle, LifecycleRow{Common: common, OccurredAt: occurred, ResourceKind: o.Kind, AnalyticsResourceID: o.ID, Action: o.Action, Change: o.Change, ConversationID: o.ConversationID, ConversationGroupID: o.GroupID, ContentType: o.ContentType, MemoryKind: o.MemoryKind, SnapshotAvailable: o.Record != nil})
		}
		if o.Kind == "membership" {
			if o.Deleted || o.Record != nil {
				p.pending.Memberships = append(p.pending.Memberships, MembershipRow{Common: common, ResourceID: o.ID, ConversationGroupID: o.GroupID, UserID: o.UserID, AccessLevel: o.AccessLevel, CreatedAt: o.CreatedAt, IsDeleted: o.Deleted})
			}
		} else if !p.cfg.featureDisabled(o.Kind, disableProjections) {
			selector := o.ContentType
			if o.Kind == "memory" {
				selector = o.MemoryKind
			}
			for _, projection := range p.projections.Match(o.Kind, selector) {
				base := ProjectionRow{Common: common, ResourceID: o.ID, ConversationID: o.ConversationID, ConversationGroupID: o.GroupID}
				if o.Deleted {
					p.pending.Projections = append(p.pending.Projections, projection.TombstoneRow(base))
					continue
				}
				var rows []ProjectionRow
				code := "snapshot_unavailable"
				if o.Record != nil {
					rows, code = projection.Rows(ctx, base, map[string]any{"resource": o.Kind, "record": o.Record})
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if code != "" {
					if err := p.recordProjectionFailure(common, o, projection.Name, code); err != nil {
						return err
					}
				} else {
					p.pending.Projections = append(p.pending.Projections, rows...)
					for _, row := range rows {
						if raw, err := json.Marshal(row.Values); err == nil {
							p.bytes += len(raw) + 128
						}
					}
				}
			}
		}
	}
	if o.Cursor != "" {
		p.pending.LastCursor = o.Cursor
	}
	encoded, err := json.Marshal(o.Record)
	if err != nil {
		return errors.New("invalid_normalized_record")
	}
	p.bytes += len(encoded) + 256

	processorBufferedRows.WithLabelValues(p.cfg.ExporterID).Set(float64(batchRowCount(p.pending)))
	processorBufferedBytes.WithLabelValues(p.cfg.ExporterID).Set(float64(p.bytes))
	if !o.OccurredAt.IsZero() {
		processorSourceLag.WithLabelValues(p.cfg.ExporterID).Set(max(0, time.Since(o.OccurredAt).Seconds()))
	}
	if p.observations >= p.cfg.BatchEvents || batchRowCount(p.pending) >= p.cfg.BatchRows || p.bytes >= p.cfg.BatchBytes {
		return p.flushLocked(ctx)
	}
	return nil
}
func (p *Processor) recordProjectionFailure(common Common, o observation, name, code string) error {
	processorProjectionResults.WithLabelValues(p.cfg.ExporterID, name, code).Inc()
	if p.cfg.ProjectionFailurePolicy == "stop" {
		return fmt.Errorf("analytics projection %s failed with %s", name, code)
	}
	p.pending.ProjectionFailures = append(p.pending.ProjectionFailures, ProjectionFailureRow{Common: common, ResourceKind: o.Kind, AnalyticsResourceID: o.ID, ConversationID: o.ConversationID, ConversationGroupID: o.GroupID, ProjectionName: name, ErrorCode: code, AttemptCount: 1, FirstSeenAt: common.ObservedAt, LastSeenAt: common.ObservedAt})
	return nil
}
func newBatch(exporter, cursor string) (*Batch, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return nil, err
	}
	return &Batch{ID: hex.EncodeToString(value[:]), ExporterID: exporter, FirstCursor: cursor, ObservedAt: time.Now().UTC()}, nil
}
func batchRowCount(b *Batch) int {
	if b == nil {
		return 0
	}
	return len(b.Lifecycle) + len(b.Fences) + len(b.Purges) + len(b.Memberships) + len(b.Projections) + len(b.ProjectionFailures)
}
func (p *Processor) Flush(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flushLocked(ctx)
}
func (p *Processor) flushLocked(ctx context.Context) error {
	if p.pending == nil {
		return nil
	}
	backoff := p.cfg.RetryMin
	for attempt := 0; ; attempt++ {
		event := operationevent.New("job.process.clickhouse.insert")
		event.SetWorkCount(int64(batchRowCount(p.pending)))
		event.SetRetryAttempt(attempt)
		event.SetDestinationClass(clickHouseDestinationClass(p.cfg.Addresses))
		err := p.sink.WriteBatch(ctx, *p.pending)
		if err == nil {
			event.EmitTerminal(operationevent.ResultSuccess)
			processorBatchAttempts.WithLabelValues(p.cfg.ExporterID, "success").Inc()
			processorBatchesWritten.WithLabelValues(p.cfg.ExporterID).Inc()
			processorLastWrite.WithLabelValues(p.cfg.ExporterID).SetToCurrentTime()
			for table, count := range map[string]int{"lifecycle_events": len(p.pending.Lifecycle), "deletion_fences": len(p.pending.Fences), "conversation_memberships": len(p.pending.Memberships), "projection_failures": len(p.pending.ProjectionFailures)} {
				processorRowsWritten.WithLabelValues(p.cfg.ExporterID, table).Add(float64(count))
			}
			counts := map[string]int{}
			for _, row := range p.pending.Projections {
				counts[row.TableName]++
			}
			for table, count := range counts {
				processorRowsWritten.WithLabelValues(p.cfg.ExporterID, table).Add(float64(count))
			}
			break
		}
		event.SetReason("clickhouse_write_failed")
		if ctx.Err() != nil {
			event.EmitTerminal(operationevent.ResultCanceled)
			return ctx.Err()
		}
		processorBatchAttempts.WithLabelValues(p.cfg.ExporterID, "failed").Inc()
		event.EmitTerminal(operationevent.ResultRetrying)
		log.Warn("ClickHouse batch retry", "errorType", fmt.Sprintf("%T", err), "retryIn", backoff)
		timer := time.NewTimer(backoff/2 + time.Duration(mathrand.Int64N(int64(backoff))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, p.cfg.RetryMax)
	}
	if p.pending.LastCursor != "" {
		p.state.SafeCursor = p.pending.LastCursor
	}
	p.pending = nil
	p.bytes = 0
	p.observations = 0
	processorBufferedRows.WithLabelValues(p.cfg.ExporterID).Set(0)
	processorBufferedBytes.WithLabelValues(p.cfg.ExporterID).Set(0)
	return nil
}
