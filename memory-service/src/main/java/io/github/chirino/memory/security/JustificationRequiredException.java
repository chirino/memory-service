package io.github.chirino.memory.security;

public class JustificationRequiredException extends RuntimeException {

    public JustificationRequiredException() {
        super("Justification is required for admin operations");
    }
}
