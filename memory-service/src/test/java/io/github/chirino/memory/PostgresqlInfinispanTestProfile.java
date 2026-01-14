package io.github.chirino.memory;

import io.quarkus.test.junit.QuarkusTestProfile;
import java.util.Map;

public class PostgresqlInfinispanTestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.of(
                "memory-service.datastore.type", "postgres",
                "memory-service.vector.type", "pgvector",
                "memory-service.response-resumer", "infinispan",
                "quarkus.infinispan-client.cache.response-resumer.configuration",
                        "<distributed-cache><encoding"
                                + " media-type=\"application/x-protostream\"/></distributed-cache>",
                "quarkus.datasource.devservices.enabled", "true",
                "quarkus.infinispan-client.devservices.enabled", "true",
                "quarkus.liquibase.migrate-at-start", "true",
                "quarkus.datasource.devservices.image-name", "pgvector/pgvector:pg17");
    }
}
