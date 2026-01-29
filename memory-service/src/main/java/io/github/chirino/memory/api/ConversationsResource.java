package io.github.chirino.memory.api;

import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.CreateUserEntryRequest;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.PagedEntries;
import io.github.chirino.memory.api.dto.SyncResult;
import io.github.chirino.memory.client.model.Conversation;
import io.github.chirino.memory.client.model.ConversationForkSummary;
import io.github.chirino.memory.client.model.ConversationMembership;
import io.github.chirino.memory.client.model.ConversationSummary;
import io.github.chirino.memory.client.model.CreateConversationRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.Entry;
import io.github.chirino.memory.client.model.ErrorResponse;
import io.github.chirino.memory.client.model.ForkFromEntryRequest;
import io.github.chirino.memory.client.model.IndexTranscriptRequest;
import io.github.chirino.memory.client.model.ShareConversationRequest;
import io.github.chirino.memory.client.model.SyncEntriesRequest;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.model.Channel;
import io.github.chirino.memory.resumer.AdvertisedAddress;
import io.github.chirino.memory.resumer.ResponseResumerBackend;
import io.github.chirino.memory.resumer.ResponseResumerRedirectException;
import io.github.chirino.memory.resumer.ResponseResumerSelector;
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
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
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

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

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
            @QueryParam("epoch") String epoch) {
        LOG.infof(
                "Listing messages for conversationId=%s, user=%s, after=%s, limit=%s,"
                        + " channel=%s",
                conversationId, currentUserId(), after, limit, channel);
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
                                    hasApiKey ? apiKeyContext.getClientId() : null);
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
                // Agents provide fully-typed content and channel/epoch directly
                List<CreateEntryRequest> messages = List.of(request);
                List<EntryDto> appended =
                        store().appendAgentEntries(
                                        currentUserId(), conversationId, messages, clientId);
                EntryDto dto =
                        appended != null && !appended.isEmpty()
                                ? appended.get(appended.size() - 1)
                                : null;
                result = dto != null ? toClientEntry(dto) : null;
            } else {
                // Users cannot set channel to MEMORY - only agents can
                if (request.getChannel() == CreateEntryRequest.ChannelEnum.MEMORY) {
                    return forbidden(
                            new AccessDeniedException(
                                    "Only agents can append messages to the MEMORY channel"));
                }
                // Users: convert CreateEntryRequest to CreateUserEntryRequest
                String textContent = extractTextFromContent(request.getContent());
                if (textContent == null || textContent.isBlank()) {
                    io.github.chirino.memory.client.model.ErrorResponse error =
                            new io.github.chirino.memory.client.model.ErrorResponse();
                    error.setError("Message content is required");
                    error.setCode("invalid_request");
                    return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
                }
                CreateUserEntryRequest userRequest = new CreateUserEntryRequest();
                userRequest.setContent(textContent);
                // Note: CreateEntryRequest doesn't have metadata, so we skip it
                EntryDto dto =
                        store().appendUserEntry(currentUserId(), conversationId, userRequest);
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
            @PathParam("conversationId") String conversationId, SyncEntriesRequest request) {
        if (apiKeyContext == null || !apiKeyContext.hasValidApiKey()) {
            return forbidden(
                    new AccessDeniedException("Agent API key is required to sync memory messages"));
        }
        String clientId = apiKeyContext.getClientId();
        if (clientId == null || clientId.isBlank()) {
            return forbidden(
                    new AccessDeniedException("Client id is required to sync memory messages"));
        }
        if (request == null || request.getEntries() == null || request.getEntries().isEmpty()) {
            return badRequest("messages are required");
        }
        for (CreateEntryRequest message : request.getEntries()) {
            if (message == null
                    || message.getChannel() == null
                    || message.getChannel() != CreateEntryRequest.ChannelEnum.MEMORY) {
                return badRequest("all sync messages must target the memory channel");
            }
        }
        try {
            SyncResult result =
                    store().syncAgentEntries(
                                    currentUserId(),
                                    conversationId,
                                    request.getEntries(),
                                    clientId);
            Map<String, Object> response = new HashMap<>();
            response.put("epoch", result.getEpoch());
            response.put("noOp", result.isNoOp());
            response.put("epochIncremented", result.isEpochIncremented());
            List<Entry> data = result.getEntries().stream().map(this::toClientEntry).toList();
            response.put("entries", data);
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
    @Path("/conversations/{conversationId}/forks")
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
    @Path("/conversations/{conversationId}/transfer-ownership")
    public Response transferOwnership(
            @PathParam("conversationId") String conversationId, Map<String, String> body) {
        try {
            String newOwnerUserId = body.get("newOwnerUserId");
            store().requestOwnershipTransfer(currentUserId(), conversationId, newOwnerUserId);
            return Response.status(Response.Status.ACCEPTED).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @POST
    @Path("/conversations/index")
    public Response indexConversationTranscript(IndexTranscriptRequest request) {
        if (apiKeyContext == null || !apiKeyContext.hasValidApiKey()) {
            return forbidden(
                    new AccessDeniedException("Agent API key is required to index transcripts"));
        }
        String clientId = apiKeyContext.getClientId();
        if (clientId == null || clientId.isBlank()) {
            return forbidden(
                    new AccessDeniedException("Client id is required to index transcripts"));
        }
        if (request == null) {
            ErrorResponse error = new ErrorResponse();
            error.setError("Invalid request");
            error.setCode("bad_request");
            error.setDetails(Map.of("message", "Index request body is required"));
            return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
        }
        if (request.getConversationId() == null || request.getConversationId().isBlank()) {
            ErrorResponse error = new ErrorResponse();
            error.setError("Invalid request");
            error.setCode("bad_request");
            error.setDetails(Map.of("message", "conversationId is required"));
            return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
        }
        if (request.getTranscript() == null || request.getTranscript().isBlank()) {
            ErrorResponse error = new ErrorResponse();
            error.setError("Invalid request");
            error.setCode("bad_request");
            error.setDetails(Map.of("message", "Transcript text is required"));
            return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
        }
        if (request.getUntilEntryId() == null || request.getUntilEntryId().isBlank()) {
            ErrorResponse error = new ErrorResponse();
            error.setError("Invalid request");
            error.setCode("bad_request");
            error.setDetails(Map.of("message", "untilEntryId is required"));
            return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
        }
        String conversationId = request.getConversationId();
        String untilEntryId = request.getUntilEntryId();
        LOG.infof(
                "Indexing transcript for conversationId=%s, untilEntryId=%s",
                conversationId, untilEntryId);
        try {
            io.github.chirino.memory.api.dto.IndexTranscriptRequest internal =
                    new io.github.chirino.memory.api.dto.IndexTranscriptRequest();
            internal.setConversationId(request.getConversationId());
            internal.setTitle(request.getTitle());
            internal.setTranscript(request.getTranscript());
            internal.setUntilEntryId(request.getUntilEntryId());

            EntryDto dto = store().indexTranscript(internal, clientId);
            LOG.infof(
                    "Successfully indexed transcript for conversationId=%s, entryId=%s",
                    conversationId, dto.getId());
            Entry result = toClientEntry(dto);
            return Response.status(Response.Status.CREATED).entity(result).build();
        } catch (ResourceNotFoundException e) {
            LOG.infof("Conversation not found: conversationId=%s", conversationId);
            return notFound(e);
        } catch (AccessDeniedException e) {
            LOG.infof("Access denied for conversationId=%s", conversationId);
            return forbidden(e);
        }
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

    private Response badRequest(String message) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Bad request");
        error.setCode("bad_request");
        error.setDetails(Map.of("message", message));
        return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
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
        result.setId(dto.getId());
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
        result.setId(dto.getId());
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
        result.setForkedAtEntryId(dto.getForkedAtEntryId());
        result.setForkedAtConversationId(dto.getForkedAtConversationId());
        return result;
    }

    private ConversationMembership toClientConversationMembership(
            ConversationMembershipDto dto, String conversationId) {
        if (dto == null) {
            return null;
        }
        ConversationMembership result = new ConversationMembership();
        result.setConversationId(conversationId);
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
        result.setConversationId(dto.getConversationId());
        // conversationGroupId is not exposed in API responses
        result.setForkedAtEntryId(dto.getForkedAtEntryId());
        result.setForkedAtConversationId(dto.getForkedAtConversationId());
        result.setTitle(dto.getTitle());
        result.setCreatedAt(parseDate(dto.getCreatedAt()));
        return result;
    }

    private Entry toClientEntry(EntryDto dto) {
        if (dto == null) {
            return null;
        }
        Entry result = new Entry();
        result.setId(dto.getId());
        result.setConversationId(dto.getConversationId());
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

    private OffsetDateTime parseDate(String value) {
        if (value == null) {
            return null;
        }
        return OffsetDateTime.parse(value, ISO_FORMATTER);
    }
}
