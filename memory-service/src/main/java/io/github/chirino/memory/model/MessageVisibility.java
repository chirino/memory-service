package io.github.chirino.memory.model;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum MessageVisibility {
    USER,
    AGENT,
    SYSTEM;

    @JsonValue
    public String toValue() {
        return name().toLowerCase();
    }

    @JsonCreator
    public static MessageVisibility fromString(String value) {
        if (value == null) {
            return null;
        }
        return MessageVisibility.valueOf(value.toUpperCase());
    }
}
