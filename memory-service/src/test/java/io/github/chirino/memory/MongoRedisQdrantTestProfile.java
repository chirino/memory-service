package io.github.chirino.memory;

import io.quarkus.test.junit.QuarkusTestProfile;
import java.util.Map;

public class MongoRedisQdrantTestProfile implements QuarkusTestProfile {

    @Override
    public Map<String, String> getConfigOverrides() {
        return Map.ofEntries(
                Map.entry("memory-service.datastore.type", "mongo"),
                Map.entry("memory-service.vector.store.type", "qdrant"),
                Map.entry("memory-service.cache.type", "redis"),
                Map.entry("quarkus.mongodb.devservices.enabled", "true"),
                Map.entry("quarkus.redis.devservices.enabled", "true"),
                Map.entry("quarkus.infinispan-client.devservices.enabled", "false"));
    }
}
