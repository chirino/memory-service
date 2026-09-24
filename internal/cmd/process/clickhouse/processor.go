package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	processruntime "github.com/chirino/memory-service/internal/cmd/process/runtime"
	pb "github.com/chirino/memory-service/internal/generated/pb/memory/v1"
	"github.com/chirino/memory-service/internal/operationevent"
)

const checkpointContentType = "application/vnd.memory-service.clickhouse-checkpoint+json;v=1"

var errBackfillRequired = errors.New("backfill_required: durable event history was invalidated; restore ClickHouse from a complete backfill before restarting the exporter")

var clickHouseEntryChannels = []string{"history", "context", "journal"}

type checkpoint struct {
	Version            int                 `json:"version"`
	ExporterID         string              `json:"exporterId"`
	SafeCursor         string              `json:"lastEventCursor,omitempty"`
	BatchSequence      uint32              `json:"batchSequence"`
	ProjectionDigest   string              `json:"projectionDigest,omitempty"`
	ProjectionReplayID string              `json:"projectionReplayId,omitempty"`
	EnabledOutputs     []string            `json:"enabledOutputs,omitempty"`
	PurgeMode          string              `json:"purgeMode,omitempty"`
	Bootstrap          bootstrapCheckpoint `json:"bootstrap"`
}

type bootstrapCheckpoint struct {
	State               string `json:"state,omitempty"`
	BackfillStartCursor string `json:"backfillStartCursor,omitempty"`
	Phase               string `json:"phase,omitempty"`
	PageToken           string `json:"pageToken,omitempty"`
}

// Processor converts durable event notifications into ClickHouse batches.
// pendingCursor never becomes SafeCursor until every table write has been
// acknowledged by the sink.
type Processor struct {
	mu              sync.Mutex
	cfg             Config
	sink            Sink
	state           checkpoint
	pending         *Batch
	bytes           int
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
	return &Processor{cfg: cfg, sink: sink, projections: projections, state: checkpoint{Version: 1, ExporterID: cfg.ExporterID, ProjectionDigest: projections.Digest, EnabledOutputs: cfg.enabledOutputs(), PurgeMode: cfg.PurgeMode}}, nil
}

func (p *Processor) ContentType() string { return checkpointContentType }

func (p *Processor) BackgroundErrors() <-chan error {
	if source, ok := p.sink.(processruntime.BackgroundErrorSource); ok {
		return source.BackgroundErrors()
	}
	return nil
}

func (p *Processor) Bootstrap(ctx context.Context, events processruntime.EventClient, save func(context.Context) error) (string, error) {
	if p.cfg.TailOnlyDevelopment {
		stream, err := events.Subscribe(ctx, processruntime.SubscribeRequest{Kinds: p.cfg.subscriptionKinds(), Detail: "full", Scope: "admin", EntryChannels: clickHouseEntryChannels, Justification: "ClickHouse analytics development tail"})
		if err != nil {
			return "", fmt.Errorf("capture development tail cursor: %w", err)
		}
		for {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				return "", fmt.Errorf("capture development tail cursor: %w", recvErr)
			}
			if event.Kind != "stream" || event.Event != "phase" || event.Cursor == "" {
				continue
			}
			var marker map[string]string
			if json.Unmarshal(event.Data, &marker) != nil || marker["phase"] != "live" {
				continue
			}
			p.mu.Lock()
			p.state.SafeCursor = event.Cursor
			p.state.Bootstrap = bootstrapCheckpoint{State: "complete", BackfillStartCursor: event.Cursor}
			p.mu.Unlock()
			if err := save(ctx); err != nil {
				return "", err
			}
			return event.Cursor, nil
		}
	}

	p.mu.Lock()
	state := p.state.Bootstrap
	safeCursor := p.state.SafeCursor
	completedProjectionReplayID := p.state.ProjectionReplayID
	p.mu.Unlock()
	projectionReplay := p.cfg.ProjectionReplayID != "" && p.cfg.ProjectionReplayID != completedProjectionReplayID && safeCursor != "" && (state.State == "complete" || state.State == "catchup")
	if (state.State == "complete" || state.State == "catchup") && !projectionReplay {
		if safeCursor != "" {
			return safeCursor, nil
		}
		return state.BackfillStartCursor, nil
	}

	boundary := ""
	if state.State == "scanning" {
		boundary = state.BackfillStartCursor
	} else if projectionReplay {
		boundary = safeCursor
	}
	p.mu.Lock()
	p.state.Bootstrap = bootstrapCheckpoint{State: "scanning", BackfillStartCursor: boundary}
	p.mu.Unlock()
	if err := save(ctx); err != nil {
		return "", err
	}
	stream, err := events.Subscribe(ctx, processruntime.SubscribeRequest{
		Kinds: p.cfg.resourceKinds(), Detail: "full", Scope: "admin",
		EntryChannels: clickHouseEntryChannels, InitialState: "current", Justification: "ClickHouse analytics bootstrap",
	})
	if err != nil {
		return "", fmt.Errorf("subscribe to current analytics state: %w", err)
	}
	phase := ""
	records := make([]*pb.AnalyticsRecord, 0, min(500, p.cfg.BatchRows))
	flush := func() error {
		if len(records) == 0 {
			return nil
		}
		if err := p.writeBackfillPage(ctx, phase, records); err != nil {
			return err
		}
		processorBootstrapPages.WithLabelValues(p.cfg.ExporterID, phase).Inc()
		processorBootstrapRows.WithLabelValues(p.cfg.ExporterID, phase).Add(float64(len(records)))
		records = records[:0]
		return save(ctx)
	}
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			return "", fmt.Errorf("receive current analytics state: %w", recvErr)
		}
		if event.Kind == "stream" && event.Event == "phase" {
			var marker map[string]string
			_ = json.Unmarshal(event.Data, &marker)
			switch marker["phase"] {
			case "snapshot":
				if boundary == "" {
					boundary = event.Cursor
					p.mu.Lock()
					p.state.Bootstrap.BackfillStartCursor = boundary
					p.mu.Unlock()
					if err := save(ctx); err != nil {
						return "", err
					}
				}
			case "replay", "live":
				if err := flush(); err != nil {
					return "", err
				}
				if boundary == "" {
					boundary = event.Cursor
				}
				p.mu.Lock()
				p.state.Bootstrap = bootstrapCheckpoint{State: "catchup", BackfillStartCursor: boundary}
				p.state.SafeCursor = boundary
				if p.cfg.ProjectionReplayID != "" {
					p.state.ProjectionReplayID = p.cfg.ProjectionReplayID
				}
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
		if phase != "" && phase != event.Kind {
			if err := flush(); err != nil {
				return "", err
			}
		}
		phase = event.Kind
		record, err := analyticsRecordFromResource(event.Kind, event.Data)
		if err != nil {
			return "", err
		}
		records = append(records, record)
		if len(records) >= min(500, max(1, p.cfg.BatchRows)) {
			if err := flush(); err != nil {
				return "", err
			}
		}
	}
}

