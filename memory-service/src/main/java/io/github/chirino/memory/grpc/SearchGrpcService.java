package io.github.chirino.memory.grpc;

import static io.github.chirino.memory.grpc.UuidUtils.byteStringToString;

import io.github.chirino.memory.api.dto.SearchResultDto;
import io.github.chirino.memory.config.VectorStoreSelector;
import io.github.chirino.memory.grpc.v1.Entry;
import io.github.chirino.memory.grpc.v1.IndexTranscriptRequest;
import io.github.chirino.memory.grpc.v1.SearchEntriesRequest;
import io.github.chirino.memory.grpc.v1.SearchEntriesResponse;
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
    public Uni<SearchEntriesResponse> searchConversations(SearchEntriesRequest request) {
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
                            io.github.chirino.memory.api.dto.SearchEntriesRequest internal =
                                    new io.github.chirino.memory.api.dto.SearchEntriesRequest();
                            internal.setQuery(request.getQuery());
                            internal.setTopK(request.getTopK() > 0 ? request.getTopK() : 20);
                            if (!request.getConversationIdsList().isEmpty()) {
                                List<String> conversationIds =
                                        request.getConversationIdsList().stream()
                                                .map(UuidUtils::byteStringToString)
                                                .collect(Collectors.toList());
                                internal.setConversationIds(new ArrayList<>(conversationIds));
                            }
                            String before = byteStringToString(request.getBefore());
                            if (before != null && !before.isBlank()) {
                                internal.setBefore(before);
                            }
                            List<SearchResultDto> internalResults =
                                    vectorStore.search(currentUserId(), internal);
                            return SearchEntriesResponse.newBuilder()
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
    public Uni<Entry> indexTranscript(IndexTranscriptRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            if (!hasValidApiKey()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Agent API key is required to index transcripts")
                                        .asRuntimeException();
                            }
                            StatusRuntimeException validation = validateIndexRequest(request);
                            if (validation != null) {
                                throw validation;
                            }
                            String clientId = currentClientId();
                            if (clientId == null || clientId.isBlank()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Client id is required to index transcripts")
                                        .asRuntimeException();
                            }
                            String conversationId = byteStringToString(request.getConversationId());
                            String untilEntryId = byteStringToString(request.getUntilEntryId());
                            io.github.chirino.memory.api.dto.IndexTranscriptRequest internal =
                                    new io.github.chirino.memory.api.dto.IndexTranscriptRequest();
                            internal.setConversationId(conversationId);
                            internal.setTitle(request.hasTitle() ? request.getTitle() : null);
                            internal.setTranscript(request.getTranscript());
                            internal.setUntilEntryId(untilEntryId);
                            io.github.chirino.memory.api.dto.EntryDto dto =
                                    store().indexTranscript(internal, clientId);
                            return GrpcDtoMapper.toProto(dto);
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    private StatusRuntimeException validateIndexRequest(IndexTranscriptRequest request) {
        String conversationId = byteStringToString(request.getConversationId());
        if (conversationId == null || conversationId.isBlank()) {
            return Status.INVALID_ARGUMENT
                    .withDescription("conversationId is required")
                    .asRuntimeException();
        }
        if (request.getTranscript() == null || request.getTranscript().isBlank()) {
            return Status.INVALID_ARGUMENT
                    .withDescription("transcript is required")
                    .asRuntimeException();
        }
        String untilEntryId = byteStringToString(request.getUntilEntryId());
        if (untilEntryId == null || untilEntryId.isBlank()) {
            return Status.INVALID_ARGUMENT
                    .withDescription("untilEntryId is required")
                    .asRuntimeException();
        }
        return null;
    }
}
