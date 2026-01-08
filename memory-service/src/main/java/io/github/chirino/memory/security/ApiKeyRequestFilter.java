package io.github.chirino.memory.security;

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
import org.jboss.logging.Logger;

@Provider
@Priority(Priorities.AUTHENTICATION + 10)
@ApplicationScoped
public class ApiKeyRequestFilter implements ContainerRequestFilter {

    private static final Logger LOG = Logger.getLogger(ApiKeyRequestFilter.class);
    private static final String HEADER_NAME = "X-API-Key";

    @Context ResourceInfo resourceInfo;

    @Inject ApiKeyContext apiKeyContext;

    @Inject ApiKeyManager apiKeyManager;

    @Override
    public void filter(ContainerRequestContext requestContext) throws IOException {
        String apiKeyHeader = requestContext.getHeaderString(HEADER_NAME);
        boolean hasValidKey = false;

        apiKeyContext.setValid(false);
        apiKeyContext.setApiKey(null);

        if (apiKeyHeader != null && !apiKeyHeader.isBlank()) {
            apiKeyHeader = apiKeyHeader.trim();
            if (apiKeyManager.validate(apiKeyHeader)) {
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