func (p *Processor) writeBackfillPage(ctx context.Context, phase string, records []*pb.AnalyticsRecord) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending != nil {
		return errors.New("cannot start analytics backfill with a pending live batch")
	}
	batch, err := p.newBatch("")
	if err != nil {
		return err
	}
	p.pending = batch
	for i, record := range records {
		row, rowErr := p.backfillResourceRow(phase, record)
		if rowErr != nil {
			return rowErr
		}
		if row != nil {
			if len(row.PayloadJSON) > p.cfg.MaxRecordBytes {
				return fmt.Errorf("record_too_large: backfill %s payload is %d bytes and record limit is %d", phase, len(row.PayloadJSON), p.cfg.MaxRecordBytes)
			}
			if p.hasPendingMultiRowSnapshot(row.ResourceID) {
				if err := p.flushLocked(ctx); err != nil {
					return err
				}
				batch, err = p.newBatch("")
				if err != nil {
					return err
				}
				p.pending = batch
			}
			row.Common.BatchID = batch.ID
			row.Common.IngestVersion = batch.Version
			row.Common.ObservedAt = batch.ObservedAt
			p.pending.Resources = append(p.pending.Resources, *row)
			p.bytes += len(row.PayloadJSON) + 256
			if phase == "conversation" && !p.cfg.featureDisabled("conversation", disableLineage) {
				conversation := record.GetConversation()
				if conversation != nil && conversation.GetForkedAtConversationId() != "" {
					lineagePayload := map[string]any{
						"ancestorConversationId":   conversation.GetForkedAtConversationId(),
						"descendantConversationId": conversation.GetId(),
						"depth":                    1,
					}
					if conversation.GetForkedAtEntryId() != "" {
						lineagePayload["forkedAtEntryId"] = conversation.GetForkedAtEntryId()
					}
					encoded, encodeErr := json.Marshal(lineagePayload)
					if encodeErr != nil {
						return encodeErr
					}
					lineageID := conversation.GetForkedAtConversationId() + "\n" + conversation.GetId()
					lineageCommon := row.Common
					lineageCommon.EventID = eventID(p.cfg.ExporterID, "backfill:"+lineageID, "lineage", "snapshot", "backfill")
					p.pending.Resources = append(p.pending.Resources, ResourceRow{
						Common: lineageCommon, ResourceID: lineageID, ResourceType: "lineage",
						ConversationID: conversation.GetId(), ConversationGroupID: conversation.GetConversationGroupId(),
						CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, PayloadJSON: string(encoded),
					})
					p.bytes += len(encoded) + 128
				}
			}
			if err := p.applyProjections(ctx, row.Common, row.ResourceID, row.ConversationID, row.ConversationGroupID, phase, nil, record, false); err != nil {
				return err
			}
			if batchRowCount(p.pending) >= p.cfg.BatchRows || p.bytes >= p.cfg.BatchBytes {
				if err := p.flushLocked(ctx); err != nil {
					return err
				}
				if i+1 < len(records) {
					batch, err = p.newBatch("")
					if err != nil {
						return err
					}
					p.pending = batch
				}
			}
		}
	}
	return p.flushLocked(ctx)
}

