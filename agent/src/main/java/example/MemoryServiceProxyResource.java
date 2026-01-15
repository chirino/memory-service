package example;

import static jakarta.ws.rs.core.Response.Status.CREATED;
import static jakarta.ws.rs.core.Response.Status.NO_CONTENT;
import static jakarta.ws.rs.core.Response.Status.OK;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.ForkFromMessageRequest;
import io.github.chirino.memory.client.model.MessageChannel;
import io.github.chirino.memory.client.model.ShareConversationRequest;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.oidc.AccessTokenCredential;
import io.quarkus.security.credential.TokenCredential;
import io.quarkus.security.identity.SecurityIdentity;
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
import java.util.Map;
import java.util.function.Supplier;
import org.jboss.logging.Logger;

/**
 * JAX-RS resource that proxies requests to the /v1/conversations endpoint
 * using the ConversationsApi REST client.
 */
@Path("/v1/conversations")
@ApplicationScoped
public class MemoryServiceProxyResource {

    private static final Logger LOG = Logger.getLogger(MemoryServiceProxyResource.class);
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    @Inject MemoryServiceApiBuilder memoryServiceApiBuilder;

    @Inject SecurityIdentity securityIdentity;

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversations(
            @QueryParam("mode") String mode,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("query") String query) {
        return execute(
                () -> conversationsApi().listConversations(mode, after, limit, query),
                OK,
                "Error listing conversations");
    }

    @GET
    @Path("/{conversationId}")
    @Produces(MediaType.APPLICATION_JSON)
    public Response getConversation(@PathParam("conversationId") String conversationId) {
        return execute(
                () -> conversationsApi().getConversation(conversationId),
                OK,
                "Error getting history %s",
                conversationId);
    }

    @DELETE
    @Path("/{conversationId}")
    public Response deleteConversation(@PathParam("conversationId") String conversationId) {
        return executeVoid(
                () -> conversationsApi().deleteConversation(conversationId),
                NO_CONTENT,
                "Error deleting history %s",
                conversationId);
    }

    @GET
    @Path("/{conversationId}/messages")
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversationMessages(
            @PathParam("conversationId") String conversationId,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("channel") MessageChannel channel,
            @QueryParam("epoch") String epoch) {
        return execute(
                () ->
                        conversationsApi()
                                .listConversationMessages(
                                        conversationId, after, limit, channel, epoch),
                OK,
                "Error listing messages for history %s",
                conversationId);
    }

