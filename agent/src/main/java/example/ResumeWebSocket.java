package example;

import io.github.chirino.memory.conversation.runtime.ResponseResumer;
import io.quarkus.websockets.next.CloseReason;
import io.quarkus.websockets.next.OnOpen;
import io.quarkus.websockets.next.PathParam;
import io.quarkus.websockets.next.WebSocket;
import io.quarkus.websockets.next.WebSocketConnection;
import io.smallrye.mutiny.Multi;
import org.jboss.logging.Logger;

/**
 * WebSocket endpoint for resuming in-progress conversation response.
 * Opens with a resume position, sends all cached tokens from that position,
 * and closes the connection when all tokens are sent.
 */
@WebSocket(path = "/customer-support-agent/{conversationId}/ws/{resumePosition}")
public class ResumeWebSocket {

    private static final Logger LOG = Logger.getLogger(ResumeWebSocket.class);

    private final ResponseResumer resumer;

    public ResumeWebSocket(ResponseResumer resumer) {
        this.resumer = resumer;
    }

    @OnOpen
    public Multi<Void> onOpen(
            WebSocketConnection connection,
            @PathParam("conversationId") String conversationId,
            @PathParam("resumePosition") String resumePosition) {

        LOG.infof(
                "Resume WebSocket opened for conversationId=%s resumePosition=%s",
                conversationId, resumePosition);

        return resumer.replay(conversationId, resumePosition)
                .onItem()
                .transformToUniAndConcatenate(connection::sendText)
                .onCompletion()
                .call(
                        () -> {
                            LOG.infof(
                                    "Resume completed for conversationId=%s resumePosition=%s,"
                                            + " closing connection",
                                    conversationId, resumePosition);
                            return connection.close(CloseReason.NORMAL);
                        })
                .onFailure()
                .invoke(
                        failure ->
                                LOG.warnf(
                                        failure,
                                        "Failed to replay cached tokens for conversationId=%s from"
                                                + " resumePosition=%s",
                                        conversationId,
                                        resumePosition))
                .onFailure()
                .call(
                        () ->
                                connection.close(
                                        new CloseReason(
                                                CloseReason.INTERNAL_SERVER_ERROR.getCode(),
                                                "resume failed")));
    }
}
