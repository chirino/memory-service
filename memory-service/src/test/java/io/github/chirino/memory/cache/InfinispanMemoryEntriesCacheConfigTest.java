package io.github.chirino.memory.cache;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.config.TestInstance;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import java.time.Duration;
import java.util.Optional;
import org.infinispan.client.hotrod.RemoteCacheManager;
import org.junit.jupiter.api.Test;

class InfinispanMemoryEntriesCacheConfigTest {

    @Test
    void defaultCacheNameIsUsed() {
        InfinispanMemoryEntriesCache cache =
                new InfinispanMemoryEntriesCache(
                        Optional.of("infinispan"),
                        Duration.ofSeconds(30),
                        "memory-entries",
                        Duration.ofMinutes(10),
                        TestInstance.<RemoteCacheManager>unsatisfied(),
                        new ObjectMapper(),
                        new SimpleMeterRegistry());

        assertEquals("memory-entries", cache.getCacheName());
    }

    @Test
    void customCacheNameIsUsed() {
        InfinispanMemoryEntriesCache cache =
                new InfinispanMemoryEntriesCache(
                        Optional.of("infinispan"),
                        Duration.ofSeconds(30),
                        "prod-memory-entries",
                        Duration.ofMinutes(10),
                        TestInstance.<RemoteCacheManager>unsatisfied(),
                        new ObjectMapper(),
                        new SimpleMeterRegistry());

        assertEquals("prod-memory-entries", cache.getCacheName());
    }

    @Test
    void cacheConfigXmlContainsCacheName() {
        String name = "my-custom-cache";
        String xml = InfinispanMemoryEntriesCache.buildCacheConfigXml(name);
        assertTrue(xml.contains("name=\"my-custom-cache\""));
    }
}
