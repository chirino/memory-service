package io.github.chirino.memory.vector;

import java.util.List;

/**
 * CDI event fired when newly appended entries have indexed content that should be vectorized.
 *
 * <p>This event is observed with {@code TransactionPhase.AFTER_SUCCESS} so that vectorization
 * happens AFTER the originating transaction commits. This is critical because:
 *
 * <ol>
 *   <li>The entry row must exist in the database before the {@code entry_embeddings} foreign key
 *       can reference it.
 *   <li>We don't want to hold the original transaction (and its DB connection) open while waiting
 *       for the embedding provider network call, which could take seconds.
 *   <li>The embedding upsert runs in a separate, short-lived transaction.
 * </ol>
 *
 * <p>If vectorization fails or times out, it is logged as a warning. The entry remains with {@code
 * indexed_at = NULL} and will be picked up by the existing retry mechanism ({@code
 * findEntriesPendingVectorIndexing}).
 */
public class EntryVectorizationEvent {

    /** Simple carrier for the data needed to vectorize an entry after commit. */
    public record EntryToVectorize(
            String entryId,
            String conversationId,
            String conversationGroupId,
            String indexedContent) {}

    private final List<EntryToVectorize> entries;

    public EntryVectorizationEvent(List<EntryToVectorize> entries) {
        this.entries = entries;
    }

    public List<EntryToVectorize> getEntries() {
        return entries;
    }
}
