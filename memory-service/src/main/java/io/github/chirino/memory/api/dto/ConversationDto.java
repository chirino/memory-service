package io.github.chirino.memory.api.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;

public class ConversationDto extends ConversationSummaryDto {

    // Internal field - not exposed in API responses
    @JsonIgnore private String conversationGroupId;
    private String forkedAtEntryId;
    private String forkedAtConversationId;

    public String getConversationGroupId() {
        return conversationGroupId;
    }

    public void setConversationGroupId(String conversationGroupId) {
        this.conversationGroupId = conversationGroupId;
    }

    public String getForkedAtEntryId() {
        return forkedAtEntryId;
    }

    public void setForkedAtEntryId(String forkedAtEntryId) {
        this.forkedAtEntryId = forkedAtEntryId;
    }

    public String getForkedAtConversationId() {
        return forkedAtConversationId;
    }

    public void setForkedAtConversationId(String forkedAtConversationId) {
        this.forkedAtConversationId = forkedAtConversationId;
    }
}
