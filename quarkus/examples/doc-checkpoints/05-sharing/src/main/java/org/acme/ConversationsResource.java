package org.acme;

import io.github.chirino.memory.client.model.Channel;
import io.github.chirino.memory.runtime.MemoryServiceProxy;
import io.smallrye.common.annotation.Blocking;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.PATCH;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

@Path("/v1/conversations")
@ApplicationScoped
@Blocking
public class ConversationsResource {

    @Inject MemoryServiceProxy proxy;

    @GET
    @Path("/{conversationId}")
    @Produces(MediaType.APPLICATION_JSON)
    public Response getConversation(@PathParam("conversationId") String conversationId) {
        return proxy.getConversation(conversationId);
    }

    @GET
    @Path("/{conversationId}/entries")
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversationEntries(
            @PathParam("conversationId") String conversationId,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit) {
        return proxy.listConversationEntries(
                conversationId, after, limit, Channel.HISTORY, null, null);
    }

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversations(
            @QueryParam("mode") String mode,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("query") String query) {
        return proxy.listConversations(mode, after, limit, query);
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

    // Membership management endpoints
    @GET
    @Path("/{conversationId}/memberships")
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversationMemberships(
            @PathParam("conversationId") String conversationId) {
        return proxy.listConversationMemberships(conversationId);
    }

    @POST
    @Path("/{conversationId}/memberships")
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Response shareConversation(
            @PathParam("conversationId") String conversationId, String body) {
        return proxy.shareConversation(conversationId, body);
    }

    @PATCH
    @Path("/{conversationId}/memberships/{userId}")
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Response updateConversationMembership(
            @PathParam("conversationId") String conversationId,
            @PathParam("userId") String userId,
            String body) {
        return proxy.updateConversationMembership(conversationId, userId, body);
    }

    @DELETE
    @Path("/{conversationId}/memberships/{userId}")
    public Response deleteConversationMembership(
            @PathParam("conversationId") String conversationId,
            @PathParam("userId") String userId) {
        return proxy.deleteConversationMembership(conversationId, userId);
    }
}
