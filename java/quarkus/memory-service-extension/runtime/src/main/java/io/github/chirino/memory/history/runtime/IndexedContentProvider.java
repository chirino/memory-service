package io.github.chirino.memory.history.runtime;

/**
 * Optional provider for transforming message text before search indexing.
 *
 * <p>When a bean implementing this interface is available, the ConversationStore will use it to
 * compute the indexedContent field for history entries. When no implementation is available,
 * entries are stored without indexedContent (default behavior).
 */
@FunctionalInterface
public interface IndexedContentProvider {

    /**
     * Transforms message text into content suitable for search indexing.
     *
     * @param text the original message text
     * @param role the message role ("USER" or "AI")
     * @return the transformed text for indexing, or null to skip setting indexedContent
     */
    String getIndexedContent(String text, String role);
}
