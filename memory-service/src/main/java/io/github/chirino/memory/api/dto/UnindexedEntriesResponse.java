package io.github.chirino.memory.api.dto;

import java.util.List;

public class UnindexedEntriesResponse {

    private List<UnindexedEntry> data;
    private String cursor;

    public UnindexedEntriesResponse() {}

    public UnindexedEntriesResponse(List<UnindexedEntry> data, String cursor) {
        this.data = data;
        this.cursor = cursor;
    }

    public List<UnindexedEntry> getData() {
        return data;
    }

    public void setData(List<UnindexedEntry> data) {
        this.data = data;
    }

    public String getCursor() {
        return cursor;
    }

    public void setCursor(String cursor) {
        this.cursor = cursor;
    }
}
