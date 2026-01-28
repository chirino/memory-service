package io.github.chirino.memory.grpc;

import com.google.protobuf.Empty;
import io.github.chirino.memory.api.dto.ConversationMembershipDto;
import io.github.chirino.memory.api.dto.ShareConversationRequest;
import io.github.chirino.memory.grpc.v1.ConversationMembership;
import io.github.chirino.memory.grpc.v1.ConversationMembershipsService;
import io.github.chirino.memory.grpc.v1.DeleteMembershipRequest;
import io.github.chirino.memory.grpc.v1.ListMembershipsRequest;
import io.github.chirino.memory.grpc.v1.ListMembershipsResponse;
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
                            List<ConversationMembershipDto> internal =
                                    store().listMemberships(
                                                    currentUserId(), request.getConversationId());
                            return ListMembershipsResponse.newBuilder()
                                    .addAllMemberships(
                                            internal.stream()
                                                    .map(
                                                            dto ->
                                                                    GrpcDtoMapper.toProto(
                                                                            dto,
                                                                            request
                                                                                    .getConversationId()))
                                                    .collect(Collectors.toList()))
                                    .build();
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
                            ShareConversationRequest internal = new ShareConversationRequest();
                            internal.setUserId(request.getUserId());
                            internal.setAccessLevel(
                                    GrpcDtoMapper.accessLevelFromProto(request.getAccessLevel()));
                            ConversationMembershipDto dto =
                                    store().shareConversation(
                                                    currentUserId(),
                                                    request.getConversationId(),
                                                    internal);
                            return GrpcDtoMapper.toProto(dto, request.getConversationId());
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<ConversationMembership> updateMembership(UpdateMembershipRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            ShareConversationRequest internal = new ShareConversationRequest();
                            internal.setUserId(request.getMemberUserId());
                            internal.setAccessLevel(
                                    GrpcDtoMapper.accessLevelFromProto(request.getAccessLevel()));
                            ConversationMembershipDto dto =
                                    store().updateMembership(
                                                    currentUserId(),
                                                    request.getConversationId(),
                                                    request.getMemberUserId(),
                                                    internal);
                            return GrpcDtoMapper.toProto(dto, request.getConversationId());
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Empty> deleteMembership(DeleteMembershipRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            store().deleteMembership(
                                            currentUserId(),
                                            request.getConversationId(),
                                            request.getMemberUserId());
                            return Empty.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }
}
