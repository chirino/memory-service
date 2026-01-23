package io.github.chirino.memoryservice.client;

import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;
import org.springframework.boot.context.properties.ConfigurationProperties;

@ConfigurationProperties(prefix = "memory-service.client")
public class MemoryServiceClientProperties {

    private String baseUrl = "http://localhost:8080";
    private String apiKey;

    /**
     * Optional static bearer token used when no user token is available. Most calls
     * should still forward the authenticated user's token from the incoming request.
     */
    private String bearerToken;

    private String oidcClientRegistration;
    private Duration timeout = Duration.ofSeconds(30);
    private boolean logRequests = false;
    private boolean withCredentials = true;
    private Map<String, String> defaultHeaders = new LinkedHashMap<>();

    public String getBaseUrl() {
        return baseUrl;
    }

    public void setBaseUrl(String baseUrl) {
        this.baseUrl = baseUrl;
    }

    public String getApiKey() {
        return apiKey;
    }

    public void setApiKey(String apiKey) {
        this.apiKey = apiKey;
    }

    public String getOidcClientRegistration() {
        return oidcClientRegistration;
    }

    public void setOidcClientRegistration(String oidcClientRegistration) {
        this.oidcClientRegistration = oidcClientRegistration;
    }

    public String getBearerToken() {
        return bearerToken;
    }

    public void setBearerToken(String bearerToken) {
        this.bearerToken = bearerToken;
    }

    public Duration getTimeout() {
        return timeout;
    }

    public void setTimeout(Duration timeout) {
        this.timeout = timeout;
    }

    public boolean isLogRequests() {
        return logRequests;
    }

    public void setLogRequests(boolean logRequests) {
        this.logRequests = logRequests;
    }

    public boolean isWithCredentials() {
        return withCredentials;
    }

    public void setWithCredentials(boolean withCredentials) {
        this.withCredentials = withCredentials;
    }

    public Map<String, String> getDefaultHeaders() {
        return defaultHeaders;
    }

    public void setDefaultHeaders(Map<String, String> defaultHeaders) {
        this.defaultHeaders = defaultHeaders;
    }
}
