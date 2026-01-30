package io.github.chirino.memory.mongo.model;

import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import org.bson.codecs.pojo.annotations.BsonId;

/**
 * Represents a pending ownership transfer request.
 * Transfers are always "pending" while they exist; accepted/rejected transfers are hard deleted.
 */
@MongoEntity(collection = "conversation_ownership_transfers")
public class MongoConversationOwnershipTransfer {

    @BsonId public String id;

    public String conversationGroupId;
    public String fromUserId;
    public String toUserId;
    public Instant createdAt;
}
