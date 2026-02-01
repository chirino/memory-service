package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.persistence.entity.TaskEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.persistence.EntityManager;
import java.time.OffsetDateTime;
import java.util.List;
import java.util.Map;
import java.util.UUID;

@ApplicationScoped
public class TaskRepository implements PanacheRepositoryBase<TaskEntity, UUID> {

    @Inject EntityManager entityManager;

    /**
     * Find and lock ready tasks using FOR UPDATE SKIP LOCKED.
     * Safe for concurrent execution across multiple replicas.
     */
    @SuppressWarnings("unchecked")
    public List<TaskEntity> findReadyTasks(int limit) {
        return entityManager
                .createNativeQuery(
                        "SELECT * FROM tasks "
                                + "WHERE retry_at <= NOW() "
                                + "ORDER BY retry_at "
                                + "LIMIT :limit "
                                + "FOR UPDATE SKIP LOCKED",
                        TaskEntity.class)
                .setParameter("limit", limit)
                .getResultList();
    }

    /**
     * Create a new task for background processing.
     */
    public void createTask(String taskType, Map<String, Object> body) {
        createTask(null, taskType, body);
    }

    /**
     * Create a named task (singleton/idempotent).
     * If a task with the given name already exists, this is a no-op.
     */
    public void createTask(String taskName, String taskType, Map<String, Object> body) {
        if (taskName != null && findByName(taskName) != null) {
            return; // Task already exists, idempotent no-op
        }
        TaskEntity task = new TaskEntity();
        task.setId(UUID.randomUUID());
        task.setTaskName(taskName);
        task.setTaskType(taskType);
        task.setTaskBody(body);
        task.setCreatedAt(OffsetDateTime.now());
        task.setRetryAt(OffsetDateTime.now());
        persist(task);
    }

    /**
     * Find a task by its unique name.
     */
    public TaskEntity findByName(String taskName) {
        return find("taskName", taskName).firstResult();
    }

    /**
     * Mark a task as failed and schedule retry.
     */
    public void markFailed(TaskEntity task, String error, java.time.Duration retryDelay) {
        task.setLastError(error);
        task.setRetryCount(task.getRetryCount() + 1);
        task.setRetryAt(OffsetDateTime.now().plus(retryDelay));
        persist(task);
    }
}
