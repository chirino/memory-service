package io.github.chirino.memoryservice.memory;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memoryservice.client.api.ConversationsApi;
import io.github.chirino.memoryservice.client.model.CreateMessageRequest;
import io.github.chirino.memoryservice.client.model.ListConversationMessages200Response;
import io.github.chirino.memoryservice.client.model.MessageChannel;
import io.github.chirino.memoryservice.client.model.SyncMessagesRequest;
import io.github.chirino.memoryservice.history.ConversationsApiFactory;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
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

        ListConversationMessages200Response response;
        try {
            response =
                    conversationsApi()
                            .listConversationMessages(
                                    conversationId, null, 1000, MessageChannel.MEMORY, null)
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
        for (io.github.chirino.memoryservice.client.model.Message msg : response.getData()) {
            if (msg.getContent() == null) {
                continue;
            }
            for (Object contentBlock : msg.getContent()) {
                Message decoded = decodeContentBlock(contentBlock, conversationId, msg.getId());
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

        SyncMessagesRequest syncRequest = new SyncMessagesRequest();
        List<CreateMessageRequest> syncMessages = new ArrayList<>();

        for (Message message : messages) {
            if (message == null) {
                continue;
            }
            CreateMessageRequest request = toCreateMessageRequest(message);
            if (request != null) {
                syncMessages.add(request);
            }
        }

        if (syncMessages.isEmpty()) {
            LOG.debug("No valid messages to sync for conversationId={}", conversationId);
            return;
        }

        syncRequest.setMessages(syncMessages);

        try {
            conversationsApi().syncConversationMemory(conversationId, syncRequest).block();
            LOG.debug(
                    "Successfully synced {} messages for conversationId={}",
                    syncMessages.size(),
                    conversationId);
        } catch (Exception e) {
            LOG.warn("Failed to sync messages for conversationId={}", conversationId, e);
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

    private CreateMessageRequest toCreateMessageRequest(Message message) {
        CreateMessageRequest request = new CreateMessageRequest();
        request.setChannel(MessageChannel.MEMORY);

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
    private Message decodeContentBlock(Object block, String conversationId, String messageId) {
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
                        "Content block missing role or text for conversationId={}, messageId={}",
                        conversationId,
                        messageId);
                return null;
            }

            return createMessage(role, text);
        } catch (Exception e) {
            LOG.warn(
                    "Failed to decode content block for conversationId={}, messageId={}",
                    conversationId,
                    messageId,
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
