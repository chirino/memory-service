package io.github.chirino.memory.api.dto;

public class SearchResultDto {

    private String conversationId;
    private String conversationTitle;
    private EntryDto entry;
    private double score;
    private String highlights;

    public String getConversationId() {
        return conversationId;
    }

    public void setConversationId(String conversationId) {
        this.conversationId = conversationId;
    }

    public String getConversationTitle() {
        return conversationTitle;
    }

    public void setConversationTitle(String conversationTitle) {
        this.conversationTitle = conversationTitle;
    }

    public EntryDto getEntry() {
        return entry;
    }

    public void setEntry(EntryDto entry) {
        this.entry = entry;
    }

    public double getScore() {
        return score;
    }

    public void setScore(double score) {
        this.score = score;
    }

    public String getHighlights() {
        return highlights;
    }

    public void setHighlights(String highlights) {
        this.highlights = highlights;
    }
}
