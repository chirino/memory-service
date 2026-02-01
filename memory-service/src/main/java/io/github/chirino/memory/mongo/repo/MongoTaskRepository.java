package io.github.chirino.memory.mongo.repo;

import com.mongodb.client.MongoClient;
import com.mongodb.client.MongoCollection;
import com.mongodb.client.model.Filters;
import com.mongodb.client.model.FindOneAndUpdateOptions;
import com.mongodb.client.model.ReturnDocument;
import com.mongodb.client.model.Sorts;
import com.mongodb.client.model.Updates;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.bson.Document;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class MongoTaskRepository {

    @Inject MongoClient mongoClient;

    @ConfigProperty(name = "memory-service.tasks.stale-claim-timeout", defaultValue = "PT5M")
    Duration staleClaimTimeout;

    private MongoCollection<Document> getCollection() {
        return mongoClient.getDatabase("memory").getCollection("tasks");
    }

    /**
     * Atomically find and claim a ready task using findOneAndUpdate.
     * Safe for concurrent execution across multiple replicas.
     *
     * Each call claims ONE task by setting processingAt to now.
     * Returns null if no tasks are ready.
     */
    public Document claimNextTask() {
        Instant now = Instant.now();
        Instant staleClaimCutoff = now.minus(staleClaimTimeout);

        return getCollection()
                .findOneAndUpdate(
                        Filters.and(
                                Filters.lte("retryAt", now),
                                Filters.or(
                                        Filters.eq("processingAt", null),
                                        Filters.lt("processingAt", staleClaimCutoff))),
                        Updates.set("processingAt", now),
                        new FindOneAndUpdateOptions()
                                .sort(Sorts.ascending("retryAt"))
                                .returnDocument(ReturnDocument.AFTER));
    }

    /**
     * Find multiple ready tasks by claiming them one at a time.
     */
    public List<Document> findReadyTasks(int limit) {
        List<Document> tasks = new ArrayList<>();
        for (int i = 0; i < limit; i++) {
            Document task = claimNextTask();
            if (task == null) break;
            tasks.add(task);
        }
        return tasks;
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
        Document task =
                new Document()
                        .append("_id", UUID.randomUUID().toString())
                        .append("taskName", taskName)
                        .append("taskType", taskType)
                        .append("taskBody", new Document(body))
                        .append("createdAt", Instant.now())
                        .append("retryAt", Instant.now())
                        .append("processingAt", null)
                        .append("lastError", null)
                        .append("retryCount", 0);
        getCollection().insertOne(task);
    }

    /**
     * Find a task by its unique name.
     */
    public Document findByName(String taskName) {
        return getCollection().find(Filters.eq("taskName", taskName)).first();
    }

    /**
     * Delete a completed task.
     */
    public void deleteTask(String taskId) {
        getCollection().deleteOne(Filters.eq("_id", taskId));
    }

    /**
     * Mark a task as failed and schedule retry.
     */
    public void markFailed(String taskId, String error, Duration retryDelay) {
        getCollection()
                .updateOne(
                        Filters.eq("_id", taskId),
                        Updates.combine(
                                Updates.set("lastError", error),
                                Updates.inc("retryCount", 1),
                                Updates.set("retryAt", Instant.now().plus(retryDelay)),
                                Updates.set("processingAt", null) // Release claim
                                ));
    }
}
