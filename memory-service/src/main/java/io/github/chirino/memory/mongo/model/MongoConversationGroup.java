package io.github.chirino.memory.mongo.model;

import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "conversation_groups")
public class MongoConversationGroup {

    @BsonId public String id;

    public Instant createdAt;
}
