package io.github.chirino.memory.subagent.runtime;

import java.util.Map;
import java.util.Objects;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Thread-local auth context used by async sub-agent tasks when no HTTP request scope is active.
 */
public final class SubAgentExecutionContext {

    private static final ThreadLocal<State> CURRENT = new ThreadLocal<>();
    private static final Map<String, State> ACTIVE_BY_CONVERSATION = new ConcurrentHashMap<>();

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

    public static void bindConversation(String conversationId, String userId, String bearerToken) {
        if (conversationId == null || conversationId.isBlank()) {
            return;
        }
        ACTIVE_BY_CONVERSATION.put(conversationId, new State(userId, bearerToken));
    }

    public static void unbindConversation(String conversationId, String bearerToken) {
        if (conversationId == null || conversationId.isBlank()) {
            return;
        }
        State current = ACTIVE_BY_CONVERSATION.get(conversationId);
        if (current != null && Objects.equals(current.bearerToken(), bearerToken)) {
            ACTIVE_BY_CONVERSATION.remove(conversationId, current);
        }
    }

    public static State forConversation(String conversationId) {
        return ACTIVE_BY_CONVERSATION.get(conversationId);
    }

    @FunctionalInterface
    public interface ThrowingSupplier<T> {
        T get() throws Exception;
    }

    public record State(String userId, String bearerToken) {}
}