func (p *Processor) backfillResourceRow(phase string, record *pb.AnalyticsRecord) (*ResourceRow, error) {
	if record == nil || !record.GetSnapshotAvailable() || record.GetReference() == nil {
		return nil, nil
	}
	ref := record.GetReference()
	sourceID := ref.GetId()
	resourceType := phase
	common := Common{ExporterID: p.cfg.ExporterID, EventID: eventID(p.cfg.ExporterID, "backfill:"+sourceID, resourceType, "snapshot", "backfill"), SchemaVersion: schemaVersion}
	payload, err := p.analyticsRecordPayload(record)
	if err != nil {
		return nil, err
	}
	row := &ResourceRow{Common: common, ResourceID: sourceID, ResourceType: resourceType, PayloadJSON: payload}
	switch value := record.GetRecord().(type) {
	case *pb.AnalyticsRecord_Conversation:
		row.ConversationID = value.Conversation.GetId()
		row.ConversationGroupID = value.Conversation.GetConversationGroupId()
		row.CreatedAt = value.Conversation.GetCreatedAt().AsTime().UTC()
		row.UpdatedAt = value.Conversation.GetUpdatedAt().AsTime().UTC()
		row.IsArchived = value.Conversation.GetArchivedAt() != nil
	case *pb.AnalyticsRecord_Entry:
		row.ConversationID = value.Entry.GetConversationId()
		row.ConversationGroupID = value.Entry.GetConversationGroupId()
		row.CreatedAt = value.Entry.GetCreatedAt().AsTime().UTC()
		row.UpdatedAt = row.CreatedAt
	case *pb.AnalyticsRecord_Memory:
		row.CreatedAt = value.Memory.GetCreatedAt().AsTime().UTC()
		row.UpdatedAt = row.CreatedAt
		row.IsArchived = value.Memory.GetArchivedAt() != nil
	}
	return row, nil
}

func (p *Processor) SetLeaseGeneration(generation uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation == 0 || generation > uint64(^uint32(0)) {
		return fmt.Errorf("ClickHouse lease generation %d is outside the 32-bit ingest version range", generation)
	}
	if p.pending != nil {
		return errors.New("cannot change ClickHouse lease generation with a pending batch")
	}
	p.leaseGeneration = generation
	p.state.BatchSequence = 0
	processorLeaseGeneration.WithLabelValues(p.cfg.ExporterID).Set(float64(generation))
	return nil
}

func (p *Processor) Load(raw json.RawMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var state checkpoint
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Version != 1 {
		return fmt.Errorf("unsupported ClickHouse checkpoint version %d", state.Version)
	}
	if state.ExporterID != p.cfg.ExporterID {
		return fmt.Errorf("checkpoint exporter ID %q does not match %q", state.ExporterID, p.cfg.ExporterID)
	}
	configuredOutputs := p.cfg.enabledOutputs()
	storedOutputs := state.EnabledOutputs
	if len(storedOutputs) == 0 {
		storedOutputs = (Config{}).enabledOutputs()
	}
	for _, output := range addedStrings(storedOutputs, configuredOutputs) {
		if strings.HasSuffix(output, ":"+disableProjections) && len(p.cfg.ProjectionPaths) > 0 && p.cfg.ProjectionReplayID != "" && p.cfg.ProjectionReplayID != state.ProjectionReplayID {
			continue
		}
		return fmt.Errorf("configured ClickHouse output %q was disabled in the checkpoint; reset the exporter checkpoint and ClickHouse rows or use a new exporter ID", output)
	}
	storedPurgeMode := state.PurgeMode
	if storedPurgeMode == "" {
		storedPurgeMode = PurgeManaged
	}
	if storedPurgeMode != PurgeManaged && p.cfg.PurgeMode == PurgeManaged {
		return fmt.Errorf("managed ClickHouse purge cannot resume after checkpoint mode %q because hard-delete events may have been skipped; reset the exporter checkpoint and ClickHouse rows or use a new exporter ID", storedPurgeMode)
	}
	state.EnabledOutputs = configuredOutputs
	state.PurgeMode = p.cfg.PurgeMode
	state.ProjectionDigest = p.projections.Digest
	p.state = state
	return nil
}

