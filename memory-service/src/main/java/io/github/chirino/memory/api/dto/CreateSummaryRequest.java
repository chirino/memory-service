package io.github.chirino.memory.api.dto;

public class CreateSummaryRequest {

    private String title;
    private String summary;
    private String untilEntryId;
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

    public String getUntilEntryId() {
        return untilEntryId;
    }

    public void setUntilEntryId(String untilEntryId) {
        this.untilEntryId = untilEntryId;
    }

    public String getSummarizedAt() {
        return summarizedAt;
    }

    public void setSummarizedAt(String summarizedAt) {
        this.summarizedAt = summarizedAt;
    }
}
