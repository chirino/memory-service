package io.github.chirino.memory.api.dto;

public class IndexEntryRequest {

    private String conversationId;
    private String entryId;
    private String indexedContent;

    public String getConversationId() {
        return conversationId;
    }

    public void setConversationId(String conversationId) {
        this.conversationId = conversationId;
    }

    public String getEntryId() {
        return entryId;
    }

    public void setEntryId(String entryId) {
        this.entryId = entryId;
    }

    public String getIndexedContent() {
        return indexedContent;
    }

    public void setIndexedContent(String indexedContent) {
        this.indexedContent = indexedContent;
    }
}
