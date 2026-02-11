package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.model.Channel;
import io.github.chirino.memory.mongo.model.MongoEntry;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import io.quarkus.panache.common.Sort;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import java.util.Optional;

@ApplicationScoped
public class MongoEntryRepository implements PanacheMongoRepositoryBase<MongoEntry, String> {

    public List<MongoEntry> listUserVisible(String conversationId, String afterEntryId, int limit) {
        Sort sort = Sort.by("createdAt").and("id");
        if (afterEntryId != null) {
            Optional<MongoEntry> afterOptional = findByIdOptional(afterEntryId);
            if (afterOptional.isPresent()) {
                MongoEntry after = afterOptional.get();
                if (conversationId.equals(after.conversationId)
                        && after.channel == Channel.HISTORY
                        && after.createdAt != null) {
                    return find(
                                    "conversationId = ?1 and channel = ?2 and createdAt > ?3",
                                    sort,
                                    conversationId,
                                    Channel.HISTORY,
                                    after.createdAt)
                            .page(0, limit)
                            .list();
                }
            }
        }
        return find("conversationId = ?1 and channel = ?2", sort, conversationId, Channel.HISTORY)
                .page(0, limit)
                .list();
    }

    public List<MongoEntry> listByChannel(
            String conversationId,
            String afterEntryId,
            int limit,
            Channel channel,
            String clientId) {
        Sort sort = Sort.by("createdAt").and("id");
        if (afterEntryId != null) {
            Optional<MongoEntry> afterOptional = findByIdOptional(afterEntryId);
            if (afterOptional.isPresent()) {
                MongoEntry after = afterOptional.get();
                if (conversationId.equals(after.conversationId) && after.createdAt != null) {
                    if (channel != null && after.channel != channel) {
                        // If the cursor is from a different channel, ignore it and fall through
                    } else {
                        if (channel != null) {
                            if (channel == Channel.MEMORY && clientId != null) {
                                return find(
                                                "conversationId = ?1 and channel = ?2 and clientId"
                                                        + " = ?3 and createdAt > ?4",
                                                sort,
                                                conversationId,
                                                channel,
                                                clientId,
                                                after.createdAt)
                                        .page(0, limit)
                                        .list();
                            }
                            return find(
                                            "conversationId = ?1 and channel = ?2 and createdAt >"
                                                    + " ?3",
                                            sort,
                                            conversationId,
                                            channel,
                                            after.createdAt)
                                    .page(0, limit)
                                    .list();
                        }
                        return find(
                                        "conversationId = ?1 and createdAt > ?2",
                                        sort,
                                        conversationId,
                                        after.createdAt)
                                .page(0, limit)
                                .list();
                    }
                }
            }
        }

        if (channel != null) {
            if (channel == Channel.MEMORY && clientId != null) {
                return find(
                                "conversationId = ?1 and channel = ?2 and clientId = ?3",
                                sort,
                                conversationId,
                                channel,
                                clientId)
                        .page(0, limit)
                        .list();
            }
            return find("conversationId = ?1 and channel = ?2", sort, conversationId, channel)
                    .page(0, limit)
                    .list();
        }
        return find("conversationId = ?1", sort, conversationId).page(0, limit).list();
    }

    /**
     * Lists memory entries at the latest epoch. For API consistency with PostgreSQL implementation.
     * MongoDB doesn't support subqueries, so this uses a two-step approach internally.
     *
     * @return entries at latest epoch, or empty list if no memory entries exist
     */
    public List<MongoEntry> listMemoryEntriesAtLatestEpoch(String conversationId, String clientId) {
        return listMemoryEntriesAtLatestEpoch(conversationId, null, Integer.MAX_VALUE, clientId);
    }

    /**
     * Lists memory entries at the latest epoch with pagination support.
     *
     * @return entries at latest epoch, or empty list if no memory entries exist
     */
    public List<MongoEntry> listMemoryEntriesAtLatestEpoch(
            String conversationId, String afterEntryId, int limit, String clientId) {
        // Find max epoch first (efficient: uses descending sort + limit 1)
        Sort epochSort = Sort.by("epoch").descending();
        MongoEntry latest =
                find(
                                "conversationId = ?1 and channel = ?2 and clientId = ?3 and"
                                        + " epoch != null",
                                epochSort,
                                conversationId,
                                Channel.MEMORY,
                                clientId)
                        .page(0, 1)
                        .firstResult();

        if (latest == null) {
            return List.of();
        }

        // Then query entries at that epoch
        return listMemoryEntriesByEpoch(
                conversationId, afterEntryId, limit, latest.epoch, clientId);
    }

    public List<MongoEntry> listMemoryEntriesByEpoch(
            String conversationId, Long epoch, String clientId) {
        return listMemoryEntriesByEpoch(conversationId, null, Integer.MAX_VALUE, epoch, clientId);
    }

    public List<MongoEntry> listMemoryEntriesByEpoch(
            String conversationId, String afterEntryId, int limit, Long epoch, String clientId) {
        Sort sort = Sort.by("createdAt").and("id");
        List<Object> params = new ArrayList<>();
        params.add(conversationId);
        params.add(Channel.MEMORY);
        params.add(clientId);
        String query = "conversationId = ?1 and channel = ?2 and clientId = ?3";
        if (epoch == null) {
            query += " and epoch = null";
        } else {
            query += " and epoch = ?" + (params.size() + 1);
            params.add(epoch);
        }
        if (afterEntryId != null) {
            Optional<MongoEntry> afterOptional = findByIdOptional(afterEntryId);
            if (afterOptional.isPresent()) {
                MongoEntry after = afterOptional.get();
                if (conversationId.equals(after.conversationId)
                        && after.channel == Channel.MEMORY
                        && after.createdAt != null
                        && Objects.equals(after.epoch, epoch)) {
                    params.add(after.createdAt);
                    query += " and createdAt > ?" + params.size();
                }
            }
        }
        return find(query, sort, params.toArray()).page(0, limit).list();
    }

    /**
     * Lists entries by conversation group ID, ordered by created_at. Used for fork-aware entry
     * retrieval where we need all entries across a conversation group.
     *
     * @param conversationGroupId the conversation group ID
     * @param channel optional channel filter (null for all channels)
     * @param clientId client ID filter (required for MEMORY channel)
     * @return entries ordered by created_at, id
     */
    public List<MongoEntry> listByConversationGroup(
            String conversationGroupId, Channel channel, String clientId) {
        Sort sort = Sort.by("createdAt").and("id");
        if (channel != null) {
            if (channel == Channel.MEMORY && clientId != null) {
                return find(
                                "conversationGroupId = ?1 and channel = ?2 and clientId = ?3",
                                sort,
                                conversationGroupId,
                                channel,
                                clientId)
                        .list();
            }
            return find(
                            "conversationGroupId = ?1 and channel = ?2",
                            sort,
                            conversationGroupId,
                            channel)
                    .list();
        }
        return find("conversationGroupId = ?1", sort, conversationGroupId).list();
    }
}
