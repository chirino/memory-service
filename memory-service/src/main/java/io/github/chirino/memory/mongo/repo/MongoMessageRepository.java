package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.model.MessageChannel;
import io.github.chirino.memory.mongo.model.MongoMessage;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import io.quarkus.panache.common.Sort;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import java.util.Optional;

@ApplicationScoped
public class MongoMessageRepository implements PanacheMongoRepositoryBase<MongoMessage, String> {

    public List<MongoMessage> listUserVisible(
            String conversationId, String afterMessageId, int limit) {
        Sort sort = Sort.by("createdAt").and("id");
        if (afterMessageId != null) {
            Optional<MongoMessage> afterOptional = findByIdOptional(afterMessageId);
            if (afterOptional.isPresent()) {
                MongoMessage after = afterOptional.get();
                if (conversationId.equals(after.conversationId)
                        && after.channel == MessageChannel.HISTORY
                        && after.createdAt != null) {
                    return find(
                                    "conversationId = ?1 and channel = ?2 and createdAt > ?3",
                                    sort,
                                    conversationId,
                                    MessageChannel.HISTORY,
                                    after.createdAt)
                            .page(0, limit)
                            .list();
                }
            }
        }
        return find(
                        "conversationId = ?1 and channel = ?2",
                        sort,
                        conversationId,
                        MessageChannel.HISTORY)
                .page(0, limit)
                .list();
    }

    public List<MongoMessage> listByChannel(
            String conversationId,
            String afterMessageId,
            int limit,
            MessageChannel channel,
            String clientId) {
        Sort sort = Sort.by("createdAt").and("id");
        if (afterMessageId != null) {
            Optional<MongoMessage> afterOptional = findByIdOptional(afterMessageId);
            if (afterOptional.isPresent()) {
                MongoMessage after = afterOptional.get();
                if (conversationId.equals(after.conversationId) && after.createdAt != null) {
                    if (channel != null && after.channel != channel) {
                        // If the cursor is from a different channel, ignore it and fall through
                    } else {
                        if (channel != null) {
                            if (channel == MessageChannel.MEMORY) {
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
            if (channel == MessageChannel.MEMORY) {
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

    public Long findLatestMemoryEpoch(String conversationId, String clientId) {
        Sort sort = Sort.by("epoch").descending();
        MongoMessage latest =
                find(
                                "conversationId = ?1 and channel = ?2 and clientId = ?3 and"
                                        + " epoch != null",
                                sort,
                                conversationId,
                                MessageChannel.MEMORY,
                                clientId)
                        .page(0, 1)
                        .firstResult();
        return latest != null ? latest.epoch : null;
    }

    public List<MongoMessage> listMemoryMessagesByEpoch(
            String conversationId, Long epoch, String clientId) {
        return listMemoryMessagesByEpoch(conversationId, null, Integer.MAX_VALUE, epoch, clientId);
    }

    public List<MongoMessage> listMemoryMessagesByEpoch(
            String conversationId, String afterMessageId, int limit, Long epoch, String clientId) {
        Sort sort = Sort.by("createdAt").and("id");
        List<Object> params = new ArrayList<>();
        params.add(conversationId);
        params.add(MessageChannel.MEMORY);
        params.add(clientId);
        String query = "conversationId = ?1 and channel = ?2 and clientId = ?3";
        if (epoch == null) {
            query += " and epoch = null";
        } else {
            query += " and epoch = ?" + (params.size() + 1);
            params.add(epoch);
        }
        if (afterMessageId != null) {
            Optional<MongoMessage> afterOptional = findByIdOptional(afterMessageId);
            if (afterOptional.isPresent()) {
                MongoMessage after = afterOptional.get();
                if (conversationId.equals(after.conversationId)
                        && after.channel == MessageChannel.MEMORY
                        && after.createdAt != null
                        && Objects.equals(after.epoch, epoch)) {
                    params.add(after.createdAt);
                    query += " and createdAt > ?" + params.size();
                }
            }
        }
        return find(query, sort, params.toArray()).page(0, limit).list();
    }
}
