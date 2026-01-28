package io.github.chirino.memory.api.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;
import io.github.chirino.memory.model.AccessLevel;

public class ConversationMembershipDto {

    // Internal field - not exposed in API responses
    @JsonIgnore private String conversationGroupId;
    // Public field - exposed in API responses
    private String conversationId;
    private String userId;
    private AccessLevel accessLevel;
    private String createdAt;

    public String getConversationGroupId() {
        return conversationGroupId;
    }

    public void setConversationGroupId(String conversationGroupId) {
        this.conversationGroupId = conversationGroupId;
    }

    public String getConversationId() {
        return conversationId;
    }

    public void setConversationId(String conversationId) {
        this.conversationId = conversationId;
    }

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

    public String getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(String createdAt) {
        this.createdAt = createdAt;
    }
}
