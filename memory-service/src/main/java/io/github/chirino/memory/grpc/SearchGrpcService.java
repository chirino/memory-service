package io.github.chirino.memory.grpc;

import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.config.VectorStoreSelector;
import io.github.chirino.memory.grpc.v1.CreateSummaryRequest;
import io.github.chirino.memory.grpc.v1.Message;
import io.github.chirino.memory.grpc.v1.SearchMessagesRequest;
import io.github.chirino.memory.grpc.v1.SearchMessagesResponse;
import io.github.chirino.memory.grpc.v1.SearchService;
import io.github.chirino.memory.vector.VectorStore;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import java.util.ArrayList;
import java.util.List;
import java.util.stream.Collectors;

@GrpcService
@Blocking
public class SearchGrpcService extends AbstractGrpcService implements SearchService {

    private final VectorStoreSelector vectorStoreSelector;

    public SearchGrpcService(VectorStoreSelector vectorStoreSelector) {
        this.vectorStoreSelector = vectorStoreSelector;
    }

    @Override
    public Uni<SearchMessagesResponse> searchMessages(SearchMessagesRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            if (request.getQuery() == null || request.getQuery().isBlank()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("query is required")
                                        .asRuntimeException();
                            }
                            VectorStore vectorStore = vectorStoreSelector.getVectorStore();
                            if (vectorStore == null || !vectorStore.isEnabled()) {
                                throw Status.UNIMPLEMENTED
                                        .withDescription("Vector store is not available")
                                        .asRuntimeException();
                            }
                            io.github.chirino.memory.api.dto.SearchMessagesRequest internal =
                                    new io.github.chirino.memory.api.dto.SearchMessagesRequest();
                            internal.setQuery(request.getQuery());
                            internal.setTopK(request.getTopK() > 0 ? request.getTopK() : 20);
                            if (!request.getConversationIdsList().isEmpty()) {
                                internal.setConversationIds(
                                        new ArrayList<>(request.getConversationIdsList()));
                            }
                            if (request.getBefore() != null && !request.getBefore().isBlank()) {
                                internal.setBefore(request.getBefore());
                            }
                            List<SearchResultDto> internalResults =
                                    vectorStore.search(currentUserId(), internal);
                            return SearchMessagesResponse.newBuilder()
                                    .addAllResults(
                                            internalResults.stream()
                                                    .map(GrpcDtoMapper::toProto)
                                                    .collect(Collectors.toList()))
                                    .build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Message> createSummary(CreateSummaryRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            if (!hasValidApiKey()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Agent API key is required to create summaries")
                                        .asRuntimeException();
                            }
                            StatusRuntimeException validation = validateSummaryRequest(request);
                            if (validation != null) {
                                throw validation;
                            }
                            String clientId = currentClientId();
                            if (clientId == null || clientId.isBlank()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Client id is required to create summaries")
                                        .asRuntimeException();
                            }
                            io.github.chirino.memory.api.dto.CreateSummaryRequest internal =
                                    new io.github.chirino.memory.api.dto.CreateSummaryRequest();
                            internal.setTitle(request.getTitle());
                            internal.setSummary(request.getSummary());
                            internal.setUntilMessageId(request.getUntilMessageId());
                            internal.setSummarizedAt(request.getSummarizedAt());
                            io.github.chirino.memory.api.dto.MessageDto dto =
                                    store().createSummary(
                                                    request.getConversationId(),
                                                    internal,
                                                    clientId);
                            return GrpcDtoMapper.toProto(dto);
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    private StatusRuntimeException validateSummaryRequest(CreateSummaryRequest request) {
        if (request.getConversationId() == null || request.getConversationId().isBlank()) {
            return Status.INVALID_ARGUMENT
                    .withDescription("conversationId is required")
                    .asRuntimeException();
        }
        if (request.getTitle() == null || request.getTitle().isBlank()) {
            return Status.INVALID_ARGUMENT
                    .withDescription("title is required")
                    .asRuntimeException();
        }
        if (request.getSummary() == null || request.getSummary().isBlank()) {
            return Status.INVALID_ARGUMENT
                    .withDescription("summary is required")
                    .asRuntimeException();
        }
        if (request.getUntilMessageId() == null || request.getUntilMessageId().isBlank()) {
            return Status.INVALID_ARGUMENT
                    .withDescription("untilMessageId is required")
                    .asRuntimeException();
        }
        if (request.getSummarizedAt() == null || request.getSummarizedAt().isBlank()) {
            return Status.INVALID_ARGUMENT
                    .withDescription("summarizedAt is required")
                    .asRuntimeException();
        }
        return null;
    }
}
