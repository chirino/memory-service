package io.github.chirino.memory.model;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum Channel {
    HISTORY,
    MEMORY;

    @JsonValue
    public String toValue() {
        return name().toLowerCase();
    }

    @JsonCreator
    public static Channel fromString(String value) {
        if (value == null) {
            return null;
        }
        return Channel.valueOf(value.toUpperCase());
    }
}
