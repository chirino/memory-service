package io.github.chirino.memory.api.dto;

public class CreateOwnershipTransferRequest {

    private String conversationId;
    private String newOwnerUserId;

    public String getConversationId() {
        return conversationId;
    }

    public void setConversationId(String conversationId) {
        this.conversationId = conversationId;
    }

    public String getNewOwnerUserId() {
        return newOwnerUserId;
    }

    public void setNewOwnerUserId(String newOwnerUserId) {
        this.newOwnerUserId = newOwnerUserId;
    }
}
