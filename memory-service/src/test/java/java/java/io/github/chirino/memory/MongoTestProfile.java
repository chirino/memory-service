package io.github.chirino.memory;

import io.quarkus.test.junit.QuarkusTestProfile;
import java.util.Map;

public class MongoTestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.of(
                "memory.datastore.type", "mongo",
                "memory.vector.type", "mongodb",
                "quarkus.mongodb.devservices.enabled", "true",
                "quarkus.liquibase-mongodb.migrate-at-start", "true",
                "quarkus.liquibase-mongodb.change-log",
                        "db/changelog-mongodb/db.changelog-master.yaml");
    }
}
