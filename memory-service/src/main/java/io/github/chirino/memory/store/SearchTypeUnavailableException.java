package io.github.chirino.memory.store;

import java.util.List;

/**
 * Exception thrown when the requested search type is not available on the server.
 *
 * <p>This can happen when:
 *
 * <ul>
 *   <li>Semantic search is requested but embeddings are disabled
 *   <li>Full-text search is requested but the PostgreSQL GIN index is not available
 * </ul>
 */
public class SearchTypeUnavailableException extends RuntimeException {

    private final List<String> availableTypes;

    public SearchTypeUnavailableException(String message, List<String> availableTypes) {
        super(message);
        this.availableTypes = availableTypes;
    }

    public List<String> getAvailableTypes() {
        return availableTypes;
    }
}
