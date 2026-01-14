package io.github.chirino.memory;

import io.quarkus.test.junit.QuarkusTestProfile;
import java.util.Map;

public class MongoRedisTestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.of(
                "memory-service.datastore.type", "mongo",
                "memory-service.vector.type", "mongodb",
                "memory-service.response-resumer", "redis",
                "quarkus.mongodb.devservices.enabled", "true",
                "quarkus.redis.devservices.enabled", "true",
                "quarkus.liquibase-mongodb.migrate-at-start", "true",
                "quarkus.liquibase-mongodb.change-log",
                        "db/changelog-mongodb/db.changelog-master.yaml");
    }
}
