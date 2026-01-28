package example;

import io.github.chirino.memory.client.model.Channel;
import io.github.chirino.memory.runtime.MemoryServiceProxy;
import io.smallrye.common.annotation.Blocking;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

/**
 * JAX-RS resource that proxies requests to the /v1/conversations endpoint so that the frontend can
 * access the conversation history.
 */
@Path("/v1/conversations")
@ApplicationScoped
@Blocking // Offload REST client calls from the event loop to prevent deadlock
public class MemoryServiceProxyResource {

    @Inject MemoryServiceProxy proxy;

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversations(
            @QueryParam("mode") String mode,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("query") String query) {
        return proxy.listConversations(mode, after, limit, query);
    }

    @GET
    @Path("/{conversationId}")
    @Produces(MediaType.APPLICATION_JSON)
    public Response getConversation(@PathParam("conversationId") String conversationId) {
        return proxy.getConversation(conversationId);
    }

    @DELETE
    @Path("/{conversationId}")
    public Response deleteConversation(@PathParam("conversationId") String conversationId) {
        return proxy.deleteConversation(conversationId);
    }

    @GET
    @Path("/{conversationId}/entries")
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversationEntries(
            @PathParam("conversationId") String conversationId,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit) {
        return proxy.listConversationEntries(conversationId, after, limit, Channel.HISTORY, null);
    }

    @POST
    @Path("/{conversationId}/entries/{entryId}/fork")
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Response forkConversationAtEntry(
            @PathParam("conversationId") String conversationId,
            @PathParam("entryId") String entryId,
            String body) {
        return proxy.forkConversationAtEntry(conversationId, entryId, body);
    }

    @GET
    @Path("/{conversationId}/forks")
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversationForks(@PathParam("conversationId") String conversationId) {
        return proxy.listConversationForks(conversationId);
    }

    @POST
    @Path("/{conversationId}/forks")
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Response shareConversation(
            @PathParam("conversationId") String conversationId, String body) {
        return proxy.shareConversation(conversationId, body);
    }

    @POST
    @Path("/{conversationId}/cancel-response")
    public Response cancelResponse(@PathParam("conversationId") String conversationId) {
        return proxy.cancelResponse(conversationId);
    }

    //
    // These are addtional APIs we could expose, but our frontend does not need them yet.
    //
    // @POST
    // @Consumes(MediaType.APPLICATION_JSON)
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response createConversation(String body) {
    //     return proxy.createConversation(body);
    // }
    // @POST
    // @Path("/{conversationId}/entries")
    // @Consumes(MediaType.APPLICATION_JSON)
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response appendConversationEntry(
    //         @PathParam("conversationId") String conversationId, String body) {
    //     return proxy.appendConversationEntry(conversationId, body);
    // }
    // @GET
    // @Path("/{conversationId}/memberships")
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response listConversationMemberships(
    //         @PathParam("conversationId") String conversationId) {
    //     return proxy.listConversationMemberships(conversationId);
    // }
    // @PATCH
    // @Path("/{conversationId}/memberships/{userId}")
    // @Consumes(MediaType.APPLICATION_JSON)
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response updateConversationMembership(
    //         @PathParam("conversationId") String conversationId,
    //         @PathParam("userId") String userId,
    //         String body) {
    //     return proxy.updateConversationMembership(conversationId, userId, body);
    // }
    // @POST
    // @Path("/{conversationId}/transfer-ownership")
    // @Consumes(MediaType.APPLICATION_JSON)
    // public Response transferConversationOwnership(
    //         @PathParam("conversationId") String conversationId, String body) {
    //     return proxy.transferConversationOwnership(conversationId, body);
    // }
    // @POST
    // @Path("/{conversationId}/summaries")
    // @Consumes(MediaType.APPLICATION_JSON)
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response createConversationSummary(
    //         @PathParam("conversationId") String conversationId, String body) {
    //     return proxy.createConversationSummary(conversationId, body);
    // }

}
