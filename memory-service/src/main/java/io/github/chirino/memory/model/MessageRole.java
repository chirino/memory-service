package io.github.chirino.memory.model;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum MessageRole {
    USER,
    ASSISTANT,
    SYSTEM,
    TOOL,
    AGENT;

    @JsonValue
    public String toValue() {
        return name().toLowerCase();
    }

    @JsonCreator
    public static MessageRole fromString(String value) {
        if (value == null) {
            return null;
        }
        return MessageRole.valueOf(value.toUpperCase());
    }
}
