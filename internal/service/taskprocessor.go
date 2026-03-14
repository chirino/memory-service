package service

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"github.com/chirino/memory-service/internal/model"
	registrystore "github.com/chirino/memory-service/internal/registry/store"
	registryvector "github.com/chirino/memory-service/internal/registry/vector"
	"github.com/google/uuid"
)

// TaskProcessor polls for ready tasks and executes them. It processes
// vector_store_delete tasks by calling the vector store's delete method.
type TaskProcessor struct {
	store      registrystore.MemoryStore
	vector     registryvector.VectorStore
	interval   time.Duration
	retryDelay time.Duration
	batchSize  int
}

// NewTaskProcessor creates a new background task processor.
func NewTaskProcessor(store registrystore.MemoryStore, vector registryvector.VectorStore) *TaskProcessor {
	return &TaskProcessor{
		store:      store,
		vector:     vector,
		interval:   1 * time.Minute,
		retryDelay: 10 * time.Minute,
		batchSize:  100,
	}
}

// Start begins the periodic task processing loop. Returns when ctx is cancelled.
func (p *TaskProcessor) Start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.processBatch(ctx)
		}
	}
}

func (p *TaskProcessor) logErr(ctx context.Context, msg string, args ...any) {
	if ctx.Err() != nil {
		return // shutting down — suppress errors after context cancellation
	}
	log.Error(msg, args...)
}

func (p *TaskProcessor) processBatch(ctx context.Context) {
	var tasks []model.Task
	err := p.store.InWriteTx(ctx, func(writeCtx context.Context) error {
		var err error
		tasks, err = p.store.ClaimReadyTasks(writeCtx, p.batchSize)
		return err
	})
	if err != nil {
		p.logErr(ctx, "TaskProcessor: claim tasks failed", "err", err)
		return
	}
	for _, task := range tasks {
		if err := p.executeTask(ctx, task.TaskType, task.TaskBody); err != nil {
			p.logErr(ctx, "TaskProcessor: task failed", "taskId", task.ID, "type", task.TaskType, "err", err)
			if fErr := p.store.InWriteTx(ctx, func(writeCtx context.Context) error {
				return p.store.FailTask(writeCtx, task.ID, err.Error(), p.retryDelay)
			}); fErr != nil {
				p.logErr(ctx, "TaskProcessor: fail task record failed", "taskId", task.ID, "err", fErr)
			}
		} else {
			if dErr := p.store.InWriteTx(ctx, func(writeCtx context.Context) error {
				return p.store.DeleteTask(writeCtx, task.ID)
			}); dErr != nil {
				p.logErr(ctx, "TaskProcessor: delete task failed", "taskId", task.ID, "err", dErr)
			}
		}
	}
}

func (p *TaskProcessor) executeTask(ctx context.Context, taskType string, body map[string]any) error {
	switch taskType {
	case "vector_store_delete":
		return p.executeVectorStoreDelete(ctx, body)
	default:
		return fmt.Errorf("unknown task type: %s", taskType)
	}
}

func (p *TaskProcessor) executeVectorStoreDelete(ctx context.Context, body map[string]any) error {
	if p.vector == nil || !p.vector.IsEnabled() {
		return nil // skip silently — vector store not configured
	}
	groupIDStr, ok := body["conversationGroupId"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid conversationGroupId in task body")
	}
	groupID, err := uuid.Parse(groupIDStr)
	if err != nil {
		return fmt.Errorf("invalid conversationGroupId %q: %w", groupIDStr, err)
	}
	return p.vector.DeleteByConversationGroupID(ctx, groupID)
}
