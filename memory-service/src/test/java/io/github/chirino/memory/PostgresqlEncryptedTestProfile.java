package io.github.chirino.memory;

import io.quarkus.test.junit.QuarkusTestProfile;
import java.util.Map;

/**
 * Test profile that enables file store encryption with the DEK provider. Used by
 * PostgresqlEncryptedCucumberTest to validate transparent encryption/decryption of attachments.
 */
public class PostgresqlEncryptedTestProfile implements QuarkusTestProfile {

    /** Base64 encoding of 32 zero bytes — test key only, never use in production. */
    static final String TEST_KEY = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.ofEntries(
                Map.entry("memory-service.datastore.type", "postgres"),
                Map.entry("memory-service.vector.store.type", "none"),
                Map.entry("memory-service.cache.type", "none"),
                Map.entry("quarkus.datasource.devservices.enabled", "true"),
                Map.entry("quarkus.datasource.devservices.image-name", "pgvector/pgvector:pg18"),
                // Disable unused dev services
                Map.entry("quarkus.mongodb.devservices.enabled", "false"),
                Map.entry("quarkus.redis.devservices.enabled", "false"),
                Map.entry("quarkus.infinispan-client.devservices.enabled", "false"),
                // Enable DEK encryption for both MemoryStore and FileStore
                Map.entry("memory-service.encryption.providers", "dek"),
                Map.entry("memory-service.encryption.dek.key", TEST_KEY));
    }
}
