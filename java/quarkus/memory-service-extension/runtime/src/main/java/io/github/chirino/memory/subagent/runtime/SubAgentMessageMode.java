package io.github.chirino.memory.subagent.runtime;

public enum SubAgentMessageMode {
    QUEUE("queue"),
    INTERRUPT("interrupt");

    private final String wireValue;

    SubAgentMessageMode(String wireValue) {
        this.wireValue = wireValue;
    }

    public String wireValue() {
        return wireValue;
    }

    public static SubAgentMessageMode parse(String raw) {
        if (raw == null || raw.isBlank()) {
            return null;
        }
        for (SubAgentMessageMode mode : values()) {
            if (mode.wireValue.equalsIgnoreCase(raw.trim())) {
                return mode;
            }
        }
        throw new IllegalArgumentException(
                "Invalid mode '" + raw + "'. Expected one of: queue, interrupt");
    }
}
