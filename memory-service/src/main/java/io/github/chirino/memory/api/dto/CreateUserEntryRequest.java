package io.github.chirino.memory.api.dto;

import java.util.List;
import java.util.Map;

public class CreateUserEntryRequest {

    private String content;
    private Map<String, Object> metadata;
    private List<Map<String, Object>> attachments;
    private String forkedAtConversationId;
    private String forkedAtEntryId;

    public String getContent() {
        return content;
    }

    public void setContent(String content) {
        this.content = content;
    }

    public Map<String, Object> getMetadata() {
        return metadata;
    }

    public void setMetadata(Map<String, Object> metadata) {
        this.metadata = metadata;
    }

    public List<Map<String, Object>> getAttachments() {
        return attachments;
    }

    public void setAttachments(List<Map<String, Object>> attachments) {
        this.attachments = attachments;
    }

    public String getForkedAtConversationId() {
        return forkedAtConversationId;
    }

    public void setForkedAtConversationId(String forkedAtConversationId) {
        this.forkedAtConversationId = forkedAtConversationId;
    }

    public String getForkedAtEntryId() {
        return forkedAtEntryId;
    }

    public void setForkedAtEntryId(String forkedAtEntryId) {
        this.forkedAtEntryId = forkedAtEntryId;
    }
}
