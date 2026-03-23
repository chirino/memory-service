package io.github.chirino.memory.subagent.runtime;

import java.util.Objects;

/**
 * Thread-local auth context used by async sub-agent tasks when no HTTP request scope is active.
 */
public final class SubAgentExecutionContext {

    private static final ThreadLocal<State> CURRENT = new ThreadLocal<>();

    private SubAgentExecutionContext() {}

    public static State current() {
        return CURRENT.get();
    }

    public static <T> T with(String userId, String bearerToken, ThrowingSupplier<T> supplier)
            throws Exception {
        Objects.requireNonNull(supplier, "supplier");
        State previous = CURRENT.get();
        CURRENT.set(new State(userId, bearerToken));
        try {
            return supplier.get();
        } finally {
            if (previous == null) {
                CURRENT.remove();
            } else {
                CURRENT.set(previous);
            }
        }
    }

    @FunctionalInterface
    public interface ThrowingSupplier<T> {
        T get() throws Exception;
    }

    public record State(String userId, String bearerToken) {}
}
