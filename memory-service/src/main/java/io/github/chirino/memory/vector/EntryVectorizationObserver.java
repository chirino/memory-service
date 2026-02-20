package io.github.chirino.memory.vector;

import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.config.SearchStoreSelector;
import io.github.chirino.memory.vector.EntryVectorizationEvent.EntryToVectorize;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.event.Observes;
import jakarta.enterprise.event.TransactionPhase;
import jakarta.inject.Inject;
import java.time.Duration;
import java.time.OffsetDateTime;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Observes {@link EntryVectorizationEvent} after the originating transaction commits successfully.
 *
 * <p>Why this is an AFTER_SUCCESS observer rather than inline in appendMemoryEntries:
 *
 * <ul>
 *   <li><b>Transaction duration:</b> Computing embeddings requires a network call to the embedding
 *       provider (e.g., OpenAI), which can take seconds. If done inside the append transaction, the
 *       DB connection would be held open for the entire duration, starving the connection pool under
 *       load.
 *   <li><b>Foreign key safety:</b> The {@code entry_embeddings} table has a FK to {@code entries}.
 *       By waiting until AFTER_SUCCESS, the entry row is guaranteed to be committed and visible to
 *       the new transaction that upserts the embedding.
 *   <li><b>Best-effort semantics:</b> Vectorization is not required for the append to succeed. If
 *       it fails or times out, the entry is still persisted with {@code indexed_at = NULL} and will
 *       be picked up by the existing retry mechanism ({@code findEntriesPendingVectorIndexing}).
 * </ul>
 *
 * <p>Note: {@code TransactionPhase.AFTER_SUCCESS} observers run synchronously on the same thread
 * after the transaction commits. This means the HTTP response is slightly delayed by the embedding
 * call, but the DB connection is free. The timeout bounds the worst case.
 */
@ApplicationScoped
public class EntryVectorizationObserver {

    private static final Logger LOG = Logger.getLogger(EntryVectorizationObserver.class);

    @ConfigProperty(name = "memory-server.vectorize-timeout", defaultValue = "PT5S")
    Duration vectorizeTimeout;

    @Inject SearchStoreSelector searchStoreSelector;

    @Inject EmbeddingService embeddingService;

    @Inject MemoryStoreSelector storeSelector;

    /**
     * Called after the transaction that created the entries commits successfully. Attempts to
     * compute embeddings and store them in the vector store.
     *
     * <p>Each entry is processed independently — a failure on one entry does not prevent others
     * from being vectorized.
     */
    public void onEntriesCreated(
            @Observes(during = TransactionPhase.AFTER_SUCCESS) EntryVectorizationEvent event) {

        SearchStore store = searchStoreSelector.getSearchStore();
        boolean canVectorize = store != null && store.isEnabled() && embeddingService.isEnabled();

        if (!canVectorize) {
            // No vector store active — full-text indexing (DB generated column for Postgres,
            // or text index for Mongo) is the only indexing store and it succeeded on persist.
            // Mark entries as fully indexed so findEntriesPendingVectorIndexing skips them.
            for (EntryToVectorize entry : event.getEntries()) {
                storeSelector.getStore().setIndexedAt(entry.entryId(), OffsetDateTime.now());
            }
            return;
        }

        long timeoutMs = vectorizeTimeout.toMillis();
        for (EntryToVectorize entry : event.getEntries()) {
            try {
                String text = entry.indexedContent();
                if (text == null || text.isBlank()) {
                    continue;
                }

                // Compute embedding on a separate thread with a timeout.
                // This is the slow part — a network call to the embedding provider.
                // Using supplyAsync so we can bound the wait time.
                float[] embedding =
                        CompletableFuture.supplyAsync(() -> embeddingService.embed(text))
                                .get(timeoutMs, TimeUnit.MILLISECONDS);
                if (embedding == null || embedding.length == 0) {
                    continue;
                }

                // Upsert the embedding into the vector store.
                // This runs on the current thread. The entry row is committed (AFTER_SUCCESS
                // guarantee), so the FK constraint is satisfied. The upsert method is
                // @Transactional(REQUIRES_NEW) so it creates a new short-lived transaction.
                store.upsertTranscriptEmbedding(
                        entry.conversationGroupId(),
                        entry.conversationId(),
                        entry.entryId(),
                        embedding);

                // Mark the entry as fully indexed (both full-text and vector store succeeded).
                // This also runs in its own transaction via the MemoryStore method.
                storeSelector.getStore().setIndexedAt(entry.entryId(), OffsetDateTime.now());

            } catch (TimeoutException e) {
                LOG.warnf(
                        "Vectorization timed out after %dms for entry %s — will be retried by"
                                + " background task",
                        timeoutMs, entry.entryId());
            } catch (Exception e) {
                LOG.warnf(
                        e,
                        "Failed to vectorize entry %s on append — will be retried by background"
                                + " task",
                        entry.entryId());
            }
        }
    }
}
