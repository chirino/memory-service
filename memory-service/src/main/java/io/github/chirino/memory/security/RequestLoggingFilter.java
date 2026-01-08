package io.github.chirino.memory.security;

import io.quarkus.security.identity.SecurityIdentity;
import jakarta.annotation.Priority;
import jakarta.inject.Inject;
import jakarta.ws.rs.Priorities;
import jakarta.ws.rs.container.ContainerRequestContext;
import jakarta.ws.rs.container.ContainerRequestFilter;
import jakarta.ws.rs.ext.Provider;
import java.io.IOException;
import org.jboss.logging.Logger;

@Provider
@Priority(Priorities.AUTHENTICATION - 1) // Run just before authentication
public class RequestLoggingFilter implements ContainerRequestFilter {

    private static final Logger LOG = Logger.getLogger(RequestLoggingFilter.class);

    @Inject SecurityIdentity identity;

    @Override
    public void filter(ContainerRequestContext requestContext) throws IOException {
        String method = requestContext.getMethod();
        String path = requestContext.getUriInfo().getPath();

        String principalName = "";
        if (identity != null && !identity.isAnonymous()) {
            principalName = identity.getPrincipal().getName();
        }

        String apiKey = requestContext.getHeaderString(ApiKeyRequestFilter.HEADER_NAME);

        LOG.infof(
                "Incoming request: %s %s => principal name: %s, has api key: %b",
                method, path, principalName, apiKey != null);
    }
}
