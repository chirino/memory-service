package io.github.chirino.memory;

import io.quarkus.test.junit.QuarkusTestProfile;
import java.util.Map;

public class PostgresqlTestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.of(
                "memory-service.datastore.type", "postgres",
                "memory-service.vector.type", "pgvector",
                "quarkus.datasource.devservices.enabled", "true",
                "quarkus.liquibase.migrate-at-start", "true",
                "quarkus.datasource.devservices.image-name", "pgvector/pgvector:pg17");
    }
}
