package io.github.chirino.memoryservice.memory;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.model.Channel;
import io.github.chirino.memoryservice.client.model.CreateEntryRequest;
import io.github.chirino.memoryservice.client.model.Entry;
import io.github.chirino.memoryservice.client.model.ListConversationEntries200Response;
import io.github.chirino.memoryservice.client.model.SyncEntriesRequest;
import io.github.chirino.memoryservice.history.ConversationsApiFactory;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.UUID;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.chat.memory.ChatMemoryRepository;
import org.springframework.ai.chat.messages.AssistantMessage;
import org.springframework.ai.chat.messages.Message;
import org.springframework.ai.chat.messages.MessageType;
import org.springframework.ai.chat.messages.SystemMessage;
import org.springframework.ai.chat.messages.UserMessage;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.web.reactive.function.client.WebClientResponseException;

/**
 * ChatMemoryRepository implementation backed by the Memory Service HTTP API.
 *
 * <p>This implementation stores Spring AI messages in the memory-service using the MEMORY channel.
 * Messages are serialized to JSON with role and text fields for storage and deserialized back
 * to Spring AI Message objects on retrieval.
 */
public class MemoryServiceChatMemoryRepository implements ChatMemoryRepository {

    private static final Logger LOG =
            LoggerFactory.getLogger(MemoryServiceChatMemoryRepository.class);
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private final ConversationsApiFactory apiFactory;
    private final String bearerToken;

    public MemoryServiceChatMemoryRepository(
            ConversationsApiFactory apiFactory,
            @Nullable OAuth2AuthorizedClientService authorizedClientService,
            String bearerToken) {
        this.apiFactory = Objects.requireNonNull(apiFactory, "apiFactory");
        this.bearerToken = bearerToken;
    }

    @Override
    public List<String> findConversationIds() {
        // The memory-service API doesn't have a direct way to list all conversation IDs
        // that have memory messages. This method is typically used for administrative purposes.
        // Return empty list as this is not directly supported.
        LOG.debug(
                "findConversationIds called - returning empty list (not supported by"
                        + " memory-service)");
        return Collections.emptyList();
    }

    @Override
    public List<Message> findByConversationId(String conversationId) {
        Objects.requireNonNull(conversationId, "conversationId");
        LOG.debug("findByConversationId called for conversationId={}", conversationId);

        ListConversationEntries200Response response;
        try {
            response =
                    conversationsApi()
                            .listConversationEntries(
                                    UUID.fromString(conversationId),
                                    null,
                                    1000,
                                    Channel.MEMORY,
                                    null)
                            .block();
        } catch (WebClientResponseException e) {
            int status = e.getStatusCode().value();
            if (status == 404) {
                LOG.debug(
                        "Conversation {} not found (404), returning empty message list",
                        conversationId);
                return new ArrayList<>();
            }
            throw e;
        }

        if (response == null || response.getData() == null) {
            return new ArrayList<>();
        }

        List<Message> result = new ArrayList<>();
        for (Entry entry : response.getData()) {
            if (entry.getContent() == null) {
                continue;
            }
            for (Object contentBlock : entry.getContent()) {
                String entryId = entry.getId() != null ? entry.getId().toString() : null;
                Message decoded = decodeContentBlock(contentBlock, conversationId, entryId);
                if (decoded != null) {
                    result.add(decoded);
                }
            }
        }

        LOG.debug("findByConversationId({}) returned {} messages", conversationId, result.size());
        return result;
    }

    @Override
    public void saveAll(String conversationId, List<Message> messages) {
        Objects.requireNonNull(conversationId, "conversationId");
        LOG.debug(
                "saveAll called for conversationId={} with {} messages",
                conversationId,
                messages == null ? 0 : messages.size());

        if (messages == null || messages.isEmpty()) {
            LOG.debug("No messages to save for conversationId={}", conversationId);
            return;
        }

        SyncEntriesRequest syncRequest = new SyncEntriesRequest();
        List<CreateEntryRequest> syncEntries = new ArrayList<>();

        for (Message message : messages) {
            if (message == null) {
                continue;
            }
            CreateEntryRequest request = toCreateEntryRequest(message);
            if (request != null) {
                syncEntries.add(request);
            }
        }

        if (syncEntries.isEmpty()) {
            LOG.debug("No valid entries to sync for conversationId={}", conversationId);
            return;
        }

        syncRequest.setEntries(syncEntries);

        try {
            conversationsApi()
                    .syncConversationMemory(UUID.fromString(conversationId), syncRequest)
                    .block();
            LOG.debug(
                    "Successfully synced {} entries for conversationId={}",
                    syncEntries.size(),
                    conversationId);
        } catch (Exception e) {
            LOG.warn("Failed to sync entries for conversationId={}", conversationId, e);
            throw e;
        }
    }

    @Override
    public void deleteByConversationId(String conversationId) {
        Objects.requireNonNull(conversationId, "conversationId");
        LOG.debug("deleteByConversationId called for conversationId={}", conversationId);
        // The memory-service doesn't support deleting just the memory channel messages.
        // Sync with an empty list is not supported either.
        // For now, this is a no-op with a warning.
        LOG.warn(
                "deleteByConversationId is not fully supported by memory-service; "
                        + "memory for conversationId={} will not be cleared",
                conversationId);
    }

    private ConversationsApi conversationsApi() {
        return apiFactory.create(bearerToken);
    }

    private CreateEntryRequest toCreateEntryRequest(Message message) {
        CreateEntryRequest request = new CreateEntryRequest();
        request.setChannel(Channel.MEMORY);
        request.setContentType("SpringAI");

        String userId = SecurityHelper.principalName();
        if (userId != null) {
            request.setUserId(userId);
        }

        Map<String, Object> contentBlock = new HashMap<>();
        contentBlock.put("role", message.getMessageType().getValue());
        contentBlock.put("text", message.getText());
        request.setContent(List.of(contentBlock));
        return request;
    }

    @Nullable
    private Message decodeContentBlock(Object block, String conversationId, String entryId) {
        if (block == null) {
            return null;
        }

        try {
            // Convert the block to a map for easier access
            @SuppressWarnings("unchecked")
            Map<String, Object> contentMap =
                    block instanceof Map
                            ? (Map<String, Object>) block
                            : OBJECT_MAPPER.convertValue(block, Map.class);

            String role = (String) contentMap.get("role");
            String text = (String) contentMap.get("text");

            if (role == null || text == null) {
                LOG.debug(
                        "Content block missing role or text for conversationId={}, entryId={}",
                        conversationId,
                        entryId);
                return null;
            }

            return createMessage(role, text);
        } catch (Exception e) {
            LOG.warn(
                    "Failed to decode content block for conversationId={}, entryId={}",
                    conversationId,
                    entryId,
                    e);
            return null;
        }
    }

    @Nullable
    private Message createMessage(String role, String text) {
        MessageType messageType;
        try {
            messageType = MessageType.fromValue(role);
        } catch (IllegalArgumentException e) {
            LOG.warn("Unknown message role: {}", role);
            return null;
        }

        return switch (messageType) {
            case USER -> new UserMessage(text);
            case ASSISTANT -> new AssistantMessage(text);
            case SYSTEM -> new SystemMessage(text);
            case TOOL -> {
                // Tool messages require more context that we don't have
                LOG.debug("Skipping TOOL message during retrieval");
                yield null;
            }
        };
    }
}
