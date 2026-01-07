package io.github.chirino.memory.api.dto;

import java.util.Map;

public class CreateConversationRequest {

    private String title;
    private Map<String, Object> metadata;

    public String getTitle() {
        return title;
    }

    public void setTitle(String title) {
        this.title = title;
    }

    public Map<String, Object> getMetadata() {
        return metadata;
    }

    public void setMetadata(Map<String, Object> metadata) {
        this.metadata = metadata;
    }
}