func addedStrings(previous, current []string) []string {
	added := make([]string, 0)
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

func (p *Processor) Handle(ctx context.Context, event processruntime.EventEnvelope) error {
	if event.Kind == "stream" {
		if event.Event == "invalidate" {
			return errBackfillRequired
		}
		if event.Event != "phase" || strings.TrimSpace(event.Cursor) == "" {
			return nil
		}
		var marker map[string]string
		if json.Unmarshal(event.Data, &marker) == nil && marker["phase"] == "live" {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.pending == nil {
				p.state.SafeCursor = event.Cursor
				if p.state.Bootstrap.State == "catchup" {
					p.state.Bootstrap.State = "complete"
				}
			}
		}
		return nil
	}
	if strings.TrimSpace(event.Cursor) == "" {
		return nil
	}
	var snapshot *pb.AnalyticsRecord
	var eventData map[string]any
	if len(event.Data) > 0 {
		_ = json.Unmarshal(event.Data, &eventData)
	}
	change := strings.TrimSpace(event.Change)
	if change == "" {
		change = firstString(eventData, "change")
	}
	deleted := event.Event == "deleted" || change == "evicted" || change == "hard_deleted"
	if p.cfg.exportsResourceKind(event.Kind) && !deleted && firstString(eventData, "id") != "" {
		var err error
		snapshot, err = analyticsRecordFromResource(event.Kind, event.Data)
		if err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hasPendingMultiRowSnapshot(resourceSourceID(event.Kind, eventData)) {
		if err := p.flushLocked(ctx); err != nil {
			return err
		}
	}
	if p.pending == nil {
		batch, err := p.newBatch(event.Cursor)
		if err != nil {
			return err
		}
		p.pending = batch
	}
	if err := p.appendEvent(ctx, event, snapshot); err != nil {
		return err
	}
	processorEvents.WithLabelValues(p.cfg.ExporterID, event.Kind, event.Event).Inc()
	if !event.Time.IsZero() {
		processorSourceLag.WithLabelValues(p.cfg.ExporterID).Set(max(0, time.Since(event.Time).Seconds()))
	}
	processorBufferedRows.WithLabelValues(p.cfg.ExporterID).Set(float64(batchRowCount(p.pending)))
	processorBufferedBytes.WithLabelValues(p.cfg.ExporterID).Set(float64(p.bytes))
	if len(p.pending.Lifecycle) >= p.cfg.BatchEvents || batchRowCount(p.pending) >= p.cfg.BatchRows || p.bytes >= p.cfg.BatchBytes {
		return p.flushLocked(ctx)
	}
	return nil
}

// Distinct snapshots of a multi-row resource must receive distinct batch versions.
// ReplacingMergeTree replaces each row index independently; equal versions can
// otherwise leave a mixture of snapshots that the projection view cannot recover.
func (p *Processor) hasPendingMultiRowSnapshot(resourceID string) bool {
	if p.pending != nil && resourceID != "" {
		for _, row := range p.pending.Projections {
			if row.Multi && row.ResourceID == resourceID {
				return true
			}
		}
	}
	return false
}

func batchRowCount(batch *Batch) int {
	if batch == nil {
		return 0
	}
	return len(batch.Lifecycle) + len(batch.Resources) + len(batch.Purges) + len(batch.Projections) + len(batch.ProjectionFailures)
}

func (p *Processor) newBatch(firstCursor string) (*Batch, error) {
	if p.leaseGeneration == 0 {
		return nil, errors.New("ClickHouse checkpoint lease generation is not initialized")
	}
	id, err := randomBatchID()
	if err != nil {
		return nil, err
	}
	p.state.BatchSequence++
	if p.state.BatchSequence == 0 {
		return nil, errors.New("ClickHouse batch sequence overflow")
	}
	version := p.leaseGeneration<<32 | uint64(p.state.BatchSequence)
	return &Batch{ID: id, ExporterID: p.cfg.ExporterID, FirstCursor: firstCursor, Version: version, ObservedAt: time.Now().UTC()}, nil
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
		event.SetClientID(p.cfg.ExporterID)
		event.SetProtocol(p.cfg.Protocol)
		event.SetPayloadMode(string(p.cfg.PayloadMode))
		event.SetWorkCount(int64(len(p.pending.Lifecycle) + len(p.pending.Resources) + len(p.pending.Purges) + len(p.pending.Projections) + len(p.pending.ProjectionFailures)))
		event.SetRetryAttempt(attempt)
		event.EmitStart()
		err := p.sink.WriteBatch(ctx, *p.pending)
		if err == nil {
			processorBatchAttempts.WithLabelValues(p.cfg.ExporterID, "success").Inc()
			processorBatchesWritten.WithLabelValues(p.cfg.ExporterID).Inc()
			processorRowsWritten.WithLabelValues(p.cfg.ExporterID, "lifecycle").Add(float64(len(p.pending.Lifecycle)))
			processorRowsWritten.WithLabelValues(p.cfg.ExporterID, "resources").Add(float64(len(p.pending.Resources)))
			processorRowsWritten.WithLabelValues(p.cfg.ExporterID, "purge_queue").Add(float64(len(p.pending.Purges)))
			processorRowsWritten.WithLabelValues(p.cfg.ExporterID, "projections").Add(float64(len(p.pending.Projections)))
			processorRowsWritten.WithLabelValues(p.cfg.ExporterID, "projection_failures").Add(float64(len(p.pending.ProjectionFailures)))
			processorLastWrite.WithLabelValues(p.cfg.ExporterID).SetToCurrentTime()
			event.EmitTerminal(operationevent.ResultSuccess)
			break
		}
		processorBatchAttempts.WithLabelValues(p.cfg.ExporterID, "failed").Inc()
		event.SetReason("clickhouse_write_failed")
		event.EnrichError(err)
		log.Warn("ClickHouse frozen batch write failed", "exporterID", p.cfg.ExporterID, "attempt", attempt+1, "retryIn", backoff, "errorType", fmt.Sprintf("%T", err))
		wait := backoff/2 + time.Duration(mathrand.Int64N(int64(backoff)))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			event.SetFailureCount(1)
			result := operationevent.ResultCanceled
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				result = operationevent.ResultTimedOut
			}
			event.EmitTerminal(result)
			return ctx.Err()
		case <-timer.C:
			event.EmitTerminal(operationevent.ResultRetrying)
		}
		backoff = min(backoff*2, p.cfg.RetryMax)
	}
	if p.pending.LastCursor != "" {
		p.state.SafeCursor = p.pending.LastCursor
	}
	p.pending = nil
	p.bytes = 0
	processorBufferedRows.WithLabelValues(p.cfg.ExporterID).Set(0)
	processorBufferedBytes.WithLabelValues(p.cfg.ExporterID).Set(0)
	return nil
}

