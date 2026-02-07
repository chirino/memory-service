package io.github.chirino.memory;

import io.quarkus.test.junit.QuarkusTestProfile;
import java.util.Map;

public class PostgresqlInfinispanS3TestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.ofEntries(
                Map.entry("memory-service.datastore.type", "postgres"),
                Map.entry("memory-service.vector.type", "pgvector"),
                Map.entry("memory-service.cache.type", "infinispan"),
                Map.entry(
                        "quarkus.infinispan-client.cache.response-resumer.configuration",
                        "<distributed-cache><encoding"
                            + " media-type=\"application/x-protostream\"/></distributed-cache>"),
                Map.entry(
                        "quarkus.infinispan-client.cache.memory-entries.configuration",
                        "<distributed-cache><encoding"
                                + " media-type=\"text/plain\"/></distributed-cache>"),
                Map.entry("quarkus.datasource.devservices.enabled", "true"),
                Map.entry("quarkus.infinispan-client.devservices.enabled", "true"),
                Map.entry("quarkus.liquibase.migrate-at-start", "true"),
                Map.entry("quarkus.datasource.devservices.image-name", "pgvector/pgvector:pg17"),
                // S3 FileStore via LocalStack
                Map.entry("memory-service.attachments.store", "s3"),
                Map.entry("quarkus.s3.devservices.enabled", "true"),
                Map.entry("quarkus.s3.devservices.buckets", "memory-service-attachments"));
    }
}
