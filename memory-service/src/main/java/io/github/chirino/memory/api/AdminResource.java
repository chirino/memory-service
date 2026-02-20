package io.github.chirino.memory.api;

import io.github.chirino.memory.admin.client.model.AdminConversation;
import io.github.chirino.memory.admin.client.model.AdminConversationSummary;
import io.github.chirino.memory.admin.client.model.ConversationForkSummary;
import io.github.chirino.memory.admin.client.model.ConversationMembership;
import io.github.chirino.memory.admin.client.model.Entry;
import io.github.chirino.memory.admin.client.model.ErrorResponse;
import io.github.chirino.memory.admin.client.model.EvictRequest;
import io.github.chirino.memory.admin.client.model.SearchResult;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.PagedEntries;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.model.AdminConversationQuery;
import io.github.chirino.memory.model.AdminMessageQuery;
import io.github.chirino.memory.model.AdminSearchQuery;
import io.github.chirino.memory.model.Channel;
import io.github.chirino.memory.security.AdminAuditLogger;
import io.github.chirino.memory.security.AdminRoleResolver;
import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.security.JustificationRequiredException;
import io.github.chirino.memory.service.EvictionService;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceConflictException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.github.chirino.memory.store.SearchTypeUnavailableException;
import io.github.chirino.memory.vector.SearchExecutionService;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.validation.Valid;
import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.Size;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.HeaderParam;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.core.StreamingOutput;
import java.io.IOException;
import java.io.OutputStream;
import java.io.PrintWriter;
import java.time.Duration;
import java.time.OffsetDateTime;
import java.time.format.DateTimeFormatter;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.UUID;
import org.jboss.logging.Logger;

