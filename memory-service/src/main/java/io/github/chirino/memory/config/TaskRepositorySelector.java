package io.github.chirino.memory.config;

import io.github.chirino.memory.mongo.repo.MongoTaskRepository;
import io.github.chirino.memory.persistence.repo.TaskRepository;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class TaskRepositorySelector {

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject TaskRepository postgresTaskRepository;

    @Inject MongoTaskRepository mongoTaskRepository;

    public boolean isPostgres() {
        String type = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        return "postgres".equals(type);
    }

    public TaskRepository getPostgresRepository() {
        return postgresTaskRepository;
    }

    public MongoTaskRepository getMongoRepository() {
        return mongoTaskRepository;
    }
}
