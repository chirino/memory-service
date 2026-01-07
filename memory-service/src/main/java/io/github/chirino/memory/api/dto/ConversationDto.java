package io.github.chirino.memory.api.dto;

public class ConversationDto extends ConversationSummaryDto {

    private String conversationGroupId;
    private String forkedAtMessageId;
    private String forkedAtConversationId;

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
}
