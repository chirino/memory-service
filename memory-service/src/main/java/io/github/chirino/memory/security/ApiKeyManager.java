package io.github.chirino.memory.security;

import io.smallrye.config.SmallRyeConfig;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

@ApplicationScoped
public class ApiKeyManager {

    private static final Logger LOG = Logger.getLogger(ApiKeyManager.class);
    private static final String PROPERTY_NAME = "memory.api-keys";

    private final Set<String> validApiKeys;

    public ApiKeyManager() {
        SmallRyeConfig config = (SmallRyeConfig) ConfigProvider.getConfig();
        List<String> keys =
                config.getOptionalValues(PROPERTY_NAME, String.class)
                        .orElse(Collections.emptyList());
        Set<String> normalized = new HashSet<>();
        for (String key : keys) {
            if (key != null) {
                String trimmed = key.trim();
                if (!trimmed.isEmpty()) {
                    normalized.add(trimmed);
                }
            }
        }
        this.validApiKeys = Collections.unmodifiableSet(normalized);
        if (this.validApiKeys.isEmpty()) {
            LOG.info(
                    "No API keys configured (memory.api-keys); API key authentication is"
                            + " effectively disabled.");
        } else {
            LOG.infof("Configured %d API key(s) for agent access.", this.validApiKeys.size());
        }
    }

    public boolean hasKeys() {
        return !validApiKeys.isEmpty();
    }

    public boolean validate(String apiKey) {
        if (apiKey == null) {
            return false;
        }
        return validApiKeys.contains(apiKey.trim());
    }
}
