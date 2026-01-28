package io.github.chirino.memory.api;

import io.github.chirino.memory.api.dto.MessageDto;
import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.client.model.ErrorResponse;
import io.github.chirino.memory.client.model.Message;
import io.github.chirino.memory.client.model.SearchMessagesRequest;
import io.github.chirino.memory.client.model.SearchResult;
import io.github.chirino.memory.config.VectorStoreSelector;
import io.github.chirino.memory.model.MessageChannel;
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

@Path("/v1/user/search")
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
    @Path("/messages")
    public Response searchMessages(SearchMessagesRequest request) {
        VectorStore vectorStore = vectorStore();
        if (vectorStore == null || !vectorStore.isEnabled()) {
            return vectorStoreUnavailable();
        }
        try {
            io.github.chirino.memory.api.dto.SearchMessagesRequest internal =
                    new io.github.chirino.memory.api.dto.SearchMessagesRequest();
            internal.setQuery(request.getQuery());
            internal.setTopK(request.getTopK());
            internal.setConversationIds(request.getConversationIds());
            internal.setBefore(request.getBefore());

            List<SearchResultDto> internalResults = vectorStore.search(currentUserId(), internal);
            List<SearchResult> data =
                    internalResults.stream().map(this::toClientSearchResult).toList();
            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
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

    private SearchResult toClientSearchResult(SearchResultDto dto) {
        if (dto == null) {
            return null;
        }
        SearchResult result = new SearchResult();
        result.setMessage(toClientMessage(dto.getMessage()));
        result.setScore((float) dto.getScore());
        result.setHighlights(dto.getHighlights());
        return result;
    }

    private Message toClientMessage(MessageDto dto) {
        if (dto == null) {
            return null;
        }
        Message result = new Message();
        result.setId(dto.getId());
        result.setConversationId(dto.getConversationId());
        result.setUserId(dto.getUserId());
        MessageChannel channel = dto.getChannel();
        if (channel != null) {
            result.setChannel(Message.ChannelEnum.fromString(channel.toValue()));
        }
        result.setEpoch(dto.getEpoch());
        if (dto.getContent() != null) {
            result.setContent(dto.getContent());
        }
        if (dto.getCreatedAt() != null) {
            result.setCreatedAt(OffsetDateTime.parse(dto.getCreatedAt(), ISO_FORMATTER));
        }
        return result;
    }
}
