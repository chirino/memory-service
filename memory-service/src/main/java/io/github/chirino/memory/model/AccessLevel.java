package io.github.chirino.memory.model;

public enum AccessLevel {
    OWNER,
    MANAGER,
    WRITER,
    READER;

    public static AccessLevel fromString(String value) {
        if (value == null) {
            return null;
        }
        return AccessLevel.valueOf(value.toUpperCase());
    }
}
