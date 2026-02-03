package io.github.chirino.memory.langchain4j;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.util.RawValue;
import dev.langchain4j.data.message.ChatMessage;
import dev.langchain4j.data.message.JacksonChatMessageJsonCodec;
import dev.langchain4j.memory.ChatMemory;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.Channel;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest.ChannelEnum;
import io.github.chirino.memory.client.model.Entry;
import io.github.chirino.memory.client.model.ListConversationEntries200Response;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.ws.rs.WebApplicationException;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import java.util.UUID;
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

    private final MemoryServiceApiBuilder conversationsApiBuilder;
    private final String conversationId;
    private final SecurityIdentity securityIdentity;

    public MemoryServiceChatMemory(
            MemoryServiceApiBuilder conversationsApiBuilder,
            String conversationId,
            SecurityIdentity securityIdentity) {
        this.conversationsApiBuilder =
                Objects.requireNonNull(conversationsApiBuilder, "conversationsApiBuilder");
        this.conversationId = Objects.requireNonNull(conversationId, "conversationId");
        this.securityIdentity = securityIdentity; // Can be null if not authenticated
    }

    @Override
    public Object id() {
        return conversationId;
    }

    @Override
    public void add(ChatMessage chatMessage) {
        CreateEntryRequest request = new CreateEntryRequest();
        String userId = principalName(securityIdentity);
        if (userId != null) {
            request.setUserId(userId);
        }
        request.setChannel(ChannelEnum.MEMORY);
        request.setContentType("LC4J");

        String json = CODEC.messageToJson(chatMessage);
        // LOG.infof("Encoding content block: [%s]", json);
        request.setContent(List.of(new RawValue(json)));

        conversationsApi().appendConversationEntry(UUID.fromString(conversationId), request);
    }

    @Override
    public List<ChatMessage> messages() {
        LOG.infof("messages(%s)", conversationId);

        ListConversationEntries200Response context;
        try {
            context =
                    conversationsApi()
                            .listConversationEntries(
                                    UUID.fromString(conversationId),
                                    null,
                                    50,
                                    Channel.MEMORY,
                                    null,
                                    null);
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

        for (Entry entry : context.getData()) {
            if (entry.getContent() == null) {
                continue;
            }

            for (Object block : entry.getContent()) {
                if (block == null) {
                    continue;
                }
                String entryIdStr = entry.getId() != null ? entry.getId().toString() : null;
                var decoded = decodeContentBlock(block, entryIdStr);
                result.addAll(decoded);
            }
        }
        return result;
    }

    @Override
    public void clear() {
        // Not yet implemented against the Memory Service API.
    }

    private ConversationsApi conversationsApi() {
        String bearerToken = bearerToken(securityIdentity);
        return conversationsApiBuilder.withBearerAuth(bearerToken).build(ConversationsApi.class);
    }

    /**
     * Decode a single content block coming back from the memory-service into a LangChain4j
     * ChatMessage.
     */
    private List<ChatMessage> decodeContentBlock(Object block, String entryId) {
        try {
            String json = OBJECT_MAPPER.writeValueAsString(List.of(block));
            // LOG.infof("Decoding content block: %s", json);
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
