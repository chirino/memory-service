package io.github.chirino.memory.persistence.repo;

import io.github.chirino.memory.model.MessageChannel;
import io.github.chirino.memory.persistence.entity.MessageEntity;
import io.quarkus.hibernate.orm.panache.PanacheRepositoryBase;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;

@ApplicationScoped
public class MessageRepository implements PanacheRepositoryBase<MessageEntity, UUID> {

    public List<MessageEntity> listUserVisible(
            UUID conversationId, String afterMessageId, int limit) {
        String baseQuery = "from MessageEntity m where m.conversation.id = ?1 and m.channel = ?2";
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
            UUID conversationId, String afterMessageId, int limit, MessageChannel channel) {
        String baseQuery = "from MessageEntity m where m.conversation.id = ?1";
        List<Object> params = new ArrayList<>();
        params.add(conversationId);

        if (channel != null) {
            baseQuery += " and m.channel = ?" + (params.size() + 1);
            params.add(channel);
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
}
