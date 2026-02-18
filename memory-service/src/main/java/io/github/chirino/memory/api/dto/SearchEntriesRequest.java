package io.github.chirino.memory.api.dto;

public class SearchEntriesRequest {

    private String query;
    private String searchType;
    private Integer limit;
    private String afterCursor;
    private Boolean includeEntry;
    private Boolean groupByConversation;

    public String getQuery() {
        return query;
    }

    public void setQuery(String query) {
        this.query = query;
    }

    public String getSearchType() {
        return searchType;
    }

    public void setSearchType(String searchType) {
        this.searchType = searchType;
    }

    public Integer getLimit() {
        return limit;
    }

    public void setLimit(Integer limit) {
        this.limit = limit;
    }

    public String getAfterCursor() {
        return afterCursor;
    }

    public void setAfterCursor(String afterCursor) {
        this.afterCursor = afterCursor;
    }

    public Boolean getIncludeEntry() {
        return includeEntry;
    }

    public void setIncludeEntry(Boolean includeEntry) {
        this.includeEntry = includeEntry;
    }

    public Boolean getGroupByConversation() {
        return groupByConversation;
    }

    public void setGroupByConversation(Boolean groupByConversation) {
        this.groupByConversation = groupByConversation;
    }
}
