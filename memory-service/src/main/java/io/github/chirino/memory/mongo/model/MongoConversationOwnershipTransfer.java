package io.github.chirino.memory.mongo.model;

import io.github.chirino.memory.persistence.entity.ConversationOwnershipTransferEntity.TransferStatus;
import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "conversation_ownership_transfers")
public class MongoConversationOwnershipTransfer {

    @BsonId public String id;

    public String conversationGroupId;
    public String fromUserId;
    public String toUserId;
    public TransferStatus status;
    public Instant createdAt;
    public Instant updatedAt;
}
