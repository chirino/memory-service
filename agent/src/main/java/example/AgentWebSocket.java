package example;

import io.quarkus.websockets.next.CloseReason;
import io.quarkus.websockets.next.OnTextMessage;
import io.quarkus.websockets.next.PathParam;
import io.quarkus.websockets.next.WebSocket;
import io.quarkus.websockets.next.WebSocketConnection;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Multi;
import jakarta.ws.rs.BadRequestException;
import org.jboss.logging.Logger;

/**
 * WebSocket endpoint for normal chat interactions.
 * Receives user messages and streams AI responses.
 */
@WebSocket(path = "/customer-support-agent/{conversationId}/ws")
public class AgentWebSocket {

    private static final Logger LOG = Logger.getLogger(AgentWebSocket.class);

    private final HistoryRecordingAgent agent;

    public AgentWebSocket(HistoryRecordingAgent agent) {
        this.agent = agent;
    }

    @OnTextMessage
    @Blocking // Offload REST client calls from the event loop
    public Multi<Void> onTextMessage(
            WebSocketConnection connection,
            @PathParam("conversationId") String conversationId,
            String message) {
        if (conversationId == null || conversationId.isBlank()) {
            throw new BadRequestException("Conversation ID is required");
        }
        if (message == null || message.isBlank()) {
            LOG.warnf("Received empty message for conversationId=%s", conversationId);
            return Multi.createFrom().empty();
        }
        LOG.infof("Received message for conversationId=%s", conversationId);
        return agent.chat(conversationId, message)
                .onItem()
                .transformToUniAndConcatenate(connection::sendText)
                .onCompletion()
                .call(
                        () -> {
                            LOG.infof(
                                    "Chat stream completed for conversationId=%s, closing"
                                            + " connection",
                                    conversationId);
                            return connection.close(CloseReason.NORMAL);
                        })
                .onFailure()
                .invoke(
                        failure ->
                                LOG.warnf(
                                        failure,
                                        "Chat failed for conversationId=%s",
                                        conversationId))
                .onFailure()
                .call(
                        failure -> {
                            LOG.infof(
                                    "Closing connection due to chat failure for conversationId=%s",
                                    conversationId);
                            return connection.close(
                                    new CloseReason(
                                            CloseReason.INTERNAL_SERVER_ERROR.getCode(),
                                            "chat failed"));
                        });
    }
}
