package io.github.chirino.memory.api.dto;

public class CreateSummaryRequest {

    private String title;
    private String summary;
    private String untilMessageId;
    private String summarizedAt;

    public String getTitle() {
        return title;
    }

    public void setTitle(String title) {
        this.title = title;
    }

    public String getSummary() {
        return summary;
    }

    public void setSummary(String summary) {
        this.summary = summary;
    }

    public String getUntilMessageId() {
        return untilMessageId;
    }

    public void setUntilMessageId(String untilMessageId) {
        this.untilMessageId = untilMessageId;
    }

    public String getSummarizedAt() {
        return summarizedAt;
    }

    public void setSummarizedAt(String summarizedAt) {
        this.summarizedAt = summarizedAt;
    }
}
