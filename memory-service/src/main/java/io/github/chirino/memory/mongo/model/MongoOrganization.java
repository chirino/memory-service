package io.github.chirino.memory.mongo.model;

import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import java.util.Map;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "organizations")
public class MongoOrganization {

    @BsonId public String id;

    public String name;
    public String slug;
    public Map<String, Object> metadata;
    public Instant createdAt;
    public Instant updatedAt;
    public Instant deletedAt;
}
