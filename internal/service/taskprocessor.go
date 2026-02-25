package service

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
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

func (p *TaskProcessor) processBatch(ctx context.Context) {
	tasks, err := p.store.ClaimReadyTasks(ctx, p.batchSize)
	if err != nil {
		log.Error("TaskProcessor: claim tasks failed", "err", err)
		return
	}
	for _, task := range tasks {
		if err := p.executeTask(ctx, task.TaskType, task.TaskBody); err != nil {
			log.Error("TaskProcessor: task failed", "taskId", task.ID, "type", task.TaskType, "err", err)
			if fErr := p.store.FailTask(ctx, task.ID, err.Error(), p.retryDelay); fErr != nil {
				log.Error("TaskProcessor: fail task record failed", "taskId", task.ID, "err", fErr)
			}
		} else {
			if dErr := p.store.DeleteTask(ctx, task.ID); dErr != nil {
				log.Error("TaskProcessor: delete task failed", "taskId", task.ID, "err", dErr)
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