func (p *Processor) appendEvent(ctx context.Context, event processruntime.EventEnvelope, snapshot *pb.AnalyticsRecord) error {
	var data map[string]any
	if len(event.Data) > 0 {
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("decode %s event: %w", event.Kind, err)
		}
	}
	if data == nil {
		data = map[string]any{}
	}
	change := strings.TrimSpace(event.Change)
	if change == "" {
		change = firstString(data, "change")
	}
	if change == "" {
		change = event.Event
	}
	hardDeleted := change == "hard_deleted" || event.Kind == "conversation" && event.Event == "deleted"
	sourceID := resourceSourceID(event.Kind, data)
	resourceID := sourceID
	conversation := firstString(data, "conversation", "conversation_id", "conversationId", "ConversationID", "id", "ID")
	group := firstString(data, "conversation_group", "conversation_group_id", "conversationGroupId", "ConversationGroupID")
	occurred := event.Time.UTC()
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	eid := eventID(p.cfg.ExporterID, event.Cursor, event.Kind, event.Event, change)
	common := Common{ExporterID: p.cfg.ExporterID, BatchID: p.pending.ID, EventID: eid, SourceCursor: event.Cursor, IngestVersion: p.pending.Version, ObservedAt: p.pending.ObservedAt, SchemaVersion: schemaVersion}
	if !p.cfg.exportsResourceKind(event.Kind) {
		if p.cfg.PurgeMode != PurgeExternal && hardDeleted && sourceID != "" {
			p.pending.Purges = append(p.pending.Purges, PurgeRow{
				ExporterID: p.cfg.ExporterID, BatchID: p.pending.ID, PurgeID: eid, EventID: eid,
				ResourceKind: event.Kind, AnalyticsResourceID: resourceID, ConversationID: conversation,
				ConversationGroupID: group, RequestedAt: occurred,
			})
		}
		p.pending.LastCursor = event.Cursor
		p.bytes += len(event.Data) + 128
		return nil
	}
	summaryJSON := []byte(nil)
	if !p.cfg.featureDisabled(event.Kind, disableLifecycleEvents) {
		summaryJSON, _ = json.Marshal(safeSummary(event.Kind, data))
	}
	deleted := event.Event == "deleted" || change == "evicted" || change == "hard_deleted"
	snapshotAvailable := snapshot != nil && snapshot.GetSnapshotAvailable() && !deleted
	if !p.cfg.featureDisabled(event.Kind, disableLifecycleEvents) {
		contentType := firstString(data, "entry_content_type", "contentType", "content_type", "ContentType")
		if contentType == "" && snapshot != nil {
			if entry := snapshot.GetEntry(); entry != nil {
				contentType = entry.GetContentType()
			}
		}
		p.pending.Lifecycle = append(p.pending.Lifecycle, LifecycleRow{
			Common: common, OccurredAt: occurred, ResourceKind: event.Kind, AnalyticsResourceID: resourceID,
			Action: event.Event, Change: change, ConversationID: conversation,
			ConversationGroupID: group, ContentType: contentType,
			MemoryKind: firstString(data, "kind", "memory_kind", "memoryKind", "MemoryKind"), SnapshotAvailable: snapshotAvailable, SummaryJSON: string(summaryJSON),
		})
	}
	if sourceID != "" {
		payload, created, updated, archived, hydratedConversation, hydratedGroup, err := p.hydratedPayload(event.Kind, data, snapshot)
		if err != nil {
			return err
		}
		if len(payload) > p.cfg.MaxRecordBytes {
			return fmt.Errorf("record_too_large: %s payload is %d bytes and record limit is %d", event.Kind, len(payload), p.cfg.MaxRecordBytes)
		}
		if hydratedConversation != "" {
			conversation = hydratedConversation
		}
		if hydratedGroup != "" {
			group = hydratedGroup
		}
		if created.IsZero() {
			created = firstTime(data, "createdAt", "created_at", "CreatedAt")
		}
		if updated.IsZero() {
			updated = firstTime(data, "updatedAt", "updated_at", "UpdatedAt")
		}
		if created.IsZero() {
			created = occurred
		}
		if updated.IsZero() {
			updated = created
		}
		p.pending.Resources = append(p.pending.Resources, ResourceRow{
			Common: common, ResourceID: resourceID, ConversationID: conversation,
			ConversationGroupID: group, ResourceType: event.Kind,
			CreatedAt: created, UpdatedAt: updated, IsArchived: archived || firstBool(data, "archived", "is_archived", "isArchived"),
			IsDeleted: deleted, PayloadJSON: payload,
		})
		p.bytes += len(payload)
		conversationID := conversation
		conversationGroupID := group
		if err := p.applyProjections(ctx, common, resourceID, conversationID, conversationGroupID, event.Kind, data, snapshot, deleted); err != nil {
			return err
		}
		if event.Kind == "conversation" && event.Event == "created" && !deleted && !p.cfg.featureDisabled("conversation", disableLineage) {
			conversationRecord := snapshot.GetConversation()
			parentID := conversationRecord.GetForkedAtConversationId()
			if parentID != "" {
				lineagePayload := map[string]any{
					"ancestorConversationId":   parentID,
					"descendantConversationId": conversationID,
					"depth":                    1,
				}
				if forkedAtEntryID := conversationRecord.GetForkedAtEntryId(); forkedAtEntryID != "" {
					lineagePayload["forkedAtEntryId"] = forkedAtEntryID
				}
				encoded, err := json.Marshal(lineagePayload)
				if err != nil {
					return fmt.Errorf("encode conversation lineage: %w", err)
				}
				lineageResourceID := parentID + "\n" + conversationID
				lineageCommon := common
				lineageCommon.EventID = eventID(p.cfg.ExporterID, event.Cursor, "lineage", "snapshot", lineageResourceID)
				p.pending.Resources = append(p.pending.Resources, ResourceRow{
					Common: lineageCommon, ResourceID: lineageResourceID, ConversationID: conversationID,
					ConversationGroupID: conversationGroupID, ResourceType: "lineage",
					CreatedAt: created, UpdatedAt: updated, PayloadJSON: string(encoded),
				})
				p.bytes += len(encoded) + 128
			}
		}
		if hardDeleted && p.cfg.PurgeMode != PurgeExternal {
			p.pending.Purges = append(p.pending.Purges, PurgeRow{
				ExporterID: p.cfg.ExporterID, BatchID: p.pending.ID, PurgeID: eid, EventID: eid,
				ResourceKind: event.Kind, AnalyticsResourceID: resourceID, ConversationID: conversationID,
				ConversationGroupID: conversationGroupID, RequestedAt: occurred,
			})
		}
	}
	p.pending.LastCursor = event.Cursor
	p.bytes += len(event.Data) + len(summaryJSON) + 256
	return nil
}

