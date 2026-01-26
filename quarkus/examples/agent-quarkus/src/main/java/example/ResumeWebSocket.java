package example;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import io.github.chirino.memory.history.runtime.ResponseResumer;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.websockets.next.CloseReason;
import io.quarkus.websockets.next.OnOpen;
import io.quarkus.websockets.next.PathParam;
import io.quarkus.websockets.next.WebSocket;
import io.quarkus.websockets.next.WebSocketConnection;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.infrastructure.Infrastructure;
import jakarta.inject.Inject;
import org.jboss.logging.Logger;

/**
 * WebSocket endpoint for resuming in-progress conversation response.
 * Sends all cached tokens from the beginning and closes the connection when all tokens are sent.
 */
@WebSocket(path = "/customer-support-agent/{conversationId}/ws/resume")
public class ResumeWebSocket {

    private static final Logger LOG = Logger.getLogger(ResumeWebSocket.class);

    private final ResponseResumer resumer;

    @Inject SecurityIdentity securityIdentity;

    public ResumeWebSocket(ResponseResumer resumer) {
        this.resumer = resumer;
    }

    @OnOpen
    public Multi<Void> onOpen(
            WebSocketConnection connection, @PathParam("conversationId") String conversationId) {

        LOG.infof("Resume WebSocket opened for conversationId=%s", conversationId);

        String bearerToken = bearerToken(securityIdentity);
        if (bearerToken != null) {
            LOG.info("Captured bearer token for response resumer");
        }

        return Multi.createFrom()
                .deferred(() -> resumer.replay(conversationId, bearerToken))
                .runSubscriptionOn(Infrastructure.getDefaultExecutor())
                .onItem()
                .transformToUniAndConcatenate(connection::sendText)
                .onCompletion()
                .call(
                        () -> {
                            LOG.infof(
                                    "Resume completed for conversationId=%s, closing connection",
                                    conversationId);
                            return connection.close(CloseReason.NORMAL);
                        })
                .onFailure()
                .invoke(
                        failure ->
                                LOG.warnf(
                                        failure,
                                        "Failed to replay cached tokens for conversationId=%s",
                                        conversationId))
                .onFailure()
                .call(
                        () ->
                                connection.close(
                                        new CloseReason(
                                                CloseReason.INTERNAL_SERVER_ERROR.getCode(),
                                                "resume failed")));
    }
}
