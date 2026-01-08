package io.github.chirino.memory.grpc;

import io.github.chirino.memory.api.dto.PagedMessages;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.grpc.v1.AppendMessageRequest;
import io.github.chirino.memory.grpc.v1.ListMessagesRequest;
import io.github.chirino.memory.grpc.v1.ListMessagesResponse;
import io.github.chirino.memory.grpc.v1.Message;
import io.github.chirino.memory.grpc.v1.MessagesService;
import io.github.chirino.memory.grpc.v1.PageInfo;
import io.grpc.Status;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Uni;
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
                                                    channel);
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
                                                    List.of(internal));
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

    private static String normalizeToken(String token) {
        if (token == null || token.isBlank()) {
            return null;
        }
        return token;
    }
}
