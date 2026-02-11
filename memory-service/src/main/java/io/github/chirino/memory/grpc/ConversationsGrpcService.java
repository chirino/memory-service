package io.github.chirino.memory.grpc;

import static io.github.chirino.memory.grpc.UuidUtils.byteStringToString;

import com.google.protobuf.Empty;
import io.github.chirino.memory.api.ConversationListMode;
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.ConversationForkSummaryDto;
import io.github.chirino.memory.api.dto.ConversationSummaryDto;
import io.github.chirino.memory.grpc.v1.Conversation;
import io.github.chirino.memory.grpc.v1.ConversationsService;
import io.github.chirino.memory.grpc.v1.CreateConversationRequest;
import io.github.chirino.memory.grpc.v1.DeleteConversationRequest;
import io.github.chirino.memory.grpc.v1.GetConversationRequest;
import io.github.chirino.memory.grpc.v1.ListConversationsRequest;
import io.github.chirino.memory.grpc.v1.ListConversationsResponse;
import io.github.chirino.memory.grpc.v1.ListForksRequest;
import io.github.chirino.memory.grpc.v1.ListForksResponse;
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
                            String conversationId = byteStringToString(request.getConversationId());
                            ConversationDto dto =
                                    store().getConversation(currentUserId(), conversationId);
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
                            String conversationId = byteStringToString(request.getConversationId());
                            store().deleteConversation(currentUserId(), conversationId);
                            return Empty.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<ListForksResponse> listForks(ListForksRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String conversationId = byteStringToString(request.getConversationId());
                            List<ConversationForkSummaryDto> forks =
                                    store().listForks(currentUserId(), conversationId);
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

    private static String normalizeToken(String token) {
        if (token == null || token.isBlank()) {
            return null;
        }
        return token;
    }

    private static ConversationListMode toConversationListMode(
            io.github.chirino.memory.grpc.v1.ConversationListMode mode) {
        if (mode == null) {
            return ConversationListMode.LATEST_FORK;
        }
        return switch (mode) {
            case ROOTS -> ConversationListMode.ROOTS;
            case ALL -> ConversationListMode.ALL;
            default -> ConversationListMode.LATEST_FORK;
        };
    }
}
