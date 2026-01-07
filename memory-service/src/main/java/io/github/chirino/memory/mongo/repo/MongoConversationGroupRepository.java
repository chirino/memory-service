package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.mongo.model.MongoConversationGroup;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class MongoConversationGroupRepository
        implements PanacheMongoRepositoryBase<MongoConversationGroup, String> {}
