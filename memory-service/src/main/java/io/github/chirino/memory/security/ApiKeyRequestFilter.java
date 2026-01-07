package io.github.chirino.memory.security;

import io.smallrye.config.SmallRyeConfig;
import jakarta.annotation.Priority;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.Priorities;
import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerRequestFilter;
import jakarta.ws.rs.container.ResourceInfo;
import jakarta.ws.rs.core.Context;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.ext.Provider;
import java.io.IOException;
import java.lang.reflect.Method;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

@Provider
@Priority(Priorities.AUTHENTICATION + 10)
@ApplicationScoped
public class ApiKeyRequestFilter implements ContainerRequestFilter {

    private static final Logger LOG = Logger.getLogger(ApiKeyRequestFilter.class);
    private static final String HEADER_NAME = "X-API-Key";

    private final Set<String> validApiKeys;

    @Context ResourceInfo resourceInfo;

    @Inject ApiKeyContext apiKeyContext;

    public ApiKeyRequestFilter() {
        SmallRyeConfig config = (SmallRyeConfig) ConfigProvider.getConfig();
        List<String> keys =
                config.getOptionalValues("memory.api-keys", String.class)
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

    @Override
    public void filter(ContainerRequestContext requestContext) throws IOException {
        String apiKeyHeader = requestContext.getHeaderString(HEADER_NAME);
        boolean hasValidKey = false;

        if (apiKeyHeader != null && !apiKeyHeader.isEmpty()) {
            if (validApiKeys.contains(apiKeyHeader)) {
                hasValidKey = true;
                apiKeyContext.setValid(true);
                apiKeyContext.setApiKey(apiKeyHeader);
                LOG.infof("Received valid API key");
            } else {
                LOG.debug("Received invalid API key");
            }
        }

        boolean required = isApiKeyRequired();
        if (required && !hasValidKey) {
            LOG.debugf(
                    "API key required but missing or invalid for %s %s",
                    requestContext.getMethod(), requestContext.getUriInfo().getPath());
            requestContext.abortWith(
                    Response.status(Response.Status.UNAUTHORIZED)
                            .entity("API key required")
                            .build());
        }
    }

    private boolean isApiKeyRequired() {
        if (resourceInfo == null) {
            return false;
        }
        Method method = resourceInfo.getResourceMethod();
        Class<?> resourceClass = resourceInfo.getResourceClass();
        if (method != null && method.isAnnotationPresent(RequireApiKey.class)) {
            return true;
        }
        return resourceClass != null && resourceClass.isAnnotationPresent(RequireApiKey.class);
    }
}
