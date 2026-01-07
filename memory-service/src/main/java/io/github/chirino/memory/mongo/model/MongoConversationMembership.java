package io.github.chirino.memory.mongo.model;

import io.github.chirino.memory.model.AccessLevel;
import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "conversation_memberships")
public class MongoConversationMembership {

    @BsonId public String id; // conversationId:userId

    public String conversationGroupId;
    public String userId;
    public AccessLevel accessLevel;
    public Instant createdAt;
}
