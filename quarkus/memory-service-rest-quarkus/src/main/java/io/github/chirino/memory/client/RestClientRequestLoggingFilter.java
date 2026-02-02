package io.github.chirino.memory.client;

import jakarta.ws.rs.client.ClientRequestContext;
import jakarta.ws.rs.client.ClientRequestFilter;
import jakarta.ws.rs.ext.Provider;
import java.io.IOException;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

/**
 * Logs outbound REST client requests to the configured memory-service base URL.
 * This filter does not modify headers.
 */
@Provider
public class RestClientRequestLoggingFilter implements ClientRequestFilter {

    private static final Logger LOG = Logger.getLogger(RestClientRequestLoggingFilter.class);

    private final String memoryServiceBaseUrl;

    public RestClientRequestLoggingFilter() {
        this.memoryServiceBaseUrl =
                ConfigProvider.getConfig()
                        .getOptionalValue("memory-service.client.url", String.class)
                        .orElse(null);
    }

    @Override
    public void filter(ClientRequestContext requestContext) throws IOException {
        String uri = requestContext.getUri().toString();
        if (memoryServiceBaseUrl == null || !uri.startsWith(memoryServiceBaseUrl)) {
            return;
        }
        String method = requestContext.getMethod();
        boolean sentAuthorizationHeader = requestContext.getHeaderString("Authorization") != null;
        boolean sentApiKeyHeader = requestContext.getHeaderString("X-API-Key") != null;
        LOG.infof(
                "REST client request: %s %s, sent Authorization header: %b, sent X-API-Key header:"
                        + " %b",
                method, uri, sentAuthorizationHeader, sentApiKeyHeader);
    }
}
