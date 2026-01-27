package io.github.chirino.memory.mongo.model;

import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import java.util.Map;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "conversations")
public class MongoConversation {

    @BsonId public String id;

    public byte[] title;
    public String ownerUserId;
    public Map<String, Object> metadata;
    public String conversationGroupId;
    public String forkedAtMessageId;
    public String forkedAtConversationId;
    public Instant createdAt;
    public Instant updatedAt;
    public Instant vectorizedAt;
    public Instant deletedAt;
}
