package io.github.chirino.memory.grpc;

import static io.github.chirino.memory.grpc.UuidUtils.byteStringToString;

import com.google.protobuf.Empty;
import io.github.chirino.memory.api.dto.CreateOwnershipTransferRequest;
import io.github.chirino.memory.api.dto.OwnershipTransferDto;
import io.github.chirino.memory.grpc.v1.AcceptOwnershipTransferRequest;
import io.github.chirino.memory.grpc.v1.DeleteOwnershipTransferRequest;
import io.github.chirino.memory.grpc.v1.GetOwnershipTransferRequest;
import io.github.chirino.memory.grpc.v1.ListOwnershipTransfersRequest;
import io.github.chirino.memory.grpc.v1.ListOwnershipTransfersResponse;
import io.github.chirino.memory.grpc.v1.OwnershipTransfer;
import io.github.chirino.memory.grpc.v1.OwnershipTransfersService;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
import java.util.List;
import java.util.Optional;
import java.util.stream.Collectors;

@GrpcService
@Blocking
public class OwnershipTransfersGrpcService extends AbstractGrpcService
        implements OwnershipTransfersService {

    @Override
    public Uni<ListOwnershipTransfersResponse> listOwnershipTransfers(
            ListOwnershipTransfersRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String role = GrpcDtoMapper.transferRoleFromProto(request.getRole());
                            List<OwnershipTransferDto> transfers =
                                    store().listPendingTransfers(currentUserId(), role);
                            return ListOwnershipTransfersResponse.newBuilder()
                                    .addAllTransfers(
                                            transfers.stream()
                                                    .map(GrpcDtoMapper::toProto)
                                                    .collect(Collectors.toList()))
                                    .build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<OwnershipTransfer> getOwnershipTransfer(GetOwnershipTransferRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String transferId = byteStringToString(request.getTransferId());
                            Optional<OwnershipTransferDto> transfer =
                                    store().getTransfer(currentUserId(), transferId);
                            if (transfer.isEmpty()) {
                                throw new ResourceNotFoundException("transfer", transferId);
                            }
                            return GrpcDtoMapper.toProto(transfer.get());
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<OwnershipTransfer> createOwnershipTransfer(
            io.github.chirino.memory.grpc.v1.CreateOwnershipTransferRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            CreateOwnershipTransferRequest internal =
                                    new CreateOwnershipTransferRequest();
                            internal.setConversationId(
                                    byteStringToString(request.getConversationId()));
                            internal.setNewOwnerUserId(request.getNewOwnerUserId());
                            OwnershipTransferDto transfer =
                                    store().createOwnershipTransfer(currentUserId(), internal);
                            return GrpcDtoMapper.toProto(transfer);
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Empty> acceptOwnershipTransfer(AcceptOwnershipTransferRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String transferId = byteStringToString(request.getTransferId());
                            store().acceptTransfer(currentUserId(), transferId);
                            return Empty.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<Empty> deleteOwnershipTransfer(DeleteOwnershipTransferRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String transferId = byteStringToString(request.getTransferId());
                            store().deleteTransfer(currentUserId(), transferId);
                            return Empty.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }
}
