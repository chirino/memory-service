package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.model.MessageChannel;
import io.github.chirino.memory.persistence.entity.MessageEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.persistence.NoResultException;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import java.util.UUID;

@ApplicationScoped
public class MessageRepository implements PanacheRepositoryBase<MessageEntity, UUID> {

    public List<MessageEntity> listUserVisible(
            UUID conversationId, String afterMessageId, int limit) {
        String baseQuery =
                "from MessageEntity m where m.conversation.id = ?1 and m.channel = ?2 and"
                        + " m.conversation.deletedAt IS NULL and"
                        + " m.conversation.conversationGroup.deletedAt IS NULL";
        if (afterMessageId != null) {
            UUID afterId = UUID.fromString(afterMessageId);
            MessageEntity afterMessage = findById(afterId);
            if (afterMessage != null
                    && afterMessage.getConversation() != null
                    && conversationId.equals(afterMessage.getConversation().getId())) {
                return find(
                                baseQuery + " and m.createdAt > ?3 order by m.createdAt, m.id",
                                conversationId,
                                MessageChannel.HISTORY,
                                afterMessage.getCreatedAt())
                        .page(0, limit)
                        .list();
            }
        }
        return find(
                        baseQuery + " order by m.createdAt, m.id",
                        conversationId,
                        MessageChannel.HISTORY)
                .page(0, limit)
                .list();
    }

    public List<MessageEntity> listByChannel(
            UUID conversationId,
            String afterMessageId,
            int limit,
            MessageChannel channel,
            String clientId) {
        String baseQuery =
                "from MessageEntity m where m.conversation.id = ?1 and m.conversation.deletedAt IS"
                        + " NULL and m.conversation.conversationGroup.deletedAt IS NULL";
        List<Object> params = new ArrayList<>();
        params.add(conversationId);

        if (channel != null) {
            baseQuery += " and m.channel = ?" + (params.size() + 1);
            params.add(channel);
        }
        if (channel == MessageChannel.MEMORY) {
            baseQuery += " and m.clientId = ?" + (params.size() + 1);
            params.add(clientId);
        }

        if (afterMessageId != null) {
            UUID afterId = UUID.fromString(afterMessageId);
            MessageEntity afterMessage = findById(afterId);
            if (afterMessage != null
                    && afterMessage.getConversation() != null
                    && conversationId.equals(afterMessage.getConversation().getId())
                    && afterMessage.getCreatedAt() != null) {
                params.add(afterMessage.getCreatedAt());
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
                            "select max(m.memoryEpoch) from MessageEntity m where m.conversation.id"
                                + " = :cid and m.channel = :channel and m.clientId = :clientId and"
                                + " m.conversation.deletedAt IS NULL and"
                                + " m.conversation.conversationGroup.deletedAt IS NULL",
                            Long.class)
                    .setParameter("cid", conversationId)
                    .setParameter("channel", MessageChannel.MEMORY)
                    .setParameter("clientId", clientId)
                    .getSingleResult();
        } catch (NoResultException e) {
            return null;
        }
    }

    public List<MessageEntity> listMemoryMessagesByEpoch(
            UUID conversationId, Long epoch, String clientId) {
        return listMemoryMessagesByEpoch(conversationId, null, Integer.MAX_VALUE, epoch, clientId);
    }

    public List<MessageEntity> listMemoryMessagesByEpoch(
            UUID conversationId, String afterMessageId, int limit, Long epoch, String clientId) {
        String baseQuery =
                "from MessageEntity m where m.conversation.id = ?1 and m.channel = ?2 and"
                        + " m.clientId = ?3 and m.conversation.deletedAt IS NULL and"
                        + " m.conversation.conversationGroup.deletedAt IS NULL";
        List<Object> params = new ArrayList<>();
        params.add(conversationId);
        params.add(MessageChannel.MEMORY);
        params.add(clientId);

        if (epoch == null) {
            baseQuery += " and m.memoryEpoch is null";
        } else {
            baseQuery += " and m.memoryEpoch = ?" + (params.size() + 1);
            params.add(epoch);
        }

        if (afterMessageId != null) {
            UUID afterId = UUID.fromString(afterMessageId);
            MessageEntity afterMessage = findById(afterId);
            if (afterMessage != null
                    && afterMessage.getConversation() != null
                    && conversationId.equals(afterMessage.getConversation().getId())
                    && afterMessage.getCreatedAt() != null
                    && afterMessage.getChannel() == MessageChannel.MEMORY
                    && Objects.equals(afterMessage.getMemoryEpoch(), epoch)) {
                params.add(afterMessage.getCreatedAt());
                baseQuery += " and m.createdAt > ?" + params.size();
            }
        }

        String query = baseQuery + " order by m.createdAt, m.id";
        return find(query, params.toArray()).page(0, limit).list();
    }
}
