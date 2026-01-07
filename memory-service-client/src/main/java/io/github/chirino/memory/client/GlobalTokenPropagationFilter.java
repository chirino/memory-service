package io.github.chirino.memory.client;

import io.quarkus.oidc.AccessTokenCredential;
import io.quarkus.security.credential.TokenCredential;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.ws.rs.client.ClientRequestContext;
import jakarta.ws.rs.client.ClientRequestFilter;
import jakarta.ws.rs.ext.Provider;
import java.io.IOException;
import org.eclipse.microprofile.config.ConfigProvider;
import org.jboss.logging.Logger;

/**
 * Global REST Client filter that ensures token propagation from SecurityIdentity.
 * This works even when the incoming request has no Authorization header (e.g. session cookies).
 */
@Provider
public class GlobalTokenPropagationFilter implements ClientRequestFilter {

    private static final Logger LOG = Logger.getLogger(GlobalTokenPropagationFilter.class);

    @Inject Instance<SecurityIdentityAssociation> securityIdentityAssociationInstance;

    private final String memoryServiceBaseUrl;
    private final String configuredApiKey;
    private final String profile;

    public GlobalTokenPropagationFilter() {
        // Limit propagation strictly to calls targeting the configured memory-service base URL
        // so that we never leak user tokens to external services such as OpenAI.
        var config = ConfigProvider.getConfig();
        // Primary source: shared memory-service-client URL used by the agent proxy and REST client.
        String baseUrl =
                config.getOptionalValue("memory-service-client.url", String.class).orElse(null);
        if (baseUrl == null) {
            // Backward compatibility with older configuration
            baseUrl = config.getOptionalValue("memory-service.url", String.class).orElse(null);
        }
        this.memoryServiceBaseUrl = baseUrl;
        this.configuredApiKey =
                config.getOptionalValue("memory-service-client.api-key", String.class).orElse(null);
        this.profile = config.getOptionalValue("quarkus.profile", String.class).orElse("prod");
    }

    @Override
    public void filter(ClientRequestContext requestContext) throws IOException {
        String uri = requestContext.getUri().toString();
        String method = requestContext.getMethod();

        // Only intercept calls to the memory-service base URL to avoid leaking tokens
        // to external services (e.g. OpenAI, other REST clients).
        if (memoryServiceBaseUrl == null || !uri.startsWith(memoryServiceBaseUrl)) {
            return;
        }

        boolean sentAuthorizationHeader = false;
        SecurityIdentity identity = getSecurityIdentity();
        if (identity != null && requestContext.getHeaderString("Authorization") == null) {
            String token = resolveToken(identity);
            if (token != null) {
                requestContext.getHeaders().putSingle("Authorization", "Bearer " + token);
                sentAuthorizationHeader = true;
            }
        }

        // If we're not debugging and no API key is configured, skip verbose
        // header logging and API key injection to keep logs quieter.
        if (!LOG.isDebugEnabled() && configuredApiKey == null) {
            return;
        }

        boolean sentApiKeyHeader = false;
        String apiKey = requestContext.getHeaderString("X-API-Key");
        if (apiKey == null && configuredApiKey != null) {
            requestContext.getHeaders().putSingle("X-API-Key", configuredApiKey);
            apiKey = configuredApiKey;
            sentApiKeyHeader = true;
        }

        LOG.infof(
                "REST client request: %s %s, sent Authorization header: %b, sent X-API-Key header:"
                        + " %b",
                method, uri, sentAuthorizationHeader, sentApiKeyHeader);
    }

    private SecurityIdentity getSecurityIdentity() {
        if (securityIdentityAssociationInstance != null
                && securityIdentityAssociationInstance.isResolvable()) {
            return securityIdentityAssociationInstance.get().getIdentity();
        }
        return null;
    }

    private String resolveToken(SecurityIdentity identity) {
        // Try AccessTokenCredential (OIDC session/hybrid mode)
        AccessTokenCredential atc = identity.getCredential(AccessTokenCredential.class);
        if (atc != null) {
            return atc.getToken();
        }
        // Try TokenCredential (Bearer mode)
        TokenCredential tc = identity.getCredential(TokenCredential.class);
        if (tc != null) {
            return tc.getToken();
        }
        return null;
    }
}
