package io.github.chirino.memory.mongo.model;

import io.quarkus.mongodb.panache.common.MongoEntity;
import java.time.Instant;
import org.bson.codecs.pojo.annotations.BsonId;

@MongoEntity(collection = "team_members")
public class MongoTeamMember {

    @BsonId public String id; // teamId:userId

    public String teamId;
    public String userId;
    public Instant createdAt;
}
