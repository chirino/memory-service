package io.github.chirino.memory.security;

import jakarta.enterprise.context.RequestScoped;

@RequestScoped
public class ApiKeyContext {

    private boolean valid;
    private String apiKey;
    private String clientId;

    public boolean hasValidApiKey() {
        return valid;
    }

    public void setValid(boolean valid) {
        this.valid = valid;
    }

    public String getApiKey() {
        return apiKey;
    }

    public void setApiKey(String apiKey) {
        this.apiKey = apiKey;
    }

    public String getClientId() {
        return clientId;
    }

    public void setClientId(String clientId) {
        this.clientId = clientId;
    }
}
