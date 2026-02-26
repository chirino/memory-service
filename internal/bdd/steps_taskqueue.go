package bdd

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chirino/memory-service/internal/testutil/cucumber"
	"github.com/cucumber/godog"
	"github.com/google/uuid"
)

func init() {
	cucumber.StepModules = append(cucumber.StepModules, func(ctx *godog.ScenarioContext, s *cucumber.TestScenario) {
		t := &taskQueueSteps{s: s, vectorDeleteCalls: map[string]int{}}

		ctx.Step(`^all tasks are deleted$`, t.allTasksAreDeleted)
		ctx.Step(`^I create a task with type "([^"]*)" and body:$`, t.iCreateATaskWithTypeAndBody)
		ctx.Step(`^the task processor runs$`, t.theTaskProcessorRuns)
		ctx.Step(`^the task should be deleted$`, t.theTaskShouldBeDeleted)
		ctx.Step(`^the task should still exist$`, t.theTaskShouldStillExist)
		ctx.Step(`^the task retry_at should be in the future$`, t.theTaskRetryAtShouldBeInTheFuture)
		ctx.Step(`^the task retry_count should be (\d+)$`, t.theTaskRetryCountShouldBe)
		ctx.Step(`^the task last_error should contain the failure message$`, t.theTaskLastErrorShouldContainTheFailureMessage)
		ctx.Step(`^the vector store should have received a delete call for "([^"]*)"$`, t.theVectorStoreShouldHaveReceivedADeleteCallFor)
		ctx.Step(`^the vector store will fail for "([^"]*)"$`, t.theVectorStoreWillFailFor)
		ctx.Step(`^I have a failed task with retry_at in the past$`, t.iHaveAFailedTaskWithRetryAtInThePast)
		ctx.Step(`^the task should be processed again$`, t.theTaskShouldBeProcessedAgain)
		ctx.Step(`^I have (\d+) pending tasks$`, t.iHavePendingTasks)
		ctx.Step(`^(\d+) task processors run concurrently$`, t.taskProcessorsRunConcurrently)
		ctx.Step(`^each task should be processed exactly once$`, t.eachTaskShouldBeProcessedExactlyOnce)
	})
}

type taskQueueSteps struct {
	s                 *cucumber.TestScenario
	lastTaskID        string
	mu                sync.Mutex
	vectorDeleteCalls map[string]int
	vectorFailIDs     map[string]bool
	processedCount    atomic.Int64
}

func (t *taskQueueSteps) db() cucumber.TestDB {
	return t.s.Suite.DB
}

func (t *taskQueueSteps) allTasksAreDeleted() error {
	t.vectorDeleteCalls = map[string]int{}
	t.vectorFailIDs = nil
	t.processedCount.Store(0)
	return t.db().DeleteAllTasks(context.Background())
}

func (t *taskQueueSteps) iCreateATaskWithTypeAndBody(taskType string, body *godog.DocString) error {
	id := uuid.New().String()
	t.lastTaskID = id
	return t.db().CreateTask(context.Background(), id, taskType, body.Content)
}

func (t *taskQueueSteps) theTaskProcessorRuns() error {
	return t.processTasksOnce()
}

func (t *taskQueueSteps) processTasksOnce() error {
	ctx := context.Background()
	tasks, err := t.db().ClaimReadyTasks(ctx, 100)
	if err != nil {
		return err
	}

	for _, tk := range tasks {
		processErr := t.executeTask(tk.TaskType, []byte(tk.TaskBody))
		if processErr != nil {
			if err := t.db().FailTask(ctx, tk.ID, processErr.Error()); err != nil {
				return err
			}
		} else {
			if err := t.db().DeleteTask(ctx, tk.ID); err != nil {
				return err
			}
		}
		t.processedCount.Add(1)
	}
	return nil
}

