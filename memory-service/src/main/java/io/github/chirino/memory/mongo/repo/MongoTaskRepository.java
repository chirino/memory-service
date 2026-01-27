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
        Document task =
                new Document()
                        .append("_id", UUID.randomUUID().toString())
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
