package io.github.chirino.memory.api;

import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.MessageDto;
import io.github.chirino.memory.api.dto.PagedMessages;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.client.model.ConversationMembership;
import io.github.chirino.memory.client.model.ErrorResponse;
import io.github.chirino.memory.client.model.Message;
import io.github.chirino.memory.client.model.SearchResult;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.model.AdminActionRequest;
import io.github.chirino.memory.model.AdminConversationQuery;
import io.github.chirino.memory.model.AdminMessageQuery;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.model.MessageChannel;
import io.github.chirino.memory.security.AdminAuditLogger;
import io.github.chirino.memory.security.AdminRoleResolver;
import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.security.JustificationRequiredException;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceConflictException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
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
import java.time.OffsetDateTime;
import java.time.format.DateTimeFormatter;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.jboss.logging.Logger;

@Path("/v1/admin")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class AdminResource {

    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;
    private static final Logger LOG = Logger.getLogger(AdminResource.class);

    @Inject MemoryStoreSelector storeSelector;

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

    @Inject AdminRoleResolver roleResolver;

    @Inject AdminAuditLogger auditLogger;

    private MemoryStore store() {
        return storeSelector.getStore();
    }

    @GET
    @Path("/conversations")
    public Response listConversations(
            @QueryParam("userId") String userId,
            @QueryParam("includeDeleted") @jakarta.ws.rs.DefaultValue("false")
                    boolean includeDeleted,
            @QueryParam("onlyDeleted") @jakarta.ws.rs.DefaultValue("false") boolean onlyDeleted,
            @QueryParam("deletedAfter") String deletedAfter,
            @QueryParam("deletedBefore") String deletedBefore,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("userId", userId);
            params.put("includeDeleted", includeDeleted);
            params.put("onlyDeleted", onlyDeleted);
            auditLogger.logRead(
                    "listConversations", params, justification, identity, apiKeyContext);

            AdminConversationQuery query = new AdminConversationQuery();
            query.setUserId(userId);
            query.setIncludeDeleted(includeDeleted);
            query.setOnlyDeleted(onlyDeleted);
            if (deletedAfter != null && !deletedAfter.isBlank()) {
                query.setDeletedAfter(OffsetDateTime.parse(deletedAfter, ISO_FORMATTER));
            }
            if (deletedBefore != null && !deletedBefore.isBlank()) {
                query.setDeletedBefore(OffsetDateTime.parse(deletedBefore, ISO_FORMATTER));
            }
            query.setAfter(after);
            query.setLimit(limit != null ? limit : 100);

            List<ConversationSummaryDto> internal = store().adminListConversations(query);
            Map<String, Object> response = new HashMap<>();
            response.put("data", internal);
            return Response.ok(response).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @GET
    @Path("/conversations/{id}")
    public Response getConversation(
            @PathParam("id") String id,
            @QueryParam("includeDeleted") @jakarta.ws.rs.DefaultValue("false")
                    boolean includeDeleted,
            @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            params.put("includeDeleted", includeDeleted);
            auditLogger.logRead("getConversation", params, justification, identity, apiKeyContext);

            Optional<ConversationDto> dto = store().adminGetConversation(id, includeDeleted);
            if (dto.isEmpty()) {
                return notFound(new ResourceNotFoundException("conversation", id));
            }
            return Response.ok(dto.get()).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @DELETE
    @Path("/conversations/{id}")
    public Response deleteConversation(@PathParam("id") String id, AdminActionRequest request) {
        try {
            roleResolver.requireAdmin(identity, apiKeyContext);
            String justification = request != null ? request.getJustification() : null;
            auditLogger.logWrite("deleteConversation", id, justification, identity, apiKeyContext);

            store().adminDeleteConversation(id);
            return Response.noContent().build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @POST
    @Path("/conversations/{id}/restore")
    public Response restoreConversation(@PathParam("id") String id, AdminActionRequest request) {
        try {
            roleResolver.requireAdmin(identity, apiKeyContext);
            String justification = request != null ? request.getJustification() : null;
            auditLogger.logWrite("restoreConversation", id, justification, identity, apiKeyContext);

            store().adminRestoreConversation(id);
            Optional<ConversationDto> dto = store().adminGetConversation(id, true);
            return Response.ok(dto.orElse(null)).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (ResourceConflictException e) {
            return conflict(e.getMessage());
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @GET
    @Path("/conversations/{id}/messages")
    public Response getMessages(
            @PathParam("id") String id,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("channel") String channel,
            @QueryParam("includeDeleted") @jakarta.ws.rs.DefaultValue("false")
                    boolean includeDeleted,
            @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            params.put("includeDeleted", includeDeleted);
            auditLogger.logRead("getMessages", params, justification, identity, apiKeyContext);

            AdminMessageQuery query = new AdminMessageQuery();
            query.setAfterMessageId(after);
            query.setLimit(limit != null ? limit : 50);
            if (channel != null && !channel.isBlank()) {
                query.setChannel(MessageChannel.fromString(channel));
            }
            query.setIncludeDeleted(includeDeleted);

            PagedMessages result = store().adminGetMessages(id, query);
            Map<String, Object> response = new HashMap<>();
            response.put("data", result.getMessages());
            response.put("nextCursor", result.getNextCursor());
            return Response.ok(response).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @GET
    @Path("/conversations/{id}/memberships")
    public Response getMemberships(
            @PathParam("id") String id,
            @QueryParam("includeDeleted") @jakarta.ws.rs.DefaultValue("false")
                    boolean includeDeleted,
            @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            params.put("includeDeleted", includeDeleted);
            auditLogger.logRead("getMemberships", params, justification, identity, apiKeyContext);

            List<ConversationMembershipDto> memberships =
                    store().adminListMemberships(id, includeDeleted);
            List<ConversationMembership> data =
                    memberships.stream()
                            .map(
                                    dto -> {
                                        ConversationMembership result =
                                                new ConversationMembership();
                                        result.setConversationGroupId(dto.getConversationGroupId());
                                        result.setUserId(dto.getUserId());
                                        if (dto.getAccessLevel() != null) {
                                            result.setAccessLevel(
                                                    ConversationMembership.AccessLevelEnum
                                                            .fromString(
                                                                    dto.getAccessLevel()
                                                                            .name()
                                                                            .toLowerCase()));
                                        }
                                        result.setCreatedAt(parseDate(dto.getCreatedAt()));
                                        return result;
                                    })
                            .toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            return Response.ok(response).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @POST
    @Path("/search/messages")
    public Response searchMessages(
            AdminSearchQuery request, @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("query", request != null ? request.getQuery() : null);
            params.put("userId", request != null ? request.getUserId() : null);
            params.put("includeDeleted", request != null && request.isIncludeDeleted());
            auditLogger.logRead("searchMessages", params, justification, identity, apiKeyContext);

            if (request == null || request.getQuery() == null || request.getQuery().isBlank()) {
                return badRequest("query is required");
            }

            List<SearchResultDto> results = store().adminSearchMessages(request);
            List<SearchResult> data =
                    results.stream()
                            .map(
                                    dto -> {
                                        SearchResult result = new SearchResult();
                                        result.setMessage(toClientMessage(dto.getMessage()));
                                        result.setScore((float) dto.getScore());
                                        result.setHighlights(dto.getHighlights());
                                        return result;
                                    })
                            .toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            return Response.ok(response).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    private Response notFound(ResourceNotFoundException e) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Not found");
        error.setCode("not_found");
        error.setDetails(Map.of("resource", e.getResource(), "id", e.getId()));
        return Response.status(Response.Status.NOT_FOUND).entity(error).build();
    }

    private Response forbidden(AccessDeniedException e) {
        LOG.infof("Access denied for admin operation: %s", e.getMessage());
        ErrorResponse error = new ErrorResponse();
        error.setError("Forbidden");
        error.setCode("forbidden");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(Response.Status.FORBIDDEN).entity(error).build();
    }

    private Response justificationRequired() {
        ErrorResponse error = new ErrorResponse();
        error.setError("Justification is required for admin operations");
        error.setCode("JUSTIFICATION_REQUIRED");
        return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
    }

    private Response conflict(String message) {
        ErrorResponse error = createErrorResponse("Conflict", "conflict", message);
        return Response.status(Response.Status.CONFLICT).entity(error).build();
    }

    private Response badRequest(String message) {
        ErrorResponse error = createErrorResponse("Bad request", "bad_request", message);
        return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
    }

    private ErrorResponse createErrorResponse(String error, String code, String message) {
        ErrorResponse response = new ErrorResponse();
        response.setError(error);
        response.setCode(code);
        response.setDetails(Map.of("message", message));
        return response;
    }

    private Message toClientMessage(MessageDto dto) {
        if (dto == null) {
            return null;
        }
        Message result = new Message();
        result.setId(dto.getId());
        result.setConversationId(dto.getConversationId());
        result.setUserId(dto.getUserId());
        if (dto.getChannel() != null) {
            result.setChannel(Message.ChannelEnum.fromString(dto.getChannel().toValue()));
        }
        result.setMemoryEpoch(dto.getMemoryEpoch());
        if (dto.getContent() != null) {
            result.setContent(dto.getContent());
        }
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        return result;
    }

    private OffsetDateTime parseDate(String value) {
        if (value == null) {
            return null;
        }
        return OffsetDateTime.parse(value, ISO_FORMATTER);
    }
}
