package io.github.chirino.memory.api.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;

public class ConversationForkSummaryDto {

    private String conversationId;
    // Internal field - not exposed in API responses
    @JsonIgnore private String conversationGroupId;
    private String forkedAtMessageId;
    private String forkedAtConversationId;
    private String title;
    private String createdAt;

    public String getConversationId() {
        return conversationId;
    }

    public void setConversationId(String conversationId) {
        this.conversationId = conversationId;
    }

    public String getConversationGroupId() {
        return conversationGroupId;
    }

    public void setConversationGroupId(String conversationGroupId) {
        this.conversationGroupId = conversationGroupId;
    }

    public String getForkedAtMessageId() {
        return forkedAtMessageId;
    }

    public void setForkedAtMessageId(String forkedAtMessageId) {
        this.forkedAtMessageId = forkedAtMessageId;
    }

    public String getForkedAtConversationId() {
        return forkedAtConversationId;
    }

    public void setForkedAtConversationId(String forkedAtConversationId) {
        this.forkedAtConversationId = forkedAtConversationId;
    }

    public String getTitle() {
        return title;
    }

    public void setTitle(String title) {
        this.title = title;
    }

    public String getCreatedAt() {
        return createdAt;
    }

    public void setCreatedAt(String createdAt) {
        this.createdAt = createdAt;
    }
}
