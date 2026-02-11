package example;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.history.runtime.AttachmentRef;
import io.github.chirino.memory.history.runtime.AttachmentResolver;
import io.github.chirino.memory.history.runtime.Attachments;
import io.github.chirino.memory.history.runtime.ResponseResumer;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.logging.Log;
import io.quarkus.security.identity.SecurityIdentity;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.BadRequestException;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.List;

@Path("/v1/conversations")
@ApplicationScoped
public class AgentSseResource {

    private final HistoryRecordingAgent agent;

    @Inject ResponseResumer resumer;
    @Inject SecurityIdentity securityIdentity;
    @Inject ObjectMapper objectMapper;
    @Inject AttachmentResolver attachmentResolver;

    public AgentSseResource(HistoryRecordingAgent agent) {
        this.agent = agent;
    }

    /**
     * Chat endpoint that returns rich event stream (ChatEvent JSON objects). This enables real-time
     * rendering of tool calls, thinking, and other events in the UI.
     */
    @POST
    @Path("/{conversationId}/chat")
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

        Multi<ChatEvent> events = agent.chat(conversationId, request.getMessage(), attachments);

        return events.map(this::encode)
                .onFailure()
                .invoke(
                        failure ->
                                Log.warnf(
                                        failure,
                                        "Chat failed for conversationId=%s",
                                        conversationId));
    }

    /**
     * Resume a rich event stream. Returns complete JSON lines from the recorded event stream. This
     * is the default resume endpoint that matches the rich event /chat endpoint.
     */
    @GET
    @Path("/{conversationId}/resume")
    @Blocking
    @Produces(MediaType.SERVER_SENT_EVENTS)
    public Multi<String> resume(@PathParam("conversationId") String conversationId) {
        if (conversationId == null || conversationId.isBlank()) {
            throw new BadRequestException("Conversation ID is required");
        }

        Log.infof("SSE resume request for conversationId=%s", conversationId);

        String bearerToken = bearerToken(securityIdentity);
        // Return raw JSON lines (efficient - no decode/re-encode)
        return resumer.replayEvents(conversationId, bearerToken, String.class)
                .onFailure()
                .invoke(
                        failure ->
                                Log.warnf(
                                        failure,
                                        "Resume failed for conversationId=%s",
                                        conversationId));
    }

    private String encode(ChatEvent chatEvent) {
        try {
            return objectMapper.writeValueAsString(chatEvent);
        } catch (JsonProcessingException e) {
            // Some ChatEvent subtypes (e.g. ChatCompletedEvent) contain non-serializable fields.
            // Fall back to a minimal JSON representation.
            Log.debugf(
                    e,
                    "Failed to serialize ChatEvent of type %s, using fallback",
                    chatEvent.getClass().getSimpleName());
            return "{\"eventType\":\""
                    + chatEvent.getClass().getSimpleName().replace("Event", "")
                    + "\"}";
        }
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
