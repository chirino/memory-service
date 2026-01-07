package io.github.chirino.memory.langchain4j;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.util.RawValue;
import dev.langchain4j.data.message.ChatMessage;
import dev.langchain4j.data.message.JacksonChatMessageJsonCodec;
import dev.langchain4j.memory.ChatMemory;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.client.model.ListConversationMessages200Response;
import io.github.chirino.memory.client.model.Message;
import io.github.chirino.memory.client.model.MessageChannel;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import jakarta.ws.rs.WebApplicationException;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import org.jboss.logging.Logger;

/**
 * ChatMemory implementation backed by the Memory Service HTTP API.
 *
 * This implementation uses the shared ConversationsApi REST client. The same append endpoint
 * is used for both user and agent calls; the server determines whether the caller is a user
 * or an agent based on authentication (for example, API keys).
 */
public class MemoryServiceChatMemory implements ChatMemory {

    private static final Logger LOG = Logger.getLogger(MemoryServiceChatMemory.class);
    private static final JacksonChatMessageJsonCodec CODEC = new JacksonChatMessageJsonCodec();
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private final ConversationsApi conversationsApi;
    private final String conversationId;
    private final RequestContextExecutor requestContextExecutor;
    private final SecurityIdentity securityIdentity;
    private final SecurityIdentityAssociation securityIdentityAssociation;

    public MemoryServiceChatMemory(
            ConversationsApi conversationsApi,
            String conversationId,
            RequestContextExecutor requestContextExecutor,
            SecurityIdentity securityIdentity,
            SecurityIdentityAssociation securityIdentityAssociation) {
        this.conversationsApi = Objects.requireNonNull(conversationsApi, "conversationsApi");
        this.conversationId = Objects.requireNonNull(conversationId, "conversationId");
        this.requestContextExecutor =
                Objects.requireNonNull(requestContextExecutor, "requestContextExecutor");
        this.securityIdentity = securityIdentity; // Can be null if not authenticated
        this.securityIdentityAssociation = securityIdentityAssociation; // Can be null
    }

    @Override
    public Object id() {
        return conversationId;
    }

    @Override
    public void add(ChatMessage chatMessage) {
        runWithRequestContext(
                () -> {
                    CreateMessageRequest request = new CreateMessageRequest();
                    if (securityIdentity != null && securityIdentity.getPrincipal() != null) {
                        request.setUserId(securityIdentity.getPrincipal().getName());
                    }
                    request.setChannel(CreateMessageRequest.ChannelEnum.MEMORY);

                    String json = CODEC.messageToJson(chatMessage);
                    LOG.infof("Encoding content block: [%s]", json);
                    request.setContent(List.of(new RawValue(json)));

                    conversationsApi.appendConversationMessage(conversationId, request);
                });
    }

    @Override
    public List<ChatMessage> messages() {
        return callWithRequestContext(
                () -> {
                    ListConversationMessages200Response context;
                    try {
                        context =
                                conversationsApi.listConversationMessages(
                                        conversationId, null, 50, MessageChannel.MEMORY);
                    } catch (WebApplicationException e) {
                        int status = e.getResponse() != null ? e.getResponse().getStatus() : -1;
                        if (status == 404) {
                            LOG.infof(
                                    "Treating status %d for conversationId=%s as empty memory",
                                    status, conversationId);
                            return new ArrayList<>();
                        }
                        throw e;
                    }

                    List<ChatMessage> result = new ArrayList<>();
                    if (context == null || context.getData() == null) {
                        return result;
                    }

                    for (Message message : context.getData()) {
                        if (message.getContent() == null) {
                            continue;
                        }

                        LOG.infof("Message was on channel: %s", message.getChannel());

                        for (Object block : message.getContent()) {
                            if (block == null) {
                                continue;
                            }
                            var decoded = decodeContentBlock(block, message.getId());
                            result.addAll(decoded);
                        }
                    }

                    return result;
                });
    }

    @Override
    public void clear() {
        // Not yet implemented against the Memory Service API.
    }

    private void runWithRequestContext(Runnable action) {
        requestContextExecutor.run(
                () -> {
                    propagateIdentity();
                    try {
                        action.run();
                    } finally {
                        clearIdentity();
                    }
                });
    }

    private <T> T callWithRequestContext(java.util.function.Supplier<T> supplier) {
        return requestContextExecutor.call(
                () -> {
                    propagateIdentity();
                    try {
                        return supplier.get();
                    } finally {
                        clearIdentity();
                    }
                });
    }

    private void propagateIdentity() {
        if (securityIdentity != null && securityIdentityAssociation != null) {
            securityIdentityAssociation.setIdentity(securityIdentity);
        }
    }

    private void clearIdentity() {
        // Do not clear identity here; the association is managed by Quarkus.
    }

    /**
     * Decode a single content block coming back from the memory-service into a LangChain4j
     * ChatMessage.
     */
    private List<ChatMessage> decodeContentBlock(Object block, String messageId) {
        try {
            String json = OBJECT_MAPPER.writeValueAsString(List.of(block));
            // LOG.infof("Decoding content block: %s", json);
            return CODEC.messagesFromJson(json);
        } catch (Exception e) {
            LOG.warnf(
                    e,
                    "Failed to decode content block for conversationId=%s, messageId=%s",
                    conversationId,
                    messageId);
        }
        return null;
    }
}
