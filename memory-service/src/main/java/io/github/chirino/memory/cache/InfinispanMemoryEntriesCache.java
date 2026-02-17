package io.github.chirino.memory.cache;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.MeterRegistry;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.function.Supplier;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.infinispan.client.hotrod.RemoteCache;
import org.infinispan.client.hotrod.RemoteCacheManager;
import org.infinispan.client.hotrod.exceptions.HotRodClientException;
import org.infinispan.client.hotrod.exceptions.TransportException;
import org.infinispan.commons.configuration.XMLStringConfiguration;
import org.jboss.logging.Logger;

@ApplicationScoped
public class InfinispanMemoryEntriesCache implements MemoryEntriesCache {
    private static final Logger LOG = Logger.getLogger(InfinispanMemoryEntriesCache.class);
    private static final Duration RETRY_DELAY = Duration.ofMillis(200);

    private final boolean infinispanEnabled;
    private final Instance<RemoteCacheManager> cacheManagers;
    private final Duration startupTimeout;
    private final String cacheName;
    private final Duration ttl;
    private final ObjectMapper objectMapper;
    private final MeterRegistry meterRegistry;
    private volatile RemoteCache<String, String> cache;
    private Counter cacheHits;
    private Counter cacheMisses;
    private Counter cacheErrors;

    @Inject
    public InfinispanMemoryEntriesCache(
            @ConfigProperty(name = "memory-service.cache.type") Optional<String> cacheType,
            @ConfigProperty(
                            name = "memory-service.cache.infinispan.startup-timeout",
                            defaultValue = "PT30S")
                    Duration startupTimeout,
            @ConfigProperty(
                            name = "memory-service.cache.infinispan.memory-entries-cache-name",
                            defaultValue = "memory-entries")
                    String cacheName,
            @ConfigProperty(name = "memory-service.cache.epoch.ttl", defaultValue = "PT10M")
                    Duration ttl,
            @Any Instance<RemoteCacheManager> cacheManagers,
            ObjectMapper objectMapper,
            MeterRegistry meterRegistry) {
        this.infinispanEnabled = cacheType.map("infinispan"::equalsIgnoreCase).orElse(false);
        this.startupTimeout = startupTimeout;
        this.cacheName = cacheName;
        this.ttl = ttl;
        this.cacheManagers = cacheManagers;
        this.objectMapper = objectMapper;
        this.meterRegistry = meterRegistry;
    }

    @PostConstruct
    void init() {
        // Initialize metrics
        cacheHits = meterRegistry.counter("memory.entries.cache.hits", "backend", "infinispan");
        cacheMisses = meterRegistry.counter("memory.entries.cache.misses", "backend", "infinispan");
        cacheErrors = meterRegistry.counter("memory.entries.cache.errors", "backend", "infinispan");

        if (!infinispanEnabled) {
            return;
        }

        if (cacheManagers.isUnsatisfied()) {
            LOG.warn(
                    "Memory entries cache is enabled (memory-service.cache.type=infinispan) but"
                            + " no Infinispan client is available.");
            return;
        }

        try {
            RemoteCacheManager cacheManager = cacheManagers.get();
            cache =
                    cacheManager
                            .administration()
                            .getOrCreateCache(cacheName, buildCacheConfig(cacheName));
            if (cache == null) {
                LOG.warnf(
                        "Memory entries cache is enabled (memory-service.cache.type=infinispan) but"
                                + " cache '%s' could not be created.",
                        cacheName);
            }
        } catch (Exception e) {
            LOG.warnf(e, "Failed to initialize Infinispan memory entries cache");
        }
    }

    @Override
    public boolean available() {
        if (cache == null) {
            return false;
        }
        try {
            withRetry(
                    "available",
                    () -> {
                        cache.size();
                        return Boolean.TRUE;
                    });
            return true;
        } catch (RuntimeException e) {
            LOG.debugf(e, "Infinispan memory entries cache is not available yet");
            return false;
        }
    }

    @Override
    public Optional<CachedMemoryEntries> get(UUID conversationId, String clientId) {
        if (!available()) {
            cacheMisses.increment();
            return Optional.empty();
        }
        try {
            String key = buildKey(conversationId, clientId);
            // maxIdle automatically refreshes TTL on access - no manual re-put needed
            String json = withRetry("get", () -> cache.get(key));
            if (json == null) {
                cacheMisses.increment();
                return Optional.empty();
            }
            cacheHits.increment();
            return Optional.of(objectMapper.readValue(json, CachedMemoryEntries.class));
        } catch (JsonProcessingException e) {
            LOG.warnf(e, "Failed to deserialize memory entries from Infinispan cache");
            cacheErrors.increment();
            return Optional.empty();
        } catch (RuntimeException e) {
            LOG.warnf(
                    e,
                    "Failed to get memory entries from Infinispan cache for %s/%s",
                    conversationId,
                    clientId);
            cacheErrors.increment();
            return Optional.empty();
        }
    }

    @Override
    public void set(UUID conversationId, String clientId, CachedMemoryEntries entries) {
        if (!available()) {
            return;
        }
        try {
            String key = buildKey(conversationId, clientId);
            String json = objectMapper.writeValueAsString(entries);
            // Use maxIdle for sliding TTL that refreshes on access
            // lifespan=-1 means no hard expiration, only idle-based expiration
            withRetry(
                    "set",
                    () ->
                            cache.put(
                                    key,
                                    json,
                                    -1,
                                    TimeUnit.MILLISECONDS,
                                    ttl.toMillis(),
                                    TimeUnit.MILLISECONDS));
        } catch (JsonProcessingException e) {
            LOG.warnf(e, "Failed to serialize memory entries for Infinispan cache");
        } catch (RuntimeException e) {
            LOG.warnf(
                    e,
                    "Failed to set memory entries in Infinispan cache for %s/%s",
                    conversationId,
                    clientId);
        }
    }

    @Override
    public void remove(UUID conversationId, String clientId) {
        if (!available()) {
            return;
        }
        try {
            String key = buildKey(conversationId, clientId);
            withRetry("remove", () -> cache.remove(key));
        } catch (RuntimeException e) {
            LOG.warnf(
                    e,
                    "Failed to remove memory entries from Infinispan cache for %s/%s",
                    conversationId,
                    clientId);
        }
    }

    static String buildCacheConfigXml(String name) {
        return "<distributed-cache name=\""
                + name
                + "\">"
                + "<encoding media-type=\"text/plain\"/>"
                + "</distributed-cache>";
    }

    private static XMLStringConfiguration buildCacheConfig(String name) {
        return new XMLStringConfiguration(buildCacheConfigXml(name));
    }

    String getCacheName() {
        return cacheName;
    }

    private String buildKey(UUID conversationId, String clientId) {
        return conversationId + ":" + clientId;
    }

    private <T> T withRetry(String operation, Supplier<T> action) {
        long deadline = System.nanoTime() + startupTimeout.toNanos();
        RuntimeException lastFailure = null;
        while (true) {
            try {
                return action.get();
            } catch (RuntimeException e) {
                if (!isRetryable(e)) {
                    throw e;
                }
                lastFailure = e;
                if (System.nanoTime() >= deadline) {
                    throw e;
                }
                sleep();
                LOG.debugf(
                        "Retrying Infinispan memory entries cache %s after connection failure",
                        operation);
            }
        }
    }

    private boolean isRetryable(RuntimeException e) {
        return e instanceof TransportException || e instanceof HotRodClientException;
    }

    private void sleep() {
        try {
            Thread.sleep(RETRY_DELAY.toMillis());
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        }
    }
}