func (p *Processor) hydratedPayload(kind string, data map[string]any, snapshot *pb.AnalyticsRecord) (string, time.Time, time.Time, bool, string, string, error) {
	if snapshot == nil || !snapshot.GetSnapshotAvailable() {
		payload, err := p.payload(kind, data)
		return payload, time.Time{}, time.Time{}, false, "", "", err
	}
	raw, err := p.analyticsRecordPayload(snapshot)
	if err != nil {
		return "", time.Time{}, time.Time{}, false, "", "", err
	}
	var created, updated time.Time
	var archived bool
	var conversation, group string
	switch value := snapshot.GetRecord().(type) {
	case *pb.AnalyticsRecord_Conversation:
		created = value.Conversation.GetCreatedAt().AsTime().UTC()
		updated = value.Conversation.GetUpdatedAt().AsTime().UTC()
		archived = value.Conversation.GetArchivedAt() != nil
		conversation, group = value.Conversation.GetId(), value.Conversation.GetConversationGroupId()
	case *pb.AnalyticsRecord_Entry:
		created = value.Entry.GetCreatedAt().AsTime().UTC()
		updated = created
		conversation, group = value.Entry.GetConversationId(), value.Entry.GetConversationGroupId()
	case *pb.AnalyticsRecord_Memory:
		created = value.Memory.GetCreatedAt().AsTime().UTC()
		updated = created
		archived = value.Memory.GetArchivedAt() != nil
	}
	return raw, created, updated, archived, conversation, group, nil
}

