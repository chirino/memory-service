package io.github.chirino.memory.api.dto;

public class UnindexedEntry {

    private String conversationId;
    private EntryDto entry;

    public UnindexedEntry() {}

    public UnindexedEntry(String conversationId, EntryDto entry) {
        this.conversationId = conversationId;
        this.entry = entry;
    }

    public String getConversationId() {
        return conversationId;
    }

    public void setConversationId(String conversationId) {
        this.conversationId = conversationId;
    }

    public EntryDto getEntry() {
        return entry;
    }

    public void setEntry(EntryDto entry) {
        this.entry = entry;
    }
}
