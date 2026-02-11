package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.model.Channel;
import io.github.chirino.memory.persistence.entity.EntryEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import java.util.UUID;
import org.jboss.logging.Logger;

@ApplicationScoped
public class EntryRepository implements PanacheRepositoryBase<EntryEntity, UUID> {

    private static final Logger LOG = Logger.getLogger(EntryRepository.class);

    public List<EntryEntity> listByChannel(
            UUID conversationId, String afterEntryId, int limit, Channel channel, String clientId) {
        LOG.infof(
                "listByChannel: conversationId=%s, afterEntryId=%s, limit=%d, channel=%s,"
                        + " clientId=%s",
                conversationId, afterEntryId, limit, channel, clientId);
        String baseQuery =
                "from EntryEntity m where m.conversation.id = ?1 and m.conversation.deletedAt IS"
                        + " NULL and m.conversation.conversationGroup.deletedAt IS NULL";
        List<Object> params = new ArrayList<>();
        params.add(conversationId);

        if (channel != null) {
            baseQuery += " and m.channel = ?" + (params.size() + 1);
            params.add(channel);
        }
        if (channel == Channel.MEMORY && clientId != null) {
            baseQuery += " and m.clientId = ?" + (params.size() + 1);
            params.add(clientId);
        }

        if (afterEntryId != null) {
            UUID afterId = UUID.fromString(afterEntryId);
            EntryEntity afterEntry = findById(afterId);
            if (afterEntry != null
                    && afterEntry.getConversation() != null
                    && conversationId.equals(afterEntry.getConversation().getId())
                    && afterEntry.getCreatedAt() != null) {
                params.add(afterEntry.getCreatedAt());
                String query =
                        baseQuery
                                + " and m.createdAt > ?"
                                + params.size()
                                + " order by m.createdAt, m.id";
                LOG.infof("listByChannel: executing query=%s with params=%s", query, params);
                List<EntryEntity> result = find(query, params.toArray()).page(0, limit).list();
                LOG.infof("listByChannel: found %d entries", result.size());
                return result;
            }
        }

        String query = baseQuery + " order by m.createdAt, m.id";
        LOG.infof("listByChannel: executing query=%s with params=%s", query, params);
        List<EntryEntity> result = find(query, params.toArray()).page(0, limit).list();
        LOG.infof("listByChannel: found %d entries", result.size());
        return result;
    }

    /**
     * Lists memory entries at the latest epoch in a single query. This combines the max(epoch)
     * lookup with the entry list query to eliminate one database round-trip.
     *
     * @return entries at latest epoch, or empty list if no memory entries exist
     */
    public List<EntryEntity> listMemoryEntriesAtLatestEpoch(UUID conversationId, String clientId) {
        return listMemoryEntriesAtLatestEpoch(conversationId, null, Integer.MAX_VALUE, clientId);
    }

    /**
     * Lists memory entries at the latest epoch with pagination support. Uses a subquery to find
     * max(epoch) and fetch entries in a single database round-trip.
     *
     * @return entries at latest epoch, or empty list if no memory entries exist
     */
    public List<EntryEntity> listMemoryEntriesAtLatestEpoch(
            UUID conversationId, String afterEntryId, int limit, String clientId) {
        LOG.infof(
                "listMemoryEntriesAtLatestEpoch: conversationId=%s, afterEntryId=%s, limit=%d,"
                        + " clientId=%s",
                conversationId, afterEntryId, limit, clientId);
        // Build the subquery to find max epoch
        String maxEpochSubquery =
                "(select max(m2.epoch) from EntryEntity m2 where m2.conversation.id = ?1"
                        + " and m2.channel = ?2 and m2.clientId = ?3)";

        String baseQuery =
                "from EntryEntity m where m.conversation.id = ?1 and m.channel = ?2 and"
                        + " m.clientId = ?3 and m.conversation.deletedAt IS NULL and"
                        + " m.conversation.conversationGroup.deletedAt IS NULL"
                        + " and m.epoch = "
                        + maxEpochSubquery;

        List<Object> params = new ArrayList<>();
        params.add(conversationId);
        params.add(Channel.MEMORY);
        params.add(clientId);

        if (afterEntryId != null) {
            UUID afterId = UUID.fromString(afterEntryId);
            EntryEntity afterEntry = findById(afterId);
            if (afterEntry != null
                    && afterEntry.getConversation() != null
                    && conversationId.equals(afterEntry.getConversation().getId())
                    && afterEntry.getCreatedAt() != null
                    && afterEntry.getChannel() == Channel.MEMORY) {
                params.add(afterEntry.getCreatedAt());
                baseQuery += " and m.createdAt > ?" + params.size();
            }
        }

        String query = baseQuery + " order by m.createdAt, m.id";
        LOG.infof(
                "listMemoryEntriesAtLatestEpoch: executing query=%s with params=%s", query, params);
        List<EntryEntity> result = find(query, params.toArray()).page(0, limit).list();
        LOG.infof("listMemoryEntriesAtLatestEpoch: found %d entries", result.size());
        return result;
    }

    public List<EntryEntity> listMemoryEntriesByEpoch(
            UUID conversationId, String afterEntryId, int limit, Long epoch, String clientId) {
        String baseQuery =
                "from EntryEntity m where m.conversation.id = ?1 and m.channel = ?2 and"
                        + " m.clientId = ?3 and m.conversation.deletedAt IS NULL and"
                        + " m.conversation.conversationGroup.deletedAt IS NULL";
        List<Object> params = new ArrayList<>();
        params.add(conversationId);
        params.add(Channel.MEMORY);
        params.add(clientId);

        if (epoch == null) {
            baseQuery += " and m.epoch is null";
        } else {
            baseQuery += " and m.epoch = ?" + (params.size() + 1);
            params.add(epoch);
        }

        if (afterEntryId != null) {
            UUID afterId = UUID.fromString(afterEntryId);
            EntryEntity afterEntry = findById(afterId);
            if (afterEntry != null
                    && afterEntry.getConversation() != null
                    && conversationId.equals(afterEntry.getConversation().getId())
                    && afterEntry.getCreatedAt() != null
                    && afterEntry.getChannel() == Channel.MEMORY
                    && Objects.equals(afterEntry.getEpoch(), epoch)) {
                params.add(afterEntry.getCreatedAt());
                baseQuery += " and m.createdAt > ?" + params.size();
            }
        }

        String query = baseQuery + " order by m.createdAt, m.id";
        return find(query, params.toArray()).page(0, limit).list();
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
    public List<EntryEntity> listByConversationGroup(
            UUID conversationGroupId, Channel channel, String clientId) {
        LOG.infof(
                "listByConversationGroup: conversationGroupId=%s, channel=%s, clientId=%s",
                conversationGroupId, channel, clientId);

        String baseQuery =
                "from EntryEntity m where m.conversationGroupId = ?1"
                        + " and m.conversation.deletedAt IS NULL"
                        + " and m.conversation.conversationGroup.deletedAt IS NULL";
        List<Object> params = new ArrayList<>();
        params.add(conversationGroupId);

        if (channel != null) {
            baseQuery += " and m.channel = ?" + (params.size() + 1);
            params.add(channel);
        }
        if (channel == Channel.MEMORY && clientId != null) {
            baseQuery += " and m.clientId = ?" + (params.size() + 1);
            params.add(clientId);
        }

        String query = baseQuery + " order by m.createdAt, m.id";
        LOG.infof("listByConversationGroup: executing query=%s with params=%s", query, params);
        List<EntryEntity> result = find(query, params.toArray()).list();
        LOG.infof("listByConversationGroup: found %d entries", result.size());
        return result;
    }
}
