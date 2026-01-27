package io.github.chirino.memory.persistence.entity;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.PrePersist;
import jakarta.persistence.Table;
import java.time.OffsetDateTime;
import java.util.Map;
import java.util.UUID;
import org.hibernate.annotations.JdbcTypeCode;
import org.hibernate.type.SqlTypes;

@Entity
@Table(name = "tasks")
public class TaskEntity {

    @Id
    @Column(name = "id", nullable = false, updatable = false)
    private UUID id;

    @Column(name = "task_type", nullable = false)
    private String taskType;

    @JdbcTypeCode(SqlTypes.JSON)
    @Column(name = "task_body", nullable = false, columnDefinition = "jsonb")
    private Map<String, Object> taskBody;

    @Column(name = "created_at", nullable = false)
    private OffsetDateTime createdAt;

    @Column(name = "retry_at", nullable = false)
    private OffsetDateTime retryAt;

    @Column(name = "last_error")
    private String lastError;

    @Column(name = "retry_count", nullable = false)
    private int retryCount = 0;

    @PrePersist
    public void prePersist() {
        if (id == null) {
            id = UUID.randomUUID();
        }
        if (createdAt == null) {
            createdAt = OffsetDateTime.now();
        }
        if (retryAt == null) {
            retryAt = OffsetDateTime.now();
        }
    }

    // Getters and setters
    public UUID getId() {
        return id;
    }

    public void setId(UUID id) {
        this.id = id;
    }

    public String getTaskType() {
        return taskType;
    }

    public void setTaskType(String taskType) {
        this.taskType = taskType;
    }

    public Map<String, Object> getTaskBody() {
        return taskBody;
    }

    public void setTaskBody(Map<String, Object> taskBody) {
        this.taskBody = taskBody;
    }

    public OffsetDateTime getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(OffsetDateTime createdAt) {
        this.createdAt = createdAt;
    }

    public OffsetDateTime getRetryAt() {
        return retryAt;
    }

    public void setRetryAt(OffsetDateTime retryAt) {
        this.retryAt = retryAt;
    }

    public String getLastError() {
        return lastError;
    }

    public void setLastError(String lastError) {
        this.lastError = lastError;
    }

    public int getRetryCount() {
        return retryCount;
    }

    public void setRetryCount(int retryCount) {
        this.retryCount = retryCount;
    }
}
