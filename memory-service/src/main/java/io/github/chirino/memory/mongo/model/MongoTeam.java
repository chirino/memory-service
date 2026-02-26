package io.github.chirino.memory.mongo.model;

import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "teams")
public class MongoTeam {

    @BsonId public String id;

    public String organizationId;
    public String name;
    public String slug;
    public Instant createdAt;
    public Instant updatedAt;
    public Instant deletedAt;
}
