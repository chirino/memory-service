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
import jakarta.ws.rs.core.MediaType;

@Path("/v1/conversations")
@ApplicationScoped
public class AgentSseResource {

    private final HistoryRecordingAgent agent;

    @Inject ResponseResumer resumer;
    @Inject SecurityIdentity securityIdentity;
    @Inject ObjectMapper objectMapper;

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

        return agent.chatDetailed(conversationId, request.getMessage())
                .map(this::encode)
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
            throw new RuntimeException(e);
        }
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
    }
}
