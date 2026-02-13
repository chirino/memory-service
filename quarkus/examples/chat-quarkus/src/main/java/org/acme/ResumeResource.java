package org.acme;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import io.github.chirino.memory.history.runtime.ResponseResumer;
import io.github.chirino.memory.runtime.MemoryServiceProxy;
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
import jakarta.ws.rs.core.Response;
import java.util.List;

@Path("/v1/conversations")
@ApplicationScoped
public class ResumeResource {

    @Inject ResponseResumer resumer;
    @Inject SecurityIdentity securityIdentity;
    @Inject MemoryServiceProxy proxy;

    @POST
    @Produces(MediaType.APPLICATION_JSON)
    @Consumes(MediaType.APPLICATION_JSON)
    @Path("/resume-check")
    public List<String> check(List<String> conversationIds) {
        return resumer.check(conversationIds, bearerToken(securityIdentity));
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

    @POST
    @Path("/{conversationId}/cancel")
    public Response cancelResponse(@PathParam("conversationId") String conversationId) {
        return proxy.cancelResponse(conversationId);
    }
}