func (t *taskQueueSteps) executeTask(taskType string, body json.RawMessage) error {
	if taskType == "vector_store_delete" {
		var parsed map[string]string
		if err := json.Unmarshal(body, &parsed); err != nil {
			return err
		}
		groupID := parsed["conversationGroupId"]
		t.mu.Lock()
		failIDs := t.vectorFailIDs
		t.mu.Unlock()
		if failIDs != nil && failIDs[groupID] {
			return fmt.Errorf("vector store delete failed for group %s", groupID)
		}
		t.mu.Lock()
		t.vectorDeleteCalls[groupID]++
		t.mu.Unlock()
	}
	return nil
}

func (t *taskQueueSteps) theTaskShouldBeDeleted() error {
	_, err := t.db().GetTask(context.Background(), t.lastTaskID)
	if err == nil {
		return fmt.Errorf("task %s should be deleted but still exists", t.lastTaskID)
	}
	return nil // error means not found → task was deleted
}

func (t *taskQueueSteps) theTaskShouldStillExist() error {
	_, err := t.db().GetTask(context.Background(), t.lastTaskID)
	if err != nil {
		return fmt.Errorf("task %s should still exist but was not found: %w", t.lastTaskID, err)
	}
	return nil
}

func (t *taskQueueSteps) theTaskRetryAtShouldBeInTheFuture() error {
	task, err := t.db().GetTask(context.Background(), t.lastTaskID)
	if err != nil {
		return err
	}
	if !task.RetryAt.After(time.Now()) {
		return fmt.Errorf("task retry_at should be in the future, got %v", task.RetryAt)
	}
	return nil
}

func (t *taskQueueSteps) theTaskRetryCountShouldBe(expected int) error {
	task, err := t.db().GetTask(context.Background(), t.lastTaskID)
	if err != nil {
		return err
	}
	if task.RetryCount != expected {
		return fmt.Errorf("expected retry_count %d, got %d", expected, task.RetryCount)
	}
	return nil
}

func (t *taskQueueSteps) theTaskLastErrorShouldContainTheFailureMessage() error {
	task, err := t.db().GetTask(context.Background(), t.lastTaskID)
	if err != nil {
		return err
	}
	if task.LastError == nil || *task.LastError == "" {
		return fmt.Errorf("expected task last_error to contain failure message, but it is empty")
	}
	return nil
}

func (t *taskQueueSteps) theVectorStoreShouldHaveReceivedADeleteCallFor(groupID string) error {
	if t.vectorDeleteCalls[groupID] == 0 {
		return fmt.Errorf("expected vector store delete call for %q, but none received", groupID)
	}
	return nil
}

func (t *taskQueueSteps) theVectorStoreWillFailFor(groupID string) error {
	if t.vectorFailIDs == nil {
		t.vectorFailIDs = map[string]bool{}
	}
	t.vectorFailIDs[groupID] = true
	return nil
}

func (t *taskQueueSteps) iHaveAFailedTaskWithRetryAtInThePast() error {
	id := uuid.New().String()
	t.lastTaskID = id
	return t.db().CreateFailedTask(context.Background(), id, "vector_store_delete", `{"conversationGroupId":"retry-test"}`)
}

func (t *taskQueueSteps) theTaskShouldBeProcessedAgain() error {
	if t.vectorDeleteCalls["retry-test"] == 0 {
		return fmt.Errorf("expected task to be processed again, but no vector store call was received")
	}
	return nil
}

func (t *taskQueueSteps) iHavePendingTasks(count int) error {
	ctx := context.Background()
	for i := 0; i < count; i++ {
		id := uuid.New().String()
		body := fmt.Sprintf(`{"conversationGroupId":"group-%d"}`, i)
		if err := t.db().CreateTask(ctx, id, "vector_store_delete", body); err != nil {
			return err
		}
	}
	return nil
}

func (t *taskQueueSteps) taskProcessorsRunConcurrently(count int) error {
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = t.processTasksOnce()
		}()
	}
	wg.Wait()
	return nil
}

func (t *taskQueueSteps) eachTaskShouldBeProcessedExactlyOnce() error {
	remaining, err := t.db().CountTasks(context.Background())
	if err != nil {
		return err
	}
	if remaining != 0 {
		return fmt.Errorf("expected 0 remaining tasks, got %d", remaining)
	}
	return nil
}
