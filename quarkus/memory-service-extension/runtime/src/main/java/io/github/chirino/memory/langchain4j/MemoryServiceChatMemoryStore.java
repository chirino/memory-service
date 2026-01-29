package io.github.chirino.memory.langchain4j;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.util.RawValue;
import dev.langchain4j.data.message.ChatMessage;
import dev.langchain4j.data.message.JacksonChatMessageJsonCodec;
import dev.langchain4j.store.memory.chat.ChatMemoryStore;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.Channel;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest.ChannelEnum;
import io.github.chirino.memory.client.model.Entry;
import io.github.chirino.memory.client.model.ListConversationEntries200Response;
import io.github.chirino.memory.client.model.SyncEntriesRequest;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import jakarta.ws.rs.WebApplicationException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Objects;
import java.util.UUID;
import java.util.stream.Collectors;
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

    private final MemoryServiceApiBuilder conversationsApiBuilder;
    private final Instance<SecurityIdentity> securityIdentityInstance;

    @Inject
    public MemoryServiceChatMemoryStore(
            MemoryServiceApiBuilder conversationsApiBuilder,
            Instance<SecurityIdentity> securityIdentityInstance) {
        this.conversationsApiBuilder =
                Objects.requireNonNull(conversationsApiBuilder, "conversationsApiBuilder");
        this.securityIdentityInstance =
                Objects.requireNonNull(securityIdentityInstance, "securityIdentityInstance");
    }

    @Override
    public List<ChatMessage> getMessages(Object memoryId) {
        Objects.requireNonNull(memoryId, "memoryId");
        ListConversationEntries200Response context;
        try {
            context =
                    conversationsApi()
                            .listConversationEntries(
                                    UUID.fromString(memoryId.toString()),
                                    null,
                                    50,
                                    Channel.MEMORY,
                                    null);
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

        for (Entry entry : context.getData()) {
            if (entry.getContent() == null) {
                continue;
            }
            for (Object block : entry.getContent()) {
                if (block == null) {
                    continue;
                }
                String entryIdStr = entry.getId() != null ? entry.getId().toString() : null;
                List<ChatMessage> decoded =
                        decodeContentBlock(block, memoryId.toString(), entryIdStr);
                if (decoded != null && !decoded.isEmpty()) {
                    result.addAll(decoded);
                }
            }
        }

        LOG.infof(
                "getMessages(%s)=>\n%s\nat: %s",
                memoryId,
                result.stream().map(ChatMessage::toString).collect(Collectors.joining("\n")),
                stackTrace());

        return result;
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

        SyncEntriesRequest syncRequest = new SyncEntriesRequest();
        List<CreateEntryRequest> syncEntries = new ArrayList<>();
        for (ChatMessage chatMessage : messages) {
            if (chatMessage == null) {
                continue;
            }
            syncEntries.add(toCreateEntryRequest(chatMessage));
        }
        if (syncEntries.isEmpty()) {
            LOG.infof("Skipping sync for empty memory update on conversationId=%s", memoryId);
            return;
        }
        syncRequest.setEntries(syncEntries);
        conversationsApi()
                .syncConversationMemory(UUID.fromString(memoryId.toString()), syncRequest);
    }

    @Override
    public void deleteMessages(Object memoryId) {
        Objects.requireNonNull(memoryId, "memoryId");
        LOG.infof(
                "Memory service does not support empty sync requests; delete is a no-op for"
                        + " conversationId=%s\nat: %s",
                memoryId, stackTrace());
    }

    private CreateEntryRequest toCreateEntryRequest(ChatMessage chatMessage) {
        CreateEntryRequest request = new CreateEntryRequest();
        SecurityIdentity identity = resolveSecurityIdentity();
        String userId = principalName(identity);
        if (userId != null) {
            request.setUserId(userId);
        }
        request.setChannel(ChannelEnum.MEMORY);
        request.setContentType("LC4J");

        String json = CODEC.messageToJson(chatMessage);
        // LOG.infof("Encoding content block: [%s]", json);
        request.setContent(List.of(new RawValue(json)));
        return request;
    }

    private ConversationsApi conversationsApi() {
        String bearerToken = bearerToken(resolveSecurityIdentity());
        return conversationsApiBuilder.withBearerAuth(bearerToken).build(ConversationsApi.class);
    }

    private SecurityIdentity resolveSecurityIdentity() {
        return securityIdentityInstance.isResolvable() ? securityIdentityInstance.get() : null;
    }

    private List<ChatMessage> decodeContentBlock(
            Object block, String conversationId, String entryId) {
        try {
            String json = OBJECT_MAPPER.writeValueAsString(List.of(block));
            return CODEC.messagesFromJson(json);
        } catch (Exception e) {
            LOG.warnf(
                    e,
                    "Failed to decode content block for conversationId=%s, entryId=%s",
                    conversationId,
                    entryId);
        }
        return null;
    }
}
