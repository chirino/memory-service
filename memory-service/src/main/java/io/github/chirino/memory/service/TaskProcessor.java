package io.github.chirino.memory.service;

import io.github.chirino.memory.config.TaskRepositorySelector;
import io.github.chirino.memory.config.VectorStoreSelector;
import io.github.chirino.memory.persistence.entity.TaskEntity;
import io.github.chirino.memory.vector.VectorStore;
import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import org.bson.Document;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@ApplicationScoped
public class TaskProcessor {

    private static final Logger LOG = Logger.getLogger(TaskProcessor.class);

    @Inject TaskRepositorySelector taskRepositorySelector;

    @Inject VectorStoreSelector vectorStoreSelector;

    @ConfigProperty(name = "memory-service.tasks.retry-delay", defaultValue = "PT10M")
    Duration retryDelay;

    @ConfigProperty(name = "memory-service.tasks.batch-size", defaultValue = "100")
    int batchSize;

    @Scheduled(every = "${memory-service.tasks.processor-interval:1m}")
    @Transactional
    public void processPendingTasks() {
        List<?> tasks;
        if (taskRepositorySelector.isPostgres()) {
            tasks = taskRepositorySelector.getPostgresRepository().findReadyTasks(batchSize);
        } else {
            tasks = taskRepositorySelector.getMongoRepository().findReadyTasks(batchSize);
        }

        for (Object task : tasks) {
            try {
                String taskId;
                String taskType;
                Map<String, Object> taskBody;

                if (task instanceof TaskEntity) {
                    TaskEntity te = (TaskEntity) task;
                    taskId = te.getId().toString();
                    taskType = te.getTaskType();
                    taskBody = te.getTaskBody();
                    executeTask(taskType, taskBody);
                    taskRepositorySelector.getPostgresRepository().delete(te);
                } else if (task instanceof Document) {
                    Document doc = (Document) task;
                    taskId = doc.getString("_id");
                    taskType = doc.getString("taskType");
                    Document taskBodyDoc = doc.get("taskBody", Document.class);
                    executeTask(taskType, convertDocumentToMap(taskBodyDoc));
                    taskRepositorySelector.getMongoRepository().deleteTask(taskId);
                } else {
                    LOG.warn("Unknown task type: " + task.getClass().getName());
                    continue;
                }

                LOG.debugf("Task %s completed successfully", taskId);
            } catch (Exception e) {
                LOG.warnf(e, "Task failed, scheduling retry");
                String taskId;
                if (task instanceof TaskEntity) {
                    TaskEntity te = (TaskEntity) task;
                    taskId = te.getId().toString();
                    taskRepositorySelector
                            .getPostgresRepository()
                            .markFailed(te, e.getMessage(), retryDelay);
                } else if (task instanceof Document) {
                    Document doc = (Document) task;
                    taskId = doc.getString("_id");
                    taskRepositorySelector
                            .getMongoRepository()
                            .markFailed(taskId, e.getMessage(), retryDelay);
                } else {
                    continue;
                }
            }
        }

        if (!tasks.isEmpty()) {
            LOG.infof("Processed %d tasks", tasks.size());
        }
    }

    private void executeTask(String taskType, Map<String, Object> taskBody) {
        VectorStore vectorStore = vectorStoreSelector.getVectorStore();
        switch (taskType) {
            case "vector_store_delete" -> {
                String groupId = (String) taskBody.get("conversationGroupId");
                vectorStore.deleteByConversationGroupId(groupId);
            }
            default -> throw new IllegalArgumentException("Unknown task type: " + taskType);
        }
    }

    @SuppressWarnings("unchecked")
    private Map<String, Object> convertDocumentToMap(Document doc) {
        if (doc == null) {
            return Map.of();
        }
        // Convert Document to Map recursively
        Map<String, Object> result = new java.util.HashMap<>();
        for (String key : doc.keySet()) {
            Object value = doc.get(key);
            if (value instanceof Document) {
                result.put(key, convertDocumentToMap((Document) value));
            } else {
                result.put(key, value);
            }
        }
        return result;
    }
}
