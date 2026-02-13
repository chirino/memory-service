package io.github.chirino.memory.mongo.model;

import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "attachments")
public class MongoAttachment {

    @BsonId public String id;

    public String storageKey;
    public String filename;
    public String contentType;
    public Long size;
    public String sha256;
    public String userId;
    public String entryId;
    public Instant expiresAt;
    public Instant createdAt;
    public Instant deletedAt;
    public String status = "ready";
    public String sourceUrl;
}
