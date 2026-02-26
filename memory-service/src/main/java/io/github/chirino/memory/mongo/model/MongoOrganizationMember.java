package io.github.chirino.memory.mongo.model;

import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "organization_members")
public class MongoOrganizationMember {

    @BsonId public String id; // orgId:userId

    public String organizationId;
    public String userId;
    public String role;
    public Instant createdAt;
}
