package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.mongo.model.MongoConversation;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class MongoConversationRepository
        implements PanacheMongoRepositoryBase<MongoConversation, String> {}
