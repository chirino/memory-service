package io.github.chirino.memory.security;

import io.smallrye.config.SmallRyeConfig;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

@ApplicationScoped
public class ApiKeyManager {

    private static final Logger LOG = Logger.getLogger(ApiKeyManager.class);
    private static final String PROPERTY_NAME = "memory-service.api-keys";
    private static final String PROPERTY_PREFIX = PROPERTY_NAME + ".";
    private static final String PROPERTY_PREFIX_NORMALIZED = "memory.service.api.keys.";

    private final Map<String, Set<String>> apiKeysByClientId;
    private final Map<String, String> clientIdByApiKey;

    public ApiKeyManager() {
        SmallRyeConfig config = (SmallRyeConfig) ConfigProvider.getConfig();
        Map<String, Set<String>> keysByClient = new HashMap<>();
        Map<String, String> keyToClient = new HashMap<>();
        for (String propertyName : config.getPropertyNames()) {
            String clientId = resolveClientIdFromPropertyName(propertyName);
            if (clientId == null) {
                continue;
            }
            if (clientId.isEmpty()) {
                LOG.warnf("Skipping API key config with empty client id: %s", propertyName);
                continue;
            }
            List<String> keys =
                    config.getOptionalValues(propertyName, String.class)
                            .orElse(Collections.emptyList());
            Set<String> normalized =
                    keysByClient.computeIfAbsent(clientId, ignored -> new HashSet<>());
            for (String key : keys) {
                if (key == null) {
                    continue;
                }
                String trimmed = key.trim();
                if (trimmed.isEmpty()) {
                    continue;
                }
                String existing = keyToClient.get(trimmed);
                if (existing != null && !existing.equals(clientId)) {
                    LOG.warnf(
                            "API key is configured for multiple client ids; using %s, ignoring %s",
                            existing, clientId);
                    continue;
                }
                normalized.add(trimmed);
                keyToClient.put(trimmed, clientId);
            }
        }
        this.apiKeysByClientId = Collections.unmodifiableMap(keysByClient);
        this.clientIdByApiKey = Collections.unmodifiableMap(keyToClient);
        if (this.clientIdByApiKey.isEmpty()) {
            LOG.info(
                    "No API keys configured (memory-service.api-keys.<client-id>); API key"
                            + " authentication is effectively disabled.");
        } else {
            LOG.infof(
                    "Configured %d API key(s) for %d client id(s).",
                    this.clientIdByApiKey.size(), this.apiKeysByClientId.size());
        }
    }

    public boolean hasKeys() {
        return !clientIdByApiKey.isEmpty();
    }

    public boolean validate(String apiKey) {
        return resolveClientId(apiKey).isPresent();
    }

    public Optional<String> resolveClientId(String apiKey) {
        if (apiKey == null) {
            return Optional.empty();
        }
        String trimmed = apiKey.trim();
        if (trimmed.isEmpty()) {
            return Optional.empty();
        }
        return Optional.ofNullable(clientIdByApiKey.get(trimmed));
    }

    private String resolveClientIdFromPropertyName(String propertyName) {
        if (propertyName == null) {
            return null;
        }
        String trimmed = propertyName.trim();
        String clientId;
        if (trimmed.startsWith(PROPERTY_PREFIX)) {
            clientId = trimmed.substring(PROPERTY_PREFIX.length());
        } else if (trimmed.startsWith(PROPERTY_PREFIX_NORMALIZED)) {
            clientId = trimmed.substring(PROPERTY_PREFIX_NORMALIZED.length());
        } else {
            return null;
        }
        clientId = clientId.trim();
        return clientId.isEmpty() ? null : clientId;
    }
}