    @POST
    @Path("/{conversationId}/messages/{messageId}/fork")
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Response forkConversationAtMessage(
            @PathParam("conversationId") String conversationId,
            @PathParam("messageId") String messageId,
            String body) {
        try {
            ForkFromMessageRequest request =
                    body == null || body.isBlank()
                            ? new ForkFromMessageRequest()
                            : OBJECT_MAPPER.readValue(body, ForkFromMessageRequest.class);
            return execute(
                    () ->
                            conversationsApi()
                                    .forkConversationAtMessage(conversationId, messageId, request),
                    OK,
                    "Error forking history %s at message %s",
                    conversationId,
                    messageId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing fork request body");
            return handleException(e);
        }
    }

    @GET
    @Path("/{conversationId}/forks")
    @Produces(MediaType.APPLICATION_JSON)
    public Response listConversationForks(@PathParam("conversationId") String conversationId) {
        return execute(
                () -> conversationsApi().listConversationForks(conversationId),
                OK,
                "Error listing forks for history %s",
                conversationId);
    }

    @POST
    @Path("/{conversationId}/forks")
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Response shareConversation(
            @PathParam("conversationId") String conversationId, String body) {
        try {
            ShareConversationRequest request =
                    OBJECT_MAPPER.readValue(body, ShareConversationRequest.class);
            return execute(
                    () -> conversationsApi().shareConversation(conversationId, request),
                    CREATED,
                    "Error sharing history %s",
                    conversationId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing share request body");
            return handleException(e);
        }
    }

    @POST
    @Path("/{conversationId}/cancel-response")
    public Response cancelResponse(@PathParam("conversationId") String conversationId) {
        return executeVoid(
                () -> conversationsApi().cancelConversationResponse(conversationId),
                OK,
                "Error cancelling response for history %s",
                conversationId);
    }

    //
    // don't expose these operations to the frontend.
    // @POST
    // @Consumes(MediaType.APPLICATION_JSON)
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response createConversation(String body) {
    //     try {
    //         CreateConversationRequest request =
    //                 OBJECT_MAPPER.readValue(body, CreateConversationRequest.class);
    //         return proxy(
    //                 () -> conversationsApi.createConversation(request),
    //                 CREATED,
    //                 "Error creating history");
    //     } catch (Exception e) {
    //         LOG.errorf(e, "Error parsing create history request body");
    //         return handleException(e);
    //     }
    // }
    // @POST
    // @Path("/{conversationId}/messages")
    // @Consumes(MediaType.APPLICATION_JSON)
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response appendConversationMessage(
    //         @PathParam("conversationId") String conversationId, String body) {
    //     try {
    //         CreateMessageRequest request =
    //                 OBJECT_MAPPER.readValue(body, CreateMessageRequest.class);
    //         return proxy(
    //                 () -> conversationsApi.appendConversationMessage(conversationId, request),
    //                 CREATED,
    //                 "Error appending message to history %s",
    //                 conversationId);
    //     } catch (Exception e) {
    //         LOG.errorf(e, "Error parsing append message request body");
    //         return handleException(e);
    //     }
    // }
    // @GET
    // @Path("/{conversationId}/memberships")
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response listConversationMemberships(
    //         @PathParam("conversationId") String conversationId) {
    //     return proxy(
    //             () -> conversationsApi.listConversationMemberships(conversationId),
    //             OK,
    //             "Error listing memberships for history %s",
    //             conversationId);
    // }
    // @PATCH
    // @Path("/{conversationId}/memberships/{userId}")
    // @Consumes(MediaType.APPLICATION_JSON)
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response updateConversationMembership(
    //         @PathParam("conversationId") String conversationId,
    //         @PathParam("userId") String userId,
    //         String body) {
    //     try {
    //         UpdateConversationMembershipRequest request =
    //                 OBJECT_MAPPER.readValue(body, UpdateConversationMembershipRequest.class);
    //         return proxy(
    //                 () ->
    //                         conversationsApi.updateConversationMembership(
    //                                 conversationId, userId, request),
    //                 OK,
    //                 "Error updating membership for history %s, user %s",
    //                 conversationId,
    //                 userId);
    //     } catch (Exception e) {
    //         LOG.errorf(e, "Error parsing update membership request body");
    //         return handleException(e);
    //     }
    // }
    // @POST
    // @Path("/{conversationId}/transfer-ownership")
    // @Consumes(MediaType.APPLICATION_JSON)
    // public Response transferConversationOwnership(
    //         @PathParam("conversationId") String conversationId, String body) {
    //     try {
    //         TransferConversationOwnershipRequest request =
    //                 OBJECT_MAPPER.readValue(body, TransferConversationOwnershipRequest.class);
    //         return proxyVoid(
    //                 () -> conversationsApi.transferConversationOwnership(conversationId,
    // request),
    //                 Response.Status.ACCEPTED,
    //                 "Error transferring ownership of history %s",
    //                 conversationId);
    //     } catch (Exception e) {
    //         LOG.errorf(e, "Error parsing transfer ownership request body");
    //         return handleException(e);
    //     }
    // }
    // @POST
    // @Path("/{conversationId}/summaries")
    // @Consumes(MediaType.APPLICATION_JSON)
    // @Produces(MediaType.APPLICATION_JSON)
    // public Response createConversationSummary(
    //         @PathParam("conversationId") String conversationId, String body) {
    //     try {
    //         CreateSummaryRequest request =
    //                 OBJECT_MAPPER.readValue(body, CreateSummaryRequest.class);
    //         return proxy(
    //                 () -> conversationsApi.createConversationSummary(conversationId, request),
    //                 CREATED,
    //                 "Error creating summary for history %s",
    //                 conversationId);
    //     } catch (Exception e) {
    //         LOG.errorf(e, "Error parsing create summary request body");
    //         return handleException(e);
    //     }
    // }

    /**
     * Helper method that executes an API call with proper error handling and security
     * identity propagation.
     *
     * @param apiCall The API call to execute
     * @param status The HTTP status code to return on success
     * @param errorMsg Error message format string (supports String.format placeholders)
     * @param args Arguments for the error message format string
     * @return Response with the API call result
     */
    private <T> Response execute(
            Supplier<T> apiCall, Response.Status status, String errorMsg, Object... args) {
        try {
            T result = apiCall.get();
            Response.ResponseBuilder builder = Response.status(status);
            if (result != null) {
                builder.entity(result);
            }
            return builder.build();
        } catch (Exception e) {
            LOG.errorf(e, errorMsg, args);
            return handleException(e);
        }
    }

    /**
     * Helper method for API calls that return void (e.g., DELETE operations).
     *
     * @param apiCall The API call to execute
     * @param status The HTTP status code to return on success
     * @param errorMsg Error message format string (supports String.format placeholders)
     * @param args Arguments for the error message format string
     * @return Response with the specified status code
     */
    private Response executeVoid(
            Runnable apiCall, Response.Status status, String errorMsg, Object... args) {
        try {
            apiCall.run();
            return Response.status(status).build();
        } catch (Exception e) {
            LOG.errorf(e, errorMsg, args);
            return handleException(e);
        }
    }

    private ConversationsApi conversationsApi() {
        String bearerToken = resolveBearerToken();
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(ConversationsApi.class);
    }

    private String resolveBearerToken() {
        if (securityIdentity == null) {
            return null;
        }
        AccessTokenCredential atc = securityIdentity.getCredential(AccessTokenCredential.class);
        if (atc != null) {
            return atc.getToken();
        }
        TokenCredential tc = securityIdentity.getCredential(TokenCredential.class);
        if (tc != null) {
            return tc.getToken();
        }
        return null;
    }

    private Response handleException(Exception e) {
        if (e instanceof jakarta.ws.rs.WebApplicationException) {
            jakarta.ws.rs.WebApplicationException wae = (jakarta.ws.rs.WebApplicationException) e;
            Response.ResponseBuilder builder = Response.status(wae.getResponse().getStatus());
            if (wae.getResponse().hasEntity()) {
                builder.entity(wae.getResponse().getEntity());
            }
            return builder.build();
        }
        LOG.errorf(e, "Unexpected error");
        return Response.status(Response.Status.INTERNAL_SERVER_ERROR)
                .entity(Map.of("error", "Internal server error"))
                .build();
    }
}
