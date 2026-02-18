package io.github.chirino.memory.grpc;

import static io.github.chirino.memory.grpc.UuidUtils.byteStringToString;

import com.google.protobuf.Empty;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ShareConversationRequest;
import io.github.chirino.memory.grpc.v1.ConversationMembership;
import io.github.chirino.memory.grpc.v1.ConversationMembershipsService;
import io.github.chirino.memory.grpc.v1.DeleteMembershipRequest;
import io.github.chirino.memory.grpc.v1.ListMembershipsRequest;
import io.github.chirino.memory.grpc.v1.ListMembershipsResponse;
import io.github.chirino.memory.grpc.v1.PageInfo;
import io.github.chirino.memory.grpc.v1.UpdateMembershipRequest;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import java.util.List;
import java.util.stream.Collectors;

@GrpcService
@Blocking
public class ConversationMembershipsGrpcService extends AbstractGrpcService
        implements ConversationMembershipsService {

    @Override
    public Uni<ListMembershipsResponse> listMemberships(ListMembershipsRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String conversationId = byteStringToString(request.getConversationId());
                            String token =
                                    request.hasPage()
                                            ? normalizeToken(request.getPage().getPageToken())
                                            : null;
                            int pageSize =
                                    request.hasPage() && request.getPage().getPageSize() > 0
                                            ? request.getPage().getPageSize()
                                            : 50;
                            List<ConversationMembershipDto> internal =
                                    store().listMemberships(
                                                    currentUserId(),
                                                    conversationId,
                                                    token,
                                                    pageSize);
                            ListMembershipsResponse.Builder builder =
                                    ListMembershipsResponse.newBuilder()
                                            .addAllMemberships(
                                                    internal.stream()
                                                            .map(
                                                                    dto ->
                                                                            GrpcDtoMapper.toProto(
                                                                                    dto,
                                                                                    conversationId))
                                                            .collect(Collectors.toList()));
                            if (internal.size() == pageSize && !internal.isEmpty()) {
                                builder.setPageInfo(
                                        PageInfo.newBuilder()
                                                .setNextPageToken(
                                                        internal.get(internal.size() - 1)
                                                                .getUserId()));
                            }
                            return builder.build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<ConversationMembership> shareConversation(
            io.github.chirino.memory.grpc.v1.ShareConversationRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String conversationId = byteStringToString(request.getConversationId());
                            ShareConversationRequest internal = new ShareConversationRequest();
                            internal.setUserId(request.getUserId());
                            internal.setAccessLevel(
                                    GrpcDtoMapper.accessLevelFromProto(request.getAccessLevel()));
                            ConversationMembershipDto dto =
                                    store().shareConversation(
                                                    currentUserId(), conversationId, internal);
                            return GrpcDtoMapper.toProto(dto, conversationId);
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<ConversationMembership> updateMembership(UpdateMembershipRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String conversationId = byteStringToString(request.getConversationId());
                            ShareConversationRequest internal = new ShareConversationRequest();
                            internal.setUserId(request.getMemberUserId());
                            internal.setAccessLevel(
                                    GrpcDtoMapper.accessLevelFromProto(request.getAccessLevel()));
                            ConversationMembershipDto dto =
                                    store().updateMembership(
                                                    currentUserId(),
                                                    conversationId,
                                                    request.getMemberUserId(),
                                                    internal);
                            return GrpcDtoMapper.toProto(dto, conversationId);
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

    @Override
    public Uni<Empty> deleteMembership(DeleteMembershipRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String conversationId = byteStringToString(request.getConversationId());
                            store().deleteMembership(
                                            currentUserId(),
                                            conversationId,
                                            request.getMemberUserId());
                            return Empty.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }
}
