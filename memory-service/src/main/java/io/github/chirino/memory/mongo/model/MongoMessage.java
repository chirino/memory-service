package io.github.chirino.memory.mongo.model;

import io.github.chirino.memory.model.MessageChannel;
import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import java.util.List;
import org.bson.codecs.pojo.annotations.BsonId;
import org.bson.codecs.pojo.annotations.BsonIgnore;

@MongoEntity(collection = "messages")
public class MongoMessage {

    @BsonId public String id;

    public String conversationId;
    public String conversationGroupId;
    public String userId;
    public String clientId;
    public MessageChannel channel;
    public Long epoch;
    public byte[] content;
    @BsonIgnore public List<Object> decodedContent;
    public Instant createdAt;
}
