package io.github.chirino.memory.model;

public class AdminSearchQuery {

    private String query;
    private String searchType;
    private Integer limit;
    private String after;
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

    public String getAfter() {
        return after;
    }

    public void setAfter(String after) {
        this.after = after;
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
