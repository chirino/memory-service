package io.github.chirino.memory.runtime;

import io.quarkus.runtime.StartupEvent;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

/**
 * Observes application startup and logs the configured memory service client base URL.
 * This ensures the log happens after dev services have configured the URL.
 */
@ApplicationScoped
public class MemoryServiceApiStartupObserver {

    private static final Logger LOG = Logger.getLogger(MemoryServiceApiStartupObserver.class);

    void onStart(@Observes StartupEvent ev) {
        var config = ConfigProvider.getConfig();
        String baseUrl =
                config.getOptionalValue(
                                "quarkus.rest-client.memory-service-client.url", String.class)
                        .orElse("not configured");
        LOG.infof("Memory Service REST client base URL configured: %s", baseUrl);
    }
}
