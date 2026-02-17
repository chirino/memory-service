package io.github.chirino.memory.resumer;

import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.Optional;
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
public class InfinispanResponseResumerLocatorStore implements ResponseResumerLocatorStore {
    private static final Logger LOG = Logger.getLogger(InfinispanResponseResumerLocatorStore.class);
    private static final String RESPONSE_KEY_PREFIX = "response:";
    private static final Duration RETRY_DELAY = Duration.ofMillis(200);

    private final boolean infinispanEnabled;
    private final Instance<RemoteCacheManager> cacheManagers;
    private final Duration startupTimeout;
    private final String cacheName;
    private volatile RemoteCache<String, String> cache;

    @Inject
    public InfinispanResponseResumerLocatorStore(
            @ConfigProperty(name = "memory-service.cache.type") Optional<String> cacheType,
            @ConfigProperty(
                            name = "memory-service.cache.infinispan.startup-timeout",
                            defaultValue = "PT30S")
                    Duration startupTimeout,
            @ConfigProperty(
                            name = "memory-service.cache.infinispan.response-recordings-cache-name",
                            defaultValue = "response-recordings")
                    String cacheName,
            @Any Instance<RemoteCacheManager> cacheManagers) {
        this.infinispanEnabled = cacheType.map("infinispan"::equalsIgnoreCase).orElse(false);
        this.startupTimeout = startupTimeout;
        this.cacheName = cacheName;
        this.cacheManagers = cacheManagers;
    }

    @PostConstruct
    void init() {
        if (!infinispanEnabled) {
            return;
        }

        if (cacheManagers.isUnsatisfied()) {
            throw new IllegalStateException(
                    "Response resumer is enabled (memory-service.cache.type=infinispan) but"
                            + " no Infinispan client is available.");
        }

        RemoteCacheManager cacheManager = cacheManagers.get();
        cache =
                cacheManager
                        .administration()
                        .getOrCreateCache(cacheName, buildCacheConfig(cacheName));
        if (cache == null) {
            throw new IllegalStateException(
                    "Response resumer is enabled (memory-service.cache.type=infinispan) but"
                            + " cache '"
                            + cacheName
                            + "' could not be created.");
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
            LOG.debugf(e, "Infinispan response resumer cache is not available yet");
            return false;
        }
    }

    @Override
    public Optional<ResponseResumerLocator> get(String conversationId) {
        if (!available()) {
            return Optional.empty();
        }
        String value = withRetry("get", () -> cache.get(key(conversationId)));
        return ResponseResumerLocator.decode(value);
    }

    @Override
    public void upsert(String conversationId, ResponseResumerLocator locator, Duration ttl) {
        if (!available()) {
            return;
        }
        String key = key(conversationId);
        String value = locator.encode();
        withRetry("upsert", () -> cache.put(key, value, ttl.toMillis(), TimeUnit.MILLISECONDS));
    }

    @Override
    public void remove(String conversationId) {
        if (!available()) {
            return;
        }
        withRetry("remove", () -> cache.remove(key(conversationId)));
    }

    @Override
    public boolean exists(String conversationId) {
        if (!available()) {
            return false;
        }
        return Boolean.TRUE.equals(
                withRetry("exists", () -> cache.containsKey(key(conversationId))));
    }

    static String buildCacheConfigXml(String name) {
        return "<distributed-cache name=\""
                + name
                + "\">"
                + "<encoding media-type=\"application/x-protostream\"/>"
                + "</distributed-cache>";
    }

    private static XMLStringConfiguration buildCacheConfig(String name) {
        return new XMLStringConfiguration(buildCacheConfigXml(name));
    }

    String getCacheName() {
        return cacheName;
    }

    private String key(String conversationId) {
        return RESPONSE_KEY_PREFIX + conversationId;
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
                        "Retrying Infinispan response resumer %s after connection failure",
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
