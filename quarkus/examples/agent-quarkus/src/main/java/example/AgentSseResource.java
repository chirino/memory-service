package example;

import io.quarkus.logging.Log;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.ws.rs.BadRequestException;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;

@Path("/customer-support-agent")
@ApplicationScoped
public class AgentSseResource {

    private final HistoryRecordingAgent agent;

    public AgentSseResource(HistoryRecordingAgent agent) {
        this.agent = agent;
    }

    @POST
    @Path("/{conversationId}/sse")
    @Blocking
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.SERVER_SENT_EVENTS)
    public Multi<TokenFrame> stream(
            @PathParam("conversationId") String conversationId, MessageRequest request) {
        if (conversationId == null || conversationId.isBlank()) {
            throw new BadRequestException("Conversation ID is required");
        }
        if (request == null || request.getMessage() == null || request.getMessage().isBlank()) {
            throw new BadRequestException("Message is required");
        }

        Log.infof("Received SSE request for conversationId=%s", conversationId);

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
