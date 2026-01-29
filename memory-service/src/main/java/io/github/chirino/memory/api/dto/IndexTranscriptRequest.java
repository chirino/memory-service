package io.github.chirino.memory.api.dto;

public class IndexTranscriptRequest {

    private String conversationId;
    private String title;
    private String transcript;
    private String untilEntryId;

    public String getConversationId() {
        return conversationId;
    }

    public void setConversationId(String conversationId) {
        this.conversationId = conversationId;
    }

    public String getTitle() {
        return title;
    }

    public void setTitle(String title) {
        this.title = title;
    }

    public String getTranscript() {
        return transcript;
    }

    public void setTranscript(String transcript) {
        this.transcript = transcript;
    }

    public String getUntilEntryId() {
        return untilEntryId;
    }

    public void setUntilEntryId(String untilEntryId) {
        this.untilEntryId = untilEntryId;
    }
}
