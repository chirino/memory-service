package io.github.chirino.memory.service;

import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.store.MemoryStore;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.time.Duration;
import java.time.OffsetDateTime;
import java.util.Set;
import java.util.function.Consumer;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@ApplicationScoped
public class EvictionService {

    private static final Logger LOG = Logger.getLogger(EvictionService.class);

    @Inject MemoryStoreSelector storeSelector;

    @ConfigProperty(name = "memory-service.eviction.batch-size", defaultValue = "1000")
    int batchSize;

    @ConfigProperty(name = "memory-service.eviction.batch-delay-ms", defaultValue = "100")
    long batchDelayMs;

    /**
     * Evict soft-deleted resources older than the cutoff.
     *
     * @param retentionPeriod resources deleted longer than this are hard-deleted
     * @param resourceTypes which resource types to evict
     * @param progressCallback optional callback for progress updates (0-100)
     */
    public void evict(
            Duration retentionPeriod,
            Set<String> resourceTypes,
            Consumer<Integer> progressCallback) {
        OffsetDateTime cutoff = OffsetDateTime.now().minus(retentionPeriod);

        // Estimate total work for progress calculation
        MemoryStore store = storeSelector.getStore();
        long totalEstimate = estimateTotalRecords(store, cutoff, resourceTypes);
        long processed = 0;

        LOG.infof(
                "Starting eviction: retentionPeriod=%s, resourceTypes=%s, estimatedRecords=%d",
                retentionPeriod, resourceTypes, totalEstimate);

        if (resourceTypes.contains("conversation_groups")) {
            processed =
                    evictConversationGroups(
                            store, cutoff, processed, totalEstimate, progressCallback);
        }

        if (resourceTypes.contains("conversation_memberships")) {
            processed = evictMemberships(store, cutoff, processed, totalEstimate, progressCallback);
        }

        // Final 100% progress
        if (progressCallback != null) {
            progressCallback.accept(100);
        }

        LOG.infof("Eviction completed: processed %d records", processed);
    }

    private long evictConversationGroups(
            MemoryStore store,
            OffsetDateTime cutoff,
            long processed,
            long totalEstimate,
            Consumer<Integer> progressCallback) {
        while (true) {
            var batch = store.findEvictableGroupIds(cutoff, batchSize);
            if (batch.isEmpty()) {
                break;
            }

            // Hard delete (creates vector_store_delete tasks, then deletes from data store)
            store.hardDeleteConversationGroups(batch);

            processed += batch.size();
            reportProgress(processed, totalEstimate, progressCallback);

            sleepBetweenBatches();
        }
        return processed;
    }

    private long evictMemberships(
            MemoryStore store,
            OffsetDateTime cutoff,
            long processed,
            long totalEstimate,
            Consumer<Integer> progressCallback) {
        while (true) {
            int deleted = store.hardDeleteMembershipsBatch(cutoff, batchSize);
            if (deleted == 0) {
                break;
            }

            processed += deleted;
            reportProgress(processed, totalEstimate, progressCallback);

            sleepBetweenBatches();
        }
        return processed;
    }

    private void reportProgress(long processed, long total, Consumer<Integer> callback) {
        if (callback != null && total > 0) {
            int progress = (int) Math.min(99, (processed * 100) / total);
            callback.accept(progress);
        }
    }

    private long estimateTotalRecords(
            MemoryStore store, OffsetDateTime cutoff, Set<String> resourceTypes) {
        long total = 0;
        if (resourceTypes.contains("conversation_groups")) {
            total += store.countEvictableGroups(cutoff);
        }
        if (resourceTypes.contains("conversation_memberships")) {
            total += store.countEvictableMemberships(cutoff);
        }
        return total;
    }

    private void sleepBetweenBatches() {
        if (batchDelayMs > 0) {
            try {
                Thread.sleep(batchDelayMs);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                throw new RuntimeException("Eviction interrupted", e);
            }
        }
    }
}
