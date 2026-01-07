package io.github.chirino.memory.api.dto;

import io.github.chirino.memory.model.AccessLevel;

public class ConversationMembershipDto {

    private String conversationGroupId;
    private String userId;
    private AccessLevel accessLevel;
    private String createdAt;

    public String getConversationGroupId() {
        return conversationGroupId;
    }

    public void setConversationGroupId(String conversationGroupId) {
        this.conversationGroupId = conversationGroupId;
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