func (p *Processor) analyticsRecordPayload(record *pb.AnalyticsRecord) (string, error) {
	payload := map[string]any{}
	switch value := record.GetRecord().(type) {
	case *pb.AnalyticsRecord_Conversation:
		conversation := value.Conversation
		payload["ownerUserId"] = conversation.GetOwnerUserId()
		payload["clientId"] = conversation.GetClientId()
		if conversation.AgentId != nil {
			payload["agentId"] = conversation.GetAgentId()
		}
		if conversation.GetCreatedAt() != nil {
			payload["createdAt"] = conversation.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano)
		}
		if conversation.GetUpdatedAt() != nil {
			payload["updatedAt"] = conversation.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339Nano)
		}
		payload["archived"] = conversation.GetArchivedAt() != nil
		if p.cfg.PayloadMode == PayloadFull && conversation.Title != nil {
			payload["title"] = conversation.GetTitle()
		}
		if conversation.GetMetadata() != nil {
			metadata := conversation.GetMetadata().AsMap()
			if p.cfg.PayloadMode != PayloadFull {
				filtered := make(map[string]any, len(p.cfg.MetadataKeys))
				for _, key := range p.cfg.MetadataKeys {
					if value, ok := metadata[key]; ok {
						filtered[key] = value
					}
				}
				metadata = filtered
			}
			if len(metadata) > 0 {
				payload["metadata"] = metadata
			}
		}
		if conversation.ForkedAtConversationId != nil {
			payload["forkedAtConversationId"] = conversation.GetForkedAtConversationId()
		}
		if conversation.ForkedAtEntryId != nil {
			payload["forkedAtEntryId"] = conversation.GetForkedAtEntryId()
		}
		if conversation.StartedByConversationId != nil {
			payload["startedByConversationId"] = conversation.GetStartedByConversationId()
		}
		if conversation.StartedByEntryId != nil {
			payload["startedByEntryId"] = conversation.GetStartedByEntryId()
		}
	case *pb.AnalyticsRecord_Entry:
		entry := value.Entry
		payload["channel"] = entry.GetChannel().String()
		payload["contentType"] = entry.GetContentType()
		if entry.UserId != nil {
			payload["userId"] = entry.GetUserId()
		}
		if entry.ClientId != nil {
			payload["clientId"] = entry.GetClientId()
		}
		if entry.AgentId != nil {
			payload["agentId"] = entry.GetAgentId()
		}
		if entry.Epoch != nil {
			payload["epoch"] = entry.GetEpoch()
		}
		if entry.Seq != nil {
			payload["seq"] = entry.GetSeq()
		}
		if p.cfg.PayloadMode == PayloadFull && entry.IndexedContent != nil {
			payload["indexedContent"] = entry.GetIndexedContent()
		}
		if entry.IndexedAt != nil {
			payload["indexedAt"] = entry.GetIndexedAt().AsTime().UTC().Format(time.RFC3339Nano)
		}
		if entry.GetCreatedAt() != nil {
			payload["createdAt"] = entry.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano)
		}
		if p.cfg.PayloadMode == PayloadFull && entry.GetContent() != nil {
			payload["content"] = entry.GetContent().AsInterface()
		}
	case *pb.AnalyticsRecord_Memory:
		memory := value.Memory
		payload["kind"] = memory.GetMemoryKind()
		payload["revision"] = memory.GetRevision()
		if memory.GetCreatedAt() != nil {
			payload["createdAt"] = memory.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano)
		}
		if memory.GetExpiresAt() != nil {
			payload["expiresAt"] = memory.GetExpiresAt().AsTime().UTC().Format(time.RFC3339Nano)
		}
		payload["archived"] = memory.GetArchivedAt() != nil
		if p.cfg.PayloadMode == PayloadFull && memory.GetValue() != nil {
			payload["value"] = memory.GetValue().AsMap()
		}
		if p.cfg.PayloadMode == PayloadFull && memory.GetAttributes() != nil {
			payload["attributes"] = memory.GetAttributes().AsMap()
		}
		if len(memory.GetLogicalIdSource()) > 0 {
			payload["logicalMemoryId"] = hex.EncodeToString(memory.GetLogicalIdSource())
		}
		payload["namespace"], payload["key"] = memory.GetNamespace(), memory.GetKey()
	}
	if record.GetReference() != nil {
		payload["sourceId"] = record.GetReference().GetId()
	}
	raw, err := json.Marshal(payload)
	return string(raw), err
}

