package io.github.chirino.memory.api.dto;

import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.NotNull;
import jakarta.validation.constraints.Size;

public class CreateOwnershipTransferRequest {

    @NotNull private String conversationId;

    @NotBlank
    @Size(max = 255)
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
