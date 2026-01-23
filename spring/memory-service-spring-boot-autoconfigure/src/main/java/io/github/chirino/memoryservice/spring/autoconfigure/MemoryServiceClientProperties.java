package io.github.chirino.memoryservice.spring.autoconfigure;

import java.net.URI;
import org.springframework.boot.context.properties.ConfigurationProperties;

@ConfigurationProperties(prefix = "memory-service.client")
public class MemoryServiceClientProperties {

    /**
     * Base URL used by the generated REST client. When absent, the auto-config will
     * prefer service-connection details and fall back to the client's own default.
     */
    private URI baseUrl;

    /**
     * Optional static bearer token to use when no OAuth2 client registration is
     * configured.
     */
    private String bearerToken;

    /**
     * Optional client registration id to pull bearer tokens from Spring Security's
     * OAuth2AuthorizedClientManager.
     */
    private String oidcClientRegistrationId;

    public URI getBaseUrl() {
        return baseUrl;
    }

    public void setBaseUrl(URI baseUrl) {
        this.baseUrl = baseUrl;
    }

    public String getBearerToken() {
        return bearerToken;
    }

    public void setBearerToken(String bearerToken) {
        this.bearerToken = bearerToken;
    }

    public String getOidcClientRegistrationId() {
        return oidcClientRegistrationId;
    }

    public void setOidcClientRegistrationId(String oidcClientRegistrationId) {
        this.oidcClientRegistrationId = oidcClientRegistrationId;
    }
}
