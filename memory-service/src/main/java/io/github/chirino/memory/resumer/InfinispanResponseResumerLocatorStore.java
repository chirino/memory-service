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
import org.jboss.logging.Logger;

@ApplicationScoped
public class InfinispanResponseResumerLocatorStore implements ResponseResumerLocatorStore {
    private static final Logger LOG = Logger.getLogger(InfinispanResponseResumerLocatorStore.class);
    private static final String RESPONSE_KEY_PREFIX = "response:";
    private static final String CACHE_NAME = "response-resumer";
    private static final Duration RETRY_DELAY = Duration.ofMillis(200);

    private final boolean infinispanEnabled;
    private final Instance<RemoteCacheManager> cacheManagers;
    private final Duration startupTimeout;
    private volatile RemoteCache<String, String> cache;

    @Inject
    public InfinispanResponseResumerLocatorStore(
            @ConfigProperty(name = "memory-service.response-resumer") Optional<String> resumerType,
            @ConfigProperty(
                            name = "memory-service.response-resumer.infinispan.startup-timeout",
                            defaultValue = "PT30S")
                    Duration startupTimeout,
            @Any Instance<RemoteCacheManager> cacheManagers) {
        this.infinispanEnabled = resumerType.map("infinispan"::equalsIgnoreCase).orElse(false);
        this.startupTimeout = startupTimeout;
        this.cacheManagers = cacheManagers;
    }

    @PostConstruct
    void init() {
        if (!infinispanEnabled) {
            return;
        }

        if (cacheManagers.isUnsatisfied()) {
            throw new IllegalStateException(
                    "Response resumer is enabled (memory-service.response-resumer=infinispan) but"
                            + " no Infinispan client is available.");
        }

        RemoteCacheManager cacheManager = cacheManagers.get();
        cache = cacheManager.getCache(CACHE_NAME);
        if (cache == null) {
            throw new IllegalStateException(
                    "Response resumer is enabled (memory-service.response-resumer=infinispan) but"
                            + " cache '"
                            + CACHE_NAME
                            + "' is not available.");
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
