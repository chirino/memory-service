package io.github.chirino.memory.grpc;

import static io.github.chirino.memory.grpc.UuidUtils.byteStringToString;

import io.github.chirino.memory.api.dto.PagedEntries;
import io.github.chirino.memory.api.dto.SyncResult;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.grpc.v1.AppendEntryRequest;
import io.github.chirino.memory.grpc.v1.EntriesService;
import io.github.chirino.memory.grpc.v1.Entry;
import io.github.chirino.memory.grpc.v1.ListEntriesRequest;
import io.github.chirino.memory.grpc.v1.ListEntriesResponse;
import io.github.chirino.memory.grpc.v1.PageInfo;
import io.github.chirino.memory.grpc.v1.SyncEntriesRequest;
import io.github.chirino.memory.grpc.v1.SyncEntriesResponse;
import io.github.chirino.memory.store.MemoryEpochFilter;
import io.grpc.Status;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import java.util.ArrayList;
import java.util.List;
import java.util.stream.Collectors;

@GrpcService
@Blocking
public class EntriesGrpcService extends AbstractGrpcService implements EntriesService {

    @Override
    public Uni<ListEntriesResponse> listEntries(ListEntriesRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String conversationId = byteStringToString(request.getConversationId());
                            if (conversationId == null || conversationId.isBlank()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("conversationId is required")
                                        .asRuntimeException();
                            }
                            io.github.chirino.memory.model.Channel requestedChannel =
                                    GrpcDtoMapper.fromProtoChannel(request.getChannel());
                            io.github.chirino.memory.model.Channel channel =
                                    toEffectiveChannel(requestedChannel);
                            MemoryEpochFilter epochFilter = null;
                            if (channel == io.github.chirino.memory.model.Channel.MEMORY) {
                                if (currentClientId() == null || currentClientId().isBlank()) {
                                    throw Status.PERMISSION_DENIED
                                            .withDescription(
                                                    "Client id is required for memory access")
                                            .asRuntimeException();
                                }
                                try {
                                    epochFilter = MemoryEpochFilter.parse(request.getEpochFilter());
                                } catch (IllegalArgumentException e) {
                                    throw Status.INVALID_ARGUMENT
                                            .withDescription(e.getMessage())
                                            .asRuntimeException();
                                }
                            }
                            String token =
                                    normalizeToken(
                                            request.hasPage()
                                                    ? request.getPage().getPageToken()
                                                    : null);
                            int pageSize =
                                    request.hasPage() && request.getPage().getPageSize() > 0
                                            ? request.getPage().getPageSize()
                                            : 50;
                            PagedEntries paged =
                                    store().getEntries(
                                                    currentUserId(),
                                                    conversationId,
                                                    token,
                                                    pageSize,
                                                    channel,
                                                    epochFilter,
                                                    currentClientId());
                            ListEntriesResponse.Builder builder = ListEntriesResponse.newBuilder();
                            if (paged != null) {
                                builder.addAllEntries(
                                        paged.getEntries().stream()
                                                .map(GrpcDtoMapper::toProto)
                                                .collect(Collectors.toList()));
                                String nextCursor = paged.getNextCursor();
                                if (nextCursor != null && !nextCursor.isBlank()) {
                                    builder.setPageInfo(
                                            PageInfo.newBuilder().setNextPageToken(nextCursor));
                                }
                            }
                            return builder.build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Entry> appendEntry(AppendEntryRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            if (!hasValidApiKey()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Agent API key is required to append entries")
                                        .asRuntimeException();
                            }
                            String conversationId = byteStringToString(request.getConversationId());
                            if (conversationId == null || conversationId.isBlank()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("conversationId is required")
                                        .asRuntimeException();
                            }
                            if (!request.hasEntry()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("entry payload is required")
                                        .asRuntimeException();
                            }
                            String clientId = currentClientId();
                            if (clientId == null || clientId.isBlank()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription("Client id is required for agent entries")
                                        .asRuntimeException();
                            }
                            CreateEntryRequest internal = new CreateEntryRequest();
                            internal.setUserId(request.getEntry().getUserId());
                            io.github.chirino.memory.model.Channel requestChannel =
                                    GrpcDtoMapper.fromProtoChannel(request.getEntry().getChannel());
                            internal.setChannel(GrpcDtoMapper.toCreateEntryChannel(requestChannel));
                            internal.setEpoch(request.getEntry().getEpoch());
                            internal.setContentType(request.getEntry().getContentType());
                            internal.setContent(
                                    GrpcDtoMapper.fromValues(request.getEntry().getContentList()));
                            List<io.github.chirino.memory.api.dto.EntryDto> appended =
                                    store().appendAgentEntries(
                                                    currentUserId(),
                                                    conversationId,
                                                    List.of(internal),
                                                    clientId);
                            io.github.chirino.memory.api.dto.EntryDto latest =
                                    appended != null && !appended.isEmpty()
                                            ? appended.get(appended.size() - 1)
                                            : null;
                            return latest != null
                                    ? GrpcDtoMapper.toProto(latest)
                                    : Entry.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<SyncEntriesResponse> syncEntries(SyncEntriesRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            if (!hasValidApiKey()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Agent API key is required to sync memory entries")
                                        .asRuntimeException();
                            }
                            String conversationId = byteStringToString(request.getConversationId());
                            if (conversationId == null || conversationId.isBlank()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("conversationId is required")
                                        .asRuntimeException();
                            }
                            if (request.getEntriesCount() == 0) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("at least one entry is required")
                                        .asRuntimeException();
                            }
                            String clientId = currentClientId();
                            if (clientId == null || clientId.isBlank()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Client id is required to sync memory entries")
                                        .asRuntimeException();
                            }
                            List<CreateEntryRequest> internal =
                                    new ArrayList<>(request.getEntriesCount());
                            for (io.github.chirino.memory.grpc.v1.CreateEntryRequest entry :
                                    request.getEntriesList()) {
                                if (entry == null
                                        || entry.getChannel()
                                                != io.github.chirino.memory.grpc.v1.Channel
                                                        .MEMORY) {
                                    throw Status.INVALID_ARGUMENT
                                            .withDescription(
                                                    "all sync entries must target the memory"
                                                            + " channel")
                                            .asRuntimeException();
                                }
                                internal.add(toClientCreateEntry(entry));
                            }
                            SyncResult result =
                                    store().syncAgentEntries(
                                                    currentUserId(),
                                                    conversationId,
                                                    internal,
                                                    clientId);
                            SyncEntriesResponse.Builder builder =
                                    SyncEntriesResponse.newBuilder()
                                            .setNoOp(result.isNoOp())
                                            .setEpochIncremented(result.isEpochIncremented());
                            if (result.getEpoch() != null) {
                                builder.setEpoch(result.getEpoch());
                            }
                            builder.addAllEntries(
                                    result.getEntries().stream()
                                            .map(GrpcDtoMapper::toProto)
                                            .collect(Collectors.toList()));
                            return builder.build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    private io.github.chirino.memory.model.Channel toEffectiveChannel(
            io.github.chirino.memory.model.Channel requested) {
        io.github.chirino.memory.model.Channel safeChannel =
                requested != null ? requested : io.github.chirino.memory.model.Channel.HISTORY;
        if (!hasValidApiKey()) {
            return io.github.chirino.memory.model.Channel.HISTORY;
        }
        return safeChannel;
    }

    private CreateEntryRequest toClientCreateEntry(
            io.github.chirino.memory.grpc.v1.CreateEntryRequest request) {
        CreateEntryRequest internal = new CreateEntryRequest();
        String userId = request.getUserId();
        internal.setUserId(userId == null || userId.isBlank() ? null : userId);
        io.github.chirino.memory.model.Channel requestChannel =
                GrpcDtoMapper.fromProtoChannel(request.getChannel());
        internal.setChannel(GrpcDtoMapper.toCreateEntryChannel(requestChannel));
        internal.setEpoch(request.getEpoch());
        internal.setContentType(request.getContentType());
        internal.setContent(GrpcDtoMapper.fromValues(request.getContentList()));
        return internal;
    }

    private static String normalizeToken(String token) {
        if (token == null || token.isBlank()) {
            return null;
        }
        return token;
    }
}
