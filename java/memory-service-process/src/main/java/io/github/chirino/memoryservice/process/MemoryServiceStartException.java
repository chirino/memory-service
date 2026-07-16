package io.github.chirino.memoryservice.process;

/** Raised when a managed Memory Service process cannot be resolved, launched, or made ready. */
public class MemoryServiceStartException extends RuntimeException {

    public MemoryServiceStartException(String message) {
        super(message);
    }

    public MemoryServiceStartException(String message, Throwable cause) {
        super(message, cause);
    }
}
