package io.github.chirino.memory.grpc;

import static io.github.chirino.memory.grpc.UuidUtils.byteStringToString;
import static io.github.chirino.memory.grpc.UuidUtils.stringToByteString;

import io.github.chirino.memory.api.dto.IndexConversationsResponse;
import io.github.chirino.memory.api.dto.IndexEntryRequest;
import io.github.chirino.memory.api.dto.SearchResultsDto;
import io.github.chirino.memory.api.dto.UnindexedEntriesResponse;
import io.github.chirino.memory.config.SearchStoreSelector;
import io.github.chirino.memory.grpc.v1.IndexConversationsRequest;
import io.github.chirino.memory.grpc.v1.ListUnindexedEntriesRequest;
import io.github.chirino.memory.grpc.v1.ListUnindexedEntriesResponse;
import io.github.chirino.memory.grpc.v1.SearchEntriesRequest;
import io.github.chirino.memory.grpc.v1.SearchEntriesResponse;
import io.github.chirino.memory.grpc.v1.SearchService;
import io.github.chirino.memory.grpc.v1.UnindexedEntry;
import io.github.chirino.memory.security.AdminRoleResolver;
import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.vector.SearchStore;
import io.grpc.Status;
import io.quarkus.grpc.GrpcService;
import io.quarkus.security.identity.SecurityIdentity;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import java.util.List;
import java.util.stream.Collectors;

@GrpcService
@Blocking
public class SearchGrpcService extends AbstractGrpcService implements SearchService {

    private final SearchStoreSelector searchStoreSelector;

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

    @Inject AdminRoleResolver roleResolver;

    public SearchGrpcService(SearchStoreSelector searchStoreSelector) {
        this.searchStoreSelector = searchStoreSelector;
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
                            SearchStore searchStore = searchStoreSelector.getSearchStore();
                            if (searchStore == null || !searchStore.isEnabled()) {
                                throw Status.UNIMPLEMENTED
                                        .withDescription("Search store is not available")
                                        .asRuntimeException();
                            }
                            io.github.chirino.memory.api.dto.SearchEntriesRequest internal =
                                    new io.github.chirino.memory.api.dto.SearchEntriesRequest();
                            internal.setQuery(request.getQuery());
                            internal.setLimit(request.getLimit() > 0 ? request.getLimit() : 20);
                            if (request.getAfter() != null && !request.getAfter().isBlank()) {
                                internal.setAfter(request.getAfter());
                            }
                            boolean includeEntry =
                                    !request.hasIncludeEntry() || request.getIncludeEntry();
                            internal.setIncludeEntry(includeEntry);

                            SearchResultsDto internalResults =
                                    searchStore.search(currentUserId(), internal);

                            SearchEntriesResponse.Builder responseBuilder =
                                    SearchEntriesResponse.newBuilder()
                                            .addAllResults(
                                                    internalResults.getResults().stream()
                                                            .map(
                                                                    r ->
                                                                            GrpcDtoMapper.toProto(
                                                                                    r,
                                                                                    includeEntry))
                                                            .collect(Collectors.toList()));
                            if (internalResults.getNextCursor() != null) {
                                responseBuilder.setNextCursor(internalResults.getNextCursor());
                            }
                            return responseBuilder.build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<io.github.chirino.memory.grpc.v1.IndexConversationsResponse> indexConversations(
            IndexConversationsRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            try {
                                roleResolver.requireIndexer(identity, apiKeyContext);
                            } catch (AccessDeniedException e) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(e.getMessage())
                                        .asRuntimeException();
                            }

                            if (request.getEntriesCount() == 0) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("At least one entry is required")
                                        .asRuntimeException();
                            }

                            // Validate each entry
                            for (int i = 0; i < request.getEntriesCount(); i++) {
                                io.github.chirino.memory.grpc.v1.IndexEntryRequest entry =
                                        request.getEntries(i);
                                String conversationId =
                                        byteStringToString(entry.getConversationId());
                                if (conversationId == null || conversationId.isBlank()) {
                                    throw Status.INVALID_ARGUMENT
                                            .withDescription(
                                                    "conversationId is required for entry " + i)
                                            .asRuntimeException();
                                }
                                String entryId = byteStringToString(entry.getEntryId());
                                if (entryId == null || entryId.isBlank()) {
                                    throw Status.INVALID_ARGUMENT
                                            .withDescription("entryId is required for entry " + i)
                                            .asRuntimeException();
                                }
                                if (entry.getIndexedContent() == null
                                        || entry.getIndexedContent().isBlank()) {
                                    throw Status.INVALID_ARGUMENT
                                            .withDescription(
                                                    "indexedContent is required for entry " + i)
                                            .asRuntimeException();
                                }
                            }

                            // Convert to internal DTO
                            List<IndexEntryRequest> internalEntries =
                                    request.getEntriesList().stream()
                                            .map(
                                                    e -> {
                                                        IndexEntryRequest internal =
                                                                new IndexEntryRequest();
                                                        internal.setConversationId(
                                                                byteStringToString(
                                                                        e.getConversationId()));
                                                        internal.setEntryId(
                                                                byteStringToString(e.getEntryId()));
                                                        internal.setIndexedContent(
                                                                e.getIndexedContent());
                                                        return internal;
                                                    })
                                            .toList();

                            IndexConversationsResponse dto = store().indexEntries(internalEntries);

                            return io.github.chirino.memory.grpc.v1.IndexConversationsResponse
                                    .newBuilder()
                                    .setIndexed(dto.getIndexed())
                                    .build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<ListUnindexedEntriesResponse> listUnindexedEntries(
            ListUnindexedEntriesRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            try {
                                roleResolver.requireIndexer(identity, apiKeyContext);
                            } catch (AccessDeniedException e) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(e.getMessage())
                                        .asRuntimeException();
                            }

                            int limit = request.getLimit() > 0 ? request.getLimit() : 100;
                            String cursor =
                                    request.hasCursor() && !request.getCursor().isBlank()
                                            ? request.getCursor()
                                            : null;

                            UnindexedEntriesResponse dto =
                                    store().listUnindexedEntries(limit, cursor);

                            ListUnindexedEntriesResponse.Builder builder =
                                    ListUnindexedEntriesResponse.newBuilder();

                            if (dto.getData() != null) {
                                builder.addAllEntries(
                                        dto.getData().stream()
                                                .map(
                                                        e ->
                                                                UnindexedEntry.newBuilder()
                                                                        .setConversationId(
                                                                                stringToByteString(
                                                                                        e
                                                                                                .getConversationId()))
                                                                        .setEntry(
                                                                                GrpcDtoMapper
                                                                                        .toProto(
                                                                                                e
                                                                                                        .getEntry()))
                                                                        .build())
                                                .collect(Collectors.toList()));
                            }

                            if (dto.getCursor() != null) {
                                builder.setCursor(dto.getCursor());
                            }

                            return builder.build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }
}
