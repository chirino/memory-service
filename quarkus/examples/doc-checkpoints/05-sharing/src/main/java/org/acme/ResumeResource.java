package org.acme;

import io.github.chirino.memory.history.runtime.ResponseResumer;
import io.github.chirino.memory.runtime.MemoryServiceProxy;
import io.quarkus.security.identity.SecurityIdentity;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

import java.util.List;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

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

    @GET
    @Path("/{conversationId}/resume")
    @Blocking
    @Produces(MediaType.SERVER_SENT_EVENTS)
    public Multi<String> resume(
            @PathParam("conversationId") String conversationId) {
        String bearerToken = bearerToken(securityIdentity);
        return resumer.replay(conversationId, bearerToken);
    }

    @POST
    @Path("/{conversationId}/cancel")
    public Response cancelResponse(@PathParam("conversationId") String conversationId) {
        return proxy.cancelResponse(conversationId);
    }
}
