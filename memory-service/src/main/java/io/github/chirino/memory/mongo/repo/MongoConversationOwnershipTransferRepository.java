package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.mongo.model.MongoConversationOwnershipTransfer;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class MongoConversationOwnershipTransferRepository
        implements PanacheMongoRepositoryBase<MongoConversationOwnershipTransfer, String> {}
