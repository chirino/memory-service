package io.github.chirino.memory.resumer;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.github.chirino.memory.config.TestInstance;
import java.time.Duration;
import java.util.Optional;
import org.infinispan.client.hotrod.RemoteCacheManager;
import org.junit.jupiter.api.Test;

class InfinispanResponseResumerLocatorStoreConfigTest {

    @Test
    void defaultCacheNameIsUsed() {
        InfinispanResponseResumerLocatorStore store =
                new InfinispanResponseResumerLocatorStore(
                        Optional.of("infinispan"),
                        Duration.ofSeconds(30),
                        "response-recordings",
                        TestInstance.<RemoteCacheManager>unsatisfied());

        assertEquals("response-recordings", store.getCacheName());
    }

    @Test
    void customCacheNameIsUsed() {
        InfinispanResponseResumerLocatorStore store =
                new InfinispanResponseResumerLocatorStore(
                        Optional.of("infinispan"),
                        Duration.ofSeconds(30),
                        "staging-response-recordings",
                        TestInstance.<RemoteCacheManager>unsatisfied());

        assertEquals("staging-response-recordings", store.getCacheName());
    }

    @Test
    void cacheConfigXmlContainsCacheName() {
        String name = "my-custom-cache";
        String xml = InfinispanResponseResumerLocatorStore.buildCacheConfigXml(name);
        assertTrue(xml.contains("name=\"my-custom-cache\""));
    }
}
