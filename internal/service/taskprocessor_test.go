package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
)

func TestTaskProcessorEmitsOneTerminalEventPerClaimedAttempt(t *testing.T) {
	tests := []struct {
		name       string
		executeErr error
		failErr    error
		deleteErr  error
		cancel     bool
		deadline   bool
		wantResult string
		wantReason string
		pointLogs  []string
	}{
		{name: "success", wantResult: "success"},
		{name: "retry scheduled", executeErr: errors.New("private provider response"), wantResult: "retrying", wantReason: "retry_scheduled", pointLogs: []string{"TaskProcessor: task failed"}},
		{name: "retry persistence failure", executeErr: errors.New("private provider response"), failErr: errors.New("private database error"), wantResult: "failed", wantReason: "retry_persistence_failed", pointLogs: []string{"TaskProcessor: task failed", "TaskProcessor: fail task record failed"}},
		{name: "cleanup failure", deleteErr: errors.New("private database error"), wantResult: "failed", wantReason: "task_cleanup_failed", pointLogs: []string{"TaskProcessor: delete task failed"}},
		{name: "cleanup cancellation", deleteErr: fmt.Errorf("delete interrupted: %w", context.Canceled), wantResult: "failed", wantReason: "task_cleanup_failed", pointLogs: []string{"TaskProcessor: delete task failed"}},
		{name: "cleanup deadline", deleteErr: fmt.Errorf("delete interrupted: %w", context.DeadlineExceeded), wantResult: "failed", wantReason: "task_cleanup_failed", pointLogs: []string{"TaskProcessor: delete task failed"}},
		{name: "cancellation", executeErr: context.Canceled, cancel: true, wantResult: "canceled", wantReason: "shutdown"},
		{name: "wrapped cancellation", executeErr: fmt.Errorf("vector request stopped: %w", context.Canceled), wantResult: "canceled", wantReason: "shutdown"},
		{name: "deadline", executeErr: context.DeadlineExceeded, deadline: true, wantResult: "timed_out", wantReason: "deadline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			log.SetOutput(&output)
			log.SetReportTimestamp(false)
			t.Cleanup(func() {
				log.SetOutput(os.Stderr)
				log.SetReportTimestamp(true)
			})

			taskID := uuid.New()
			store := &taskProcessorTestStore{
				tasks: []model.Task{{
					ID: taskID, TaskType: "vector_store_delete", RetryCount: 2,
					TaskBody: map[string]any{"conversationGroupId": uuid.NewString()},
				}},
				failErr: tt.failErr, deleteErr: tt.deleteErr,
			}
			processor := NewTaskProcessor(store, &taskProcessorTestVector{err: tt.executeErr})
			ctx := context.Background()
			if tt.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else if tt.deadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			processor.processBatch(ctx)

			text := output.String()
			if strings.Count(text, "phase=complete") != 1 {
				t.Fatalf("expected one terminal event, got:\n%s", text)
			}
			for _, expected := range []string{"job.vector_store_delete", "taskID=" + taskID.String(), "retryAttempt=3", "result=" + tt.wantResult} {
				if !strings.Contains(text, expected) {
					t.Fatalf("event missing %q:\n%s", expected, text)
				}
			}
			if tt.wantReason != "" && !strings.Contains(text, "reason="+tt.wantReason) {
				t.Fatalf("event missing reason %q:\n%s", tt.wantReason, text)
			}
			for _, pointLog := range tt.pointLogs {
				if !strings.Contains(text, pointLog) {
					t.Fatalf("missing diagnostic point log %q:\n%s", pointLog, text)
				}
			}
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(line, "job.vector_store_delete") &&
					(strings.Contains(line, "private provider response") || strings.Contains(line, "private database error")) {
					t.Fatalf("raw error leaked into canonical event:\n%s", line)
				}
			}
		})
	}
}

type taskProcessorTestStore struct {
	registrystore.MemoryStore
	tasks     []model.Task
	failErr   error
	deleteErr error
}

func (s *taskProcessorTestStore) InWriteTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func (s *taskProcessorTestStore) ClaimReadyTasks(context.Context, int) ([]model.Task, error) {
	return s.tasks, nil
}

func (s *taskProcessorTestStore) FailTask(context.Context, uuid.UUID, string, time.Duration) error {
	return s.failErr
}

func (s *taskProcessorTestStore) DeleteTask(context.Context, uuid.UUID) error {
	return s.deleteErr
}

type taskProcessorTestVector struct {
	err error
}

func (v *taskProcessorTestVector) Search(context.Context, []float32, []uuid.UUID, int) ([]registryvector.VectorSearchResult, error) {
	return nil, nil
}
func (v *taskProcessorTestVector) Upsert(context.Context, []registryvector.UpsertRequest) error {
	return nil
}
func (v *taskProcessorTestVector) DeleteByConversationGroupID(context.Context, uuid.UUID) error {
	return v.err
}
func (v *taskProcessorTestVector) IsEnabled() bool { return true }
func (v *taskProcessorTestVector) Name() string    { return "test" }
