package io.github.chirino.memory.grpc;

import io.github.chirino.memory.api.dto.PagedMessages;
import io.github.chirino.memory.api.dto.SyncResult;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.grpc.v1.AppendMessageRequest;
import io.github.chirino.memory.grpc.v1.ListMessagesRequest;
import io.github.chirino.memory.grpc.v1.ListMessagesResponse;
import io.github.chirino.memory.grpc.v1.Message;
import io.github.chirino.memory.grpc.v1.MessagesService;
import io.github.chirino.memory.grpc.v1.PageInfo;
import io.github.chirino.memory.grpc.v1.SyncMessagesRequest;
import io.github.chirino.memory.grpc.v1.SyncMessagesResponse;
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
public class MessagesGrpcService extends AbstractGrpcService implements MessagesService {

    @Override
    public Uni<ListMessagesResponse> listMessages(ListMessagesRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            if (request.getConversationId() == null
                                    || request.getConversationId().isBlank()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("conversationId is required")
                                        .asRuntimeException();
                            }
                            io.github.chirino.memory.model.MessageChannel requestedChannel =
                                    GrpcDtoMapper.fromProtoChannel(request.getChannel());
                            io.github.chirino.memory.model.MessageChannel channel =
                                    toEffectiveChannel(requestedChannel);
                            MemoryEpochFilter epochFilter = null;
                            if (channel == io.github.chirino.memory.model.MessageChannel.MEMORY) {
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
                            PagedMessages paged =
                                    store().getMessages(
                                                    currentUserId(),
                                                    request.getConversationId(),
                                                    token,
                                                    pageSize,
                                                    channel,
                                                    epochFilter,
                                                    currentClientId());
                            ListMessagesResponse.Builder builder =
                                    ListMessagesResponse.newBuilder();
                            if (paged != null) {
                                builder.addAllMessages(
                                        paged.getMessages().stream()
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
    public Uni<Message> appendMessage(AppendMessageRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            if (!hasValidApiKey()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Agent API key is required to append messages")
                                        .asRuntimeException();
                            }
                            if (request.getConversationId() == null
                                    || request.getConversationId().isBlank()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("conversationId is required")
                                        .asRuntimeException();
                            }
                            if (!request.hasMessage()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("message payload is required")
                                        .asRuntimeException();
                            }
                            String clientId = currentClientId();
                            if (clientId == null || clientId.isBlank()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription("Client id is required for agent messages")
                                        .asRuntimeException();
                            }
                            CreateMessageRequest internal = new CreateMessageRequest();
                            internal.setUserId(request.getMessage().getUserId());
                            io.github.chirino.memory.model.MessageChannel requestChannel =
                                    GrpcDtoMapper.fromProtoChannel(
                                            request.getMessage().getChannel());
                            internal.setChannel(
                                    GrpcDtoMapper.toCreateMessageChannel(requestChannel));
                            internal.setMemoryEpoch(request.getMessage().getMemoryEpoch());
                            internal.setContent(
                                    GrpcDtoMapper.fromValues(
                                            request.getMessage().getContentList()));
                            List<io.github.chirino.memory.api.dto.MessageDto> appended =
                                    store().appendAgentMessages(
                                                    currentUserId(),
                                                    request.getConversationId(),
                                                    List.of(internal),
                                                    clientId);
                            io.github.chirino.memory.api.dto.MessageDto latest =
                                    appended != null && !appended.isEmpty()
                                            ? appended.get(appended.size() - 1)
                                            : null;
                            return latest != null
                                    ? GrpcDtoMapper.toProto(latest)
                                    : Message.getDefaultInstance();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Uni<SyncMessagesResponse> syncMessages(SyncMessagesRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            if (!hasValidApiKey()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Agent API key is required to sync memory messages")
                                        .asRuntimeException();
                            }
                            if (request.getConversationId() == null
                                    || request.getConversationId().isBlank()) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("conversationId is required")
                                        .asRuntimeException();
                            }
                            if (request.getMessagesCount() == 0) {
                                throw Status.INVALID_ARGUMENT
                                        .withDescription("at least one message is required")
                                        .asRuntimeException();
                            }
                            String clientId = currentClientId();
                            if (clientId == null || clientId.isBlank()) {
                                throw Status.PERMISSION_DENIED
                                        .withDescription(
                                                "Client id is required to sync memory messages")
                                        .asRuntimeException();
                            }
                            List<CreateMessageRequest> internal =
                                    new ArrayList<>(request.getMessagesCount());
                            for (io.github.chirino.memory.grpc.v1.CreateMessageRequest message :
                                    request.getMessagesList()) {
                                if (message == null
                                        || message.getChannel()
                                                != io.github.chirino.memory.grpc.v1.MessageChannel
                                                        .MEMORY) {
                                    throw Status.INVALID_ARGUMENT
                                            .withDescription(
                                                    "all sync messages must target the memory"
                                                            + " channel")
                                            .asRuntimeException();
                                }
                                internal.add(toClientCreateMessage(message));
                            }
                            SyncResult result =
                                    store().syncAgentMessages(
                                                    currentUserId(),
                                                    request.getConversationId(),
                                                    internal,
                                                    clientId);
                            SyncMessagesResponse.Builder builder =
                                    SyncMessagesResponse.newBuilder()
                                            .setNoOp(result.isNoOp())
                                            .setEpochIncremented(result.isEpochIncremented());
                            if (result.getMemoryEpoch() != null) {
                                builder.setMemoryEpoch(result.getMemoryEpoch());
                            }
                            builder.addAllMessages(
                                    result.getMessages().stream()
                                            .map(GrpcDtoMapper::toProto)
                                            .collect(Collectors.toList()));
                            return builder.build();
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    private io.github.chirino.memory.model.MessageChannel toEffectiveChannel(
            io.github.chirino.memory.model.MessageChannel requested) {
        io.github.chirino.memory.model.MessageChannel safeChannel =
                requested != null
                        ? requested
                        : io.github.chirino.memory.model.MessageChannel.HISTORY;
        if (!hasValidApiKey()) {
            return io.github.chirino.memory.model.MessageChannel.HISTORY;
        }
        return safeChannel;
    }

    private CreateMessageRequest toClientCreateMessage(
            io.github.chirino.memory.grpc.v1.CreateMessageRequest request) {
        CreateMessageRequest internal = new CreateMessageRequest();
        String userId = request.getUserId();
        internal.setUserId(userId == null || userId.isBlank() ? null : userId);
        io.github.chirino.memory.model.MessageChannel requestChannel =
                GrpcDtoMapper.fromProtoChannel(request.getChannel());
        internal.setChannel(GrpcDtoMapper.toCreateMessageChannel(requestChannel));
        internal.setMemoryEpoch(request.getMemoryEpoch());
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
