package org.acme;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.history.runtime.AttachmentRef;
import io.github.chirino.memory.history.runtime.AttachmentResolver;
import io.github.chirino.memory.history.runtime.Attachments;
import io.github.chirino.memory.history.runtime.ConversationEventStreamAdapter;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.logging.Log;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.BadRequestException;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.List;

@Path("/chat")
@ApplicationScoped
public class ChatResource {

    private final HistoryRecordingAgent agent;

    @Inject ObjectMapper objectMapper;
    @Inject AttachmentResolver attachmentResolver;

    public ChatResource(HistoryRecordingAgent agent) {
        this.agent = agent;
    }

    /**
     * Chat endpoint that returns rich event stream (ChatEvent JSON objects). This enables real-time
     * rendering of tool calls, thinking, and other events in the UI.
     */
    @POST
    @Path("/{conversationId}")
    @Blocking
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.SERVER_SENT_EVENTS)
    public Multi<String> stream(
            @PathParam("conversationId") String conversationId, MessageRequest request) {
        if (conversationId == null || conversationId.isBlank()) {
            throw new BadRequestException("Conversation ID is required");
        }
        if (request == null || request.getMessage() == null || request.getMessage().isBlank()) {
            throw new BadRequestException("Message is required");
        }

        Log.infof("Received SSE request for conversationId=%s", conversationId);

        Attachments attachments = attachmentResolver.resolve(toRefs(request.getAttachments()));

        Multi<ChatEvent> events =
                agent.chat(
                        conversationId,
                        request.getMessage(),
                        attachments,
                        request.getForkedAtConversationId(),
                        request.getForkedAtEntryId());

        return events.map(this::encode)
                .onFailure()
                .invoke(
                        failure ->
                                Log.warnf(
                                        failure,
                                        "Chat failed for conversationId=%s",
                                        conversationId));
    }

    private String encode(ChatEvent chatEvent) {
        return ConversationEventStreamAdapter.buildEventJson(chatEvent, objectMapper);
    }

    private static List<AttachmentRef> toRefs(List<RequestAttachmentRef> attachments) {
        if (attachments == null || attachments.isEmpty()) {
            return List.of();
        }
        return attachments.stream()
                .map(a -> new AttachmentRef(a.getAttachmentId(), a.getContentType(), a.getName()))
                .toList();
    }

    public static final class MessageRequest {

        private String message;
        private List<RequestAttachmentRef> attachments;
        private String forkedAtConversationId;
        private String forkedAtEntryId;

        public MessageRequest() {}

        public MessageRequest(String message) {
            this.message = message;
        }

        public String getMessage() {
            return message;
        }

        public void setMessage(String message) {
            this.message = message;
        }

        public List<RequestAttachmentRef> getAttachments() {
            return attachments;
        }

        public void setAttachments(List<RequestAttachmentRef> attachments) {
            this.attachments = attachments;
        }

        public String getForkedAtConversationId() {
            return forkedAtConversationId;
        }

        public void setForkedAtConversationId(String forkedAtConversationId) {
            this.forkedAtConversationId = forkedAtConversationId;
        }

        public String getForkedAtEntryId() {
            return forkedAtEntryId;
        }

        public void setForkedAtEntryId(String forkedAtEntryId) {
            this.forkedAtEntryId = forkedAtEntryId;
        }
    }

    public static final class RequestAttachmentRef {

        private String attachmentId;
        private String contentType;
        private String name;

        public RequestAttachmentRef() {}

        public String getAttachmentId() {
            return attachmentId;
        }

        public void setAttachmentId(String attachmentId) {
            this.attachmentId = attachmentId;
        }

        public String getContentType() {
            return contentType;
        }

        public void setContentType(String contentType) {
            this.contentType = contentType;
        }

        public String getName() {
            return name;
        }

        public void setName(String name) {
            this.name = name;
        }
    }
}
