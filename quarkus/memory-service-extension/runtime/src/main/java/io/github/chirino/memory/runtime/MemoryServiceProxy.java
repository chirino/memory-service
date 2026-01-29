package io.github.chirino.memory.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static jakarta.ws.rs.core.Response.Status.CREATED;
import static jakarta.ws.rs.core.Response.Status.NO_CONTENT;
import static jakarta.ws.rs.core.Response.Status.OK;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.api.SearchApi;
import io.github.chirino.memory.client.api.SharingApi;
import io.github.chirino.memory.client.model.Channel;
import io.github.chirino.memory.client.model.CreateConversationRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.ForkFromEntryRequest;
import io.github.chirino.memory.client.model.IndexTranscriptRequest;
import io.github.chirino.memory.client.model.ShareConversationRequest;
import io.github.chirino.memory.client.model.TransferConversationOwnershipRequest;
import io.github.chirino.memory.client.model.UpdateConversationMembershipRequest;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.ws.rs.core.Response;
import java.util.Map;
import java.util.function.Supplier;
import org.jboss.logging.Logger;

/**
 * Helper class to make it easier to implement a JAXRS proxy to the memory service apis.
 */
public class MemoryServiceProxy {

    private static final Logger LOG = Logger.getLogger(MemoryServiceProxy.class);
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    @Inject MemoryServiceApiBuilder memoryServiceApiBuilder;

    @Inject SecurityIdentity securityIdentity;

    public Response listConversations(String mode, String after, Integer limit, String query) {
        return execute(
                () -> conversationsApi().listConversations(mode, after, limit, query),
                OK,
                "Error listing conversations");
    }

    public Response getConversation(String conversationId) {
        return execute(
                () -> conversationsApi().getConversation(conversationId),
                OK,
                "Error getting history %s",
                conversationId);
    }

    public Response deleteConversation(String conversationId) {
        return executeVoid(
                () -> conversationsApi().deleteConversation(conversationId),
                NO_CONTENT,
                "Error deleting history %s",
                conversationId);
    }

    public Response listConversationEntries(
            String conversationId, String after, Integer limit, Channel channel, String epoch) {
        return execute(
                () ->
                        conversationsApi()
                                .listConversationEntries(
                                        conversationId, after, limit, channel, epoch),
                OK,
                "Error listing entries for history %s",
                conversationId);
    }

    public Response forkConversationAtEntry(String conversationId, String entryId, String body) {
        try {
            ForkFromEntryRequest request =
                    body == null || body.isBlank()
                            ? new ForkFromEntryRequest()
                            : OBJECT_MAPPER.readValue(body, ForkFromEntryRequest.class);
            return execute(
                    () ->
                            conversationsApi()
                                    .forkConversationAtEntry(conversationId, entryId, request),
                    OK,
                    "Error forking history %s at entry %s",
                    conversationId,
                    entryId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing fork request body");
            return handleException(e);
        }
    }

    public Response listConversationForks(String conversationId) {
        return execute(
                () -> conversationsApi().listConversationForks(conversationId),
                OK,
                "Error listing forks for history %s",
                conversationId);
    }

    public Response shareConversation(String conversationId, String body) {
        try {
            ShareConversationRequest request =
                    OBJECT_MAPPER.readValue(body, ShareConversationRequest.class);
            return execute(
                    () -> sharingApi().shareConversation(conversationId, request),
                    CREATED,
                    "Error sharing history %s",
                    conversationId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing share request body");
            return handleException(e);
        }
    }

    public Response cancelResponse(String conversationId) {
        return executeVoid(
                () -> conversationsApi().deleteConversationResponse(conversationId),
                OK,
                "Error cancelling response for history %s",
                conversationId);
    }

    public Response createConversation(String body) {
        try {
            CreateConversationRequest request =
                    OBJECT_MAPPER.readValue(body, CreateConversationRequest.class);
            return execute(
                    () -> conversationsApi().createConversation(request),
                    CREATED,
                    "Error creating history");
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing create history request body");
            return handleException(e);
        }
    }

    public Response appendConversationEntry(String conversationId, String body) {
        try {
            CreateEntryRequest request = OBJECT_MAPPER.readValue(body, CreateEntryRequest.class);
            return execute(
                    () -> conversationsApi().appendConversationEntry(conversationId, request),
                    CREATED,
                    "Error appending entry to history %s",
                    conversationId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing append entry request body");
            return handleException(e);
        }
    }

    public Response listConversationMemberships(String conversationId) {
        return execute(
                () -> sharingApi().listConversationMemberships(conversationId),
                OK,
                "Error listing memberships for history %s",
                conversationId);
    }

    public Response updateConversationMembership(
            String conversationId, String userId, String body) {
        try {
            UpdateConversationMembershipRequest request =
                    OBJECT_MAPPER.readValue(body, UpdateConversationMembershipRequest.class);
            return execute(
                    () ->
                            sharingApi()
                                    .updateConversationMembership(conversationId, userId, request),
                    OK,
                    "Error updating membership for history %s, user %s",
                    conversationId,
                    userId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing update membership request body");
            return handleException(e);
        }
    }

    public Response transferConversationOwnership(String conversationId, String body) {
        try {
            TransferConversationOwnershipRequest request =
                    OBJECT_MAPPER.readValue(body, TransferConversationOwnershipRequest.class);
            return executeVoid(
                    () -> sharingApi().transferConversationOwnership(conversationId, request),
                    Response.Status.ACCEPTED,
                    "Error transferring ownership of history %s",
                    conversationId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing transfer ownership request body");
            return handleException(e);
        }
    }

    public Response indexConversationTranscript(String body) {
        try {
            IndexTranscriptRequest request =
                    OBJECT_MAPPER.readValue(body, IndexTranscriptRequest.class);
            return execute(
                    () -> searchApi().indexConversationTranscript(request),
                    CREATED,
                    "Error indexing transcript for conversation %s",
                    request.getConversationId());
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing index transcript request body");
            return handleException(e);
        }
    }

    /**
     * Helper method that executes an API call with proper error handling and security
     * identity propagation.
     *
     * @param apiCall  The API call to execute
     * @param status   The HTTP status code to return on success
     * @param errorMsg Error message format string (supports String.format placeholders)
     * @param args     Arguments for the error message format string
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
     * @param apiCall  The API call to execute
     * @param status   The HTTP status code to return on success
     * @param errorMsg Error message format string (supports String.format placeholders)
     * @param args     Arguments for the error message format string
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
        String bearerToken = bearerToken(securityIdentity);
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(ConversationsApi.class);
    }

    private SharingApi sharingApi() {
        String bearerToken = bearerToken(securityIdentity);
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(SharingApi.class);
    }

    private SearchApi searchApi() {
        String bearerToken = bearerToken(securityIdentity);
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(SearchApi.class);
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
