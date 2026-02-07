package example;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
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
import jakarta.ws.rs.client.Client;
import jakarta.ws.rs.client.ClientBuilder;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@Path("/v1/conversations")
@ApplicationScoped
public class AgentSseResource {

    private final HistoryRecordingAgent agent;

    @Inject ResponseResumer resumer;
    @Inject SecurityIdentity securityIdentity;
    @Inject ObjectMapper objectMapper;

    @ConfigProperty(name = "memory-service.client.url")
    Optional<String> clientUrl;

    @ConfigProperty(name = "quarkus.rest-client.memory-service-client.url")
    Optional<String> quarkusRestClientUrl;

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

        // If there's an image attachment, use chatWithImage so the LLM can see it
        String imageUrl = resolveFirstImageUrl(request.getAttachments());
        Multi<ChatEvent> events;
        if (imageUrl != null) {
            // Build attachment metadata for history recording (references, not data URIs)
            List<Map<String, Object>> attachmentMeta =
                    buildAttachmentMetadata(request.getAttachments());
            events =
                    agent.chatWithImage(
                            conversationId, request.getMessage(), imageUrl, attachmentMeta);
        } else {
            events = agent.chatDetailed(conversationId, request.getMessage());
        }

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
     * Simple chat endpoint that returns plain text tokens. Kept for backward compatibility.
     */
    @POST
    @Path("/{conversationId}/chat-simple")
    @Blocking
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.SERVER_SENT_EVENTS)
    public Multi<TokenFrame> chatSimple(
            @PathParam("conversationId") String conversationId, MessageRequest request) {
        if (conversationId == null || conversationId.isBlank()) {
            throw new BadRequestException("Conversation ID is required");
        }
        if (request == null || request.getMessage() == null || request.getMessage().isBlank()) {
            throw new BadRequestException("Message is required");
        }

        Log.infof("Received simple SSE request for conversationId=%s", conversationId);

        return agent.chat(conversationId, request.getMessage())
                .map(TokenFrame::new)
                .onFailure()
                .invoke(
                        failure ->
                                Log.warnf(
                                        failure,
                                        "Chat failed for conversationId=%s",
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

    /**
     * Build attachment metadata maps for history recording. These contain references (attachmentId,
     * contentType, name) instead of the actual file data.
     */
    private List<Map<String, Object>> buildAttachmentMetadata(List<AttachmentRef> attachments) {
        if (attachments == null || attachments.isEmpty()) {
            return List.of();
        }
        List<Map<String, Object>> result = new ArrayList<>();
        for (AttachmentRef att : attachments) {
            if (att.getAttachmentId() == null || att.getAttachmentId().isBlank()) {
                continue;
            }
            Map<String, Object> meta = new LinkedHashMap<>();
            meta.put("attachmentId", att.getAttachmentId());
            if (att.getContentType() != null && !att.getContentType().isBlank()) {
                meta.put("contentType", att.getContentType());
            }
            if (att.getName() != null && !att.getName().isBlank()) {
                meta.put("name", att.getName());
            }
            result.add(meta);
        }
        return result;
    }

    /**
     * Resolves the first attachment to a data URI that can be sent to the LLM. Downloads the
     * attachment from the memory-service and base64-encodes it.
     */
    private String resolveFirstImageUrl(List<AttachmentRef> attachments) {
        if (attachments == null || attachments.isEmpty()) {
            return null;
        }
        for (AttachmentRef att : attachments) {
            if (att.getAttachmentId() == null || att.getAttachmentId().isBlank()) {
                continue;
            }
            try {
                String baseUrl =
                        clientUrl.orElseGet(
                                () -> quarkusRestClientUrl.orElse("http://localhost:8080"));
                String url = baseUrl + "/v1/attachments/" + att.getAttachmentId();
                String bearer = bearerToken(securityIdentity);
                Client client = ClientBuilder.newClient();
                try {
                    var req = client.target(url).request();
                    if (bearer != null) {
                        req = req.header("Authorization", "Bearer " + bearer);
                    }
                    Response response = req.get();
                    if (response.getStatus() == 302) {
                        // S3 redirect — use the signed URL directly (LLM can fetch it)
                        return response.getHeaderString("Location");
                    }
                    if (response.getStatus() == 200) {
                        String contentType = response.getHeaderString("Content-Type");
                        if (contentType == null) {
                            contentType = "application/octet-stream";
                        }
                        byte[] bytes = response.readEntity(InputStream.class).readAllBytes();
                        String base64 = Base64.getEncoder().encodeToString(bytes);
                        return "data:" + contentType + ";base64," + base64;
                    }
                } finally {
                    client.close();
                }
            } catch (Exception e) {
                Log.warnf(e, "Failed to resolve attachment %s", att.getAttachmentId());
            }
        }
        return null;
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

    /**
     * Resume a simple token stream. Kept for backward compatibility with /chat-simple endpoint.
     */
    @GET
    @Path("/{conversationId}/resume-simple")
    @Blocking
    @Produces(MediaType.SERVER_SENT_EVENTS)
    public Multi<TokenFrame> resumeSimple(@PathParam("conversationId") String conversationId) {
        if (conversationId == null || conversationId.isBlank()) {
            throw new BadRequestException("Conversation ID is required");
        }

        Log.infof("SSE resume-simple request for conversationId=%s", conversationId);

        String bearerToken = bearerToken(securityIdentity);
        return resumer.replay(conversationId, bearerToken)
                .map(TokenFrame::new)
                .onFailure()
                .invoke(
                        failure ->
                                Log.warnf(
                                        failure,
                                        "Resume simple failed for conversationId=%s",
                                        conversationId));
    }

    public static final class TokenFrame {

        private final String token;

        public TokenFrame(String token) {
            this.token = token;
        }

        public String getToken() {
            return token;
        }
    }

    public static final class MessageRequest {

        private String message;
        private java.util.List<AttachmentRef> attachments;

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

        public java.util.List<AttachmentRef> getAttachments() {
            return attachments;
        }

        public void setAttachments(java.util.List<AttachmentRef> attachments) {
            this.attachments = attachments;
        }
    }

    public static final class AttachmentRef {

        private String attachmentId;
        private String contentType;
        private String name;

        public AttachmentRef() {}

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
