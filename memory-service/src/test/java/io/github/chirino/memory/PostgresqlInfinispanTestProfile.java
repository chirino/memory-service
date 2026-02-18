package io.github.chirino.memory;

import io.quarkus.test.junit.QuarkusTestProfile;
import java.util.Map;

public class PostgresqlInfinispanTestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.ofEntries(
                Map.entry("memory-service.datastore.type", "postgres"),
                Map.entry("memory-service.vector.type", "pgvector"),
                Map.entry("memory-service.cache.type", "infinispan"),
                Map.entry(
                        "memory-service.cache.infinispan.memory-entries-cache-name",
                        "test-memory-entries"),
                Map.entry(
                        "memory-service.cache.infinispan.response-recordings-cache-name",
                        "test-response-recordings"),
                Map.entry(
                        "quarkus.infinispan-client.cache.test-response-recordings.configuration",
                        "<distributed-cache><encoding"
                            + " media-type=\"application/x-protostream\"/></distributed-cache>"),
                Map.entry(
                        "quarkus.infinispan-client.cache.test-memory-entries.configuration",
                        "<distributed-cache><encoding"
                                + " media-type=\"text/plain\"/></distributed-cache>"),
                Map.entry("quarkus.datasource.devservices.enabled", "true"),
                Map.entry("quarkus.infinispan-client.devservices.enabled", "true"),
                Map.entry("quarkus.datasource.devservices.image-name", "pgvector/pgvector:pg18"),
                // Disable unused MongoDB/Redis dev services
                Map.entry("quarkus.mongodb.devservices.enabled", "false"),
                Map.entry("quarkus.redis.devservices.enabled", "false"));
    }
}
