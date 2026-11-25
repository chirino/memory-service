package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAttachmentSupersededOrError(t *testing.T) {
	t.Parallel()
	id := uuid.New()

	t.Run("not found becomes a conflict, not an inconclusive 404", func(t *testing.T) {
		wrapped := errors.Join(errors.New("context"), &NotFoundError{Resource: "attachment", ID: id.String()})
		var conflict *ConflictError
		require.ErrorAs(t, AttachmentSupersededOrError(id, wrapped), &conflict)
		require.Equal(t, AttachmentSupersededConflictCode, conflict.Code)
		require.Contains(t, conflict.Message, id.String())
		require.Contains(t, conflict.Message, "discarded")
	})

	t.Run("every other error is returned unchanged", func(t *testing.T) {
		for _, err := range []error{
			errors.New("database unavailable"),
			&ForbiddenError{},
			&ConflictError{Message: "other", Code: "other_code"},
			context.Canceled,
		} {
			require.Same(t, err, AttachmentSupersededOrError(id, err))
		}
	})

	t.Run("no error stays no error", func(t *testing.T) {
		require.NoError(t, AttachmentSupersededOrError(id, nil))
	})
}

func TestNewAttachmentIDExistsConflict(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	var conflict *ConflictError
	require.ErrorAs(t, NewAttachmentIDExistsConflict(id), &conflict)
	require.Equal(t, AttachmentIDExistsConflictCode, conflict.Code)
	require.Contains(t, conflict.Message, id.String())
	require.NotEqual(t, AttachmentSupersededConflictCode, conflict.Code, "an existing ID and a superseded attempt are different outcomes")
}

// attemptFailStore is a MemoryStore whose InWriteTx can replay the callback and
// whose MarkAttachmentFailed is scripted. Every other method panics through the
// nil embedded interface, which proves MarkAttachmentAttemptFailed uses nothing else.
type attemptFailStore struct {
	MemoryStore
	replays int
	results []bool
	err     error
	calls   int
	ctxErr  []error
}

func (s *attemptFailStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	var err error
	for i := 0; i <= s.replays; i++ {
		err = fn(ctx)
	}
	return err
}

func (s *attemptFailStore) MarkAttachmentFailed(ctx context.Context, _ string, _, _ uuid.UUID) (bool, error) {
	s.ctxErr = append(s.ctxErr, ctx.Err())
	changed := s.results[s.calls]
	s.calls++
	return changed, s.err
}

func TestMarkAttachmentAttemptFailedReportsChange(t *testing.T) {
	t.Parallel()
	id, attempt := uuid.New(), uuid.New()

	changed, err := MarkAttachmentAttemptFailed(context.Background(), &attemptFailStore{results: []bool{true}}, "alice", id, attempt)
	require.NoError(t, err)
	require.True(t, changed)

	changed, err = MarkAttachmentAttemptFailed(context.Background(), &attemptFailStore{results: []bool{false}}, "alice", id, attempt)
	require.NoError(t, err, "a fence that matches nothing is expected, not an error")
	require.False(t, changed)

	boom := errors.New("database unavailable")
	_, err = MarkAttachmentAttemptFailed(context.Background(), &attemptFailStore{results: []bool{false}, err: boom}, "alice", id, attempt)
	require.ErrorIs(t, err, boom)
}

// The callback can be replayed. The first attempt's `true` is rolled back, so a
// replay that matches nothing must be what the caller sees.
func TestMarkAttachmentAttemptFailedKeepsOnlyTheLastAttemptResult(t *testing.T) {
	t.Parallel()
	store := &attemptFailStore{replays: 1, results: []bool{true, false}}

	changed, err := MarkAttachmentAttemptFailed(context.Background(), store, "alice", uuid.New(), uuid.New())

	require.NoError(t, err)
	require.Equal(t, 2, store.calls)
	require.False(t, changed, "an aborted attempt's result must not leak through a replay")
}

// A client disconnect is what makes an upload fail, so the failure record must
// survive the cancellation that caused it.
func TestMarkAttachmentAttemptFailedIgnoresCallerCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &attemptFailStore{results: []bool{true}}

	changed, err := MarkAttachmentAttemptFailed(ctx, store, "alice", uuid.New(), uuid.New())

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []error{nil}, store.ctxErr, "the store must see a live context")
}
