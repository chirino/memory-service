package io.github.chirino.memory.api.dto;

import java.util.List;

public class UnindexedEntriesResponse {

    private List<UnindexedEntry> data;
    private String afterCursor;

    public UnindexedEntriesResponse() {}

    public UnindexedEntriesResponse(List<UnindexedEntry> data, String afterCursor) {
        this.data = data;
        this.afterCursor = afterCursor;
    }

    public List<UnindexedEntry> getData() {
        return data;
    }

    public void setData(List<UnindexedEntry> data) {
        this.data = data;
    }

    public String getAfterCursor() {
        return afterCursor;
    }

    public void setAfterCursor(String afterCursor) {
        this.afterCursor = afterCursor;
    }
}