@Path("/v1/admin")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class AdminResource {

    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;
    private static final Logger LOG = Logger.getLogger(AdminResource.class);

    @Inject MemoryStoreSelector storeSelector;

    @Inject SearchExecutionService searchExecutionService;

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

    @Inject AdminRoleResolver roleResolver;

    @Inject AdminAuditLogger auditLogger;

    @Inject EvictionService evictionService;

    private MemoryStore store() {
        return storeSelector.getStore();
    }

    @GET
    @Path("/conversations")
    public Response listConversations(
            @QueryParam("mode") String mode,
            @QueryParam("userId") String userId,
            @QueryParam("includeDeleted") @jakarta.ws.rs.DefaultValue("false")
                    boolean includeDeleted,
            @QueryParam("onlyDeleted") @jakarta.ws.rs.DefaultValue("false") boolean onlyDeleted,
            @QueryParam("deletedAfter") String deletedAfter,
            @QueryParam("deletedBefore") String deletedBefore,
            @QueryParam("afterCursor") String afterCursor,
            @QueryParam("limit") @Min(1) @Max(1000) Integer limit,
            @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("mode", mode);
            params.put("userId", userId);
            params.put("includeDeleted", includeDeleted);
            params.put("onlyDeleted", onlyDeleted);
            auditLogger.logRead(
                    "listConversations", params, justification, identity, apiKeyContext);

            AdminConversationQuery query = new AdminConversationQuery();
            query.setMode(ConversationListMode.fromQuery(mode));
            query.setUserId(userId);
            query.setIncludeDeleted(includeDeleted);
            query.setOnlyDeleted(onlyDeleted);
            if (deletedAfter != null && !deletedAfter.isBlank()) {
                query.setDeletedAfter(OffsetDateTime.parse(deletedAfter, ISO_FORMATTER));
            }
            if (deletedBefore != null && !deletedBefore.isBlank()) {
                query.setDeletedBefore(OffsetDateTime.parse(deletedBefore, ISO_FORMATTER));
            }
            query.setAfterCursor(afterCursor);
            query.setLimit(limit != null ? limit : 100);

            List<ConversationSummaryDto> internal = store().adminListConversations(query);
            List<AdminConversationSummary> data =
                    internal.stream().map(this::toAdminConversationSummary).toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            String cursor =
                    data.size() == query.getLimit() && !data.isEmpty()
                            ? data.get(data.size() - 1).getId().toString()
                            : null;
            response.put("afterCursor", cursor);
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
            @PathParam("id") String id, @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            auditLogger.logRead("getConversation", params, justification, identity, apiKeyContext);

            Optional<ConversationDto> dto = store().adminGetConversation(id);
            if (dto.isEmpty()) {
                return notFound(new ResourceNotFoundException("conversation", id));
            }
            return Response.ok(toAdminConversation(dto.get())).build();
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
    public Response deleteConversation(
            @PathParam("id") String id,
            io.github.chirino.memory.admin.client.model.AdminActionRequest request) {
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
    public Response restoreConversation(
            @PathParam("id") String id,
            io.github.chirino.memory.admin.client.model.AdminActionRequest request) {
        try {
            roleResolver.requireAdmin(identity, apiKeyContext);
            String justification = request != null ? request.getJustification() : null;
            auditLogger.logWrite("restoreConversation", id, justification, identity, apiKeyContext);

            store().adminRestoreConversation(id);
            Optional<ConversationDto> dto = store().adminGetConversation(id);
            return Response.ok(dto.map(this::toAdminConversation).orElse(null)).build();
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
    @Path("/conversations/{id}/entries")
    public Response getEntries(
            @PathParam("id") String id,
            @QueryParam("afterCursor") @Size(max = 100) String afterCursor,
            @QueryParam("limit") @Min(1) @Max(1000) Integer limit,
            @QueryParam("channel") String channel,
            @QueryParam("justification") String justification,
            @QueryParam("forks") @DefaultValue("none") String forks) {
        boolean allForks = "all".equalsIgnoreCase(forks);
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            auditLogger.logRead("getMessages", params, justification, identity, apiKeyContext);

            AdminMessageQuery query = new AdminMessageQuery();
            query.setAfterEntryId(afterCursor);
            query.setLimit(limit != null ? limit : 50);
            if (channel != null && !channel.isBlank()) {
                query.setChannel(Channel.fromString(channel));
            }
            query.setAllForks(allForks);

            PagedEntries result = store().adminGetEntries(id, query);
            List<Entry> entries = result.getEntries().stream().map(this::toAdminEntry).toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", entries);
            response.put("afterCursor", result.getAfterCursor());
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
            @QueryParam("afterCursor") String afterCursor,
            @QueryParam("limit") @Min(1) @Max(1000) Integer limit,
            @QueryParam("justification") String justification) {
        int pageSize = limit != null ? limit : 50;
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            auditLogger.logRead("getMemberships", params, justification, identity, apiKeyContext);

            List<ConversationMembershipDto> memberships =
                    store().adminListMemberships(id, afterCursor, pageSize);
            List<ConversationMembership> data =
                    memberships.stream()
                            .map(
                                    dto -> {
                                        ConversationMembership result =
                                                new ConversationMembership();
                                        result.setConversationId(
                                                id != null ? UUID.fromString(id) : null);
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
            String cursor =
                    data.size() == pageSize && !data.isEmpty()
                            ? data.get(data.size() - 1).getUserId()
                            : null;
            response.put("afterCursor", cursor);
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
    @Path("/conversations/{id}/forks")
    public Response listForks(
            @PathParam("id") String id,
            @QueryParam("afterCursor") String afterCursor,
            @QueryParam("limit") @Min(1) @Max(1000) Integer limit,
            @QueryParam("justification") String justification) {
        int pageSize = limit != null ? limit : 50;
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            auditLogger.logRead("listForks", params, justification, identity, apiKeyContext);

            List<ConversationForkSummaryDto> forks =
                    store().adminListForks(id, afterCursor, pageSize);
            List<ConversationForkSummary> data =
                    forks.stream()
                            .map(
                                    dto -> {
                                        ConversationForkSummary result =
                                                new ConversationForkSummary();
                                        result.setConversationId(
                                                dto.getConversationId() != null
                                                        ? UUID.fromString(dto.getConversationId())
                                                        : null);
                                        result.setForkedAtEntryId(
                                                dto.getForkedAtEntryId() != null
                                                        ? UUID.fromString(dto.getForkedAtEntryId())
                                                        : null);
                                        result.setForkedAtConversationId(
                                                dto.getForkedAtConversationId() != null
                                                        ? UUID.fromString(
                                                                dto.getForkedAtConversationId())
                                                        : null);
                                        result.setTitle(dto.getTitle());
                                        result.setCreatedAt(parseDate(dto.getCreatedAt()));
                                        return result;
                                    })
                            .toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            String cursorValue =
                    data.size() == pageSize && !data.isEmpty()
                            ? data.get(data.size() - 1).getConversationId().toString()
                            : null;
            response.put("afterCursor", cursorValue);
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
    @Path("/conversations/search")
    public Response searchConversations(
            @Valid AdminSearchQuery request, @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("query", request != null ? request.getQuery() : null);
            params.put("searchType", request != null ? request.getSearchType() : null);
            params.put("userId", request != null ? request.getUserId() : null);
            params.put("includeDeleted", request != null && request.isIncludeDeleted());
            auditLogger.logRead("searchMessages", params, justification, identity, apiKeyContext);

            if (!searchExecutionService.isSearchAvailable()) {
                return searchStoreUnavailable();
            }

            boolean includeEntry = request.getIncludeEntry() == null || request.getIncludeEntry();
            SearchResultsDto internalResults = searchExecutionService.adminSearch(request);
            List<SearchResult> data =
                    internalResults.getResults().stream()
                            .map(dto -> toAdminSearchResult(dto, includeEntry))
                            .toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            if (internalResults.getAfterCursor() != null) {
                response.put("afterCursor", internalResults.getAfterCursor());
            }
            return Response.ok(response).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (SearchTypeUnavailableException e) {
            return searchTypeUnavailable(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    private Response searchStoreUnavailable() {
        ErrorResponse error = new ErrorResponse();
        error.setError("Search not available");
        error.setCode("search_store_disabled");
        error.setDetails(
                Map.of(
                        "message",
                        "Enable a vector search store and/or full-text datastore search to use"
                                + " this endpoint."));
        return Response.status(Response.Status.NOT_IMPLEMENTED).entity(error).build();
    }

    private Response searchTypeUnavailable(SearchTypeUnavailableException e) {
        Map<String, Object> body = new HashMap<>();
        body.put("error", "search_type_unavailable");
        body.put("message", e.getMessage());
        body.put("availableTypes", e.getAvailableTypes());
        return Response.status(501).entity(body).build();
    }

    @POST
    @Path("/evict")
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces({MediaType.APPLICATION_JSON, "text/event-stream"})
    public Response evict(
            @HeaderParam("Accept") @DefaultValue(MediaType.APPLICATION_JSON) String accept,
            @QueryParam("async") @DefaultValue("false") boolean async,
            @Valid EvictRequest request) {
        try {
            roleResolver.requireAdmin(identity, apiKeyContext);

            String target =
                    String.format(
                            "retentionPeriod=%s,resourceTypes=%s",
                            request.getRetentionPeriod(), request.getResourceTypes());
            auditLogger.logWrite(
                    "evict", target, request.getJustification(), identity, apiKeyContext);

            Duration retention = Duration.parse(request.getRetentionPeriod());

            // Validate retention period is positive
            if (retention.isNegative() || retention.isZero()) {
                return badRequest("retentionPeriod must be a positive duration");
            }

            Set<String> resourceTypes =
                    request.getResourceTypes().stream()
                            .map(EvictRequest.ResourceTypesEnum::value)
                            .collect(java.util.stream.Collectors.toSet());

            // Async mode or SSE header: stream progress via SSE
            boolean wantsSSE = async || accept.contains("text/event-stream");

            if (wantsSSE) {
                // Return SSE stream with progress updates
                return Response.ok(
                                new StreamingOutput() {
                                    @Override
                                    public void write(OutputStream output) throws IOException {
                                        try (PrintWriter writer = new PrintWriter(output, true)) {
                                            try {
                                                evictionService.evict(
                                                        retention,
                                                        resourceTypes,
                                                        progress -> {
                                                            if (writer.checkError()) {
                                                                throw new RuntimeException(
                                                                        "Client disconnected");
                                                            }
                                                            writer.println(
                                                                    "data: {\"progress\": "
                                                                            + progress
                                                                            + "}");
                                                            writer.println();
                                                            writer.flush();
                                                        });
                                            } catch (Exception e) {
                                                // Emit error event before closing the stream
                                                String errorMsg =
                                                        e.getMessage() != null
                                                                ? e.getMessage()
                                                                        .replace("\"", "\\\"")
                                                                        .replace("\n", " ")
                                                                : "Unknown error";
                                                writer.println(
                                                        "event: error\ndata: {\"error\": \""
                                                                + errorMsg
                                                                + "\"}");
                                                writer.println();
                                                writer.flush();
                                            }
                                        }
                                    }
                                })
                        .type("text/event-stream")
                        .build();
            } else {
                // Default: simple 204 No Content
                evictionService.evict(retention, resourceTypes, null);
                return Response.noContent().build();
            }
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        } catch (java.time.format.DateTimeParseException e) {
            return badRequest("Invalid retention period format: " + e.getMessage());
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

    private OffsetDateTime parseDate(String value) {
        if (value == null) {
            return null;
        }
        return OffsetDateTime.parse(value, ISO_FORMATTER);
    }

    private Entry toAdminEntry(EntryDto dto) {
        if (dto == null) {
            return null;
        }
        Entry result = new Entry();
        result.setId(dto.getId() != null ? UUID.fromString(dto.getId()) : null);
        result.setConversationId(
                dto.getConversationId() != null ? UUID.fromString(dto.getConversationId()) : null);
        result.setUserId(dto.getUserId());
        result.setClientId(dto.getClientId());
        if (dto.getChannel() != null) {
            result.setChannel(
                    io.github.chirino.memory.admin.client.model.Channel.fromString(
                            dto.getChannel().toValue()));
        }
        result.setEpoch(dto.getEpoch());
        result.setContentType(dto.getContentType());
        if (dto.getContent() != null) {
            result.setContent(dto.getContent());
        }
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        return result;
    }

    private SearchResult toAdminSearchResult(SearchResultDto dto, boolean includeEntry) {
        SearchResult result = new SearchResult();
        result.setConversationId(
                dto.getConversationId() != null ? UUID.fromString(dto.getConversationId()) : null);
        result.setConversationTitle(dto.getConversationTitle());
        result.setEntryId(dto.getEntryId() != null ? UUID.fromString(dto.getEntryId()) : null);
        if (includeEntry) {
            result.setEntry(toAdminEntry(dto.getEntry()));
        }
        result.setScore((float) dto.getScore());
        result.setHighlights(dto.getHighlights());
        return result;
    }

    private AdminConversationSummary toAdminConversationSummary(ConversationSummaryDto dto) {
        AdminConversationSummary result = new AdminConversationSummary();
        result.setId(dto.getId() != null ? UUID.fromString(dto.getId()) : null);
        result.setTitle(dto.getTitle());
        result.setOwnerUserId(dto.getOwnerUserId());
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        result.setUpdatedAt(parseDate(dto.getUpdatedAt()));
        result.setDeletedAt(parseDate(dto.getDeletedAt()));
        result.setLastMessagePreview(dto.getLastMessagePreview());
        if (dto.getAccessLevel() != null) {
            result.setAccessLevel(
                    AdminConversationSummary.AccessLevelEnum.fromString(
                            dto.getAccessLevel().name().toLowerCase()));
        }
        return result;
    }

    private AdminConversation toAdminConversation(ConversationDto dto) {
        AdminConversation result = new AdminConversation();
        result.setId(dto.getId() != null ? UUID.fromString(dto.getId()) : null);
        result.setTitle(dto.getTitle());
        result.setOwnerUserId(dto.getOwnerUserId());
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        result.setUpdatedAt(parseDate(dto.getUpdatedAt()));
        result.setDeletedAt(parseDate(dto.getDeletedAt()));
        result.setLastMessagePreview(dto.getLastMessagePreview());
        if (dto.getAccessLevel() != null) {
            result.setAccessLevel(
                    AdminConversation.AccessLevelEnum.fromString(
                            dto.getAccessLevel().name().toLowerCase()));
        }
        result.setForkedAtEntryId(
                dto.getForkedAtEntryId() != null
                        ? UUID.fromString(dto.getForkedAtEntryId())
                        : null);
        result.setForkedAtConversationId(
                dto.getForkedAtConversationId() != null
                        ? UUID.fromString(dto.getForkedAtConversationId())
                        : null);
        return result;
    }
}