func (p *Processor) applyProjections(ctx context.Context, common Common, resourceID, conversationID, conversationGroupID, kind string, eventData map[string]any, record *pb.AnalyticsRecord, deleted bool) error {
	if p.projections == nil || len(p.projections.Items) == 0 || (kind != "entry" && kind != "memory") || p.cfg.featureDisabled(kind, disableProjections) {
		return nil
	}
	selector := ""
	if kind == "entry" {
		selector = firstString(eventData, "entry_content_type", "contentType", "content_type", "ContentType")
		if entry := record.GetEntry(); entry != nil {
			selector = entry.GetContentType()
		}
	} else {
		selector = firstString(eventData, "kind", "memoryKind", "memory_kind", "MemoryKind")
		if memory := record.GetMemory(); memory != nil {
			selector = memory.GetMemoryKind()
		}
	}
	for _, projection := range p.projections.Items {
		if projection.Resource != kind || projection.Selector != selector {
			continue
		}
		base := ProjectionRow{Common: common, ResourceID: resourceID, ConversationID: conversationID, ConversationGroupID: conversationGroupID}
		if deleted {
			p.pending.Projections = append(p.pending.Projections, projection.TombstoneRow(base))
			p.bytes += 128
			continue
		}
		if record == nil || !record.GetSnapshotAvailable() {
			if err := p.recordProjectionFailure(common, resourceID, conversationID, conversationGroupID, projection.Name, "snapshot_unavailable"); err != nil {
				return err
			}
			continue
		}
		rows, code := projection.Rows(ctx, base, projectionInput(record))
		if code != "" {
			if err := p.recordProjectionFailure(common, resourceID, conversationID, conversationGroupID, projection.Name, code); err != nil {
				return err
			}
			continue
		}
		p.pending.Projections = append(p.pending.Projections, rows...)
		for _, row := range rows {
			if encoded, err := json.Marshal(row.Values); err == nil {
				p.bytes += len(encoded) + 128
			}
		}
	}
	return nil
}

func (p *Processor) recordProjectionFailure(common Common, resourceID, conversationID, conversationGroupID, projectionName, code string) error {
	processorProjectionResults.WithLabelValues(p.cfg.ExporterID, projectionName, code).Inc()
	if p.cfg.ProjectionFailurePolicy == "stop" {
		return fmt.Errorf("analytics projection %s failed with %s", projectionName, code)
	}
	p.pending.ProjectionFailures = append(p.pending.ProjectionFailures, ProjectionFailureRow{
		ExporterID: p.cfg.ExporterID, BatchID: common.BatchID, EventID: common.EventID,
		AnalyticsResourceID: resourceID, ConversationID: conversationID, ConversationGroupID: conversationGroupID, ProjectionName: projectionName, ErrorCode: code,
		AttemptCount: 1, FirstSeenAt: common.ObservedAt, LastSeenAt: common.ObservedAt, Version: common.IngestVersion,
	})
	p.bytes += 256
	return nil
}

func projectionInput(record *pb.AnalyticsRecord) map[string]any {
	input := map[string]any{}
	switch value := record.GetRecord().(type) {
	case *pb.AnalyticsRecord_Entry:
		entry := value.Entry
		input["resource"] = "entry"
		input["contentType"] = entry.GetContentType()
		input["channel"] = entry.GetChannel().String()
		if entry.GetContent() != nil {
			input["content"] = entry.GetContent().AsInterface()
		}
	case *pb.AnalyticsRecord_Memory:
		memory := value.Memory
		input["resource"] = "memory"
		input["kind"] = memory.GetMemoryKind()
		input["revision"] = memory.GetRevision()
		if memory.GetValue() != nil {
			input["value"] = memory.GetValue().AsMap()
		}
		if memory.GetAttributes() != nil {
			input["attributes"] = memory.GetAttributes().AsMap()
		}
	}
	return input
}

func (p *Processor) payload(kind string, data map[string]any) (string, error) {
	if p.cfg.PayloadMode != PayloadFull {
		value, err := json.Marshal(safeSummary(kind, data))
		return string(value), err
	}
	copy := scrubPayload(data)
	value, err := json.Marshal(copy)
	return string(value), err
}

func resourceSourceID(kind string, data map[string]any) string {
	switch kind {
	case "conversation":
		return firstString(data, "conversation", "id", "ID")
	case "entry":
		return firstString(data, "entry", "id", "ID")
	case "memory":
		return firstString(data, "memory", "memory_id", "memoryId", "id", "ID")
	default:
		return firstString(data, "id", "ID")
	}
}

func safeSummary(kind string, data map[string]any) map[string]any {
	allowed := map[string]bool{
		"change": true, "channel": true, "entry_channel": true,
		"contentType": true, "content_type": true, "entry_content_type": true,
		"role": true, "entry_role": true,
		"kind": true, "memoryKind": true, "memory_kind": true, "revision": true, "archived": true,
		"createdAt": true, "created_at": true, "updatedAt": true, "updated_at": true,
		"expiresAt": true, "expires_at": true, "archivedAt": true, "archived_at": true,
	}
	out := map[string]any{"resourceKind": kind}
	for key, value := range data {
		if allowed[key] {
			out[key] = value
		}
	}
	return out
}

func scrubPayload(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for key, value := range data {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "attachment") && (strings.Contains(lower, "bytes") || strings.Contains(lower, "data")) {
			continue
		}
		out[key] = value
	}
	return out
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key].(string); ok {
			return value
		}
	}
	return ""
}

func firstBool(data map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := data[key].(bool); ok {
			return value
		}
	}
	return false
}

func firstTime(data map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		value, ok := data[key].(string)
		if !ok {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func randomBatchID() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
