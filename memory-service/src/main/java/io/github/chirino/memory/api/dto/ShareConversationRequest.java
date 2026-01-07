package io.github.chirino.memory.api.dto;

import io.github.chirino.memory.model.AccessLevel;

public class ShareConversationRequest {

    private String userId;
    private AccessLevel accessLevel;

    public String getUserId() {
        return userId;
    }

    public void setUserId(String userId) {
        this.userId = userId;
    }

    public AccessLevel getAccessLevel() {
        return accessLevel;
    }

    public void setAccessLevel(AccessLevel accessLevel) {
        this.accessLevel = accessLevel;
    }
}
