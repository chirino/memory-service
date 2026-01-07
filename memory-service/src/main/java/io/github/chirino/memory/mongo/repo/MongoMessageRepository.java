package io.github.chirino.memory.mongo.repo;

import io.github.chirino.memory.model.MessageChannel;
import io.github.chirino.memory.mongo.model.MongoMessage;
import io.quarkus.mongodb.panache.PanacheMongoRepositoryBase;
import io.quarkus.panache.common.Sort;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.List;
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
            String conversationId, String afterMessageId, int limit, MessageChannel channel) {
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
            return find("conversationId = ?1 and channel = ?2", sort, conversationId, channel)
                    .page(0, limit)
                    .list();
        }
        return find("conversationId = ?1", sort, conversationId).page(0, limit).list();
    }
}
