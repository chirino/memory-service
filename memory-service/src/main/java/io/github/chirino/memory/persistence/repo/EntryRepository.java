package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.model.Channel;
import io.github.chirino.memory.persistence.entity.EntryEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.persistence.NoResultException;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import java.util.UUID;

@ApplicationScoped
public class EntryRepository implements PanacheRepositoryBase<EntryEntity, UUID> {

    public List<EntryEntity> listUserVisible(UUID conversationId, String afterEntryId, int limit) {
        String baseQuery =
                "from EntryEntity m where m.conversation.id = ?1 and m.channel = ?2 and"
                        + " m.conversation.deletedAt IS NULL and"
                        + " m.conversation.conversationGroup.deletedAt IS NULL";
        if (afterEntryId != null) {
            UUID afterId = UUID.fromString(afterEntryId);
            EntryEntity afterEntry = findById(afterId);
            if (afterEntry != null
                    && afterEntry.getConversation() != null
                    && conversationId.equals(afterEntry.getConversation().getId())) {
                return find(
                                baseQuery + " and m.createdAt > ?3 order by m.createdAt, m.id",
                                conversationId,
                                Channel.HISTORY,
                                afterEntry.getCreatedAt())
                        .page(0, limit)
                        .list();
            }
        }
        return find(baseQuery + " order by m.createdAt, m.id", conversationId, Channel.HISTORY)
                .page(0, limit)
                .list();
    }

    public List<EntryEntity> listByChannel(
            UUID conversationId, String afterEntryId, int limit, Channel channel, String clientId) {
        String baseQuery =
                "from EntryEntity m where m.conversation.id = ?1 and m.conversation.deletedAt IS"
                        + " NULL and m.conversation.conversationGroup.deletedAt IS NULL";
        List<Object> params = new ArrayList<>();
        params.add(conversationId);

        if (channel != null) {
            baseQuery += " and m.channel = ?" + (params.size() + 1);
            params.add(channel);
        }
        if (channel == Channel.MEMORY) {
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
                return find(query, params.toArray()).page(0, limit).list();
            }
        }

        String query = baseQuery + " order by m.createdAt, m.id";
        return find(query, params.toArray()).page(0, limit).list();
    }

    public Long findLatestMemoryEpoch(UUID conversationId, String clientId) {
        try {
            return getEntityManager()
                    .createQuery(
                            "select max(m.epoch) from EntryEntity m where m.conversation.id = :cid"
                                    + " and m.channel = :channel and m.clientId = :clientId and"
                                    + " m.conversation.deletedAt IS NULL and"
                                    + " m.conversation.conversationGroup.deletedAt IS NULL",
                            Long.class)
                    .setParameter("cid", conversationId)
                    .setParameter("channel", Channel.MEMORY)
                    .setParameter("clientId", clientId)
                    .getSingleResult();
        } catch (NoResultException e) {
            return null;
        }
    }

    public List<EntryEntity> listMemoryEntriesByEpoch(
            UUID conversationId, Long epoch, String clientId) {
        return listMemoryEntriesByEpoch(conversationId, null, Integer.MAX_VALUE, epoch, clientId);
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
}
