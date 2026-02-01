package io.github.chirino.memory.api.dto;

public class SearchEntriesRequest {

    private String query;
    private Integer limit;
    private String after;
    private Boolean includeEntry;

    public String getQuery() {
        return query;
    }

    public void setQuery(String query) {
        this.query = query;
    }

    public Integer getLimit() {
        return limit;
    }

    public void setLimit(Integer limit) {
        this.limit = limit;
    }

    public String getAfter() {
        return after;
    }

    public void setAfter(String after) {
        this.after = after;
    }

    public Boolean getIncludeEntry() {
        return includeEntry;
    }

    public void setIncludeEntry(Boolean includeEntry) {
        this.includeEntry = includeEntry;
    }
}
