package io.github.chirino.memory.service;

import java.time.Duration;
import java.time.Instant;
import java.util.Set;

/**
 * Represents an async eviction job with its current state.
 */
public class EvictionJob {

    public enum Status {
        PENDING,
        RUNNING,
        COMPLETED,
        FAILED
    }

    private final String id;
    private final Duration retentionPeriod;
    private final Set<String> resourceTypes;
    private final Instant createdAt;
    private volatile Status status;
    private volatile int progress;
    private volatile String error;
    private volatile Instant completedAt;

    public EvictionJob(String id, Duration retentionPeriod, Set<String> resourceTypes) {
        this.id = id;
        this.retentionPeriod = retentionPeriod;
        this.resourceTypes = resourceTypes;
        this.createdAt = Instant.now();
        this.status = Status.PENDING;
        this.progress = 0;
    }

    public String getId() {
        return id;
    }

    public Duration getRetentionPeriod() {
        return retentionPeriod;
    }

    public Set<String> getResourceTypes() {
        return resourceTypes;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }

    public Status getStatus() {
        return status;
    }

    public void setStatus(Status status) {
        this.status = status;
        if (status == Status.COMPLETED || status == Status.FAILED) {
            this.completedAt = Instant.now();
        }
    }

    public int getProgress() {
        return progress;
    }

    public void setProgress(int progress) {
        this.progress = progress;
    }

    public String getError() {
        return error;
    }

    public void setError(String error) {
        this.error = error;
    }

    public Instant getCompletedAt() {
        return completedAt;
    }
}
