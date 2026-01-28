package io.github.chirino.memory.grpc;

import com.google.protobuf.Empty;
import io.github.chirino.memory.api.ConversationListMode;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.api.dto.ForkFromEntryRequest;
import io.github.chirino.memory.grpc.v1.Conversation;
import io.github.chirino.memory.grpc.v1.ConversationsService;
import io.github.chirino.memory.grpc.v1.CreateConversationRequest;
import io.github.chirino.memory.grpc.v1.DeleteConversationRequest;
import io.github.chirino.memory.grpc.v1.ForkConversationRequest;
import io.github.chirino.memory.grpc.v1.GetConversationRequest;
import io.github.chirino.memory.grpc.v1.ListConversationsRequest;
import io.github.chirino.memory.grpc.v1.ListConversationsResponse;
import io.github.chirino.memory.grpc.v1.ListForksRequest;
import io.github.chirino.memory.grpc.v1.ListForksResponse;
import io.github.chirino.memory.grpc.v1.TransferOwnershipRequest;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

@GrpcService
@Blocking
public class ConversationsGrpcService extends AbstractGrpcService implements ConversationsService {

    @Override
    public Uni<ListConversationsResponse> listConversations(ListConversationsRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String query = request.getQuery();
                            String token =
                                    normalizeToken(
                                            request.hasPage()
                                                    ? request.getPage().getPageToken()
                                                    : null);
                            int pageSize =
                                    request.hasPage() && request.getPage().getPageSize() > 0
                                            ? request.getPage().getPageSize()
                                            : 20;
                            ConversationListMode mode = toConversationListMode(request.getMode());
                            List<ConversationSummaryDto> internal =
                                    store().listConversations(
                                                    currentUserId(), query, token, pageSize, mode);
                            return ListConversationsResponse.newBuilder()
                                    .addAllConversations(
                                            internal.stream()
                                                    .map(GrpcDtoMapper::toProto)
                                                    .collect(Collectors.toList()))
                                    .build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Conversation> createConversation(CreateConversationRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            io.github.chirino.memory.api.dto.CreateConversationRequest internal =
                                    new io.github.chirino.memory.api.dto
                                            .CreateConversationRequest();
                            internal.setTitle(request.getTitle());
                            Map<String, Object> metadata =
                                    GrpcDtoMapper.structToMap(request.getMetadata());
                            if (metadata != null) {
                                internal.setMetadata(metadata);
                            }
                            ConversationDto created =
                                    store().createConversation(currentUserId(), internal);
                            return GrpcDtoMapper.toProto(created);
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Conversation> getConversation(GetConversationRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            ConversationDto dto =
                                    store().getConversation(
                                                    currentUserId(), request.getConversationId());
                            return GrpcDtoMapper.toProto(dto);
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Empty> deleteConversation(DeleteConversationRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            store().deleteConversation(
                                            currentUserId(), request.getConversationId());
                            return Empty.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Conversation> forkConversation(ForkConversationRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            ForkFromEntryRequest internal = new ForkFromEntryRequest();
                            if (request.getTitle() != null) {
                                internal.setTitle(request.getTitle());
                            }
                            ConversationDto forked =
                                    store().forkConversationAtEntry(
                                                    currentUserId(),
                                                    request.getConversationId(),
                                                    request.getEntryId(),
                                                    internal);
                            return GrpcDtoMapper.toProto(forked);
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<ListForksResponse> listForks(ListForksRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            List<ConversationForkSummaryDto> forks =
                                    store().listForks(currentUserId(), request.getConversationId());
                            return ListForksResponse.newBuilder()
                                    .addAllForks(
                                            forks.stream()
                                                    .map(GrpcDtoMapper::toProto)
                                                    .collect(Collectors.toList()))
                                    .build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Empty> transferOwnership(TransferOwnershipRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            store().requestOwnershipTransfer(
                                            currentUserId(),
                                            request.getConversationId(),
                                            request.getNewOwnerUserId());
                            return Empty.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    private static String normalizeToken(String token) {
        if (token == null || token.isBlank()) {
            return null;
        }
        return token;
    }

    private static ConversationListMode toConversationListMode(
            io.github.chirino.memory.grpc.v1.ConversationListMode mode) {
        if (mode == null) {
            return ConversationListMode.ALL;
        }
        return switch (mode) {
            case ROOTS -> ConversationListMode.ROOTS;
            case LATEST_FORK -> ConversationListMode.LATEST_FORK;
            default -> ConversationListMode.ALL;
        };
    }
}
