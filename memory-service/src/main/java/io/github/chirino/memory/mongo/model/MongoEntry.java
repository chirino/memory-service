package io.github.chirino.memory.mongo.model;

import io.github.chirino.memory.model.Channel;
import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import java.util.List;
import org.bson.codecs.pojo.annotations.BsonId;
import org.bson.codecs.pojo.annotations.BsonIgnore;

@MongoEntity(collection = "entries")
public class MongoEntry {

    @BsonId public String id;

    public String conversationId;
    public String conversationGroupId;
    public String userId;
    public String clientId;
    public Channel channel;
    public Long epoch;
    public String contentType;
    public byte[] content;
    @BsonIgnore public List<Object> decodedContent;
    public String indexedContent;
    public Instant indexedAt;
    public Instant createdAt;
}
