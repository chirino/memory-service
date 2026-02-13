package io.github.chirino.memoryservice.history;

import java.util.List;
import java.util.Map;
import org.springframework.lang.Nullable;

/**
 * Builder for creating {@link ConversationHistoryStreamAdvisor} instances with a specific bearer
 * token.
 *
 * <p>This builder should be used to create a new advisor instance per request, capturing the bearer
 * token on the HTTP request thread before any reactive processing occurs. This ensures the token is
 * available throughout the advisor's lifecycle, even when processing moves to worker threads where
 * the SecurityContext is not available.
 */
public class ConversationHistoryStreamAdvisorBuilder {

    private final ConversationStore conversationStore;
    private final ResponseResumer responseResumer;

    public ConversationHistoryStreamAdvisorBuilder(
            ConversationStore conversationStore, ResponseResumer responseResumer) {
        this.conversationStore = conversationStore;
        this.responseResumer = responseResumer;
    }

    /**
     * Builds a new {@link ConversationHistoryStreamAdvisor} with the given bearer token.
     *
     * @param bearerToken the OAuth2 bearer token to use for memory-service API calls, or null if
     *     not available
     * @return a new advisor instance configured with the token
     */
    public ConversationHistoryStreamAdvisor build(@Nullable String bearerToken) {
        return new ConversationHistoryStreamAdvisor(
                conversationStore, responseResumer, bearerToken);
    }

    /**
     * Builds a new {@link ConversationHistoryStreamAdvisor} with the given bearer token and a
     * shared list for collecting tool-produced attachments.
     *
     * @param bearerToken the OAuth2 bearer token to use for memory-service API calls, or null if
     *     not available
     * @param toolAttachments a thread-safe list that tools populate with attachment metadata during
     *     execution; the advisor links these to the AI response entry
     * @return a new advisor instance configured with the token and attachment collector
     */
    public ConversationHistoryStreamAdvisor build(
            @Nullable String bearerToken, List<Map<String, Object>> toolAttachments) {
        return new ConversationHistoryStreamAdvisor(
                conversationStore, responseResumer, bearerToken, toolAttachments);
    }
}
