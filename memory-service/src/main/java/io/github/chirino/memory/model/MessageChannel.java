package io.github.chirino.memory.model;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum MessageChannel {
    HISTORY,
    MEMORY,
    SUMMARY;

    @JsonValue
    public String toValue() {
        return name().toLowerCase();
    }

    @JsonCreator
    public static MessageChannel fromString(String value) {
        if (value == null) {
            return null;
        }
        return MessageChannel.valueOf(value.toUpperCase());
    }
}
