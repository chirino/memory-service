package io.github.chirino.memory.langchain4j;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.util.RawValue;
import dev.langchain4j.data.message.ChatMessage;
import dev.langchain4j.data.message.JacksonChatMessageJsonCodec;
import dev.langchain4j.store.memory.chat.ChatMemoryStore;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.client.model.ListConversationMessages200Response;
import io.github.chirino.memory.client.model.Message;
import io.github.chirino.memory.client.model.MessageChannel;
import io.github.chirino.memory.client.model.SyncMessagesRequest;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import jakarta.ws.rs.WebApplicationException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Objects;
import java.util.stream.Collectors;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

/**
 * ChatMemoryStore implementation backed by the Memory Service HTTP API.
 *
 * This implementation stores messages using the memory channel and uses the
 * sync API to replace the persisted memory epoch when updates are required.
 */
@Singleton
public class MemoryServiceChatMemoryStore implements ChatMemoryStore {

    private static final Logger LOG = Logger.getLogger(MemoryServiceChatMemoryStore.class);
    private static final JacksonChatMessageJsonCodec CODEC = new JacksonChatMessageJsonCodec();
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private final ConversationsApi conversationsApi;
    private final RequestContextExecutor requestContextExecutor;
    private final Instance<SecurityIdentity> securityIdentityInstance;
    private final Instance<SecurityIdentityAssociation> securityIdentityAssociationInstance;

    @Inject
    public MemoryServiceChatMemoryStore(
            @RestClient ConversationsApi conversationsApi,
            RequestContextExecutor requestContextExecutor,
            Instance<SecurityIdentity> securityIdentityInstance,
            Instance<SecurityIdentityAssociation> securityIdentityAssociationInstance) {
        this.conversationsApi = Objects.requireNonNull(conversationsApi, "conversationsApi");
        this.requestContextExecutor =
                Objects.requireNonNull(requestContextExecutor, "requestContextExecutor");
        this.securityIdentityInstance =
                Objects.requireNonNull(securityIdentityInstance, "securityIdentityInstance");
        this.securityIdentityAssociationInstance =
                Objects.requireNonNull(
                        securityIdentityAssociationInstance, "securityIdentityAssociationInstance");
    }

    @Override
    public List<ChatMessage> getMessages(Object memoryId) {
        Objects.requireNonNull(memoryId, "memoryId");
        return callWithRequestContext(
                () -> {
                    ListConversationMessages200Response context;
                    try {
                        context =
                                conversationsApi.listConversationMessages(
                                        memoryId.toString(), null, 50, MessageChannel.MEMORY, null);
                    } catch (WebApplicationException e) {
                        int status = e.getResponse() != null ? e.getResponse().getStatus() : -1;
                        if (status == 404) {
                            LOG.infof(
                                    "Treating status %d for conversationId=%s as empty memory",
                                    status, memoryId);
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
                        for (Object block : message.getContent()) {
                            if (block == null) {
                                continue;
                            }
                            List<ChatMessage> decoded =
                                    decodeContentBlock(block, memoryId.toString(), message.getId());
                            if (decoded != null && !decoded.isEmpty()) {
                                result.addAll(decoded);
                            }
                        }
                    }

                    LOG.infof(
                            "getMessages(%s)=>\n%s\nat: %s",
                            memoryId,
                            result.stream()
                                    .map(ChatMessage::toString)
                                    .collect(Collectors.joining("\n")),
                            stackTrace());

                    return result;
                });
    }

    private String stackTrace() {
        return Arrays.stream(new Exception().getStackTrace())
                .map(StackTraceElement::toString)
                .collect(Collectors.joining("\n"));
    }

    @Override
    public void updateMessages(Object memoryId, List<ChatMessage> messages) {
        Objects.requireNonNull(memoryId, "memoryId");
        if (messages == null || messages.isEmpty()) {
            LOG.infof("Skipping sync for empty memory update on conversationId=%s", memoryId);
            return;
        }

        LOG.infof(
                "updateMessages(%s)=>\n%s\nat: %s",
                memoryId,
                messages.stream().map(ChatMessage::toString).collect(Collectors.joining("\n")),
                stackTrace());

        runWithRequestContext(
                () -> {
                    SyncMessagesRequest syncRequest = new SyncMessagesRequest();
                    List<CreateMessageRequest> syncMessages = new ArrayList<>();
                    for (ChatMessage chatMessage : messages) {
                        if (chatMessage == null) {
                            continue;
                        }
                        syncMessages.add(toCreateMessageRequest(chatMessage));
                    }
                    if (syncMessages.isEmpty()) {
                        LOG.infof(
                                "Skipping sync for empty memory update on conversationId=%s",
                                memoryId);
                        return;
                    }
                    syncRequest.setMessages(syncMessages);
                    conversationsApi.syncConversationMemory(memoryId.toString(), syncRequest);
                });
    }

    @Override
    public void deleteMessages(Object memoryId) {
        Objects.requireNonNull(memoryId, "memoryId");
        LOG.infof(
                "Memory service does not support empty sync requests; delete is a no-op for"
                        + " conversationId=%s\nat: %s",
                memoryId, stackTrace());
    }

    private CreateMessageRequest toCreateMessageRequest(ChatMessage chatMessage) {
        CreateMessageRequest request = new CreateMessageRequest();
        SecurityIdentity identity = resolveSecurityIdentity();
        if (identity != null && identity.getPrincipal() != null) {
            request.setUserId(identity.getPrincipal().getName());
        }
        request.setChannel(CreateMessageRequest.ChannelEnum.MEMORY);

        String json = CODEC.messageToJson(chatMessage);
        // LOG.infof("Encoding content block: [%s]", json);
        request.setContent(List.of(new RawValue(json)));
        return request;
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
        SecurityIdentity identity = resolveSecurityIdentity();
        SecurityIdentityAssociation association = resolveSecurityIdentityAssociation();
        if (identity != null && association != null) {
            association.setIdentity(identity);
        }
    }

    private void clearIdentity() {
        // Do not clear identity here; the association is managed by Quarkus.
    }

    private SecurityIdentity resolveSecurityIdentity() {
        return securityIdentityInstance.isResolvable() ? securityIdentityInstance.get() : null;
    }

    private SecurityIdentityAssociation resolveSecurityIdentityAssociation() {
        return securityIdentityAssociationInstance.isResolvable()
                ? securityIdentityAssociationInstance.get()
                : null;
    }

    private List<ChatMessage> decodeContentBlock(
            Object block, String conversationId, String messageId) {
        try {
            String json = OBJECT_MAPPER.writeValueAsString(List.of(block));
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
