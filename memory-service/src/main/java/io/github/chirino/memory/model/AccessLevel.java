package io.github.chirino.memory.model;

import com.fasterxml.jackson.annotation.JsonCreator;
import com.fasterxml.jackson.annotation.JsonValue;

public enum AccessLevel {
    OWNER,
    MANAGER,
    WRITER,
    READER;

    @JsonValue
    public String toValue() {
        return name().toLowerCase();
    }

    @JsonCreator
    public static AccessLevel fromString(String value) {
        if (value == null) {
            return null;
        }
        return AccessLevel.valueOf(value.toUpperCase());
    }
}
