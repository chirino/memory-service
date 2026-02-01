package io.github.chirino.memory.api.dto;

public class IndexConversationsResponse {

    private int indexed;

    public IndexConversationsResponse() {}

    public IndexConversationsResponse(int indexed) {
        this.indexed = indexed;
    }

    public int getIndexed() {
        return indexed;
    }

    public void setIndexed(int indexed) {
        this.indexed = indexed;
    }
}
