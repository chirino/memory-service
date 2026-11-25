package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	"github.com/chirino/memory-service/internal/security"
	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// recordingStore captures what the decorator forwards. The embedded interface
// stays nil, so any call the decorator should not make panics.
type recordingStore struct {
	registrystore.MemoryStore
	calls []string
	args  []any
}

func (r *recordingStore) CreateAttachmentWithID(_ context.Context, userID string, id uuid.UUID, a model.Attachment) (*registrystore.CreateAttachmentResult, error) {
	r.calls = append(r.calls, "create")
	r.args = append(r.args, userID, id, a.Status)
	return &registrystore.CreateAttachmentResult{Attachment: &a, Created: true}, nil
}

func (r *recordingStore) FinalizeAttachment(_ context.Context, userID string, id, attempt uuid.UUID, f registrystore.AttachmentFinalize) (*model.Attachment, error) {
	r.calls = append(r.calls, "finalize")
	r.args = append(r.args, userID, id, attempt, f.StorageKey)
	return &model.Attachment{ID: id, Status: "ready"}, nil
}

func (r *recordingStore) MarkAttachmentFailed(_ context.Context, userID string, id, attempt uuid.UUID) (bool, error) {
	r.calls = append(r.calls, "failed")
	r.args = append(r.args, userID, id, attempt)
	return true, nil
}

func (r *recordingStore) DeleteExpiredAttachment(_ context.Context, id uuid.UUID, attempt *uuid.UUID) (bool, error) {
	r.calls = append(r.calls, "delete")
	r.args = append(r.args, id, attempt)
	return true, nil
}

func TestWrapForwardsConditionalAttachmentOperations(t *testing.T) {
	// observe() writes to a package-level histogram that exists only after
	// InitMetrics. It is idempotent, so calling it here is safe.
	security.InitMetrics(nil)
	inner := &recordingStore{}
	wrapped := Wrap(inner)
	ctx := context.Background()
	id, attempt := uuid.New(), uuid.New()
	expires := time.Now()

	created, err := wrapped.CreateAttachmentWithID(ctx, "alice", id, model.Attachment{Status: "uploading", ExpiresAt: &expires})
	require.NoError(t, err)
	require.True(t, created.Created)
	require.Equal(t, "uploading", created.Attachment.Status)

	finalized, err := wrapped.FinalizeAttachment(ctx, "alice", id, attempt, registrystore.AttachmentFinalize{StorageKey: "key"})
	require.NoError(t, err)
	require.Equal(t, id, finalized.ID)

	changed, err := wrapped.MarkAttachmentFailed(ctx, "alice", id, attempt)
	require.NoError(t, err)
	require.True(t, changed)

	deleted, err := wrapped.DeleteExpiredAttachment(ctx, id, &attempt)
	require.NoError(t, err)
	require.True(t, deleted)

	require.Equal(t, []string{"create", "finalize", "failed", "delete"}, inner.calls)
	require.Equal(t, []any{
		"alice", id, "uploading",
		"alice", id, attempt, "key",
		"alice", id, attempt,
		id, &attempt,
	}, inner.args)
}

func TestWrapRecordsLatencyForConditionalAttachmentOperations(t *testing.T) {
	security.InitMetrics(nil)
	wrapped := Wrap(&recordingStore{})
	ctx := context.Background()
	id, attempt := uuid.New(), uuid.New()

	count := func(op string) uint64 {
		return histogramCount(t, op)
	}
	before := map[string]uint64{}
	ops := []string{"create_attachment_with_id", "finalize_attachment", "mark_attachment_failed", "delete_expired_attachment"}
	for _, op := range ops {
		before[op] = count(op)
	}

	_, _ = wrapped.CreateAttachmentWithID(ctx, "u", id, model.Attachment{Status: "uploading"})
	_, _ = wrapped.FinalizeAttachment(ctx, "u", id, attempt, registrystore.AttachmentFinalize{StorageKey: "k"})
	_, _ = wrapped.MarkAttachmentFailed(ctx, "u", id, attempt)
	_, _ = wrapped.DeleteExpiredAttachment(ctx, id, nil)

	for _, op := range ops {
		require.Equal(t, before[op]+1, count(op), "operation %q must be observed exactly once", op)
	}
}

func histogramCount(t *testing.T, op string) uint64 {
	t.Helper()
	observer, err := security.StoreLatency.GetMetricWithLabelValues(op)
	require.NoError(t, err)
	collector, ok := observer.(interface {
		Write(*dto.Metric) error
	})
	require.True(t, ok)
	var m dto.Metric
	require.NoError(t, collector.Write(&m))
	return m.GetHistogram().GetSampleCount()
}
