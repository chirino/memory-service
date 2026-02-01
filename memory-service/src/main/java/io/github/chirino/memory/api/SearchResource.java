package io.github.chirino.memory.api;

import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.client.model.Entry;
import io.github.chirino.memory.client.model.ErrorResponse;
import io.github.chirino.memory.client.model.SearchConversationsRequest;
import io.github.chirino.memory.client.model.SearchResult;
import io.github.chirino.memory.config.VectorStoreSelector;
import io.github.chirino.memory.model.Channel;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.github.chirino.memory.vector.VectorStore;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.time.OffsetDateTime;
import java.time.format.DateTimeFormatter;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;

@Path("/v1")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class SearchResource {

    private static final DateTimeFormatter ISO_FORMATTER = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    @Inject VectorStoreSelector vectorStoreSelector;

    @Inject SecurityIdentity identity;

    private VectorStore vectorStore() {
        return vectorStoreSelector.getVectorStore();
    }

    private String currentUserId() {
        return identity.getPrincipal().getName();
    }

    @POST
    @Path("/conversations/search")
    public Response searchConversations(SearchConversationsRequest request) {
        VectorStore vectorStore = vectorStore();
        if (vectorStore == null || !vectorStore.isEnabled()) {
            return vectorStoreUnavailable();
        }
        try {
            io.github.chirino.memory.api.dto.SearchEntriesRequest internal =
                    new io.github.chirino.memory.api.dto.SearchEntriesRequest();
            internal.setQuery(request.getQuery());
            internal.setLimit(request.getLimit());
            internal.setAfter(request.getAfter());
            internal.setIncludeEntry(request.getIncludeEntry());
            internal.setGroupByConversation(request.getGroupByConversation());

            SearchResultsDto internalResults = vectorStore.search(currentUserId(), internal);
            List<SearchResult> data =
                    internalResults.getResults().stream()
                            .map(dto -> toClientSearchResult(dto, internal.getIncludeEntry()))
                            .toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            response.put("nextCursor", internalResults.getNextCursor());
            return Response.ok(response).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    private Response vectorStoreUnavailable() {
        ErrorResponse error = new ErrorResponse();
        error.setError("Vector search not available");
        error.setCode("vector_store_disabled");
        error.setDetails(Map.of("message", "Enable a vector store to use semantic search."));
        return Response.status(Response.Status.NOT_IMPLEMENTED).entity(error).build();
    }

    private Response notFound(ResourceNotFoundException e) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Not found");
        error.setCode("not_found");
        error.setDetails(Map.of("resource", e.getResource(), "id", e.getId()));
        return Response.status(Response.Status.NOT_FOUND).entity(error).build();
    }

    private Response forbidden(AccessDeniedException e) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Forbidden");
        error.setCode("forbidden");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(Response.Status.FORBIDDEN).entity(error).build();
    }

    private SearchResult toClientSearchResult(SearchResultDto dto, Boolean includeEntry) {
        if (dto == null) {
            return null;
        }
        SearchResult result = new SearchResult();
        result.setConversationId(
                dto.getConversationId() != null ? UUID.fromString(dto.getConversationId()) : null);
        result.setConversationTitle(dto.getConversationTitle());
        result.setEntryId(dto.getEntryId() != null ? UUID.fromString(dto.getEntryId()) : null);
        result.setScore((float) dto.getScore());
        result.setHighlights(dto.getHighlights());
        if (includeEntry == null || includeEntry) {
            result.setEntry(toClientEntry(dto.getEntry()));
        }
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
        Channel channel = dto.getChannel();
        if (channel != null) {
            result.setChannel(Entry.ChannelEnum.fromString(channel.toValue()));
        }
        result.setEpoch(dto.getEpoch());
        result.setContentType(dto.getContentType());
        if (dto.getContent() != null) {
            result.setContent(dto.getContent());
        }
        if (dto.getCreatedAt() != null) {
            result.setCreatedAt(OffsetDateTime.parse(dto.getCreatedAt(), ISO_FORMATTER));
        }
        return result;
    }
}
