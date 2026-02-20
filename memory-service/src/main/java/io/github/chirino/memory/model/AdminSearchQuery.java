package io.github.chirino.memory.model;

import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.Size;

public class AdminSearchQuery {

    @NotBlank
    @Size(max = 1000)
    private String query;

    private String searchType;

    @Min(1)
    @Max(1000)
    private Integer limit;

    @Size(max = 100)
    private String afterCursor;

    @Size(max = 255)
    private String userId;

    private boolean includeDeleted;
    private Boolean includeEntry;
    private Boolean groupByConversation;

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

    public String getAfterCursor() {
        return afterCursor;
    }

    public void setAfterCursor(String afterCursor) {
        this.afterCursor = afterCursor;
    }

    public String getUserId() {
        return userId;
    }

    public void setUserId(String userId) {
        this.userId = userId;
    }

    public boolean isIncludeDeleted() {
        return includeDeleted;
    }

    public void setIncludeDeleted(boolean includeDeleted) {
        this.includeDeleted = includeDeleted;
    }

    public Boolean getIncludeEntry() {
        return includeEntry;
    }

    public void setIncludeEntry(Boolean includeEntry) {
        this.includeEntry = includeEntry;
    }

    public String getSearchType() {
        return searchType;
    }

    public void setSearchType(String searchType) {
        this.searchType = searchType;
    }

    public Boolean getGroupByConversation() {
        return groupByConversation;
    }

    public void setGroupByConversation(Boolean groupByConversation) {
        this.groupByConversation = groupByConversation;
    }
}
