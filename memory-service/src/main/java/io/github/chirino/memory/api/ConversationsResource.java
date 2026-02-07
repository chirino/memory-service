package io.github.chirino.memory.api;

import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.CreateUserEntryRequest;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.PagedEntries;
import io.github.chirino.memory.api.dto.SyncResult;
import io.github.chirino.memory.attachment.AttachmentStore;
import io.github.chirino.memory.attachment.AttachmentStoreSelector;
import io.github.chirino.memory.client.model.Conversation;
import io.github.chirino.memory.client.model.ConversationForkSummary;
import io.github.chirino.memory.client.model.ConversationMembership;
import io.github.chirino.memory.client.model.ConversationSummary;
import io.github.chirino.memory.client.model.CreateConversationRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.Entry;
import io.github.chirino.memory.client.model.ErrorResponse;
import io.github.chirino.memory.client.model.ForkFromEntryRequest;
import io.github.chirino.memory.client.model.IndexConversationsResponse;
import io.github.chirino.memory.client.model.IndexEntryRequest;
import io.github.chirino.memory.client.model.ShareConversationRequest;
import io.github.chirino.memory.client.model.UnindexedEntriesResponse;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.model.Channel;
import io.github.chirino.memory.resumer.AdvertisedAddress;
import io.github.chirino.memory.resumer.ResponseResumerBackend;
import io.github.chirino.memory.resumer.ResponseResumerRedirectException;
import io.github.chirino.memory.resumer.ResponseResumerSelector;
import io.github.chirino.memory.security.AdminRoleResolver;
import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.MemoryEpochFilter;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
import io.vertx.core.net.SocketAddress;
import io.vertx.ext.web.RoutingContext;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.PATCH;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.Context;
import jakarta.ws.rs.core.HttpHeaders;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import jakarta.ws.rs.core.UriBuilder;
import jakarta.ws.rs.core.UriInfo;
import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.time.OffsetDateTime;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@Path("/v1")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class ConversationsResource {

    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    private static final Logger LOG = Logger.getLogger(ConversationsResource.class);

    private static final int MAX_REDIRECTS = 3;

    @Inject MemoryStoreSelector storeSelector;

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

    @Inject AdminRoleResolver roleResolver;

    @Inject ResponseResumerSelector resumerSelector;

    @Context HttpHeaders httpHeaders;

    @Context UriInfo uriInfo;

    @Context RoutingContext routingContext;

    @ConfigProperty(name = "memory-service.grpc-advertised-address")
    Optional<String> advertisedAddress;

    private MemoryStore store() {
        return storeSelector.getStore();
    }

    private String currentUserId() {
        return identity.getPrincipal().getName();
    }

    @GET
    @Path("/conversations")
    public Response listConversations(
            @QueryParam("mode") String mode,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("query") String query) {
        int pageSize = limit != null ? limit : 20;
        ConversationListMode listMode = ConversationListMode.fromQuery(mode);
        List<ConversationSummaryDto> internal =
                store().listConversations(currentUserId(), query, after, pageSize, listMode);
        List<ConversationSummary> data =
                internal.stream().map(this::toClientConversationSummary).toList();
        Map<String, Object> response = new HashMap<>();
        response.put("data", data);
        response.put("nextCursor", null);
        return Response.ok(response).build();
    }

    @POST
    @Path("/conversations")
    public Response createConversation(CreateConversationRequest request) {
        io.github.chirino.memory.api.dto.CreateConversationRequest internal =
                new io.github.chirino.memory.api.dto.CreateConversationRequest();
        internal.setTitle(request.getTitle());
        internal.setMetadata(request.getMetadata());

        ConversationDto dto = store().createConversation(currentUserId(), internal);
        Conversation result = toClientConversation(dto);
        return Response.status(Response.Status.CREATED).entity(result).build();
    }

    @GET
    @Path("/conversations/{conversationId}")
    public Response getConversation(@PathParam("conversationId") String conversationId) {
        try {
            ConversationDto dto = store().getConversation(currentUserId(), conversationId);
            Conversation result = toClientConversation(dto);
            return Response.ok(result).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @DELETE
    @Path("/conversations/{conversationId}")
    public Response deleteConversation(@PathParam("conversationId") String conversationId) {
        try {
            store().deleteConversation(currentUserId(), conversationId);
            return Response.noContent().build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @GET
    @Path("/conversations/{conversationId}/entries")
    public Response listEntries(
            @PathParam("conversationId") String conversationId,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("channel") String channel,
            @QueryParam("epoch") String epoch,
            @QueryParam("forks") @DefaultValue("none") String forks) {
        boolean allForks = "all".equalsIgnoreCase(forks);
        LOG.infof(
                "Listing messages for conversationId=%s, user=%s, after=%s, limit=%s,"
                        + " channel=%s, forks=%s",
                conversationId, currentUserId(), after, limit, channel, forks);
        Channel requestedChannel = channel != null ? Channel.fromString(channel) : null;
        try {
            int pageSize = limit != null ? limit : 50;
            List<EntryDto> internal;
            String nextCursor = null;
            boolean hasApiKey = apiKeyContext != null && apiKeyContext.hasValidApiKey();
            MemoryEpochFilter epochFilter = null;
            Channel effectiveChannel = requestedChannel;
            if (!hasApiKey) {
                effectiveChannel = Channel.HISTORY;
            } else if (effectiveChannel == Channel.MEMORY) {
                if (apiKeyContext.getClientId() == null || apiKeyContext.getClientId().isBlank()) {
                    return forbidden(
                            new AccessDeniedException("Client id is required for memory access"));
                }
                try {
                    epochFilter = MemoryEpochFilter.parse(epoch);
                } catch (IllegalArgumentException e) {
                    return badRequest(e.getMessage());
                }
            }
            PagedEntries context =
                    store().getEntries(
                                    currentUserId(),
                                    conversationId,
                                    after,
                                    pageSize,
                                    effectiveChannel,
                                    epochFilter,
                                    hasApiKey ? apiKeyContext.getClientId() : null,
                                    allForks);
            internal = context.getEntries();
            nextCursor = context.getNextCursor();
            List<Entry> data = internal.stream().map(this::toClientEntry).toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            response.put("nextCursor", nextCursor);
            return Response.ok(response).build();
        } catch (ResourceNotFoundException e) {
            LOG.infof(
                    "Conversation not found when listing messages: conversationId=%s, user=%s",
                    conversationId, currentUserId());
            return notFound(e);
        } catch (AccessDeniedException e) {
            LOG.infof(
                    "Access denied when listing messages: conversationId=%s, user=%s, reason=%s",
                    conversationId, currentUserId(), e.getMessage());
            return forbidden(e);
        } catch (IllegalArgumentException e) {
            // Invalid UUID format - treat as not found
            LOG.infof(
                    "Invalid conversation ID format when listing messages: conversationId=%s,"
                            + " user=%s",
                    conversationId, currentUserId());
            return notFound(new ResourceNotFoundException("conversation", conversationId));
        }
    }

    @POST
    @Path("/conversations/{conversationId}/entries")
    public Response appendEntry(
            @PathParam("conversationId") String conversationId, CreateEntryRequest request) {
        try {
            Entry result;
            if (apiKeyContext != null && apiKeyContext.hasValidApiKey()) {
                String clientId = apiKeyContext.getClientId();
                if (clientId == null || clientId.isBlank()) {
                    return forbidden(
                            new AccessDeniedException("Client id is required for agent messages"));
                }
                // indexedContent is only allowed on history channel
                if (request.getIndexedContent() != null
                        && !request.getIndexedContent().isBlank()
                        && request.getChannel() != CreateEntryRequest.ChannelEnum.HISTORY) {
                    return badRequest("indexedContent is only allowed on history channel");
                }
                // Validate history channel entry format
                Response historyValidationError = validateHistoryEntry(request);
                if (historyValidationError != null) {
                    return historyValidationError;
                }
                // Agents provide fully-typed content and channel directly
                // Epoch is auto-calculated by the store for MEMORY channel entries
                // Rewrite attachmentId → href before persisting so stored content has href
                List<String> rewrittenIds =
                        rewriteAttachmentIds(request.getContent(), currentUserId());
                List<CreateEntryRequest> messages = List.of(request);
                List<EntryDto> appended =
                        store().appendAgentEntries(
                                        currentUserId(), conversationId, messages, clientId, null);
                EntryDto dto =
                        appended != null && !appended.isEmpty()
                                ? appended.get(appended.size() - 1)
                                : null;
                if (dto != null) {
                    linkAttachmentsToEntry(rewrittenIds, dto.getId());
                }
                result = dto != null ? toClientEntry(dto) : null;
            } else {
                // Users cannot set channel to MEMORY - only agents can
                if (request.getChannel() == CreateEntryRequest.ChannelEnum.MEMORY) {
                    return forbidden(
                            new AccessDeniedException(
                                    "Only agents can append messages to the MEMORY channel"));
                }
                // Validate history channel entry format
                Response historyValidationError = validateHistoryEntry(request);
                if (historyValidationError != null) {
                    return historyValidationError;
                }
                // Users: convert CreateEntryRequest to CreateUserEntryRequest
                // Rewrite attachmentId → href before persisting
                List<String> rewrittenIds =
                        rewriteAttachmentIds(request.getContent(), currentUserId());
                String textContent = extractTextFromContent(request.getContent());
                List<Map<String, Object>> attachments =
                        extractAttachmentsFromContent(request.getContent());
                boolean hasText = textContent != null && !textContent.isBlank();
                boolean hasAttachments = attachments != null && !attachments.isEmpty();
                if (!hasText && !hasAttachments) {
                    io.github.chirino.memory.client.model.ErrorResponse error =
                            new io.github.chirino.memory.client.model.ErrorResponse();
                    error.setError("Message content is required");
                    error.setCode("invalid_request");
                    return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
                }
                CreateUserEntryRequest userRequest = new CreateUserEntryRequest();
                userRequest.setContent(hasText ? textContent : null);
                userRequest.setAttachments(hasAttachments ? attachments : null);
                // Note: CreateEntryRequest doesn't have metadata, so we skip it
                EntryDto dto =
                        store().appendUserEntry(currentUserId(), conversationId, userRequest);
                linkAttachmentsToEntry(rewrittenIds, dto.getId());
                result = toClientEntry(dto);
            }
            return Response.status(Response.Status.CREATED).entity(result).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @POST
    @Path("/conversations/{conversationId}/entries/sync")
    public Response syncMemoryEntries(
            @PathParam("conversationId") String conversationId, CreateEntryRequest request) {
        if (apiKeyContext == null || !apiKeyContext.hasValidApiKey()) {
            return forbidden(
                    new AccessDeniedException("Agent API key is required to sync memory messages"));
        }
        String clientId = apiKeyContext.getClientId();
        if (clientId == null || clientId.isBlank()) {
            return forbidden(
                    new AccessDeniedException("Client id is required to sync memory messages"));
        }
        if (request == null) {
            return badRequest("entry request is required");
        }
        if (request.getChannel() == null
                || request.getChannel() != CreateEntryRequest.ChannelEnum.MEMORY) {
            return badRequest("sync entry must target the memory channel");
        }
        try {
            SyncResult result =
                    store().syncAgentEntry(currentUserId(), conversationId, request, clientId);
            Map<String, Object> response = new HashMap<>();
            response.put("epoch", result.getEpoch());
            response.put("noOp", result.isNoOp());
            response.put("epochIncremented", result.isEpochIncremented());
            response.put(
                    "entry", result.getEntry() != null ? toClientEntry(result.getEntry()) : null);
            return Response.ok(response).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @GET
    @Path("/conversations/{conversationId}/memberships")
    public Response listMemberships(@PathParam("conversationId") String conversationId) {
        try {
            List<ConversationMembershipDto> internal =
                    store().listMemberships(currentUserId(), conversationId);
            List<ConversationMembership> data =
                    internal.stream()
                            .map(dto -> toClientConversationMembership(dto, conversationId))
                            .toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            return Response.ok(response).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @POST
    @Path("/conversations/{conversationId}/memberships")
    public Response shareConversation(
            @PathParam("conversationId") String conversationId, ShareConversationRequest request) {
        try {
            io.github.chirino.memory.api.dto.ShareConversationRequest internal =
                    new io.github.chirino.memory.api.dto.ShareConversationRequest();
            internal.setUserId(request.getUserId());
            if (request.getAccessLevel() != null) {
                internal.setAccessLevel(AccessLevel.fromString(request.getAccessLevel().value()));
            }

            ConversationMembershipDto dto =
                    store().shareConversation(currentUserId(), conversationId, internal);
            ConversationMembership result = toClientConversationMembership(dto, conversationId);
            return Response.status(Response.Status.CREATED).entity(result).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @PATCH
    @Path("/conversations/{conversationId}/memberships/{userId}")
    public Response updateMembership(
            @PathParam("conversationId") String conversationId,
            @PathParam("userId") String userId,
            ShareConversationRequest request) {
        try {
            io.github.chirino.memory.api.dto.ShareConversationRequest internal =
                    new io.github.chirino.memory.api.dto.ShareConversationRequest();
            internal.setUserId(request.getUserId());
            if (request.getAccessLevel() != null) {
                internal.setAccessLevel(AccessLevel.fromString(request.getAccessLevel().value()));
            }

            ConversationMembershipDto dto =
                    store().updateMembership(currentUserId(), conversationId, userId, internal);
            ConversationMembership result = toClientConversationMembership(dto, conversationId);
            return Response.ok(result).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @DELETE
    @Path("/conversations/{conversationId}/memberships/{userId}")
    public Response deleteMembership(
            @PathParam("conversationId") String conversationId,
            @PathParam("userId") String userId) {
        try {
            store().deleteMembership(currentUserId(), conversationId, userId);
            return Response.noContent().build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @POST
    @Path("/conversations/{conversationId}/entries/{entryId}/fork")
    public Response forkConversation(
            @PathParam("conversationId") String conversationId,
            @PathParam("entryId") String entryId,
            ForkFromEntryRequest request) {
        try {
            if (request == null) {
                request = new ForkFromEntryRequest();
            }
            io.github.chirino.memory.api.dto.ForkFromEntryRequest internal =
                    new io.github.chirino.memory.api.dto.ForkFromEntryRequest();
            internal.setTitle(request.getTitle());

            ConversationDto dto =
                    store().forkConversationAtEntry(
                                    currentUserId(), conversationId, entryId, internal);
            Conversation result = toClientConversation(dto);
            return Response.status(Response.Status.CREATED).entity(result).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @GET
    @Path("/conversations/{conversationId}/forks")
    public Response listForks(@PathParam("conversationId") String conversationId) {
        try {
            List<ConversationForkSummaryDto> internal =
                    store().listForks(currentUserId(), conversationId);
            List<ConversationForkSummary> data =
                    internal.stream().map(this::toClientConversationForkSummary).toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            return Response.ok(response).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @POST
    @Path("/conversations/index")
    public Response indexConversations(List<IndexEntryRequest> request) {
        try {
            roleResolver.requireIndexer(identity, apiKeyContext);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }

        if (request == null || request.isEmpty()) {
            ErrorResponse error = new ErrorResponse();
            error.setError("Invalid request");
            error.setCode("bad_request");
            error.setDetails(Map.of("message", "At least one entry is required"));
            return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
        }

        // Validate each entry
        for (int i = 0; i < request.size(); i++) {
            IndexEntryRequest entry = request.get(i);
            if (entry.getConversationId() == null) {
                return badRequest("conversationId is required for entry " + i);
            }
            if (entry.getEntryId() == null) {
                return badRequest("entryId is required for entry " + i);
            }
            if (entry.getIndexedContent() == null || entry.getIndexedContent().isBlank()) {
                return badRequest("indexedContent is required for entry " + i);
            }
        }

        try {
            // Convert client model to internal DTO
            List<io.github.chirino.memory.api.dto.IndexEntryRequest> internalEntries =
                    request.stream()
                            .map(
                                    e -> {
                                        var internal =
                                                new io.github.chirino.memory.api.dto
                                                        .IndexEntryRequest();
                                        internal.setConversationId(
                                                e.getConversationId().toString());
                                        internal.setEntryId(e.getEntryId().toString());
                                        internal.setIndexedContent(e.getIndexedContent());
                                        return internal;
                                    })
                            .toList();

            io.github.chirino.memory.api.dto.IndexConversationsResponse dto =
                    store().indexEntries(internalEntries);

            LOG.infof("Indexed %d entries", dto.getIndexed());

            IndexConversationsResponse result = new IndexConversationsResponse();
            result.setIndexed(dto.getIndexed());
            return Response.ok(result).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @GET
    @Path("/conversations/unindexed")
    public Response listUnindexedEntries(
            @QueryParam("limit") Integer limit, @QueryParam("cursor") String cursor) {
        try {
            roleResolver.requireIndexer(identity, apiKeyContext);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }

        int effectiveLimit = limit != null && limit > 0 ? limit : 100;

        io.github.chirino.memory.api.dto.UnindexedEntriesResponse dto =
                store().listUnindexedEntries(effectiveLimit, cursor);

        // Convert to client model
        UnindexedEntriesResponse result = new UnindexedEntriesResponse();
        result.setCursor(dto.getCursor());
        if (dto.getData() != null) {
            result.setData(
                    dto.getData().stream()
                            .map(
                                    e -> {
                                        var ue =
                                                new io.github.chirino.memory.client.model
                                                        .UnindexedEntriesResponseDataInner();
                                        ue.setConversationId(
                                                UUID.fromString(e.getConversationId()));
                                        ue.setEntry(toClientEntry(e.getEntry()));
                                        return ue;
                                    })
                            .toList());
        }
        return Response.ok(result).build();
    }

    private Response badRequest(String message) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Invalid request");
        error.setCode("bad_request");
        error.setDetails(Map.of("message", message));
        return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
    }

    /**
     * Validates that history channel entries use the correct contentType and content structure.
     * Returns null if valid, or an error Response if invalid.
     *
     * <p>Supported content types:
     * <ul>
     *   <li>{@code history} - simple text-only history</li>
     *   <li>{@code history/lc4j} - LangChain4j event format</li>
     *   <li>{@code history/*} - other subtypes for future frameworks</li>
     * </ul>
     *
     * <p>History content blocks support:
     * <ul>
     *   <li>{@code role} (required): "USER" or "AI"</li>
     *   <li>{@code text} (optional): the message text</li>
     *   <li>{@code events} (optional): array of event objects (structure not validated)</li>
     *   <li>{@code attachments} (optional): array of attachment objects with href and contentType</li>
     * </ul>
     *
     * <p>At least one of {@code text}, {@code events}, or {@code attachments} must be present.
     */
    private Response validateHistoryEntry(CreateEntryRequest request) {
        // Only validate history channel entries
        if (request.getChannel() != CreateEntryRequest.ChannelEnum.HISTORY) {
            return null;
        }

        // History channel entries must use "history" or "history/*" contentType
        String contentType = request.getContentType();
        if (contentType == null
                || (!contentType.equals("history") && !contentType.startsWith("history/"))) {
            return badRequest(
                    "History channel entries must use 'history' or 'history/<subtype>' as the"
                            + " contentType");
        }

        // Content must contain exactly 1 object
        List<Object> content = request.getContent();
        if (content == null || content.size() != 1) {
            return badRequest("History channel entries must contain exactly 1 content object");
        }

        // The object must have role and at least one of text, events, or attachments
        Object block = content.get(0);
        if (!(block instanceof Map)) {
            return badRequest(
                    "History channel content must be an object with 'role' and at least one of"
                            + " 'text', 'events', or 'attachments'");
        }

        @SuppressWarnings("unchecked")
        Map<String, Object> blockMap = (Map<String, Object>) block;

        Object role = blockMap.get("role");
        if (role == null || (!"USER".equals(role) && !"AI".equals(role))) {
            return badRequest(
                    "History channel content must have a 'role' field with value 'USER' or 'AI'");
        }

        // Check for text, events, and attachments - at least one must be present
        boolean hasText = blockMap.containsKey("text") && blockMap.get("text") != null;
        boolean hasEvents = blockMap.containsKey("events") && blockMap.get("events") != null;
        boolean hasAttachments =
                blockMap.containsKey("attachments") && blockMap.get("attachments") != null;

        if (!hasText && !hasEvents && !hasAttachments) {
            return badRequest(
                    "History channel content must have at least one of 'text', 'events', or"
                            + " 'attachments'");
        }

        // Validate events is an array if present (no validation of individual event structure)
        if (hasEvents) {
            Object events = blockMap.get("events");
            if (!(events instanceof List)) {
                return badRequest("History channel 'events' field must be an array");
            }
        }

        // Validate attachments structure if present
        if (hasAttachments) {
            Object attachments = blockMap.get("attachments");
            if (!(attachments instanceof List)) {
                return badRequest("History channel 'attachments' field must be an array");
            }
            @SuppressWarnings("unchecked")
            List<Object> attachmentList = (List<Object>) attachments;
            for (int i = 0; i < attachmentList.size(); i++) {
                Object att = attachmentList.get(i);
                if (!(att instanceof Map)) {
                    return badRequest(
                            "History channel attachment at index " + i + " must be an object");
                }
                @SuppressWarnings("unchecked")
                Map<String, Object> attMap = (Map<String, Object>) att;
                boolean hasHref =
                        attMap.get("href") != null && attMap.get("href") instanceof String;
                boolean hasAttachmentId =
                        attMap.get("attachmentId") != null
                                && attMap.get("attachmentId") instanceof String;
                if (!hasHref && !hasAttachmentId) {
                    return badRequest(
                            "History channel attachment at index "
                                    + i
                                    + " must have an 'href' or 'attachmentId' field");
                }
                // contentType is required for href attachments, optional for attachmentId
                // (it's already stored on the attachment record)
                if (hasHref
                        && (attMap.get("contentType") == null
                                || !(attMap.get("contentType") instanceof String))) {
                    return badRequest(
                            "History channel attachment at index "
                                    + i
                                    + " must have a 'contentType' field");
                }
            }
        }

        return null;
    }

    @DELETE
    @Path("/conversations/{conversationId}/response")
    public Response cancelResponse(@PathParam("conversationId") String conversationId) {
        ResponseResumerBackend backend = resumerSelector.getBackend();
        if (!backend.enabled()) {
            return conflict("Response resumer is not enabled");
        }

        try {
            ConversationDto conversation = store().getConversation(currentUserId(), conversationId);
            if (!hasWriterAccess(conversation)) {
                throw new AccessDeniedException("User does not have WRITER access to conversation");
            }
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }

        try {
            backend.requestCancel(conversationId, resolveAdvertisedAddress());
        } catch (ResponseResumerRedirectException redirect) {
            LOG.infof(
                    "Redirecting delete-response for conversationId=%s from %s to %s",
                    conversationId, resolveAdvertisedAddress(), redirect.target());
            URI target = buildRedirectLocation(conversationId, redirect.target());
            return forwardCancel(conversationId, target);
        }
        waitForResponseCompletion(conversationId, backend, Duration.ofSeconds(30));
        return Response.ok().build();
    }

    private Response notFound(ResourceNotFoundException e) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Not found");
        error.setCode("not_found");
        error.setDetails(Map.of("resource", e.getResource(), "id", e.getId()));
        return Response.status(Response.Status.NOT_FOUND).entity(error).build();
    }

    private Response forbidden(AccessDeniedException e) {
        LOG.infof("Access denied for user=%s: %s", currentUserId(), e.getMessage());
        ErrorResponse error = new ErrorResponse();
        error.setError("Forbidden");
        error.setCode("forbidden");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(Response.Status.FORBIDDEN).entity(error).build();
    }

    private Response conflict(String message) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Conflict");
        error.setCode("conflict");
        error.setDetails(Map.of("message", message));
        return Response.status(Response.Status.CONFLICT).entity(error).build();
    }

    private boolean hasWriterAccess(ConversationDto conversation) {
        AccessLevel level = conversation.getAccessLevel();
        return level == AccessLevel.WRITER
                || level == AccessLevel.MANAGER
                || level == AccessLevel.OWNER;
    }

    private void waitForResponseCompletion(
            String conversationId, ResponseResumerBackend backend, Duration timeout) {
        long deadline = System.nanoTime() + timeout.toNanos();
        while (System.nanoTime() < deadline) {
            if (!backend.hasResponseInProgress(conversationId)) {
                return;
            }
            try {
                Thread.sleep(200);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return;
            }
        }
    }

    private AdvertisedAddress resolveAdvertisedAddress() {
        Optional<AdvertisedAddress> configured =
                advertisedAddress.flatMap(AdvertisedAddress::parse);
        if (configured.isPresent()) {
            return configured.get();
        }

        if (routingContext != null) {
            SocketAddress localAddress = routingContext.request().localAddress();
            if (localAddress != null) {
                String host = localAddress.host();
                int port = localAddress.port();
                if (host != null
                        && !host.isBlank()
                        && !"0.0.0.0".equals(host)
                        && !"::".equals(host)) {
                    Optional<AdvertisedAddress> fromLocal =
                            AdvertisedAddress.fromHostAndPort(host, Integer.toString(port));
                    if (fromLocal.isPresent()) {
                        return fromLocal.get();
                    }
                }
            }
        }

        if (uriInfo != null && uriInfo.getBaseUri() != null) {
            String host = uriInfo.getBaseUri().getHost();
            int port = uriInfo.getBaseUri().getPort();
            if (port <= 0) {
                port = uriInfo.getBaseUri().getScheme().equalsIgnoreCase("https") ? 443 : 80;
            }
            if (host != null && !host.isBlank()) {
                Optional<AdvertisedAddress> fromBase =
                        AdvertisedAddress.fromHostAndPort(host, Integer.toString(port));
                if (fromBase.isPresent()) {
                    return fromBase.get();
                }
            }
        }

        return new AdvertisedAddress("localhost", 0);
    }

    private String headerValue(String name) {
        if (httpHeaders == null) {
            return null;
        }
        String value = httpHeaders.getHeaderString(name);
        if (value == null || value.isBlank()) {
            return null;
        }
        int comma = value.indexOf(',');
        if (comma > 0) {
            value = value.substring(0, comma);
        }
        return value.trim();
    }

    private java.net.URI buildRedirectLocation(String conversationId, AdvertisedAddress target) {
        if (target == null) {
            return uriInfo.getRequestUri();
        }
        UriBuilder builder = UriBuilder.fromUri(uriInfo.getBaseUri());
        builder.host(target.host());
        if (target.port() > 0) {
            builder.port(target.port());
        }
        builder.path("v1/conversations/{conversationId}/response");
        return builder.build(conversationId);
    }

    private Response forwardCancel(String conversationId, URI target) {
        URI current = target;
        for (int i = 0; i < MAX_REDIRECTS; i++) {
            HttpResponse<String> response = sendForwardedCancel(current);
            int status = response.statusCode();
            if (status == Response.Status.TEMPORARY_REDIRECT.getStatusCode()
                    || status == Response.Status.PERMANENT_REDIRECT.getStatusCode()) {
                String location = response.headers().firstValue("Location").orElse(null);
                if (location == null || location.isBlank()) {
                    return Response.status(status).build();
                }
                current = URI.create(location);
                continue;
            }
            Response.ResponseBuilder builder = Response.status(status);
            String body = response.body();
            if (body != null && !body.isBlank()) {
                builder.entity(body);
            }
            return builder.build();
        }
        LOG.warnf(
                "Cancel-response redirect loop for conversationId=%s at %s",
                conversationId, current);
        return Response.status(Response.Status.BAD_GATEWAY)
                .entity("Failed to cancel response due to redirect loop")
                .build();
    }

    private HttpResponse<String> sendForwardedCancel(URI target) {
        try {
            HttpRequest.Builder builder =
                    HttpRequest.newBuilder(target).POST(HttpRequest.BodyPublishers.noBody());
            String authHeader = headerValue(HttpHeaders.AUTHORIZATION);
            if (authHeader != null) {
                builder.header(HttpHeaders.AUTHORIZATION, authHeader);
            }
            String cookieHeader = headerValue(HttpHeaders.COOKIE);
            if (cookieHeader != null) {
                builder.header(HttpHeaders.COOKIE, cookieHeader);
            }
            return HttpClient.newHttpClient()
                    .send(builder.build(), HttpResponse.BodyHandlers.ofString());
        } catch (IOException | InterruptedException e) {
            if (e instanceof InterruptedException) {
                Thread.currentThread().interrupt();
            }
            throw new RuntimeException("Failed to forward cancel response", e);
        }
    }

    private ConversationSummary toClientConversationSummary(ConversationSummaryDto dto) {
        if (dto == null) {
            return null;
        }
        ConversationSummary result = new ConversationSummary();
        result.setId(dto.getId() != null ? UUID.fromString(dto.getId()) : null);
        result.setTitle(dto.getTitle());
        result.setOwnerUserId(dto.getOwnerUserId());
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        result.setUpdatedAt(parseDate(dto.getUpdatedAt()));
        result.setLastMessagePreview(dto.getLastMessagePreview());
        if (dto.getAccessLevel() != null) {
            result.setAccessLevel(
                    ConversationSummary.AccessLevelEnum.fromString(
                            dto.getAccessLevel().name().toLowerCase()));
        }
        return result;
    }

    private Conversation toClientConversation(ConversationDto dto) {
        if (dto == null) {
            return null;
        }
        Conversation result = new Conversation();
        result.setId(dto.getId() != null ? UUID.fromString(dto.getId()) : null);
        result.setTitle(dto.getTitle());
        result.setOwnerUserId(dto.getOwnerUserId());
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        result.setUpdatedAt(parseDate(dto.getUpdatedAt()));
        result.setLastMessagePreview(dto.getLastMessagePreview());
        if (dto.getAccessLevel() != null) {
            result.setAccessLevel(
                    Conversation.AccessLevelEnum.fromString(
                            dto.getAccessLevel().name().toLowerCase()));
        }
        // conversationGroupId is not exposed in API responses
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

    private ConversationMembership toClientConversationMembership(
            ConversationMembershipDto dto, String conversationId) {
        if (dto == null) {
            return null;
        }
        ConversationMembership result = new ConversationMembership();
        result.setConversationId(conversationId != null ? UUID.fromString(conversationId) : null);
        result.setUserId(dto.getUserId());
        if (dto.getAccessLevel() != null) {
            result.setAccessLevel(
                    ConversationMembership.AccessLevelEnum.fromString(
                            dto.getAccessLevel().name().toLowerCase()));
        }
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        return result;
    }

    private ConversationForkSummary toClientConversationForkSummary(
            ConversationForkSummaryDto dto) {
        if (dto == null) {
            return null;
        }
        ConversationForkSummary result = new ConversationForkSummary();
        result.setConversationId(
                dto.getConversationId() != null ? UUID.fromString(dto.getConversationId()) : null);
        // conversationGroupId is not exposed in API responses
        result.setForkedAtEntryId(
                dto.getForkedAtEntryId() != null
                        ? UUID.fromString(dto.getForkedAtEntryId())
                        : null);
        result.setForkedAtConversationId(
                dto.getForkedAtConversationId() != null
                        ? UUID.fromString(dto.getForkedAtConversationId())
                        : null);
        result.setTitle(dto.getTitle());
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        return result;
    }

    private Entry toClientEntry(EntryDto dto) {
        if (dto == null) {
            return null;
        }
        Entry result = new Entry();
        result.setId(dto.getId() != null ? UUID.fromString(dto.getId()) : null);
        result.setConversationId(
                dto.getConversationId() != null ? UUID.fromString(dto.getConversationId()) : null);
        result.setUserId(dto.getUserId());
        if (dto.getChannel() != null) {
            result.setChannel(Entry.ChannelEnum.fromString(dto.getChannel().toValue()));
        }
        result.setEpoch(dto.getEpoch());
        result.setContentType(dto.getContentType());
        if (dto.getContent() != null) {
            result.setContent(dto.getContent());
        }
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        return result;
    }

    /**
     * Rewrites attachmentId references to href URLs in the content, before persisting.
     * Returns the list of attachment IDs that were rewritten.
     */
    @SuppressWarnings("unchecked")
    private List<String> rewriteAttachmentIds(List<Object> content, String userId) {
        List<String> rewrittenIds = new ArrayList<>();
        if (content == null) {
            return rewrittenIds;
        }
        AttachmentStore attStore = attachmentStoreSelector.getStore();
        for (Object block : content) {
            if (!(block instanceof Map<?, ?> map)) {
                continue;
            }
            Object attachments = map.get("attachments");
            if (!(attachments instanceof List<?> list)) {
                continue;
            }
            for (Object att : list) {
                if (!(att instanceof Map<?, ?> attMap)) {
                    continue;
                }
                Object attachmentIdObj = attMap.get("attachmentId");
                if (attachmentIdObj instanceof String attachmentId) {
                    var optAtt = attStore.findByIdForUser(attachmentId, userId);
                    if (optAtt.isPresent()) {
                        @SuppressWarnings("unchecked")
                        Map<String, Object> mutableAttMap = (Map<String, Object>) attMap;
                        mutableAttMap.put("href", "/v1/attachments/" + attachmentId);
                        mutableAttMap.remove("attachmentId");
                        var record = optAtt.get();
                        if (!mutableAttMap.containsKey("contentType")) {
                            mutableAttMap.put("contentType", record.contentType());
                        }
                        if (!mutableAttMap.containsKey("name") && record.filename() != null) {
                            mutableAttMap.put("name", record.filename());
                        }
                        rewrittenIds.add(attachmentId);
                    }
                }
            }
        }
        return rewrittenIds;
    }

    /** Links attachment records to the given entry after it has been persisted. */
    private void linkAttachmentsToEntry(List<String> attachmentIds, String entryId) {
        if (attachmentIds.isEmpty() || entryId == null) {
            return;
        }
        AttachmentStore attStore = attachmentStoreSelector.getStore();
        for (String attachmentId : attachmentIds) {
            attStore.linkToEntry(attachmentId, entryId);
        }
    }

    @SuppressWarnings("unchecked")
    private String extractTextFromContent(List<Object> content) {
        if (content == null) {
            return null;
        }
        for (Object block : content) {
            if (block == null) {
                continue;
            }
            if (block instanceof Map<?, ?> map) {
                Object text = map.get("text");
                if (text instanceof String s && !s.isBlank()) {
                    return s;
                }
            } else if (block instanceof String s && !s.isBlank()) {
                return s;
            }
        }
        return null;
    }

    @SuppressWarnings("unchecked")
    private List<Map<String, Object>> extractAttachmentsFromContent(List<Object> content) {
        if (content == null) {
            return null;
        }
        for (Object block : content) {
            if (block instanceof Map<?, ?> map) {
                Object attachments = map.get("attachments");
                if (attachments instanceof List<?> list && !list.isEmpty()) {
                    return (List<Map<String, Object>>) (List<?>) list;
                }
            }
        }
        return null;
    }

    private OffsetDateTime parseDate(String value) {
        if (value == null) {
            return null;
        }
        return OffsetDateTime.parse(value, ISO_FORMATTER);
    }
}
