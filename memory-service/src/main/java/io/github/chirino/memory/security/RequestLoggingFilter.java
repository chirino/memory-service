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
        String authHeader = requestContext.getHeaderString("Authorization");

        LOG.infof(
                "Incoming request: %s %s, Auth Header present: %b",
                method, path, (authHeader != null && !authHeader.isEmpty()));

        if (identity != null && !identity.isAnonymous()) {
            LOG.infof("Authenticated user: %s", identity.getPrincipal().getName());
        }
    }
}
