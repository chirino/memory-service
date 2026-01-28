package io.github.chirino.memory.api.dto;

import java.util.List;

public class SearchEntriesRequest {

    private String query;
    private Integer topK;
    private List<String> conversationIds;
    private String before;

    public String getQuery() {
        return query;
    }

    public void setQuery(String query) {
        this.query = query;
    }

    public Integer getTopK() {
        return topK;
    }

    public void setTopK(Integer topK) {
        this.topK = topK;
    }

    public List<String> getConversationIds() {
        return conversationIds;
    }

    public void setConversationIds(List<String> conversationIds) {
        this.conversationIds = conversationIds;
    }

    public String getBefore() {
        return before;
    }

    public void setBefore(String before) {
        this.before = before;
    }
}
